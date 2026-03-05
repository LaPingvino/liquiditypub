<?php
declare(strict_types=1);

$memberId = Auth::requireMember();
$member   = Member::find($memberId);
if ($member === null) {
    Auth::logout();
    header('Location: /login');
    exit;
}

$walletId = Ledger::walletAccount($memberId);
$balance  = Ledger::balance($walletId);
$history  = Ledger::history($memberId, 30);

$sym = htmlspecialchars(Node::get('currency_symbol', '¤'));
$currencyName = htmlspecialchars(Node::get('currency_name', 'Credits'));

// Format micro-units to display value (divide by 1,000,000)
$formatAmount = function(int $amount) use ($sym): string {
    $val = $amount / 1_000_000;
    return ($amount >= 0 ? '+' : '') . $sym . number_format($val, 2);
};

$formatBalance = function(int $amount) use ($sym): string {
    return $sym . number_format($amount / 1_000_000, 2);
};

$body = '<div class="container">';

// Welcome flash
if (isset($_GET['welcome'])) {
    $body .= '<div class="alert alert-success">Welcome! Your wallet is ready.</div>';
}

// Balance
$body .= '<div class="balance-card">';
$body .= '<div class="amount">' . $formatBalance($balance) . '</div>';
$body .= '<div class="label">' . $currencyName . ' balance &middot; ' . htmlspecialchars($member['display_name'] ?? $member['username']) . '</div>';
$body .= '</div>';

// Quick actions
$body .= '<div style="display:flex;gap:.75rem;margin-bottom:1.5rem;">';
$body .= '<a href="/pay" class="btn btn-primary">Send Payment</a>';
$body .= '</div>';

// Transaction history
$body .= '<div class="card">';
$body .= '<h2>Transaction History</h2>';
if (empty($history)) {
    $body .= '<p class="text-muted">No transactions yet. Ask the admin to run an issuance, or receive a payment.</p>';
} else {
    $body .= '<ul class="tx-list">';
    foreach ($history as $tx) {
        $amount    = (int)$tx['amount'];
        $amountStr = $formatAmount($amount);
        $cls       = $amount >= 0 ? 'positive' : 'negative';
        $desc      = htmlspecialchars($tx['description'] ?: ucfirst($tx['type']));
        $date      = htmlspecialchars($tx['created_at']);
        $body .= "<li><div class='tx-meta'><div class='desc'>{$desc}</div><div class='date'>{$date}</div></div>"
               . "<div class='tx-amount {$cls}'>{$amountStr}</div></li>";
    }
    $body .= '</ul>';
}
$body .= '</div>';

$body .= '</div>';

echo renderLayout('Wallet', $body);
