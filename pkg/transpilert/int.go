package transpilert

import "math/big"

// Int is a Geblang integer: an int64 fast path that promotes to *big.Int on
// overflow, mirroring the interpreter's SmallInt -> Int promotion. Big is nil
// while the value fits in int64.
type Int struct {
	I64 int64
	Big *big.Int
}

// FromInt64 wraps an int64 in the fast-path representation.
func FromInt64(v int64) Int { return Int{I64: v} }

// FromBig wraps a *big.Int, collapsing to the int64 fast path when it fits.
func FromBig(v *big.Int) Int {
	if v.IsInt64() {
		return Int{I64: v.Int64()}
	}
	return Int{Big: v}
}

// IsBig reports whether the value is held as a *big.Int (overflowed int64).
func (a Int) IsBig() bool { return a.Big != nil }

func (a Int) big() *big.Int {
	if a.Big != nil {
		return a.Big
	}
	return big.NewInt(a.I64)
}

// AddInt adds two ints, promoting to big.Int on int64 overflow.
func AddInt(a, b Int) Int {
	if a.Big == nil && b.Big == nil {
		r := a.I64 + b.I64
		if (a.I64^r)&(b.I64^r) >= 0 {
			return Int{I64: r}
		}
	}
	return FromBig(new(big.Int).Add(a.big(), b.big()))
}

// SubInt subtracts b from a, promoting to big.Int on int64 overflow.
func SubInt(a, b Int) Int {
	if a.Big == nil && b.Big == nil {
		r := a.I64 - b.I64
		if (a.I64^b.I64)&(a.I64^r) >= 0 {
			return Int{I64: r}
		}
	}
	return FromBig(new(big.Int).Sub(a.big(), b.big()))
}

// MulInt multiplies two ints, promoting to big.Int on int64 overflow.
func MulInt(a, b Int) Int {
	if a.Big == nil && b.Big == nil {
		r := a.I64 * b.I64
		if a.I64 == 0 || r/a.I64 == b.I64 {
			return Int{I64: r}
		}
	}
	return FromBig(new(big.Int).Mul(a.big(), b.big()))
}

// NegInt negates a, promoting math.MinInt64 to big.Int.
func NegInt(a Int) Int {
	if a.Big == nil && a.I64 != minInt64 {
		return Int{I64: -a.I64}
	}
	return FromBig(new(big.Int).Neg(a.big()))
}

const minInt64 = -1 << 63
