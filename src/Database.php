<?php
declare(strict_types=1);

class Database
{
    private static ?PDO $instance = null;
    private static string $dbPath = __DIR__ . '/../db/liquiditypub.sqlite';

    public static function getInstance(): PDO
    {
        if (self::$instance === null) {
            $dir = dirname(self::$dbPath);
            if (!is_dir($dir)) {
                mkdir($dir, 0755, true);
            }
            $pdo = new PDO('sqlite:' . self::$dbPath);
            $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
            $pdo->setAttribute(PDO::ATTR_DEFAULT_FETCH_MODE, PDO::FETCH_ASSOC);
            $pdo->exec('PRAGMA journal_mode=WAL');
            $pdo->exec('PRAGMA foreign_keys=ON');
            self::$instance = $pdo;
            self::migrate($pdo);
        }
        return self::$instance;
    }

    private static function migrate(PDO $pdo): void
    {
        $schema = file_get_contents(__DIR__ . '/../db/schema.sql');
        // Split on semicolons and execute each statement
        $statements = array_filter(
            array_map('trim', explode(';', $schema)),
            fn($s) => $s !== '' && !str_starts_with(ltrim($s), '--')
        );
        foreach ($statements as $sql) {
            if (trim($sql) !== '') {
                $pdo->exec($sql);
            }
        }
    }

    public static function isInstalled(): bool
    {
        if (!file_exists(self::$dbPath)) {
            return false;
        }
        try {
            $pdo = self::getInstance();
            $row = $pdo->query("SELECT value FROM node_config WHERE key='installed_at'")->fetch();
            return $row !== false;
        } catch (Exception) {
            return false;
        }
    }
}
