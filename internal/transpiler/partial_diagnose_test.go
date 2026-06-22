package transpiler_test

import (
	"strings"
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/transpiler"
	"geblang/internal/transpiler/types"
)

// Partial application must diagnose cleanly, never emit broken Go.
func TestPartialApplicationDiagnosesUnderNative(t *testing.T) {
	src := `import io;
func add(int a, int b): int { return a + b; }
let add10 = add(_, 10);
io.println(add10(3));
`
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse: %s", strings.Join(errs, "; "))
	}
	_, diags, err := transpiler.Transpile(transpiler.Input{
		Modules: map[string]*ast.Program{"main": prog},
	}, transpiler.Options{EntryModule: "main", IntMode: types.IntModeFast})
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	var found bool
	for _, d := range diags {
		if d.Severity == transpiler.SeverityError && strings.Contains(d.Message, "partial application") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a partial-application diagnostic, got: %v", diags)
	}
}
