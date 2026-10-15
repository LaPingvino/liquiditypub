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

**Groups** (host.go, admin.go): Kafumu's groups are each their own node on this
host (`https://<gid>.g.kafumu.com`, stored as `g-<gid>`, registered in `LPGroup`
and opened at start); between them envelopes go in process. `/cron/ud` pays the
dividend in the main node and every group. Public reads per group at
`/lp/g/<gid>/…`. The admin API Kafumu calls (Bearer token from `LPConfig`):
`POST /lp/admin/group {gid,name}`, `POST /lp/admin/member {gid,member}`,
`GET /lp/admin/balance?gid=&member=`, `POST /lp/admin/pay {gid,from,to,amount}`,
`POST /lp/admin/link {a,b,seed?}`, `POST /lp/admin/transfer {from_gid,from,to_gid,to,amount}`,
`GET /lp/admin/transfer?gid=&id=`.
