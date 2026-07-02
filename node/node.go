package lpnode

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
	"github.com/LaPingvino/liquiditypub/node/ledger"
	"github.com/LaPingvino/liquiditypub/node/store"
)

// Clock lets tests pin time; production uses realClock.
type Clock func() time.Time

func realClock() time.Time { return time.Now().UTC() }

// Member is an active account (PROTOCOL §11).
type Member struct {
	Name        string
	DisplayName string
	Weight      int64 // micro-weight; default 1_000_000
	Active      bool
}

// ownKey is one of this node's signing keys (PROTOCOL §3). A node keeps a
// keyring so it can rotate: a new key is announced by a still-valid old one,
// and retired keys stay listed (and verifiable) until revoked.
type ownKey struct {
	LocalID string // e.g. "#nk1"
	Seed    string // base64url ed25519 seed
	Created string
	Revoked string // "" while valid; RFC3339 once revoked
	priv    ed25519.PrivateKey
	pub     ed25519.PublicKey
}

// Sender delivers an outbound envelope to a peer. The HTTP runtime supplies a
// real implementation (POST to the peer inbox); tests may supply an in-process
// one. Returning an error only affects retry/logging — protocol correctness is
// enforced by idempotency and checkpoint reconciliation, not delivery success.
type Sender interface {
	Deliver(peerBase string, envelope map[string]any) error
	// FetchIdentity retrieves and returns a peer's identity document (§3).
	FetchIdentity(peerBase string) (map[string]any, error)
	// FetchOutbox retrieves the envelopes a peer has addressed to myHost,
	// ordered by seq (§5.1, the mandatory pull baseline).
	FetchOutbox(peerBase, myHost string) ([]map[string]any, error)
	// FetchCheckpoint retrieves a peer's signed checkpoint (§8.3), used for
	// channel-root/op_seq reconciliation.
	FetchCheckpoint(peerBase string) (map[string]any, error)
}

// Node is a running LiquidityPub node. All mutating protocol logic runs under
// mu, so a contact is naturally serialized (PROTOCOL §6.3).
type Node struct {
	cfg        Config
	priv       ed25519.PrivateKey // the active signing key
	pub        ed25519.PublicKey
	keyLocalID string    // active key's local id, e.g. "#nk1"
	ownKeys    []*ownKey // full keyring (active + retired-but-valid + revoked)
	created    string    // key/node creation timestamp
	clock      Clock

	mu  sync.Mutex
	led *ledger.Ledger

	members map[string]*Member // local name -> member

	contacts      map[string]*Contact // contact_id -> contact
	contactByHost map[string]*Contact // peer host -> contact

	transfers map[string]*Transfer // transfer_id -> transfer

	// Per-channel envelope bookkeeping (§4). Keyed by the *other* host.
	outSeq   map[string]int64             // my next outgoing seq to a peer (last used)
	inState  map[string]*channelInbound   // validation state for peer -> me
	peerKeys map[string]ed25519.PublicKey // fully-qualified key id -> public key

	// Per-peer outbox: ordered envelopes addressed to that peer (§5.1).
	outbox map[string][]map[string]any // peer host -> envelopes
	pushed map[string]int64            // peer host -> highest seq successfully pushed

	currentUD int64 // last published standard-weight dividend

	send    Sender
	pushSig chan struct{} // coalescing wakeup for the push worker (nil until Start)
	store   store.Store   // persistence backend (nil = no durability)
}

type channelInbound struct {
	seenIDs map[string]bool
	lastSeq int64
	// replies caches the response we sent for an already-processed message id,
	// so a duplicate inbound is answered identically (idempotent replay, §4).
	replies map[string]map[string]any
}

// NewNode builds a node, applies genesis member grants, and computes the first
// UD figure. It does not start any network listener; wire a Sender with
// SetSender and serve HTTP with the httpapi package.
func NewNode(cfg Config) (*Node, error) {
	if cfg.Base == "" {
		return nil, fmt.Errorf("config: Base is required")
	}
	if cfg.Transparency == "" {
		cfg.Transparency = "pseudonymous"
	}
	priv := cfg.PrivKey
	if priv == nil {
		_, p, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		priv = p
	}
	n := &Node{
		cfg:           cfg,
		priv:          priv,
		pub:           priv.Public().(ed25519.PublicKey),
		keyLocalID:    "#nk1",
		clock:         realClock,
		led:           ledger.New(),
		members:       map[string]*Member{},
		contacts:      map[string]*Contact{},
		contactByHost: map[string]*Contact{},
		transfers:     map[string]*Transfer{},
		outSeq:        map[string]int64{},
		inState:       map[string]*channelInbound{},
		peerKeys:      map[string]ed25519.PublicKey{},
		outbox:        map[string][]map[string]any{},
		pushed:        map[string]int64{},
	}
	n.created = n.clock().Format(time.RFC3339)

	// Seed the keyring with the initial active key.
	n.ownKeys = []*ownKey{{
		LocalID: n.keyLocalID, Seed: b64(priv.Seed()), Created: n.created,
		priv: priv, pub: n.pub,
	}}
	// Register my own key so self-verification and identity export line up.
	n.peerKeys[keyID(cfg.Base, n.keyLocalID)] = n.pub

	// Genesis: activate members and grant initial balances (§10 note: a
	// convenience seed; ongoing issuance is the scheduler's job).
	for _, m := range cfg.Members {
		if err := validMemberName(m.Name); err != nil {
			return nil, err
		}
		n.members[m.Name] = &Member{
			Name: m.Name, DisplayName: m.DisplayName, Weight: 1_000_000, Active: true,
		}
		if m.Grant > 0 {
			_, err := n.led.Append(ledger.Tx{
				ID:      newID(),
				Type:    ledger.TxIssuanceGrant,
				Created: n.created,
				Entries: []ledger.Entry{
					{Account: ledger.MemberPrefix + m.Name, Amount: m.Grant},
					{Account: ledger.AcctIssuance, Amount: -m.Grant},
				},
			})
			if err != nil {
				return nil, fmt.Errorf("genesis grant %s: %w", m.Name, err)
			}
		}
	}
	n.currentUD = n.computeUDBase()
	return n, nil
}

// SetSender installs the delivery backend (HTTP or in-process).
func (n *Node) SetSender(s Sender) {
	n.mu.Lock()
	n.send = s
	n.mu.Unlock()
}

// sender returns the delivery backend under lock.
func (n *Node) sender() Sender {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.send
}

// SetClock overrides the time source (tests).
func (n *Node) SetClock(c Clock) {
	n.mu.Lock()
	n.clock = c
	n.mu.Unlock()
}

// Base returns the node origin.
func (n *Node) Base() string { return n.cfg.Base }

// Ledger exposes the ledger for tests/invariant checks (read-only intent).
func (n *Node) Ledger() *ledger.Ledger { return n.led }

func (n *Node) udWeightTotal() int64 {
	var t int64
	for _, m := range n.members {
		if m.Active {
			t += m.Weight
		}
	}
	return t
}

// computeUDBase runs the reference UD formula (delegated to conformance),
// flooring at GenesisUD as node policy while supply is small (§10).
func (n *Node) computeUDBase() int64 {
	wt := n.udWeightTotal()
	if wt == 0 {
		return 0
	}
	base, err := conformance.UDBase(n.led.MoneySupply(), n.cfg.CPeriodPpm, wt)
	if err != nil {
		return 0
	}
	if base < n.cfg.GenesisUD {
		return n.cfg.GenesisUD
	}
	return base
}

// RunUD issues one Universal Dividend period to every active member
// (PROTOCOL §10). Idempotency across missed periods is the caller's concern;
// this issues exactly one period per call. Returns the standard-weight dividend
// paid. MUST be driven by a scheduler, never by request handling.
func (n *Node) RunUD() (int64, error) {
	n.mu.Lock()
	udBase := n.computeUDBase()
	if udBase <= 0 {
		n.mu.Unlock()
		return 0, nil
	}
	now := n.clock().Format(time.RFC3339)
	for _, name := range n.sortedMemberNames() {
		m := n.members[name]
		if !m.Active {
			continue
		}
		amt := conformance.RecipientUD(udBase, m.Weight)
		if amt <= 0 {
			continue
		}
		if _, err := n.led.Append(ledger.Tx{
			ID:      newID(),
			Type:    ledger.TxIssuanceUD,
			Created: now,
			Entries: []ledger.Entry{
				{Account: ledger.MemberPrefix + name, Amount: amt},
				{Account: ledger.AcctIssuance, Amount: -amt},
			},
		}); err != nil {
			n.mu.Unlock()
			return 0, err
		}
	}
	n.currentUD = n.computeUDBase()
	_ = n.persistLocked()
	n.mu.Unlock()
	return udBase, nil
}

// sortedMemberNames gives a deterministic issuance order so a node's log (and
// thus its head hash) is reproducible across runs.
func (n *Node) sortedMemberNames() []string {
	names := make([]string, 0, len(n.members))
	for name := range n.members {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validMemberName(name string) error {
	if len(name) < 1 || len(name) > 32 {
		return fmt.Errorf("member %q: length must be 1..32", name)
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return fmt.Errorf("member %q: must match [a-z0-9_]", name)
		}
	}
	return nil
}
