package transpilert

import (
	"math/big"
	"testing"
)

func TestStringToInt(t *testing.T) {
	cases := map[string]int64{
		"42":     42,
		"-7":     -7,
		"0x1F":   31,
		"0b101":  5,
		"0o17":   15,
		"1_000":  1000,
	}
	for in, want := range cases {
		if got := StringToInt(in); got != want {
			t.Errorf("StringToInt(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestStringToIntBadPanics(t *testing.T) {
	defer func() {
		r := recover()
		e, ok := r.(*Error)
		if !ok {
			t.Fatalf("expected *Error panic, got %T (%v)", r, r)
		}
		if e.Class != "RuntimeError" || e.Message != `invalid integer literal "abc"` {
			t.Fatalf("wrong error: class=%q msg=%q", e.Class, e.Message)
		}
	}()
	StringToInt("abc")
}

func TestStringToFloat(t *testing.T) {
	if got := StringToFloat("3.5"); got != 3.5 {
		t.Errorf("StringToFloat = %v", got)
	}
}

func TestStringToFloatBadPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		} else if _, ok := r.(*Error); !ok {
			t.Fatalf("expected *Error, got %T", r)
		}
	}()
	StringToFloat("xyz")
}

func TestStringToDecimal(t *testing.T) {
	d := StringToDecimal("12.5")
	if d.Cmp(big.NewRat(25, 2)) != 0 {
		t.Errorf("StringToDecimal = %v", d)
	}
}

func TestStringToBool(t *testing.T) {
	if !StringToBool("true") || StringToBool("false") {
		t.Fatal("bool conversion wrong")
	}
}

func TestStringToBoolBadPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	StringToBool("yes")
}

func TestToStringForms(t *testing.T) {
	if IntToString(100) != "100" {
		t.Error("IntToString")
	}
	if FloatToString(2.5) != "2.5" {
		t.Error("FloatToString")
	}
	if DecimalToString(big.NewRat(25, 2)) != "12.5000000000" {
		t.Errorf("DecimalToString = %q", DecimalToString(big.NewRat(25, 2)))
	}
	if BoolToString(false) != "false" {
		t.Error("BoolToString")
	}
}

func TestNumericCrossConversions(t *testing.T) {
	if FloatToInt(3.9) != 3 || FloatToInt(-3.9) != -3 {
		t.Error("FloatToInt truncation")
	}
	if DecimalToInt(big.NewRat(7, 2)) != 3 {
		t.Error("DecimalToInt truncation")
	}
	if IntToFloat(5) != 5.0 {
		t.Error("IntToFloat")
	}
	if IntToDecimal(4).Cmp(big.NewRat(4, 1)) != 0 {
		t.Error("IntToDecimal")
	}
}
