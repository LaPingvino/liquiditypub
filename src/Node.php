<?php
declare(strict_types=1);

class Node
{
    public static function get(string $key, mixed $default = null): mixed
    {
        $pdo = Database::getInstance();
        $stmt = $pdo->prepare('SELECT value FROM node_config WHERE key = ?');
        $stmt->execute([$key]);
        $row = $stmt->fetch();
        return $row !== false ? $row['value'] : $default;
    }

    public static function set(string $key, mixed $value): void
    {
        $pdo = Database::getInstance();
        $stmt = $pdo->prepare(
            'INSERT INTO node_config (key, value) VALUES (?, ?)
             ON CONFLICT(key) DO UPDATE SET value = excluded.value'
        );
        $stmt->execute([$key, (string)$value]);
    }

    public static function setMany(array $data): void
    {
        $pdo = Database::getInstance();
        $stmt = $pdo->prepare(
            'INSERT INTO node_config (key, value) VALUES (?, ?)
             ON CONFLICT(key) DO UPDATE SET value = excluded.value'
        );
        foreach ($data as $key => $value) {
            $stmt->execute([$key, (string)$value]);
        }
    }

    public static function all(): array
    {
        $pdo = Database::getInstance();
        $rows = $pdo->query('SELECT key, value FROM node_config')->fetchAll();
        $result = [];
        foreach ($rows as $row) {
            $result[$row['key']] = $row['value'];
        }
        return $result;
    }

    public static function issuanceSinkAccountId(): int
    {
        $pdo = Database::getInstance();
        $row = $pdo->query(
            "SELECT id FROM accounts WHERE account_type = 'issuance_sink' LIMIT 1"
        )->fetch();
        if ($row === false) {
            throw new RuntimeException('Issuance sink account not found');
        }
        return (int)$row['id'];
    }

    public static function ensureSystemAccounts(): void
    {
        $pdo = Database::getInstance();
        $row = $pdo->query(
            "SELECT id FROM accounts WHERE account_type = 'issuance_sink' LIMIT 1"
        )->fetch();
        if ($row === false) {
            $pdo->exec(
                "INSERT INTO accounts (member_id, account_type, label)
                 VALUES (NULL, 'issuance_sink', 'System Issuance Sink')"
            );
        }
    }

    public static function publicInfo(): array
    {
        return [
            'name'            => self::get('name', 'LiquidityPub Node'),
            'currency_name'   => self::get('currency_name', 'Credits'),
            'currency_symbol' => self::get('currency_symbol', '¤'),
            'description'     => self::get('description', ''),
            'issuance_type'   => self::get('issuance_type', 'manual'),
            'public_key'      => self::get('public_key', ''),
        ];
    }
}
