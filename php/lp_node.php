<?php
// lp_node.php — the stateful node layer for the PHP implementation. It composes
// the verified core (lp_core: canonical/crypto/pricing/UD) and the ledger
// (lp_ledger) over the flock-guarded snapshot store (lp_store), and exposes the
// operations a running node needs: the read surface (identity doc, checkpoint,
// outbox, log), local issuance (the UD scheduler) and membership, inbound
// envelope validation (§4), and draining the operator action queue.
//
// Federation handlers (contact / transfer / reserve state machines) mirror the
// Go node's node/*.go and are the next slice; their seam is route_inbound(),
// which already validates every envelope the same way the Go node does.

declare(strict_types=1);

namespace lp;

require_once __DIR__ . '/lp_core.php';
require_once __DIR__ . '/lp_ledger.php';
require_once __DIR__ . '/lp_store.php';

class Node
{
    private Store $store;
    /** @var array node config (base, name, currency, transparency, issuance…) */
    private array $cfg;

    public function __construct(Store $store, array $cfg)
    {
        $this->store = $store;
        $this->cfg = $cfg;
    }

    public function store(): Store
    {
        return $this->store;
    }
    public function config(): array
    {
        return $this->cfg;
    }

    // ── ledger view (read-only derivations from a snapshot) ───────────────────

    private function ledgerFrom(array $snap): Ledger
    {
        return Ledger::from_records($snap['ledger'] ?? []);
    }

    /** active_members returns [name => member] for members with Active=true. */
    public static function activeMembers(array $snap): array
    {
        $out = [];
        foreach ($snap['members'] ?? [] as $m) {
            if (!empty($m['Active'])) {
                $out[(string) $m['Name']] = $m;
            }
        }
        return $out;
    }

    public static function weightTotal(array $snap): int
    {
        $t = 0;
        foreach (self::activeMembers($snap) as $m) {
            $t += (int) ($m['Weight'] ?? 0);
        }
        return $t;
    }

    // ── read surface (PROTOCOL §3, §8.3, §9.2) ────────────────────────────────

    /** identityDoc builds the §3 identity document served at the well-known path. */
    public function identityDoc(): array
    {
        $snap = $this->store->load() ?? Store::emptySnapshot();
        $base = (string) $this->cfg['base'];
        $keys = [];
        foreach ($snap['own_keys'] ?? [] as $k) {
            $entry = [
                'id'         => (string) $k['local_id'],
                'alg'        => 'ed25519',
                'public_key' => self::pubFromSeedB64((string) $k['seed']),
                'created'    => (string) ($k['created'] ?? ''),
            ];
            $entry['revoked'] = ($k['revoked'] ?? '') !== '' ? $k['revoked'] : null;
            $keys[] = $entry;
        }
        return [
            'lp'   => '0.2',
            'node' => [
                'base'            => $base,
                'name'            => (string) ($this->cfg['name'] ?? ''),
                'currency_name'   => (string) ($this->cfg['currency_name'] ?? ''),
                'currency_symbol' => (string) ($this->cfg['currency_symbol'] ?? ''),
                'transparency'    => (string) ($this->cfg['transparency'] ?? 'pseudonymous'),
            ],
            'keys'      => $keys,
            'endpoints' => [
                'inbox'      => $base . '/lp/inbox',
                'outbox'     => $base . '/lp/outbox/',
                'checkpoint' => $base . '/lp/checkpoint.json',
                'identity'   => $base . '/.well-known/liquiditypub',
            ],
            'issuance' => [
                'c_period_ppm' => (int) ($this->cfg['c_period_ppm'] ?? 0),
                'ud_period'    => (string) ($this->cfg['ud_period'] ?? 'P1D'),
                'current_ud'   => (int) ($snap['current_ud'] ?? 0),
            ],
        ];
    }

    /** checkpoint builds the §8.3 checkpoint document (unsigned; sign separately). */
    public function checkpoint(): array
    {
        $snap = $this->store->load() ?? Store::emptySnapshot();
        $led = $this->ledgerFrom($snap);
        $contacts = [];
        foreach ($snap['contacts'] ?? [] as $c) {
            $contacts[] = [
                'peer'              => (string) ($c['PeerBase'] ?? ''),
                'contact_id'        => (string) ($c['ID'] ?? ''),
                'peer_reserve_here' => (int) ($c['MyReserveOfPeer'] ?? 0),
                'op_seq'            => (int) ($c['OpSeq'] ?? 0),
                'channel_root'      => self::contactRootB64($c),
            ];
        }
        return [
            'lp'           => '0.2',
            'type'         => 'checkpoint',
            'node'         => (string) $this->cfg['base'],
            'log_seq'      => $led->len(),
            'log_hash'     => $led->head(),
            'money_supply' => $led->money_supply(),
            'member_count' => count($snap['members'] ?? []),
            'current_ud'   => (int) ($snap['current_ud'] ?? 0),
            'contacts'     => $contacts,
        ];
    }

    /** outboxFor returns the ordered envelopes addressed to a peer host (§5.1). */
    public function outboxFor(string $peerHost): array
    {
        $snap = $this->store->load() ?? Store::emptySnapshot();
        $ob = $snap['outbox'] ?? [];
        if (is_array($ob) && isset($ob[$peerHost]) && is_array($ob[$peerHost])) {
            return array_values($ob[$peerHost]);
        }
        return [];
    }

    /** logPage returns a fixed-size page of ledger records (§9.2), newest-agnostic order. */
    public function logPage(int $page): array
    {
        $snap = $this->store->load() ?? Store::emptySnapshot();
        $recs = $snap['ledger'] ?? [];
        $start = $page * LP_PAGE_SIZE;
        return array_slice($recs, $start, LP_PAGE_SIZE);
    }

    // ── issuance (PROTOCOL §10) — the UD scheduler, applied to the real ledger ──

    /**
     * runUD issues one Universal Dividend period to every active member and
     * returns the standard-weight dividend paid. Deterministic member order
     * (sorted) so the log head is reproducible, matching the Go node.
     */
    public function runUD(): int
    {
        return $this->store->transact(function (array &$snap): int {
            $led = Ledger::from_records($snap['ledger'] ?? []);
            $wt = self::weightTotal($snap);
            if ($wt <= 0) {
                return 0;
            }
            $udBase = ud_base($led->money_supply(), (int) ($this->cfg['c_period_ppm'] ?? 0), $wt);
            $genesis = (int) ($this->cfg['genesis_ud'] ?? 0);
            if ($udBase < $genesis) {
                $udBase = $genesis;
            }
            if ($udBase <= 0) {
                return 0;
            }
            $now = gmdate('c');
            $names = array_keys(self::activeMembers($snap));
            sort($names, SORT_STRING);
            foreach ($names as $name) {
                $m = self::activeMembers($snap)[$name];
                $amt = ud_recipient($udBase, (int) $m['Weight']);
                if ($amt <= 0) {
                    continue;
                }
                $led->append([
                    'id' => self::uuid4(), 'type' => 'issuance.ud', 'created' => $now,
                    'entries' => [
                        ['account' => ACCT_MEMBER_PREFIX . $name, 'amount' => $amt],
                        ['account' => ACCT_ISSUANCE, 'amount' => -$amt],
                    ],
                ]);
            }
            $snap['ledger'] = $led->records();
            $snap['current_ud'] = $udBase;
            return $udBase;
        });
    }

    /** addMember admits an account, optionally with a genesis grant. */
    public function addMember(string $name, string $display, int $weightMicro, int $grantMicro): void
    {
        if (!preg_match('/^[a-z0-9_]{1,32}$/', $name)) {
            throw new \InvalidArgumentException('member name must match [a-z0-9_]{1,32}');
        }
        $this->store->transact(function (array &$snap) use ($name, $display, $weightMicro, $grantMicro): void {
            foreach ($snap['members'] ?? [] as $m) {
                if (($m['Name'] ?? '') === $name) {
                    throw new \RuntimeException("member $name already exists");
                }
            }
            $snap['members'][] = [
                'Name' => $name, 'DisplayName' => $display,
                'Weight' => $weightMicro > 0 ? $weightMicro : 1000000, 'Active' => true,
            ];
            if ($grantMicro > 0) {
                $led = Ledger::from_records($snap['ledger'] ?? []);
                $led->append([
                    'id' => self::uuid4(), 'type' => 'issuance.grant', 'created' => gmdate('c'),
                    'entries' => [
                        ['account' => ACCT_MEMBER_PREFIX . $name, 'amount' => $grantMicro],
                        ['account' => ACCT_ISSUANCE, 'amount' => -$grantMicro],
                    ],
                ]);
                $snap['ledger'] = $led->records();
            }
        });
    }

    public function deactivateMember(string $name): void
    {
        $this->store->transact(function (array &$snap) use ($name): void {
            foreach ($snap['members'] as $i => $m) {
                if (($m['Name'] ?? '') === $name) {
                    $snap['members'][$i]['Active'] = false;
                    return;
                }
            }
            throw new \RuntimeException("no such member $name");
        });
    }

    // ── inbound validation (PROTOCOL §4) ──────────────────────────────────────

    /**
     * validateInbound returns a verdict string mirroring conformance
     * ValidateEnvelope, INCLUDING the sig.key↔from binding fix: a key only
     * validates if published by the claimed sender. Signature verification needs
     * the sodium extension; without it, an otherwise-valid envelope yields
     * 'no-sodium' so the caller can surface the missing dependency rather than
     * silently accept.
     *
     * @return string one of ok|unknown-key|bad-signature|duplicate|stale-seq|too-old|future|malformed|no-sodium
     */
    public function validateInbound(array $env, array $snap): string
    {
        $sig = $env['sig'] ?? null;
        if (!is_array($sig)) {
            return 'malformed';
        }
        $keyId = (string) ($sig['key'] ?? '');
        $from = (string) ($env['from'] ?? '');
        $peerKeys = (array) ($snap['peer_keys'] ?? []);
        if (!isset($peerKeys[$keyId]) || !self::keyBoundToSender($keyId, $from)) {
            return 'unknown-key';
        }
        // major version (§14) and addressing (§4)
        $major = explode('.', (string) ($env['lp'] ?? ''))[0] ?? '';
        if ($major !== '0') {
            return 'malformed';
        }
        if (!have_sodium()) {
            return 'no-sodium';
        }
        $pub = b64url_decode((string) $peerKeys[$keyId]);
        $bare = $env;
        unset($bare['sig']);
        $canonicalBytes = self::canonicalEnvelope($bare);
        $rawSig = b64url_decode((string) ($sig['value'] ?? ''));
        if (!verify_detached($canonicalBytes, $rawSig, $pub)) {
            return 'bad-signature';
        }
        $id = (string) ($env['id'] ?? '');
        if ($id === '') {
            return 'malformed';
        }
        $fromHost = self::hostOf($from);
        $inbound = (array) ($snap['inbound'] ?? []);
        $ci = $inbound[$fromHost] ?? ['seen_ids' => [], 'last_seq' => 0];
        if (in_array($id, (array) ($ci['seen_ids'] ?? []), true)) {
            return 'duplicate';
        }
        $seq = (int) ($env['seq'] ?? 0);
        if ($seq <= (int) ($ci['last_seq'] ?? 0)) {
            return 'stale-seq';
        }
        // time window omitted here (needs a clock injection); handled by caller.
        return 'ok';
    }

    // ── operator action queue ─────────────────────────────────────────────────

    /**
     * drainActionQueue applies the *local* operator intents the dashboard queued
     * (run_ud, add_member, deactivate_member) and leaves federation intents
     * (open_contact, send_transfer, adjust_reserve) in place for the transport
     * layer. Returns [applied, deferred, errors].
     */
    public function drainActionQueue(string $queuePath): array
    {
        if (!is_file($queuePath)) {
            return ['applied' => 0, 'deferred' => 0, 'errors' => []];
        }
        $applied = 0;
        $deferred = [];
        $errors = [];
        $lines = file($queuePath, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES);
        foreach ($lines as $line) {
            $it = json_decode($line, true);
            if (!is_array($it)) {
                continue;
            }
            try {
                switch ($it['action'] ?? '') {
                    case 'run_ud':
                        $this->runUD();
                        $applied++;
                        break;
                    case 'add_member':
                        $this->addMember((string) $it['name'], (string) ($it['display_name'] ?? ''),
                            (int) ($it['weight_micro'] ?? 1000000), 0);
                        $applied++;
                        break;
                    case 'deactivate_member':
                        $this->deactivateMember((string) $it['name']);
                        $applied++;
                        break;
                    default:
                        $deferred[] = $it; // needs the federation transport
                }
            } catch (\Throwable $e) {
                $errors[] = ($it['action'] ?? '?') . ': ' . $e->getMessage();
            }
        }
        // Rewrite the queue with only the deferred (federation) intents.
        file_put_contents($queuePath, implode('', array_map(
            fn($x) => json_encode($x, JSON_UNESCAPED_SLASHES) . "\n", $deferred)));
        return ['applied' => $applied, 'deferred' => count($deferred), 'errors' => $errors];
    }

    // ── helpers ───────────────────────────────────────────────────────────────

    public static function keyBoundToSender(string $keyId, string $from): bool
    {
        if ($from === '' || strlen($keyId) <= strlen($from)) {
            return false;
        }
        return strncmp($keyId, $from, strlen($from)) === 0 && $keyId[strlen($from)] === '/';
    }

    public static function hostOf(string $base): string
    {
        $h = parse_url($base, PHP_URL_HOST);
        if (is_string($h) && $h !== '') {
            return $h;
        }
        return preg_replace('#^https?://#', '', $base);
    }

    private static function canonicalEnvelope(array $env): string
    {
        // Rebuild as nested stdClass so canonical() treats maps as objects.
        return canonical(self::toObject($env));
    }

    /** toObject recursively turns associative arrays into stdClass, leaving lists as arrays. */
    private static function toObject($v)
    {
        if (is_array($v)) {
            if ($v === [] || array_is_list($v)) {
                return array_map([self::class, 'toObject'], $v);
            }
            $o = new \stdClass();
            foreach ($v as $k => $vv) {
                $o->{$k} = self::toObject($vv);
            }
            return $o;
        }
        return $v;
    }

    private static function contactRootB64(array $c): string
    {
        $op = (int) ($c['OpSeq'] ?? 0);
        $roots = $c['Roots'] ?? [];
        if (is_array($roots) && isset($roots[$op]) && $roots[$op] !== '') {
            return (string) $roots[$op];
        }
        return '';
    }

    private static function pubFromSeedB64(string $seedB64): string
    {
        if (!have_sodium()) {
            return ''; // cannot derive without sodium; identity doc still lists the key id
        }
        $seed = b64url_decode($seedB64);
        $kp = \sodium_crypto_sign_seed_keypair($seed);
        return b64url(\sodium_crypto_sign_publickey($kp));
    }

    public static function uuid4(): string
    {
        $b = random_bytes(16);
        $b[6] = chr((ord($b[6]) & 0x0f) | 0x40);
        $b[8] = chr((ord($b[8]) & 0x3f) | 0x80);
        return 'urn:uuid:' . vsprintf('%s%s-%s-%s-%s-%s%s%s', str_split(bin2hex($b), 4));
    }
}
