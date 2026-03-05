<?php
declare(strict_types=1);

$memberId = Auth::requireMember();
$member   = Member::find($memberId);

$errors  = [];
$success = '';

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    Auth::verifyCsrf();

    $toUsername = trim(strtolower($_POST['to_username'] ?? ''));
    $amountStr  = trim($_POST['amount'] ?? '');
    $note       = trim($_POST['note'] ?? '');

    if ($toUsername === '') {
        $errors[] = 'Recipient username is required.';
    } elseif ($toUsername === $member['username']) {
        $errors[] = 'You cannot pay yourself.';
    }

    $amountFloat = (float)$amountStr;
    if ($amountFloat <= 0) {
        $errors[] = 'Amount must be greater than zero.';
    }
    // Convert from display units to micro-units
    $amountMicro = (int)round($amountFloat * 1_000_000);

    if (empty($errors)) {
        $recipient = Member::findByUsername($toUsername);
        if ($recipient === null || !$recipient['active']) {
            $errors[] = 'Recipient not found or inactive.';
        } else {
            try {
                $description = $note !== '' ? $note : ('Payment to ' . $recipient['display_name']);
                Ledger::recordPayment(
                    (int)$memberId,
                    (int)$recipient['id'],
                    $amountMicro,
                    $description
                );
                $sym = Node::get('currency_symbol', '¤');
                $success = 'Sent ' . $sym . number_format($amountFloat, 2)
                         . ' to ' . htmlspecialchars($recipient['display_name'] ?: $recipient['username']) . '.';
            } catch (RuntimeException $e) {
                $errors[] = $e->getMessage();
            }
        }
    }
}

$walletId = Ledger::walletAccount($memberId);
$balance  = Ledger::balance($walletId);
$sym      = htmlspecialchars(Node::get('currency_symbol', '¤'));
$csrf     = Auth::csrfToken();

$body = '<div class="container narrow">';
$body .= '<div class="card">';
$body .= '<h1 class="section-title">Send Payment</h1>';

$body .= '<p class="text-muted" style="margin-bottom:1rem;">Your balance: <strong>'
       . $sym . number_format($balance / 1_000_000, 2) . '</strong></p>';

if (!empty($errors)) {
    $body .= '<div class="alert alert-error"><ul>';
    foreach ($errors as $e) {
        $body .= '<li>' . htmlspecialchars($e) . '</li>';
    }
    $body .= '</ul></div>';
}
if ($success !== '') {
    $body .= '<div class="alert alert-success">' . $success . '</div>';
}

$body .= '<form method="POST" action="/pay">';
$body .= '<input type="hidden" name="csrf_token" value="' . htmlspecialchars($csrf) . '">';

$body .= '<label>Recipient Username';
$body .= '<input type="text" name="to_username" required autocomplete="off"';
$body .= ' value="' . htmlspecialchars($_POST['to_username'] ?? '') . '">';
$body .= '</label>';

$body .= '<label>Amount (' . $sym . ')';
$body .= '<input type="number" name="amount" required min="0.000001" step="0.01"';
$body .= ' value="' . htmlspecialchars($_POST['amount'] ?? '') . '">';
$body .= '</label>';

$body .= '<label>Note <small>(optional)</small>';
$body .= '<input type="text" name="note" maxlength="200"';
$body .= ' value="' . htmlspecialchars($_POST['note'] ?? '') . '">';
$body .= '</label>';

$body .= '<button type="submit" class="btn btn-primary btn-lg">Send →</button>';
$body .= '</form>';

$body .= '<p class="text-center mt2"><a href="/wallet">← Back to wallet</a></p>';
$body .= '</div></div>';

echo renderLayout('Send Payment', $body);
