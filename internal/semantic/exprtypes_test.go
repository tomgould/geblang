package semantic_test

import (
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/semantic"
)

// collectIdentTypes returns the recorded type for the first identifier
// with each name, walking the map's keys.
func identType(t *testing.T, types map[ast.Expression]semantic.ExprType, name string) (semantic.ExprType, bool) {
	t.Helper()
	for expr, et := range types {
		if id, ok := expr.(*ast.Identifier); ok && id.Value == name {
			return et, true
		}
	}
	return semantic.ExprType{}, false
}

func litType(types map[ast.Expression]semantic.ExprType) (semantic.ExprType, bool) {
	for expr, et := range types {
		if _, ok := expr.(*ast.IntegerLiteral); ok {
			return et, true
		}
	}
	return semantic.ExprType{}, false
}

func TestResolveExpressionTypesRecordsTypes(t *testing.T) {
	input := `
class Box {
    int count;
    func Box(int count) { this.count = count; }
    func report(): int {
        let local = this.count;
        return local;
    }
}

int n = 5;
let usedN = n;
let xs = [1, 2, 3];
let usedXs = xs;
let b = Box(7);
let usedB = b;
for (item in xs) {
    let doubled = item;
}
`
	p := parser.New(lexer.New(input))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	types := semantic.ResolveExpressionTypes(program)
	if len(types) == 0 {
		t.Fatal("no expression types recorded")
	}

	if lit, ok := litType(types); !ok || lit.Name != "int" {
		t.Fatalf("integer literal type: got %+v ok=%v, want int", lit, ok)
	}
	if nt, ok := identType(t, types, "n"); !ok || nt.Name != "int" {
		t.Fatalf("declared int var n: got %+v ok=%v, want int", nt, ok)
	}
	// `local` inside a method body reads `this.count` (int) - Phase 2
	// method-body analysis must have typed it.
	if lt, ok := identType(t, types, "local"); !ok || lt.Name != "int" {
		t.Fatalf("method-body local from this.count: got %+v ok=%v, want int", lt, ok)
	}
	// `xs` is list<int>; `item` (for-in binding) and `doubled` must be int.
	if xt, ok := identType(t, types, "xs"); !ok || xt.Name != "list" || len(xt.Args) != 1 || xt.Args[0].Name != "int" {
		t.Fatalf("xs type: got %+v ok=%v, want list<int>", xt, ok)
	}
	if it, ok := identType(t, types, "item"); !ok || it.Name != "int" {
		t.Fatalf("for-in binding item: got %+v ok=%v, want int", it, ok)
	}
	if bt, ok := identType(t, types, "b"); !ok || bt.Name != "Box" {
		t.Fatalf("constructor result b: got %+v ok=%v, want Box", bt, ok)
	}
}

// TestResolveExpressionTypesNoNormalImpact verifies recording mode does
// not change the diagnostics a normal Analyze produces.
func TestResolveExpressionTypesNoNormalImpact(t *testing.T) {
	input := `
int n = 5;
let xs = [1, 2, 3];
for (x in xs) {
    let y = x;
}
`
	p := parser.New(lexer.New(input))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	normal := semantic.New().Analyze(program)
	if len(normal) != 0 {
		t.Fatalf("clean program produced diagnostics: %v", normal)
	}
	// Recording must not panic or wedge on the same program.
	types := semantic.ResolveExpressionTypes(program)
	if len(types) == 0 {
		t.Fatal("recording produced no types")
	}
}
