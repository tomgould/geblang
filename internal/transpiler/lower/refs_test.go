package lower

import (
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

func parseProgram(t *testing.T, src string) *ast.Program {
	t.Helper()
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parser errors: %v", errs)
	}
	return prog
}

func TestReferencesNameFindsIdentifierInExprStmt(t *testing.T) {
	prog := parseProgram(t, "io.println(x);\n")
	if !referencesName(prog.Statements[0], "x") {
		t.Errorf("expected to find reference to x")
	}
}

func TestReferencesNameMissesAbsentName(t *testing.T) {
	prog := parseProgram(t, "io.println(y);\n")
	if referencesName(prog.Statements[0], "x") {
		t.Errorf("expected miss for x")
	}
}

func TestReferencesNameTreatsUnderscoreAndEmptyAsAbsent(t *testing.T) {
	prog := parseProgram(t, "io.println(x);\n")
	if referencesName(prog.Statements[0], "_") {
		t.Errorf("referencesName(_) should return false")
	}
	if referencesName(prog.Statements[0], "") {
		t.Errorf("referencesName(empty) should return false")
	}
}

func TestReferencesNameWalksNestedExpressions(t *testing.T) {
	prog := parseProgram(t, "let r = a[i] + b.length();\n")
	for _, want := range []string{"a", "i", "b"} {
		if !referencesName(prog.Statements[0], want) {
			t.Errorf("expected to find reference to %q", want)
		}
	}
	if referencesName(prog.Statements[0], "z") {
		t.Errorf("did not expect reference to z")
	}
}

func TestReferencesNameWalksForBody(t *testing.T) {
	prog := parseProgram(t, "for (n in [1, 2, 3]) { total = total + n; }\n")
	if !referencesName(prog.Statements[0], "total") {
		t.Errorf("expected to find total in for body")
	}
	if !referencesName(prog.Statements[0], "n") {
		t.Errorf("expected to find n in for body")
	}
}
