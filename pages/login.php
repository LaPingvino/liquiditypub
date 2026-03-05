<?php
declare(strict_types=1);

if (Auth::isLoggedIn()) {
    header('Location: /wallet');
    exit;
}

$errors = [];

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    Auth::verifyCsrf();
    $username = trim($_POST['username'] ?? '');
    $password = $_POST['password'] ?? '';

    $member = Member::authenticate($username, $password);
    if ($member === null) {
        $errors[] = 'Invalid username or password.';
    } else {
        Auth::loginMember($member);
        $redirect = $_GET['redirect'] ?? '/wallet';
        header('Location: ' . $redirect);
        exit;
    }
}

$csrf = Auth::csrfToken();

$body = '<div class="container narrow">';
$body .= '<div class="card">';
$body .= '<h1 class="section-title">Member Login</h1>';

if (!empty($errors)) {
    $body .= '<div class="alert alert-error">' . htmlspecialchars($errors[0]) . '</div>';
}

$body .= '<form method="POST" action="/login">';
$body .= '<input type="hidden" name="csrf_token" value="' . htmlspecialchars($csrf) . '">';

$body .= '<label>Username';
$body .= '<input type="text" name="username" required autocomplete="username"';
$body .= ' value="' . htmlspecialchars($_POST['username'] ?? '') . '">';
$body .= '</label>';

$body .= '<label>Password';
$body .= '<input type="password" name="password" required autocomplete="current-password">';
$body .= '</label>';

$body .= '<button type="submit" class="btn btn-primary btn-lg">Login →</button>';
$body .= '</form>';

$body .= '<p class="text-center mt2">No account yet? <a href="/register">Register</a></p>';
$body .= '<p class="text-center mt1"><a href="/admin">Admin login</a></p>';
$body .= '</div></div>';

echo renderLayout('Login', $body);
