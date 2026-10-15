package main

import (
	"sync"
	"testing"
	"time"

	lpnode "github.com/LaPingvino/liquiditypub/node"
	"github.com/LaPingvino/liquiditypub/node/ledger"
	"github.com/LaPingvino/liquiditypub/node/store"
)

func groupCfg(gid, name string) lpnode.Config {
	return lpnode.Config{Name: name, CurrencyName: "CoffeeBucks " + name, CurrencySymbol: "K$",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000, Transparency: "pseudonymous",
		AutoAcceptSeed: 100_000_000, AdminToken: "t"}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out: %s", what)
}

// Two Kafumu groups, each its own K$ node: dividends on both, a link at UD
// parity, and a payment from one group's member to the other's clears through
// the pool — in process, with the full signed protocol.
func TestHostTwoGroupsLinkAndPay(t *testing.T) {
	h := NewHost("g.kafumu.test", func(string) store.Store { return store.Nop{} }, groupCfg)
	a, err := h.Open("barreiro", "Barreiro")
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Open("arnhem", "Arnhem")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := h.Open("barreiro", "Barreiro"); again != a {
		t.Fatal("opening a group twice made two nodes")
	}
	if _, err := h.Open("Bad Id!", "x"); err == nil {
		t.Fatal("a bad group id was accepted")
	}
	for _, m := range []struct {
		n    *lpnode.Node
		name string
	}{{a, "ana"}, {a, "rui"}, {b, "bea"}} {
		if err := m.n.AddMember(m.name, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := h.RunUD(); err != nil || n != 2 {
		t.Fatalf("dividend in %d groups: %v", n, err)
	}
	ana := a.Balance(ledger.MemberPrefix + "ana")
	if ana <= 0 || b.Balance(ledger.MemberPrefix+"bea") <= 0 {
		t.Fatalf("no dividend: ana %d, bea %d", ana, b.Balance(ledger.MemberPrefix+"bea"))
	}
	// Within a group: a plain payment.
	if _, err := a.Pay("ana", "rui", ana/4); err != nil {
		t.Fatal(err)
	}
	// Across groups: link, then pay through the pool.
	if err := h.Link("barreiro", "arnhem", 100_000_000); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the link", func() bool { return a.ContactActive(h.Base("arnhem")) && b.ContactActive(h.Base("barreiro")) })
	bea := b.Balance(ledger.MemberPrefix + "bea")
	tid, err := a.StartTransfer(h.Base("arnhem"), "ana@"+a.Host(), "bea@"+b.Host(), ana/4, "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "settled on both sides", func() bool { return a.TransferState(tid) == "SETTLED" })
	waitFor(t, "bea paid", func() bool { return b.Balance(ledger.MemberPrefix+"bea") > bea })
	for _, n := range []*lpnode.Node{a, b} {
		if err := n.Ledger().VerifyChain(); err != nil {
			t.Fatalf("%s: %v", n.Host(), err)
		}
	}
}

// Two autonomous nodes paying each other at the same time, many times: the
// race the Go race detector can't see. The protocol must serialize it (one op
// in flight per contact, §6.3): every payment settles or is refused cleanly,
// both sides agree on the channel history, reserves mirror, and no money is
// made or lost.
func TestHostConcurrentCrossPayments(t *testing.T) {
	h := NewHost("g.kafumu.test", func(string) store.Store { return store.Nop{} }, groupCfg)
	a, _ := h.Open("lisboa", "Lisboa")
	b, _ := h.Open("porto", "Porto")
	for _, m := range []struct {
		n    *lpnode.Node
		name string
	}{{a, "ana"}, {b, "bea"}} {
		if err := m.n.AddMember(m.name, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		if _, err := h.RunUD(); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Link("lisboa", "porto", 100_000_000); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the link", func() bool { return a.ContactActive(h.Base("porto")) && b.ContactActive(h.Base("lisboa")) })
	supplyA, supplyB := a.Ledger().MoneySupply(), b.Ledger().MoneySupply()

	const each = 15
	type side struct {
		from, to *lpnode.Node
		payer    string
		payee    string
		peer     string
	}
	sides := []side{{a, b, "ana", "bea", "porto"}, {b, a, "bea", "ana", "lisboa"}}
	var mu sync.Mutex
	var ids []struct {
		n  *lpnode.Node
		id string
	}
	var wg sync.WaitGroup
	for _, s := range sides {
		wg.Add(1)
		go func(s side) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				for try := 0; try < 200; try++ { // busy (§6.3): wait for the op in flight
					id, err := s.from.StartTransfer(h.Base(s.peer), s.payer+"@"+s.from.Host(), s.payee+"@"+s.to.Host(), 100_000, "")
					if err == nil {
						mu.Lock()
						ids = append(ids, struct {
							n  *lpnode.Node
							id string
						}{s.from, id})
						mu.Unlock()
						break
					}
					time.Sleep(2 * time.Millisecond)
				}
			}
		}(s)
	}
	wg.Wait()
	waitFor(t, "every payment finished", func() bool {
		ca, _ := a.ContactByPeer(h.Base("porto"))
		cb, _ := b.ContactByPeer(h.Base("lisboa"))
		return !ca.Busy && !cb.Busy
	})
	settled := 0
	for _, x := range ids {
		if x.n.TransferState(x.id) == "SETTLED" {
			settled++
		}
	}
	if settled == 0 {
		t.Fatalf("none of %d payments settled", len(ids))
	}
	ca, _ := a.ContactByPeer(h.Base("porto"))
	cb, _ := b.ContactByPeer(h.Base("lisboa"))
	if ca.Diverged || cb.Diverged {
		t.Fatal("the contact diverged")
	}
	if ca.ChannelRoot != cb.ChannelRoot || ca.OpSeq != cb.OpSeq {
		t.Fatalf("channel history differs: %s/%d vs %s/%d", ca.ChannelRoot, ca.OpSeq, cb.ChannelRoot, cb.OpSeq)
	}
	if ca.MyReserveOfPeer != cb.PeerReserveOfMe || ca.PeerReserveOfMe != cb.MyReserveOfPeer {
		t.Fatalf("reserves don't mirror: %+v vs %+v", ca, cb)
	}
	if a.Ledger().MoneySupply() != supplyA || b.Ledger().MoneySupply() != supplyB {
		t.Fatal("payments changed a money supply")
	}
	for _, n := range []*lpnode.Node{a, b} {
		if err := n.Ledger().VerifyChain(); err != nil {
			t.Fatalf("%s: %v", n.Host(), err)
		}
	}
	t.Logf("%d payments started, %d settled, %d ops on the channel", len(ids), settled, ca.OpSeq)
}
