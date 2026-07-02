// genvectors regenerates the signature-bearing test vectors
// (envelope_sign.json, envelope_validation.json) deterministically from a
// fixed ed25519 seed. Run from the conformance directory:
//
//	go run ./cmd/genvectors
//
// ed25519 signatures are deterministic (RFC 8032), so output is byte-stable.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"

	conformance "github.com/LaPingvino/liquiditypub/conformance"
)

const (
	keyID   = "https://riverside.example/.well-known/liquiditypub#nk1"
	now     = "2026-07-02T12:00:00Z"
	outDir  = "vectors"
	baseSeq = 5
)

func seed() []byte {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = byte(i)
	}
	return s
}

func envelope(id string, seq int64, created string) map[string]any {
	return map[string]any{
		"lp":      "0.2",
		"id":      id,
		"type":    "ping",
		"from":    "https://riverside.example",
		"to":      "https://hilltop.example",
		"seq":     seq,
		"re":      nil,
		"created": created,
		"payload": map[string]any{},
	}
}

func sign(env map[string]any, priv ed25519.PrivateKey) map[string]any {
	sig, err := conformance.SignEnvelope(env, priv)
	if err != nil {
		log.Fatal(err)
	}
	env["sig"] = map[string]any{
		"key":   keyID,
		"alg":   "ed25519",
		"value": base64.RawURLEncoding.EncodeToString(sig),
	}
	return env
}

func writeJSON(name string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(outDir+"/"+name, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote", outDir+"/"+name)
}

func main() {
	priv := ed25519.NewKeyFromSeed(seed())
	pub := priv.Public().(ed25519.PublicKey)

	// ── envelope_sign.json ──────────────────────────────────────────────
	env := envelope("urn:uuid:11111111-1111-1111-1111-111111111111", 1, now)
	canonical, err := conformance.SigningBytes(env)
	if err != nil {
		log.Fatal(err)
	}
	signed := sign(env, priv)
	writeJSON("envelope_sign.json", map[string]any{
		"description":    "PROTOCOL §4 — signature over JCS(envelope minus sig), ed25519. Deterministic key from seed_hex; RFC 8032 signatures are deterministic, so re-signing must reproduce sig exactly.",
		"seed_hex":       hex.EncodeToString(seed()),
		"public_key_b64": base64.RawURLEncoding.EncodeToString(pub),
		"key_id":         keyID,
		"canonical":      string(canonical),
		"envelope":       signed,
	})

	// ── envelope_validation.json ────────────────────────────────────────
	mk := func(id string, seq int64, created string) map[string]any {
		return sign(envelope(id, seq, created), priv)
	}
	tampered := mk("urn:uuid:22222222-2222-2222-2222-22222222bad1", baseSeq+1, now)
	tampered["payload"] = map[string]any{"tampered": true} // after signing

	wrongKey := mk("urn:uuid:22222222-2222-2222-2222-22222222bad2", baseSeq+1, now)
	wrongKey["sig"].(map[string]any)["key"] = "https://riverside.example/.well-known/liquiditypub#nk9"

	scenarios := []map[string]any{
		{"name": "valid message", "envelope": mk("urn:uuid:33333333-3333-3333-3333-333333333301", baseSeq+1, "2026-07-02T11:59:00Z"), "expect": "ok"},
		{"name": "payload tampered after signing", "envelope": tampered, "expect": "bad-signature"},
		{"name": "unknown key id", "envelope": wrongKey, "expect": "unknown-key"},
		{"name": "replayed id", "envelope": mk("urn:uuid:33333333-3333-3333-3333-333333333300", baseSeq+2, now), "expect": "duplicate"},
		{"name": "stale seq", "envelope": mk("urn:uuid:33333333-3333-3333-3333-333333333302", baseSeq, now), "expect": "stale-seq"},
		{"name": "older than 7 days", "envelope": mk("urn:uuid:33333333-3333-3333-3333-333333333303", baseSeq+1, "2026-06-24T12:00:00Z"), "expect": "too-old"},
		{"name": "more than 1h in the future", "envelope": mk("urn:uuid:33333333-3333-3333-3333-333333333304", baseSeq+1, "2026-07-02T13:30:00Z"), "expect": "future"},
	}
	writeJSON("envelope_validation.json", map[string]any{
		"description": "PROTOCOL §4 validation order: unknown-key → bad-signature → duplicate → stale-seq → too-old/future → ok. State below is the receiver's channel view at evaluation time.",
		"keys":        map[string]string{keyID: base64.RawURLEncoding.EncodeToString(pub)},
		"now":         now,
		"state": map[string]any{
			"last_seq": baseSeq,
			"seen_ids": []string{"urn:uuid:33333333-3333-3333-3333-333333333300"},
		},
		"scenarios": scenarios,
	})
}
