package lpnode_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
	lpnode "github.com/LaPingvino/liquiditypub/node"
	"github.com/LaPingvino/liquiditypub/node/ledger"
	"github.com/LaPingvino/liquiditypub/node/store"
)

// bus is an in-process Sender: it delivers envelopes by calling the target
// node's ProcessInbound directly, exercising the full protocol logic without
// HTTP. Delivery is synchronous within the caller's worker goroutine, matching
// the real transport's ordering guarantees.
type bus struct {
	byBase map[string]*lpnode.Node
}

func hostOf(base string) string {
	u, err := url.Parse(base)
	if err == nil && u.Host != "" {
		return u.Host
	}
	return base
}

func (b *bus) Deliver(peerBase string, env map[string]any) error {
	if n := b.byBase[peerBase]; n != nil {
		n.ProcessInbound(env)
	}
	return nil
}

func (b *bus) FetchIdentity(peerBase string) (map[string]any, error) {
	return b.byBase[peerBase].IdentityDoc(), nil
}

func (b *bus) FetchOutbox(peerBase, myHost string) ([]map[string]any, error) {
	if n := b.byBase[peerBase]; n != nil {
		return n.OutboxFor(myHost), nil
	}
	return nil, nil
}

func twoNodes(t *testing.T) (a, b *lpnode.Node, baseA, baseB string) {
	t.Helper()
	baseA, baseB = "https://riverside.example", "https://hilltop.example"
	var err error
	a, err = lpnode.NewNode(lpnode.Config{
		Base: baseA, Name: "Riverside", CurrencyName: "River", CurrencySymbol: "R",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000,
		AutoAcceptSeed: 500_000_000,
		Members:        []lpnode.MemberConfig{{Name: "alice", Grant: 100_000_000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err = lpnode.NewNode(lpnode.Config{
		Base: baseB, Name: "Hilltop", CurrencyName: "Hill", CurrencySymbol: "H",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000,
		AutoAcceptSeed: 500_000_000,
		Members:        []lpnode.MemberConfig{{Name: "bob", Grant: 100_000_000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	shared := &bus{byBase: map[string]*lpnode.Node{baseA: a, baseB: b}}
	a.SetSender(shared)
	b.SetSender(shared)
	a.Start()
	b.Start()
	return
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", d)
	}
}

func TestTwoNodeRoundTrip(t *testing.T) {
	a, b, baseA, baseB := twoNodes(t)

	if _, err := a.OpenContact(baseB, 500_000_000, "market overlap"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return a.ContactActive(baseB) && b.ContactActive(baseA)
	})

	// Replay the pool math independently to prove reserves are a pure function
	// of the op history (PROTOCOL §8.1) and that both nodes agree.
	// Riverside custodies node:hilltop = 500M (R); Riverside's reserve at
	// Hilltop = 500M (H). From Riverside's view: MyReserveOfPeer=500M,
	// PeerReserveOfMe=500M.
	rMyOfPeer, rPeerOfMe := int64(500_000_000), int64(500_000_000)

	// Transfer 1: alice -> bob, 10M R.
	src1 := int64(10_000_000)
	wantDst1, err := conformance.PoolPrice(rMyOfPeer, rPeerOfMe, src1)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), src1, "veggie box")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.TransferState(t1) == "SETTLED" })
	waitFor(t, 2*time.Second, func() bool { return b.TransferState(t1) == "SETTLED" })
	rMyOfPeer += src1
	rPeerOfMe -= wantDst1

	// Transfer 2: bob -> alice, 7M H. From Riverside's view it is incoming, so
	// the roles of the two reserves swap in the formula.
	src2 := int64(7_000_000)
	// Hilltop is source; its MyReserveOfPeer (node:riverside on Hilltop) and
	// PeerReserveOfMe mirror Riverside's two reserves.
	hMyOfPeer := rPeerOfMe // node:riverside on Hilltop == Riverside's reserve at Hilltop
	hPeerOfMe := rMyOfPeer // Hilltop's reserve at Riverside == node:hilltop on Riverside
	wantDst2, err := conformance.PoolPrice(hMyOfPeer, hPeerOfMe, src2)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := b.StartTransfer(baseA, "bob@"+b.Host(), "alice@"+a.Host(), src2, "return favor")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return b.TransferState(t2) == "SETTLED" })
	waitFor(t, 2*time.Second, func() bool { return a.TransferState(t2) == "SETTLED" })
	// Apply on Riverside's view: incoming grows PeerReserveOfMe, shrinks MyReserveOfPeer.
	rPeerOfMe += src2
	rMyOfPeer -= wantDst2

	// Node wallet (authoritative custody) must equal the replayed reserve.
	if got := a.Balance(ledger.NodeWalletPrefix + b.Host()); got != rMyOfPeer {
		t.Errorf("Riverside node:hilltop = %d, replay = %d", got, rMyOfPeer)
	}
	if got := b.Balance(ledger.NodeWalletPrefix + a.Host()); got != rPeerOfMe {
		t.Errorf("Hilltop node:riverside = %d, replay = %d", got, rPeerOfMe)
	}

	// Checkpoints must agree on op_seq and channel_root (reconciliation, §8.3).
	va, _ := a.ContactByPeer(baseB)
	vb, _ := b.ContactByPeer(baseA)
	if va.OpSeq != vb.OpSeq || va.OpSeq != 2 {
		t.Errorf("op_seq mismatch: A=%d B=%d (want 2)", va.OpSeq, vb.OpSeq)
	}
	if va.ChannelRoot != vb.ChannelRoot {
		t.Errorf("channel root divergence:\n A=%s\n B=%s", va.ChannelRoot, vb.ChannelRoot)
	}

	// Both ledgers: hash chain + conservation + non-negative node wallets.
	if err := a.Ledger().VerifyChain(); err != nil {
		t.Errorf("Riverside ledger: %v", err)
	}
	if err := b.Ledger().VerifyChain(); err != nil {
		t.Errorf("Hilltop ledger: %v", err)
	}
}

// TestReplayIdempotent re-delivers an already-processed envelope and asserts the
// receiver treats it as a duplicate without re-applying (PROTOCOL §4).
func TestReplayIdempotent(t *testing.T) {
	a, b, baseA, baseB := twoNodes(t)
	if _, err := a.OpenContact(baseB, 500_000_000, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.ContactActive(baseB) && b.ContactActive(baseA) })
	t1, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), 10_000_000, "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return b.TransferState(t1) == "SETTLED" })

	// Grab the commit envelope Riverside sent to Hilltop and replay it.
	var commit map[string]any
	for _, env := range a.OutboxFor(b.Host()) {
		if env["type"] == "transfer.commit" {
			commit = env
		}
	}
	if commit == nil {
		t.Fatal("no commit envelope found in outbox")
	}
	beforeLen := b.Ledger().Len()
	beforeReserve := b.Balance(ledger.NodeWalletPrefix + a.Host())

	if verdict := b.ProcessInbound(commit); verdict != conformance.VerdictDuplicate {
		t.Errorf("replayed commit verdict = %q, want duplicate", verdict)
	}
	if b.Ledger().Len() != beforeLen {
		t.Errorf("replay changed ledger length %d -> %d", beforeLen, b.Ledger().Len())
	}
	if got := b.Balance(ledger.NodeWalletPrefix + a.Host()); got != beforeReserve {
		t.Errorf("replay changed reserve %d -> %d", beforeReserve, got)
	}
}

// TestContactSerialization asserts a second transfer is refused while the first
// still holds the contact (PROTOCOL §6.3). We freeze delivery by using a sender
// that drops the propose, so the contact stays busy.
func TestContactSerialization(t *testing.T) {
	baseA, baseB := "https://a.example", "https://b.example"
	a, err := lpnode.NewNode(lpnode.Config{
		Base: baseA, Name: "A", CurrencyName: "A", CurrencySymbol: "A",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000, AutoAcceptSeed: 500_000_000,
		Members: []lpnode.MemberConfig{{Name: "alice", Grant: 100_000_000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := lpnode.NewNode(lpnode.Config{
		Base: baseB, Name: "B", CurrencyName: "B", CurrencySymbol: "B",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000, AutoAcceptSeed: 500_000_000,
		Members: []lpnode.MemberConfig{{Name: "bob", Grant: 100_000_000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	shared := &bus{byBase: map[string]*lpnode.Node{baseA: a, baseB: b}}
	a.SetSender(shared)
	b.SetSender(shared)
	a.Start()
	b.Start()
	if _, err := a.OpenContact(baseB, 500_000_000, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.ContactActive(baseB) })

	// Swap in a black-hole sender so the first propose never reaches B; the
	// contact on A stays busy.
	a.SetSender(blackhole{})
	if _, err := a.StartTransfer(baseB, "alice@a.example", "bob@b.example", 10_000_000, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartTransfer(baseB, "alice@a.example", "bob@b.example", 5_000_000, ""); err == nil {
		t.Error("second concurrent transfer should be refused (contact busy)")
	}
}

type blackhole struct{}

func (blackhole) Deliver(string, map[string]any) error                 { return nil }
func (blackhole) FetchIdentity(string) (map[string]any, error)         { return map[string]any{}, nil }
func (blackhole) FetchOutbox(string, string) ([]map[string]any, error) { return nil, nil }

// pullBus federates by pull only: Deliver is a no-op (no push), so the entire
// round trip is driven by fetching outboxes (§5.1).
type pullBus struct{ byBase map[string]*lpnode.Node }

func (pullBus) Deliver(string, map[string]any) error { return nil }
func (p pullBus) FetchIdentity(peerBase string) (map[string]any, error) {
	return p.byBase[peerBase].IdentityDoc(), nil
}
func (p pullBus) FetchOutbox(peerBase, myHost string) ([]map[string]any, error) {
	if n := p.byBase[peerBase]; n != nil {
		return n.OutboxFor(myHost), nil
	}
	return nil, nil
}

// TestPullOnlyFederation drives a full contact-open + transfer entirely over the
// pull baseline, with push disabled — proving §5.1 works standalone.
func TestPullOnlyFederation(t *testing.T) {
	baseA, baseB := "https://a.example", "https://b.example"
	mk := func(base, cur, member string) *lpnode.Node {
		n, err := lpnode.NewNode(lpnode.Config{
			Base: base, Name: cur, CurrencyName: cur, CurrencySymbol: cur[:1],
			CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000, AutoAcceptSeed: 500_000_000,
			Members: []lpnode.MemberConfig{{Name: member, Grant: 100_000_000}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	a, b := mk(baseA, "Aaa", "alice"), mk(baseB, "Bbb", "bob")
	pb := pullBus{byBase: map[string]*lpnode.Node{baseA: a, baseB: b}}
	a.SetSender(pb)
	b.SetSender(pb)
	a.Start()
	b.Start()

	// Both poll each other every tick; a bounded loop stands in for tickers.
	pump := func() {
		for i := 0; i < 60; i++ {
			b.PollPeer(baseA)
			a.PollPeer(baseB)
			time.Sleep(time.Millisecond)
		}
	}

	if _, err := a.OpenContact(baseB, 500_000_000, "pull only"); err != nil {
		t.Fatal(err)
	}
	pump()
	if !a.ContactActive(baseB) || !b.ContactActive(baseA) {
		t.Fatal("contact never activated over pull")
	}

	tid, err := a.StartTransfer(baseB, "alice@a.example", "bob@b.example", 10_000_000, "")
	if err != nil {
		t.Fatal(err)
	}
	pump()
	if a.TransferState(tid) != "SETTLED" || b.TransferState(tid) != "SETTLED" {
		t.Fatalf("transfer not settled over pull: A=%s B=%s",
			a.TransferState(tid), b.TransferState(tid))
	}
	va, _ := a.ContactByPeer(baseB)
	vb, _ := b.ContactByPeer(baseA)
	if va.ChannelRoot != vb.ChannelRoot || va.OpSeq != vb.OpSeq {
		t.Errorf("reconciliation mismatch over pull: A(%d,%s) B(%d,%s)",
			va.OpSeq, va.ChannelRoot, vb.OpSeq, vb.ChannelRoot)
	}
	if err := a.Ledger().VerifyChain(); err != nil {
		t.Error(err)
	}
	if err := b.Ledger().VerifyChain(); err != nil {
		t.Error(err)
	}
}

// TestRestartReplay persists a node through a contact + transfer, rebuilds it
// from the store, and asserts the resumed node has identical state and can
// keep transacting.
func TestRestartReplay(t *testing.T) {
	dir := t.TempDir()
	st := store.NewFile(dir + "/riverside.json")
	baseA, baseB := "https://riverside.example", "https://hilltop.example"
	cfgA := lpnode.Config{
		Base: baseA, Name: "Riverside", CurrencyName: "River", CurrencySymbol: "R",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000, AutoAcceptSeed: 500_000_000,
		Members: []lpnode.MemberConfig{{Name: "alice", Grant: 100_000_000}},
	}
	a, err := lpnode.Restore(cfgA, st) // fresh: store is empty
	if err != nil {
		t.Fatal(err)
	}
	b, err := lpnode.NewNode(lpnode.Config{
		Base: baseB, Name: "Hilltop", CurrencyName: "Hill", CurrencySymbol: "H",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000, AutoAcceptSeed: 500_000_000,
		Members: []lpnode.MemberConfig{{Name: "bob", Grant: 100_000_000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bus1 := &bus{byBase: map[string]*lpnode.Node{baseA: a, baseB: b}}
	a.SetSender(bus1)
	b.SetSender(bus1)
	a.Start()
	b.Start()

	if _, err := a.OpenContact(baseB, 500_000_000, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.ContactActive(baseB) && b.ContactActive(baseA) })
	t1, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), 10_000_000, "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.TransferState(t1) == "SETTLED" })

	// Snapshot the pre-restart state we expect to survive.
	wantReserve := a.Balance(ledger.NodeWalletPrefix + b.Host())
	wantView, _ := a.ContactByPeer(baseB)
	wantCP := a.Checkpoint()

	// Restart Riverside from the store as a brand-new object.
	a2, err := lpnode.Restore(cfgA, st)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := a2.Balance(ledger.NodeWalletPrefix + b.Host()); got != wantReserve {
		t.Errorf("reserve after restart = %d, want %d", got, wantReserve)
	}
	gotView, ok := a2.ContactByPeer(baseB)
	if !ok || gotView.OpSeq != wantView.OpSeq || gotView.ChannelRoot != wantView.ChannelRoot {
		t.Errorf("contact after restart = %+v, want opSeq=%d root=%s", gotView, wantView.OpSeq, wantView.ChannelRoot)
	}
	if a2.TransferState(t1) != "SETTLED" {
		t.Errorf("transfer state after restart = %q, want SETTLED", a2.TransferState(t1))
	}
	if err := a2.Ledger().VerifyChain(); err != nil {
		t.Errorf("restored ledger invalid: %v", err)
	}
	// Checkpoint log_hash must be byte-identical across the restart.
	if a2.Checkpoint()["log_hash"] != wantCP["log_hash"] {
		t.Errorf("log_hash changed across restart")
	}

	// Prove the resumed node still transacts: rewire the bus to a2 and send.
	bus2 := &bus{byBase: map[string]*lpnode.Node{baseA: a2, baseB: b}}
	a2.SetSender(bus2)
	b.SetSender(bus2)
	a2.Start()
	t2, err := a2.StartTransfer(baseB, "alice@"+a2.Host(), "bob@"+b.Host(), 5_000_000, "post-restart")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a2.TransferState(t2) == "SETTLED" })
	va, _ := a2.ContactByPeer(baseB)
	vb, _ := b.ContactByPeer(baseA)
	if va.OpSeq != vb.OpSeq || va.ChannelRoot != vb.ChannelRoot {
		t.Errorf("post-restart reconciliation mismatch: A(%d) B(%d)", va.OpSeq, vb.OpSeq)
	}
}

var _ = hostOf
