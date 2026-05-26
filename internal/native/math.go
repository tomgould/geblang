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
