<?php
declare(strict_types=1);

$uri    = parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);
$method = $_SERVER['REQUEST_METHOD'];

// ── Admin login ──────────────────────────────────────────────────────────────
$loginErrors = [];

if (!Auth::isAdmin()) {
    if ($method === 'POST' && isset($_POST['admin_password'])) {
        Auth::verifyCsrf();
        if (Auth::checkAdminPassword($_POST['admin_password'])) {
            Auth::loginAdmin();
            header('Location: /admin');
            exit;
        } else {
            $loginErrors[] = 'Invalid admin password.';
        }
    }

    // Show login form
    $csrf = Auth::csrfToken();
    $body = '<div class="container narrow"><div class="card">';
    $body .= '<h1 class="section-title">Admin Login</h1>';
    if (!empty($loginErrors)) {
        $body .= '<div class="alert alert-error">' . htmlspecialchars($loginErrors[0]) . '</div>';
    }
    $body .= '<form method="POST" action="/admin">';
    $body .= '<input type="hidden" name="csrf_token" value="' . htmlspecialchars($csrf) . '">';
    $body .= '<label>Admin Password';
    $body .= '<input type="password" name="admin_password" required autocomplete="current-password">';
    $body .= '</label>';
    $body .= '<button type="submit" class="btn btn-primary btn-lg">Login as Admin →</button>';
    $body .= '</form>';
    $body .= '<p class="text-center mt2"><a href="/login">Member login</a></p>';
    $body .= '</div></div>';
    echo renderLayout('Admin Login', $body);
    exit;
}

// ── Admin actions ────────────────────────────────────────────────────────────
$flash = '';

// Mint to all
if ($method === 'POST' && $uri === '/admin/mint') {
    Auth::verifyCsrf();
    $customAmount = isset($_POST['custom_amount']) && (int)$_POST['custom_amount'] > 0
        ? (int)$_POST['custom_amount']
        : null;

    if ($customAmount !== null) {
        // Override issuance amount temporarily
        $saved = Node::get('issuance_amount');
        Node::set('issuance_amount', (string)$customAmount);
        $count = Issuance::mintToAll('Manual admin mint');
        Node::set('issuance_amount', $saved);
    } else {
        $count = Issuance::mintToAll('Manual admin mint');
    }
    $flash = "Minted to {$count} member(s).";
}

// Toggle member active
if ($method === 'POST' && isset($_ROUTE_PARAMS['member_id'])) {
    Auth::verifyCsrf();
    Member::toggleActive((int)$_ROUTE_PARAMS['member_id']);
    $flash = 'Member status updated.';
}

// ── Admin dashboard ──────────────────────────────────────────────────────────
$members    = Member::all();
$activeCount = count(array_filter($members, fn($m) => $m['active']));
$config     = Node::all();
$sym        = htmlspecialchars(Node::get('currency_symbol', '¤'));
$conservation = Ledger::checkConservation();

$body = '<div class="container wide">';
$body .= '<h1 class="section-title">Admin Panel</h1>';

if ($flash !== '') {
    $body .= '<div class="alert alert-success">' . htmlspecialchars($flash) . '</div>';
}

// Conservation check
$conservationClass = $conservation === 0 ? 'alert-success' : 'alert-error';
$body .= '<div class="alert ' . $conservationClass . '">Ledger conservation check: sum of all entries = ' . $conservation . ' ' . ($conservation === 0 ? '✓ Balanced' : '✗ IMBALANCED') . '</div>';

// Stats
$body .= '<div class="stat-grid">';
$body .= '<div class="stat-box"><div class="val">' . count($members) . '</div><div class="lbl">Total Members</div></div>';
$body .= '<div class="stat-box"><div class="val">' . $activeCount . '</div><div class="lbl">Active Members</div></div>';
$body .= '<div class="stat-box"><div class="val">' . htmlspecialchars($config['issuance_type'] ?? 'manual') . '</div><div class="lbl">Issuance Type</div></div>';
$body .= '<div class="stat-box"><div class="val">' . $sym . number_format((int)($config['issuance_amount'] ?? 0) / 1_000_000, 2) . '</div><div class="lbl">Issuance Amount</div></div>';
$body .= '</div>';

// Manual mint
$csrf = Auth::csrfToken();
$body .= '<div class="card"><h2>Manual Issuance</h2>';
$body .= '<form method="POST" action="/admin/mint" style="display:flex;gap:.75rem;align-items:flex-end;">';
$body .= '<input type="hidden" name="csrf_token" value="' . htmlspecialchars($csrf) . '">';
$body .= '<label style="flex:1;margin:0;">Custom amount (micro-units, blank = use config)<input type="number" name="custom_amount" min="1" placeholder="e.g. 1000000 = 1 credit"></label>';
$body .= '<button type="submit" class="btn btn-primary">Mint to All Active Members</button>';
$body .= '</form></div>';

// Members table
$body .= '<div class="card"><h2>Members</h2>';
$body .= '<table><thead><tr>';
$body .= '<th>Username</th><th>Display Name</th><th>Joined</th><th>Last Dividend</th><th>Balance</th><th>Status</th><th>Actions</th>';
$body .= '</tr></thead><tbody>';

foreach ($members as $m) {
    $walletId = Ledger::walletAccount((int)$m['id']);
    $balance  = Ledger::balance($walletId);
    $statusBadge = $m['active']
        ? '<span class="badge badge-active">active</span>'
        : '<span class="badge badge-inactive">inactive</span>';
    $toggleLabel = $m['active'] ? 'Deactivate' : 'Activate';
    $toggleClass = $m['active'] ? 'btn-danger btn-sm' : 'btn-primary btn-sm';

    $body .= '<tr>';
    $body .= '<td>' . htmlspecialchars($m['username']) . '</td>';
    $body .= '<td>' . htmlspecialchars($m['display_name'] ?? '') . '</td>';
    $body .= '<td>' . htmlspecialchars($m['joined_at']) . '</td>';
    $body .= '<td>' . htmlspecialchars($m['last_dividend_at'] ?? '—') . '</td>';
    $body .= '<td>' . $sym . number_format($balance / 1_000_000, 2) . '</td>';
    $body .= '<td>' . $statusBadge . '</td>';
    $body .= '<td><form method="POST" action="/admin/member/' . (int)$m['id'] . '/toggle" style="display:inline">';
    $body .= '<input type="hidden" name="csrf_token" value="' . htmlspecialchars($csrf) . '">';
    $body .= '<button type="submit" class="btn ' . $toggleClass . '">' . $toggleLabel . '</button>';
    $body .= '</form></td>';
    $body .= '</tr>';
}

$body .= '</tbody></table></div>';

// Node config
$body .= '<div class="card"><h2>Node Configuration</h2><table>';
$hidden = ['private_key', 'admin_password_hash'];
foreach ($config as $k => $v) {
    if (in_array($k, $hidden)) continue;
    $body .= '<tr><th style="width:220px">' . htmlspecialchars($k) . '</th>';
    $body .= '<td><code>' . htmlspecialchars(strlen($v) > 120 ? substr($v, 0, 120) . '…' : $v) . '</code></td></tr>';
}
$body .= '</table></div>';

$body .= '</div>';

echo renderLayout('Admin', $body);
