<?php
// test_node.php — exercises the PHP node layer end to end against its own store:
// genesis + issuance apply to the real ledger, the read surface is well-formed,
// the snapshot round-trips in the Go schema shape, and the operator action queue
// drains local intents while deferring federation ones.
//
//   php php/test_node.php

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

$dir = sys_get_temp_dir() . '/lpnode-test-' . getmypid();
@mkdir($dir, 0700, true);
$statePath = $dir . '/state.json';
$queuePath = $dir . '/action-queue.jsonl';

$cfg = [
    'base' => 'https://riverside.example', 'name' => 'Riverside',
    'currency_name' => 'River Credits', 'currency_symbol' => 'ʀ',
    'transparency' => 'pseudonymous', 'c_period_ppm' => 274,
    'ud_period' => 'P1D', 'genesis_ud' => 1000000,
];
$node = new Node(new Store($statePath), $cfg);

// ── genesis: admit two members with grants ───────────────────────────────────
$node->addMember('alice', 'Alice', 1000000, 100000000); // 100.00 ʀ
$node->addMember('bob', 'Bob', 2000000, 50000000);       // 50.00 ʀ, weight 2×

$snap = $node->store()->load();
$led = Ledger::from_records($snap['ledger']);
ok($led->money_supply() === 150000000, 'genesis: money supply = 150.00 (sum of grants)');
ok($led->balance('m:alice') === 100000000, 'genesis: alice balance');
ok($led->balance('m:bob') === 50000000, 'genesis: bob balance');
ok(count(Node::activeMembers($snap)) === 2, 'genesis: two active members');
ok(Node::weightTotal($snap) === 3000000, 'genesis: weight total 3.0');

// ── run one UD period, applied to the real ledger ────────────────────────────
$supplyBefore = $led->money_supply();
$udBase = $node->runUD();
// ud_base = floor(150000000 * 274 / 3000000) = floor(13700) = 13700; genesis floor
// lifts it to 1_000_000. alice gets floor(1e6 * 1e6 / 1e6) = 1_000_000; bob 2_000_000.
ok($udBase === 1000000, "UD: ud_base floored to genesis (got $udBase)");
$snap = $node->store()->load();
$led = Ledger::from_records($snap['ledger']);
ok($led->balance('m:alice') === 101000000, 'UD: alice +1.00');
ok($led->balance('m:bob') === 52000000, 'UD: bob +2.00 (weight 2×)');
ok($led->money_supply() === $supplyBefore + 3000000, 'UD: supply grew by total dividend');
ok((int) $snap['current_ud'] === 1000000, 'UD: current_ud recorded');
$led->verify_chain();
ok(true, 'UD: ledger hash chain still verifies after issuance');

// ── read surface shape ───────────────────────────────────────────────────────
$id = $node->identityDoc();
ok(($id['lp'] ?? '') === '0.2' && ($id['node']['base'] ?? '') === $cfg['base'], 'identity doc: base + version');
ok(isset($id['endpoints']['inbox'], $id['endpoints']['checkpoint']), 'identity doc: endpoints present');
$cp = $node->checkpoint();
ok($cp['log_hash'] === $led->head() && $cp['log_seq'] === $led->len(), 'checkpoint: head matches ledger');
ok($cp['money_supply'] === $led->money_supply(), 'checkpoint: money supply matches ledger');
$node->initKey();
$cp = $node->checkpoint();
ok(!empty($cp['sig']['value']), 'checkpoint: carries a signature');
ok(Node::verifySignedDoc($cp, $node->activePubB64()), 'checkpoint: signature verifies against the node key');

// ── snapshot round-trips in Go-schema shape (empty maps as objects) ──────────
$raw = file_get_contents($statePath);
ok(strpos($raw, '"out_seq":{}') !== false || strpos($raw, '"out_seq": {}') !== false || strpos($raw, '"peer_keys":{}') !== false,
    'snapshot: empty map fields encode as {} not [] (Go-unmarshalable)');
$reloaded = json_decode($raw, true);
ok(is_array($reloaded['members']) && ($reloaded['members'][0]['Name'] ?? '') === 'alice',
    'snapshot: members are a list with capitalized Go field names');

// ── action queue: local intents apply, federation intents defer ──────────────
file_put_contents($queuePath, implode("\n", [
    json_encode(['action' => 'add_member', 'name' => 'carol', 'display_name' => 'Carol', 'weight_micro' => 1000000]),
    json_encode(['action' => 'run_ud']),
    json_encode(['action' => 'open_contact', 'peer_base' => 'https://hilltop.example', 'my_seed_micro' => 500000000]),
    json_encode(['action' => 'send_transfer', 'from_member' => 'alice', 'to' => 'x@hilltop.example', 'src_amount_micro' => 1000000]),
]) . "\n");
$res = $node->drainActionQueue($queuePath);
ok($res['applied'] === 2, "action queue: applied 2 local intents (got {$res['applied']})");
ok($res['deferred'] === 2, "action queue: deferred 2 federation intents (got {$res['deferred']})");
ok(empty($res['errors']), 'action queue: no errors');
$snap = $node->store()->load();
ok(isset(Node::activeMembers($snap)['carol']), 'action queue: carol admitted via queue');
$remaining = file_get_contents($queuePath);
ok(strpos($remaining, 'open_contact') !== false && strpos($remaining, 'add_member') === false,
    'action queue: only federation intents remain queued');

// ── inbound validation: sig.key must be bound to from ────────────────────────
$snap = $node->store()->load();
$snap['peer_keys'] = ['https://attacker.example/.well-known/liquiditypub#nk1' => 'AAAA'];
$spoof = ['lp' => '0.2', 'from' => 'https://victim.example', 'id' => 'urn:uuid:x', 'seq' => 1,
    'sig' => ['key' => 'https://attacker.example/.well-known/liquiditypub#nk1', 'value' => 'x']];
ok($node->validateInbound($spoof, $snap) === 'unknown-key',
    'inbound: key not published by `from` is rejected (impersonation guard)');

// ── cleanup ──────────────────────────────────────────────────────────────────
@unlink($statePath);
@unlink($statePath . '.lock');
@unlink($queuePath);
@rmdir($dir);

$total = $pass + $fail;
echo "\nPHP node layer: $pass/$total passed\n";
if ($fail) {
    echo "FAILURES:\n  - " . implode("\n  - ", $fails) . "\n";
    exit(1);
}
echo "OK — store, ledger issuance, read surface, action queue, and inbound validation all behave.\n";
exit(0);
