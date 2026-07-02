<?php
// test_expire_accept.php — the payer-side expiry guard (§7.4). A transfer.accept
// that arrives after the transfer has expired must NOT commit: committing would
// append the payer's leg (debiting the sender) with no counterpart on the payee,
// destroying money one-sidedly and forking the channel. The payer must instead
// expire its side, release the lock, and reply transfer.abort.
//
//   php php/test_expire_accept.php

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

$dir = sys_get_temp_dir() . '/lpexp-' . getmypid();
@mkdir($dir, 0700, true);

$baseA = 'https://riverside.example';
$baseB = 'https://hilltop.example';
$node = new Node(new Store("$dir/a.json"), [
    'base' => $baseA, 'name' => 'A', 'currency_name' => 'R', 'currency_symbol' => 'R',
    'transparency' => 'pseudonymous', 'c_period_ppm' => 274, 'ud_period' => 'P1D',
    'genesis_ud' => 1000000, 'auto_accept_seed' => 500000000,
]);
$node->initKey();
$node->addMember('alice', 'Alice', 1000000, 100000000);

// Trust-mode inbound still checks the key↔from binding, so register a (dummy)
// key published by B; the signature itself is skipped under $trust=true.
$bKey = $baseB . '/.well-known/liquiditypub#nk1';
$node->registerPeerKey($bKey, 'AA');

// Inject an active contact to B with a busy, already-expired outgoing PROPOSED
// transfer — the exact state left when our transfer.propose was accepted late.
$node->store()->transact(function (array &$snap): void {
    $snap['contacts'][] = [
        'ID' => 'c1', 'PeerBase' => 'https://hilltop.example', 'PeerHost' => 'hilltop.example',
        'IAmProposer' => true, 'Active' => true, 'Closed' => false,
        'ProposerSeed' => 500000000, 'ResponderSeed' => 500000000,
        'MyReserveOfPeer' => 500000000, 'PeerReserveOfMe' => 500000000,
        'OpSeq' => 0, 'Roots' => ['seed-root-b64'], 'Busy' => true, 'BusyTransfer' => 't1',
    ];
    $snap['transfers'][] = [
        'ID' => 't1', 'ContactID' => 'c1', 'Outgoing' => true, 'State' => 'PROPOSED',
        'OpSeq' => 0, 'FromMember' => 'alice@riverside.example', 'ToMember' => 'bob@hilltop.example',
        'SrcAmount' => 10000000, 'DstAmount' => 9803921, 'Expires' => gmdate('c', time() - 10),
    ];
});

$before = Ledger::from_records($node->store()->load()['ledger'])->balance('m:alice');

// B's transfer.accept lands late (after expiry). Trust mode: binding checked,
// signature skipped.
$accept = [
    'lp' => '0.2', 'id' => 'urn:uuid:late-accept', 'type' => 'transfer.accept',
    'from' => $baseB, 'to' => $baseA, 'seq' => 1, 'created' => gmdate('c'), 're' => null,
    'payload' => ['transfer_id' => 't1'],
    'sig' => ['key' => $bKey, 'alg' => 'ed25519', 'value' => 'AA'],
];
$res = $node->processInbound($accept, true);

ok($res['verdict'] === 'ok', 'accept processed (verdict ok)');
ok(($res['reply']['type'] ?? '') === 'transfer.abort', 'late accept => transfer.abort, not commit');

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
ok($t['State'] === 'EXPIRED', 'transfer moved to EXPIRED (not COMMITTED)');
ok(empty($c['Busy']), 'contact lock released');
ok((int) $c['OpSeq'] === 0, 'channel op_seq did NOT advance (no committed op)');

$after = Ledger::from_records($snap['ledger'])->balance('m:alice');
ok($after === $before, 'sender NOT debited — no money destroyed one-sidedly');

array_map('unlink', glob("$dir/*") ?: []);
@rmdir($dir);

$total = $pass + $fail;
echo "\nPHP payer-side expiry guard: $pass/$total passed\n";
if ($fail) {
    echo "FAILURES:\n  - " . implode("\n  - ", $fails) . "\n";
    exit(1);
}
echo "OK — a transfer.accept after expiry aborts instead of committing; money conserved.\n";
exit(0);
