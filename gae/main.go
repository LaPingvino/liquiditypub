// Command gae serves a LiquidityPub node on Google App Engine (Profile B,
// PROTOCOL §12; beads joop-3mu). First user: Kafumu's CoffeeBucks (K$),
// deployed as the service "lp" behind kafumu.com.
//
// Configuration comes from the environment (app.yaml), never secrets; the
// node's state, key and admin token live in Datastore (dsstore.go):
//
//	LP_BASE      public origin, e.g. https://kafumu.com
//	LP_NAME      node name
//	LP_DESC      one-line description
//	LP_CURRENCY  currency name (CoffeeBucks)
//	LP_SYMBOL    currency symbol (K$)
//	LP_NODE_ID   the node's id in Datastore (coffeebucks)
//	PORT         set by App Engine
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/datastore"
	lpnode "github.com/LaPingvino/liquiditypub/node"
	"github.com/LaPingvino/liquiditypub/node/fed"
	"github.com/LaPingvino/liquiditypub/node/httpapi"
	"github.com/LaPingvino/liquiditypub/node/store"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	base := env("LP_BASE", "http://localhost:"+env("PORT", "8080"))
	nodeID := env("LP_NODE_ID", "coffeebucks")
	cfg := lpnode.Config{
		Base:           base,
		Name:           env("LP_NAME", "CoffeeBucks"),
		Description:    env("LP_DESC", "CoffeeBucks (K$): Kafumu's local currency, a Universal Dividend for every member"),
		CurrencyName:   env("LP_CURRENCY", "CoffeeBucks"),
		CurrencySymbol: env("LP_SYMBOL", "K$"),
		CPeriodPpm:     274, // ≈ 10 %/year, daily (PROTOCOL §3)
		UDPeriod:       "P1D",
		GenesisUD:      1_000_000,
		Transparency:   "pseudonymous",
	}
	// Kafumu's groups (VISION §4): each its own K$ node on this host.
	groupCfg := func(gid, name string) lpnode.Config {
		c := cfg
		c.Name, c.Description = name, name+": a CoffeeBucks group on Kafumu"
		c.CurrencyName = "CoffeeBucks " + name
		c.AutoAcceptSeed = 100 * cfg.GenesisUD // a link is seeded with a hundred dividends a side: UD parity
		return c
	}
	ctx := context.Background()
	var db *datastore.Client
	var n *lpnode.Node
	var h *Host
	var err error
	if project := env("GOOGLE_CLOUD_PROJECT", env("DATASTORE_PROJECT_ID", "")); project != "" {
		// On App Engine (or against the emulator): every node lives in
		// Datastore, the admin token too; a second writer makes this process
		// exit and restart from what's stored rather than fork a ledger.
		if db, err = datastore.NewClient(ctx, project); err != nil {
			log.Fatal(err)
		}
		if cfg.AdminToken, err = adminToken(ctx, db, nodeID); err != nil {
			log.Fatal(err)
		}
		n, err = lpnode.Restore(cfg, exitOnConflict{NewDSStore(db, nodeID)})
		h = NewHost(env("LP_GROUP_DOMAIN", "g.kafumu.com"), func(id string) store.Store { return exitOnConflict{NewDSStore(db, id)} }, groupCfg)
	} else {
		// Local, no Datastore: in memory, and the admin API closed (a random
		// token nobody knows).
		tok := make([]byte, 32)
		rand.Read(tok)
		cfg.AdminToken = hex.EncodeToString(tok)
		n, err = lpnode.NewNode(cfg)
		h = NewHost(env("LP_GROUP_DOMAIN", "g.kafumu.com"), func(string) store.Store { return store.Nop{} }, groupCfg)
	}
	if err != nil {
		log.Fatal(err)
	}
	n.SetSender(fed.New())
	n.Start()
	n.StartExpirySweeper(30*time.Second, make(chan struct{}))
	n.StartKeyRefresher(5*time.Minute, make(chan struct{}))
	if db != nil {
		if err := openGroups(ctx, db, h); err != nil {
			log.Fatal(err)
		}
	}

	addr := ":" + env("PORT", "8080")
	log.Printf("%s (%s) serving at %s, base %s", cfg.Name, cfg.CurrencySymbol, addr, base)
	mux := http.NewServeMux()
	// The daily dividend (PROTOCOL §10: issuance is driven by a scheduler,
	// never by request handling), for the node and every group: App Engine
	// cron calls this; the header is set only by cron (App Engine strips it
	// from outside requests).
	mux.HandleFunc("/cron/ud", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Appengine-Cron") != "true" {
			http.NotFound(w, r)
			return
		}
		issued, err := n.RunUD()
		if err != nil {
			log.Printf("ud: %v", err)
			http.Error(w, "ud failed", http.StatusInternalServerError)
			return
		}
		groups, err := h.RunUD()
		if err != nil {
			log.Printf("ud: groups: %v", err)
			http.Error(w, "ud failed", http.StatusInternalServerError)
			return
		}
		if db != nil {
			if gone, err := sweepGroups(r.Context(), db, h, time.Now()); err != nil {
				log.Printf("sweep groups: %v", err)
			} else if gone > 0 {
				log.Printf("sweep groups: %d deleted", gone)
			}
		}
		log.Printf("ud: issued %d micro-units; %d groups", issued, groups)
		w.Write([]byte("ok\n"))
	})
	mux.Handle("/lp/admin/", adminAPI(h, db, cfg.AdminToken))
	mux.Handle("/lp/g/", groupReads(h))
	mux.Handle("/", httpapi.Handler(n))
	log.Fatal(http.ListenAndServe(addr, mux))
}

// exitOnConflict: when another writer saved first, this process's state is
// stale; exiting lets App Engine start a fresh instance that loads the truth.
type exitOnConflict struct{ *DSStore }

func (e exitOnConflict) Save(data []byte) error {
	err := e.DSStore.Save(data)
	if err == ErrConflict {
		log.Fatalf("%v: exiting to reload", err)
	}
	return err
}
