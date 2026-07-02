package lpnode

import "github.com/LaPingvino/liquiditypub/node/ledger"

// LogPageSize is the fixed page size for the paginated log (§9.2).
const LogPageSize = 100

// LogHead returns the head pointer of the hash-linked log plus paging metadata
// (§9.2).
func (n *Node) LogHead() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	total := n.led.Len()
	pageCount := (total + LogPageSize - 1) / LogPageSize
	return map[string]any{
		"seq":        int64(total),
		"hash":       n.led.Head(),
		"page_size":  int64(LogPageSize),
		"page_count": int64(pageCount),
	}
}

// LogPage returns one fixed-size page of the log (0-indexed): page 0 holds
// records 1..LogPageSize, and so on (§9.2). Out-of-range pages are empty.
func (n *Node) LogPage(page int) []ledger.Record {
	n.mu.Lock()
	defer n.mu.Unlock()
	recs := n.led.Records()
	start := page * LogPageSize
	if page < 0 || start >= len(recs) {
		return []ledger.Record{}
	}
	end := start + LogPageSize
	if end > len(recs) {
		end = len(recs)
	}
	out := make([]ledger.Record, end-start)
	copy(out, recs[start:end])
	return out
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
