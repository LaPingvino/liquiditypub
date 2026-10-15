package lpnode

import (
	"testing"

	"github.com/LaPingvino/liquiditypub/node/ledger"
)

func leaveNode(t *testing.T) *Node {
	t.Helper()
	n, err := NewNode(Config{Base: "https://leave.test", Name: "L", CurrencyName: "C", CurrencySymbol: "C",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000,
		Members: []MemberConfig{{Name: "ana", Grant: 9_000_000}, {Name: "bea", Grant: 1_000_000}, {Name: "cai", Grant: 1_000_000}, {Name: "dan", Grant: 1_000_000}}})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func bal(n *Node, m string) int64 { return n.Balance(ledger.MemberPrefix + m) }

// Leaving: destroy shrinks the supply; give and redistribute keep it; the
// next dividend shares the fund out equally; nobody leaves twice; the chain
// and conservation hold throughout.
func TestLeave(t *testing.T) {
	n := leaveNode(t)
	supply := n.Ledger().MoneySupply()

	if n.ActiveMembers() != 4 {
		t.Fatalf("active %d", n.ActiveMembers())
	}
	if err := n.Leave("dan", LeaveDestroy, ""); err != nil {
		t.Fatal(err)
	}
	if n.ActiveMembers() != 3 {
		t.Fatal("someone who left still counts as active")
	}
	if st := n.IdentityDoc()["stats"].(map[string]any); st["active_members"] != int64(3) {
		t.Fatalf("identity doc active_members = %v, want 3 (PROTOCOL §3: peers price dilution from it)", st["active_members"])
	}
	if n.MemberActive("dan") || bal(n, "dan") != 0 || n.Ledger().MoneySupply() != supply-1_000_000 {
		t.Fatalf("destroy: active %v, balance %d, supply %d (was %d)", n.MemberActive("dan"), bal(n, "dan"), n.Ledger().MoneySupply(), supply)
	}
	supply = n.Ledger().MoneySupply()

	if err := n.Leave("cai", LeaveGive, "bea"); err != nil {
		t.Fatal(err)
	}
	if bal(n, "bea") != 2_000_000 || bal(n, "cai") != 0 || n.Ledger().MoneySupply() != supply {
		t.Fatalf("give: bea %d, cai %d", bal(n, "bea"), bal(n, "cai"))
	}
	if err := n.Leave("bea", LeaveGive, "cai"); err == nil {
		t.Fatal("gave to someone who left")
	}

	if err := n.Leave("ana", LeaveRedistribute, ""); err != nil {
		t.Fatal(err)
	}
	if n.Balance(FundRedistribute) != 9_000_000 || n.Ledger().MoneySupply() != supply {
		t.Fatalf("redistribute: fund %d", n.Balance(FundRedistribute))
	}
	beaBefore := bal(n, "bea")
	ud, err := n.RunUD()
	if err != nil {
		t.Fatal(err)
	}
	// bea is the only active member left: the dividend plus the whole fund.
	if got := bal(n, "bea") - beaBefore; got != ud+9_000_000 || n.Balance(FundRedistribute) != 0 {
		t.Fatalf("after the dividend bea got %d (ud %d), fund %d", got, ud, n.Balance(FundRedistribute))
	}
	if bal(n, "ana") != 0 || bal(n, "dan") != 0 {
		t.Fatal("someone who left got the dividend")
	}

	for _, m := range []string{"ana", "nobody"} {
		if err := n.Leave(m, LeaveKeep, ""); err == nil {
			t.Fatalf("%s left again", m)
		}
	}
	if err := n.Leave("bea", "vanish", ""); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	if err := n.Ledger().VerifyChain(); err != nil {
		t.Fatal(err)
	}
}

// The fund's remainder stays for the next time rather than being lost.
func TestRedistributeRemainder(t *testing.T) {
	n := leaveNode(t)
	n.Pay("ana", "dan", 1) // dan: 1_000_001
	if err := n.Leave("dan", LeaveRedistribute, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := n.RunUD(); err != nil {
		t.Fatal(err)
	}
	// 1_000_001 over three active members: 333_333 each, 2 left in the fund.
	if n.Balance(FundRedistribute) != 2 {
		t.Fatalf("remainder %d", n.Balance(FundRedistribute))
	}
}

// Rejoining: the same member, dividend again, balance as it was.
func TestReactivate(t *testing.T) {
	n := leaveNode(t)
	if err := n.Leave("bea", LeaveKeep, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := n.RunUD(); err != nil {
		t.Fatal(err)
	}
	kept := bal(n, "bea")
	if kept != 1_000_000 {
		t.Fatalf("a member who left got a dividend: %d", kept)
	}
	if err := n.Reactivate("bea"); err != nil || !n.MemberActive("bea") {
		t.Fatalf("reactivate: %v", err)
	}
	if err := n.Reactivate("bea"); err == nil {
		t.Fatal("reactivated an active member")
	}
	if err := n.Reactivate("nobody"); err == nil || n.MemberKnown("nobody") {
		t.Fatal("reactivated someone who never was a member")
	}
	if _, err := n.RunUD(); err != nil {
		t.Fatal(err)
	}
	if bal(n, "bea") <= kept {
		t.Fatal("no dividend after rejoining")
	}
}

func TestSetIdentity(t *testing.T) {
	n := leaveNode(t)
	supply := n.Ledger().MoneySupply()
	n.SetIdentity("Kaffeeklatsch", "Bohnen", "☕B")
	doc := n.IdentityDoc()
	cur := doc["currency"].(map[string]any)
	if doc["node"].(map[string]any)["name"] != "Kaffeeklatsch" || cur["name"] != "Bohnen" || cur["symbol"] != "☕B" {
		t.Fatalf("identity: %v %v", doc["node"], cur)
	}
	n.SetIdentity("", "", "BB")
	if c := n.IdentityDoc()["currency"].(map[string]any); c["name"] != "Bohnen" || c["symbol"] != "BB" {
		t.Fatalf("empty kept? %v", c)
	}
	if n.Ledger().MoneySupply() != supply {
		t.Fatal("a rename moved money")
	}
}
