package transpiler_test

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/transpiler"
)

// TestTranspileFuzzThreeWay generates random transpile-safe programs and asserts
// the evaluator, the bytecode VM, and the transpiled native binary agree. It is
// the generative sibling of the hand-written tests/transpile corpus: the gaps
// the net is built to catch hide in composition, and random composition reaches
// shapes a fixed corpus does not. A generated program that transpilation
// diagnoses is SKIPPED for the native leg (it is not transpile-safe); only
// cleanly-transpiling programs are three-way compared. VM vs eval is always
// compared. Determinism: a fixed seed runs the same program set; override
// GEBLANG_TFUZZ_SEED / GEBLANG_TFUZZ_ITERS to hunt; a failure prints the program.
func TestTranspileFuzzThreeWay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping transpile fuzz in -short mode")
	}

	repoRoot := repoRootFromTest(t)
	geblangBin := findGeblangBinary(t, repoRoot)

	seed := int64(1)
	if s := os.Getenv("GEBLANG_TFUZZ_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	iters := 60
	if s := os.Getenv("GEBLANG_TFUZZ_ITERS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			iters = v
		}
	}

	g := &transpileFuzzGen{rng: rand.New(rand.NewSource(seed))}
	for i := 0; i < iters; i++ {
		src := g.program()

		// Reject programs the parser does not accept (the generator aims for
		// valid, but bounds keep it simple rather than perfectly closed).
		p := parser.New(lexer.New(src))
		prog := p.ParseProgram()
		if len(p.Errors()) != 0 {
			continue
		}

		out, diags, err := transpiler.Transpile(transpiler.Input{
			Modules: map[string]*ast.Program{"main": prog},
		}, transpiler.Options{EntryModule: "main"})
		if err != nil {
			continue
		}
		if hasTranspileError(diags) {
			// Not transpile-safe: VM/eval still must agree, native is skipped.
			compareVMEval(t, repoRoot, geblangBin, src, seed, i)
			continue
		}

		vmOut, vmCode := runFuzzScript(t, repoRoot, geblangBin, src, "--vm-strict")
		evalOut, evalCode := runFuzzScript(t, repoRoot, geblangBin, src, "--disable-vm")
		if vmCode != evalCode || string(vmOut) != string(evalOut) {
			t.Fatalf("VM/eval divergence (seed=%d iter=%d)\nprogram:\n%s\n--- vm(%d) ---\n%q\n--- eval(%d) ---\n%q",
				seed, i, src, vmCode, vmOut, evalCode, evalOut)
		}

		work := t.TempDir()
		writeOutputTree(t, work, out)
		writeGoMod(t, work, repoRoot)
		natOut, _, err := goBuildAndRun(t, work, nil)
		if err != nil {
			t.Fatalf("native build/run failed for a cleanly-transpiling program (seed=%d iter=%d)\nprogram:\n%s\nerror: %v\noutput: %s",
				seed, i, src, err, natOut)
		}
		if string(vmOut) != string(natOut) {
			t.Fatalf("VM/native divergence (seed=%d iter=%d)\nprogram:\n%s\n--- vm ---\n%q\n--- native ---\n%q",
				seed, i, src, vmOut, natOut)
		}
	}
}

func compareVMEval(t *testing.T, repoRoot, bin, src string, seed int64, iter int) {
	t.Helper()
	vmOut, vmCode := runFuzzScript(t, repoRoot, bin, src, "--vm-strict")
	evalOut, evalCode := runFuzzScript(t, repoRoot, bin, src, "--disable-vm")
	if vmCode != evalCode || string(vmOut) != string(evalOut) {
		t.Fatalf("VM/eval divergence on diagnosed program (seed=%d iter=%d)\nprogram:\n%s\n--- vm ---\n%q\n--- eval ---\n%q",
			seed, iter, src, vmOut, evalOut)
	}
}

func hasTranspileError(diags []transpiler.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == transpiler.SeverityError {
			return true
		}
	}
	return false
}

func runFuzzScript(t *testing.T, dir, bin, src, flag string) ([]byte, int) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "tfuzz-*.gb")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	if _, err := f.WriteString(src); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()
	return runScript(t, dir, bin, f.Name(), flag, nil)
}

// transpileFuzzGen emits random transpile-safe programs over a constrained
// subset: int/string/bool expressions, lists with HOF, control flow, and a
// small class with a method. Every program ends by printing its results.
type transpileFuzzGen struct {
	rng *rand.Rand
}

type fzKind int

const (
	fzkInt fzKind = iota
	fzkString
	fzkBool
)

func (g *transpileFuzzGen) program() string {
	var b strings.Builder
	b.WriteString("import io;\n")

	// Occasionally declare a small class used later for method-result chaining.
	useClass := g.rng.Intn(2) == 0
	if useClass {
		b.WriteString("class Bag {\n")
		b.WriteString("    list<int> xs;\n")
		b.WriteString("    func Bag() { this.xs = [")
		n := 2 + g.rng.Intn(4)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(g.intLit())
		}
		b.WriteString("]; }\n")
		b.WriteString("    func evens(): list<int> { return this.xs.filter(func(int x): bool { return x % 2 == 0; }); }\n")
		b.WriteString("    func sum(): int { return this.xs.reduce(func(int a, int b): int { return a + b; }, 0); }\n")
		b.WriteString("}\n")
	}

	stmts := 3 + g.rng.Intn(4)
	for i := 0; i < stmts; i++ {
		b.WriteString(g.statement(i, useClass))
	}
	return b.String()
}

func (g *transpileFuzzGen) statement(idx int, useClass bool) string {
	switch g.rng.Intn(6) {
	case 0:
		return fmt.Sprintf("io.println(%s);\n", g.expr(fzkInt, 3))
	case 1:
		return fmt.Sprintf("io.println(%s);\n", g.expr(fzkBool, 3))
	case 2:
		return fmt.Sprintf("io.println(%s);\n", g.expr(fzkString, 3))
	case 3:
		return g.listStmt()
	case 4:
		return g.controlStmt(idx)
	default:
		if useClass {
			return g.classStmt(idx)
		}
		return fmt.Sprintf("io.println(%s);\n", g.expr(fzkInt, 2))
	}
}

func (g *transpileFuzzGen) listStmt() string {
	n := 2 + g.rng.Intn(4)
	var elems []string
	for i := 0; i < n; i++ {
		elems = append(elems, g.intLit())
	}
	list := "[" + strings.Join(elems, ", ") + "]"
	switch g.rng.Intn(5) {
	case 0:
		return fmt.Sprintf("io.println(%s.map(func(int x): int { return x * 2; }));\n", list)
	case 1:
		return fmt.Sprintf("io.println(%s.filter(func(int x): bool { return x > 0; }));\n", list)
	case 2:
		return fmt.Sprintf("io.println(%s.reduce(func(int a, int b): int { return a + b; }, 0));\n", list)
	case 3:
		return fmt.Sprintf("io.println(%s.sorted());\n", list)
	default:
		return fmt.Sprintf("io.println(%s.map(func(int x): int { return x + 1; }).filter(func(int x): bool { return x %% 2 == 0; }).reduce(func(int a, int b): int { return a + b; }, 0));\n", list)
	}
}

func (g *transpileFuzzGen) controlStmt(idx int) string {
	v := fmt.Sprintf("acc%d", idx)
	hi := 2 + g.rng.Intn(4)
	switch g.rng.Intn(3) {
	case 0:
		return fmt.Sprintf("int %s = 0;\nfor (int i = 0; i < %d; i = i + 1) { %s = %s + i; }\nio.println(%s);\n", v, hi, v, v, v)
	case 1:
		return fmt.Sprintf("int %s = %s;\nif (%s) { %s = %s + 1; } else { %s = %s - 1; }\nio.println(%s);\n",
			v, g.intLit(), g.expr(fzkBool, 2), v, v, v, v, v)
	default:
		return fmt.Sprintf("int %s = 0;\nfor (int i = 0; i < %d; i = i + 1) { if (i %% 2 == 0) { %s = %s + i; } }\nio.println(%s);\n", v, hi, v, v, v)
	}
}

func (g *transpileFuzzGen) classStmt(idx int) string {
	bag := fmt.Sprintf("bag%d", idx)
	switch g.rng.Intn(3) {
	case 0:
		return fmt.Sprintf("let %s = Bag();\nio.println(%s.evens().sorted());\n", bag, bag)
	case 1:
		return fmt.Sprintf("let %s = Bag();\nio.println(%s.sum());\n", bag, bag)
	default:
		e := fmt.Sprintf("e%d", idx)
		return fmt.Sprintf("let %s = Bag();\nlet %s = %s.evens();\nio.println(%s.reduce(func(int a, int b): int { return a + b; }, 0));\n", bag, e, bag, e)
	}
}

func (g *transpileFuzzGen) intLit() string {
	return strconv.Itoa(g.rng.Intn(40) - 20)
}

func (g *transpileFuzzGen) expr(k fzKind, depth int) string {
	if depth <= 0 {
		return g.leaf(k)
	}
	switch k {
	case fzkInt:
		switch g.rng.Intn(5) {
		case 0:
			return g.leaf(k)
		case 1:
			return "(" + g.expr(fzkInt, depth-1) + " + " + g.expr(fzkInt, depth-1) + ")"
		case 2:
			return "(" + g.expr(fzkInt, depth-1) + " - " + g.expr(fzkInt, depth-1) + ")"
		case 3:
			return "(" + g.expr(fzkInt, depth-1) + " * " + g.expr(fzkInt, depth-1) + ")"
		default:
			return "(" + g.expr(fzkBool, depth-1) + " ? " + g.expr(fzkInt, depth-1) + " : " + g.expr(fzkInt, depth-1) + ")"
		}
	case fzkString:
		switch g.rng.Intn(4) {
		case 0:
			return g.leaf(k)
		case 1:
			return "(" + g.expr(fzkString, depth-1) + " + " + g.expr(fzkString, depth-1) + ")"
		case 2:
			return "(" + g.expr(fzkString, depth-1) + ".upper())"
		default:
			return "(\"n=\" + (" + g.expr(fzkInt, depth-1) + " as string))"
		}
	default: // fzkBool
		switch g.rng.Intn(5) {
		case 0:
			return g.leaf(k)
		case 1:
			return "(" + g.expr(fzkInt, depth-1) + " < " + g.expr(fzkInt, depth-1) + ")"
		case 2:
			return "(" + g.expr(fzkInt, depth-1) + " == " + g.expr(fzkInt, depth-1) + ")"
		case 3:
			return "(" + g.expr(fzkBool, depth-1) + " && " + g.expr(fzkBool, depth-1) + ")"
		default:
			return "(!" + g.expr(fzkBool, depth-1) + ")"
		}
	}
}

func (g *transpileFuzzGen) leaf(k fzKind) string {
	switch k {
	case fzkInt:
		return g.intLit()
	case fzkString:
		words := []string{"\"\"", "\"a\"", "\"Ada\"", "\"hi\"", "\"X9\""}
		return words[g.rng.Intn(len(words))]
	default:
		if g.rng.Intn(2) == 0 {
			return "true"
		}
		return "false"
	}
}
