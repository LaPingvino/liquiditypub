package lpnode

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
	"github.com/LaPingvino/liquiditypub/node/ledger"
)

// Contact is one bilateral pool (PROTOCOL §6, §8). Both sides maintain an
// identical view of (proposerSeed, responderSeed, opSeq, channelRoot) and of
// both reserves, so pricing and reconciliation are deterministic functions of
// the shared operation history.
type Contact struct {
	ID       string
	PeerBase string
	PeerHost string

	IAmProposer bool
	Active      bool
	Closed      bool

	// Seeds are recorded in each currency once, unchanging, so the seed op (op 0)
	// hashes identically on both sides regardless of who we are (§8.2).
	ProposerSeed  int64 // proposing currency
	ResponderSeed int64 // responding currency

	// Pool reserves, both mirrored (§6.2). MyReserveOfPeer is authoritative on
	// our own ledger as node:<peer>; PeerReserveOfMe mirrors the peer's custody.
	MyReserveOfPeer int64 // our currency
	PeerReserveOfMe int64 // peer currency

	OpSeq       int64    // last committed operation index; the seed is op 0
	ChannelRoot [32]byte // folds in seed + every committed transfer

	// Busy holds the single in-flight operation (§6.3).
	Busy         bool
	BusyTransfer string

	// A reserve adjustment (§8.4) in flight on the proposer side: the delta is
	// applied to our ledger only once the peer accepts.
	PendingAdjustID    string
	PendingAdjustDelta int64
}

// channelRootB64 encodes the current root for checkpoints (§8.3).
func (c *Contact) channelRootB64() string {
	return base64.RawURLEncoding.EncodeToString(c.ChannelRoot[:])
}

// applySeed folds operation 0 into the channel root and sets opSeq=0 (§8.2).
func (c *Contact) applySeed() error {
	root0 := conformance.ChannelRoot0(c.ID)
	next, err := conformance.ChannelNext(root0, "seed", c.ID, c.ProposerSeed, c.ResponderSeed)
	if err != nil {
		return err
	}
	c.ChannelRoot = next
	c.OpSeq = 0
	return nil
}

// applyTransfer folds a committed transfer into the channel root and bumps
// opSeq. src/dst are in source and destination currency for that transfer
// (§8.2) — identical on both sides.
func (c *Contact) applyTransfer(transferID string, src, dst int64) error {
	next, err := conformance.ChannelNext(c.ChannelRoot, "transfer", transferID, src, dst)
	if err != nil {
		return err
	}
	c.ChannelRoot = next
	c.OpSeq++
	return nil
}

// applyAdjust folds a consensual reserve adjustment into the channel root and
// bumps opSeq (§8.4). The delta is the proposer-side reserve change; the tuple
// is identical on both sides, so the root stays consistent.
func (c *Contact) applyAdjust(adjustID string, delta int64) error {
	next, err := conformance.ChannelNext(c.ChannelRoot, "adjust", adjustID, delta, 0)
	if err != nil {
		return err
	}
	c.ChannelRoot = next
	c.OpSeq++
	return nil
}

// priceOutgoing computes dst_amount for a transfer we send (§6.2): we are the
// source, so R_src = our reserve of the peer, R_dst = the peer's reserve of us.
func (c *Contact) priceOutgoing(src int64) (int64, error) {
	return conformance.PoolPrice(c.MyReserveOfPeer, c.PeerReserveOfMe, src)
}

// priceIncoming computes dst_amount (our currency) for a transfer the peer
// sends us: the peer is the source, so R_src = the peer's reserve of us,
// R_dst = our reserve of the peer.
func (c *Contact) priceIncoming(src int64) (int64, error) {
	return conformance.PoolPrice(c.PeerReserveOfMe, c.MyReserveOfPeer, src)
}

// ── opening a contact ────────────────────────────────────────────────────────

// OpenContact proposes a new contact to peerBase, offering mySeed of our
// currency (§6.1). Returns the contact_id. The seed is applied on acceptance.
func (n *Node) OpenContact(peerBase string, mySeed int64, note string) (string, error) {
	if mySeed <= 0 {
		return "", fmt.Errorf("seed must be positive")
	}
	n.mu.Lock()
	peerHost := host(peerBase)
	if _, exists := n.contactByHost[peerHost]; exists {
		n.mu.Unlock()
		return "", fmt.Errorf("contact with %s already exists", peerHost)
	}
	id := newID()
	c := &Contact{
		ID:           id,
		PeerBase:     peerBase,
		PeerHost:     peerHost,
		IAmProposer:  true,
		ProposerSeed: mySeed,
	}
	n.contacts[id] = c
	n.contactByHost[peerHost] = c
	env := n.buildSigned("contact.propose", peerBase, "", map[string]any{
		"contact_id": id,
		"my_seed":    mySeed,
		"note":       note,
	})
	_ = n.persistLocked()
	n.mu.Unlock()
	n.dispatch(peerBase, env)
	return id, nil
}

// handleContactPropose is the responder side: auto-accept using our configured
// responder seed, apply our seed leg, and answer with contact.accept.
func (n *Node) handleContactPropose(env map[string]any) map[string]any {
	p, ok := payloadOf(env)
	if !ok {
		return n.errorReply(env, "malformed", "missing payload")
	}
	responderSeed := n.cfg.AutoAcceptSeed
	if responderSeed <= 0 {
		return n.errorReply(env, "refused", "node does not auto-accept contacts")
	}
	id := pStr(p, "contact_id")
	fromBase := envStr(env, "from")
	fromHost := host(fromBase)
	proposerSeed, ok := pInt(p, "my_seed")
	if !ok || proposerSeed <= 0 {
		return n.errorReply(env, "malformed", "invalid my_seed")
	}
	if _, exists := n.contactByHost[fromHost]; exists {
		return n.errorReply(env, "duplicate-contact", "already have a contact with "+fromHost)
	}
	c := &Contact{
		ID:            id,
		PeerBase:      fromBase,
		PeerHost:      fromHost,
		IAmProposer:   false,
		ProposerSeed:  proposerSeed,
		ResponderSeed: responderSeed,
	}
	// Apply our seed leg: node:<proposer> += responderSeed, from issuance (§6.1).
	if err := n.seedLedger(c.PeerHost, responderSeed); err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	c.MyReserveOfPeer = responderSeed // our currency, our custody
	c.PeerReserveOfMe = proposerSeed  // peer currency, mirrored
	if err := c.applySeed(); err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	c.Active = true
	n.contacts[id] = c
	n.contactByHost[fromHost] = c
	return n.buildSigned("contact.accept", fromBase, envStr(env, "id"), map[string]any{
		"contact_id": id,
		"my_seed":    responderSeed,
	})
}

// handleContactAccept is the proposer side: record the responder seed, apply
// our own seed leg, activate the contact.
func (n *Node) handleContactAccept(env map[string]any) map[string]any {
	p, ok := payloadOf(env)
	if !ok {
		return n.errorReply(env, "malformed", "missing payload")
	}
	id := pStr(p, "contact_id")
	c := n.contacts[id]
	if c == nil || !c.IAmProposer {
		return n.errorReply(env, "unknown-contact", "no matching pending contact")
	}
	if c.Active {
		return nil // idempotent: already active
	}
	responderSeed, ok := pInt(p, "my_seed")
	if !ok || responderSeed <= 0 {
		return n.errorReply(env, "malformed", "invalid my_seed")
	}
	c.ResponderSeed = responderSeed
	if err := n.seedLedger(c.PeerHost, c.ProposerSeed); err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	c.MyReserveOfPeer = c.ProposerSeed  // our currency, our custody
	c.PeerReserveOfMe = c.ResponderSeed // peer currency, mirrored
	if err := c.applySeed(); err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	c.Active = true
	return nil // acceptance needs no reply; both sides are now open
}

// seedLedger books our seed leg: our node wallet for the peer grows, sourced
// from `issuance` (freshly issued; a treasury variant is pure node policy, §6.1).
func (n *Node) seedLedger(peerHost string, amount int64) error {
	_, err := n.led.Append(ledger.Tx{
		ID:      newID(),
		Type:    ledger.TxSeed,
		Created: n.clock().Format(time.RFC3339),
		Entries: []ledger.Entry{
			{Account: ledger.NodeWalletPrefix + peerHost, Amount: amount},
			{Account: ledger.AcctIssuance, Amount: -amount},
		},
	})
	return err
}
