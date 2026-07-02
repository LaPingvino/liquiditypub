<?php
// lp_federation.php — the contact / transfer / reserve state machines for the
// PHP node (PROTOCOL §6, §7, §8), ported faithfully from the Go node
// (node/contact.go, node/transfer.go, node/reserve.go, node/handlers.go).
//
// This is a trait mixed into Node. All protocol state lives in the snapshot
// (contacts, transfers, outbox, out_seq, inbound), so every mutation runs inside
// store->transact() — the single-writer boundary. Signing/verifying is isolated
// in buildSigned()/process_inbound(); the money logic (reserves, channel roots,
// op_seq, ledger legs) is independent of it and fully exercised by the in-process
// two-node test even where the sodium extension is absent.

declare(strict_types=1);

namespace lp;

const LP_IDENTITY_PATH = '/.well-known/liquiditypub';

// Transfer state machine (PROTOCOL §7.1), identical table to conformance/statemachine.go.
const LP_TRANSITIONS = [
    'NONE'      => ['propose' => 'PROPOSED'],
    'PROPOSED'  => ['accept' => 'ACCEPTED', 'reject' => 'REJECTED', 'abort' => 'ABORTED', 'expire' => 'EXPIRED'],
    'ACCEPTED'  => ['commit' => 'COMMITTED', 'abort' => 'ABORTED', 'expire' => 'EXPIRED'],
    'COMMITTED' => ['receipt' => 'SETTLED', 'commit' => 'COMMITTED'],
    'SETTLED'   => ['commit' => 'SETTLED'],
];

trait FederationTrait
{
    // ── state-machine helper ─────────────────────────────────────────────────

    private static function transition(string $state, string $event): string
    {
        if (isset(LP_TRANSITIONS[$state][$event])) {
            return LP_TRANSITIONS[$state][$event];
        }
        throw new \RuntimeException("invalid transition: $state + $event");
    }

    // ── reference helpers into the snapshot ──────────────────────────────────

    private static function &contactById(array &$snap, string $id)
    {
        foreach ($snap['contacts'] as $i => &$c) {
            if (($c['ID'] ?? '') === $id) {
                return $c;
            }
        }
        $null = null;
        return $null;
    }

    private static function &contactByHost(array &$snap, string $host)
    {
        foreach ($snap['contacts'] as $i => &$c) {
            if (($c['PeerHost'] ?? '') === $host) {
                return $c;
            }
        }
        $null = null;
        return $null;
    }

    private static function &transferById(array &$snap, string $id)
    {
        foreach ($snap['transfers'] as $i => &$t) {
            if (($t['ID'] ?? '') === $id) {
                return $t;
            }
        }
        $null = null;
        return $null;
    }

    // ── channel folding (PROTOCOL §8.2) ──────────────────────────────────────

    private static function applySeed(array &$c): void
    {
        $root0 = channel_root0((string) $c['ID']);
        $next = channel_next($root0, 'seed', (string) $c['ID'], (int) $c['ProposerSeed'], (int) $c['ResponderSeed']);
        $c['OpSeq'] = 0;
        $c['Roots'] = [b64url($next)];
    }

    private static function applyOp(array &$c, string $opType, string $opId, int $src, int $dst): void
    {
        $prev = b64url_decode((string) $c['Roots'][(int) $c['OpSeq']]);
        $next = channel_next($prev, $opType, $opId, $src, $dst);
        $c['OpSeq'] = (int) $c['OpSeq'] + 1;
        $c['Roots'][(int) $c['OpSeq']] = b64url($next);
    }

    // ── pricing (PROTOCOL §6.2) ──────────────────────────────────────────────

    private static function priceOutgoing(array $c, int $src): int
    {
        return pool_price((int) $c['MyReserveOfPeer'], (int) $c['PeerReserveOfMe'], $src);
    }
    private static function priceIncoming(array $c, int $src): int
    {
        return pool_price((int) $c['PeerReserveOfMe'], (int) $c['MyReserveOfPeer'], $src);
    }

    // ── envelope building + signing ──────────────────────────────────────────

    private function buildSigned(array &$snap, string $type, string $toBase, string $re, array $payload): array
    {
        $toHost = self::hostOf($toBase);
        $seq = ((int) (($snap['out_seq'][$toHost] ?? 0))) + 1;
        $snap['out_seq'][$toHost] = $seq;
        $env = [
            'lp'      => '0.2',
            'id'      => self::uuid4(),
            'type'    => $type,
            'from'    => (string) $this->cfg['base'],
            'to'      => $toBase,
            'seq'     => $seq,
            'created' => gmdate('c'),
            're'      => $re !== '' ? $re : null,
            'payload' => $payload,
        ];
        $keyId = (string) $this->cfg['base'] . LP_IDENTITY_PATH . (string) ($snap['active_key'] ?? '#nk1');
        $value = '';
        if (have_sodium()) {
            $seed = $this->activeSeed($snap);
            if ($seed !== null) {
                $value = sign_envelope(self::toObject($env), $seed);
            }
        }
        $env['sig'] = ['key' => $keyId, 'alg' => 'ed25519', 'value' => $value];
        // Record in the peer's outbox for the pull binding (§5.1).
        $snap['outbox'][$toHost][] = $env;
        return $env;
    }

    private function activeSeed(array $snap): ?string
    {
        foreach ($snap['own_keys'] ?? [] as $k) {
            if (($k['local_id'] ?? '') === ($snap['active_key'] ?? '')) {
                return b64url_decode((string) $k['seed']);
            }
        }
        return null;
    }

    // ── inbound entry point ──────────────────────────────────────────────────

    /**
     * processInbound validates an envelope and, if valid, routes it under the
     * write lock, returning ['verdict'=>, 'reply'=>?env]. In $trust mode (the
     * in-process test transport, or where sodium is unavailable) signature
     * verification is skipped but every other §4 check still runs.
     */
    public function processInbound(array $env, bool $trust = false): array
    {
        $snapForCheck = $this->store->load() ?? Store::emptySnapshot();
        $verdict = $this->validateInbound($env, $snapForCheck, $trust);
        // On an unknown key that is nonetheless bound to `from`, fetch the
        // sender's identity document once and retry (§3 TOFU) — this is what lets
        // a passive push-only node verify a peer it has not polled yet.
        if ($verdict === 'unknown-key' && !$trust && $this->transport !== null) {
            $from = (string) ($env['from'] ?? '');
            $keyId = (string) (($env['sig']['key'] ?? '') ?: '');
            if ($from !== '' && self::keyBoundToSender($keyId, $from)) {
                $doc = $this->transport->fetchIdentity($from);
                if ($doc !== null) {
                    $this->registerPeerFromDoc($doc);
                    $snapForCheck = $this->store->load() ?? Store::emptySnapshot();
                    $verdict = $this->validateInbound($env, $snapForCheck, $trust);
                }
            }
        }
        if ($verdict === 'duplicate') {
            $fromHost = self::hostOf((string) ($env['from'] ?? ''));
            $reply = $snapForCheck['inbound'][$fromHost]['replies'][(string) $env['id']] ?? null;
            return ['verdict' => 'duplicate', 'reply' => $reply];
        }
        if ($verdict !== 'ok') {
            return ['verdict' => $verdict, 'reply' => null];
        }
        $reply = $this->store->transact(function (array &$snap) use ($env): ?array {
            $reply = $this->route($snap, $env);
            $fromHost = self::hostOf((string) $env['from']);
            $snap['inbound'][$fromHost]['seen_ids'][] = (string) $env['id'];
            $seq = (int) ($env['seq'] ?? 0);
            if ($seq > (int) ($snap['inbound'][$fromHost]['last_seq'] ?? 0)) {
                $snap['inbound'][$fromHost]['last_seq'] = $seq;
            }
            if ($reply !== null) {
                $snap['inbound'][$fromHost]['replies'][(string) $env['id']] = $reply;
            }
            return $reply;
        });
        return ['verdict' => 'ok', 'reply' => $reply];
    }

    private function route(array &$snap, array $env): ?array
    {
        switch ((string) ($env['type'] ?? '')) {
            case 'contact.propose':  return $this->hContactPropose($snap, $env);
            case 'contact.accept':   return $this->hContactAccept($snap, $env);
            case 'transfer.propose': return $this->hTransferPropose($snap, $env);
            case 'transfer.accept':  return $this->hTransferAccept($snap, $env);
            case 'transfer.commit':  return $this->hTransferCommit($snap, $env);
            case 'transfer.receipt': return $this->hTransferReceipt($snap, $env);
            case 'transfer.reject':  return $this->hTransferReject($snap, $env);
            case 'transfer.abort':   return $this->hTransferAbort($snap, $env);
            case 'reserve.adjust':   return $this->hReserveAdjust($snap, $env);
            case 'reserve.accept':   return $this->hReserveAccept($snap, $env);
            case 'contact.close':    return $this->hContactClose($snap, $env);
            case 'contact.update':   return $this->hContactUpdate($snap, $env);
            case 'key.announce':     return $this->hKeyAnnounce($snap, $env);
            case 'member.lookup':    return $this->hMemberLookup($snap, $env);
            case 'member.result':    return null;
            case 'ping':             return $this->buildSigned($snap, 'pong', (string) $env['from'], (string) $env['id'], []);
            case 'pong':             return null;
            default:                 return $this->errorReply($snap, $env, 'unknown-type', (string) ($env['type'] ?? ''));
        }
    }

    private function errorReply(array &$snap, array $env, string $code, string $detail): array
    {
        return $this->buildSigned($snap, 'error', (string) $env['from'], (string) ($env['id'] ?? ''),
            ['code' => $code, 'detail' => $detail]);
    }

    private static function ledgerLegs(array &$snap, string $type, string $ref, array $entries): void
    {
        $led = Ledger::from_records($snap['ledger'] ?? []);
        $tx = ['id' => self::uuid4(), 'type' => $type, 'created' => gmdate('c'), 'entries' => $entries];
        if ($ref !== '') {
            $tx['ref'] = $ref;
        }
        $led->append($tx);
        $snap['ledger'] = $led->records();
    }

    // ── contacts (PROTOCOL §6) ───────────────────────────────────────────────

    public function openContact(string $peerBase, int $mySeed, string $note = ''): array
    {
        if ($mySeed <= 0) {
            throw new \InvalidArgumentException('seed must be positive');
        }
        return $this->store->transact(function (array &$snap) use ($peerBase, $mySeed, $note): array {
            $host = self::hostOf($peerBase);
            $existing = &self::contactByHost($snap, $host);
            if ($existing !== null) {
                throw new \RuntimeException("contact with $host already exists");
            }
            unset($existing);
            $id = self::uuid4();
            $snap['contacts'][] = [
                'ID' => $id, 'PeerBase' => $peerBase, 'PeerHost' => $host,
                'IAmProposer' => true, 'Active' => false, 'Closed' => false,
                'ProposerSeed' => $mySeed, 'ResponderSeed' => 0,
                'MyReserveOfPeer' => 0, 'PeerReserveOfMe' => 0, 'OpSeq' => 0, 'Roots' => [],
            ];
            return $this->buildSigned($snap, 'contact.propose', $peerBase, '',
                ['contact_id' => $id, 'my_seed' => $mySeed, 'note' => $note]);
        });
    }

    private function hContactPropose(array &$snap, array $env): array
    {
        $p = (array) ($env['payload'] ?? []);
        $responderSeed = (int) ($this->cfg['auto_accept_seed'] ?? 0);
        if ($responderSeed <= 0) {
            return $this->errorReply($snap, $env, 'refused', 'node does not auto-accept contacts');
        }
        $id = (string) ($p['contact_id'] ?? '');
        $fromBase = (string) $env['from'];
        $host = self::hostOf($fromBase);
        $proposerSeed = (int) ($p['my_seed'] ?? 0);
        if ($proposerSeed <= 0) {
            return $this->errorReply($snap, $env, 'malformed', 'invalid my_seed');
        }
        $dup = &self::contactByHost($snap, $host);
        if ($dup !== null) {
            return $this->errorReply($snap, $env, 'duplicate-contact', "already have a contact with $host");
        }
        unset($dup);
        $c = [
            'ID' => $id, 'PeerBase' => $fromBase, 'PeerHost' => $host,
            'IAmProposer' => false, 'Active' => true, 'Closed' => false,
            'ProposerSeed' => $proposerSeed, 'ResponderSeed' => $responderSeed,
            'MyReserveOfPeer' => $responderSeed, 'PeerReserveOfMe' => $proposerSeed,
            'OpSeq' => 0, 'Roots' => [],
        ];
        self::applySeed($c);
        // Our seed leg: node:<peer> += responderSeed, from issuance (§6.1).
        self::ledgerLegs($snap, 'contact.seed', '', [
            ['account' => ACCT_NODE_PREFIX . $host, 'amount' => $responderSeed],
            ['account' => ACCT_ISSUANCE, 'amount' => -$responderSeed],
        ]);
        $snap['contacts'][] = $c;
        return $this->buildSigned($snap, 'contact.accept', $fromBase, (string) $env['id'],
            ['contact_id' => $id, 'my_seed' => $responderSeed]);
    }

    private function hContactAccept(array &$snap, array $env): ?array
    {
        $p = (array) ($env['payload'] ?? []);
        $id = (string) ($p['contact_id'] ?? '');
        $c = &self::contactById($snap, $id);
        if ($c === null || empty($c['IAmProposer'])) {
            return $this->errorReply($snap, $env, 'unknown-contact', 'no matching pending contact');
        }
        if ($c['PeerHost'] !== self::hostOf((string) $env['from'])) {
            return $this->errorReply($snap, $env, 'wrong-sender', 'contact does not belong to sender');
        }
        if (!empty($c['Active'])) {
            return null; // idempotent
        }
        $responderSeed = (int) ($p['my_seed'] ?? 0);
        if ($responderSeed <= 0) {
            return $this->errorReply($snap, $env, 'malformed', 'invalid my_seed');
        }
        $c['ResponderSeed'] = $responderSeed;
        self::ledgerLegs($snap, 'contact.seed', '', [
            ['account' => ACCT_NODE_PREFIX . $c['PeerHost'], 'amount' => (int) $c['ProposerSeed']],
            ['account' => ACCT_ISSUANCE, 'amount' => -(int) $c['ProposerSeed']],
        ]);
        $c['MyReserveOfPeer'] = (int) $c['ProposerSeed'];
        $c['PeerReserveOfMe'] = $responderSeed;
        self::applySeed($c);
        $c['Active'] = true;
        return null;
    }

    // ── transfers (PROTOCOL §7) ──────────────────────────────────────────────

    public function startTransfer(string $peerBase, string $fromMember, string $toMember, int $src, string $note = ''): array
    {
        if ($src <= 0) {
            throw new \InvalidArgumentException('src_amount must be positive');
        }
        return $this->store->transact(function (array &$snap) use ($peerBase, $fromMember, $toMember, $src, $note): array {
            $host = self::hostOf($peerBase);
            $c = &self::contactByHost($snap, $host);
            if ($c === null || empty($c['Active']) || !empty($c['Closed'])) {
                throw new \RuntimeException("no active contact with $host");
            }
            if (!empty($c['Busy'])) {
                throw new \RuntimeException('contact busy (§6.3)');
            }
            if (!empty($c['Diverged'])) {
                throw new \RuntimeException('contact frozen: checkpoint divergence (§8.3)');
            }
            $fm = self::localPart($fromMember);
            $led = Ledger::from_records($snap['ledger'] ?? []);
            if ($led->balance(ACCT_MEMBER_PREFIX . $fm) < $src) {
                throw new \RuntimeException("member $fm balance below $src");
            }
            $dst = self::priceOutgoing($c, $src);
            $id = self::uuid4();
            $snap['transfers'][] = [
                'ID' => $id, 'ContactID' => $c['ID'], 'Outgoing' => true,
                'State' => 'PROPOSED', 'OpSeq' => (int) $c['OpSeq'],
                'FromMember' => $fromMember, 'ToMember' => $toMember,
                'SrcAmount' => $src, 'DstAmount' => $dst,
                'Expires' => gmdate('c', time() + 3600),
            ];
            $c['Busy'] = true;
            $c['BusyTransfer'] = $id;
            return $this->buildSigned($snap, 'transfer.propose', $peerBase, '', [
                'transfer_id' => $id, 'contact_id' => $c['ID'], 'op_seq' => (int) $c['OpSeq'],
                'from_member' => $fromMember, 'to_member' => $toMember,
                'src_amount' => $src, 'dst_amount' => $dst, 'note' => $note,
                'expires' => gmdate('c', time() + 3600),
            ]);
        });
    }

    private function rejectTransfer(array &$snap, array $env, string $tid, string $code, string $detail): array
    {
        return $this->buildSigned($snap, 'transfer.reject', (string) $env['from'], (string) $env['id'],
            ['transfer_id' => $tid, 'code' => $code, 'detail' => $detail]);
    }

    private function hTransferPropose(array &$snap, array $env): array
    {
        $p = (array) ($env['payload'] ?? []);
        $tid = (string) ($p['transfer_id'] ?? '');
        $host = self::hostOf((string) $env['from']);
        $c = &self::contactByHost($snap, $host);
        if ($c === null || empty($c['Active']) || !empty($c['Closed'])) {
            return $this->rejectTransfer($snap, $env, $tid, 'unknown-contact', 'no active contact');
        }
        if (!empty($c['Busy'])) {
            return $this->rejectTransfer($snap, $env, $tid, 'busy', 'contact has an operation in flight');
        }
        if (!empty($c['Diverged'])) {
            return $this->rejectTransfer($snap, $env, $tid, 'frozen', 'contact frozen: checkpoint divergence (§8.3)');
        }
        if ((int) ($p['op_seq'] ?? -1) !== (int) $c['OpSeq']) {
            return $this->rejectTransfer($snap, $env, $tid, 'stale-pool', 'op_seq mismatch');
        }
        $toMember = (string) ($p['to_member'] ?? '');
        $tm = self::localPart($toMember);
        if (!isset(self::activeMembers($snap)[$tm])) {
            return $this->rejectTransfer($snap, $env, $tid, 'unknown-member', 'to_member not found');
        }
        $src = (int) ($p['src_amount'] ?? 0);
        $dst = (int) ($p['dst_amount'] ?? 0);
        if ($src <= 0) {
            return $this->rejectTransfer($snap, $env, $tid, 'malformed', 'invalid amounts');
        }
        try {
            $wantDst = self::priceIncoming($c, $src);
        } catch (\RuntimeException $e) {
            return $this->rejectTransfer($snap, $env, $tid, 'dust', $e->getMessage());
        }
        if ($wantDst !== $dst) {
            return $this->rejectTransfer($snap, $env, $tid, 'price-mismatch', "our price $wantDst != $dst");
        }
        $snap['transfers'][] = [
            'ID' => $tid, 'ContactID' => $c['ID'], 'Outgoing' => false,
            'State' => 'ACCEPTED', 'OpSeq' => (int) $p['op_seq'],
            'FromMember' => (string) ($p['from_member'] ?? ''), 'ToMember' => $toMember,
            'SrcAmount' => $src, 'DstAmount' => $dst, 'Expires' => (string) ($p['expires'] ?? ''),
        ];
        $c['Busy'] = true;
        $c['BusyTransfer'] = $tid;
        return $this->buildSigned($snap, 'transfer.accept', (string) $c['PeerBase'], (string) $env['id'],
            ['transfer_id' => $tid]);
    }

    private function hTransferAccept(array &$snap, array $env): ?array
    {
        $p = (array) ($env['payload'] ?? []);
        $t = &self::transferById($snap, (string) ($p['transfer_id'] ?? ''));
        if ($t === null || empty($t['Outgoing'])) {
            return $this->errorReply($snap, $env, 'unknown-transfer', 'no matching outgoing transfer');
        }
        $c = &self::contactById($snap, (string) $t['ContactID']);
        if ($c === null || $c['PeerHost'] !== self::hostOf((string) $env['from'])) {
            return $this->errorReply($snap, $env, 'wrong-sender', 'transfer does not belong to sender');
        }
        if ($t['State'] === 'COMMITTED' || $t['State'] === 'SETTLED') {
            return null; // idempotent
        }
        // A transfer past its expiry must never commit (§7.4): the payee may have
        // already swept its side to EXPIRED, and appending our leg here would move
        // money with no counterpart — destroying it one-sidedly and forking the
        // channel. Expire our side, release the lock, and tell the peer to abort.
        if (self::expireIfDue($t, $c, time())) {
            return $this->buildSigned($snap, 'transfer.abort', (string) $c['PeerBase'], (string) $env['id'],
                ['transfer_id' => (string) $t['ID']]);
        }
        $t['State'] = self::transition($t['State'], 'accept');   // -> ACCEPTED
        // Append our leg (§7.1): m:from -src, node:peer +src.
        self::ledgerLegs($snap, 'transfer.out', (string) $t['ID'], [
            ['account' => ACCT_MEMBER_PREFIX . self::localPart((string) $t['FromMember']), 'amount' => -(int) $t['SrcAmount']],
            ['account' => ACCT_NODE_PREFIX . $c['PeerHost'], 'amount' => (int) $t['SrcAmount']],
        ]);
        $c['MyReserveOfPeer'] = (int) $c['MyReserveOfPeer'] + (int) $t['SrcAmount'];
        $c['PeerReserveOfMe'] = (int) $c['PeerReserveOfMe'] - (int) $t['DstAmount'];
        self::applyOp($c, 'transfer', (string) $t['ID'], (int) $t['SrcAmount'], (int) $t['DstAmount']);
        $t['State'] = self::transition($t['State'], 'commit');   // -> COMMITTED
        return $this->buildSigned($snap, 'transfer.commit', (string) $c['PeerBase'], (string) $env['id'], [
            'transfer_id' => (string) $t['ID'],
            'entry' => ['log_seq' => count($snap['ledger']), 'log_hash' => self::ledgerHead($snap)],
        ]);
    }

    private function hTransferCommit(array &$snap, array $env): ?array
    {
        $p = (array) ($env['payload'] ?? []);
        $t = &self::transferById($snap, (string) ($p['transfer_id'] ?? ''));
        if ($t === null || !empty($t['Outgoing'])) {
            return $this->errorReply($snap, $env, 'unknown-transfer', 'no matching incoming transfer');
        }
        $c = &self::contactById($snap, (string) $t['ContactID']);
        if ($c === null || $c['PeerHost'] !== self::hostOf((string) $env['from'])) {
            return $this->errorReply($snap, $env, 'wrong-sender', 'transfer does not belong to sender');
        }
        if ($t['State'] === 'SETTLED' || $t['State'] === 'COMMITTED') {
            return $t['Receipt'] ?? null; // idempotent retry
        }
        $t['State'] = self::transition($t['State'], 'commit');   // -> COMMITTED
        // Append our leg (§7.1): node:peer -dst, m:to +dst.
        self::ledgerLegs($snap, 'transfer.in', (string) $t['ID'], [
            ['account' => ACCT_NODE_PREFIX . $c['PeerHost'], 'amount' => -(int) $t['DstAmount']],
            ['account' => ACCT_MEMBER_PREFIX . self::localPart((string) $t['ToMember']), 'amount' => (int) $t['DstAmount']],
        ]);
        $c['PeerReserveOfMe'] = (int) $c['PeerReserveOfMe'] + (int) $t['SrcAmount'];
        $c['MyReserveOfPeer'] = (int) $c['MyReserveOfPeer'] - (int) $t['DstAmount'];
        self::applyOp($c, 'transfer', (string) $t['ID'], (int) $t['SrcAmount'], (int) $t['DstAmount']);
        $t['State'] = self::transition($t['State'], 'receipt');  // -> SETTLED
        $c['Busy'] = false;
        $c['BusyTransfer'] = '';
        $receipt = $this->buildSigned($snap, 'transfer.receipt', (string) $c['PeerBase'], (string) $env['id'], [
            'transfer_id' => (string) $t['ID'],
            'entry' => ['log_seq' => count($snap['ledger']), 'log_hash' => self::ledgerHead($snap)],
        ]);
        $t['Receipt'] = $receipt;
        return $receipt;
    }

    private function hTransferReceipt(array &$snap, array $env): ?array
    {
        $p = (array) ($env['payload'] ?? []);
        $t = &self::transferById($snap, (string) ($p['transfer_id'] ?? ''));
        if ($t === null || empty($t['Outgoing'])) {
            return $this->errorReply($snap, $env, 'unknown-transfer', 'no matching outgoing transfer');
        }
        $c = &self::contactById($snap, (string) $t['ContactID']);
        if ($c === null || $c['PeerHost'] !== self::hostOf((string) $env['from'])) {
            return $this->errorReply($snap, $env, 'wrong-sender', 'transfer does not belong to sender');
        }
        if ($t['State'] === 'SETTLED') {
            return null;
        }
        $t['State'] = self::transition($t['State'], 'receipt');  // -> SETTLED
        if (($c['BusyTransfer'] ?? '') === $t['ID']) {
            $c['Busy'] = false;
            $c['BusyTransfer'] = '';
        }
        return null;
    }

    private function hTransferReject(array &$snap, array $env): ?array
    {
        $p = (array) ($env['payload'] ?? []);
        $t = &self::transferById($snap, (string) ($p['transfer_id'] ?? ''));
        if ($t === null || empty($t['Outgoing'])) {
            return null;
        }
        $c = &self::contactById($snap, (string) $t['ContactID']);
        if ($c === null || $c['PeerHost'] !== self::hostOf((string) $env['from'])) {
            return null;
        }
        try {
            $t['State'] = self::transition($t['State'], 'reject');
            if (($c['BusyTransfer'] ?? '') === $t['ID']) {
                $c['Busy'] = false;
                $c['BusyTransfer'] = '';
            }
        } catch (\RuntimeException $e) {
        }
        return null;
    }

    private function hTransferAbort(array &$snap, array $env): ?array
    {
        $p = (array) ($env['payload'] ?? []);
        $t = &self::transferById($snap, (string) ($p['transfer_id'] ?? ''));
        if ($t === null || !empty($t['Outgoing'])) {
            return null;
        }
        $c = &self::contactById($snap, (string) $t['ContactID']);
        if ($c === null || $c['PeerHost'] !== self::hostOf((string) $env['from'])) {
            return null;
        }
        try {
            $t['State'] = self::transition($t['State'], 'abort');
            if (($c['BusyTransfer'] ?? '') === $t['ID']) {
                $c['Busy'] = false;
                $c['BusyTransfer'] = '';
            }
        } catch (\RuntimeException $e) {
        }
        return null;
    }

    // ── reserve adjustments (PROTOCOL §8.4) ──────────────────────────────────

    public function adjustReserve(string $peerBase, int $delta, string $memo = ''): array
    {
        if ($delta === 0) {
            throw new \InvalidArgumentException('delta must be non-zero');
        }
        return $this->store->transact(function (array &$snap) use ($peerBase, $delta, $memo): array {
            $host = self::hostOf($peerBase);
            $c = &self::contactByHost($snap, $host);
            if ($c === null || empty($c['Active']) || !empty($c['Closed'])) {
                throw new \RuntimeException("no active contact with $host");
            }
            if (!empty($c['Busy'])) {
                throw new \RuntimeException('contact busy (§6.3)');
            }
            if (!empty($c['Diverged'])) {
                throw new \RuntimeException('contact frozen: checkpoint divergence (§8.3)');
            }
            if ((int) $c['MyReserveOfPeer'] + $delta < 0) {
                throw new \RuntimeException('withdrawal exceeds reserve');
            }
            $adjustId = self::uuid4();
            $c['Busy'] = true;
            $c['BusyTransfer'] = $adjustId;
            $c['PendingAdjustID'] = $adjustId;
            $c['PendingAdjustDelta'] = $delta;
            return $this->buildSigned($snap, 'reserve.adjust', $peerBase, '', [
                'contact_id' => (string) $c['ID'], 'op_seq' => (int) $c['OpSeq'],
                'adjust_id' => $adjustId, 'my_delta' => $delta, 'memo' => $memo,
            ]);
        });
    }

    private function hReserveAdjust(array &$snap, array $env): array
    {
        $p = (array) ($env['payload'] ?? []);
        $host = self::hostOf((string) $env['from']);
        $c = &self::contactByHost($snap, $host);
        if ($c === null || empty($c['Active']) || !empty($c['Closed'])) {
            return $this->errorReply($snap, $env, 'unknown-contact', 'no active contact');
        }
        if (!empty($c['Busy'])) {
            return $this->errorReply($snap, $env, 'busy', 'contact has an operation in flight');
        }
        if (!empty($c['Diverged'])) {
            return $this->errorReply($snap, $env, 'frozen', 'contact frozen: checkpoint divergence (§8.3)');
        }
        if ((int) ($p['op_seq'] ?? -1) !== (int) $c['OpSeq']) {
            return $this->errorReply($snap, $env, 'stale-pool', 'op_seq mismatch');
        }
        $adjustId = (string) ($p['adjust_id'] ?? '');
        $delta = (int) ($p['my_delta'] ?? 0);
        if ($delta === 0) {
            return $this->errorReply($snap, $env, 'malformed', 'invalid my_delta');
        }
        if (!empty($c['AppliedAdjusts'][$adjustId])) {
            return $this->buildSigned($snap, 'reserve.accept', (string) $c['PeerBase'], (string) $env['id'],
                ['contact_id' => (string) $c['ID'], 'adjust_id' => $adjustId]);
        }
        if ((int) $c['PeerReserveOfMe'] + $delta < 0) {
            return $this->errorReply($snap, $env, 'insufficient-reserve', 'withdrawal exceeds mirrored reserve');
        }
        $c['PeerReserveOfMe'] = (int) $c['PeerReserveOfMe'] + $delta;
        self::applyOp($c, 'adjust', $adjustId, $delta, 0);
        $c['AppliedAdjusts'][$adjustId] = true;
        return $this->buildSigned($snap, 'reserve.accept', (string) $c['PeerBase'], (string) $env['id'],
            ['contact_id' => (string) $c['ID'], 'adjust_id' => $adjustId]);
    }

    private function hReserveAccept(array &$snap, array $env): ?array
    {
        $p = (array) ($env['payload'] ?? []);
        $host = self::hostOf((string) $env['from']);
        $c = &self::contactByHost($snap, $host);
        if ($c === null) {
            return $this->errorReply($snap, $env, 'unknown-contact', 'no contact');
        }
        $adjustId = (string) ($p['adjust_id'] ?? '');
        if (($c['PendingAdjustID'] ?? '') !== $adjustId) {
            return null; // idempotent / unknown
        }
        $delta = (int) $c['PendingAdjustDelta'];
        self::ledgerLegs($snap, 'reserve.adjust', $adjustId, [
            ['account' => ACCT_NODE_PREFIX . $c['PeerHost'], 'amount' => $delta],
            ['account' => ACCT_TREASURY, 'amount' => -$delta],
        ]);
        $c['MyReserveOfPeer'] = (int) $c['MyReserveOfPeer'] + $delta;
        self::applyOp($c, 'adjust', $adjustId, $delta, 0);
        $c['Busy'] = false;
        $c['BusyTransfer'] = '';
        $c['PendingAdjustID'] = '';
        $c['PendingAdjustDelta'] = 0;
        return null;
    }

    // ── helpers ──────────────────────────────────────────────────────────────

    // ── contact close/update, lookup, key announce (PROTOCOL §6, §11, §3) ────

    /** closeContact freezes new operations on a contact and notifies the peer (§6). */
    public function closeContact(string $peerBase, string $note = ''): ?array
    {
        return $this->store->transact(function (array &$snap) use ($peerBase, $note): array {
            $c = &self::contactByHost($snap, self::hostOf($peerBase));
            if ($c === null) {
                throw new \RuntimeException('no contact with ' . self::hostOf($peerBase));
            }
            $c['Closed'] = true;
            return $this->buildSigned($snap, 'contact.close', $peerBase, '',
                ['contact_id' => (string) $c['ID'], 'note' => $note]);
        });
    }

    private function hContactClose(array &$snap, array $env): ?array
    {
        $c = &self::contactByHost($snap, self::hostOf((string) $env['from']));
        if ($c !== null) {
            $c['Closed'] = true;
        }
        return null;
    }

    private function hContactUpdate(array &$snap, array $env): ?array
    {
        // Informational (§6): actual reserve changes go through reserve.adjust, so
        // we acknowledge by accepting the envelope and make no state change.
        $c = &self::contactByHost($snap, self::hostOf((string) $env['from']));
        if ($c === null) {
            return $this->errorReply($snap, $env, 'unknown-contact', 'no contact');
        }
        return null;
    }

    private function hKeyAnnounce(array &$snap, array $env): ?array
    {
        // The envelope was already verified against a currently-valid key of
        // `from` (§3), so registering the announced key is safe.
        $p = (array) ($env['payload'] ?? []);
        $localId = (string) ($p['id'] ?? '');
        $pub = (string) ($p['public_key'] ?? '');
        if ($localId === '' || $pub === '') {
            return $this->errorReply($snap, $env, 'malformed', 'missing key id or public_key');
        }
        $snap['peer_keys'][(string) $env['from'] . LP_IDENTITY_PATH . $localId] = $pub;
        return null;
    }

    private function hMemberLookup(array &$snap, array $env): array
    {
        // Only answer over an active contact (§11).
        $c = &self::contactByHost($snap, self::hostOf((string) $env['from']));
        if ($c === null || empty($c['Active'])) {
            return $this->errorReply($snap, $env, 'no-contact', 'lookups require an active contact');
        }
        $p = (array) ($env['payload'] ?? []);
        $member = (string) ($p['member'] ?? '');
        $local = self::localPart($member);
        $actives = self::activeMembers($snap);
        $found = isset($actives[$local]);
        $res = ['member' => $member, 'found' => $found];
        if ($found && ($this->cfg['transparency'] ?? '') === 'public' && !empty($actives[$local]['DisplayName'])) {
            $res['display_name'] = (string) $actives[$local]['DisplayName'];
        }
        return $this->buildSigned($snap, 'member.result', (string) $env['from'], (string) $env['id'], $res);
    }

    /** lookupMember asks a peer whether an address exists there (§11). */
    public function lookupMember(string $peerBase, string $member): ?array
    {
        return $this->store->transact(function (array &$snap) use ($peerBase, $member): array {
            return $this->buildSigned($snap, 'member.lookup', $peerBase, '', ['member' => $member]);
        });
    }

    /**
     * rotateKey generates a new signing key, announces it to every open contact
     * signed by the CURRENT key, then activates it (§3). The old key stays valid
     * (and listed) until revoked. Returns the new local id.
     */
    public function rotateKey(): string
    {
        if (!have_sodium()) {
            throw new \RuntimeException('key rotation requires the sodium extension');
        }
        return $this->store->transact(function (array &$snap): string {
            $seed = random_bytes(32);
            $kp = \sodium_crypto_sign_seed_keypair($seed);
            $pub = b64url(\sodium_crypto_sign_publickey($kp));
            $newLocal = '#nk' . (count($snap['own_keys']) + 1);
            $now = gmdate('c');
            // Announce first — buildSigned signs with the still-active old key.
            foreach ($snap['contacts'] as $c) {
                if (!empty($c['Closed'])) {
                    continue;
                }
                $this->buildSigned($snap, 'key.announce', (string) $c['PeerBase'], '',
                    ['id' => $newLocal, 'alg' => 'ed25519', 'public_key' => $pub, 'created' => $now]);
            }
            $snap['own_keys'][] = ['local_id' => $newLocal, 'seed' => b64url($seed), 'created' => $now, 'revoked' => ''];
            $snap['active_key'] = $newLocal;
            return $newLocal;
        });
    }

    // ── expiry (PROTOCOL §7.4) ───────────────────────────────────────────────

    /**
     * expireIfDue moves one transfer to EXPIRED if it is past `expires` and still
     * pre-commit (PROPOSED/ACCEPTED), releasing the contact lock. Returns whether
     * it expired. This is the single expiry rule shared by the sweeper and the
     * payer-side commit guard, so both paths agree (mirrors the Go
     * expireIfDueLocked). $t and $c are snapshot references.
     */
    private static function expireIfDue(array &$t, array &$c, int $now): bool
    {
        $st = (string) ($t['State'] ?? '');
        if ($st !== 'PROPOSED' && $st !== 'ACCEPTED') {
            return false;
        }
        $exp = strtotime((string) ($t['Expires'] ?? ''));
        if ($exp === false || $now <= $exp) {
            return false;
        }
        try {
            $t['State'] = self::transition($st, 'expire');
        } catch (\Throwable $e) {
            return false;
        }
        if (($c['BusyTransfer'] ?? '') === $t['ID']) {
            $c['Busy'] = false;
            $c['BusyTransfer'] = '';
        }
        return true;
    }

    /**
     * sweepExpired expires every transfer past its `expires` that is still
     * pre-commit (PROPOSED/ACCEPTED) and releases the contact lock, so a dropped
     * accept/commit can't pin a contact busy forever. Both sides expire
     * independently against their own clock and stay consistent because nothing
     * was committed. Returns how many it closed. Run it from poll.php.
     */
    public function sweepExpired(): int
    {
        return $this->store->transact(function (array &$snap): int {
            $now = time();
            $count = 0;
            foreach ($snap['transfers'] as &$t) {
                $c = &self::contactById($snap, (string) $t['ContactID']);
                $stub = [];
                $lock = &$c;
                if ($c === null) {
                    $lock = &$stub; // no contact to unlock; give the helper a throwaway
                }
                if (self::expireIfDue($t, $lock, $now)) {
                    $count++;
                }
                unset($c, $lock);
            }
            unset($t);
            return $count;
        });
    }

    // ── checkpoint reconciliation (PROTOCOL §8.3) ────────────────────────────

    /** reconcilePeer fetches a peer's checkpoint and reconciles the shared contact. */
    public function reconcilePeer(HttpTransport $t, string $peerBase): array
    {
        $cp = $t->fetchCheckpoint($peerBase);
        if ($cp === null) {
            return ['compared' => false];
        }
        return $this->reconcileAgainst($peerBase, $cp);
    }

    /**
     * reconcileAgainst compares a peer's checkpoint to our contact: it prunes
     * outbox entries the peer has acknowledged (§5.1) and freezes the contact if
     * the peer's channel root at its op_seq disagrees with our recorded root at
     * that index (a fork). A peer merely behind but consistent is normal lag.
     */
    public function reconcileAgainst(string $peerBase, array $cp): array
    {
        return $this->store->transact(function (array &$snap) use ($peerBase, $cp): array {
            $c = &self::contactByHost($snap, self::hostOf($peerBase));
            if ($c === null) {
                return ['compared' => false];
            }
            foreach ((array) ($cp['contacts'] ?? []) as $pc) {
                if ((string) ($pc['contact_id'] ?? '') !== (string) $c['ID']) {
                    continue;
                }
                $peerOpSeq = (int) ($pc['op_seq'] ?? -1);
                $peerRoot = (string) ($pc['channel_root'] ?? '');

                // Prune acknowledged outbox entries.
                $pruned = 0;
                $lastProc = (int) ($pc['last_seq_processed'] ?? 0);
                if ($lastProc > 0) {
                    $host = (string) $c['PeerHost'];
                    $kept = [];
                    foreach ((array) ($snap['outbox'][$host] ?? []) as $e) {
                        if ((int) ($e['seq'] ?? 0) <= $lastProc) {
                            $pruned++;
                            continue;
                        }
                        $kept[] = $e;
                    }
                    $snap['outbox'][$host] = $kept;
                }

                // Divergence at the common op_seq.
                if ($peerOpSeq >= 0 && $peerOpSeq <= (int) $c['OpSeq']
                    && isset($c['Roots'][$peerOpSeq]) && $peerRoot !== ''
                    && $peerRoot !== (string) $c['Roots'][$peerOpSeq]) {
                    $c['Diverged'] = true;
                    return ['compared' => true, 'diverged' => true, 'pruned' => $pruned];
                }
                return ['compared' => true, 'diverged' => false, 'pruned' => $pruned];
            }
            return ['compared' => false];
        });
    }

    private static function ledgerHead(array $snap): string
    {
        $recs = $snap['ledger'] ?? [];
        return $recs ? (string) $recs[count($recs) - 1]['hash'] : '';
    }

    private static function localPart(string $addr): string
    {
        $i = strpos($addr, '@');
        return $i === false ? $addr : substr($addr, 0, $i);
    }
}
