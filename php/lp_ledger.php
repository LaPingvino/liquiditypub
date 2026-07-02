<?php
// lp_ledger.php — the append-only, double-entry, hash-linked ledger (PROTOCOL
// §9), in PHP. Mirrors node/ledger/ledger.go: every reserve and balance is a
// deterministic function of the ordered transaction history, and the two hard
// invariants — entries sum to zero, and no node:* wallet goes negative — are
// enforced on every append. Record hashing is delegated to lp_core's canonical
// JSON so the on-disk log hashes byte-for-byte as the Go node and the spec
// vectors do.

declare(strict_types=1);

namespace lp;

require_once __DIR__ . '/lp_core.php';

// Account id namespaces (PROTOCOL §9.1).
const ACCT_NODE_PREFIX = 'node:';
const ACCT_MEMBER_PREFIX = 'm:';
const ACCT_ISSUANCE = 'issuance';
const ACCT_TREASURY = 'treasury';

class LedgerError extends \RuntimeException {}

class Ledger
{
    /** @var array<int,array> hash-linked records */
    private array $log = [];
    /** @var array<string,int> derived balances */
    private array $balances = [];

    /**
     * from_records rebuilds a ledger from a persisted record list, verifying the
     * hash chain and conservation before deriving balances — a tampered or
     * truncated log is rejected, so a node never resumes on a corrupt ledger.
     */
    public static function from_records(array $records): self
    {
        $l = new self();
        $l->log = array_values($records);
        $l->verify_chain();
        foreach ($l->log as $rec) {
            foreach ($rec['tx']['entries'] as $e) {
                $acct = (string) $e['account'];
                $l->balances[$acct] = ($l->balances[$acct] ?? 0) + (int) $e['amount'];
            }
        }
        return $l;
    }

    public function balance(string $account): int
    {
        return $this->balances[$account] ?? 0;
    }

    public function head(): string
    {
        if (!$this->log) {
            return '';
        }
        return (string) $this->log[count($this->log) - 1]['hash'];
    }

    public function len(): int
    {
        return count($this->log);
    }

    /** @return array<int,array> */
    public function records(): array
    {
        return $this->log;
    }

    /** money_supply = -(issuance + treasury), matching Ledger.MoneySupply(). */
    public function money_supply(): int
    {
        return -($this->balance(ACCT_ISSUANCE) + $this->balance(ACCT_TREASURY));
    }

    /**
     * append validates and commits a transaction, returning the new record. It
     * enforces conservation (sum=0) and non-negative node wallets (PROTOCOL §8.1),
     * then chains the hash. On any error the ledger is left unchanged.
     *
     * $tx: ['id'=>, 'type'=>, 'created'=>, 'ref'?=>, 'entries'=>[['account'=>,'amount'=>], ...]]
     */
    public function append(array $tx): array
    {
        $entries = $tx['entries'] ?? [];
        if (!$entries) {
            throw new LedgerError('transaction has no entries');
        }
        $sum = 0;
        foreach ($entries as $e) {
            $sum += (int) $e['amount'];
        }
        if ($sum !== 0) {
            throw new LedgerError("entries do not sum to zero: sum=$sum");
        }
        // Non-negativity check is all-or-nothing: project touched node wallets
        // before mutating anything.
        foreach ($entries as $e) {
            $acct = (string) $e['account'];
            if (self::is_node_wallet($acct)
                && ($this->balances[$acct] ?? 0) + (int) $e['amount'] < 0) {
                throw new LedgerError("node wallet would go negative: $acct");
            }
        }
        $seq  = count($this->log) + 1;
        $prev = $this->head();
        $hash = self::record_hash($seq, $prev, $tx);
        // Commit.
        foreach ($entries as $e) {
            $acct = (string) $e['account'];
            $this->balances[$acct] = ($this->balances[$acct] ?? 0) + (int) $e['amount'];
        }
        $rec = ['seq' => $seq, 'prev' => $prev, 'tx' => $tx, 'member_sig' => null, 'hash' => $hash];
        $this->log[] = $rec;
        return $rec;
    }

    /**
     * verify_chain recomputes every hash link and the conservation invariant
     * across the whole log — the self-audit used on load.
     */
    public function verify_chain(): void
    {
        $running = [];
        $prev = '';
        foreach ($this->log as $i => $rec) {
            if ((int) $rec['seq'] !== $i + 1) {
                throw new LedgerError("record $i has seq {$rec['seq']}");
            }
            if ((string) $rec['prev'] !== $prev) {
                throw new LedgerError("record {$rec['seq']} prev mismatch");
            }
            $sum = 0;
            foreach ($rec['tx']['entries'] as $e) {
                $acct = (string) $e['account'];
                $sum += (int) $e['amount'];
                $running[$acct] = ($running[$acct] ?? 0) + (int) $e['amount'];
                if (self::is_node_wallet($acct) && $running[$acct] < 0) {
                    throw new LedgerError("record {$rec['seq']} drove $acct negative");
                }
            }
            if ($sum !== 0) {
                throw new LedgerError("record {$rec['seq']} unbalanced (sum=$sum)");
            }
            $want = (string) $rec['hash'];
            $got = self::record_hash((int) $rec['seq'], $prev, $rec['tx']);
            if (!hash_equals($want, $got)) {
                throw new LedgerError("record {$rec['seq']} hash mismatch");
            }
            $prev = $want;
        }
    }

    public static function is_node_wallet(string $account): bool
    {
        return strncmp($account, ACCT_NODE_PREFIX, strlen(ACCT_NODE_PREFIX)) === 0;
    }

    /**
     * record_hash is base64url(SHA-256(JCS({seq,prev,tx,member_sig}))) (PROTOCOL
     * §9.2), with tx = {id,type,created,[ref],entries:[{account,amount}]}.
     * member_sig is null in this reference model.
     */
    public static function record_hash(int $seq, string $prev, array $tx): string
    {
        $txo = new \stdClass();
        $txo->id      = (string) $tx['id'];
        $txo->type    = (string) $tx['type'];
        $txo->created = (string) $tx['created'];
        if (isset($tx['ref']) && $tx['ref'] !== '') {
            $txo->ref = (string) $tx['ref'];
        }
        $entries = [];
        foreach ($tx['entries'] as $e) {
            $eo = new \stdClass();
            $eo->account = (string) $e['account'];
            $eo->amount  = (int) $e['amount'];
            $entries[] = $eo;
        }
        $txo->entries = $entries;

        $rec = new \stdClass();
        $rec->seq        = $seq;
        $rec->prev       = $prev;
        $rec->tx         = $txo;
        $rec->member_sig = null;

        return b64url(sha256_raw(canonical($rec)));
    }
}
