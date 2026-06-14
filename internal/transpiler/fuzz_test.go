package transpiler_test

import (
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/transpiler"
)

func FuzzTranspileSurvivesArbitraryGeblang(f *testing.F) {
	for _, seed := range []string{
		"",
		"import io;\nio.println(\"hi\");\n",
		"let x = 1;\n",
		"let xs = [1, 2, 3];\n",
		"func add(int a, int b): int { return a + b; }\n",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src string) {
		p := parser.New(lexer.New(src))
		program := p.ParseProgram()
		if len(p.Errors()) != 0 {
			t.Skip("parser rejected input")
		}

		// Phase 0 invariant: Transpile must never panic on parseable input.
		_, _, err := transpiler.Transpile(transpiler.Input{
			Modules: map[string]*ast.Program{"main": program},
		}, transpiler.Options{EntryModule: "main"})
		if err != nil {
			t.Skip("transpile returned error")
		}
	})
}
