package transpilert

import (
	"math"
	"math/big"
	"testing"
)

func TestAddNoOverflow(t *testing.T) {
	r := AddInt(FromInt64(2), FromInt64(3))
	if r.IsBig() || r.I64 != 5 {
		t.Fatalf("2+3 = %+v, want fast-path 5", r)
	}
}

func TestAddOverflowPromotes(t *testing.T) {
	r := AddInt(FromInt64(math.MaxInt64), FromInt64(1))
	if !r.IsBig() {
		t.Fatal("MaxInt64+1 did not promote to big")
	}
	want := new(big.Int).Add(big.NewInt(math.MaxInt64), big.NewInt(1))
	if r.Big.Cmp(want) != 0 {
		t.Fatalf("got %s want %s", r.Big, want)
	}
}

func TestSubOverflowPromotes(t *testing.T) {
	r := SubInt(FromInt64(math.MinInt64), FromInt64(1))
	if !r.IsBig() {
		t.Fatal("MinInt64-1 did not promote to big")
	}
	want := new(big.Int).Sub(big.NewInt(math.MinInt64), big.NewInt(1))
	if r.Big.Cmp(want) != 0 {
		t.Fatalf("got %s want %s", r.Big, want)
	}
}

func TestMulOverflowPromotes(t *testing.T) {
	// 2^62 * 4 overflows int64; mirrors TestParitySmallIntOverflow.
	r := MulInt(FromInt64(1<<62), FromInt64(4))
	if !r.IsBig() {
		t.Fatal("2^62 * 4 did not promote to big")
	}
	want := new(big.Int).Mul(big.NewInt(1<<62), big.NewInt(4))
	if r.Big.Cmp(want) != 0 {
		t.Fatalf("got %s want %s", r.Big, want)
	}
}

func TestMulByZeroFastPath(t *testing.T) {
	r := MulInt(FromInt64(0), FromInt64(math.MaxInt64))
	if r.IsBig() || r.I64 != 0 {
		t.Fatalf("0 * MaxInt64 = %+v, want fast-path 0", r)
	}
}

func TestNegMinInt64Promotes(t *testing.T) {
	r := NegInt(FromInt64(math.MinInt64))
	if !r.IsBig() {
		t.Fatal("-MinInt64 did not promote to big")
	}
	want := new(big.Int).Neg(big.NewInt(math.MinInt64))
	if r.Big.Cmp(want) != 0 {
		t.Fatalf("got %s want %s", r.Big, want)
	}
}

func TestBigCollapsesBackToFastPath(t *testing.T) {
	// (MaxInt64 + 1) - 1 should collapse back into the int64 fast path.
	promoted := AddInt(FromInt64(math.MaxInt64), FromInt64(1))
	r := SubInt(promoted, FromInt64(1))
	if r.IsBig() || r.I64 != math.MaxInt64 {
		t.Fatalf("collapse failed: %+v", r)
	}
}

func TestFromBigCollapses(t *testing.T) {
	r := FromBig(big.NewInt(7))
	if r.IsBig() || r.I64 != 7 {
		t.Fatalf("FromBig(7) = %+v, want fast-path 7", r)
	}
}
