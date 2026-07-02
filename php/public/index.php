<?php
// index.php — the node's HTTP front controller (PROTOCOL §3–5, §8.3, §9.2). It
// serves the read surface (identity doc, checkpoint, outbox, log) and accepts
// pushed envelopes at /lp/inbox. This is the only file that needs to be reachable
// from the web; keep the state file and config OUT of the web root.
//
// Run locally:
//   LP_STATE_FILE=/tmp/state.json php -S 127.0.0.1:8080 php/public/index.php
// Under Apache, point a rewrite of everything to this file (see .htaccess sample
// in php/README.md).

declare(strict_types=1);

require __DIR__ . '/../lp_node.php';

use lp\Node;
use lp\Store;

$cfg = is_file(__DIR__ . '/../config.php')
    ? require __DIR__ . '/../config.php'
    : require __DIR__ . '/../config.example.php';

$node = new Node(new Store($cfg['state_file']), $cfg);
$node->setTransport(new \lp\HttpTransport());

$path = parse_url($_SERVER['REQUEST_URI'] ?? '/', PHP_URL_PATH) ?: '/';
$method = $_SERVER['REQUEST_METHOD'] ?? 'GET';

function send_json(int $code, $body): void
{
    http_response_code($code);
    header('Content-Type: application/json');
    echo json_encode($body, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
}

// ── read surface (GET) ────────────────────────────────────────────────────────
if ($method === 'GET') {
    if ($path === '/.well-known/liquiditypub') {
        send_json(200, $node->identityDoc());
        return;
    }
    if ($path === '/lp/checkpoint.json') {
        send_json(200, $node->checkpoint());
        return;
    }
    if (preg_match('#^/lp/outbox/([^/]+)\.json$#', $path, $m)) {
        send_json(200, $node->outboxFor($m[1]));
        return;
    }
    if ($path === '/lp/log/head.json') {
        $cp = $node->checkpoint();
        send_json(200, ['log_seq' => $cp['log_seq'], 'log_hash' => $cp['log_hash']]);
        return;
    }
    if (preg_match('#^/lp/log/page-(\d+)\.json$#', $path, $m)) {
        send_json(200, $node->logPage((int) $m[1]));
        return;
    }
    send_json(404, ['code' => 'not-found', 'detail' => $path]);
    return;
}

// ── push inbox (POST) ─────────────────────────────────────────────────────────
if ($method === 'POST' && $path === '/lp/inbox') {
    $raw = file_get_contents('php://input');
    $env = json_decode((string) $raw, true);
    if (!is_array($env)) {
        send_json(400, ['code' => 'malformed', 'detail' => 'body is not a JSON object']);
        return;
    }
    $res = $node->processInbound($env, false);
    $verdict = $res['verdict'];
    if ($verdict === 'ok' || $verdict === 'duplicate') {
        // If a reply was produced it is already durably recorded in our outbox;
        // acknowledge (the peer picks up the reply by pulling our outbox, or the
        // push transport delivers it separately).
        send_json(202, ['status' => 'accepted', 'verdict' => $verdict]);
        return;
    }
    $code = ($verdict === 'unknown-key' || $verdict === 'bad-signature') ? 403 : 400;
    send_json($code, ['code' => $verdict]);
    return;
}

send_json(405, ['code' => 'method-not-allowed', 'detail' => $method . ' ' . $path]);
