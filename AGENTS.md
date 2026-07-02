# Agent Guide — LiquidityPub

Instructions for any coding agent (Claude, Opus subagents, or humans in a
hurry) building on this repository.

## What this project is

A federated **libre currency** protocol (TRM lineage: Universal Dividend money)
without a blockchain. Communities run sovereign nodes; nodes meet at **contact
points** — reciprocal reserve wallets that price exchange like a constant-product
market. Read, in order:

1. `docs/DESIGN.md` — the concept, the reasoning, what's settled vs. open.
2. `docs/PROTOCOL.md` — the v0.2-draft spec (normative for the contact surface).
3. `conformance/README.md` — the executable spec and test vectors.

## The two rules that shape everything

- **The membrane principle**: the protocol governs only the contact surface
  (identity, envelopes, contacts, transfers, checkpoints). Node internals are
  deliberately free. Never add internal requirements to the protocol; never
  make peers depend on another node's internals.
- **No floats, ever.** Integers only (micro-units, micro-weights, ppm); exact
  intermediates before flooring. If you find yourself writing `float`, stop.

## Building an implementation (PoC 1 = Go single binary, task joop-eci)

Definition of done, in order:

1. `cd conformance && go test ./...` stays green (never edit vectors to make
   an implementation pass — a vector mismatch is a bug in the implementation
   or a spec issue to raise).
2. The implementation's pure core either imports
   `github.com/LaPingvino/liquiditypub/conformance` directly (Go) or passes
   all `conformance/vectors/*.json` (other languages).
3. `go run ./cmd/lpconform <node-url>` passes against a running instance.
4. Two local instances open a contact (seeded pool), execute transfers both
   directions, and `lpconform <a> <b>` cross-check shows matching channel
   roots and op_seq.
5. Ledger invariants hold under test: `SUM(entries)=0` always; node wallets
   never negative; reserves are a pure function of the op history.

Conventions: Go stdlib only where possible; SQLite for storage (schema shared
with the future Cloudflare D1 profile); issuance driven by a scheduler, never
by request handling.

## Task tracking

This project uses **bd (beads)**, not ad-hoc TODO lists:
`bd ready --json`, `bd show <id> --json`, `bd update <id> --claim`,
`bd close <id> --reason "..."`, then `bd dolt push`. Epic: `joop-pbe`.

## Implementations

- `node/` — the **Go reference node** (Profile A, task joop-eci).
- `php/` — the **independent PHP node** for cheap shared hosting (task joop-j4y):
  `lp_core.php`/`lp_ledger.php` (vector-verified protocol core), `lp_store.php`
  (flock JSON snapshot), `lp_node.php` (read surface, issuance, §4 validation),
  and `php/web/` (operator dashboard). Run `php php/test_vectors.php` and
  `php php/test_node.php`.

The dead v0.1 PHP PoC (root `index.php`, `install.php`, `src/`, `pages/`, `api/`,
`db/schema.sql`) was **removed** once the real PHP implementation landed. If you
see it referenced anywhere, it's stale.
