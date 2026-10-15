package lpnode

import (
	"errors"
	"fmt"
	"time"

	"github.com/LaPingvino/liquiditypub/node/ledger"
)

// ErrInsufficient: the payer's balance doesn't cover the payment.
var ErrInsufficient = errors.New("insufficient balance")

// Pay moves amount micro-units from one active member to another on this
// node's own ledger: an ordinary internal payment (ledger.TxPayment), no
// federation involved. Node-internal by the membrane principle (DESIGN §1):
// peers only ever see its effect on published totals. No note goes into the
// ledger — what a payment was for stays with the people who made it.
func (n *Node) Pay(from, to string, amount int64) (ledger.Record, error) {
	if amount <= 0 {
		return ledger.Record{}, fmt.Errorf("amount must be positive, got %d", amount)
	}
	if from == to {
		return ledger.Record{}, fmt.Errorf("can't pay yourself")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, name := range []string{from, to} {
		if m, ok := n.members[name]; !ok || !m.Active {
			return ledger.Record{}, fmt.Errorf("no active member %q", name)
		}
	}
	if n.led.Balance(ledger.MemberPrefix+from) < amount {
		return ledger.Record{}, ErrInsufficient
	}
	rec, err := n.led.Append(ledger.Tx{
		ID:      newID(),
		Type:    ledger.TxPayment,
		Created: n.clock().Format(time.RFC3339),
		Entries: []ledger.Entry{
			{Account: ledger.MemberPrefix + from, Amount: -amount},
			{Account: ledger.MemberPrefix + to, Amount: amount},
		},
	})
	if err != nil {
		return ledger.Record{}, err
	}
	return rec, n.persistLocked()
}
