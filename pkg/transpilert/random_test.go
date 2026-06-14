package transpilert

import (
	mrand "math/rand"
	"testing"
)

// TestRandomSeedMatchesStdlib proves a seeded sequence is identical to the
// reference math/rand the interpreter uses, so seeded scripts agree across
// backends.
func TestRandomSeedMatchesStdlib(t *testing.T) {
	RandomSeed(42)
	ref := mrand.New(mrand.NewSource(42))
	for i := 0; i < 5; i++ {
		if got, want := RandomNext(), ref.Int63(); got != want {
			t.Fatalf("next[%d] = %d, want %d", i, got, want)
		}
	}

	RandomSeed(7)
	ref = mrand.New(mrand.NewSource(7))
	for i := 0; i < 5; i++ {
		got := RandomIntRange(10, 20)
		want := int64(10) + ref.Int63n(10)
		if got != want {
			t.Fatalf("intRange[%d] = %d, want %d", i, got, want)
		}
	}
}

func TestRandomSeedReproducible(t *testing.T) {
	RandomSeed(99)
	a := []int64{RandomNext(), RandomNext(), RandomNext()}
	RandomSeed(99)
	b := []int64{RandomNext(), RandomNext(), RandomNext()}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("seed not reproducible at %d: %d vs %d", i, a[i], b[i])
		}
	}
}
