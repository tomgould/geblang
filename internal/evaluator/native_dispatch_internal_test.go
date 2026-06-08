package evaluator

import (
	"io"
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/runtime"
)

// An env-local *runtime.Module binding must be authoritative even when its
// Canonical is empty; the dispatch must not silently fall back to the global
// importNames alias map and misresolve against a different native module.
func TestNativeDispatchPrefersEnvLocalModuleOverImportNames(t *testing.T) {
	e := New(io.Discard)

	p := parser.New(lexer.New("m.sqrt(4);"))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	exprStmt, ok := program.Statements[0].(*ast.ExpressionStatement)
	if !ok {
		t.Fatalf("expected expression statement, got %T", program.Statements[0])
	}

	e.imports["m"] = true
	e.importNames["m"] = "math"

	env := runtime.NewEnvironment()
	if err := env.Define("m", &runtime.Module{Name: "m", Canonical: ""}, true); err != nil {
		t.Fatalf("define m: %v", err)
	}

	result, err := e.evalExpression(exprStmt.Expression, env)
	if err == nil {
		t.Fatalf("expected error from empty-canonical env-local module, got result %v", result)
	}
}
