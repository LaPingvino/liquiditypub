// Package ledger is the double-entry, append-only, hash-linked ledger that
// backs a LiquidityPub node (PROTOCOL §9, reference model). It is pure in the
// sense that matters for conformance: every reserve and balance is a
// deterministic function of the ordered transaction history, and the two hard
// invariants — SUM(entries)=0 and non-negative node wallets — are enforced on
// every append. Canonicalization/hashing is delegated to the conformance
// package so the on-disk log hashes identically to the spec's reference.
package ledger

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/LaPingvino/liquiditypub/conformance"
)

// Account id namespaces (PROTOCOL §9.1).
const (
	NodeWalletPrefix = "node:" // node:<host> — a peer's reserve here, in our currency
	MemberPrefix     = "m:"    // m:<opaque> — a member account
	FundPrefix       = "fund:" // fund:<opaque> — a community fund
	AcctIssuance     = "issuance"
	AcctTreasury     = "treasury"
)

// Transaction types (PROTOCOL §9.1).
const (
	TxIssuanceUD    = "issuance.ud"
	TxIssuanceGrant = "issuance.grant"
	TxPayment       = "payment"
	TxTransferOut   = "transfer.out"
	TxTransferIn    = "transfer.in"
	TxReserveAdjust = "reserve.adjust"
	TxSeed          = "contact.seed"
)

// Entry is one leg of a transaction.
type Entry struct {
	Account string `json:"account"`
	Amount  int64  `json:"amount"`
}

// Tx is a balanced set of entries; its entries MUST sum to zero.
type Tx struct {
	ID      string  `json:"id"`
	Type    string  `json:"type"`
	Ref     string  `json:"ref,omitempty"`
	Created string  `json:"created"`
	Entries []Entry `json:"entries"`
}

// Record is one hash-linked log entry (PROTOCOL §9.2).
type Record struct {
	Seq       int64   `json:"seq"`
	Prev      string  `json:"prev"`
	Tx        Tx      `json:"tx"`
	MemberSig *string `json:"member_sig"`
	Hash      string  `json:"hash"`
}

var (
	ErrUnbalanced   = errors.New("ledger: entries do not sum to zero")
	ErrNodeNegative = errors.New("ledger: node wallet would go negative")
	ErrEmptyTx      = errors.New("ledger: transaction has no entries")
)

// Ledger is the append-only log plus derived balances. Not safe for concurrent
// use; the node serializes writes behind a single lock.
type Ledger struct {
	log      []Record
	balances map[string]int64
}

func New() *Ledger {
	return &Ledger{balances: map[string]int64{}}
}

// Load rebuilds a ledger from a persisted record slice, verifying the hash
// chain and conservation before deriving balances. A tampered or truncated log
// is rejected, so a node never resumes on a corrupt ledger.
func Load(records []Record) (*Ledger, error) {
	l := &Ledger{balances: map[string]int64{}, log: records}
	if err := l.VerifyChain(); err != nil {
		return nil, err
	}
	for _, rec := range records {
		for _, e := range rec.Tx.Entries {
			l.balances[e.Account] += e.Amount
		}
	}
	return l, nil
}

// Balance returns an account's current balance (0 if never touched).
func (l *Ledger) Balance(account string) int64 { return l.balances[account] }

// Head returns the hash of the last record, or "" for an empty log.
func (l *Ledger) Head() string {
	if len(l.log) == 0 {
		return ""
	}
	return l.log[len(l.log)-1].Hash
}

// Len is the number of records (the next seq is Len()+1).
func (l *Ledger) Len() int { return len(l.log) }

// Records returns the log slice (read-only; callers must not mutate).
func (l *Ledger) Records() []Record { return l.log }

// MoneySupply is the total value issued into circulation: the negative of the
// system source accounts. Because SUM(all)=0, this equals the sum of every
// member, fund, and node-wallet balance.
func (l *Ledger) MoneySupply() int64 {
	return -(l.balances[AcctIssuance] + l.balances[AcctTreasury])
}

// Append validates and commits a transaction, returning the new record. It
// enforces conservation (SUM=0) and non-negative node wallets (PROTOCOL §8.1),
// then chains the hash. On any error the ledger is unchanged.
func (l *Ledger) Append(tx Tx) (Record, error) {
	if len(tx.Entries) == 0 {
		return Record{}, ErrEmptyTx
	}
	var sum int64
	for _, e := range tx.Entries {
		sum += e.Amount
	}
	if sum != 0 {
		return Record{}, fmt.Errorf("%w: sum=%d", ErrUnbalanced, sum)
	}
	// Non-negativity check must be all-or-nothing: compute projected balances
	// for touched node wallets before mutating anything.
	for _, e := range tx.Entries {
		if isNodeWallet(e.Account) && l.balances[e.Account]+e.Amount < 0 {
			return Record{}, fmt.Errorf("%w: %s %d%+d", ErrNodeNegative,
				e.Account, l.balances[e.Account], e.Amount)
		}
	}
	rec := Record{
		Seq:  int64(len(l.log)) + 1,
		Prev: l.Head(),
		Tx:   tx,
	}
	hash, err := recordHash(rec)
	if err != nil {
		return Record{}, err
	}
	rec.Hash = hash
	// Commit.
	for _, e := range tx.Entries {
		l.balances[e.Account] += e.Amount
	}
	l.log = append(l.log, rec)
	return rec, nil
}

// VerifyChain recomputes every hash link and the conservation invariant across
// the whole log — a self-audit used in tests and on load.
func (l *Ledger) VerifyChain() error {
	var running = map[string]int64{}
	prev := ""
	for i, rec := range l.log {
		if rec.Seq != int64(i)+1 {
			return fmt.Errorf("ledger: record %d has seq %d", i, rec.Seq)
		}
		if rec.Prev != prev {
			return fmt.Errorf("ledger: record %d prev mismatch", rec.Seq)
		}
		var sum int64
		for _, e := range rec.Tx.Entries {
			sum += e.Amount
			running[e.Account] += e.Amount
			if isNodeWallet(e.Account) && running[e.Account] < 0 {
				return fmt.Errorf("ledger: record %d drove %s negative", rec.Seq, e.Account)
			}
		}
		if sum != 0 {
			return fmt.Errorf("ledger: record %d unbalanced (sum=%d)", rec.Seq, sum)
		}
		want := rec.Hash
		bare := rec
		got, err := recordHash(bare)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("ledger: record %d hash mismatch", rec.Seq)
		}
		prev = rec.Hash
	}
	return nil
}

func isNodeWallet(account string) bool {
	return len(account) > len(NodeWalletPrefix) && account[:len(NodeWalletPrefix)] == NodeWalletPrefix
}

// recordHash is SHA-256 of JCS(record without the hash field), base64url
// unpadded (PROTOCOL §9.2). Delegated to conformance.Canonical so a node's log
// hashes byte-for-byte as the spec's reference does.
func recordHash(rec Record) (string, error) {
	m := map[string]any{
		"seq":  rec.Seq,
		"prev": rec.Prev,
		"tx":   txMap(rec.Tx),
	}
	if rec.MemberSig == nil {
		m["member_sig"] = nil
	} else {
		m["member_sig"] = *rec.MemberSig
	}
	b, err := conformance.Canonical(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func txMap(tx Tx) map[string]any {
	entries := make([]any, len(tx.Entries))
	for i, e := range tx.Entries {
		entries[i] = map[string]any{"account": e.Account, "amount": e.Amount}
	}
	m := map[string]any{
		"id":      tx.ID,
		"type":    tx.Type,
		"created": tx.Created,
		"entries": entries,
	}
	if tx.Ref != "" {
		m["ref"] = tx.Ref
	}
	return m
}
