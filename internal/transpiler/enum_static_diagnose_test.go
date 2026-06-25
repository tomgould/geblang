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

// values()/fromName() lower natively; any other enum static call is unsupported
// and MUST fail loud with a diagnostic, never emit raw Go (lower-or-diagnose).
func TestEnumStaticUnknownDiagnosesUnderNative(t *testing.T) {
	src := `import io;
enum Status { Active, Closed }
export func main() {
    io.println(Status.bogus());
}`
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
		if d.Severity == transpiler.SeverityError && strings.Contains(d.Message, "enum static call") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an unsupported-enum-static-call diagnostic, got: %v", diags)
	}
}

func TestBackedEnumDiagnosesUnderNative(t *testing.T) {
	src := `import io;
enum Status: string { Active = "active"; Closed = "closed"; }
export func main() {
    io.println(Status.Active.value);
}`
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
		if d.Severity == transpiler.SeverityError && strings.Contains(d.Message, "does not support backed enum") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a backed-enum diagnostic, got: %v", diags)
	}
}
