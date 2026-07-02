# LiquidityPub

**A federated libre-currency protocol — Universal Dividend money without a blockchain.**

Each community runs a sovereign node with its own currency and its own issuance
(a Universal Dividend, in the [Relative Theory of Money][trm] lineage). Nodes
meet at **contact points**: reciprocal reserve wallets that price exchange
between two currencies like a constant-product market. There is no shared ledger
and no chain — trust is bilateral, and every node federates only with peers it
chooses.

Status: **v0.2-draft**. The protocol and its executable conformance suite are the
source of truth; two reference implementations are checked against it.

## Repository layout

| Path | What it is |
|------|------------|
| `docs/DESIGN.md` | The concept and the reasoning — settled vs. open questions. |
| `docs/PROTOCOL.md` | The normative v0.2-draft spec (the contact surface). |
| `conformance/` | The executable spec: reference arithmetic + `vectors/*.json`. **The source of truth.** |
| `node/` | **Go reference node** (Profile A) — the single-binary implementation. |
| `php/` | **Independent PHP node** for cheap shared hosting + an operator dashboard. |

Two independent implementations is deliberate: because every node is its own
currency that federates by choice, the protocol must not be captured by one
codebase. Both are pinned to the same `conformance/vectors/`, so the vectors —
not either implementation — define correctness.

## The two rules that shape everything

- **The membrane principle.** The protocol governs only the contact surface
  (identity, envelopes, contacts, transfers, checkpoints). Node internals are
  free. Peers never depend on another node's internals.
- **No floats, ever.** Integers only — money is micro-units (1 unit = 1,000,000
  micro), weights are micro-weights, growth is ppm. Exact intermediates before
  flooring.

## Quick start

**Conformance suite (Go):**
```
cd conformance && go test ./...
```

**Go reference node:**
```
cd node && go test ./...
go run ./cmd/lpnode serve -addr 127.0.0.1:8080   # serve one node
```

**PHP node (cheap hosting):**
```
php php/test_vectors.php     # PHP core + ledger vs the Go conformance vectors
php php/test_node.php         # the node layer (store, issuance, read surface, queue)
php -S 127.0.0.1:8099 -t php/web   # the operator dashboard
```
The PHP node needs the `gmp`, `hash`, and (for signing) `sodium` extensions —
all standard on PHP ≥ 7.2. See `php/README.md` for what's implemented and what's
next (the federation state machines).

## Deployment profiles

The protocol supports three deployment shapes (PROTOCOL §12): **A** — an active
server (Go binary, or the PHP node); **B** — serverless (Cloudflare Pages + D1,
GAE); **C** — an HTML-only community whose node is operated by a clearing house
under a delegated key, with the community's own static host as the root of trust.

## Task tracking

Work is tracked in **bd (beads)**, not ad-hoc lists. Epic: `joop-pbe`.

## License

[MIT](LICENSE) © 2026 Joop Kiefte

[trm]: https://en.trm.creationmonetaire.info/
