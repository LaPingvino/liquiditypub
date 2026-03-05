<?php
declare(strict_types=1);

if (Auth::isLoggedIn()) {
    header('Location: /wallet');
    exit;
}

$errors = [];
$success = false;

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    Auth::verifyCsrf();
    $username    = trim($_POST['username'] ?? '');
    $displayName = trim($_POST['display_name'] ?? '');
    $password    = $_POST['password'] ?? '';
    $password2   = $_POST['password2'] ?? '';

    if ($password !== $password2) {
        $errors[] = 'Passwords do not match.';
    } else {
        try {
            $memberId = Member::register($username, $displayName, $password);
            $member   = Member::find($memberId);
            Auth::loginMember($member);
            header('Location: /wallet?welcome=1');
            exit;
        } catch (InvalidArgumentException | RuntimeException $e) {
            $errors[] = $e->getMessage();
        }
    }
}

$csrf = Auth::csrfToken();

$body = '<div class="container narrow">';
$body .= '<div class="card">';
$body .= '<h1 class="section-title">Create Account</h1>';

if (!empty($errors)) {
    $body .= '<div class="alert alert-error"><ul>';
    foreach ($errors as $e) {
        $body .= '<li>' . htmlspecialchars($e) . '</li>';
    }
    $body .= '</ul></div>';
}

$body .= '<form method="POST" action="/register">';
$body .= '<input type="hidden" name="csrf_token" value="' . htmlspecialchars($csrf) . '">';

$body .= '<label>Username <small>(3-32 chars, letters/numbers/underscore)</small>';
$body .= '<input type="text" name="username" required pattern="[a-zA-Z0-9_]{3,32}" autocomplete="username"';
$body .= ' value="' . htmlspecialchars($_POST['username'] ?? '') . '">';
$body .= '</label>';

$body .= '<label>Display Name <small>(optional)</small>';
$body .= '<input type="text" name="display_name" autocomplete="name"';
$body .= ' value="' . htmlspecialchars($_POST['display_name'] ?? '') . '">';
$body .= '</label>';

$body .= '<label>Password <small>(min 6 characters)</small>';
$body .= '<input type="password" name="password" required minlength="6" autocomplete="new-password">';
$body .= '</label>';

$body .= '<label>Confirm Password';
$body .= '<input type="password" name="password2" required minlength="6" autocomplete="new-password">';
$body .= '</label>';

$body .= '<button type="submit" class="btn btn-primary btn-lg">Register →</button>';
$body .= '</form>';

$body .= '<p class="text-center mt2">Already have an account? <a href="/login">Login</a></p>';
$body .= '</div></div>';

echo renderLayout('Register', $body);
