package runtime

import (
	"math"
	"testing"
)

// TestSmallIntTypeProperties verifies that SmallInt satisfies the Value interface
// and has the expected type identity.
func TestSmallIntTypeProperties(t *testing.T) {
	v := SmallInt{Value: 42}
	var _ Value = v

	if got := v.TypeName(); got != "int" {
		t.Errorf("TypeName() = %q, want %q", got, "int")
	}
	if got := v.Inspect(); got != "42" {
		t.Errorf("Inspect() = %q, want %q", got, "42")
	}

	neg := SmallInt{Value: -7}
	if got := neg.Inspect(); got != "-7" {
		t.Errorf("Inspect() on negative = %q, want %q", got, "-7")
	}
}

// TestSmallIntAndIntShareTypeName verifies that SmallInt and Int both report
// "int" as their type name — this is the behaviour that type-checked collections
// and generic functions rely on.
func TestSmallIntAndIntShareTypeName(t *testing.T) {
	small := SmallInt{Value: 1}
	big := NewInt64(1)

	if small.TypeName() != big.TypeName() {
		t.Errorf("SmallInt.TypeName() = %q, Int.TypeName() = %q; must be equal",
			small.TypeName(), big.TypeName())
	}
}

// TestSmallIntValuesEqualCrossType verifies the equality comparison between
// SmallInt and Int values with the same numeric content.
func TestSmallIntValuesEqualCrossType(t *testing.T) {
	cases := []struct {
		left, right Value
		want        bool
	}{
		{SmallInt{4}, SmallInt{4}, true},
		{SmallInt{4}, SmallInt{5}, false},
		{SmallInt{100}, NewInt64(100), true},
		{NewInt64(100), SmallInt{100}, true},
		{SmallInt{0}, NewInt64(1), false},
		{SmallInt{-1}, SmallInt{-1}, true},
	}

	for _, tc := range cases {
		got := ValuesEqual(tc.left, tc.right)
		if got != tc.want {
			t.Errorf("ValuesEqual(%v, %v) = %v, want %v",
				tc.left.Inspect(), tc.right.Inspect(), got, tc.want)
		}
	}
}

// TestSmallIntIsFrozen verifies that SmallInt is considered always-frozen
// (it is a value type with no mutable backing storage).
func TestSmallIntIsFrozen(t *testing.T) {
	if !IsFrozen(SmallInt{Value: 42}) {
		t.Error("IsFrozen(SmallInt) = false, want true")
	}
}

// TestSmallIntBoundaryValues checks that the maximum and minimum int64 values
// are handled correctly by SmallInt.
func TestSmallIntBoundaryValues(t *testing.T) {
	maxVal := SmallInt{Value: math.MaxInt64}
	minVal := SmallInt{Value: math.MinInt64}

	if got := maxVal.Inspect(); got != "9223372036854775807" {
		t.Errorf("MaxInt64.Inspect() = %q", got)
	}
	if got := minVal.Inspect(); got != "-9223372036854775808" {
		t.Errorf("MinInt64.Inspect() = %q", got)
	}
}
