package evaluator

import (
	"bytes"
	"testing"

	"geblang/internal/lexer"
	"geblang/internal/parser"
)

func evalSource(t *testing.T, src string) string {
	t.Helper()
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	var out bytes.Buffer
	if _, err := New(&out).Eval(prog); err != nil {
		t.Fatalf("eval error: %v", err)
	}
	return out.String()
}

func TestEvalPartialPositional(t *testing.T) {
	got := evalSource(t, `
		import io;
		func add(int a, int b): int { return a + b; }
		let add10 = add(_, 10);
		io.println(add10(3));
	`)
	if got != "13\n" {
		t.Fatalf("got %q, want %q", got, "13\n")
	}
}
