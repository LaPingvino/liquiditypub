package lpnode

import "fmt"

// Checkpoint reconciliation (PROTOCOL §8.3). Peers MUST compare channel_root and
// op_seq on every poll; divergence freezes the contact for new operations until
// resolved out of band (the signed histories make it attributable). We freeze on
// a genuine contradiction — the peer's root at its op_seq disagrees with our
// recorded root at that same index (a fork) — while a peer that is merely behind
// but consistent is treated as normal in-flight lag that reconciles on a later
// poll. Note: a one-sided commit that leaves a peer *permanently* behind (rather
// than committing a conflicting op) is prevented upstream by the payer's expiry
// guard in handleTransferAccept, since it produces no conflicting root here.

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

		// Compare the peer's checkpoint against our root at the *same* op_seq.
		// A mismatch means a different operation was committed at that index on
		// one side — a genuine fork — which we freeze on, whether the peer is even
		// with us or behind. (A peer ahead of us has an index we cannot compare
		// yet; we catch up via pull and check on the next poll.) A peer that is
		// merely behind but consistent matches our historical root and is treated
		// as normal lag (§8.3).
		if peerOpSeq >= 0 && peerOpSeq <= c.OpSeq && peerOpSeq < int64(len(c.Roots)) &&
			peerRoot != c.Roots[peerOpSeq] {
			c.Diverged = true
			_ = n.persistLocked()
			res.Diverged = true
			res.Detail = fmt.Sprintf("channel root divergence at op_seq %d", peerOpSeq)
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
