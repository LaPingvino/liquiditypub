package ledger

import "testing"

func TestConservationRejectsUnbalanced(t *testing.T) {
	l := New()
	_, err := l.Append(Tx{
		ID: "urn:uuid:1", Type: TxPayment, Created: "2026-07-02T00:00:00Z",
		Entries: []Entry{{Account: "m:a", Amount: 10}, {Account: "m:b", Amount: -9}},
	})
	if err == nil {
		t.Fatal("expected unbalanced rejection")
	}
	if l.Len() != 0 {
		t.Fatalf("rejected tx still appended: len=%d", l.Len())
	}
}

func TestNodeWalletNonNegative(t *testing.T) {
	l := New()
	// Fund a node wallet, then try to overdraw it.
	if _, err := l.Append(Tx{
		ID: "urn:uuid:seed", Type: TxSeed, Created: "t",
		Entries: []Entry{{Account: "node:peer.example", Amount: 100}, {Account: AcctIssuance, Amount: -100}},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := l.Append(Tx{
		ID: "urn:uuid:over", Type: TxTransferIn, Created: "t",
		Entries: []Entry{{Account: "node:peer.example", Amount: -150}, {Account: "m:bob", Amount: 150}},
	})
	if err == nil {
		t.Fatal("expected non-negative node-wallet rejection")
	}
	if l.Balance("node:peer.example") != 100 {
		t.Fatalf("balance mutated on rejected append: %d", l.Balance("node:peer.example"))
	}
}

func TestHashChainAndMoneySupply(t *testing.T) {
	l := New()
	for i, amt := range []int64{40, 60} {
		if _, err := l.Append(Tx{
			ID: "urn:uuid:g", Type: TxIssuanceGrant, Created: "t",
			Entries: []Entry{{Account: "m:x", Amount: amt}, {Account: AcctIssuance, Amount: -amt}},
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if ms := l.MoneySupply(); ms != 100 {
		t.Fatalf("money supply = %d, want 100", ms)
	}
	if err := l.VerifyChain(); err != nil {
		t.Fatalf("chain verify: %v", err)
	}
	// Tampering with a record must break the chain.
	recs := l.Records()
	recs[0].Tx.Entries[0].Amount = 41
	if err := l.VerifyChain(); err == nil {
		t.Fatal("expected chain verification to catch tampering")
	}
}
