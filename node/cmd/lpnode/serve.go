package main

import (
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	lpnode "github.com/LaPingvino/liquiditypub/node"
	"github.com/LaPingvino/liquiditypub/node/fed"
	"github.com/LaPingvino/liquiditypub/node/httpapi"
	"github.com/LaPingvino/liquiditypub/node/store"
)

// runServe starts one long-running node, reachable at -base, so lpconform and
// peers can federate with it (PROTOCOL §12, Profile A).
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address")
	base := fs.String("base", "", "public base URL (default http://<addr>)")
	name := fs.String("name", "Node", "node display name")
	currency := fs.String("currency", "Credits", "currency name")
	symbol := fs.String("symbol", "C", "currency symbol")
	members := fs.String("members", "alice:100000000", "comma list of name:grant")
	seed := fs.Int64("seed", 500_000_000, "auto-accept seed (our currency)")
	peer := fs.String("peer", "", "on startup, open a contact to this peer base URL")
	udTick := fs.Bool("ud", false, "issue one UD period on startup")
	pull := fs.Duration("pull", 0, "if >0, poll peer outboxes at this cadence (pull baseline, §5.1)")
	state := fs.String("state", "", "path to a JSON state file for crash-safe persistence + resume")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" {
		*base = "http://" + *addr
	}

	var mcfg []lpnode.MemberConfig
	for _, spec := range strings.Split(*members, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		nm, grant := spec, int64(0)
		if i := strings.IndexByte(spec, ':'); i >= 0 {
			nm = spec[:i]
			grant, _ = strconv.ParseInt(spec[i+1:], 10, 64)
		}
		mcfg = append(mcfg, lpnode.MemberConfig{Name: nm, Grant: grant})
	}

	cfg := lpnode.Config{
		Base: *base, Name: *name, Description: *name + " (lpnode serve)",
		CurrencyName: *currency, CurrencySymbol: *symbol,
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000,
		AutoAcceptSeed: *seed, Members: mcfg,
	}
	var n *lpnode.Node
	var err error
	if *state != "" {
		// Resume from the state file if it exists, else create + persist.
		n, err = lpnode.Restore(cfg, store.NewFile(*state))
		if err != nil {
			return err
		}
		fmt.Printf("  state: persisting to %s\n", *state)
	} else {
		n, err = lpnode.NewNode(cfg)
		if err != nil {
			return err
		}
	}
	n.SetSender(fed.New())
	n.Start()
	// Always sweep expired transfers so a dropped accept/commit can't pin a
	// contact busy past its expiry (§7.4).
	n.StartExpirySweeper(30*time.Second, make(chan struct{}))
	// Enforce peer key revocation on the inbox path too, independent of pull (§3, §13).
	n.StartKeyRefresher(5*time.Minute, make(chan struct{}))

	if *udTick {
		if _, err := n.RunUD(); err != nil {
			return err
		}
	}
	if *pull > 0 {
		n.StartPulling(*pull, make(chan struct{}))
		fmt.Printf("  pull: polling peer outboxes every %s\n", *pull)
	}
	if *peer != "" {
		go func() {
			time.Sleep(200 * time.Millisecond)
			if _, err := n.OpenContact(*peer, *seed, "serve --peer"); err != nil {
				fmt.Printf("open contact to %s: %v\n", *peer, err)
			}
		}()
	}

	fmt.Printf("%s serving at %s (base %s)\n", *name, *addr, *base)
	fmt.Printf("  identity:   %s/.well-known/liquiditypub\n", *base)
	fmt.Printf("  checkpoint: %s/lp/checkpoint.json\n", *base)
	return http.ListenAndServe(*addr, httpapi.Handler(n))
}
