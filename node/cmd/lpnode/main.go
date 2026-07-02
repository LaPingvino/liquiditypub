// Command lpnode is the Profile-A reference node (PROTOCOL §12). It has two
// modes:
//
//	lpnode serve  -addr 127.0.0.1:8080 -base http://127.0.0.1:8080 ...
//	lpnode demo                         # two in-process nodes, full round trip
//
// `demo` is the executable proof of DoD items 4–5: two nodes open a seeded
// contact, transfer both directions, and their checkpoints agree on channel
// root and op_seq while every ledger invariant holds.
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/LaPingvino/liquiditypub/conformance/runner"
	lpnode "github.com/LaPingvino/liquiditypub/node"
	"github.com/LaPingvino/liquiditypub/node/fed"
	"github.com/LaPingvino/liquiditypub/node/httpapi"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: lpnode <serve|demo> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "demo":
		if err := runDemo(); err != nil {
			log.Fatal(err)
		}
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runDemo() error {
	// Two nodes, each with one member. Bases are assigned after listeners bind,
	// so build with placeholders then... instead, bind first, then build.
	lnA, _ := net.Listen("tcp", "127.0.0.1:0")
	lnB, _ := net.Listen("tcp", "127.0.0.1:0")
	baseA := "http://" + lnA.Addr().String()
	baseB := "http://" + lnB.Addr().String()

	nodeA, err := lpnode.NewNode(lpnode.Config{
		Base: baseA, Name: "Riverside", Description: "demo",
		CurrencyName: "River Credits", CurrencySymbol: "R",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000,
		AutoAcceptSeed: 500_000_000,
		Members:        []lpnode.MemberConfig{{Name: "alice", Grant: 100_000_000}},
	})
	if err != nil {
		return err
	}
	nodeB, err := lpnode.NewNode(lpnode.Config{
		Base: baseB, Name: "Hilltop", Description: "demo",
		CurrencyName: "Hill Credits", CurrencySymbol: "H",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000,
		AutoAcceptSeed: 500_000_000,
		Members:        []lpnode.MemberConfig{{Name: "bob", Grant: 100_000_000}},
	})
	if err != nil {
		return err
	}
	nodeA.SetSender(fed.New())
	nodeB.SetSender(fed.New())
	nodeA.Start()
	nodeB.Start()
	go (&http.Server{Handler: httpapi.Handler(nodeA)}).Serve(lnA)
	go (&http.Server{Handler: httpapi.Handler(nodeB)}).Serve(lnB)

	fmt.Printf("Riverside %s   Hilltop %s\n\n", baseA, baseB)

	// 1. Open a contact: Riverside proposes, Hilltop auto-accepts.
	cid, err := nodeA.OpenContact(baseB, 500_000_000, "demo market overlap")
	if err != nil {
		return err
	}
	if err := waitFor(3*time.Second, func() bool {
		return nodeA.ContactActive(baseB) && nodeB.ContactActive(baseA)
	}); err != nil {
		return fmt.Errorf("contact never activated: %w", err)
	}
	fmt.Printf("contact opened: %s\n", cid)

	// 2. Transfer alice -> bob (Riverside -> Hilltop).
	t1, err := nodeA.StartTransfer(baseB, "alice@"+nodeA.Host(), "bob@"+nodeB.Host(), 10_000_000, "veggie box")
	if err != nil {
		return err
	}
	if err := waitFor(3*time.Second, func() bool { return nodeA.TransferState(t1) == "SETTLED" }); err != nil {
		return fmt.Errorf("transfer 1 stuck in %s", nodeA.TransferState(t1))
	}
	fmt.Printf("transfer 1 (R->H) settled: %s\n", t1)

	// 3. Transfer bob -> alice (Hilltop -> Riverside).
	t2, err := nodeB.StartTransfer(baseA, "bob@"+nodeB.Host(), "alice@"+nodeA.Host(), 7_000_000, "return favor")
	if err != nil {
		return err
	}
	if err := waitFor(3*time.Second, func() bool { return nodeB.TransferState(t2) == "SETTLED" }); err != nil {
		return fmt.Errorf("transfer 2 stuck in %s", nodeB.TransferState(t2))
	}
	fmt.Printf("transfer 2 (H->R) settled: %s\n\n", t2)

	// 4. Black-box conformance: read surface + cross-check, over real HTTP.
	failed := 0
	report := func(rs []runner.Result) {
		for _, r := range rs {
			mark := "ok  "
			if !r.OK {
				mark, failed = "FAIL", failed+1
			}
			note := ""
			if r.Note != "" {
				note = "  (" + r.Note + ")"
			}
			fmt.Printf("  %s %s%s\n", mark, r.Check, note)
		}
	}
	fmt.Println("lpconform " + baseA)
	rsA, _, cpA := runner.CheckNode(baseA)
	report(rsA)
	fmt.Println("lpconform " + baseB)
	rsB, _, cpB := runner.CheckNode(baseB)
	report(rsB)
	if cpA != nil && cpB != nil {
		fmt.Println("cross-check")
		report(runner.CrossCheck(cpA, cpB))
	}

	// 5. Ledger invariants.
	for name, n := range map[string]*lpnode.Node{"Riverside": nodeA, "Hilltop": nodeB} {
		if err := n.Ledger().VerifyChain(); err != nil {
			return fmt.Errorf("%s ledger invariant: %w", name, err)
		}
	}
	fmt.Println("\nledger chains + conservation verified on both nodes")
	if failed > 0 {
		return fmt.Errorf("%d conformance check(s) failed", failed)
	}
	fmt.Println("ALL GREEN")
	return nil
}

func waitFor(d time.Duration, cond func() bool) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	if cond() {
		return nil
	}
	return fmt.Errorf("condition not met within %s", d)
}
