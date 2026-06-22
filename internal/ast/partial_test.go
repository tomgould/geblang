package ast

import (
	"testing"

	"geblang/internal/token"
)

func ident(name string) *Identifier { return &Identifier{Value: name} }

func TestPartialExpressionString(t *testing.T) {
	p := &PartialExpression{
		Token:  token.Token{Literal: "("},
		Callee: ident("add"),
		Arguments: []CallArgument{
			{Hole: true},
			{Value: &IntegerLiteral{Token: token.Token{Literal: "10"}, Value: "10"}},
		},
	}
	if got := p.String(); got != "add(_, 10)" {
		t.Fatalf("String() = %q, want %q", got, "add(_, 10)")
	}
}

func TestPartialExpressionStringNamedHole(t *testing.T) {
	p := &PartialExpression{
		Token:  token.Token{Literal: "("},
		Callee: ident("open"),
		Arguments: []CallArgument{
			{Name: ident("mode"), Hole: true},
		},
	}
	if got := p.String(); got != "open(mode: _)" {
		t.Fatalf("String() = %q, want %q", got, "open(mode: _)")
	}
}

func TestLowerPartialPositional(t *testing.T) {
	p := &PartialExpression{
		Token:  token.Token{Literal: "("},
		Callee: ident("add"),
		Arguments: []CallArgument{
			{Value: &IntegerLiteral{Token: token.Token{Literal: "1"}, Value: "1"}},
			{Hole: true},
		},
	}
	// One bound arg -> outer IIFE wrapping inner closure.
	got := LowerPartial(p)
	call, ok := got.(*CallExpression)
	if !ok {
		t.Fatalf("LowerPartial returned %T, want *CallExpression", got)
	}
	if _, ok := call.Callee.(*FunctionLiteral); !ok {
		t.Fatalf("IIFE callee is %T, want *FunctionLiteral", call.Callee)
	}
	if len(call.Arguments) != 1 {
		t.Fatalf("IIFE got %d outer args, want 1", len(call.Arguments))
	}
}

func TestLowerPartialAllHolesNoIIFE(t *testing.T) {
	p := &PartialExpression{
		Token:     token.Token{Literal: "("},
		Callee:    ident("add"),
		Arguments: []CallArgument{{Hole: true}, {Hole: true}},
	}
	got := LowerPartial(p)
	if _, ok := got.(*FunctionLiteral); !ok {
		t.Fatalf("with no bound args LowerPartial should return *FunctionLiteral, got %T", got)
	}
}
