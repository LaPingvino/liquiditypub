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
	Pruned   int // outbox entries pruned as acknowledged (§5.1)
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

		// Prune outbox entries the peer has acknowledged (§5.1): its
		// last_seq_processed for us is the high-water mark of our channel it
		// has durably processed.
		lastProc, _ := asInt(m["last_seq_processed"])
		pruned := n.pruneOutboxLocked(c.PeerHost, lastProc)

		if peerOpSeq == c.OpSeq && peerRoot != c.channelRootB64() {
			c.Diverged = true
			_ = n.persistLocked()
			res.Diverged = true
			res.Detail = fmt.Sprintf("channel root divergence at op_seq %d", c.OpSeq)
			return res, nil
		}
		if pruned > 0 {
			_ = n.persistLocked()
		}
		res.Pruned = pruned
		res.Detail = fmt.Sprintf("op_seq %d/%d, pruned %d", c.OpSeq, peerOpSeq, pruned)
		return res, nil
	}
	return ReconcileResult{Peer: peerBase}, nil
}

// pruneOutboxLocked drops outbox envelopes to peerHost whose seq is at or below
// upto (already acknowledged). Caller holds n.mu. Returns how many were removed.
func (n *Node) pruneOutboxLocked(peerHost string, upto int64) int {
	if upto <= 0 {
		return 0
	}
	src := n.outbox[peerHost]
	if len(src) == 0 {
		return 0
	}
	kept := make([]map[string]any, 0, len(src))
	removed := 0
	for _, e := range src {
		if seq, ok := asInt(e["seq"]); ok && seq <= upto {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	n.outbox[peerHost] = kept
	return removed
}

// ReconcileAll reconciles every open contact against its peer's checkpoint.
func (n *Node) ReconcileAll() {
	for _, base := range n.openPeerBases() {
		_, _ = n.ReconcilePeer(base)
	}
}
