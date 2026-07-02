<?php
// test_transport.php — two PHP nodes federate over REAL HTTP: each runs the
// front controller (php/public/index.php); node A drives everything through
// poll.php (drain queue -> federate). Proves the pull/push transport carries a
// contact handshake and a transfer to convergence across process boundaries,
// with on-demand identity fetch supplying the peer keys.
//
//   php php/test_transport.php

declare(strict_types=1);

require __DIR__ . '/lp_node.php';

use lp\Node;
use lp\Store;
use lp\Ledger;

$pass = 0;
$fail = 0;
$fails = [];
function ok(bool $c, string $what)
{
    global $pass, $fail, $fails;
    if ($c) {
        $pass++;
    } else {
        $fail++;
        $fails[] = $what;
        fwrite(STDERR, "  FAIL: $what\n");
    }
}

$dir = sys_get_temp_dir() . '/lptx-' . getmypid();
@mkdir($dir, 0700, true);
$portA = 8500 + (getmypid() % 200);
$portB = $portA + 1;
$baseA = "http://127.0.0.1:$portA";
$baseB = "http://127.0.0.1:$portB";
$stateA = "$dir/a.json";
$stateB = "$dir/b.json";
$queueA = "$dir/a-queue.jsonl";

function nodeCfg(string $base, string $state, string $queue, string $sym, array $peers): array
{
    return [
        'base' => $base, 'state_file' => $state, 'queue_file' => $queue, 'name' => $sym,
        'currency_name' => $sym, 'currency_symbol' => $sym, 'transparency' => 'pseudonymous',
        'c_period_ppm' => 274, 'ud_period' => 'P1D', 'genesis_ud' => 1000000,
        'auto_accept_seed' => 500000000, 'peers' => $peers,
    ];
}

// Seed both node states (key + a funded member) before booting servers.
$A = new Node(new Store($stateA), nodeCfg($baseA, $stateA, $queueA, 'R', [$baseB]));
$A->initKey();
$A->addMember('alice', 'Alice', 1000000, 100000000);
$B = new Node(new Store($stateB), nodeCfg($baseB, $stateB, "$dir/b-queue.jsonl", 'H', [$baseA]));
$B->initKey();
$B->addMember('bob', 'Bob', 1000000, 100000000);

// Boot both front-controller servers.
function boot(string $base, string $state, int $port, string $dir): int
{
    $env = "LP_BASE=$base LP_STATE_FILE=$state LP_QUEUE_FILE=$dir/x-queue.jsonl";
    $cmd = "$env php -S 127.0.0.1:$port " . escapeshellarg(dirname(__DIR__) . '/php/public/index.php') . " >/dev/null 2>&1 & echo \$!";
    return (int) trim(shell_exec($cmd));
}
$pidA = boot($baseA, $stateA, $portA, $dir);
$pidB = boot($baseB, $stateB, $portB, $dir);
foreach ([$portA, $portB] as $p) {
    for ($i = 0; $i < 60; $i++) {
        $fp = @fsockopen('127.0.0.1', $p, $e, $s, 0.1);
        if ($fp) {
            fclose($fp);
            break;
        }
        usleep(50000);
    }
}

// poll.php for node A, with A's config injected via env.
function pollA(string $baseA, string $stateA, string $queueA, string $baseB, array $extra = []): void
{
    $env = "LP_BASE=$baseA LP_STATE_FILE=$stateA LP_QUEUE_FILE=$queueA LP_PEERS=$baseB "
        . "LP_AUTO_ACCEPT_SEED=500000000 LP_SYMBOL=R LP_CURRENCY=R";
    $args = implode(' ', $extra);
    shell_exec("$env php " . escapeshellarg(dirname(__DIR__) . '/php/poll.php') . " $args 2>/dev/null");
}

function contactOf(string $statePath, string $peerHost): ?array
{
    $snap = json_decode((string) file_get_contents($statePath), true);
    foreach (($snap['contacts'] ?? []) as $c) {
        if (($c['PeerHost'] ?? '') === $peerHost) {
            return $c;
        }
    }
    return null;
}

try {
    // ── open a contact via the queue + poll ──────────────────────────────────
    file_put_contents($queueA, json_encode(['action' => 'open_contact', 'peer_base' => $baseB, 'my_seed_micro' => 500000000]) . "\n");
    pollA($baseA, $stateA, $queueA, $baseB);

    $ca = contactOf($stateA, '127.0.0.1'); // peer host is 127.0.0.1 (both on loopback)
    // Distinguish the two loopback peers by port in PeerBase instead.
    $ca = null;
    foreach ((json_decode((string) file_get_contents($stateA), true)['contacts'] ?? []) as $c) {
        if (($c['PeerBase'] ?? '') === $baseB) {
            $ca = $c;
        }
    }
    $cb = null;
    foreach ((json_decode((string) file_get_contents($stateB), true)['contacts'] ?? []) as $c) {
        if (($c['PeerBase'] ?? '') === $baseA) {
            $cb = $c;
        }
    }
    ok($ca && !empty($ca['Active']), 'HTTP: node A contact active after poll');
    ok($cb && !empty($cb['Active']), 'HTTP: node B contact active (processed pushed propose)');
    ok($ca && $cb && ($ca['Roots'][(int) $ca['OpSeq']] ?? 'a') === ($cb['Roots'][(int) $cb['OpSeq']] ?? 'b'),
        'HTTP: channel roots agree across the wire');
    ok($ca && (int) $ca['MyReserveOfPeer'] === 500000000, 'HTTP: reserve seeded on proposer');

    // ── transfer alice -> bob via the queue + poll (multi-round exchange) ─────
    file_put_contents($queueA, json_encode(['action' => 'send_transfer', 'from_member' => 'alice',
        'to' => 'bob@127.0.0.1', 'src_amount_micro' => 10000000]) . "\n");
    pollA($baseA, $stateA, $queueA, $baseB);

    $ledA = Ledger::from_records(json_decode((string) file_get_contents($stateA), true)['ledger']);
    $ledB = Ledger::from_records(json_decode((string) file_get_contents($stateB), true)['ledger']);
    $dst = (int) floor(500000000 * 10000000 / 510000000);
    ok($ledA->balance('m:alice') === 90000000, 'HTTP: payer debited 10.00 across the wire');
    ok($ledB->balance('m:bob') === 100000000 + $dst, 'HTTP: payee credited the priced amount');
} finally {
    @exec("kill $pidA $pidB 2>/dev/null");
    array_map('unlink', glob("$dir/*") ?: []);
    @rmdir($dir);
}

$total = $pass + $fail;
echo "\nPHP HTTP federation: $pass/$total passed\n";
if ($fail) {
    echo "FAILURES:\n  - " . implode("\n  - ", $fails) . "\n";
    exit(1);
}
echo "OK — two nodes complete a contact handshake over real HTTP (pull + push + identity fetch).\n";
exit(0);
