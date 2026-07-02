<?php
// test_vectors.php — runs the PHP core against the SAME conformance vectors as
// the Go reference (../conformance/vectors/*.json). This is what keeps the two
// implementations honest: if PHP and Go disagree on a single byte, a vector
// fails here. Exit code is non-zero on any failure, so it drops into CI.
//
//   php php/test_vectors.php
//
// Signature vectors need the sodium extension; without it they report SKIP
// rather than fail, since the canonical *preimage* (the real divergence risk)
// is still checked byte-for-byte.

declare(strict_types=1);

require __DIR__ . '/lp_core.php';
require __DIR__ . '/lp_ledger.php';

use lp\Ledger;
use lp\LedgerError;
use function lp\canonical;
use function lp\channel_root0;
use function lp\channel_next;
use function lp\pool_price;
use function lp\ud_base;
use function lp\ud_recipient;
use function lp\b64url_decode;
use function lp\sign_envelope;
use function lp\verify_detached;
use function lp\have_sodium;

$VEC = dirname(__DIR__) . '/conformance/vectors';

$pass = 0;
$fail = 0;
$skip = 0;
$fails = [];

function ok(bool $cond, string $what)
{
    global $pass, $fail, $fails;
    if ($cond) {
        $pass++;
    } else {
        $fail++;
        $fails[] = $what;
        fwrite(STDERR, "  FAIL: $what\n");
    }
}

function load(string $path)
{
    return json_decode(file_get_contents($path), false, 512, JSON_THROW_ON_ERROR);
}

// ── canonical JSON + envelope signing ────────────────────────────────────────
$d = load("$VEC/envelope_sign.json");
$bare = clone $d->envelope;
unset($bare->sig);
$got = canonical($bare);
ok($got === $d->canonical, "envelope_sign: canonical bytes match reference");
if ($got !== $d->canonical) {
    fwrite(STDERR, "    want: {$d->canonical}\n     got: {$got}\n");
}

if (have_sodium()) {
    $seed = hex2bin($d->seed_hex);
    $sig = sign_envelope($d->envelope, $seed);
    ok($sig === $d->envelope->sig->value, "envelope_sign: ed25519 signature reproduces (deterministic)");
    $pub = b64url_decode($d->public_key_b64);
    ok(verify_detached($d->canonical, b64url_decode($d->envelope->sig->value), $pub),
        "envelope_sign: signature verifies against published key");
} else {
    $skip += 2;
    fwrite(STDERR, "  SKIP: ed25519 sign/verify (sodium extension not loaded)\n");
}

// ── channel hash chain ───────────────────────────────────────────────────────
$d = load("$VEC/channel_hash.json");
$root = channel_root0($d->contact_id);
ok(bin2hex($root) === $d->roots[0], "channel_hash: root0 = SHA-256(contact_id)");
foreach ($d->ops as $i => $op) {
    // op = [type, id, src, dst]
    $root = channel_next($root, $op[0], $op[1], (int) $op[2], (int) $op[3]);
    ok(bin2hex($root) === $d->roots[$i + 1], "channel_hash: root after op $i ({$op[0]})");
}

// ── pool pricing ─────────────────────────────────────────────────────────────
$d = load("$VEC/pool_pricing.json");
foreach ($d->cases as $c) {
    try {
        $dst = pool_price((int) $c->r_src, (int) $c->r_dst, (int) $c->src);
        if (isset($c->error)) {
            ok(false, "pool_pricing: {$c->name} expected error {$c->error}, got $dst");
        } else {
            ok($dst === (int) $c->dst, "pool_pricing: {$c->name}");
        }
    } catch (\RuntimeException $e) {
        if (isset($c->error)) {
            ok($e->getMessage() === $c->error, "pool_pricing: {$c->name} (error {$c->error})");
        } else {
            ok(false, "pool_pricing: {$c->name} unexpected error {$e->getMessage()}");
        }
    }
}

// ── UD reference ─────────────────────────────────────────────────────────────
$d = load("$VEC/ud_reference.json");
foreach ($d->cases as $c) {
    $base = ud_base((int) $c->money_supply, (int) $c->c_period_ppm, (int) $c->ud_weight_total);
    ok($base === (int) $c->ud_base, "ud_reference: {$c->name} ud_base");
    foreach ($c->recipients as $r) {
        $amt = ud_recipient($base, (int) $r->weight);
        ok($amt === (int) $r->amount, "ud_reference: {$c->name} weight {$r->weight}");
    }
}

// ── ledger transcript (append-only log, §9.2) ────────────────────────────────
$d = json_decode(file_get_contents("$VEC/ledger_transcript.json"), true, 512, JSON_THROW_ON_ERROR);
$led = new Ledger();
foreach ($d['txs'] as $i => $tx) {
    $rec = $led->append($tx);
    ok($rec['hash'] === $d['records'][$i]['hash'], "ledger_transcript: record " . ($i + 1) . " ({$tx['type']}) hash");
    ok($rec['prev'] === $d['records'][$i]['prev'], "ledger_transcript: record " . ($i + 1) . " prev-link");
}
ok($led->head() === $d['head'], "ledger_transcript: head hash");
ok($led->money_supply() === (int) $d['money_supply'], "ledger_transcript: money supply");
$balOk = true;
foreach ($d['balances'] as $acct => $want) {
    if ($led->balance($acct) !== (int) $want) {
        $balOk = false;
    }
}
ok($balOk, "ledger_transcript: all account balances");

// verify_chain accepts the freshly built log, and rebuilding from records agrees.
try {
    $led->verify_chain();
    $reloaded = Ledger::from_records($led->records());
    ok($reloaded->head() === $d['head'] && $reloaded->money_supply() === (int) $d['money_supply'],
        "ledger_transcript: verify_chain + reload from records");
} catch (LedgerError $e) {
    ok(false, "ledger_transcript: verify_chain unexpectedly failed: {$e->getMessage()}");
}

// Invariants reject bad transactions.
$l2 = new Ledger();
try {
    $l2->append(['id' => 'x', 'type' => 't', 'created' => 'now',
        'entries' => [['account' => 'm:a', 'amount' => 5], ['account' => 'm:b', 'amount' => -4]]]);
    ok(false, "ledger invariant: unbalanced tx must be rejected");
} catch (LedgerError $e) {
    ok(true, "ledger invariant: unbalanced tx rejected");
}
try {
    $l2->append(['id' => 'y', 'type' => 't', 'created' => 'now',
        'entries' => [['account' => 'node:peer', 'amount' => -1], ['account' => 'issuance', 'amount' => 1]]]);
    ok(false, "ledger invariant: node wallet negative must be rejected");
} catch (LedgerError $e) {
    ok(true, "ledger invariant: node wallet negative rejected");
}

// ── summary ──────────────────────────────────────────────────────────────────
$total = $pass + $fail;
echo "\n";
echo "PHP core vs Go conformance vectors: $pass/$total passed";
if ($skip) {
    echo ", $skip skipped";
}
echo "\n";
if ($fail > 0) {
    echo "FAILURES:\n  - " . implode("\n  - ", $fails) . "\n";
    exit(1);
}
echo "OK — PHP and Go produce identical bytes on every runnable vector.\n";
exit(0);
