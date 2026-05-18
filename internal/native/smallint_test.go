package native

import (
	"math/big"
	"testing"

	"geblang/internal/runtime"
)

// TestAsInt64HandlesSmallInt verifies AsInt64 extracts values from both
// SmallInt (direct) and Int (promoted) without error.
func TestAsInt64HandlesSmallInt(t *testing.T) {
	cases := []struct {
		name    string
		value   runtime.Value
		want    int64
		wantOK  bool
	}{
		{"SmallInt positive", runtime.SmallInt{Value: 42}, 42, true},
		{"SmallInt negative", runtime.SmallInt{Value: -7}, -7, true},
		{"SmallInt zero", runtime.SmallInt{Value: 0}, 0, true},
		{"Int fits int64", runtime.NewInt64(100), 100, true},
		{"Int too large", runtime.Int{Value: new(big.Int).Lsh(big.NewInt(1), 65)}, 0, false},
		{"String is not int", runtime.String{Value: "42"}, 0, false},
		{"Float is not int", runtime.Float{Value: 1.5}, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := AsInt64(tc.value)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("value = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestIntValueToBigIntHandlesSmallInt verifies IntValueToBigInt promotes
// SmallInt to *big.Int correctly.
func TestIntValueToBigIntHandlesSmallInt(t *testing.T) {
	cases := []struct {
		name   string
		value  runtime.Value
		wantOK bool
		want   int64
	}{
		{"SmallInt 5", runtime.SmallInt{Value: 5}, true, 5},
		{"SmallInt -3", runtime.SmallInt{Value: -3}, true, -3},
		{"Int 99", runtime.NewInt64(99), true, 99},
		{"String", runtime.String{Value: "1"}, false, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := IntValueToBigInt(tc.value)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.Int64() != tc.want {
				t.Errorf("value = %d, want %d", got.Int64(), tc.want)
			}
		})
	}
}

// TestIsIntHandlesSmallInt verifies IsInt returns true for both SmallInt and Int.
func TestIsIntHandlesSmallInt(t *testing.T) {
	if !IsInt(runtime.SmallInt{Value: 1}) {
		t.Error("IsInt(SmallInt) = false, want true")
	}
	if !IsInt(runtime.NewInt64(1)) {
		t.Error("IsInt(Int) = false, want true")
	}
	if IsInt(runtime.Float{Value: 1.0}) {
		t.Error("IsInt(Float) = true, want false")
	}
	if IsInt(runtime.String{Value: "1"}) {
		t.Error("IsInt(String) = true, want false")
	}
}

// TestNumericCompareSmallInt verifies NumericCompare handles SmallInt operands.
func TestNumericCompareSmallInt(t *testing.T) {
	cases := []struct {
		name        string
		left, right runtime.Value
		want        int
	}{
		{"SmallInt eq", runtime.SmallInt{Value: 4}, runtime.SmallInt{Value: 4}, 0},
		{"SmallInt lt", runtime.SmallInt{Value: 3}, runtime.SmallInt{Value: 4}, -1},
		{"SmallInt gt", runtime.SmallInt{Value: 5}, runtime.SmallInt{Value: 4}, 1},
		{"SmallInt vs Int eq", runtime.SmallInt{Value: 7}, runtime.NewInt64(7), 0},
		{"Int vs SmallInt eq", runtime.NewInt64(7), runtime.SmallInt{Value: 7}, 0},
		{"SmallInt vs Int lt", runtime.SmallInt{Value: 3}, runtime.NewInt64(10), -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NumericCompare(tc.left, tc.right)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSmallIntToDecimalConverts verifies SmallIntToDecimal produces a Decimal
// with the exact rational value.
func TestSmallIntToDecimalConverts(t *testing.T) {
	d := SmallIntToDecimal(runtime.SmallInt{Value: 7})
	if d.Value.RatString() != "7" {
		t.Errorf("SmallIntToDecimal(7).Value = %q, want %q", d.Value.RatString(), "7")
	}
}
