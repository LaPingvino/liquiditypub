# LiquidityPub

See **AGENTS.md** for the full agent guide (read order, membrane principle,
no-floats rule, definition of done, beads workflow). Quick anchors:

- Concept + open questions: `docs/DESIGN.md`
- Normative spec (v0.2-draft): `docs/PROTOCOL.md`
- Executable spec + vectors: `conformance/` (`go test ./...` must stay green)
- Implementations: `node/` (Go reference), `php/` (independent PHP node + dashboard)
- The dead v0.1 PHP PoC at the repo root was removed; don't reintroduce that shape.
