package lpnode

import (
	"fmt"
	"time"

	"github.com/LaPingvino/liquiditypub/node/ledger"
)

// Reserve adjustments (PROTOCOL §8.4) — consensual liquidity changes outside
// pricing: top-ups, withdrawals, or recording out-of-band settlement. The
// proposer changes its own committed reserve (node:<peer>) by my_delta; the
// counterpart books to `treasury`. The peer mirrors the change and both fold
// the operation into the channel hash. Like a transfer it is one contact
// operation and respects the busy lock (§6.3).

// AdjustReserve proposes changing our own reserve for a peer by delta
// (positive = top-up, negative = withdraw), sourced from/returned to treasury.
// The delta is applied to our ledger only after the peer accepts.
func (n *Node) AdjustReserve(peerBase string, delta int64, memo string) (string, error) {
	n.mu.Lock()
	c := n.contactByHost[host(peerBase)]
	if c == nil || !c.Active || c.Closed {
		n.mu.Unlock()
		return "", fmt.Errorf("no active contact with %s", host(peerBase))
	}
	if c.Diverged {
		n.mu.Unlock()
		return "", fmt.Errorf("contact frozen: checkpoint divergence (§8.3)")
	}
	n.releaseIfBusyExpired(c)
	if c.Busy {
		n.mu.Unlock()
		return "", fmt.Errorf("contact busy (§6.3): op %s in flight", c.BusyTransfer)
	}
	if delta == 0 {
		n.mu.Unlock()
		return "", fmt.Errorf("delta must be non-zero")
	}
	if c.MyReserveOfPeer+delta < 0 {
		n.mu.Unlock()
		return "", fmt.Errorf("withdrawal exceeds reserve: %d + %d < 0", c.MyReserveOfPeer, delta)
	}
	adjustID := newID()
	c.Busy, c.BusyTransfer = true, adjustID
	c.PendingAdjustID, c.PendingAdjustDelta = adjustID, delta
	env := n.buildSigned("reserve.adjust", peerBase, "", map[string]any{
		"contact_id": c.ID,
		"op_seq":     c.OpSeq,
		"adjust_id":  adjustID,
		"my_delta":   delta,
		"memo":       memo,
	})
	_ = n.persistLocked()
	n.mu.Unlock()
	n.dispatch(peerBase, env)
	return adjustID, nil
}

// handleReserveAdjust — responder mirrors the proposer's reserve change and
// answers with reserve.accept. The proposer's reserve is our PeerReserveOfMe.
func (n *Node) handleReserveAdjust(env map[string]any) map[string]any {
	p, ok := payloadOf(env)
	if !ok {
		return n.errorReply(env, "malformed", "missing payload")
	}
	c := n.contactByHost[host(envStr(env, "from"))]
	if c == nil || !c.Active || c.Closed {
		return n.errorReply(env, "unknown-contact", "no active contact")
	}
	if c.Diverged {
		return n.errorReply(env, "frozen", "contact frozen: checkpoint divergence (§8.3)")
	}
	n.releaseIfBusyExpired(c)
	if c.Busy {
		return n.errorReply(env, "busy", "contact has an operation in flight")
	}
	opSeq, _ := pInt(p, "op_seq")
	if opSeq != c.OpSeq {
		return n.errorReply(env, "stale-pool", fmt.Sprintf("op_seq %d != current %d", opSeq, c.OpSeq))
	}
	adjustID := pStr(p, "adjust_id")
	delta, ok := pInt(p, "my_delta")
	if !ok || delta == 0 {
		return n.errorReply(env, "malformed", "invalid my_delta")
	}
	if c.PeerReserveOfMe+delta < 0 {
		return n.errorReply(env, "insufficient-reserve", "withdrawal exceeds mirrored reserve")
	}
	c.PeerReserveOfMe += delta // the proposer's own reserve, mirrored here
	if err := c.applyAdjust(adjustID, delta); err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	return n.buildSigned("reserve.accept", c.PeerBase, envStr(env, "id"), map[string]any{
		"contact_id": c.ID,
		"adjust_id":  adjustID,
	})
}

// handleReserveAccept — proposer commits the adjustment to its ledger.
func (n *Node) handleReserveAccept(env map[string]any) map[string]any {
	p, ok := payloadOf(env)
	if !ok {
		return n.errorReply(env, "malformed", "missing payload")
	}
	c := n.contactByHost[host(envStr(env, "from"))]
	if c == nil {
		return n.errorReply(env, "unknown-contact", "no contact")
	}
	adjustID := pStr(p, "adjust_id")
	if c.PendingAdjustID != adjustID {
		return nil // idempotent / unknown adjustment: nothing pending
	}
	delta := c.PendingAdjustDelta
	if _, err := n.led.Append(ledger.Tx{
		ID:      newID(),
		Type:    ledger.TxReserveAdjust,
		Ref:     adjustID,
		Created: n.clock().Format(time.RFC3339),
		Entries: []ledger.Entry{
			{Account: ledger.NodeWalletPrefix + c.PeerHost, Amount: delta},
			{Account: ledger.AcctTreasury, Amount: -delta},
		},
	}); err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	c.MyReserveOfPeer += delta
	if err := c.applyAdjust(adjustID, delta); err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	c.Busy, c.BusyTransfer = false, ""
	c.PendingAdjustID, c.PendingAdjustDelta = "", 0
	return nil
}
