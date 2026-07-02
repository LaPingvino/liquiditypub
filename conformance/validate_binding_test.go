package conformance

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"
)

// TestSigKeyBoundToSender proves that a signature verified against a known key
// is still rejected when that key does not belong to the envelope's `from`
// (§4, §13): otherwise any peer whose key we hold could impersonate any other.
func TestSigKeyBoundToSender(t *testing.T) {
	// Attacker's real key, published under the attacker's origin.
	pub, priv, err := ed25519.GenerateKey(nil2Reader{})
	if err != nil {
		t.Fatal(err)
	}
	attackerKeyID := "https://attacker.example/.well-known/liquiditypub#nk1"

	sign := func(env map[string]any) map[string]any {
		sig, err := SignEnvelope(env, priv)
		if err != nil {
			t.Fatal(err)
		}
		env["sig"] = map[string]any{
			"key": attackerKeyID, "alg": "ed25519",
			"value": base64.RawURLEncoding.EncodeToString(sig),
		}
		return env
	}

	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	st := ValidationState{
		Keys:    map[string]ed25519.PublicKey{attackerKeyID: pub},
		SeenIDs: map[string]bool{},
		LastSeq: 0,
		Now:     now,
	}

	// Honest case: from matches the signing key's origin → ok.
	honest := sign(map[string]any{
		"lp": "0.2", "id": "urn:uuid:1", "type": "ping",
		"from": "https://attacker.example", "to": "https://victim.example",
		"seq": int64(1), "created": now.Format(time.RFC3339), "re": nil, "payload": map[string]any{},
	})
	if v := ValidateEnvelope(honest, st); v != VerdictOK {
		t.Fatalf("honest envelope: verdict %q, want ok", v)
	}

	// Spoof: same valid signature/key, but claims to originate from the victim.
	st.SeenIDs = map[string]bool{}
	spoof := sign(map[string]any{
		"lp": "0.2", "id": "urn:uuid:2", "type": "ping",
		"from": "https://bank.example", "to": "https://victim.example",
		"seq": int64(2), "created": now.Format(time.RFC3339), "re": nil, "payload": map[string]any{},
	})
	if v := ValidateEnvelope(spoof, st); v != VerdictUnknownKey {
		t.Fatalf("spoofed from: verdict %q, want unknown-key", v)
	}
}

// nil2Reader is crypto/rand's Reader stand-in that ed25519.GenerateKey accepts;
// using the package rand reader keeps the test deterministic-free but valid.
type nil2Reader struct{}

func (nil2Reader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(i*7 + 3)
	}
	return len(p), nil
}
