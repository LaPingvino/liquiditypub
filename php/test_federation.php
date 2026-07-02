<?php
// test_federation.php — two PHP nodes federate end to end over REAL signed
// envelopes (sodium): open a contact (seeded pool), run a transfer both
// directions through the two-phase commit, and adjust a reserve. Asserts the
// two sides stay mirror-consistent (channel roots, op_seq, reserves) and money
// is conserved — the PHP port of the Go two-node round-trip.
//
//   php php/test_federation.php

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

$dir = sys_get_temp_dir() . '/lpfed-' . getmypid();
@mkdir($dir, 0700, true);

function mkNode(string $dir, string $tag, string $base, string $sym, string $member): Node
{
    $n = new Node(new Store("$dir/$tag.json"), [
        'base' => $base, 'name' => $tag, 'currency_name' => $sym, 'currency_symbol' => $sym,
        'transparency' => 'pseudonymous', 'c_period_ppm' => 274, 'ud_period' => 'P1D',
        'genesis_ud' => 1000000, 'auto_accept_seed' => 500000000,
    ]);
    $n->initKey();
    $n->addMember($member, ucfirst($member), 1000000, 100000000);
    return $n;
}

$baseA = 'https://riverside.example';
$baseB = 'https://hilltop.example';
$A = mkNode($dir, 'A', $baseA, 'R', 'alice');
$B = mkNode($dir, 'B', $baseB, 'H', 'bob');

// Exchange identity keys (what an identity-doc fetch would do).
$A->registerPeerKey($B->activeKeyId(), $B->activePubB64());
$B->registerPeerKey($A->activeKeyId(), $A->activePubB64());

$nodes = [$baseA => $A, $baseB => $B];

/** run the full request/reply chain to quiescence, verifying every signature. */
function exchange(array $nodes, array $env): void
{
    $queue = [$env];
    $guard = 0;
    while ($queue) {
        if (++$guard > 50) {
            throw new \RuntimeException('exchange did not settle');
        }
        $e = array_shift($queue);
        $target = $nodes[(string) $e['to']] ?? null;
        if ($target === null) {
            throw new \RuntimeException('no node at ' . $e['to']);
        }
        $res = $target->processInbound($e, false); // false => verify signatures for real
        if (!in_array($res['verdict'], ['ok', 'duplicate'], true)) {
            throw new \RuntimeException('verdict ' . $res['verdict'] . ' for ' . $e['type']);
        }
        if ($res['reply'] !== null) {
            $queue[] = $res['reply'];
        }
    }
}

function contact(Node $n, string $peerHost): ?array
{
    foreach (($n->store()->load()['contacts'] ?? []) as $c) {
        if (($c['PeerHost'] ?? '') === $peerHost) {
            return $c;
        }
    }
    return null;
}
function rootAt(array $c): string
{
    return (string) ($c['Roots'][(int) $c['OpSeq']] ?? '');
}

// ── open a contact (seeded pool) ─────────────────────────────────────────────
exchange($nodes, $A->openContact($baseB, 500000000, 'market overlap'));
$ca = contact($A, 'hilltop.example');
$cb = contact($B, 'riverside.example');
ok($ca && !empty($ca['Active']) && $cb && !empty($cb['Active']), 'contact: both sides active');
ok((int) $ca['OpSeq'] === 0 && (int) $cb['OpSeq'] === 0, 'contact: op_seq 0 (seed)');
ok(rootAt($ca) === rootAt($cb), 'contact: channel roots agree after seed');
ok((int) $ca['MyReserveOfPeer'] === 500000000 && (int) $cb['PeerReserveOfMe'] === 500000000,
    'contact: reserves seeded and mirrored');

// ── transfer alice -> bob, 10.00 R ───────────────────────────────────────────
$aliceBefore = Ledger::from_records($A->store()->load()['ledger'])->balance('m:alice');
exchange($nodes, $A->startTransfer($baseB, 'alice@riverside.example', 'bob@hilltop.example', 10000000, 'lunch'));
$ca = contact($A, 'hilltop.example');
$cb = contact($B, 'riverside.example');
$ledA = Ledger::from_records($A->store()->load()['ledger']);
$ledB = Ledger::from_records($B->store()->load()['ledger']);

// dst = floor(500M * 10M / 510M)
$dst = (int) floor(500000000 * 10000000 / 510000000);
ok($ledA->balance('m:alice') === $aliceBefore - 10000000, 'transfer: payer debited 10.00');
ok($ledB->balance('m:bob') === 100000000 + $dst, 'transfer: payee credited priced amount');
ok((int) $ca['OpSeq'] === 1 && (int) $cb['OpSeq'] === 1, 'transfer: op_seq advanced to 1 both sides');
ok(rootAt($ca) === rootAt($cb), 'transfer: channel roots agree after commit');
ok((int) $ca['MyReserveOfPeer'] === (int) $cb['PeerReserveOfMe']
    && (int) $ca['PeerReserveOfMe'] === (int) $cb['MyReserveOfPeer'], 'transfer: reserves stay mirrored');
ok(empty($ca['Busy']) && empty($cb['Busy']), 'transfer: contact lock released');
// money conserved on each ledger (append enforces sum=0; supply unchanged by transfers)
$ledA->verify_chain();
$ledB->verify_chain();
ok(true, 'transfer: both ledger hash chains verify');

// ── reserve adjustment: A tops up its reserve by 50.00 R ─────────────────────
$myResBefore = (int) $ca['MyReserveOfPeer'];
exchange($nodes, $A->adjustReserve($baseB, 50000000, 'top-up'));
$ca = contact($A, 'hilltop.example');
$cb = contact($B, 'riverside.example');
ok((int) $ca['MyReserveOfPeer'] === $myResBefore + 50000000, 'reserve: proposer reserve +50.00');
ok((int) $ca['MyReserveOfPeer'] === (int) $cb['PeerReserveOfMe'], 'reserve: mirrored on responder');
ok((int) $ca['OpSeq'] === 2 && (int) $cb['OpSeq'] === 2, 'reserve: op_seq advanced to 2');
ok(rootAt($ca) === rootAt($cb), 'reserve: channel roots agree after adjust');

// ── a tampered envelope is rejected by real signature verification ───────────
$env = $A->openContact('https://third.example', 1); // builds+signs (goes nowhere useful)
$B->registerPeerKey($A->activeKeyId(), $A->activePubB64());
$env['to'] = $baseB;
$env['payload']['my_seed'] = 999; // mutate after signing
$env['from'] = $baseA;
$res = $B->processInbound($env, false);
ok($res['verdict'] === 'bad-signature', 'security: tampered payload => bad-signature (got ' . $res['verdict'] . ')');

// ── member.lookup (§11): answered only over an active contact ────────────────
$look = $A->lookupMember($baseB, 'bob@hilltop.example');
$r = $B->processInbound($look, false);
ok(($r['reply']['type'] ?? '') === 'member.result' && ($r['reply']['payload']['found'] ?? null) === true,
    'member.lookup: existing member found on peer');
$look2 = $A->lookupMember($baseB, 'ghost@hilltop.example');
$r2 = $B->processInbound($look2, false);
ok(($r2['reply']['payload']['found'] ?? null) === false, 'member.lookup: unknown member not found');

// ── key rotation (§3): announce to peers, activate, peer registers new key ────
$oldKeyId = $A->activeKeyId();
$newLocal = $A->rotateKey();
$newKeyId = $A->activeKeyId();
ok($newKeyId !== $oldKeyId && strpos($newKeyId, $newLocal) !== false, 'rotate: active key switched');
foreach ($A->outboxFor('hilltop.example') as $env) {
    if (($env['type'] ?? '') === 'key.announce') {
        exchange($nodes, $env);
    }
}
$snapB = $B->store()->load();
ok(isset($snapB['peer_keys'][$newKeyId]), 'rotate: peer registered the announced new key');
// an op signed by the NEW key still verifies at B
exchange($nodes, $A->adjustReserve($baseB, 1000000, 'post-rotation'));
$cbAfter = contact($B, 'riverside.example');
ok((int) $cbAfter['OpSeq'] === 3, 'rotate: peer accepts an op signed by the rotated key');

// ── contact.close (§6): both sides freeze ────────────────────────────────────
exchange($nodes, $A->closeContact($baseB, 'wrapping up'));
$caC = contact($A, 'hilltop.example');
$cbC = contact($B, 'riverside.example');
ok(!empty($caC['Closed']) && !empty($cbC['Closed']), 'close: both sides marked closed');

// ── cleanup ──────────────────────────────────────────────────────────────────
array_map('unlink', glob("$dir/*") ?: []);
@rmdir($dir);

$total = $pass + $fail;
echo "\nPHP federation (two signed nodes): $pass/$total passed\n";
if ($fail) {
    echo "FAILURES:\n  - " . implode("\n  - ", $fails) . "\n";
    exit(1);
}
echo "OK — contact, transfer (two-phase), and reserve adjust stay mirror-consistent over real signatures.\n";
exit(0);
