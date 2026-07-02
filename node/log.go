package lpnode

import "github.com/LaPingvino/liquiditypub/node/ledger"

// LogHead returns the head pointer of the hash-linked log (§9.2).
func (n *Node) LogHead() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	return map[string]any{
		"seq":  int64(n.led.Len()),
		"hash": n.led.Head(),
	}
}

// LogRecords returns the full log. A production node paginates under
// endpoints.log; a PoC serves it whole. Visibility is the caller's concern —
// transparency levels (§9.3) gate who may read this.
func (n *Node) LogRecords() []ledger.Record {
	n.mu.Lock()
	defer n.mu.Unlock()
	recs := n.led.Records()
	out := make([]ledger.Record, len(recs))
	copy(out, recs)
	return out
}
