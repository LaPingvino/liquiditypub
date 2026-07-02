package lpnode

import (
	"fmt"
	"time"

	"github.com/LaPingvino/liquiditypub/node/ledger"
)

// Runtime membership management (PROTOCOL §11). Adding or deactivating a member
// changes ud_weight_total and thus future dividends; it is node-internal policy
// (§10), surfaced only as the aggregate stats peers price against.

// AddMember activates a new member, optionally seeding an initial grant from
// `issuance`. Names match [a-z0-9_]{1,32} (§11).
func (n *Node) AddMember(name, displayName string, grant int64) error {
	if err := validMemberName(name); err != nil {
		return err
	}
	n.mu.Lock()
	if _, exists := n.members[name]; exists {
		n.mu.Unlock()
		return fmt.Errorf("member %q already exists", name)
	}
	n.members[name] = &Member{Name: name, DisplayName: displayName, Weight: 1_000_000, Active: true}
	if grant > 0 {
		if _, err := n.led.Append(ledger.Tx{
			ID:      newID(),
			Type:    ledger.TxIssuanceGrant,
			Created: n.clock().Format(time.RFC3339),
			Entries: []ledger.Entry{
				{Account: ledger.MemberPrefix + name, Amount: grant},
				{Account: ledger.AcctIssuance, Amount: -grant},
			},
		}); err != nil {
			delete(n.members, name) // roll back the activation
			n.mu.Unlock()
			return fmt.Errorf("grant to %s: %w", name, err)
		}
	}
	n.currentUD = n.computeUDBase()
	err := n.persistLocked()
	n.mu.Unlock()
	return err
}

// DeactivateMember stops a member from receiving UD or being a transfer target;
// its balance and ledger history are preserved. Members are never deleted
// because the log references them.
func (n *Node) DeactivateMember(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	m := n.members[name]
	if m == nil {
		return fmt.Errorf("no member %q", name)
	}
	m.Active = false
	n.currentUD = n.computeUDBase()
	return n.persistLocked()
}

// MemberActive reports whether a member exists and is active (tests/admin).
func (n *Node) MemberActive(name string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	m := n.members[name]
	return m != nil && m.Active
}
