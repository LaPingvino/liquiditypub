<?php
// test_reconcile.php — transfer expiry (§7.4) and checkpoint reconciliation
// (§8.3): a stale pre-commit transfer is swept and its lock released; a peer
// checkpoint that conflicts at the common op_seq freezes the contact and blocks
// new operations; acknowledged outbox entries are pruned.
//
//   php php/test_reconcile.php

declare(strict_types=1);

require __DIR__ . '/lp_node.php';

use lp\Node;
use lp\Store;

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

$dir = sys_get_temp_dir() . '/lprec-' . getmypid();
@mkdir($dir, 0700, true);
$cfg = [
    'base' => 'https://riverside.example', 'state_file' => "$dir/s.json",
    'queue_file' => "$dir/q.jsonl", 'name' => 'R', 'currency_name' => 'R', 'currency_symbol' => 'R',
    'transparency' => 'pseudonymous', 'c_period_ppm' => 274, 'ud_period' => 'P1D',
    'genesis_ud' => 1000000, 'auto_accept_seed' => 500000000,
];
$node = new Node(new Store($cfg['state_file']), $cfg);
$node->initKey();
$node->addMember('alice', 'Alice', 1000000, 100000000);

// Inject an active contact with a busy, already-expired pre-commit transfer, plus
// two outbox entries to test pruning.
$node->store()->transact(function (array &$snap): void {
    $snap['contacts'][] = [
        'ID' => 'c1', 'PeerBase' => 'https://peer.example', 'PeerHost' => 'peer.example',
        'IAmProposer' => true, 'Active' => true, 'Closed' => false,
        'ProposerSeed' => 500000000, 'ResponderSeed' => 500000000,
        'MyReserveOfPeer' => 500000000, 'PeerReserveOfMe' => 500000000,
        'OpSeq' => 0, 'Roots' => ['our-root-b64'], 'Busy' => true, 'BusyTransfer' => 't1',
    ];
    $snap['transfers'][] = [
        'ID' => 't1', 'ContactID' => 'c1', 'Outgoing' => true, 'State' => 'PROPOSED',
        'OpSeq' => 0, 'FromMember' => 'alice@riverside.example', 'ToMember' => 'x@peer.example',
        'SrcAmount' => 10000000, 'DstAmount' => 9803921, 'Expires' => gmdate('c', time() - 10),
    ];
    $snap['outbox']['peer.example'] = [
        ['seq' => 3, 'type' => 'transfer.propose'],
        ['seq' => 7, 'type' => 'transfer.commit'],
    ];
});

// ── expiry sweep ─────────────────────────────────────────────────────────────
$n = $node->sweepExpired();
ok($n === 1, "expiry: swept 1 stale transfer (got $n)");
$snap = $node->store()->load();
$t = null;
$c = null;
foreach ($snap['transfers'] as $x) {
    if ($x['ID'] === 't1') {
        $t = $x;
    }
}
foreach ($snap['contacts'] as $x) {
    if ($x['ID'] === 'c1') {
        $c = $x;
    }
}
ok($t['State'] === 'EXPIRED', 'expiry: transfer moved to EXPIRED');
ok(empty($c['Busy']), 'expiry: contact lock released');

// ── reconciliation: a conflicting root at op_seq freezes the contact ─────────
$cp = ['contacts' => [[
    'contact_id' => 'c1', 'op_seq' => 0, 'channel_root' => 'DIFFERENT-root-b64', 'last_seq_processed' => 5,
]]];
$res = $node->reconcileAgainst('https://peer.example', $cp);
ok(!empty($res['diverged']), 'reconcile: conflicting root at op_seq flags divergence');
ok(($res['pruned'] ?? 0) === 1, 'reconcile: pruned the acknowledged outbox entry (seq 3)');
$snap = $node->store()->load();
foreach ($snap['contacts'] as $x) {
    if ($x['ID'] === 'c1') {
        $c = $x;
    }
}
ok(!empty($c['Diverged']), 'reconcile: contact marked Diverged');
ok(count($snap['outbox']['peer.example']) === 1 && (int) $snap['outbox']['peer.example'][0]['seq'] === 7,
    'reconcile: only the unacknowledged entry (seq 7) remains');

// a frozen contact refuses new operations
$threw = false;
try {
    $node->startTransfer('https://peer.example', 'alice@riverside.example', 'x@peer.example', 1000000);
} catch (\Throwable $e) {
    $threw = strpos($e->getMessage(), 'frozen') !== false;
}
ok($threw, 'reconcile: frozen contact refuses a new transfer');

// a matching root does NOT freeze a fresh contact
$node->store()->transact(function (array &$snap): void {
    $snap['contacts'][] = [
        'ID' => 'c2', 'PeerBase' => 'https://ok.example', 'PeerHost' => 'ok.example',
        'IAmProposer' => true, 'Active' => true, 'OpSeq' => 0, 'Roots' => ['agree-root'],
        'MyReserveOfPeer' => 1, 'PeerReserveOfMe' => 1,
    ];
});
$res2 = $node->reconcileAgainst('https://ok.example',
    ['contacts' => [['contact_id' => 'c2', 'op_seq' => 0, 'channel_root' => 'agree-root', 'last_seq_processed' => 0]]]);
ok(empty($res2['diverged']), 'reconcile: matching root is normal (no false freeze)');

array_map('unlink', glob("$dir/*") ?: []);
@rmdir($dir);

$total = $pass + $fail;
echo "\nPHP expiry + reconciliation: $pass/$total passed\n";
if ($fail) {
    echo "FAILURES:\n  - " . implode("\n  - ", $fails) . "\n";
    exit(1);
}
echo "OK — stale transfers expire, conflicting checkpoints freeze, acks prune.\n";
exit(0);
