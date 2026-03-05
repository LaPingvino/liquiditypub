<?php
declare(strict_types=1);

class Issuance
{
    /**
     * Called on every page load.
     * If periodic issuance is due, mints to all active members.
     */
    public static function runDue(): void
    {
        $type = Node::get('issuance_type', 'manual');
        if ($type !== 'periodic') {
            return;
        }

        $intervalHours = (float)(Node::get('issuance_interval_hours', '24'));
        if ($intervalHours <= 0) {
            return;
        }

        $lastRun = Node::get('last_issuance_run', '');
        if ($lastRun !== '') {
            $lastRunTs = strtotime($lastRun);
            $nextRunTs = $lastRunTs + (int)($intervalHours * 3600);
            if (time() < $nextRunTs) {
                return;
            }
        }

        self::mintToAll();
    }

    /**
     * Mint issuance_amount to every active member.
     * Returns number of members minted to.
     */
    public static function mintToAll(string $description = ''): int
    {
        $amount  = (int)(Node::get('issuance_amount', '0'));
        if ($amount <= 0) {
            return 0;
        }
        if ($description === '') {
            $description = 'Periodic dividend';
        }

        $members = Member::allActive();
        $count   = 0;
        foreach ($members as $m) {
            self::mintToMember((int)$m['id'], $amount, $description);
            $count++;
        }

        Node::set('last_issuance_run', date('Y-m-d H:i:s'));
        return $count;
    }

    /**
     * Mint a specific amount to a single member.
     */
    public static function mintToMember(int $memberId, int $amount, string $description = 'Manual mint'): void
    {
        if ($amount <= 0) {
            throw new InvalidArgumentException('Mint amount must be positive');
        }
        Ledger::recordIssuance($memberId, $amount, $description);
        Member::updateLastDividend($memberId);
    }
}
