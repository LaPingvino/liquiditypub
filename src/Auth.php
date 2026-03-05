<?php
declare(strict_types=1);

class Auth
{
    public static function start(): void
    {
        if (session_status() === PHP_SESSION_NONE) {
            session_start();
        }
    }

    public static function loginMember(array $member): void
    {
        self::start();
        session_regenerate_id(true);
        $_SESSION['member_id']   = $member['id'];
        $_SESSION['username']    = $member['username'];
        $_SESSION['is_admin']    = false;
    }

    public static function loginAdmin(): void
    {
        self::start();
        session_regenerate_id(true);
        $_SESSION['is_admin'] = true;
        // Admin may also be a member if they logged in as one first
    }

    public static function logout(): void
    {
        self::start();
        session_destroy();
    }

    public static function isLoggedIn(): bool
    {
        self::start();
        return isset($_SESSION['member_id']);
    }

    public static function isAdmin(): bool
    {
        self::start();
        return !empty($_SESSION['is_admin']);
    }

    public static function memberId(): ?int
    {
        self::start();
        return isset($_SESSION['member_id']) ? (int)$_SESSION['member_id'] : null;
    }

    public static function requireMember(): int
    {
        if (!self::isLoggedIn()) {
            header('Location: /login');
            exit;
        }
        return self::memberId();
    }

    public static function requireAdmin(): void
    {
        if (!self::isAdmin()) {
            header('Location: /admin/login');
            exit;
        }
    }

    public static function checkAdminPassword(string $password): bool
    {
        $hash = Node::get('admin_password_hash', '');
        return $hash !== '' && password_verify($password, $hash);
    }

    public static function csrfToken(): string
    {
        self::start();
        if (empty($_SESSION['csrf_token'])) {
            $_SESSION['csrf_token'] = bin2hex(random_bytes(16));
        }
        return $_SESSION['csrf_token'];
    }

    public static function verifyCsrf(): void
    {
        $token = $_POST['csrf_token'] ?? '';
        if (!hash_equals(self::csrfToken(), $token)) {
            http_response_code(403);
            die('Invalid CSRF token');
        }
    }
}
