<?php
declare(strict_types=1);

class Member
{
    public static function register(string $username, string $displayName, string $password): int
    {
        $username    = trim(strtolower($username));
        $displayName = trim($displayName);

        if (!preg_match('/^[a-z0-9_]{3,32}$/', $username)) {
            throw new InvalidArgumentException('Username must be 3-32 characters: letters, numbers, underscore');
        }
        if (strlen($password) < 6) {
            throw new InvalidArgumentException('Password must be at least 6 characters');
        }

        $pdo  = Database::getInstance();
        $hash = password_hash($password, PASSWORD_BCRYPT);

        try {
            $pdo->prepare(
                'INSERT INTO members (username, display_name, password_hash) VALUES (?, ?, ?)'
            )->execute([$username, $displayName ?: $username, $hash]);
        } catch (PDOException $e) {
            if (str_contains($e->getMessage(), 'UNIQUE')) {
                throw new RuntimeException('Username already taken');
            }
            throw $e;
        }

        $memberId = (int)$pdo->lastInsertId();
        // Ensure wallet account exists
        Ledger::walletAccount($memberId);
        return $memberId;
    }

    public static function authenticate(string $username, string $password): ?array
    {
        $username = trim(strtolower($username));
        $pdo      = Database::getInstance();
        $stmt     = $pdo->prepare('SELECT * FROM members WHERE username = ? AND active = 1');
        $stmt->execute([$username]);
        $member = $stmt->fetch();
        if ($member === false) {
            return null;
        }
        if (!password_verify($password, $member['password_hash'])) {
            return null;
        }
        return $member;
    }

    public static function find(int $id): ?array
    {
        $pdo  = Database::getInstance();
        $stmt = $pdo->prepare('SELECT * FROM members WHERE id = ?');
        $stmt->execute([$id]);
        $row = $stmt->fetch();
        return $row !== false ? $row : null;
    }

    public static function findByUsername(string $username): ?array
    {
        $pdo  = Database::getInstance();
        $stmt = $pdo->prepare('SELECT * FROM members WHERE username = ?');
        $stmt->execute([trim(strtolower($username))]);
        $row = $stmt->fetch();
        return $row !== false ? $row : null;
    }

    public static function all(): array
    {
        $pdo = Database::getInstance();
        return $pdo->query(
            'SELECT id, username, display_name, joined_at, last_dividend_at, active FROM members ORDER BY joined_at'
        )->fetchAll();
    }

    public static function allActive(): array
    {
        $pdo = Database::getInstance();
        return $pdo->query(
            'SELECT * FROM members WHERE active = 1 ORDER BY username'
        )->fetchAll();
    }

    public static function toggleActive(int $id): void
    {
        $pdo = Database::getInstance();
        $pdo->prepare('UPDATE members SET active = 1 - active WHERE id = ?')->execute([$id]);
    }

    public static function updateLastDividend(int $id): void
    {
        $pdo = Database::getInstance();
        $pdo->prepare(
            "UPDATE members SET last_dividend_at = CURRENT_TIMESTAMP WHERE id = ?"
        )->execute([$id]);
    }
}
