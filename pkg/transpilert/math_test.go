package transpilert

import (
	"math"
	"testing"
)

func TestMathAbs(t *testing.T) {
	if MathAbs(-3.5) != 3.5 {
		t.Fatal("abs(-3.5) != 3.5")
	}
}

func TestMathFloorCeilRoundReturnInt(t *testing.T) {
	if MathFloor(2.9) != 2 {
		t.Fatalf("floor(2.9) = %d", MathFloor(2.9))
	}
	if MathCeil(2.1) != 3 {
		t.Fatalf("ceil(2.1) = %d", MathCeil(2.1))
	}
	if MathRound(2.5) != 3 {
		t.Fatalf("round(2.5) = %d", MathRound(2.5))
	}
	if MathTrunc(-2.9) != -2 {
		t.Fatalf("trunc(-2.9) = %d", MathTrunc(-2.9))
	}
}

func TestMathSign(t *testing.T) {
	if MathSign(-2) != -1 || MathSign(0) != 0 || MathSign(5) != 1 {
		t.Fatal("sign mismatch")
	}
}

func TestMathClamp(t *testing.T) {
	if MathClamp(5, 0, 3) != 3 || MathClamp(-1, 0, 3) != 0 || MathClamp(2, 0, 3) != 2 {
		t.Fatal("clamp mismatch")
	}
}

func TestMathLerpRemap(t *testing.T) {
	if MathLerp(0, 10, 0.5) != 5 {
		t.Fatal("lerp mismatch")
	}
	if MathRemap(5, 0, 10, 0, 100) != 50 {
		t.Fatal("remap mismatch")
	}
}

func TestMathPowSqrt(t *testing.T) {
	if MathPow(2, 10) != 1024 {
		t.Fatal("pow mismatch")
	}
	if MathSqrt(144) != 12 {
		t.Fatal("sqrt mismatch")
	}
}

func TestMathConstants(t *testing.T) {
	if MathPi() != math.Pi || MathTau() != 2*math.Pi {
		t.Fatal("constant mismatch")
	}
	if MathMaxInt() != math.MaxInt64 || MathMinInt() != math.MinInt64 {
		t.Fatal("int bound mismatch")
	}
}

func TestMathNaNInf(t *testing.T) {
	if !MathIsNaN(MathNaN()) || !MathIsInf(MathInf()) {
		t.Fatal("nan/inf predicate mismatch")
	}
}
