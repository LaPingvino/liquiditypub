# gae — a LiquidityPub node on App Engine (Profile B)

`main.go` serves one node with the reference `node` package, configured from
`app.yaml` (no secrets there). First user: Kafumu's **CoffeeBucks (K$)**, as the
App Engine service `lp` behind kafumu.com.

- Local, on the working tree (go.work): `PORT=18093 go run ./gae` from the repo root.
- Standalone, as App Engine builds it: `cd gae && GOWORK=off go build .` — it
  needs `node` and `conformance` at a pushed tag (`node/vX.Y.Z`,
  `conformance/vX.Y.Z`); bump `go.mod` after tagging.
- Deploy: `gcloud app deploy app.yaml --project <project>`.

State lives in Datastore (namespace `lp`, `dsstore.go`) whenever a project is
known (`GOOGLE_CLOUD_PROJECT` on App Engine, or `DATASTORE_PROJECT_ID` with the
emulator): every ledger record its own entity (append-only, written once), the
rest of the snapshot in chunks under one `LPState` whose version makes the
process the single writer — a second writer exits and reloads instead of forking
the ledger. The admin token is made on first start and kept in `LPConfig`.
Tests against the emulator: `DATASTORE_EMULATOR_HOST=localhost:8432 go test ./gae/`.

Not yet: the daily dividend from cron, and hosting several group nodes.
