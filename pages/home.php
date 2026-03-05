<?php
declare(strict_types=1);
// Loaded via index.php — classes already available

$info    = Node::publicInfo();
$members = Member::all();
$active  = array_filter($members, fn($m) => $m['active']);

$body = '<div class="container">';
$body .= '<div class="hero">';
$body .= '<h1>' . htmlspecialchars($info['currency_symbol']) . ' ' . htmlspecialchars($info['name']) . '</h1>';
if ($info['description'] !== '') {
    $body .= '<p>' . htmlspecialchars($info['description']) . '</p>';
}
$body .= '<span class="currency-badge">' . htmlspecialchars($info['currency_name']) . ' (' . htmlspecialchars($info['currency_symbol']) . ')</span>';
$body .= '</div>';

// Stats
$body .= '<div class="stat-grid">';
$body .= '<div class="stat-box"><div class="val">' . count($active) . '</div><div class="lbl">Active Members</div></div>';
$body .= '<div class="stat-box"><div class="val">' . ucfirst(htmlspecialchars($info['issuance_type'])) . '</div><div class="lbl">Issuance Type</div></div>';
$body .= '<div class="stat-box"><div class="val">' . htmlspecialchars($info['currency_symbol']) . '</div><div class="lbl">Currency</div></div>';
$body .= '</div>';

// CTA
$body .= '<div class="card text-center">';
if (Auth::isLoggedIn()) {
    $body .= '<p>You are logged in. <a href="/wallet">View your wallet</a> or <a href="/pay">send a payment</a>.</p>';
} else {
    $body .= '<p><a href="/register" class="btn btn-primary">Join this node</a> &nbsp; <a href="/login" class="btn btn-secondary">Login</a></p>';
}
$body .= '</div>';

// Node identity
$body .= '<div class="card"><h2>Node Info</h2>';
$body .= '<p><strong>Federation endpoint:</strong> <a href="/.well-known/liquiditypub">/.well-known/liquiditypub</a></p>';
$body .= '</div>';

$body .= '</div>';

echo renderLayout(htmlspecialchars($info['name']), $body);
