package bytecode_test

import (
	"bytes"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

func compileSource(b *testing.B, src string) bytecode.Chunk {
	b.Helper()
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if len(p.Errors()) != 0 {
		b.Fatalf("parse errors: %v", p.Errors())
	}
	chunk, err := bytecode.Compile(prog, []byte(src), "bench")
	if err != nil {
		b.Fatalf("compile error: %v", err)
	}
	return chunk
}

func runChunk(b *testing.B, chunk bytecode.Chunk) {
	b.Helper()
	var out bytes.Buffer
	vm := bytecode.NewVM(chunk, &out)
	if err := vm.Run(); err != nil {
		b.Fatalf("vm error: %v", err)
	}
}

// BenchmarkIntLoop measures a tight typed-int arithmetic loop — the primary
// target of the SmallInt and OpAddInt/OpLessInt specializations.
func BenchmarkIntLoop(b *testing.B) {
	const src = `
int total = 0;
int n = 100000;
for (int i = 0; i < n; i++) {
    total = total + i;
}
`
	chunk := compileSource(b, src)
	b.ResetTimer()
	for range b.N {
		runChunk(b, chunk)
	}
}

// BenchmarkIntArithmetic measures a tight arithmetic expression with typed ints.
func BenchmarkIntArithmetic(b *testing.B) {
	const src = `
import io;
int a = 3;
int b = 7;
int c = 0;
for (int i = 0; i < 100000; i++) {
    c = (a * b + i) % 13;
}
io.println(c);
`
	chunk := compileSource(b, src)
	b.ResetTimer()
	for range b.N {
		runChunk(b, chunk)
	}
}

// BenchmarkRecursiveFib measures recursive function calls with typed int.
func BenchmarkRecursiveFib(b *testing.B) {
	const src = `
func fib(int n): int {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
int result = fib(25);
`
	chunk := compileSource(b, src)
	b.ResetTimer()
	for range b.N {
		runChunk(b, chunk)
	}
}
