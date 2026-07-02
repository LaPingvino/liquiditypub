# LiquidityPub — PHP node (Profile A on cheap hosting)

A second, independent implementation of a LiquidityPub node, targeting the
lowest common denominator of hosting: **PHP on cheap shared hosting**, no
persistent daemon, no ability to run a Go binary. The goal is *full
independence* — a community runs its own sovereign currency on a €3/month host
that it controls end to end, depending on no one (this is the self-hosted
counterpart to Profile C, where a clearing house operates thin nodes on a
community's behalf; both are wanted).

A second implementation is not just a deployment convenience. Because every node
is its own currency that federates *by choice*, the protocol must not be captured
by a single codebase. Two interoperable implementations, both pinned to the same
conformance vectors, are what prove the spec — not the Go code — is the source of
truth.

## Why PHP can do this honestly

Everything the protocol pins bit-for-bit is available natively in PHP, so there
is no re-derivation of primitives, only re-expression:

| Protocol requirement | PHP primitive |
|---|---|
| Canonical JSON (JCS, §2) | hand-written serializer matching Go byte-for-byte |
| SHA-256 record/channel chain (§8.2, §9.2) | `hash('sha256', …)` |
| Exact pool pricing (§6.2) | `gmp_*` (big integers, no float, no overflow) |
| UD formula (§10) | `gmp_*` |
| Ed25519 sign/verify (§4) | `sodium_crypto_sign_*` (bundled with PHP ≥ 7.2) |
| base64url (§2) | `base64_encode` + `strtr`/`rtrim` |

`lp_core.php` is that core. It holds **no state** — it is the arithmetic/crypto
half, the part where a divergence from Go would be a silent consensus split.

## Verified against the Go conformance vectors

`test_vectors.php` runs the PHP core against the *same* files the Go reference
uses (`../conformance/vectors/*.json`):

```
php php/test_vectors.php
```

Status: **35/35 vectors pass** (with sodium; the ledger-transcript golden is
checked here *and* by the Go ledger, so the two agree transitively). Run the
whole PHP suite:

```
php php/test_vectors.php      # 35/35 — core + ledger vs the Go vectors
php php/test_node.php         # node layer: store, issuance, read surface, queue
php php/test_federation.php   # two signed nodes: contact, transfer, reserve (in-process)
php php/test_http.php         # the HTTP front controller
php php/test_transport.php    # two nodes federating over REAL HTTP (pull + push)
```

## What's implemented

The node is complete enough to deploy and federate:

| File | Role |
|------|------|
| `lp_core.php` | canonical JSON, hashing, base64url, pool pricing, UD, ed25519 — the vector-pinned core, no state |
| `lp_ledger.php` | append-only double-entry ledger (§9): sum=0 + non-negative-node-wallet invariants, hash chain |
| `lp_store.php` | flock single-writer JSON snapshot store (Go `store.Store` schema), atomic saves |
| `lp_node.php` | identity doc, signed checkpoint, read surface, UD scheduler, membership, §4 validation, action-queue drain |
| `lp_federation.php` | contact / transfer / reserve state machines (§6–§8), envelope build+sign, `processInbound` |
| `lp_transport.php` | HTTP client (fetch identity/outbox/checkpoint, push inbox) |
| `public/index.php` | the HTTP front controller — the only file exposed to the web |
| `poll.php` | cron driver: drain queue → federate (pull+push) → optional `--ud` |
| `web/` | operator dashboard (reads the live snapshot; forms queue intents `poll.php` applies) |

## Deploying on cheap hosting

1. Copy `config.example.php` to `config.php` and set `base`, `state_file` (keep
   it **out of the web root**), and `peers`.
2. Point your web root at `php/public/` (or rewrite everything to
   `public/index.php`). It serves the identity doc, checkpoint, outbox, and log,
   and accepts pushes at `/lp/inbox`.
3. Add a cron entry: `*/15 * * * * php /path/php/poll.php` to federate, and
   `0 0 * * * php /path/php/poll.php --ud` for the daily dividend. The mandatory
   pull baseline (§5.1) means no always-on process is needed.
4. Point the dashboard (`php/web/`) at the same host to drive the node.

Requires the `gmp`, `hash`, `curl`, and `sodium` PHP extensions — all standard.

## What's next

Interop with the Go node over HTTP (needs a running Go instance), and the
remaining niceties: transfer-expiry sweep + checkpoint-divergence detection
(port of `node/expiry.go` / `node/reconcile.go`), and the `contact.close/update`,
`member.lookup`, and key-rotation handlers.
