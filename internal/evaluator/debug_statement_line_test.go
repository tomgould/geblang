package evaluator

import (
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// statementLine must resolve a line for every ast statement type; a missing
// case silently breaks DAP breakpoints. select is the one most recently added.
func TestStatementLineCoversSelect(t *testing.T) {
	src := `import async.channel as ch;

func main(): void {
    let c = ch.Channel<int>(1);
    select {
        case let v = c.recv(): { }
    }
}`
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	var sel *ast.SelectStatement
	var walk func(stmts []ast.Statement)
	walk = func(stmts []ast.Statement) {
		for _, s := range stmts {
			if fn, ok := s.(*ast.FunctionStatement); ok && fn.Body != nil {
				walk(fn.Body.Statements)
			}
			if ss, ok := s.(*ast.SelectStatement); ok {
				sel = ss
			}
		}
	}
	walk(program.Statements)
	if sel == nil {
		t.Fatal("no select statement parsed")
	}
	if got := statementLine(sel); got != sel.Token.Line {
		t.Fatalf("statementLine(select) = %d, want %d", got, sel.Token.Line)
	}
}
