# lpnode — Profile-A reference node (PoC 1)

A single Go binary implementing the LiquidityPub v0.2 contact surface
(`../docs/PROTOCOL.md`), Profile A (active server, §12). It is the first
proof-of-concept for the epic tracked in beads as `joop-eci`.

## Design in one paragraph

Every arithmetic the protocol pins is delegated to the `conformance` package —
this node imports it directly, so pool pricing, channel hashes, the UD formula,
JCS canonicalization, ed25519 signing, envelope validation, and the transfer
state machine are the spec's own reference code, not a re-implementation. What
lives here is the *shell*: an append-only, hash-linked, double-entry ledger
(`ledger/`), the contact/transfer orchestration, the HTTP bindings, and a
federation client. Storage is a stdlib-only in-memory ledger (no external
dependencies) behind a small surface, so the Cloudflare-D1 (`joop-n7j`) and
GAE (`joop-3mu`) ports have a clean seam to slot a different backend into.

## Run it

```
go run ./cmd/lpnode demo        # two in-process nodes, full round trip + checks
```

`demo` binds two localhost nodes, opens a seeded contact, transfers in both
directions, then runs the black-box `lpconform` checks and verifies both ledger
chains. Expected tail: `ALL GREEN`.

Serve a single long-running node and point the conformance runner at it:

```
go run ./cmd/lpnode serve -addr 127.0.0.1:8092 -base http://127.0.0.1:8092 \
    -name Hilltop -currency "Hill Credits" -symbol H -members bob:100000000
go run ./cmd/lpnode serve -addr 127.0.0.1:8091 -base http://127.0.0.1:8091 \
    -name Riverside -currency "River Credits" -symbol R -members alice:100000000 \
    -peer http://127.0.0.1:8092

# from ../conformance:
go run ./cmd/lpconform http://127.0.0.1:8091 http://127.0.0.1:8092
```

## Definition of done (AGENTS.md) — status

1. `cd ../conformance && go test ./...` stays green — vectors untouched. ✅
2. The pure core imports `conformance` directly. ✅
3. `lpconform <node-url>` passes against a running instance. ✅
4. Two instances open a contact, transfer both ways, cross-check shows matching
   channel roots and op_seq. ✅ (`demo`, and the `serve` + `lpconform` pair.)
5. Ledger invariants under test: `SUM(entries)=0`, node wallets never negative,
   reserves a pure function of op history. ✅ (`go test ./...`.)

## Endpoints (read surface is mirror-friendly, §5.1/§8.3/§9.2)

| Path | Spec |
|---|---|
| `GET /.well-known/liquiditypub` | identity document (§3) |
| `POST /lp/inbox` | push binding (§5.2) |
| `GET /lp/outbox/{peer-host}.json` | pull binding (§5.1) |
| `GET /lp/checkpoint.json` | signed checkpoint (§8.3) |
| `GET /lp/log/head.json`, `GET /lp/log/` | hash-linked log (§9.2) |
| `POST /admin/{contact,transfer,ud}` | out-of-protocol driver for demos |

## Implemented behaviours

- Persistence is opt-in: `serve -state <file>` (or `lpnode.Restore` / `SetStore`
  with a `store.Store`) snapshots the full node — ledger, contacts, transfers,
  channel bookkeeping, outboxes, keys — after every state change, durable
  *before* the change is observable, and resumes from it on restart. The
  `store.Store` interface is the seam the D1 (`joop-n7j`) and GAE (`joop-3mu`)
  profiles back with their own KV/blob. Default (no `-state`) is in-memory.
- Both transports work: push (§5.2) and the pull baseline (§5.1, `serve -pull
  <cadence>`, `Node.PollPeer`/`StartPulling`), including outbox pruning against
  a peer's published `last_seq_processed` (done during reconciliation).
- Auto-accept of contacts and genesis member grants are demo conveniences, not
  protocol requirements (both are node-internal policy under the membrane).
- `reserve.adjust` (§8.4) is implemented: consensual liquidity top-ups /
  withdrawals (`serve` admin `/admin/adjust`, `Node.AdjustReserve`), folded into
  the channel hash as an operation.
- Key rotation (§3, `Node.RotateKey`/`RevokeKey`, `key.announce`) and
  `contact.close`/`contact.update` (§6) are implemented, keyring persisted.
  Verifier-side revocation is enforced (§3, §13): `RefreshPeerKeys` (run on each
  poll cycle) re-fetches a peer's identity doc and drops keys it now marks
  revoked or has dropped, so a stolen-then-revoked key stops validating.
- Transfer expiry (§7.4) is enforced: `StartExpirySweeper` (run by `serve`) and
  inline checks on the busy-guards move a stalled pre-commit transfer to EXPIRED
  and release the contact lock, so a dropped accept/commit can't pin a contact.
- Checkpoint reconciliation (§8.3) runs on every pull cycle (`ReconcilePeer`):
  the node fetches each peer's checkpoint and freezes a contact (`Diverged`) if
  the peer's `channel_root` contradicts ours at the same `op_seq`, refusing new
  operations until resolved out of band. A mere op_seq lag (a normal in-flight
  transient) is not treated as divergence.
- Transfer rejection uses `transfer.reject {transfer_id, code, detail}` (§7.2),
  so a rejected proposal advances the payer to REJECTED and frees the contact;
  the payer can also `AbortTransfer` a pre-commit transfer (`/admin/abort`).
- Runtime membership: `AddMember` / `DeactivateMember` (`/admin/member`) —
  inactive members receive no UD and cannot be transfer targets. Names are never
  deleted (the log references them).
- Log pagination (§9.2): fixed-size pages at `/lp/log/page-N.json`, with
  `page_size`/`page_count` in `/lp/log/head.json`.
- Transparency-gated log (§9.3): `public`/`pseudonymous` logs are open; a
  `peers` log requires an `X-LP-Peer` header naming an active contact.
  Checkpoints are public in every mode. The header check is advisory
  (unauthenticated GET) — adequate because the security-critical data
  (checkpoints) is always public; the full log at `peers` level is a privacy
  nicety, not a trust boundary.

## Genuinely deferred

- Sealed outboxes (§5.1, EXPERIMENTAL) — deferred to field experience per
  DESIGN §10; the `sealed-outbox` capability is reserved.
