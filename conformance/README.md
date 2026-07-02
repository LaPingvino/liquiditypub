# LiquidityPub Conformance Suite

The executable heart of the v0.2 protocol. Three layers:

1. **`vectors/*.json` — language-agnostic test vectors.** Any implementation in
   any language (Go, PHP, Workers/JS, …) must reproduce these exactly. The
   arithmetic and hash vectors were computed independently (Python, arbitrary
   precision) from the spec prose, so the Go reference below doesn't get to
   grade its own homework.
2. **`*.go` — the Go reference implementation of the pure-function core**
   (canonical JSON, envelope signing, pool pricing, channel hashes, UD formula,
   transfer state machine, envelope validation). Normative: a disagreement
   between this code and PROTOCOL.md prose is a spec bug to raise, not to
   resolve silently.
3. **`runner/` + `cmd/lpconform` — black-box federation checker** for live
   nodes: validates the read surface (identity document, public checkpoint) of
   any node by URL, and cross-checks the reconciliation invariant (matching
   `channel_root` / `op_seq`) between two nodes. Active-transfer conformance is
   exercised by the PoC integration harness once an implementation exists.

## Run

```sh
cd conformance
go test ./...                 # vectors + property tests
go run ./cmd/genvectors       # regenerate signature-bearing vectors (deterministic)
go run ./cmd/lpconform <url> [peer-url]   # against live nodes
```

## What implementations must get right (the load-bearing pins)

- **No floats anywhere.** All protocol numbers are integers (micro-units,
  micro-weights, ppm). Intermediates (pool products) must be exact — 128-bit or
  bignum — before flooring.
- **Canonical JSON** = UTF-8, no whitespace, sorted keys, plain decimal
  integers, minimal escaping (JCS reduces to this under the no-floats rule).
- **Envelope signatures** cover JCS(envelope minus `sig`), ed25519.
- **Pool pricing** `dst = floor(r_dst·src/(r_src+src))` must be *maximal* while
  preserving `k` (the tightness property test).
- **Channel hash** `root_n = SHA-256(root_{n-1} ‖ JCS([type,id,src,dst]))`,
  raw 32-byte roots, seeds are operation 0.
- **State machine**: duplicate commits are idempotent; no expiry after commit;
  abort only before commit.
