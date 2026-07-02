package lpnode

import "fmt"

// Checkpoint reconciliation (PROTOCOL §8.3). Peers MUST compare channel_root and
// op_seq on every poll; divergence freezes the contact for new operations until
// resolved out of band (the signed histories make it attributable). We freeze
// only on a genuine contradiction — equal op_seq but different root — because a
// mere op_seq lag is the normal transient of an in-flight operation and
// reconciles itself on a later poll.

// ReconcileResult reports the outcome of one reconciliation.
type ReconcileResult struct {
	Peer     string
	Compared bool // a shared contact was found and compared
	Diverged bool
	Detail   string
}

// ReconcilePeer fetches a peer's checkpoint and compares the shared contact.
func (n *Node) ReconcilePeer(peerBase string) (ReconcileResult, error) {
	s := n.sender()
	if s == nil {
		return ReconcileResult{Peer: peerBase}, nil
	}
	cp, err := s.FetchCheckpoint(peerBase)
	if err != nil {
		return ReconcileResult{Peer: peerBase}, err
	}
	contacts, _ := cp["contacts"].([]any)

	n.mu.Lock()
	defer n.mu.Unlock()
	c := n.contactByHost[host(peerBase)]
	if c == nil {
		return ReconcileResult{Peer: peerBase}, nil
	}
	for _, entry := range contacts {
		m, ok := entry.(map[string]any)
		if !ok || pStr(m, "contact_id") != c.ID {
			continue
		}
		peerOpSeq, _ := asInt(m["op_seq"])
		peerRoot, _ := m["channel_root"].(string)
		res := ReconcileResult{Peer: peerBase, Compared: true}
		if peerOpSeq == c.OpSeq && peerRoot != c.channelRootB64() {
			c.Diverged = true
			_ = n.persistLocked()
			res.Diverged = true
			res.Detail = fmt.Sprintf("channel root divergence at op_seq %d", c.OpSeq)
			return res, nil
		}
		res.Detail = fmt.Sprintf("op_seq %d/%d", c.OpSeq, peerOpSeq)
		return res, nil
	}
	return ReconcileResult{Peer: peerBase}, nil
}

// ReconcileAll reconciles every open contact against its peer's checkpoint.
func (n *Node) ReconcileAll() {
	for _, base := range n.openPeerBases() {
		_, _ = n.ReconcilePeer(base)
	}
}
