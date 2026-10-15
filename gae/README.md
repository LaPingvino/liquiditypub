# gae — a LiquidityPub node on App Engine (Profile B)

`main.go` serves one node with the reference `node` package, configured from
`app.yaml` (no secrets there). First user: Kafumu's **CoffeeBucks (K$)**, as the
App Engine service `lp` behind kafumu.com.

- Local, on the working tree (go.work): `PORT=18093 go run ./gae` from the repo root.
- Standalone, as App Engine builds it: `cd gae && GOWORK=off go build .` — it
  needs `node` and `conformance` at a pushed tag (`node/vX.Y.Z`,
  `conformance/vX.Y.Z`); bump `go.mod` after tagging.
- Deploy: `gcloud app deploy app.yaml --project <project>`.

Not yet: persistence (a Datastore store: ledger records as append-only entities,
one small state entity with a version check), the node key and admin token in
Datastore, the daily dividend from cron, and hosting several group nodes.
