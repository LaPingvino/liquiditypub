package lpnode

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"

	"github.com/LaPingvino/liquiditypub/conformance"
	"github.com/LaPingvino/liquiditypub/node/ledger"
	"github.com/LaPingvino/liquiditypub/node/store"
)

// snapshot is the full, serializable node state. Everything needed to resume a
// node after a crash lives here: the ledger IS the money, but contacts,
// transfers, channel bookkeeping, and outboxes are protocol state the ledger
// alone can't reconstruct, so they are persisted alongside it.
type snapshot struct {
	OwnKeys   []ownKeySnap                 `json:"own_keys"`
	ActiveKey string                       `json:"active_key"`
	Created   string                       `json:"created"`
	CurrentUD int64                        `json:"current_ud"`
	Members   []*Member                    `json:"members"`
	Ledger    []ledger.Record              `json:"ledger"`
	Contacts  []*Contact                   `json:"contacts"`
	Transfers []*Transfer                  `json:"transfers"`
	OutSeq    map[string]int64             `json:"out_seq"`
	Inbound   map[string]inboundSnap       `json:"inbound"`
	Outbox    map[string][]json.RawMessage `json:"outbox"`
	PeerKeys  map[string]string            `json:"peer_keys"` // keyid -> b64 pubkey
}

type ownKeySnap struct {
	LocalID string `json:"local_id"`
	Seed    string `json:"seed"`
	Created string `json:"created"`
	Revoked string `json:"revoked"`
}

type inboundSnap struct {
	SeenIDs []string `json:"seen_ids"`
	LastSeq int64    `json:"last_seq"`
	// Replies persists the cached response per processed message id so an
	// idempotent duplicate is answered identically *after a restart* — without
	// it, a re-sent request (e.g. a payer retrying transfer.commit) that is
	// recognized as a duplicate would get no reply, wedging the sender.
	Replies map[string]json.RawMessage `json:"replies,omitempty"`
}

// SetStore installs a persistence backend and immediately writes the current
// state, so the on-disk snapshot exists from the first call.
func (n *Node) SetStore(s store.Store) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.store = s
	return n.persistLocked()
}

// persistLocked serializes and durably saves the node snapshot. The caller
// holds n.mu, so the write completes *before* the mutated state becomes visible
// to any other goroutine: state is never observable before it is durable. This
// serializes the (small, infrequent) I/O under the lock, which is the right
// trade for a PoC — a peer never sees an ack the node hasn't persisted.
func (n *Node) persistLocked() error {
	if n.store == nil {
		return nil
	}
	data, err := json.Marshal(n.snapshotLocked())
	if err != nil {
		log.Printf("%s: PERSIST marshal failed (state mutation is NOT durable): %v", n.cfg.Base, err)
		return err
	}
	if err := n.store.Save(data); err != nil {
		// The caller has already mutated in-memory state under the lock and will
		// typically dispatch a reply. A Save failure means that state is live but
		// not durable, so a crash would roll it back below what the peer has seen.
		// Surface it loudly; a production node should treat this as fatal.
		log.Printf("%s: PERSIST save failed (state mutation is NOT durable): %v", n.cfg.Base, err)
		return err
	}
	return nil
}

// snapshotLocked builds the serializable snapshot. Caller holds n.mu.
func (n *Node) snapshotLocked() snapshot {
	snap := snapshot{
		ActiveKey: n.keyLocalID,
		Created:   n.created,
		CurrentUD: n.currentUD,
		OutSeq:    n.outSeq,
		Ledger:    n.led.Records(),
		Outbox:    map[string][]json.RawMessage{},
		Inbound:   map[string]inboundSnap{},
		PeerKeys:  map[string]string{},
	}
	for _, k := range n.ownKeys {
		snap.OwnKeys = append(snap.OwnKeys, ownKeySnap{
			LocalID: k.LocalID, Seed: k.Seed, Created: k.Created, Revoked: k.Revoked,
		})
	}
	for _, m := range n.members {
		snap.Members = append(snap.Members, m)
	}
	for _, c := range n.contacts {
		snap.Contacts = append(snap.Contacts, c)
	}
	for _, t := range n.transfers {
		snap.Transfers = append(snap.Transfers, t)
	}
	for host, env := range n.outbox {
		raws := make([]json.RawMessage, 0, len(env))
		for _, e := range env {
			b, err := json.Marshal(e)
			if err == nil {
				raws = append(raws, b)
			}
		}
		snap.Outbox[host] = raws
	}
	for host, ci := range n.inState {
		ids := make([]string, 0, len(ci.seenIDs))
		for id := range ci.seenIDs {
			ids = append(ids, id)
		}
		var replies map[string]json.RawMessage
		if len(ci.replies) > 0 {
			replies = make(map[string]json.RawMessage, len(ci.replies))
			for id, r := range ci.replies {
				if b, err := json.Marshal(r); err == nil {
					replies[id] = b
				}
			}
		}
		snap.Inbound[host] = inboundSnap{SeenIDs: ids, LastSeq: ci.lastSeq, Replies: replies}
	}
	for kid, pub := range n.peerKeys {
		snap.PeerKeys[kid] = base64.RawURLEncoding.EncodeToString(pub)
	}
	return snap
}

// Restore rebuilds a node from a store, or creates a fresh one (and persists
// it) if the store is empty. The returned node has the store installed and will
// persist on every subsequent state change. Wire a Sender and call Start as
// usual.
func Restore(cfg Config, s store.Store) (*Node, error) {
	data, err := s.Load()
	if err != nil {
		return nil, err
	}
	if data == nil {
		n, err := NewNode(cfg)
		if err != nil {
			return nil, err
		}
		if err := n.SetStore(s); err != nil {
			return nil, err
		}
		return n, nil
	}

	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	// Rebuild the keyring; resolve the active key.
	var ownKeys []*ownKey
	var active *ownKey
	for _, ks := range snap.OwnKeys {
		seed, err := base64.RawURLEncoding.DecodeString(ks.Seed)
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("invalid persisted key %q", ks.LocalID)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		k := &ownKey{
			LocalID: ks.LocalID, Seed: ks.Seed, Created: ks.Created, Revoked: ks.Revoked,
			priv: priv, pub: priv.Public().(ed25519.PublicKey),
		}
		ownKeys = append(ownKeys, k)
		if k.LocalID == snap.ActiveKey {
			active = k
		}
	}
	if active == nil {
		return nil, fmt.Errorf("active key %q not found in keyring", snap.ActiveKey)
	}
	led, err := ledger.Load(snap.Ledger)
	if err != nil {
		return nil, fmt.Errorf("load ledger: %w", err)
	}

	if cfg.Transparency == "" {
		cfg.Transparency = "pseudonymous"
	}
	n := &Node{
		cfg:           cfg,
		priv:          active.priv,
		pub:           active.pub,
		keyLocalID:    active.LocalID,
		ownKeys:       ownKeys,
		clock:         realClock,
		created:       snap.Created,
		currentUD:     snap.CurrentUD,
		led:           led,
		members:       map[string]*Member{},
		contacts:      map[string]*Contact{},
		contactByHost: map[string]*Contact{},
		transfers:     map[string]*Transfer{},
		outSeq:        map[string]int64{},
		inState:       map[string]*channelInbound{},
		peerKeys:      map[string]ed25519.PublicKey{},
		outbox:        map[string][]map[string]any{},
		pushed:        map[string]int64{},
		store:         s,
	}
	if n.outSeq == nil {
		n.outSeq = map[string]int64{}
	}
	for k, v := range snap.OutSeq {
		n.outSeq[k] = v
	}
	for _, m := range snap.Members {
		n.members[m.Name] = m
	}
	for _, c := range snap.Contacts {
		// Snapshots written before root history existed carry no Roots; backfill
		// the current root at the current op_seq so reconciliation can still
		// compare at the head after a restart.
		if int64(len(c.Roots)) <= c.OpSeq {
			c.recordRoot()
		}
		n.contacts[c.ID] = c
		n.contactByHost[c.PeerHost] = c
	}
	for _, t := range snap.Transfers {
		n.transfers[t.ID] = t
	}
	for host, in := range snap.Inbound {
		ci := &channelInbound{seenIDs: map[string]bool{}, replies: map[string]map[string]any{}, lastSeq: in.LastSeq}
		for _, id := range in.SeenIDs {
			ci.seenIDs[id] = true
		}
		for id, raw := range in.Replies {
			if v, err := conformance.DecodeJSON(raw); err == nil {
				if m, ok := v.(map[string]any); ok {
					ci.replies[id] = m
				}
			}
		}
		n.inState[host] = ci
	}
	for host, raws := range snap.Outbox {
		for _, raw := range raws {
			v, err := conformance.DecodeJSON(raw)
			if err != nil {
				continue
			}
			if m, ok := v.(map[string]any); ok {
				n.outbox[host] = append(n.outbox[host], m)
			}
		}
	}
	for kid, b := range snap.PeerKeys {
		if pub, err := base64.RawURLEncoding.DecodeString(b); err == nil && len(pub) == ed25519.PublicKeySize {
			n.peerKeys[kid] = ed25519.PublicKey(pub)
		}
	}
	// Belt and suspenders: every own key must always be resolvable.
	for _, k := range n.ownKeys {
		n.peerKeys[keyID(cfg.Base, k.LocalID)] = k.pub
	}
	return n, nil
}
