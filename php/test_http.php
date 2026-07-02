<?php
// test_http.php — drives the HTTP front controller (php/public/index.php) with a
// real php -S server: the read surface returns well-formed JSON and /lp/inbox
// routes through Node::processInbound (rejecting an envelope whose key we don't
// hold). Seeds a temp node state, points the server at it via env, and curls.
//
//   php php/test_http.php

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

$dir = sys_get_temp_dir() . '/lphttp-' . getmypid();
@mkdir($dir, 0700, true);
$statePath = "$dir/state.json";
$port = 8100 + (getmypid() % 400);
$base = "http://127.0.0.1:$port";

// Seed a node with a member so the read surface has content.
$cfg = [
    'base' => $base, 'state_file' => $statePath, 'name' => 'Riverside',
    'currency_name' => 'River', 'currency_symbol' => 'R', 'transparency' => 'pseudonymous',
    'c_period_ppm' => 274, 'ud_period' => 'P1D', 'genesis_ud' => 1000000, 'auto_accept_seed' => 500000000,
];
$seed = new Node(new Store($statePath), $cfg);
$seed->initKey();
$seed->addMember('alice', 'Alice', 1000000, 100000000);

// Boot the server with the front controller as the router.
$env = "LP_BASE=$base LP_STATE_FILE=$statePath";
$cmd = "$env php -S 127.0.0.1:$port " . escapeshellarg(__DIR__ . '/public/index.php') . " >/dev/null 2>&1 & echo \$!";
$pid = (int) trim(shell_exec($cmd));
// wait for the port
for ($i = 0; $i < 50; $i++) {
    $fp = @fsockopen('127.0.0.1', $port, $e, $s, 0.1);
    if ($fp) {
        fclose($fp);
        break;
    }
    usleep(50000);
}

function http(string $method, string $url, ?string $body = null): array
{
    $ch = curl_init($url);
    curl_setopt_array($ch, [
        CURLOPT_RETURNTRANSFER => true, CURLOPT_CUSTOMREQUEST => $method,
        CURLOPT_TIMEOUT => 5,
    ]);
    if ($body !== null) {
        curl_setopt($ch, CURLOPT_POSTFIELDS, $body);
        curl_setopt($ch, CURLOPT_HTTPHEADER, ['Content-Type: application/json']);
    }
    $out = curl_exec($ch);
    $code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    curl_close($ch);
    return [$code, json_decode((string) $out, true)];
}

try {
    [$c, $doc] = http('GET', "$base/.well-known/liquiditypub");
    ok($c === 200 && ($doc['lp'] ?? '') === '0.2' && ($doc['node']['base'] ?? '') === $base,
        'GET identity doc: 200 + base/version');
    ok(!empty($doc['keys']) && !empty($doc['keys'][0]['public_key']), 'identity doc: publishes a key');

    [$c, $cp] = http('GET', "$base/lp/checkpoint.json");
    ok($c === 200 && isset($cp['log_hash'], $cp['money_supply']), 'GET checkpoint: 200 + fields');
    ok((int) $cp['money_supply'] === 100000000, 'checkpoint: money supply reflects grant');
    ok(!empty($cp['sig']['value']) && Node::verifySignedDoc($cp, $seed->activePubB64()),
        'checkpoint: served signed and verifies');

    [$c, $ob] = http('GET', "$base/lp/outbox/hilltop.example.json");
    ok($c === 200 && is_array($ob), 'GET outbox: 200 + array');

    [$c, $head] = http('GET', "$base/lp/log/head.json");
    ok($c === 200 && isset($head['log_seq'], $head['log_hash']), 'GET log head: 200 + fields');

    [$c, $pg] = http('GET', "$base/lp/log/page-0.json");
    ok($c === 200 && is_array($pg) && count($pg) >= 1, 'GET log page-0: 200 + records');

    [$c, $b] = http('POST', "$base/lp/inbox", 'not json');
    ok($c === 400, 'POST inbox garbage: 400 malformed');

    // An envelope signed by a key we do not hold => unknown-key (403).
    $spoof = json_encode([
        'lp' => '0.2', 'id' => 'urn:uuid:1', 'type' => 'ping', 'from' => 'https://stranger.example',
        'to' => $base, 'seq' => 1, 'created' => gmdate('c'), 're' => null, 'payload' => new stdClass(),
        'sig' => ['key' => 'https://stranger.example/.well-known/liquiditypub#nk1', 'alg' => 'ed25519', 'value' => 'AA'],
    ]);
    [$c, $b] = http('POST', "$base/lp/inbox", $spoof);
    ok($c === 403 && ($b['code'] ?? '') === 'unknown-key', 'POST inbox unknown key: 403 unknown-key');

    [$c, $b] = http('GET', "$base/nope");
    ok($c === 404, 'GET unknown path: 404');
} finally {
    if ($pid) {
        @exec("kill $pid 2>/dev/null");
    }
    array_map('unlink', glob("$dir/*") ?: []);
    @rmdir($dir);
}

$total = $pass + $fail;
echo "\nPHP HTTP surface: $pass/$total passed\n";
if ($fail) {
    echo "FAILURES:\n  - " . implode("\n  - ", $fails) . "\n";
    exit(1);
}
echo "OK — read surface + inbox route serve correctly over HTTP.\n";
exit(0);
