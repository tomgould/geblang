package types_test

import (
	"testing"

	"geblang/internal/ast"
	"geblang/internal/token"
	"geblang/internal/transpiler/types"
)

func TestInferLiteralInteger(t *testing.T) {
	got := types.InferLiteral(&ast.IntegerLiteral{Token: token.Token{Literal: "42"}, Value: "42"})
	if got.Kind != types.KindInt {
		t.Errorf("got Kind %v, want KindInt", got.Kind)
	}
}

func TestInferLiteralFloat(t *testing.T) {
	got := types.InferLiteral(&ast.FloatLiteral{Token: token.Token{Literal: "1.5"}, Value: "1.5"})
	if got.Kind != types.KindFloat {
		t.Errorf("got Kind %v, want KindFloat", got.Kind)
	}
}

func TestInferLiteralDecimal(t *testing.T) {
	got := types.InferLiteral(&ast.DecimalLiteral{Token: token.Token{Literal: "1.5d"}, Value: "1.5"})
	if got.Kind != types.KindDecimal {
		t.Errorf("got Kind %v, want KindDecimal", got.Kind)
	}
}

func TestInferLiteralString(t *testing.T) {
	got := types.InferLiteral(&ast.StringLiteral{Token: token.Token{Literal: `"hi"`}, Value: "hi"})
	if got.Kind != types.KindString {
		t.Errorf("got Kind %v, want KindString", got.Kind)
	}
}

func TestInferLiteralInterpolatedString(t *testing.T) {
	got := types.InferLiteral(&ast.InterpolatedString{})
	if got.Kind != types.KindString {
		t.Errorf("got Kind %v, want KindString", got.Kind)
	}
}

func TestInferLiteralUnknownForNonLiteral(t *testing.T) {
	got := types.InferLiteral(&ast.Identifier{Value: "x"})
	if got.Kind != types.KindUnknown {
		t.Errorf("got Kind %v, want KindUnknown", got.Kind)
	}
}
