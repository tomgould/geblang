package transpilert

import "math"

// Typed adapters for the Geblang math module on the transpiler's unboxed
// representation. Where a Go math call is the documented equivalent it is
// used directly; floor/ceil/round/trunc yield int64 because the interpreter
// returns an int for float input.

func MathAbs(x float64) float64   { return math.Abs(x) }
func MathSqrt(x float64) float64  { return math.Sqrt(x) }
func MathCbrt(x float64) float64  { return math.Cbrt(x) }
func MathSin(x float64) float64   { return math.Sin(x) }
func MathCos(x float64) float64   { return math.Cos(x) }
func MathTan(x float64) float64   { return math.Tan(x) }
func MathAsin(x float64) float64  { return math.Asin(x) }
func MathAcos(x float64) float64  { return math.Acos(x) }
func MathAtan(x float64) float64  { return math.Atan(x) }
func MathExp(x float64) float64   { return math.Exp(x) }
func MathLog(x float64) float64   { return math.Log(x) }
func MathLog2(x float64) float64  { return math.Log2(x) }
func MathLog10(x float64) float64 { return math.Log10(x) }

func MathAtan2(y, x float64) float64    { return math.Atan2(y, x) }
func MathPow(base, exp float64) float64 { return math.Pow(base, exp) }
func MathHypot(a, b float64) float64    { return math.Hypot(a, b) }

// MathFloor / Ceil / Round / Trunc match the interpreter, returning an int64.
func MathFloor(x float64) int64 { return int64(math.Floor(x)) }
func MathCeil(x float64) int64  { return int64(math.Ceil(x)) }
func MathRound(x float64) int64 { return int64(math.Round(x)) }
func MathTrunc(x float64) int64 { return int64(math.Trunc(x)) }

// MathSign returns -1, 0, or 1.
func MathSign(x float64) int64 {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	default:
		return 0
	}
}

// MathClamp constrains x to [lo, hi].
func MathClamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// MathLerp linearly interpolates between a and b by t.
func MathLerp(a, b, t float64) float64 { return a + (b-a)*t }

// MathRemap maps x from [inLow, inHigh] onto [outLow, outHigh].
func MathRemap(x, inLow, inHigh, outLow, outHigh float64) float64 {
	return outLow + (x-inLow)*(outHigh-outLow)/(inHigh-inLow)
}

func MathIsNaN(x float64) bool { return math.IsNaN(x) }
func MathIsInf(x float64) bool { return math.IsInf(x, 0) }

// Constants (the interpreter exposes these as zero-arg functions).
func MathPi() float64       { return math.Pi }
func MathE() float64        { return math.E }
func MathTau() float64      { return 2 * math.Pi }
func MathPhi() float64      { return math.Phi }
func MathEpsilon() float64  { return 2.220446049250313e-16 }
func MathInf() float64      { return math.Inf(1) }
func MathNaN() float64      { return math.NaN() }
func MathMaxInt() int64     { return math.MaxInt64 }
func MathMinInt() int64     { return math.MinInt64 }
func MathMaxFloat() float64 { return math.MaxFloat64 }
func MathMinFloat() float64 { return math.SmallestNonzeroFloat64 }
