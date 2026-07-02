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

func (b *bus) FetchCheckpoint(peerBase string) (map[string]any, error) {
	if n := b.byBase[peerBase]; n != nil {
		return n.Checkpoint(), nil
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
func (blackhole) FetchCheckpoint(string) (map[string]any, error)       { return map[string]any{}, nil }

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
func (p pullBus) FetchCheckpoint(peerBase string) (map[string]any, error) {
	if n := p.byBase[peerBase]; n != nil {
		return n.Checkpoint(), nil
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

// TestReserveAdjust exercises consensual liquidity top-up and withdrawal
// (PROTOCOL §8.4): both sides mirror the reserve change, fold it into the
// channel hash, and stay reconciled.
func TestReserveAdjust(t *testing.T) {
	a, b, baseA, baseB := twoNodes(t)
	if _, err := a.OpenContact(baseB, 500_000_000, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.ContactActive(baseB) && b.ContactActive(baseA) })

	// Top up: Riverside adds 100M of its own liquidity to the pool.
	if _, err := a.AdjustReserve(baseB, 100_000_000, "market day top-up"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := a.ContactByPeer(baseB)
		return v.OpSeq == 1 && !v.Busy
	})
	va, _ := a.ContactByPeer(baseB)
	vb, _ := b.ContactByPeer(baseA)
	if va.MyReserveOfPeer != 600_000_000 {
		t.Errorf("A reserve after top-up = %d, want 600M", va.MyReserveOfPeer)
	}
	if vb.PeerReserveOfMe != 600_000_000 {
		t.Errorf("B mirror after top-up = %d, want 600M", vb.PeerReserveOfMe)
	}
	if va.ChannelRoot != vb.ChannelRoot || va.OpSeq != vb.OpSeq {
		t.Errorf("reconciliation mismatch after top-up")
	}
	if got := a.Balance(ledger.NodeWalletPrefix + b.Host()); got != 600_000_000 {
		t.Errorf("A node wallet = %d, want 600M", got)
	}
	if got := a.Balance(ledger.AcctTreasury); got != -100_000_000 {
		t.Errorf("A treasury = %d, want -100M", got)
	}

	// Withdraw 50M back to treasury.
	if _, err := a.AdjustReserve(baseB, -50_000_000, "end of day"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, _ := a.ContactByPeer(baseB)
		return v.OpSeq == 2 && !v.Busy
	})
	va, _ = a.ContactByPeer(baseB)
	vb, _ = b.ContactByPeer(baseA)
	if va.MyReserveOfPeer != 550_000_000 || vb.PeerReserveOfMe != 550_000_000 {
		t.Errorf("reserves after withdrawal: A=%d B=%d, want 550M", va.MyReserveOfPeer, vb.PeerReserveOfMe)
	}
	if va.ChannelRoot != vb.ChannelRoot {
		t.Errorf("channel root divergence after withdrawal")
	}

	// Over-withdrawal is refused.
	if _, err := a.AdjustReserve(baseB, -600_000_000, "too much"); err == nil {
		t.Error("expected over-withdrawal to be refused")
	}

	// The pool still prices and transfers correctly against the new reserves.
	tid, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), 10_000_000, "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.TransferState(tid) == "SETTLED" })
	if err := a.Ledger().VerifyChain(); err != nil {
		t.Error(err)
	}
	if err := b.Ledger().VerifyChain(); err != nil {
		t.Error(err)
	}
}

// cpSender wraps a bus but can serve a tampered checkpoint for one peer, to
// exercise divergence detection deterministically.
type cpSender struct {
	inner *bus
	faked map[string]map[string]any // peerBase -> tampered checkpoint
}

func (c cpSender) Deliver(p string, e map[string]any) error { return c.inner.Deliver(p, e) }
func (c cpSender) FetchIdentity(p string) (map[string]any, error) {
	return c.inner.FetchIdentity(p)
}
func (c cpSender) FetchOutbox(p, h string) ([]map[string]any, error) {
	return c.inner.FetchOutbox(p, h)
}
func (c cpSender) FetchCheckpoint(p string) (map[string]any, error) {
	if cp, ok := c.faked[p]; ok {
		return cp, nil
	}
	return c.inner.FetchCheckpoint(p)
}

// TestCheckpointDivergence freezes a contact when the peer's checkpoint
// contradicts ours at the same op_seq, and leaves it alone when they agree
// (PROTOCOL §8.3).
func TestCheckpointDivergence(t *testing.T) {
	a, b, baseA, baseB := twoNodes(t)
	if _, err := a.OpenContact(baseB, 500_000_000, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.ContactActive(baseB) && b.ContactActive(baseA) })
	tid, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), 10_000_000, "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.TransferState(tid) == "SETTLED" })

	va, _ := a.ContactByPeer(baseB)

	// Honest checkpoint (real bus) → no divergence.
	if res, err := a.ReconcilePeer(baseB); err != nil || res.Diverged {
		t.Fatalf("honest reconcile flagged divergence: %+v (err %v)", res, err)
	}
	if v, _ := a.ContactByPeer(baseB); v.Diverged {
		t.Fatal("contact wrongly frozen against honest peer")
	}

	// Tampered checkpoint: same op_seq, different channel root → freeze.
	sender := cpSender{
		inner: &bus{byBase: map[string]*lpnode.Node{baseA: a, baseB: b}},
		faked: map[string]map[string]any{
			baseB: {
				"contacts": []any{
					map[string]any{
						"contact_id":   va.ID,
						"op_seq":       va.OpSeq,
						"channel_root": "TAMPERED_ROOT_VALUE",
					},
				},
			},
		},
	}
	a.SetSender(sender)
	res, err := a.ReconcilePeer(baseB)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Diverged {
		t.Errorf("expected divergence, got %+v", res)
	}
	if v, _ := a.ContactByPeer(baseB); !v.Diverged {
		t.Error("contact not frozen after divergence")
	}
	// New operations are refused on a frozen contact.
	if _, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), 1_000_000, ""); err == nil {
		t.Error("transfer on a frozen (diverged) contact should be refused")
	}
}

// TestTransferExpiry asserts a stalled pre-commit transfer expires and releases
// the contact lock (PROTOCOL §7.1, §7.4).
func TestTransferExpiry(t *testing.T) {
	a, b, baseA, baseB := twoNodes(t)
	if _, err := a.OpenContact(baseB, 500_000_000, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.ContactActive(baseB) && b.ContactActive(baseA) })

	// Black-hole the sender so the proposal never reaches b: the transfer stays
	// PROPOSED and the contact stays busy.
	a.SetSender(blackhole{})
	tid, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), 10_000_000, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := a.ContactByPeer(baseB); !v.Busy {
		t.Fatal("contact should be busy with the in-flight transfer")
	}

	// Advance the clock past the 1h expiry and sweep.
	future := time.Now().UTC().Add(2 * time.Hour)
	a.SetClock(func() time.Time { return future })
	if n := a.SweepExpired(); n != 1 {
		t.Fatalf("swept %d transfers, want 1", n)
	}
	if st := a.TransferState(tid); st != "EXPIRED" {
		t.Errorf("transfer state = %q, want EXPIRED", st)
	}
	if v, _ := a.ContactByPeer(baseB); v.Busy {
		t.Error("contact still busy after expiry")
	}
	// The contact is usable again: a fresh transfer can start (it will price and
	// lock, even though delivery is still black-holed).
	if _, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), 5_000_000, ""); err != nil {
		t.Errorf("contact not reusable after expiry: %v", err)
	}
}

// TestKeyRotation rotates a node's signing key and asserts the identity doc
// lists both keys, an announcement is emitted, and the peer accepts messages
// signed by the new key (PROTOCOL §3).
func TestKeyRotation(t *testing.T) {
	a, b, baseA, baseB := twoNodes(t)
	if _, err := a.OpenContact(baseB, 500_000_000, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.ContactActive(baseB) && b.ContactActive(baseA) })

	if keys := a.IdentityDoc()["keys"].([]any); len(keys) != 1 {
		t.Fatalf("pre-rotation key count = %d, want 1", len(keys))
	}
	newID, err := a.RotateKey()
	if err != nil {
		t.Fatal(err)
	}
	if newID != "#nk2" {
		t.Errorf("new key id = %q, want #nk2", newID)
	}
	if keys := a.IdentityDoc()["keys"].([]any); len(keys) != 2 {
		t.Errorf("post-rotation key count = %d, want 2", len(keys))
	}
	// A key.announce was queued to the peer.
	sawAnnounce := false
	for _, e := range a.OutboxFor(b.Host()) {
		if e["type"] == "key.announce" {
			sawAnnounce = true
		}
	}
	if !sawAnnounce {
		t.Error("no key.announce emitted to peer")
	}

	// A transfer after rotation is signed by the new key and accepted by b.
	tid, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), 10_000_000, "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return b.TransferState(tid) == "SETTLED" })
	// Confirm the propose really used #nk2.
	usedNewKey := false
	for _, e := range a.OutboxFor(b.Host()) {
		if e["type"] == "transfer.propose" {
			if sig, ok := e["sig"].(map[string]any); ok {
				if k, _ := sig["key"].(string); len(k) >= 4 && k[len(k)-4:] == "#nk2" {
					usedNewKey = true
				}
			}
		}
	}
	if !usedNewKey {
		t.Error("transfer.propose was not signed by the rotated key")
	}
}

// TestKeyringPersists rotates a key, restarts the node from its store, and
// asserts both keys survive and the rotated key is still active.
func TestKeyringPersists(t *testing.T) {
	dir := t.TempDir()
	st := store.NewFile(dir + "/n.json")
	cfg := lpnode.Config{
		Base: "https://n.example", Name: "N", CurrencyName: "N", CurrencySymbol: "N",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000, AutoAcceptSeed: 1,
		Members: []lpnode.MemberConfig{{Name: "alice", Grant: 1_000_000}},
	}
	n, err := lpnode.Restore(cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	n.SetSender(blackhole{})
	n.Start()
	if _, err := n.RotateKey(); err != nil { // no contacts: just add + switch
		t.Fatal(err)
	}

	n2, err := lpnode.Restore(cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	if keys := n2.IdentityDoc()["keys"].([]any); len(keys) != 2 {
		t.Fatalf("key count after restart = %d, want 2", len(keys))
	}
	sig := n2.Checkpoint()["sig"].(map[string]any)
	if k, _ := sig["key"].(string); len(k) < 4 || k[len(k)-4:] != "#nk2" {
		t.Errorf("active key after restart = %q, want ...#nk2", sig["key"])
	}
}

// TestContactClose freezes a contact and asserts new operations are refused
// while reserves survive (PROTOCOL §6).
func TestContactClose(t *testing.T) {
	a, b, baseA, baseB := twoNodes(t)
	if _, err := a.OpenContact(baseB, 500_000_000, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return a.ContactActive(baseB) && b.ContactActive(baseA) })

	if err := a.CloseContact(baseB, "season over"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		v, ok := b.ContactByPeer(baseA)
		return ok && !b.ContactActive(baseA) && v.MyReserveOfPeer == 500_000_000
	})
	// New transfers refused on both ends.
	if _, err := a.StartTransfer(baseB, "alice@"+a.Host(), "bob@"+b.Host(), 1_000_000, ""); err == nil {
		t.Error("transfer on a closed contact should be refused (proposer side)")
	}
	if _, err := a.AdjustReserve(baseB, 1_000_000, ""); err == nil {
		t.Error("reserve.adjust on a closed contact should be refused")
	}
	// Reserves still present (survive until withdrawn by consent).
	if got := a.Balance(ledger.NodeWalletPrefix + b.Host()); got != 500_000_000 {
		t.Errorf("reserve after close = %d, want 500M (survives)", got)
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
