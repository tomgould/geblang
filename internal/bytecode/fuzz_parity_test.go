package bytecode_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// Generative parity fuzzing (D2). Emits random *valid, type-directed*
// Geblang programs whose every result (and every caught error class) is
// printed, runs each on both the evaluator and the bytecode VM, and
// fails on any stdout or success/failure divergence. This is the
// semantic-drift guard the surface guards (R-series) cannot provide: it
// would have auto-caught this session's catchability / instanceof /
// errors.is divergences.
//
// Determinism (no-flaky rule): a fixed seed runs the same program set
// every time. Override GEBLANG_FUZZ_SEED / GEBLANG_FUZZ_ITERS to hunt
// for new divergences; a failure prints the seed + program to reproduce.

type fuzzType int

const (
	fzInt fuzzType = iota
	fzDecimal
	fzString
	fzBool
)

var fuzzScalarTypes = []fuzzType{fzInt, fzDecimal, fzString, fzBool}

type fuzzGen struct {
	rng *rand.Rand
}

func (g *fuzzGen) intLit() string {
	return strconv.Itoa(g.rng.Intn(199) - 99)
}

func (g *fuzzGen) decimalLit() string {
	return fmt.Sprintf("%d.%02d", g.rng.Intn(40)-20, g.rng.Intn(100))
}

func (g *fuzzGen) stringLit() string {
	words := []string{"", "a", "Ada", "  pad  ", "Hello", "x9", "MiXeD", "z"}
	return strconv.Quote(words[g.rng.Intn(len(words))])
}

// expr returns a valid expression of type t. depth bounds recursion.
func (g *fuzzGen) expr(t fuzzType, depth int) string {
	if depth <= 0 {
		return g.leaf(t)
	}
	switch t {
	case fzInt:
		switch g.rng.Intn(9) {
		case 0:
			return g.leaf(t)
		case 1:
			return "(" + g.expr(fzInt, depth-1) + " + " + g.expr(fzInt, depth-1) + ")"
		case 2:
			return "(" + g.expr(fzInt, depth-1) + " - " + g.expr(fzInt, depth-1) + ")"
		case 3:
			return "(" + g.expr(fzInt, depth-1) + " * " + g.expr(fzInt, depth-1) + ")"
		case 4:
			return "(" + g.expr(fzInt, depth-1) + ").abs()"
		case 5:
			return "(" + g.expr(fzString, depth-1) + ").length()"
		case 6:
			return "(" + g.expr(fzInt, depth-1) + ").sign()"
		case 7: // decimal -> int cast (truncation)
			return "((" + g.expr(fzDecimal, depth-1) + ") as int)"
		default: // ternary
			return "(" + g.expr(fzBool, depth-1) + " ? " + g.expr(fzInt, depth-1) + " : " + g.expr(fzInt, depth-1) + ")"
		}
	case fzDecimal:
		switch g.rng.Intn(6) {
		case 0:
			return g.leaf(t)
		case 1:
			return "(" + g.expr(fzDecimal, depth-1) + " + " + g.expr(fzDecimal, depth-1) + ")"
		case 2:
			return "(" + g.expr(fzDecimal, depth-1) + " * " + g.expr(fzDecimal, depth-1) + ")"
		case 3:
			return "(" + g.expr(fzDecimal, depth-1) + ").round(" + strconv.Itoa(g.rng.Intn(4)) + ")"
		case 4:
			return "(" + g.expr(fzDecimal, depth-1) + ").abs()"
		default: // int -> decimal cast
			return "((" + g.expr(fzInt, depth-1) + ") as decimal)"
		}
	case fzString:
		switch g.rng.Intn(7) {
		case 0:
			return g.leaf(t)
		case 1:
			return "(" + g.expr(fzString, depth-1) + " + " + g.expr(fzString, depth-1) + ")"
		case 2:
			return "(" + g.expr(fzString, depth-1) + ").upper()"
		case 3:
			return "(" + g.expr(fzString, depth-1) + ").trim()"
		case 4:
			return "(" + g.expr(fzInt, depth-1) + ").toString()"
		case 5: // plain f-string interpolation
			return "(\"${" + g.expr(fzInt, depth-1) + "}\")"
		default: // f-string with fixed-point format spec on a decimal
			return "(\"${(" + g.expr(fzDecimal, depth-1) + "):.2f}\")"
		}
	default: // fzBool
		switch g.rng.Intn(7) {
		case 0:
			return g.leaf(t)
		case 1:
			return "(" + g.expr(fzInt, depth-1) + " < " + g.expr(fzInt, depth-1) + ")"
		case 2:
			return "(" + g.expr(fzInt, depth-1) + " == " + g.expr(fzInt, depth-1) + ")"
		case 3:
			return "(" + g.expr(fzBool, depth-1) + " && " + g.expr(fzBool, depth-1) + ")"
		case 4:
			return "(" + g.expr(fzBool, depth-1) + " || " + g.expr(fzBool, depth-1) + ")"
		case 5:
			return "(!" + g.expr(fzBool, depth-1) + ")"
		default:
			return "(" + g.expr(fzInt, depth-1) + ").isEven()"
		}
	}
}

func (g *fuzzGen) leaf(t fuzzType) string {
	switch t {
	case fzInt:
		return g.intLit()
	case fzDecimal:
		return "(" + g.decimalLit() + ")"
	case fzString:
		return g.stringLit()
	default:
		if g.rng.Intn(2) == 0 {
			return "true"
		}
		return "false"
	}
}

// faultStmt emits a try/catch around a fault-prone operation, printing
// "ok" on success or the caught class on failure - the error model is
// where eval/VM diverged this session, so this is the highest-value
// generator path. Output is class names / "ok", never raw values, so
// there is no type-formatting ambiguity to cause false positives.
func (g *fuzzGen) faultStmt() string {
	// Operands come from `.toInt()` of a string literal so the bytecode
	// compiler cannot constant-fold (and thus reject at compile time) a
	// literal `x / 0`; the fault must occur at runtime on both backends
	// for the comparison to be valid. The VM's compile-time strictness
	// for constant faults is a known, accepted difference.
	rt := func(n int) string { return "(\"" + strconv.Itoa(n) + "\".toInt())" }
	var body string
	switch g.rng.Intn(4) {
	case 0:
		body = "let q = " + g.intLit() + " / " + rt(g.rng.Intn(3)) + ";"
	case 1:
		body = "let r = " + g.intLit() + " % " + rt(g.rng.Intn(3)) + ";"
	case 2:
		body = "let xs = [1, 2, 3]; let v = xs[" + rt(g.rng.Intn(6)) + "];"
	default:
		body = "let n = " + g.stringLit() + ".toInt();"
	}
	return "try { " + body + " io.println(\"ok\"); } catch (Error e) { io.println(e.class); }"
}

// classBlock emits a valid random class hierarchy: a base class with a
// typed int field + two methods (one calling this.other() so override
// dispatch is exercised), one or two subclasses overriding a method and
// calling parent.method()/parent(...), plus a generic Box<int>. Method
// bodies print nothing and return ints/bools/strings the caller prints,
// so output is value-only with no type-formatting ambiguity. Dispatch is
// the recurring eval/VM bug source, so this is the high-value path.
func (g *fuzzGen) classBlock() (decls string, calls []string) {
	base := g.intLit()
	bump := g.intLit()
	var d strings.Builder
	// Base: field, value(), labeled() that calls this.value() (override-dispatched).
	d.WriteString("class Base {\n")
	d.WriteString("  int n;\n")
	d.WriteString("  string tag;\n")
	d.WriteString("  func Base(int n) { this.n = n; this.tag = \"base\"; }\n")
	d.WriteString("  func value(): int { return (this.n + " + base + "); }\n")
	d.WriteString("  func labeled(): int { return (this.value() * 2); }\n")
	d.WriteString("  func name(): string { return this.tag; }\n")
	d.WriteString("}\n")
	// Sub: overrides value() via parent.value(), overrides name().
	d.WriteString("class Sub extends Base {\n")
	d.WriteString("  func Sub(int n) { parent(n); this.tag = \"sub\"; }\n")
	d.WriteString("  func value(): int { return (parent.value() + " + bump + "); }\n")
	d.WriteString("  func name(): string { return (parent.name() + \"!\"); }\n")
	d.WriteString("}\n")
	// Leaf: two levels deep; overrides value() chaining parent.value().
	d.WriteString("class Leaf extends Sub {\n")
	d.WriteString("  func Leaf(int n) { parent(n); }\n")
	d.WriteString("  func value(): int { return (parent.value() - 3); }\n")
	d.WriteString("}\n")
	// Generic box.
	d.WriteString("class Box<T> {\n")
	d.WriteString("  T item;\n")
	d.WriteString("  func Box(T v) { this.item = v; }\n")
	d.WriteString("  func get(): T { return this.item; }\n")
	d.WriteString("}\n")
	ctors := []string{"Base", "Sub", "Leaf"}
	c := ctors[g.rng.Intn(len(ctors))]
	inst := "let inst = " + c + "(" + g.intLit() + ");"
	calls = append(calls,
		inst,
		"io.println(inst.value());",     // override dispatch
		"io.println(inst.labeled());",   // base method calling overridden this.value()
		"io.println(inst.name());",
		"io.println(inst.n);",           // field read (inherited)
		"io.println(inst instanceof Base);",
		"let bx = Box(" + g.intLit() + "); io.println(bx.get());",
	)
	return d.String(), calls
}

func (g *fuzzGen) program() string {
	var b strings.Builder
	b.WriteString("import io;\n")
	// Roughly half the programs exercise the class/dispatch surface.
	emitClasses := g.rng.Intn(2) == 0
	var classCalls []string
	if emitClasses {
		decls, calls := g.classBlock()
		b.WriteString(decls)
		classCalls = calls
	}
	stmts := 3 + g.rng.Intn(5)
	for i := 0; i < stmts; i++ {
		if emitClasses && i < len(classCalls) {
			b.WriteString(classCalls[i])
		} else if g.rng.Intn(3) == 0 {
			b.WriteString(g.faultStmt())
		} else {
			t := fuzzScalarTypes[g.rng.Intn(len(fuzzScalarTypes))]
			b.WriteString("io.println(" + g.expr(t, 3) + ");")
		}
		b.WriteByte('\n')
	}
	// Ensure all class calls run even if the statement budget was small.
	if emitClasses {
		for i := stmts; i < len(classCalls); i++ {
			b.WriteString(classCalls[i])
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func fuzzRunBoth(source string) (evOut string, evErr error, vmOut string, vmErr error, compileErr error) {
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		return "", nil, "", nil, fmt.Errorf("parse: %s", strings.Join(p.Errors(), "; "))
	}
	var evBuf bytes.Buffer
	_, evErr = evaluator.New(&evBuf).Eval(program)
	chunk, cErr := bytecode.Compile(program, []byte(source), "fuzz")
	if cErr != nil {
		return "", nil, "", nil, fmt.Errorf("compile: %w", cErr)
	}
	var vmBuf bytes.Buffer
	vmErr = bytecode.NewVM(chunk, &vmBuf).Run()
	return evBuf.String(), evErr, vmBuf.String(), vmErr, nil
}

func TestParityFuzz(t *testing.T) {
	seed := int64(1)
	if s := os.Getenv("GEBLANG_FUZZ_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	iters := 800
	if s := os.Getenv("GEBLANG_FUZZ_ITERS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			iters = v
		}
	}
	g := &fuzzGen{rng: rand.New(rand.NewSource(seed))}
	for i := 0; i < iters; i++ {
		src := g.program()
		evOut, evErr, vmOut, vmErr, compileErr := fuzzRunBoth(src)
		if compileErr != nil {
			t.Fatalf("generated program did not compile (seed %d, iter %d): %v\n%s", seed, i, compileErr, src)
		}
		if (evErr == nil) != (vmErr == nil) {
			t.Fatalf("PARITY: success/failure divergence (seed %d, iter %d)\nprogram:\n%s\neval err: %v\nvm err: %v\neval out: %q\nvm out: %q",
				seed, i, src, evErr, vmErr, evOut, vmOut)
		}
		if evOut != vmOut {
			t.Fatalf("PARITY: stdout divergence (seed %d, iter %d)\nprogram:\n%s\neval: %q\nvm:   %q",
				seed, i, src, evOut, vmOut)
		}
	}
}
