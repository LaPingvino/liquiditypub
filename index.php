<?php
declare(strict_types=1);

// Autoload src/
foreach (glob(__DIR__ . '/src/*.php') as $f) require_once $f;

// Redirect to install if not set up
if (!Database::isInstalled()) {
    header('Location: /install.php');
    exit;
}

// Run periodic issuance check on every request
Issuance::runDue();

// Parse path
$uri    = parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH);
$uri    = rtrim($uri, '/') ?: '/';
$method = $_SERVER['REQUEST_METHOD'];

// ── Router ────────────────────────────────────────────────────────────────────

switch (true) {

    // Public
    case $uri === '/' && $method === 'GET':
        require __DIR__ . '/pages/home.php';
        break;

    case $uri === '/register':
        require __DIR__ . '/pages/register.php';
        break;

    case $uri === '/login':
        require __DIR__ . '/pages/login.php';
        break;

    case $uri === '/logout' && $method === 'GET':
        Auth::logout();
        header('Location: /');
        exit;

    // Member
    case $uri === '/wallet' && $method === 'GET':
        require __DIR__ . '/pages/wallet.php';
        break;

    case $uri === '/pay':
        require __DIR__ . '/pages/pay.php';
        break;

    // Admin
    case $uri === '/admin' || $uri === '/admin/login':
        require __DIR__ . '/pages/admin.php';
        break;

    case $uri === '/admin/mint' && $method === 'POST':
        require __DIR__ . '/pages/admin.php';
        break;

    case preg_match('#^/admin/member/(\d+)/toggle$#', $uri, $m) && $method === 'POST':
        $_ROUTE_PARAMS = ['member_id' => (int)$m[1]];
        require __DIR__ . '/pages/admin.php';
        break;

    // Federation / Well-known
    case $uri === '/.well-known/liquiditypub' && $method === 'GET':
        require __DIR__ . '/api/nodeinfo.php';
        break;

    case $uri === '/api/inbox' && $method === 'POST':
        require __DIR__ . '/api/inbox.php';
        break;

    // 404
    default:
        http_response_code(404);
        echo renderLayout('404 Not Found', '<div class="container"><h1>404</h1><p>Page not found. <a href="/">Go home</a></p></div>');
        break;
}

// ── Layout helper (used by pages) ─────────────────────────────────────────────

function renderLayout(string $title, string $body, bool $withNav = true): string
{
    $nav = '';
    if ($withNav) {
        $sym  = htmlspecialchars(Node::get('currency_symbol', '¤'));
        $name = htmlspecialchars(Node::get('name', 'LiquidityPub'));
        $nav  = '<nav class="topnav"><a href="/" class="brand">' . $sym . ' ' . $name . '</a><div class="nav-links">';
        if (Auth::isLoggedIn()) {
            $nav .= '<a href="/wallet">Wallet</a><a href="/pay">Pay</a>';
            if (Auth::isAdmin()) {
                $nav .= '<a href="/admin">Admin</a>';
            }
            $nav .= '<a href="/logout">Logout</a>';
        } else {
            $nav .= '<a href="/login">Login</a><a href="/register">Register</a>';
            $nav .= '<a href="/admin">Admin</a>';
        }
        $nav .= '</div></nav>';
    }
    return '<!DOCTYPE html><html lang="en"><head>'
        . '<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">'
        . '<title>' . htmlspecialchars($title) . ' — ' . htmlspecialchars(Node::get('name', 'LiquidityPub')) . '</title>'
        . '<link rel="stylesheet" href="/assets/style.css">'
        . '</head><body>'
        . $nav
        . $body
        . '</body></html>';
}
