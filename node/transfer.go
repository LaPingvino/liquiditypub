package lpnode

import (
	"fmt"
	"strings"
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
	"github.com/LaPingvino/liquiditypub/node/ledger"
)

// Transfer tracks one cross-node payment through the state machine (§7.1).
type Transfer struct {
	ID        string
	ContactID string
	Outgoing  bool // true if we are the payer

	State string // conformance state: PROPOSED/ACCEPTED/COMMITTED/SETTLED/...

	OpSeq                int64 // op_seq priced against
	FromMember, ToMember string
	SrcAmount, DstAmount int64
	Expires              string

	MyEntry   *entryProof    // our committed log entry (proof)
	PeerEntry *entryProof    // the peer's, from commit/receipt
	Receipt   map[string]any // payee: cached receipt for idempotent commit retries
}

type entryProof struct {
	LogSeq  int64
	LogHash string
}

// localPart returns the account-local part of name@host (§11).
func localPart(addr string) string {
	if i := strings.IndexByte(addr, '@'); i >= 0 {
		return addr[:i]
	}
	return addr
}

// StartTransfer is the payer entry point: price the payment against our pool
// view, open the state machine, lock the contact, and send transfer.propose.
func (n *Node) StartTransfer(peerBase, fromMember, toMember string, src int64, note string) (string, error) {
	n.mu.Lock()
	peerHost := host(peerBase)
	c := n.contactByHost[peerHost]
	if c == nil || !c.Active || c.Closed {
		n.mu.Unlock()
		return "", fmt.Errorf("no active contact with %s", peerHost)
	}
	if c.Busy {
		n.mu.Unlock()
		return "", fmt.Errorf("contact busy (§6.3): op %s in flight", c.BusyTransfer)
	}
	if src <= 0 {
		n.mu.Unlock()
		return "", fmt.Errorf("src_amount must be positive")
	}
	fm := n.members[localPart(fromMember)]
	if fm == nil || !fm.Active {
		n.mu.Unlock()
		return "", fmt.Errorf("unknown local member %q", fromMember)
	}
	if bal := n.led.Balance(ledger.MemberPrefix + fm.Name); bal < src {
		n.mu.Unlock()
		return "", fmt.Errorf("member %s balance %d < %d", fm.Name, bal, src)
	}
	dst, err := c.priceOutgoing(src)
	if err != nil {
		n.mu.Unlock()
		return "", fmt.Errorf("pricing: %w", err)
	}
	id := newID()
	state, _ := conformance.Transition("NONE", "propose")
	t := &Transfer{
		ID: id, ContactID: c.ID, Outgoing: true, State: state,
		OpSeq: c.OpSeq, FromMember: fromMember, ToMember: toMember,
		SrcAmount: src, DstAmount: dst,
		Expires: n.clock().Add(time.Hour).Format(time.RFC3339),
	}
	n.transfers[id] = t
	c.Busy, c.BusyTransfer = true, id
	env := n.buildSigned("transfer.propose", peerBase, "", map[string]any{
		"transfer_id": id,
		"contact_id":  c.ID,
		"op_seq":      c.OpSeq,
		"from_member": fromMember,
		"to_member":   toMember,
		"src_amount":  src,
		"dst_amount":  dst,
		"note":        note,
		"expires":     t.Expires,
	})
	_ = n.persistLocked()
	n.mu.Unlock()
	n.dispatch(peerBase, env)
	return id, nil
}

// handleTransferPropose — payee validates the proposal and accepts or rejects.
func (n *Node) handleTransferPropose(env map[string]any) map[string]any {
	p, ok := payloadOf(env)
	if !ok {
		return n.errorReply(env, "malformed", "missing payload")
	}
	fromHost := host(envStr(env, "from"))
	c := n.contactByHost[fromHost]
	if c == nil || !c.Active || c.Closed {
		return n.errorReply(env, "unknown-contact", "no active contact")
	}
	if c.Busy {
		return n.errorReply(env, "busy", "contact has an operation in flight")
	}
	opSeq, _ := pInt(p, "op_seq")
	if opSeq != c.OpSeq {
		return n.errorReply(env, "stale-pool", fmt.Sprintf("op_seq %d != current %d", opSeq, c.OpSeq))
	}
	tid := pStr(p, "transfer_id")
	toMember := pStr(p, "to_member")
	tm := n.members[localPart(toMember)]
	if tm == nil || !tm.Active {
		return n.errorReply(env, "unknown-member", "to_member not found")
	}
	src, ok1 := pInt(p, "src_amount")
	dst, ok2 := pInt(p, "dst_amount")
	if !ok1 || !ok2 || src <= 0 {
		return n.errorReply(env, "malformed", "invalid amounts")
	}
	wantDst, err := c.priceIncoming(src)
	if err != nil {
		return n.errorReply(env, "dust", err.Error())
	}
	if wantDst != dst {
		return n.errorReply(env, "price-mismatch",
			fmt.Sprintf("our price %d != proposed %d", wantDst, dst))
	}
	// Accept: open the state machine, lock the contact, hold the payout.
	st, _ := conformance.Transition("NONE", "propose")
	st, _ = conformance.Transition(st, "accept")
	n.transfers[tid] = &Transfer{
		ID: tid, ContactID: c.ID, Outgoing: false, State: st,
		OpSeq: opSeq, FromMember: pStr(p, "from_member"), ToMember: toMember,
		SrcAmount: src, DstAmount: dst, Expires: pStr(p, "expires"),
	}
	c.Busy, c.BusyTransfer = true, tid
	return n.buildSigned("transfer.accept", c.PeerBase, envStr(env, "id"), map[string]any{
		"transfer_id": tid,
	})
}

// handleTransferAccept — payer commits: append our leg irrevocably, update the
// pool, and send transfer.commit with the entry proof (§7.1).
func (n *Node) handleTransferAccept(env map[string]any) map[string]any {
	p, _ := payloadOf(env)
	t := n.transfers[pStr(p, "transfer_id")]
	if t == nil || !t.Outgoing {
		return n.errorReply(env, "unknown-transfer", "no matching outgoing transfer")
	}
	if t.State == "COMMITTED" || t.State == "SETTLED" {
		return nil // idempotent: already committed
	}
	next, err := conformance.Transition(t.State, "accept")
	if err != nil {
		return n.errorReply(env, "bad-state", err.Error())
	}
	t.State = next
	c := n.contacts[t.ContactID]
	rec, err := n.led.Append(ledger.Tx{
		ID:      newID(),
		Type:    ledger.TxTransferOut,
		Ref:     t.ID,
		Created: n.clock().Format(time.RFC3339),
		Entries: []ledger.Entry{
			{Account: ledger.MemberPrefix + localPart(t.FromMember), Amount: -t.SrcAmount},
			{Account: ledger.NodeWalletPrefix + c.PeerHost, Amount: t.SrcAmount},
		},
	})
	if err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	// Pool update: our reserve of the peer grows, our reserve at the peer shrinks.
	c.MyReserveOfPeer += t.SrcAmount
	c.PeerReserveOfMe -= t.DstAmount
	if err := c.applyTransfer(t.ID, t.SrcAmount, t.DstAmount); err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	t.MyEntry = &entryProof{LogSeq: rec.Seq, LogHash: rec.Hash}
	t.State, _ = conformance.Transition(t.State, "commit")
	return n.buildSigned("transfer.commit", c.PeerBase, envStr(env, "id"), map[string]any{
		"transfer_id": t.ID,
		"entry":       map[string]any{"log_seq": rec.Seq, "log_hash": rec.Hash},
	})
}

// handleTransferCommit — payee applies its leg and answers with the receipt.
// A retried commit re-sends the same receipt without re-applying (§7.1).
func (n *Node) handleTransferCommit(env map[string]any) map[string]any {
	p, _ := payloadOf(env)
	t := n.transfers[pStr(p, "transfer_id")]
	if t == nil || t.Outgoing {
		return n.errorReply(env, "unknown-transfer", "no matching incoming transfer")
	}
	if t.State == "SETTLED" || t.State == "COMMITTED" {
		return t.Receipt // idempotent retry: same receipt, no re-apply
	}
	next, err := conformance.Transition(t.State, "commit")
	if err != nil {
		return n.errorReply(env, "bad-state", err.Error())
	}
	t.State = next
	c := n.contacts[t.ContactID]
	if pe, ok := p["entry"].(map[string]any); ok {
		ls, _ := pInt(pe, "log_seq")
		t.PeerEntry = &entryProof{LogSeq: ls, LogHash: pStr(pe, "log_hash")}
	}
	rec, err := n.led.Append(ledger.Tx{
		ID:      newID(),
		Type:    ledger.TxTransferIn,
		Ref:     t.ID,
		Created: n.clock().Format(time.RFC3339),
		Entries: []ledger.Entry{
			{Account: ledger.NodeWalletPrefix + c.PeerHost, Amount: -t.DstAmount},
			{Account: ledger.MemberPrefix + localPart(t.ToMember), Amount: t.DstAmount},
		},
	})
	if err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	c.PeerReserveOfMe += t.SrcAmount
	c.MyReserveOfPeer -= t.DstAmount
	if err := c.applyTransfer(t.ID, t.SrcAmount, t.DstAmount); err != nil {
		return n.errorReply(env, "internal", err.Error())
	}
	t.MyEntry = &entryProof{LogSeq: rec.Seq, LogHash: rec.Hash}
	t.State, _ = conformance.Transition(t.State, "receipt") // -> SETTLED
	c.Busy, c.BusyTransfer = false, ""
	t.Receipt = n.buildSigned("transfer.receipt", c.PeerBase, envStr(env, "id"), map[string]any{
		"transfer_id": t.ID,
		"entry":       map[string]any{"log_seq": rec.Seq, "log_hash": rec.Hash},
	})
	return t.Receipt
}

// handleTransferReceipt — payer records settlement and unlocks the contact.
func (n *Node) handleTransferReceipt(env map[string]any) map[string]any {
	p, _ := payloadOf(env)
	t := n.transfers[pStr(p, "transfer_id")]
	if t == nil || !t.Outgoing {
		return n.errorReply(env, "unknown-transfer", "no matching outgoing transfer")
	}
	if t.State == "SETTLED" {
		return nil
	}
	next, err := conformance.Transition(t.State, "receipt")
	if err != nil {
		return n.errorReply(env, "bad-state", err.Error())
	}
	t.State = next
	if pe, ok := p["entry"].(map[string]any); ok {
		ls, _ := pInt(pe, "log_seq")
		t.PeerEntry = &entryProof{LogSeq: ls, LogHash: pStr(pe, "log_hash")}
	}
	if c := n.contacts[t.ContactID]; c != nil {
		c.Busy, c.BusyTransfer = false, ""
	}
	return nil
}

// TransferState returns a transfer's current state (for tests/admin).
func (n *Node) TransferState(id string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if t := n.transfers[id]; t != nil {
		return t.State
	}
	return ""
}
