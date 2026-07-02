package lpnode_test

import (
	"sync"
	"testing"
	"time"

	lpnode "github.com/LaPingvino/liquiditypub/node"
)

// gateBus is an in-process Sender that can hold deliveries to a chosen base so a
// test can interleave the protocol with clock changes.
type gateBus struct {
	mu     sync.Mutex
	nodes  map[string]*lpnode.Node
	held   map[string]bool
	queued map[string][]map[string]any
}

func (g *gateBus) Deliver(base string, env map[string]any) error {
	g.mu.Lock()
	if g.held[base] {
		g.queued[base] = append(g.queued[base], env)
		g.mu.Unlock()
		return nil
	}
	n := g.nodes[base]
	g.mu.Unlock()
	if n != nil {
		n.ProcessInbound(env)
	}
	return nil
}

func (g *gateBus) queuedLen(base string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.queued[base])
}

func (g *gateBus) release(base string) {
	g.mu.Lock()
	q := g.queued[base]
	g.queued[base] = nil
	g.held[base] = false
	n := g.nodes[base]
	g.mu.Unlock()
	for _, e := range q {
		n.ProcessInbound(e)
	}
}

func (g *gateBus) FetchIdentity(base string) (map[string]any, error) {
	return g.nodes[base].IdentityDoc(), nil
}
func (g *gateBus) FetchOutbox(base, host string) ([]map[string]any, error) {
	if n := g.nodes[base]; n != nil {
		return n.OutboxFor(host), nil
	}
	return nil, nil
}
func (g *gateBus) FetchCheckpoint(base string) (map[string]any, error) {
	if n := g.nodes[base]; n != nil {
		return n.Checkpoint(), nil
	}
	return nil, nil
}

// TestPayerExpiryGuard proves the payer does not commit a transfer whose expiry
// has passed by the time the acceptance arrives: money must stay conserved and
// the transfer must end EXPIRED, not COMMITTED (§7.4). Without the guard the
// payer would append its leg one-sidedly while the payee had already expired.
func TestPayerExpiryGuard(t *testing.T) {
	baseA, baseB := "https://riverside.example", "https://hilltop.example"
	newN := func(base, name, sym, member string) *lpnode.Node {
		n, err := lpnode.NewNode(lpnode.Config{
			Base: base, Name: name, CurrencyName: sym, CurrencySymbol: sym,
			CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000,
			AutoAcceptSeed: 500_000_000,
			Members:        []lpnode.MemberConfig{{Name: member, Grant: 100_000_000}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	a := newN(baseA, "Riverside", "R", "alice")
	b := newN(baseB, "Hilltop", "H", "bob")

	// A shared, movable clock.
	var clkMu sync.Mutex
	cur := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { clkMu.Lock(); defer clkMu.Unlock(); return cur }
	advance := func(d time.Duration) { clkMu.Lock(); cur = cur.Add(d); clkMu.Unlock() }
	a.SetClock(clock)
	b.SetClock(clock)

	g := &gateBus{
		nodes:  map[string]*lpnode.Node{baseA: a, baseB: b},
		held:   map[string]bool{},
		queued: map[string][]map[string]any{},
	}
	a.SetSender(g)
	b.SetSender(g)
	a.Start()
	b.Start()

	if _, err := a.OpenContact(baseB, 500_000_000, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.ContactActive(baseB) && b.ContactActive(baseA) })

	aliceBefore := a.Balance("m:alice")

	// Hold acceptances destined for A, so the accept can't be processed until we
	// have moved the clock past the transfer's one-hour expiry.
	g.mu.Lock()
	g.held[baseA] = true
	g.mu.Unlock()

	tid, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), 10_000_000, "late")
	if err != nil {
		t.Fatal(err)
	}
	// Wait until B's acceptance is queued at A's gate.
	waitFor(t, 2*time.Second, func() bool { return g.queuedLen(baseA) > 0 })

	// Move past expiry, then let the acceptance through.
	advance(2 * time.Hour)
	g.release(baseA)

	waitFor(t, 2*time.Second, func() bool { return a.TransferState(tid) == "EXPIRED" })

	if got := a.Balance("m:alice"); got != aliceBefore {
		t.Errorf("alice balance changed on an expired transfer: got %d, want %d (money not conserved)", got, aliceBefore)
	}
	if view, ok := a.ContactByPeer(baseB); !ok || view.Busy {
		t.Errorf("contact still busy after expiry guard fired: %+v", view)
	}
}
