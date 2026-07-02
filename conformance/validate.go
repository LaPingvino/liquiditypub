package conformance

import (
	"crypto/ed25519"
	"encoding/base64"
	"time"
)

// Envelope validation (PROTOCOL §4), in specified order:
//  1. resolve sig.key against the sender's published keys → "unknown-key"
//  2. verify the signature → "bad-signature"
//  3. reject already-processed ids (idempotent replay) → "duplicate"
//  4. reject seq ≤ last processed for the channel → "stale-seq"
//  5. reject created older than 7 days → "too-old", or >1h in the future → "future"
//  6. ok → dispatch by type
const (
	VerdictOK           = "ok"
	VerdictUnknownKey   = "unknown-key"
	VerdictBadSignature = "bad-signature"
	VerdictDuplicate    = "duplicate"
	VerdictStaleSeq     = "stale-seq"
	VerdictTooOld       = "too-old"
	VerdictFuture       = "future"
	VerdictMalformed    = "malformed"
)

const (
	MaxAge    = 7 * 24 * time.Hour
	MaxFuture = time.Hour
)

// ValidationState is the receiver's view of one sender channel.
type ValidationState struct {
	Keys    map[string]ed25519.PublicKey // key id → public key
	SeenIDs map[string]bool
	LastSeq int64
	Now     time.Time
}

// ValidateEnvelope returns one of the Verdict* codes.
func ValidateEnvelope(env map[string]any, st ValidationState) string {
	sig, ok := env["sig"].(map[string]any)
	if !ok {
		return VerdictMalformed
	}
	keyID, _ := sig["key"].(string)
	pub, ok := st.Keys[keyID]
	if !ok {
		return VerdictUnknownKey
	}
	sigVal, _ := sig["value"].(string)
	raw, err := base64.RawURLEncoding.DecodeString(sigVal)
	if err != nil {
		return VerdictMalformed
	}
	valid, err := VerifyEnvelope(env, raw, pub)
	if err != nil || !valid {
		return VerdictBadSignature
	}
	id, _ := env["id"].(string)
	if id == "" {
		return VerdictMalformed
	}
	if st.SeenIDs[id] {
		return VerdictDuplicate
	}
	seq, ok := envInt(env, "seq")
	if !ok {
		return VerdictMalformed
	}
	if seq <= st.LastSeq {
		return VerdictStaleSeq
	}
	createdStr, _ := env["created"].(string)
	created, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return VerdictMalformed
	}
	if st.Now.Sub(created) > MaxAge {
		return VerdictTooOld
	}
	if created.Sub(st.Now) > MaxFuture {
		return VerdictFuture
	}
	return VerdictOK
}

func envInt(env map[string]any, key string) (int64, bool) {
	switch n := env[key].(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case interface{ Int64() (int64, error) }: // json.Number
		v, err := n.Int64()
		return v, err == nil
	}
	return 0, false
}
