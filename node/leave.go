package lpnode

import (
	"fmt"
	"time"

	"github.com/LaPingvino/liquiditypub/node/ledger"
)

// FundRedistribute collects balances people chose to share out when they
// left; the next dividend pays it out equally to the active members.
const FundRedistribute = ledger.FundPrefix + "redistribute"

// Leave modes: what happens to a leaving member's balance.
const (
	LeaveDestroy      = "destroy"      // back to issuance: the money supply shrinks
	LeaveRedistribute = "redistribute" // into FundRedistribute, shared out with the next dividend
	LeaveGive         = "give"         // all of it to another active member
	LeaveKeep         = "keep"         // stays where it is (e.g. already moved away)
)

// Leave deactivates a member — no more dividend; the member and its history
// stay in the log — after doing with its balance what mode says. Node policy,
// not protocol (DESIGN §1): peers see only the effect on published totals.
func (n *Node) Leave(member, mode, to string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	m := n.members[member]
	if m == nil || !m.Active {
		return fmt.Errorf("no active member %q", member)
	}
	acct := ledger.MemberPrefix + member
	bal := n.led.Balance(acct)
	var dst string
	typ := ledger.TxPayment
	switch mode {
	case LeaveDestroy:
		dst, typ = ledger.AcctIssuance, ledger.TxIssuanceBurn
	case LeaveRedistribute:
		dst = FundRedistribute
	case LeaveGive:
		if t := n.members[to]; t == nil || !t.Active || to == member {
			return fmt.Errorf("give: no other active member %q", to)
		}
		dst = ledger.MemberPrefix + to
	case LeaveKeep:
	default:
		return fmt.Errorf("unknown leave mode %q", mode)
	}
	if dst != "" && bal > 0 {
		if _, err := n.led.Append(ledger.Tx{
			ID: newID(), Type: typ, Created: n.clock().Format(time.RFC3339),
			Entries: []ledger.Entry{{Account: acct, Amount: -bal}, {Account: dst, Amount: bal}},
		}); err != nil {
			return err
		}
	}
	m.Active = false
	n.currentUD = n.computeUDBase()
	return n.persistLocked()
}

// redistributeLocked pays FundRedistribute out equally to the active members
// (the remainder of the division stays for next time). Caller holds n.mu.
func (n *Node) redistributeLocked(now string) error {
	bal := n.led.Balance(FundRedistribute)
	if bal <= 0 {
		return nil
	}
	var active []string
	for _, name := range n.sortedMemberNames() {
		if n.members[name].Active {
			active = append(active, name)
		}
	}
	if len(active) == 0 {
		return nil
	}
	share := bal / int64(len(active))
	if share <= 0 {
		return nil
	}
	entries := []ledger.Entry{{Account: FundRedistribute, Amount: -share * int64(len(active))}}
	for _, name := range active {
		entries = append(entries, ledger.Entry{Account: ledger.MemberPrefix + name, Amount: share})
	}
	_, err := n.led.Append(ledger.Tx{ID: newID(), Type: ledger.TxFundRedistrib, Created: now, Entries: entries})
	return err
}
