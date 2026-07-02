package lpnode

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"time"
)

// Key rotation (PROTOCOL §3): a new key is added to the identity document and
// announced with a key.announce message signed by a currently valid old key.
// Verifiers accept any listed, non-revoked key; revocation stamps `revoked`.

// keyDocs renders the identity document's keys array (§3), listing every key —
// active, retired-but-valid, and revoked (with its timestamp). Caller holds mu.
func (n *Node) keyDocs() []any {
	out := make([]any, 0, len(n.ownKeys))
	for _, k := range n.ownKeys {
		var revoked any
		if k.Revoked != "" {
			revoked = k.Revoked
		}
		out = append(out, map[string]any{
			"id":         k.LocalID,
			"alg":        "ed25519",
			"public_key": pubB64(k.pub),
			"created":    k.Created,
			"revoked":    revoked,
		})
	}
	return out
}

// RotateKey generates a new signing key, announces it to every contact signed
// by the current (still-valid) key, then switches active signing to the new
// key. The old key stays valid until explicitly revoked, so in-flight messages
// signed by it still verify. Returns the new key's local id.
func (n *Node) RotateKey() (string, error) {
	n.mu.Lock()
	_, newPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		n.mu.Unlock()
		return "", err
	}
	newPub := newPriv.Public().(ed25519.PublicKey)
	newLocalID := fmt.Sprintf("#nk%d", len(n.ownKeys)+1)
	now := n.clock().Format(time.RFC3339)

	// Announce to every contact, signed by the CURRENT active key. buildSigned
	// uses the active key, so we must build all announcements before switching.
	var envs []struct {
		to  string
		env map[string]any
	}
	for _, c := range n.contactByHost {
		if c.Closed {
			continue
		}
		env := n.buildSigned("key.announce", c.PeerBase, "", map[string]any{
			"id":         newLocalID,
			"alg":        "ed25519",
			"public_key": pubB64(newPub),
			"created":    now,
		})
		envs = append(envs, struct {
			to  string
			env map[string]any
		}{c.PeerBase, env})
	}

	// Add and activate the new key.
	n.ownKeys = append(n.ownKeys, &ownKey{
		LocalID: newLocalID, Seed: b64(newPriv.Seed()), Created: now,
		priv: newPriv, pub: newPub,
	})
	n.priv, n.pub, n.keyLocalID = newPriv, newPub, newLocalID
	n.peerKeys[keyID(n.cfg.Base, newLocalID)] = newPub

	_ = n.persistLocked()
	n.mu.Unlock()

	for _, e := range envs {
		n.dispatch(e.to, e.env)
	}
	return newLocalID, nil
}

// RevokeKey marks a non-active key revoked (§3). The active key cannot be
// revoked — rotate first, then revoke the retired one.
func (n *Node) RevokeKey(localID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if localID == n.keyLocalID {
		return fmt.Errorf("cannot revoke the active key; rotate first")
	}
	for _, k := range n.ownKeys {
		if k.LocalID == localID {
			if k.Revoked != "" {
				return nil
			}
			k.Revoked = n.clock().Format(time.RFC3339)
			return n.persistLocked()
		}
	}
	return fmt.Errorf("no such key %q", localID)
}

// handleKeyAnnounce registers a peer's newly announced key (§3). The envelope
// itself was already verified against a known, valid peer key by
// ValidateEnvelope, which is exactly the "signed by a currently valid old key"
// requirement — so accepting the new key here is safe.
func (n *Node) handleKeyAnnounce(env map[string]any) map[string]any {
	p, ok := payloadOf(env)
	if !ok {
		return n.errorReply(env, "malformed", "missing payload")
	}
	fromBase := envStr(env, "from")
	localID := pStr(p, "id")
	pubStr := pStr(p, "public_key")
	if localID == "" || pubStr == "" {
		return n.errorReply(env, "malformed", "missing key id or public_key")
	}
	pub, err := mustParseKey(pubStr)
	if err != nil {
		return n.errorReply(env, "malformed", "bad public_key: "+err.Error())
	}
	n.peerKeys[keyID(fromBase, localID)] = pub
	return nil // acknowledged by acceptance; no reply required
}
