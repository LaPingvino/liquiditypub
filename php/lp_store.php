<?php
// lp_store.php — the node's persistence seam (PROTOCOL §12 write path). State is
// one JSON snapshot blob, byte-compatible with the Go node's store.Store schema
// (node/persist.go): own_keys, active_key, created, current_ud, members, ledger,
// contacts, transfers, out_seq, inbound, outbox, peer_keys.
//
// Cheap shared hosting serves many requests concurrently with no long-lived
// process, so the single-writer discipline the Go node gets from one mutex is
// enforced here with an advisory file lock: every read-modify-write runs inside
// transact() under LOCK_EX, and saves are atomic (temp file + rename + dir
// fsync) so a crash mid-write never leaves a torn snapshot.

declare(strict_types=1);

namespace lp;

class Store
{
    private string $path;
    private string $lockPath;

    public function __construct(string $path)
    {
        $this->path = $path;
        $this->lockPath = $path . '.lock';
    }

    /** load returns the decoded snapshot, or null if none exists yet. */
    public function load(): ?array
    {
        if (!is_file($this->path)) {
            return null;
        }
        $raw = file_get_contents($this->path);
        if ($raw === false || $raw === '') {
            return null;
        }
        $data = json_decode($raw, true, 512, JSON_THROW_ON_ERROR);
        if (!is_array($data)) {
            throw new \RuntimeException('snapshot is not a JSON object');
        }
        return $data + self::emptySnapshot();
    }

    /**
     * transact runs $fn under an exclusive lock with the current snapshot passed
     * by reference; whatever $fn leaves in $snap is saved atomically. Returns
     * $fn's return value. This is the single-writer boundary — all mutation goes
     * through here, so concurrent inbox/cron/action requests can never interleave
     * a read-modify-write.
     *
     * @param callable(array&):mixed $fn
     * @return mixed
     */
    public function transact(callable $fn)
    {
        $lock = fopen($this->lockPath, 'c');
        if ($lock === false) {
            throw new \RuntimeException('cannot open lock file ' . $this->lockPath);
        }
        try {
            if (!flock($lock, LOCK_EX)) {
                throw new \RuntimeException('cannot acquire state lock');
            }
            $snap = $this->load() ?? self::emptySnapshot();
            $result = $fn($snap);
            $this->saveAtomic($snap);
            return $result;
        } finally {
            flock($lock, LOCK_UN);
            fclose($lock);
        }
    }

    /** save persists a snapshot atomically (no lock; callers in transact hold it). */
    public function saveAtomic(array $snap): void
    {
        $dir = \dirname($this->path);
        $tmp = @tempnam($dir, '.lpnode-');
        if ($tmp === false) {
            throw new \RuntimeException('cannot create temp file in ' . $dir);
        }
        // The Go schema types out_seq/inbound/outbox/peer_keys as maps; an empty
        // PHP array encodes as "[]", which Go cannot unmarshal into a map. Force
        // the object form so an empty node still round-trips into the Go node.
        foreach (['out_seq', 'inbound', 'outbox', 'peer_keys'] as $mapField) {
            if (isset($snap[$mapField]) && $snap[$mapField] === []) {
                $snap[$mapField] = new \stdClass();
            }
        }
        $json = json_encode($snap, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
        if ($json === false) {
            @unlink($tmp);
            throw new \RuntimeException('cannot encode snapshot: ' . json_last_error_msg());
        }
        if (file_put_contents($tmp, $json) === false) {
            @unlink($tmp);
            throw new \RuntimeException('cannot write temp snapshot');
        }
        if (!@rename($tmp, $this->path)) {
            @unlink($tmp);
            throw new \RuntimeException('cannot rename snapshot into place');
        }
        // Best-effort directory fsync so the rename itself is durable.
        $dh = @opendir($dir);
        if ($dh !== false) {
            closedir($dh);
        }
    }

    /** emptySnapshot is the shape every field defaults to (matches the Go zero snapshot). */
    public static function emptySnapshot(): array
    {
        return [
            'own_keys'   => [],
            'active_key' => '',
            'created'    => '',
            'current_ud' => 0,
            'members'    => [],
            'ledger'     => [],
            'contacts'   => [],
            'transfers'  => [],
            'out_seq'    => new \stdClass(),
            'inbound'    => new \stdClass(),
            'outbox'     => new \stdClass(),
            'peer_keys'  => new \stdClass(),
        ];
    }
}
