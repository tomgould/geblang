package types

type IntMode int

const (
	// IntModeFast is the default: int lowers to Go int64. Fast and idiomatic;
	// the one divergence from the interpreter is that it wraps past ~9.2e18
	// instead of promoting to bignum (unreachable for typical Tier-1 programs).
	IntModeFast IntMode = iota
	// IntModeBigInt is the opt-in safe mode: int lowers to transpilert.Int,
	// which promotes to *big.Int on overflow for full interpreter parity.
	IntModeBigInt
)

func (m IntMode) String() string {
	switch m {
	case IntModeFast:
		return "fast"
	case IntModeBigInt:
		return "bigint"
	default:
		return "unknown"
	}
}
