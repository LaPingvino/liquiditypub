package conformance

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func loadVectors(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile("vectors/" + name)
	if err != nil {
		t.Fatalf("read %s: %v (run `go run ./cmd/genvectors` for generated vectors)", name, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}

// Cross-language anchor: the canonical form of the sample envelope, computed
// independently with Python (json.dumps sorted, compact) during vector design.
const pythonCanonicalEnvelope = `{"created":"2026-07-02T12:00:00Z","from":"https://riverside.example","id":"urn:uuid:11111111-1111-1111-1111-111111111111","lp":"0.2","payload":{},"re":null,"seq":1,"to":"https://hilltop.example","type":"ping"}`

func TestCanonicalMatchesPython(t *testing.T) {
	env := map[string]any{
		"lp": "0.2", "id": "urn:uuid:11111111-1111-1111-1111-111111111111",
		"type": "ping", "from": "https://riverside.example", "to": "https://hilltop.example",
		"seq": int64(1), "re": nil, "created": "2026-07-02T12:00:00Z",
		"payload": map[string]any{},
	}
	got, err := Canonical(env)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != pythonCanonicalEnvelope {
		t.Errorf("canonical mismatch:\n got %s\nwant %s", got, pythonCanonicalEnvelope)
	}
}

func TestCanonicalRejectsFloats(t *testing.T) {
	if _, err := Canonical(map[string]any{"x": json.Number("1.5")}); err == nil {
		t.Error("float json.Number must be rejected")
	}
	if _, err := Canonical(map[string]any{"x": 1.5}); err == nil {
		t.Error("float64 must be rejected")
	}
}

func TestPoolPricingVectors(t *testing.T) {
	var v struct {
		Cases []struct {
			Name  string `json:"name"`
			RSrc  int64  `json:"r_src"`
			RDst  int64  `json:"r_dst"`
			Src   int64  `json:"src"`
			Dst   int64  `json:"dst"`
			Error string `json:"error"`
		} `json:"cases"`
	}
	loadVectors(t, "pool_pricing.json", &v)
	for _, c := range v.Cases {
		dst, err := PoolPrice(c.RSrc, c.RDst, c.Src)
		if c.Error != "" {
			wantErr := map[string]error{
				"dust": ErrDust, "non-positive": ErrNonPositive, "empty-pool": ErrEmptyPool,
			}[c.Error]
			if !errors.Is(err, wantErr) {
				t.Errorf("%s: want error %q, got (dst=%d, err=%v)", c.Name, c.Error, dst, err)
			}
			continue
		}
		if err != nil || dst != c.Dst {
			t.Errorf("%s: want dst=%d, got dst=%d err=%v", c.Name, c.Dst, dst, err)
		}
	}
}

func TestChannelHashVectors(t *testing.T) {
	var v struct {
		ContactID string   `json:"contact_id"`
		Ops       [][]any  `json:"ops"`
		Roots     []string `json:"roots"`
	}
	loadVectors(t, "channel_hash.json", &v)
	root := ChannelRoot0(v.ContactID)
	if hex.EncodeToString(root[:]) != v.Roots[0] {
		t.Fatalf("root0: got %x want %s", root, v.Roots[0])
	}
	for i, op := range v.Ops {
		src, _ := op[2].(float64) // amounts in this vector fit float64 exactly (< 2^53)
		dst, _ := op[3].(float64)
		next, err := ChannelNext(root, op[0].(string), op[1].(string), int64(src), int64(dst))
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(next[:]) != v.Roots[i+1] {
			t.Errorf("root%d: got %x want %s", i+1, next, v.Roots[i+1])
		}
		root = next
	}
}

func TestUDVectors(t *testing.T) {
	var v struct {
		Cases []struct {
			Name          string `json:"name"`
			MoneySupply   int64  `json:"money_supply"`
			CPeriodPpm    int64  `json:"c_period_ppm"`
			UDWeightTotal int64  `json:"ud_weight_total"`
			UDBase        int64  `json:"ud_base"`
			Recipients    []struct {
				Weight int64 `json:"weight"`
				Amount int64 `json:"amount"`
			} `json:"recipients"`
		} `json:"cases"`
	}
	loadVectors(t, "ud_reference.json", &v)
	for _, c := range v.Cases {
		base, err := UDBase(c.MoneySupply, c.CPeriodPpm, c.UDWeightTotal)
		if err != nil || base != c.UDBase {
			t.Errorf("%s: ud_base got %d err=%v, want %d", c.Name, base, err, c.UDBase)
		}
		for _, r := range c.Recipients {
			if got := RecipientUD(base, r.Weight); got != r.Amount {
				t.Errorf("%s: weight %d got %d want %d", c.Name, r.Weight, got, r.Amount)
			}
		}
	}
}

func TestStateMachineVectors(t *testing.T) {
	var v struct {
		Transitions []struct {
			From    string `json:"from"`
			Event   string `json:"event"`
			To      string `json:"to"`
			Invalid bool   `json:"invalid"`
		} `json:"transitions"`
	}
	loadVectors(t, "state_machine.json", &v)
	for _, tr := range v.Transitions {
		next, err := Transition(tr.From, tr.Event)
		if tr.Invalid {
			if err == nil {
				t.Errorf("%s + %s: want invalid, got %s", tr.From, tr.Event, next)
			}
			continue
		}
		if err != nil || next != tr.To {
			t.Errorf("%s + %s: want %s, got (%s, %v)", tr.From, tr.Event, tr.To, next, err)
		}
	}
}

func TestEnvelopeSignVector(t *testing.T) {
	var v struct {
		SeedHex   string         `json:"seed_hex"`
		PublicKey string         `json:"public_key_b64"`
		Canonical string         `json:"canonical"`
		Envelope  map[string]any `json:"envelope"`
	}
	loadVectors(t, "envelope_sign.json", &v)

	seed, _ := hex.DecodeString(v.SeedHex)
	priv := ed25519.NewKeyFromSeed(seed)
	if got := base64.RawURLEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)); got != v.PublicKey {
		t.Fatalf("public key derivation: got %s want %s", got, v.PublicKey)
	}

	env := reDecode(t, v.Envelope)
	canonical, err := SigningBytes(env)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != v.Canonical {
		t.Errorf("signing bytes:\n got %s\nwant %s", canonical, v.Canonical)
	}
	// RFC 8032: deterministic — re-signing must reproduce the vector signature.
	sig, err := SignEnvelope(env, priv)
	if err != nil {
		t.Fatal(err)
	}
	want := env["sig"].(map[string]any)["value"].(string)
	if base64.RawURLEncoding.EncodeToString(sig) != want {
		t.Error("re-signed signature differs from vector")
	}
	ok, err := VerifyEnvelope(env, sig, priv.Public().(ed25519.PublicKey))
	if err != nil || !ok {
		t.Errorf("verification failed: %v", err)
	}
}

func TestEnvelopeValidationVectors(t *testing.T) {
	var v struct {
		Keys  map[string]string `json:"keys"`
		Now   string            `json:"now"`
		State struct {
			LastSeq int64    `json:"last_seq"`
			SeenIDs []string `json:"seen_ids"`
		} `json:"state"`
		Scenarios []struct {
			Name     string         `json:"name"`
			Envelope map[string]any `json:"envelope"`
			Expect   string         `json:"expect"`
		} `json:"scenarios"`
	}
	loadVectors(t, "envelope_validation.json", &v)

	keys := map[string]ed25519.PublicKey{}
	for id, b64 := range v.Keys {
		raw, err := base64.RawURLEncoding.DecodeString(b64)
		if err != nil {
			t.Fatal(err)
		}
		keys[id] = ed25519.PublicKey(raw)
	}
	now, err := time.Parse(time.RFC3339, v.Now)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, id := range v.State.SeenIDs {
		seen[id] = true
	}
	st := ValidationState{Keys: keys, SeenIDs: seen, LastSeq: v.State.LastSeq, Now: now}

	for _, sc := range v.Scenarios {
		if got := ValidateEnvelope(reDecode(t, sc.Envelope), st); got != sc.Expect {
			t.Errorf("%s: want %s, got %s", sc.Name, sc.Expect, got)
		}
	}
}

// reDecode round-trips a vector envelope through DecodeJSON so numbers become
// json.Number (as they would arriving off the wire), not float64.
func reDecode(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	v, err := DecodeJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}
