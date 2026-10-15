package main

import (
	"context"
	"fmt"
	"os"
	"testing"

	"cloud.google.com/go/datastore"
	lpnode "github.com/LaPingvino/liquiditypub/node"
)

// Runs only against the Datastore emulator:
//
//	DATASTORE_EMULATOR_HOST=localhost:8432 go test ./gae/
func emulator(t *testing.T) *datastore.Client {
	if os.Getenv("DATASTORE_EMULATOR_HOST") == "" {
		t.Skip("needs the Datastore emulator")
	}
	db, err := datastore.NewClient(context.Background(), "lp-test")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func testCfg() lpnode.Config {
	return lpnode.Config{
		Base: "https://kafumu.test", Name: "CoffeeBucks", CurrencyName: "CoffeeBucks", CurrencySymbol: "K$",
		CPeriodPpm: 274, UDPeriod: "P1D", GenesisUD: 1_000_000, Transparency: "pseudonymous", AdminToken: "x",
	}
}

// A node survives a restart from Datastore: members, balances, the ledger
// chain, the key — and a ledger bigger than one put batch is stored record by
// record, so no entity ever nears the 1 MiB limit.
func TestDSStoreRoundTrip(t *testing.T) {
	db := emulator(t)
	id := fmt.Sprintf("rt-%d", os.Getpid())
	n, err := lpnode.Restore(testCfg(), NewDSStore(db, id))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if err := n.AddMember(fmt.Sprintf("m%d", i), "", 0); err != nil {
			t.Fatal(err)
		}
	}
	for d := 0; d < 20; d++ { // 30 members × 20 dividends: 600 records, more than one batch
		if _, err := n.RunUD(); err != nil {
			t.Fatal(err)
		}
	}
	want, head, bal := n.Ledger().Len(), n.Ledger().Head(), n.Balance("member:m7")
	if want < putBatch {
		t.Fatalf("only %d records: the test should cross a put batch", want)
	}

	back, err := lpnode.Restore(testCfg(), NewDSStore(db, id))
	if err != nil {
		t.Fatal(err)
	}
	if back.Ledger().Len() != want || back.Ledger().Head() != head || back.Balance("member:m7") != bal || !back.MemberActive("m29") {
		t.Fatalf("restored: %d records head %s balance %d; want %d %s %d", back.Ledger().Len(), back.Ledger().Head(), back.Balance("member:m7"), want, head, bal)
	}
	if err := back.Ledger().VerifyChain(); err != nil {
		t.Fatalf("restored chain: %v", err)
	}
}

// Two writers (two instances during a deploy): the one that saves second gets
// ErrConflict instead of forking the ledger.
func TestDSStoreSingleWriter(t *testing.T) {
	db := emulator(t)
	id := fmt.Sprintf("sw-%d", os.Getpid())
	a := NewDSStore(db, id)
	if _, err := lpnode.Restore(testCfg(), a); err != nil {
		t.Fatal(err)
	}
	b := NewDSStore(db, id)
	data, err := b.Load()
	if err != nil || data == nil {
		t.Fatalf("load: %v", err)
	}
	if err := a.Save(data); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(data); err != ErrConflict {
		t.Fatalf("second writer: %v, want ErrConflict", err)
	}
}

func TestAdminTokenKept(t *testing.T) {
	db := emulator(t)
	id := fmt.Sprintf("tok-%d", os.Getpid())
	a, err := adminToken(context.Background(), db, id)
	if err != nil || len(a) != 64 {
		t.Fatalf("token %q, %v", a, err)
	}
	if b, _ := adminToken(context.Background(), db, id); b != a {
		t.Fatal("a second start made a new token")
	}
}
