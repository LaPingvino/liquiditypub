<?php
declare(strict_types=1);

header('Content-Type: application/json');
header('Access-Control-Allow-Origin: *');

$info = Node::publicInfo();
$members = Member::allActive();

$response = [
    'liquiditypub' => '0.1',
    'node' => [
        'name'        => $info['name'],
        'description' => $info['description'],
        'inbox'       => (isset($_SERVER['HTTPS']) ? 'https' : 'http') . '://' . $_SERVER['HTTP_HOST'] . '/api/inbox',
    ],
    'currency' => [
        'name'     => $info['currency_name'],
        'symbol'   => $info['currency_symbol'],
        'issuance' => $info['issuance_type'],
    ],
    'stats' => [
        'active_members' => count($members),
    ],
    'public_key' => $info['public_key'],
];

echo json_encode($response, JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES);
