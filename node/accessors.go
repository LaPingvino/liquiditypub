package lpnode

import "sort"

// PeerKeyIDs returns the fully-qualified ids of every key this node currently
// trusts for verification (own + peers), sorted. Intended for tests/inspection.
func (n *Node) PeerKeyIDs() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, 0, len(n.peerKeys))
	for k := range n.peerKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Host returns the node's own bare host (used to address local members).
func (n *Node) Host() string { return host(n.cfg.Base) }

// ContactActive reports whether there is an open, non-closed contact with the
// peer at peerBase.
func (n *Node) ContactActive(peerBase string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := n.contactByHost[host(peerBase)]
	return c != nil && c.Active && !c.Closed
}

// ContactByPeer returns a snapshot of the pool state for a peer, for tests and
// admin views. The bool is false when no such contact exists.
func (n *Node) ContactByPeer(peerBase string) (ContactView, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := n.contactByHost[host(peerBase)]
	if c == nil {
		return ContactView{}, false
	}
	return ContactView{
		ID:              c.ID,
		OpSeq:           c.OpSeq,
		ChannelRoot:     c.channelRootB64(),
		MyReserveOfPeer: c.MyReserveOfPeer,
		PeerReserveOfMe: c.PeerReserveOfMe,
		Active:          c.Active,
		Busy:            c.Busy,
		Diverged:        c.Diverged,
	}, true
}

// ContactView is a read-only snapshot of a contact's pool state.
type ContactView struct {
	ID              string
	OpSeq           int64
	ChannelRoot     string
	MyReserveOfPeer int64
	PeerReserveOfMe int64
	Active          bool
	Busy            bool
	Diverged        bool
}

// Balance returns a ledger account balance (e.g. "m:alice", "node:host").
func (n *Node) Balance(account string) int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.led.Balance(account)
}
