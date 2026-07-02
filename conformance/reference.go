// Package conformance contains the executable reference for the pure-function
// core of the LiquidityPub v0.2 protocol (docs/PROTOCOL.md), plus the
// language-agnostic test vectors in vectors/ that any implementation — Go,
// PHP, Workers, or otherwise — must reproduce.
//
// Everything in this file is normative: where prose in PROTOCOL.md and this
// code disagree, that is a spec bug to be raised, not silently resolved.
package conformance

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// ── Canonical JSON (JCS profile) ─────────────────────────────────────────────
//
// Protocol JSON contains only integers, strings, booleans, null, arrays and
// objects (PROTOCOL §2), so RFC 8785 reduces to: UTF-8, no whitespace, object
// keys sorted, integers in plain decimal, minimal string escaping.

// Canonical serializes a decoded JSON value (from DecodeJSON or hand-built
// from map[string]any / []any / string / int64 / json.Number / bool / nil)
// into its canonical byte form.
func Canonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		return writeCanonicalString(buf, x)
	case int:
		fmt.Fprintf(buf, "%d", x)
	case int64:
		fmt.Fprintf(buf, "%d", x)
	case json.Number:
		s := x.String()
		if strings.ContainsAny(s, ".eE") {
			return fmt.Errorf("float in protocol JSON: %s (PROTOCOL §2 forbids floats)", s)
		}
		buf.WriteString(s)
	case float64:
		return errors.New("float64 in protocol JSON: decode with DecodeJSON (UseNumber) or use int64")
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys) // ASCII keys: byte order == UTF-16 code unit order
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("unsupported type in protocol JSON: %T", v)
	}
	return nil
}

func writeCanonicalString(buf *bytes.Buffer, s string) error {
	var sb bytes.Buffer
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	buf.Write(bytes.TrimRight(sb.Bytes(), "\n"))
	return nil
}

// DecodeJSON decodes protocol JSON preserving integers exactly (json.Number).
func DecodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// ── Envelope signing (PROTOCOL §4) ───────────────────────────────────────────

// SigningBytes returns the bytes an envelope's signature covers: the envelope
// with the "sig" member removed entirely, canonicalized.
func SigningBytes(env map[string]any) ([]byte, error) {
	stripped := make(map[string]any, len(env))
	for k, v := range env {
		if k != "sig" {
			stripped[k] = v
		}
	}
	return Canonical(stripped)
}

// SignEnvelope computes the ed25519 signature over SigningBytes.
func SignEnvelope(env map[string]any, priv ed25519.PrivateKey) ([]byte, error) {
	msg, err := SigningBytes(env)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(priv, msg), nil
}

// VerifyEnvelope checks sig over SigningBytes with the given public key.
func VerifyEnvelope(env map[string]any, sig []byte, pub ed25519.PublicKey) (bool, error) {
	msg, err := SigningBytes(env)
	if err != nil {
		return false, err
	}
	return ed25519.Verify(pub, msg, sig), nil
}

// ── Pool pricing (PROTOCOL §6.2) ─────────────────────────────────────────────

var (
	ErrNonPositive = errors.New("amount must be positive")
	ErrEmptyPool   = errors.New("empty-pool: both reserves must be positive")
	ErrDust        = errors.New("dust: computed dst_amount is 0")
)

// PoolPrice computes dst_amount = floor(rDst × src / (rSrc + src)) with exact
// intermediates. rSrc is the destination node's reserve held at the source;
// rDst is the source node's reserve held at the destination.
func PoolPrice(rSrc, rDst, src int64) (int64, error) {
	if src <= 0 {
		return 0, ErrNonPositive
	}
	if rSrc <= 0 || rDst <= 0 {
		return 0, ErrEmptyPool
	}
	num := new(big.Int).Mul(big.NewInt(rDst), big.NewInt(src))
	den := big.NewInt(rSrc + src)
	dst := num.Quo(num, den)
	if dst.Sign() == 0 {
		return 0, ErrDust
	}
	return dst.Int64(), nil
}

// ── Channel hash (PROTOCOL §8.2) ─────────────────────────────────────────────

// ChannelRoot0 is SHA-256 over the UTF-8 contact id.
func ChannelRoot0(contactID string) [32]byte {
	return sha256.Sum256([]byte(contactID))
}

// ChannelNext chains one committed operation:
// SHA-256( prev ‖ JCS([opType, opID, srcAmount, dstAmount]) ).
func ChannelNext(prev [32]byte, opType, opID string, srcAmount, dstAmount int64) ([32]byte, error) {
	c, err := Canonical([]any{opType, opID, srcAmount, dstAmount})
	if err != nil {
		return [32]byte{}, err
	}
	h := sha256.New()
	h.Write(prev[:])
	h.Write(c)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// ── Universal Dividend reference formula (PROTOCOL §10) ──────────────────────

// UDBase = floor(moneySupply × cPeriodPpm / udWeightTotal): the per-period
// dividend of a standard-weight (1,000,000 micro-weight) recipient.
func UDBase(moneySupply, cPeriodPpm, udWeightTotal int64) (int64, error) {
	if udWeightTotal <= 0 {
		return 0, errors.New("ud_weight_total must be positive")
	}
	if moneySupply < 0 || cPeriodPpm < 0 {
		return 0, errors.New("supply and c must be non-negative")
	}
	num := new(big.Int).Mul(big.NewInt(moneySupply), big.NewInt(cPeriodPpm))
	return num.Quo(num, big.NewInt(udWeightTotal)).Int64(), nil
}

// RecipientUD = floor(udBase × weight / 1,000,000).
func RecipientUD(udBase, weight int64) int64 {
	num := new(big.Int).Mul(big.NewInt(udBase), big.NewInt(weight))
	return num.Quo(num, big.NewInt(1_000_000)).Int64()
}
