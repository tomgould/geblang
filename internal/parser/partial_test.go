package parser

import (
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
)

func parseExprStmt(t *testing.T, src string) ast.Expression {
	t.Helper()
	p := New(lexer.New(src))
	prog := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("unexpected parse errors: %v", p.Errors())
	}
	es, ok := prog.Statements[0].(*ast.ExpressionStatement)
	if !ok {
		t.Fatalf("stmt is %T, want *ast.ExpressionStatement", prog.Statements[0])
	}
	return es.Expression
}

func TestParsePositionalHole(t *testing.T) {
	e := parseExprStmt(t, "add(_, 10);")
	p, ok := e.(*ast.PartialExpression)
	if !ok {
		t.Fatalf("got %T, want *ast.PartialExpression", e)
	}
	if !p.Arguments[0].Hole || p.Arguments[1].Hole {
		t.Fatalf("hole flags wrong: %+v", p.Arguments)
	}
}

func TestParseNamedHole(t *testing.T) {
	e := parseExprStmt(t, "open(mode: _);")
	p, ok := e.(*ast.PartialExpression)
	if !ok {
		t.Fatalf("got %T, want *ast.PartialExpression", e)
	}
	if !p.Arguments[0].Hole || p.Arguments[0].Name == nil || p.Arguments[0].Name.Value != "mode" {
		t.Fatalf("named hole wrong: %+v", p.Arguments[0])
	}
}

func TestNoHoleIsPlainCall(t *testing.T) {
	if _, ok := parseExprStmt(t, "add(1, 2);").(*ast.CallExpression); !ok {
		t.Fatalf("call with no holes should stay *ast.CallExpression")
	}
}

func TestHoleWithSpreadIsError(t *testing.T) {
	p := New(lexer.New("log(_, ...xs);"))
	p.ParseProgram()
	if len(p.Errors()) == 0 {
		t.Fatalf("expected an error for mixing _ and ...")
	}
}
