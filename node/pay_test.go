package lpnode

import (
	"testing"

	"github.com/LaPingvino/liquiditypub/node/ledger"
)

func TestPay(t *testing.T) {
	n, err := NewNode(Config{Base: "https://pay.test", Name: "Pay", CurrencyName: "C", CurrencySymbol: "C",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000,
		Members: []MemberConfig{{Name: "ana", Grant: 5_000_000}, {Name: "bea"}}})
	if err != nil {
		t.Fatal(err)
	}
	supply := n.Ledger().MoneySupply()
	if _, err := n.Pay("ana", "bea", 2_000_000); err != nil {
		t.Fatal(err)
	}
	if n.Balance(ledger.MemberPrefix+"ana") != 3_000_000 || n.Balance(ledger.MemberPrefix+"bea") != 2_000_000 {
		t.Fatalf("balances %d / %d", n.Balance(ledger.MemberPrefix+"ana"), n.Balance(ledger.MemberPrefix+"bea"))
	}
	if n.Ledger().MoneySupply() != supply {
		t.Fatal("a payment changed the money supply")
	}
	if _, err := n.Pay("bea", "ana", 2_000_001); err != ErrInsufficient {
		t.Fatalf("overdraft: %v", err)
	}
	for _, bad := range [][2]string{{"ana", "ana"}, {"ana", "cai"}, {"cai", "ana"}} {
		if _, err := n.Pay(bad[0], bad[1], 1); err == nil {
			t.Fatalf("%v went through", bad)
		}
	}
	if _, err := n.Pay("ana", "bea", 0); err == nil {
		t.Fatal("a zero payment went through")
	}
	if err := n.DeactivateMember("bea"); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Pay("ana", "bea", 1); err == nil {
		t.Fatal("paid an inactive member")
	}
	if err := n.Ledger().VerifyChain(); err != nil {
		t.Fatal(err)
	}
}
