<?php
declare(strict_types=1);

/**
 * Federation inbox — v0.1 stub
 *
 * Accepts signed JSON messages from federated nodes.
 * Signature verification and message routing are scaffolded
 * but not fully wired in this PoC.
 */

header('Content-Type: application/json');

$raw = file_get_contents('php://input');
if ($raw === false || $raw === '') {
    http_response_code(400);
    echo json_encode(['error' => 'Empty body']);
    exit;
}

$payload = json_decode($raw, true);
if (!is_array($payload)) {
    http_response_code(400);
    echo json_encode(['error' => 'Invalid JSON']);
    exit;
}

// Validate required fields
$type     = $payload['type'] ?? '';
$from     = $payload['from'] ?? '';
$signature= $payload['signature'] ?? '';

if ($type === '' || $from === '') {
    http_response_code(422);
    echo json_encode(['error' => 'Missing type or from fields']);
    exit;
}

// TODO: Verify HTTP Signature against sender's public key (fetched from /.well-known/liquiditypub)
// TODO: Route message types: 'exchange_request', 'exchange_confirm', 'member_lookup', etc.

// For PoC: log and acknowledge
$accepted = [
    'exchange_request',
    'exchange_confirm',
    'member_lookup',
    'ping',
];

if (!in_array($type, $accepted, true)) {
    http_response_code(422);
    echo json_encode(['error' => 'Unknown message type', 'accepted_types' => $accepted]);
    exit;
}

// Minimal ping handler
if ($type === 'ping') {
    echo json_encode([
        'status' => 'ok',
        'node'   => Node::get('name'),
        'pong'   => true,
    ]);
    exit;
}

// Stub: queue for future processing
http_response_code(202);
echo json_encode([
    'status'  => 'accepted',
    'message' => 'Message queued for processing (federation not yet wired in PoC)',
    'type'    => $type,
]);
