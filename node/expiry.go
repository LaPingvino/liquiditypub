package lpnode

import (
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
)

// Transfer expiry (PROTOCOL §7.1, §7.4). `expires` bounds the PROPOSED and
// ACCEPTED states; there is no expiry after commit. When a transfer expires
// uncommitted, neither side has appended ledger entries and op_seq has not
// advanced, so expiring is purely local: transition to EXPIRED and release the
// contact lock. Both sides expire independently against their own clock and
// stay consistent because nothing was committed.

// expireIfDueLocked expires one transfer if it is past `expires` and still in a
// pre-commit state, releasing the contact lock. Caller holds n.mu.
func (n *Node) expireIfDueLocked(t *Transfer, now time.Time) bool {
	if t == nil || (t.State != "PROPOSED" && t.State != "ACCEPTED") {
		return false
	}
	exp, err := time.Parse(time.RFC3339, t.Expires)
	if err != nil || !now.After(exp) {
		return false
	}
	next, err := conformance.Transition(t.State, "expire")
	if err != nil {
		return false
	}
	t.State = next
	if c := n.contacts[t.ContactID]; c != nil && c.BusyTransfer == t.ID {
		c.Busy, c.BusyTransfer = false, ""
	}
	return true
}

// SweepExpired expires every due transfer and returns how many it closed.
func (n *Node) SweepExpired() int {
	n.mu.Lock()
	now := n.clock()
	count := 0
	for _, t := range n.transfers {
		if n.expireIfDueLocked(t, now) {
			count++
		}
	}
	if count > 0 {
		_ = n.persistLocked()
	}
	n.mu.Unlock()
	return count
}

// StartExpirySweeper runs SweepExpired on a ticker until stop is closed. A live
// node should run this so a dropped accept/commit can never pin a contact busy
// past the transfer's expiry.
func (n *Node) StartExpirySweeper(interval time.Duration, stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				n.SweepExpired()
			}
		}
	}()
}

// releaseIfBusyExpired expires a contact's in-flight operation if it is due,
// so a caller about to refuse on "busy" first gives the stale op a chance to
// clear. Caller holds n.mu.
func (n *Node) releaseIfBusyExpired(c *Contact) {
	if c.Busy && c.BusyTransfer != "" {
		n.expireIfDueLocked(n.transfers[c.BusyTransfer], n.clock())
	}
}
