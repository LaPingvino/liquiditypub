<?php
declare(strict_types=1);

class Ledger
{
    /**
     * Get balance for an account in micro-units.
     */
    public static function balance(int $accountId): int
    {
        $pdo = Database::getInstance();
        $stmt = $pdo->prepare(
            'SELECT COALESCE(SUM(amount), 0) AS bal FROM ledger_entries WHERE account_id = ?'
        );
        $stmt->execute([$accountId]);
        return (int)$stmt->fetch()['bal'];
    }

    /**
     * Get wallet account id for a member (creates if missing).
     */
    public static function walletAccount(int $memberId): int
    {
        $pdo = Database::getInstance();
        $stmt = $pdo->prepare(
            "SELECT id FROM accounts WHERE member_id = ? AND account_type = 'wallet' LIMIT 1"
        );
        $stmt->execute([$memberId]);
        $row = $stmt->fetch();
        if ($row !== false) {
            return (int)$row['id'];
        }
        $pdo->prepare(
            "INSERT INTO accounts (member_id, account_type, label) VALUES (?, 'wallet', 'Member Wallet')"
        )->execute([$memberId]);
        return (int)$pdo->lastInsertId();
    }

    /**
     * Record an issuance: debit member wallet, credit issuance_sink.
     * amount is in micro-units.
     */
    public static function recordIssuance(int $memberId, int $amount, string $description = 'UBI dividend'): int
    {
        $pdo = Database::getInstance();
        $walletId = self::walletAccount($memberId);
        $sinkId   = Node::issuanceSinkAccountId();

        $pdo->beginTransaction();
        try {
            $pdo->prepare(
                "INSERT INTO transactions (description, type) VALUES (?, 'issuance')"
            )->execute([$description]);
            $txId = (int)$pdo->lastInsertId();

            // Positive amount into member wallet
            $pdo->prepare(
                'INSERT INTO ledger_entries (transaction_id, account_id, amount) VALUES (?, ?, ?)'
            )->execute([$txId, $walletId, $amount]);

            // Negative amount from issuance sink (counterpart)
            $pdo->prepare(
                'INSERT INTO ledger_entries (transaction_id, account_id, amount) VALUES (?, ?, ?)'
            )->execute([$txId, $sinkId, -$amount]);

            $pdo->commit();
            return $txId;
        } catch (Throwable $e) {
            $pdo->rollBack();
            throw $e;
        }
    }

    /**
     * Record a payment from one member to another.
     * amount is in micro-units.
     */
    public static function recordPayment(int $senderId, int $recipientId, int $amount, string $description = ''): int
    {
        if ($amount <= 0) {
            throw new InvalidArgumentException('Payment amount must be positive');
        }
        $pdo = Database::getInstance();
        $senderWallet    = self::walletAccount($senderId);
        $recipientWallet = self::walletAccount($recipientId);

        $senderBalance = self::balance($senderWallet);
        if ($senderBalance < $amount) {
            throw new RuntimeException('Insufficient balance');
        }

        $pdo->beginTransaction();
        try {
            $pdo->prepare(
                "INSERT INTO transactions (description, type) VALUES (?, 'payment')"
            )->execute([$description]);
            $txId = (int)$pdo->lastInsertId();

            // Debit sender wallet (negative)
            $pdo->prepare(
                'INSERT INTO ledger_entries (transaction_id, account_id, amount) VALUES (?, ?, ?)'
            )->execute([$txId, $senderWallet, -$amount]);

            // Credit recipient wallet (positive)
            $pdo->prepare(
                'INSERT INTO ledger_entries (transaction_id, account_id, amount) VALUES (?, ?, ?)'
            )->execute([$txId, $recipientWallet, $amount]);

            $pdo->commit();
            return $txId;
        } catch (Throwable $e) {
            $pdo->rollBack();
            throw $e;
        }
    }

    /**
     * Get recent transactions for a member's wallet.
     */
    public static function history(int $memberId, int $limit = 20): array
    {
        $pdo      = Database::getInstance();
        $walletId = self::walletAccount($memberId);
        $stmt     = $pdo->prepare(
            'SELECT t.id, t.description, t.type, t.created_at, le.amount
             FROM ledger_entries le
             JOIN transactions t ON t.id = le.transaction_id
             WHERE le.account_id = ?
             ORDER BY t.created_at DESC, t.id DESC
             LIMIT ?'
        );
        $stmt->execute([$walletId, $limit]);
        return $stmt->fetchAll();
    }

    /**
     * Verify conservation: sum of all ledger entries should be zero.
     */
    public static function checkConservation(): int
    {
        $pdo = Database::getInstance();
        $row = $pdo->query('SELECT COALESCE(SUM(amount), 0) AS total FROM ledger_entries')->fetch();
        return (int)$row['total'];
    }
}
