package lpnode

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/LaPingvino/liquiditypub/conformance"
)

// newID returns a fresh urn:uuid identifier (§2). Version-4 random.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("urn:uuid:%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// buildSigned constructs a signed envelope addressed to toBase, allocating the
// next per-channel seq. MUST be called with n.mu held.
func (n *Node) buildSigned(typ, toBase, re string, payload map[string]any) map[string]any {
	toHost := host(toBase)
	n.outSeq[toHost]++
	env := map[string]any{
		"lp":      "0.2",
		"id":      newID(),
		"type":    typ,
		"from":    n.cfg.Base,
		"to":      toBase,
		"seq":     n.outSeq[toHost],
		"created": n.clock().Format(time.RFC3339),
		"payload": payload,
	}
	if re == "" {
		env["re"] = nil
	} else {
		env["re"] = re
	}
	sig, err := conformance.SignEnvelope(env, n.priv)
	if err != nil {
		// Signing only fails on a float sneaking into the payload — a
		// programming error we surface loudly rather than ship unsigned.
		panic(fmt.Sprintf("sign envelope: %v", err))
	}
	env["sig"] = map[string]any{
		"key":   keyID(n.cfg.Base, n.keyLocalID),
		"alg":   "ed25519",
		"value": base64.RawURLEncoding.EncodeToString(sig),
	}
	// Record it in the peer's outbox for the pull binding (§5.1).
	n.outbox[toHost] = append(n.outbox[toHost], env)
	return env
}

// ── typed payload readers (inbound envelopes decode via json.Number) ─────────

func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// asInt accepts int64 (hand-built) or json.Number (decoded) — never a float.
func asInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	case interface{ Int64() (int64, error) }: // json.Number
		i, err := x.Int64()
		return i, err == nil
	}
	return 0, false
}

func payloadOf(env map[string]any) (map[string]any, bool) {
	p, ok := env["payload"].(map[string]any)
	return p, ok
}

func pStr(p map[string]any, key string) string {
	s, _ := asString(p[key])
	return s
}

func pInt(p map[string]any, key string) (int64, bool) {
	return asInt(p[key])
}

func envStr(env map[string]any, key string) string {
	s, _ := asString(env[key])
	return s
}
