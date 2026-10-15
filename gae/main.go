// Command gae serves a LiquidityPub node on Google App Engine (Profile B,
// PROTOCOL §12; beads joop-3mu). First user: Kafumu's CoffeeBucks (K$),
// deployed as the service "lp" behind kafumu.com.
//
// Configuration comes from the environment (app.yaml), never secrets:
//
//	LP_BASE      public origin, e.g. https://kafumu.com
//	LP_NAME      node name
//	LP_DESC      one-line description
//	LP_CURRENCY  currency name (CoffeeBucks)
//	LP_SYMBOL    currency symbol (K$)
//	PORT         set by App Engine
//
// The admin token and the node key will live in Datastore (next step), so
// nothing secret is ever in app.yaml or git. Until then the admin API is
// closed: with no token configured this binary refuses to start the admin
// surface rather than leave it open (Config.AdminToken empty = open demo API).
package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"time"

	lpnode "github.com/LaPingvino/liquiditypub/node"
	"github.com/LaPingvino/liquiditypub/node/fed"
	"github.com/LaPingvino/liquiditypub/node/httpapi"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	base := env("LP_BASE", "http://localhost:"+env("PORT", "8080"))
	// A random admin token per process until the token lives in Datastore:
	// the admin API exists but nobody can use it, which is the safe default.
	tok := make([]byte, 32)
	rand.Read(tok)
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
		AdminToken:     hex.EncodeToString(tok),
	}
	n, err := lpnode.NewNode(cfg)
	if err != nil {
		log.Fatal(err)
	}
	n.SetSender(fed.New())
	n.Start()
	n.StartExpirySweeper(30*time.Second, make(chan struct{}))
	n.StartKeyRefresher(5*time.Minute, make(chan struct{}))

	addr := ":" + env("PORT", "8080")
	log.Printf("%s (%s) serving at %s, base %s", cfg.Name, cfg.CurrencySymbol, addr, base)
	log.Fatal(http.ListenAndServe(addr, httpapi.Handler(n)))
}
