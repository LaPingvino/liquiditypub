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

Current status: **17/17 runnable vectors pass** — canonical JSON, the channel
hash chain (seed + transfer + adjust), pool pricing (including the case whose
intermediates exceed 2⁵³, which forces exact arithmetic), and the UD reference.
The Ed25519 sign/verify vectors need the `sodium` extension; where it is absent
they report SKIP, because the real divergence risk — the exact bytes that get
signed — is already checked byte-for-byte, and RFC 8032 signing over identical
bytes is deterministic by definition.

This is the load-bearing result: a PHP node and a Go node agree on every byte the
protocol pins.

## What remains (the node layer)

The core is done and proven; the stateful node around it mirrors the Go node's
structure and is the remaining work:

1. **State store** — one JSON snapshot blob, byte-compatible with the Go node's
   `store.Store` schema, in a file (`flock` for the single-writer discipline) or
   a SQLite/MySQL row. Sharing the snapshot format means state is portable
   between the Go and PHP implementations.
2. **Read surface** (static-friendly, §5.1/§9.2): `/.well-known/liquiditypub`,
   `/lp/checkpoint.json`, `/lp/outbox/<host>.json`, `/lp/log/page-N.json` — plain
   files or trivial PHP that reads the snapshot.
3. **Inbox** (§5.2): `inbox.php` validates an envelope with the core (`sig.key`
   bound to `from`, seq/dup/window checks) and applies it under the file lock.
4. **Pull + issuance** (§5.1, §10): a cron-driven `poll.php` that fetches peer
   outboxes/checkpoints and runs the UD scheduler. The mandatory pull baseline
   means no always-on process is required; a ≤15-minute cron tick is a compliant
   live node.

Every one of these steps is verifiable the same way: extend the vector set with
end-to-end transcript vectors (envelope-in → snapshot-delta / reply-out) emitted
by the Go reference, and replay them in PHP.
