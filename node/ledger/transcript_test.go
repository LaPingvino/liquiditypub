package ledger

import (
	"encoding/json"
	"os"
	"testing"
)

// TestLedgerTranscriptVector replays the neutral ledger_transcript.json golden
// (whose record hashes are computed straight from the spec canonical rule, not
// from this package) and asserts the Go ledger reproduces every record hash,
// the head, and the derived balances. The PHP ledger checks against the same
// file, so the two implementations agree transitively (PROTOCOL §9.2).
func TestLedgerTranscriptVector(t *testing.T) {
	raw, err := os.ReadFile("../../conformance/vectors/ledger_transcript.json")
	if err != nil {
		t.Fatalf("read vector: %v", err)
	}
	var vec struct {
		Txs     []Tx             `json:"txs"`
		Records []Record         `json:"records"`
		Head    string           `json:"head"`
		Supply  int64            `json:"money_supply"`
		Bal     map[string]int64 `json:"balances"`
	}
	if err := json.Unmarshal(raw, &vec); err != nil {
		t.Fatalf("decode vector: %v", err)
	}

	l := New()
	for i, tx := range vec.Txs {
		rec, err := l.Append(tx)
		if err != nil {
			t.Fatalf("append tx %d (%s): %v", i+1, tx.Type, err)
		}
		if rec.Hash != vec.Records[i].Hash {
			t.Errorf("record %d hash = %s, want %s", i+1, rec.Hash, vec.Records[i].Hash)
		}
		if rec.Prev != vec.Records[i].Prev {
			t.Errorf("record %d prev = %q, want %q", i+1, rec.Prev, vec.Records[i].Prev)
		}
	}
	if l.Head() != vec.Head {
		t.Errorf("head = %s, want %s", l.Head(), vec.Head)
	}
	if l.MoneySupply() != vec.Supply {
		t.Errorf("money supply = %d, want %d", l.MoneySupply(), vec.Supply)
	}
	for acct, want := range vec.Bal {
		if got := l.Balance(acct); got != want {
			t.Errorf("balance %s = %d, want %d", acct, got, want)
		}
	}
}
