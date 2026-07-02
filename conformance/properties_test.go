package conformance

import (
	"errors"
	"math/big"
	"math/rand"
	"testing"
)

// Pool pricing must produce the LARGEST dst that still preserves the constant
// product: (rSrc+src)(rDst−dst) ≥ k and (rSrc+src)(rDst−dst−1) < k.
// This is the "rounds in the pool's favor, but no further" property.
func TestPoolPriceTightness(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		rSrc := 1 + rng.Int63n(1e13)
		rDst := 1 + rng.Int63n(1e13)
		src := 1 + rng.Int63n(1e12)
		dst, err := PoolPrice(rSrc, rDst, src)
		if errors.Is(err, ErrDust) {
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		k := new(big.Int).Mul(big.NewInt(rSrc), big.NewInt(rDst))
		after := new(big.Int).Mul(big.NewInt(rSrc+src), big.NewInt(rDst-dst))
		if after.Cmp(k) < 0 {
			t.Fatalf("k violated: rs=%d rd=%d src=%d dst=%d", rSrc, rDst, src, dst)
		}
		tighter := new(big.Int).Mul(big.NewInt(rSrc+src), big.NewInt(rDst-dst-1))
		if tighter.Cmp(k) >= 0 {
			t.Fatalf("dst not maximal: rs=%d rd=%d src=%d dst=%d", rSrc, rDst, src, dst)
		}
	}
}

// Same committed operations in the same order ⇒ same root; any reordering of
// distinct operations ⇒ different root.
func TestChannelRootDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 200; i++ {
		a := ChannelRoot0("urn:uuid:test")
		b := ChannelRoot0("urn:uuid:test")
		var ops [][2]int64
		for j := 0; j < 5; j++ {
			ops = append(ops, [2]int64{1 + rng.Int63n(1e9), 1 + rng.Int63n(1e9)})
		}
		for j, op := range ops {
			var err error
			a, err = ChannelNext(a, "transfer", string(rune('a'+j)), op[0], op[1])
			if err != nil {
				t.Fatal(err)
			}
			b, err = ChannelNext(b, "transfer", string(rune('a'+j)), op[0], op[1])
			if err != nil {
				t.Fatal(err)
			}
		}
		if a != b {
			t.Fatal("identical histories produced different roots")
		}
		// swap two ops → root must change
		c := ChannelRoot0("urn:uuid:test")
		for j := len(ops) - 1; j >= 0; j-- {
			var err error
			c, err = ChannelNext(c, "transfer", string(rune('a'+j)), ops[j][0], ops[j][1])
			if err != nil {
				t.Fatal(err)
			}
		}
		if c == a {
			t.Fatal("reordered history produced identical root")
		}
	}
}

// A standard-weight recipient receives exactly ud_base; weights scale with
// floor, never exceeding the proportional share.
func TestUDWeightScaling(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 2000; i++ {
		supply := rng.Int63n(1e15)
		c := rng.Int63n(10000)
		w := 1 + rng.Int63n(1e9)
		base, err := UDBase(supply, c, w)
		if err != nil {
			t.Fatal(err)
		}
		if got := RecipientUD(base, 1_000_000); got != base {
			t.Fatalf("standard weight must receive ud_base: got %d want %d", got, base)
		}
		weight := rng.Int63n(1e7)
		got := RecipientUD(base, weight)
		exact := new(big.Int).Mul(big.NewInt(base), big.NewInt(weight))
		exact.Quo(exact, big.NewInt(1_000_000))
		if got != exact.Int64() {
			t.Fatalf("recipient UD mismatch: got %d want %d", got, exact.Int64())
		}
	}
}
