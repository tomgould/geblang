package native

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"geblang/internal/runtime"
)

// IntBaseArg validates an optional base argument for int<->string base
// conversion. Returns 10 when args is empty.
func IntBaseArg(args []runtime.Value, label string) (int, error) {
	if len(args) == 0 {
		return 10, nil
	}
	if len(args) != 1 {
		return 0, fmt.Errorf("%s expects optional base", label)
	}
	var base int64
	switch v := args[0].(type) {
	case runtime.SmallInt:
		base = v.Value
	case runtime.Int:
		if !v.Value.IsInt64() {
			return 0, fmt.Errorf("%s base must be between 2 and 36", label)
		}
		base = v.Value.Int64()
	default:
		return 0, fmt.Errorf("%s base must be an int", label)
	}
	if base < 2 || base > 36 {
		return 0, fmt.Errorf("%s base must be between 2 and 36", label)
	}
	return int(base), nil
}

// IntFormatBase formats a runtime int (SmallInt or Int) in the given base.
// Lowercase digits a-z for bases > 10. Caller should validate base via IntBaseArg.
func IntFormatBase(value runtime.Value, base int) (string, error) {
	switch v := value.(type) {
	case runtime.SmallInt:
		return strconv.FormatInt(v.Value, base), nil
	case runtime.Int:
		return v.Value.Text(base), nil
	}
	return "", fmt.Errorf("%s has no toString(base)", value.TypeName())
}

// StringParseBase parses a string into a runtime int using the given base.
// Returns SmallInt when the result fits in int64, otherwise Int. Caller
// should validate base via IntBaseArg.
func StringParseBase(text string, base int, label string) (runtime.Value, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("%s: empty string", label)
	}
	if n, err := strconv.ParseInt(trimmed, base, 64); err == nil {
		return runtime.SmallInt{Value: n}, nil
	}
	bi, ok := new(big.Int).SetString(trimmed, base)
	if !ok {
		return nil, fmt.Errorf("%s: invalid digit in %q for base %d", label, text, base)
	}
	if bi.IsInt64() {
		return runtime.SmallInt{Value: bi.Int64()}, nil
	}
	return runtime.Int{Value: bi}, nil
}

// IntToDecimal converts a runtime.Int to runtime.Decimal for mixed-type arithmetic.
func IntToDecimal(value runtime.Int) runtime.Decimal {
	return runtime.Decimal{Value: new(big.Rat).SetInt(value.Value)}
}

// SmallIntToDecimal converts a runtime.SmallInt to runtime.Decimal.
func SmallIntToDecimal(value runtime.SmallInt) runtime.Decimal {
	return runtime.Decimal{Value: new(big.Rat).SetInt64(value.Value)}
}

// IntValueToBigInt promotes Int or SmallInt to *big.Int.
func IntValueToBigInt(v runtime.Value) (*big.Int, bool) {
	switch v := v.(type) {
	case runtime.SmallInt:
		return big.NewInt(v.Value), true
	case runtime.Int:
		return v.Value, true
	}
	return nil, false
}

// AsInt64 extracts an int64 from a SmallInt or Int value.
// Returns (0, false) if the value is not an int type or does not fit in int64.
func AsInt64(v runtime.Value) (int64, bool) {
	switch v := v.(type) {
	case runtime.SmallInt:
		return v.Value, true
	case runtime.Int:
		if v.Value.IsInt64() {
			return v.Value.Int64(), true
		}
		return 0, false
	}
	return 0, false
}

// IsInt returns true if v is either SmallInt or Int.
func IsInt(v runtime.Value) bool {
	_, ok1 := v.(runtime.SmallInt)
	_, ok2 := v.(runtime.Int)
	return ok1 || ok2
}

// NumericCompare compares two ordered values, returning -1, 0, or 1.
// Despite the name, it also supports lexicographic comparison
// between two runtime.String values (the evaluator's compareValues
// has the same coverage; this is the VM-side path - they should
// stay in sync).
// SortLess interprets a sort callback's return value. A Bool is a less-than
// predicate (true => a sorts before b); an Int is a three-way comparator
// (negative => a sorts before b). Both styles are accepted so callbacks like
// string.compare (-1/0/1) and a plain a<b predicate both work.
func SortLess(result runtime.Value) (bool, error) {
	switch v := result.(type) {
	case runtime.Bool:
		return v.Value, nil
	case runtime.SmallInt:
		return v.Value < 0, nil
	case runtime.Int:
		return v.Value.Sign() < 0, nil
	default:
		return false, fmt.Errorf("sort callback must return bool (less-than) or int (three-way), got %s", result.TypeName())
	}
}

func NumericCompare(left runtime.Value, right runtime.Value) (int, error) {
	// Fast path: both SmallInt
	if l, ok := left.(runtime.SmallInt); ok {
		if r, ok := right.(runtime.SmallInt); ok {
			if l.Value < r.Value {
				return -1, nil
			}
			if l.Value > r.Value {
				return 1, nil
			}
			return 0, nil
		}
		lb := big.NewInt(l.Value)
		if r, ok := right.(runtime.Int); ok {
			return lb.Cmp(r.Value), nil
		}
		if r, ok := right.(runtime.Decimal); ok {
			return new(big.Rat).SetInt(lb).Cmp(r.Value), nil
		}
		return 0, fmt.Errorf("comparison expects compatible numeric operands")
	}
	switch l := left.(type) {
	case runtime.Int:
		switch r := right.(type) {
		case runtime.SmallInt:
			return l.Value.Cmp(big.NewInt(r.Value)), nil
		case runtime.Int:
			return l.Value.Cmp(r.Value), nil
		case runtime.Decimal:
			return IntToDecimal(l).Value.Cmp(r.Value), nil
		}
	case runtime.Decimal:
		if r, ok := right.(runtime.SmallInt); ok {
			return l.Value.Cmp(new(big.Rat).SetInt64(r.Value)), nil
		}
		switch r := right.(type) {
		case runtime.Int:
			return l.Value.Cmp(IntToDecimal(r).Value), nil
		case runtime.Decimal:
			return l.Value.Cmp(r.Value), nil
		}
	case runtime.Float:
		if r, ok := right.(runtime.Float); ok {
			if l.Value < r.Value {
				return -1, nil
			}
			if l.Value > r.Value {
				return 1, nil
			}
			return 0, nil
		}
	case runtime.String:
		if r, ok := right.(runtime.String); ok {
			if l.Value < r.Value {
				return -1, nil
			}
			if l.Value > r.Value {
				return 1, nil
			}
			return 0, nil
		}
	}
	return 0, fmt.Errorf("comparison expects compatible numeric operands")
}

// NumericAbs returns the absolute value of a numeric runtime.Value.
func NumericAbs(value runtime.Value) (runtime.Value, error) {
	switch value := value.(type) {
	case runtime.SmallInt:
		if value.Value >= 0 {
			return value, nil
		}
		if value.Value == math.MinInt64 {
			// overflow: promote to big.Int
			return runtime.Int{Value: new(big.Int).Abs(big.NewInt(value.Value))}, nil
		}
		return runtime.SmallInt{Value: -value.Value}, nil
	case runtime.Int:
		return runtime.Int{Value: new(big.Int).Abs(value.Value)}, nil
	case runtime.Decimal:
		return runtime.Decimal{Value: new(big.Rat).Abs(value.Value)}, nil
	case runtime.Float:
		return runtime.Float{Value: math.Abs(value.Value)}, nil
	default:
		return nil, fmt.Errorf("%s has no method abs", value.TypeName())
	}
}

// FloatLike converts a numeric runtime.Value to float64.
func FloatLike(value runtime.Value) (float64, error) {
	switch value := value.(type) {
	case runtime.SmallInt:
		return float64(value.Value), nil
	case runtime.Int:
		result, _ := new(big.Rat).SetInt(value.Value).Float64()
		return result, nil
	case runtime.Decimal:
		result, _ := value.Value.Float64()
		return result, nil
	case runtime.Float:
		return value.Value, nil
	default:
		return 0, fmt.Errorf("expected numeric value, got %s", value.TypeName())
	}
}

// NumericBest returns the best value from a non-empty slice using the given comparator.
func NumericBest(values []runtime.Value, better func(int) bool) (runtime.Value, error) {
	best := values[0]
	for _, value := range values[1:] {
		cmp, err := NumericCompare(value, best)
		if err != nil {
			return nil, err
		}
		if better(cmp) {
			best = value
		}
	}
	return best, nil
}

// IntUnaryMath applies fn to a numeric arg, returning Int unchanged for integer inputs.
func IntUnaryMath(args []runtime.Value, fn func(float64) float64, label string) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", label)
	}
	if value, ok := args[0].(runtime.SmallInt); ok {
		return value, nil
	}
	if value, ok := args[0].(runtime.Int); ok {
		return runtime.Int{Value: new(big.Int).Set(value.Value)}, nil
	}
	result, err := FloatUnaryMath(args, fn, label)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(result.(runtime.Float).Value)), nil
}

// FloatUnaryMath applies fn to a single float-like argument.
func FloatUnaryMath(args []runtime.Value, fn func(float64) float64, label string) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", label)
	}
	value, err := FloatLike(args[0])
	if err != nil {
		return nil, err
	}
	return runtime.Float{Value: fn(value)}, nil
}

// RoundMode selects the rounding direction for the value-keeping numeric
// rounding helpers (DecimalQuantize / FloatRound).
type RoundMode int

const (
	RoundHalfAwayZero RoundMode = iota // 2.5 -> 3, -2.5 -> -3
	RoundFloor                         // toward -inf
	RoundCeil                          // toward +inf
	RoundTrunc                         // toward zero
)

// roundBigRatToInt rounds num/den (den > 0) to an integer per mode.
func roundBigRatToInt(num, den *big.Int, mode RoundMode) *big.Int {
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(num, den, r) // q truncates toward zero; r carries num's sign
	if r.Sign() == 0 {
		return q
	}
	switch mode {
	case RoundFloor:
		if num.Sign() < 0 {
			return q.Sub(q, big.NewInt(1))
		}
	case RoundCeil:
		if num.Sign() > 0 {
			return q.Add(q, big.NewInt(1))
		}
	case RoundHalfAwayZero:
		twiceR := new(big.Int).Lsh(new(big.Int).Abs(r), 1)
		if twiceR.Cmp(den) >= 0 {
			if num.Sign() < 0 {
				return q.Sub(q, big.NewInt(1))
			}
			return q.Add(q, big.NewInt(1))
		}
	}
	return q
}

// DecimalQuantize rounds an exact decimal to places fractional digits.
func DecimalQuantize(d runtime.Decimal, places int, mode RoundMode) runtime.Decimal {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	num := new(big.Int).Mul(d.Value.Num(), scale)
	q := roundBigRatToInt(num, d.Value.Denom(), mode)
	return runtime.Decimal{Value: new(big.Rat).SetFrac(q, scale)}
}

// FloatRound rounds a float to places fractional digits. NaN/Inf pass through.
func FloatRound(f float64, places int, mode RoundMode) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return f
	}
	scale := math.Pow(10, float64(places))
	scaled := f * scale
	var rounded float64
	switch mode {
	case RoundFloor:
		rounded = math.Floor(scaled)
	case RoundCeil:
		rounded = math.Ceil(scaled)
	case RoundTrunc:
		rounded = math.Trunc(scaled)
	default:
		rounded = math.Round(scaled)
	}
	return rounded / scale
}

func RoundPlacesArg(args []runtime.Value, label string) (int, error) {
	if len(args) == 0 {
		return 0, nil
	}
	if len(args) > 1 {
		return 0, fmt.Errorf("%s expects optional places", label)
	}
	n, ok := AsInt64(args[0])
	if !ok {
		return 0, fmt.Errorf("%s places must be int", label)
	}
	if n < 0 || n > 10000 {
		return 0, fmt.Errorf("%s places must be between 0 and 10000", label)
	}
	return int(n), nil
}

// NumericRoundMethod backs the value-keeping round/floor/ceil/truncate
// methods on decimal and float (shared by both backends for parity).
func NumericRoundMethod(receiver runtime.Value, args []runtime.Value, mode RoundMode, label string) (runtime.Value, error) {
	places, err := RoundPlacesArg(args, label)
	if err != nil {
		return nil, err
	}
	switch v := receiver.(type) {
	case runtime.Decimal:
		return DecimalQuantize(v, places, mode), nil
	case runtime.Float:
		return runtime.Float{Value: FloatRound(v.Value, places, mode)}, nil
	}
	return nil, fmt.Errorf("%s has no method %s", receiver.TypeName(), label)
}

// NumericSign returns -1, 0, or 1 as a SmallInt.
func NumericSign(v runtime.Value) (runtime.Value, error) {
	var s int
	switch v := v.(type) {
	case runtime.SmallInt:
		switch {
		case v.Value > 0:
			s = 1
		case v.Value < 0:
			s = -1
		}
	case runtime.Int:
		s = v.Value.Sign()
	case runtime.Decimal:
		s = v.Value.Sign()
	case runtime.Float:
		switch {
		case v.Value > 0:
			s = 1
		case v.Value < 0:
			s = -1
		}
	default:
		return nil, fmt.Errorf("%s has no method sign", v.TypeName())
	}
	return runtime.SmallInt{Value: int64(s)}, nil
}

// NumericClamp constrains v to [lo, hi], returning the result in v's type.
// Bounds promote to v's type the same way arithmetic does (int into
// decimal/float); float and decimal do not mix.
func NumericClamp(v, lo, hi runtime.Value) (runtime.Value, error) {
	loC, err := coerceToReceiver(v, lo)
	if err != nil {
		return nil, err
	}
	hiC, err := coerceToReceiver(v, hi)
	if err != nil {
		return nil, err
	}
	if order, err := NumericCompare(loC, hiC); err != nil {
		return nil, err
	} else if order > 0 {
		return nil, fmt.Errorf("clamp expects lo <= hi")
	}
	if below, err := NumericCompare(v, loC); err != nil {
		return nil, err
	} else if below < 0 {
		return loC, nil
	}
	if above, err := NumericCompare(v, hiC); err != nil {
		return nil, err
	} else if above > 0 {
		return hiC, nil
	}
	return v, nil
}

func coerceToReceiver(v, bound runtime.Value) (runtime.Value, error) {
	switch v.(type) {
	case runtime.Float:
		f, err := FloatLike(bound)
		if err != nil {
			return nil, fmt.Errorf("clamp bound not compatible with float")
		}
		return runtime.Float{Value: f}, nil
	case runtime.Decimal:
		switch b := bound.(type) {
		case runtime.SmallInt:
			return SmallIntToDecimal(b), nil
		case runtime.Int:
			return IntToDecimal(b), nil
		case runtime.Decimal:
			return b, nil
		}
		return nil, fmt.Errorf("clamp bound not compatible with decimal")
	case runtime.SmallInt, runtime.Int:
		switch bound.(type) {
		case runtime.SmallInt, runtime.Int:
			return bound, nil
		}
		return nil, fmt.Errorf("clamp bound not compatible with int")
	}
	return nil, fmt.Errorf("%s has no method clamp", v.TypeName())
}
