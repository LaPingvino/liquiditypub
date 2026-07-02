// Package lpnode is the Profile-A reference node for LiquidityPub v0.2
// (PROTOCOL.md, DESIGN.md). It is a single Go binary that serves the contact
// surface — identity document, signed envelopes, contacts, transfers,
// checkpoints — over HTTP, backed by the append-only ledger in ./ledger. Every
// piece of arithmetic the protocol pins (pool pricing, channel hashes, UD,
// canonical JSON, signatures) is delegated to the conformance package, so this
// node is a thin, auditable shell around the spec's own reference core.
package lpnode

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// MemberConfig seeds a member account at genesis.
type MemberConfig struct {
	// Name is the local part (name@host); matched [a-z0-9_]{1,32} (PROTOCOL §11).
	Name string
	// Grant is an initial balance in micro-units, credited from `issuance` at
	// genesis so the member can transact in a PoC. Real deployments fund members
	// through the UD scheduler; this is a convenience seed.
	Grant int64
	// DisplayName is optional and only revealed if the node policy allows it.
	DisplayName string
}

// Config fully describes a node. Everything here is node-internal policy except
// the fields that surface in the identity document (§3).
type Config struct {
	Base        string // https origin, e.g. https://riverside.example
	Name        string
	Description string

	CurrencyName   string
	CurrencySymbol string

	// Issuance (§3, §10). CPeriodPpm is per-period supply growth in ppm;
	// UDPeriod an ISO-8601 duration string; GenesisUD an initial standard-weight
	// dividend used as a floor while money_supply is small (node policy, §10 —
	// "nodes publish whatever they actually run").
	CPeriodPpm int64
	UDPeriod   string
	GenesisUD  int64

	Transparency string // "public" | "pseudonymous" | "peers"

	// AutoAcceptSeed is the responder seed (our currency) applied when we accept
	// an inbound contact.propose. A PoC convenience: real nodes decide per-peer.
	AutoAcceptSeed int64

	Members []MemberConfig

	// PrivKey is the node signing key. If nil, NewNode generates one.
	PrivKey ed25519.PrivateKey
}

// host extracts the bare host (used for node:<host> wallets and outbox names).
func host(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		// Tolerate bare host:port strings used in tests.
		return strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	}
	return u.Host
}

// identityPath is the well-known identity document path (§3).
const identityPath = "/.well-known/liquiditypub"

// keyID is the fully-qualified key identifier used in envelope sig.key and
// resolved during verification: <base><identityPath>#<localID> (§4).
func keyID(base, localID string) string {
	return base + identityPath + localID
}

// pubB64 encodes a raw ed25519 public key as base64url without padding (§2).
func pubB64(pub ed25519.PublicKey) string {
	return base64.RawURLEncoding.EncodeToString(pub)
}

// b64 encodes arbitrary bytes as base64url without padding (§2).
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// IdentityDoc renders the node identity document (§3) as a JSON-ready map.
func (n *Node) IdentityDoc() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	return map[string]any{
		"liquiditypub": "0.2",
		"node": map[string]any{
			"name":        n.cfg.Name,
			"description": n.cfg.Description,
			"base":        n.cfg.Base,
		},
		"currency": map[string]any{
			"name":        n.cfg.CurrencyName,
			"symbol":      n.cfg.CurrencySymbol,
			"micro_units": int64(1_000_000),
		},
		"issuance": map[string]any{
			"type":         "ud",
			"c_period_ppm": n.cfg.CPeriodPpm,
			"ud_period":    n.cfg.UDPeriod,
			"current_ud":   n.currentUD,
		},
		"keys": n.keyDocs(),
		"endpoints": map[string]any{
			"inbox":      "/lp/inbox",
			"outbox":     "/lp/outbox/{peer-host}.json",
			"checkpoint": "/lp/checkpoint.json",
			"log":        "/lp/log/",
		},
		"capabilities": []any{"pull", "push"},
		"transparency": n.cfg.Transparency,
		"peers":        n.peerList(),
		"stats": map[string]any{
			"active_members":  int64(len(n.members)),
			"ud_weight_total": n.udWeightTotal(),
			"money_supply":    n.led.MoneySupply(),
		},
	}
}

// peerList returns the revealed contact peers (§3), sorted for determinism.
func (n *Node) peerList() []any {
	out := make([]any, 0, len(n.contactByHost))
	for _, c := range n.contactByHost {
		if c.Active && !c.Closed {
			out = append(out, c.PeerBase)
		}
	}
	return out
}

func mustParseKey(pub string) (ed25519.PublicKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(pub)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
