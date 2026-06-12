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
// "ok" on success or the caught class AND message on failure - error
// class dispatch and message text are both guarded parity surfaces.
func (g *fuzzGen) faultStmt() string {
	// Operands come from `.toInt()` of a string literal so the bytecode
	// compiler cannot constant-fold (and thus reject at compile time) a
	// literal `x / 0`; the fault must occur at runtime on both backends
	// for the comparison to be valid. The VM's compile-time strictness
	// for constant faults is a known, accepted difference.
	rt := func(n int) string { return "(\"" + strconv.Itoa(n) + "\".toInt())" }
	var body string
	switch g.rng.Intn(6) {
	case 0:
		body = "let q = " + g.intLit() + " / " + rt(g.rng.Intn(3)) + ";"
	case 1:
		body = "let r = " + g.intLit() + " % " + rt(g.rng.Intn(3)) + ";"
	case 2:
		body = "let xs = [1, 2, 3]; let v = xs[" + rt(g.rng.Intn(6)) + "];"
	case 3:
		receivers := []string{"[1, 2]", "{1, 2}", "{\"k\": 1}", g.stringLit(), g.intLit()}
		body = "let m = (" + receivers[g.rng.Intn(len(receivers))] + ").bogusMethod();"
	case 4:
		lefts := []string{g.stringLit(), "[1]", "true"}
		ops := []string{"-", "*", "%"}
		body = "let o = " + lefts[g.rng.Intn(len(lefts))] + " " + ops[g.rng.Intn(len(ops))] + " " + rt(1+g.rng.Intn(3)) + ";"
	default:
		body = "let n = " + g.stringLit() + ".toInt();"
	}
	return "try { " + body + " io.println(\"ok\"); } catch (Error e) { io.println(e.class); io.println(e.message); }"
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
	d.WriteString("  func put(T v): void { this.item = v; }\n")
	d.WriteString("}\n")
	ctors := []string{"Base", "Sub", "Leaf"}
	c := ctors[g.rng.Intn(len(ctors))]
	inst := "let inst = " + c + "(" + g.intLit() + ");"
	calls = append(calls,
		inst,
		"io.println(inst.value());",   // override dispatch
		"io.println(inst.labeled());", // base method calling overridden this.value()
		"io.println(inst.name());",
		"io.println(inst.n);", // field read (inherited)
		"io.println(inst instanceof Base);",
		"let bx = Box("+g.intLit()+"); io.println(bx.get());",
	)
	// Explicit type-arg enforcement + binding-aware instanceof (1.20.0):
	// matching constructions succeed, mismatching constructor and method
	// args throw - class AND message must agree across backends.
	wrap := func(body string) string {
		return "try { " + body + " io.println(\"ok\"); } catch (Error e) { io.println(e.class); io.println(e.message); }"
	}
	if g.rng.Intn(2) == 0 {
		calls = append(calls,
			"let gb = Box<int>("+g.intLit()+"); io.println(gb.get()); io.println(gb instanceof Box<int>); io.println(gb instanceof Box<string>);",
			wrap("let gbad = Box<string>("+g.intLit()+");"),
			wrap("let gm = Box<int>("+g.intLit()+"); gm.put("+g.stringLit()+");"),
		)
	}
	return d.String(), calls
}

// signatureBlock emits a function (and method/static/constructor
// carriers of the same signature) mixing required, defaulted, and
// variadic parameters, plus calls in every binding shape: positional
// subsets, named args for defaulted params, and list spread. Argument
// binding across dispatch contexts is a proven divergence source
// (the default+variadic cluster fixed in 1.17.0).
func (g *fuzzGen) signatureBlock() (decls string, calls []string) {
	// Non-negative: a negative default is a prefix expression, which the
	// bytecode compiler rejects (literal defaults only).
	defB := strconv.Itoa(g.rng.Intn(99))
	hasVariadic := g.rng.Intn(2) == 0
	sig := "int a, int b = " + defB
	body := "\"${a}|${b}\""
	if hasVariadic {
		sig += ", int ...rest"
		body = "\"${a}|${b}|${rest}\""
	}
	var d strings.Builder
	d.WriteString("func sigf(" + sig + "): string { return " + body + "; }\n")
	d.WriteString("class Sig {\n")
	d.WriteString("  string s;\n")
	d.WriteString("  func Sig(" + sig + ") { this.s = " + body + "; }\n")
	d.WriteString("  func m(" + sig + "): string { return " + body + "; }\n")
	d.WriteString("  static func sm(" + sig + "): string { return " + body + "; }\n")
	d.WriteString("}\n")
	d.WriteString("let sigl = func(" + sig + "): string { return " + body + "; };\n")

	argShapes := []string{
		"(" + g.intLit() + ")",
		"(" + g.intLit() + ", " + g.intLit() + ")",
		"(" + g.intLit() + ", b: " + g.intLit() + ")",
		"(a: " + g.intLit() + ", b: " + g.intLit() + ")",
		"(b: " + g.intLit() + ", a: " + g.intLit() + ")",
		"(...{\"a\": " + g.intLit() + ", \"b\": " + g.intLit() + "})",
		"(...{\"a\": " + g.intLit() + "})",
	}
	if hasVariadic {
		argShapes = append(argShapes,
			"("+g.intLit()+", "+g.intLit()+", "+g.intLit()+", "+g.intLit()+")",
			"(...["+g.intLit()+", "+g.intLit()+", "+g.intLit()+"])",
		)
	} else {
		argShapes = append(argShapes, "(...["+g.intLit()+", "+g.intLit()+"])")
	}
	shape := func() string { return argShapes[g.rng.Intn(len(argShapes))] }
	calls = append(calls,
		"io.println(sigf"+shape()+");",
		"io.println(Sig"+shape()+".s);",
		"io.println(Sig(0).m"+shape()+");",
		"io.println(Sig.sm"+shape()+");",
		"io.println(sigl"+shape()+");",
	)
	return d.String(), calls
}

// generatorBlock emits a generator that yields a few values and then
// either finishes or throws a typed error, consumed by a for-in inside
// try/catch that prints every value and any caught class+message.
// Generator fault routing is a proven divergence source (class loss
// fixed in 1.17.0).
func (g *fuzzGen) generatorBlock() (decls string, calls []string) {
	yields := 1 + g.rng.Intn(3)
	var d strings.Builder
	d.WriteString("class GenErr extends ValueError { func GenErr(string m) { parent(m); } }\n")
	d.WriteString("func gen(): generator<int> {\n")
	if g.rng.Intn(2) == 0 {
		for i := 0; i < yields; i++ {
			d.WriteString("  yield " + g.intLit() + ";\n")
		}
	} else {
		// Loop-driven yields: resumption interleaves with loop state.
		d.WriteString("  for (i in 0..<" + strconv.Itoa(yields+1) + ") {\n")
		d.WriteString("    yield (i * " + strconv.Itoa(1+g.rng.Intn(9)) + ");\n")
		d.WriteString("  }\n")
	}
	switch g.rng.Intn(3) {
	case 0:
		// clean finish
	case 1:
		d.WriteString("  throw ValueError(" + g.stringLit() + ");\n")
	default:
		d.WriteString("  throw GenErr(" + g.stringLit() + ");\n")
	}
	d.WriteString("}\n")
	calls = append(calls,
		`try { for (n in gen()) { io.println(n); } io.println("done"); } catch (ValueError e) { io.println(e.class); io.println(e.message); }`,
	)
	return d.String(), calls
}

// loopBlock emits instruction-dense control flow: a for loop with
// break/continue and compound assignment, a while-driven string
// accumulator, and container index mutation. Broad straight-line
// instruction coverage guards representation changes (e.g. operand
// packing) where sparse expression trees would not.
func (g *fuzzGen) loopBlock() (decls string, calls []string) {
	limit := strconv.Itoa(2 + g.rng.Intn(9))
	step := strconv.Itoa(1 + g.rng.Intn(4))
	var d strings.Builder
	d.WriteString("func acc(int n): int {\n")
	d.WriteString("  int total = 0;\n")
	d.WriteString("  for (i in 0..<n) {\n")
	d.WriteString("    if (i % 3 == 1) { continue; }\n")
	d.WriteString("    if (i > " + limit + ") { break; }\n")
	d.WriteString("    total += (i * " + step + ");\n")
	d.WriteString("  }\n")
	d.WriteString("  return total;\n")
	d.WriteString("}\n")
	d.WriteString("func wloop(int n): string {\n")
	d.WriteString("  string out = \"\";\n")
	d.WriteString("  int i = n;\n")
	d.WriteString("  while (i > 0) {\n")
	d.WriteString("    out += (i as string);\n")
	d.WriteString("    i -= 1;\n")
	d.WriteString("  }\n")
	d.WriteString("  return out;\n")
	d.WriteString("}\n")
	calls = append(calls,
		"io.println(acc("+strconv.Itoa(g.rng.Intn(14))+"));",
		"io.println(wloop("+strconv.Itoa(1+g.rng.Intn(5))+"));",
		"let lxs = [3, 1, 2]; lxs[1] = lxs[1] + "+g.intLit()+"; io.println(\"${lxs.sorted()}\");",
		"let ldd = {\"k\": "+g.intLit()+"}; ldd[\"k\"] = (ldd[\"k\"] as int) * 2; io.println(ldd[\"k\"]);",
	)
	return d.String(), calls
}

func (g *fuzzGen) program() string {
	var b strings.Builder
	b.WriteString("import io;\n")
	// Pick at most one structural section per program so the generated
	// declarations cannot collide; the rest of the budget is scalar
	// expressions and fault statements.
	var sectionCalls []string
	switch g.rng.Intn(5) {
	case 0:
		decls, calls := g.classBlock()
		b.WriteString(decls)
		sectionCalls = calls
	case 1:
		decls, calls := g.signatureBlock()
		b.WriteString(decls)
		sectionCalls = calls
	case 2:
		decls, calls := g.generatorBlock()
		b.WriteString(decls)
		sectionCalls = calls
	case 3:
		decls, calls := g.loopBlock()
		b.WriteString(decls)
		sectionCalls = calls
	}
	stmts := 3 + g.rng.Intn(5)
	for i := 0; i < stmts; i++ {
		if i < len(sectionCalls) {
			b.WriteString(sectionCalls[i])
		} else if g.rng.Intn(3) == 0 {
			b.WriteString(g.faultStmt())
		} else {
			t := fuzzScalarTypes[g.rng.Intn(len(fuzzScalarTypes))]
			b.WriteString("io.println(" + g.expr(t, 3) + ");")
		}
		b.WriteByte('\n')
	}
	// Ensure all section calls run even if the statement budget was small.
	for i := stmts; i < len(sectionCalls); i++ {
		b.WriteString(sectionCalls[i])
		b.WriteByte('\n')
	}
	return b.String()
}

// uncaughtProgram emits a program whose final statement throws or faults
// with nothing catching it; the rendered uncaught output (header + frame
// lines) is a guarded parity surface since 1.19.0.
func (g *fuzzGen) uncaughtProgram() string {
	var b strings.Builder
	b.WriteString("import io;\n")
	rt := func(n int) string { return "(\"" + strconv.Itoa(n) + "\".toInt())" }
	msg := g.stringLit()
	switch g.rng.Intn(7) {
	case 0: // plain call chain
		b.WriteString("func inner(int x): int { throw ValueError(" + msg + "); }\n")
		b.WriteString("func middle(int x): int { let r = inner(x + 1); return r; }\n")
		b.WriteString("io.println(\"pre\");\n")
		b.WriteString("io.println(middle(" + g.intLit() + "));\n")
	case 1: // method throw
		b.WriteString("class Boomer {\n  func work(int x): int { throw ValueError(" + msg + "); }\n}\n")
		b.WriteString("io.println(Boomer().work(" + g.intLit() + "));\n")
	case 2: // self tail recursion (collapse path)
		depth := 2 + g.rng.Intn(40)
		b.WriteString("func down(int n): int {\n  if (n == 0) { throw ValueError(" + msg + "); }\n  return down(n - 1);\n}\n")
		b.WriteString("io.println(down(" + strconv.Itoa(depth) + "));\n")
	case 3: // deferred throw
		b.WriteString("func explode() { throw ValueError(" + msg + "); }\n")
		b.WriteString("func run() {\n  defer explode();\n  io.println(\"body\");\n}\n")
		b.WriteString("run();\n")
	case 4: // runtime fault, uncaught (runtime operands defeat constant folding)
		if g.rng.Intn(2) == 0 {
			b.WriteString("let xs = [" + g.intLit() + ", " + g.intLit() + "];\n")
			b.WriteString("io.println(xs[" + rt(7+g.rng.Intn(90)) + "]);\n")
		} else {
			b.WriteString("io.println(" + rt(1+g.rng.Intn(99)) + " // " + rt(0) + ");\n")
		}
	case 5: // caught then rethrown, escaping at top level
		b.WriteString("func origin() { throw ValueError(" + msg + "); }\n")
		b.WriteString("func relay() {\n  try { origin(); } catch (ValueError e) { throw e; }\n}\n")
		b.WriteString("relay();\n")
	default: // closure throw
		b.WriteString("let f = func(int x): int { throw ValueError(" + msg + "); };\n")
		b.WriteString("io.println(f(" + g.intLit() + "));\n")
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
		wantUncaught := i%4 == 3
		var src string
		if wantUncaught {
			src = g.uncaughtProgram()
		} else {
			src = g.program()
		}
		evOut, evErr, vmOut, vmErr, compileErr := fuzzRunBoth(src)
		if compileErr != nil {
			t.Fatalf("generated program did not compile (seed %d, iter %d): %v\n%s", seed, i, compileErr, src)
		}
		if wantUncaught && evErr == nil {
			t.Fatalf("uncaught program did not fail (seed %d, iter %d)\n%s", seed, i, src)
		}
		if (evErr == nil) != (vmErr == nil) {
			t.Fatalf("PARITY: success/failure divergence (seed %d, iter %d)\nprogram:\n%s\neval err: %v\nvm err: %v\neval out: %q\nvm out: %q",
				seed, i, src, evErr, vmErr, evOut, vmOut)
		}
		if evErr != nil && vmErr != nil && evErr.Error() != vmErr.Error() {
			t.Fatalf("PARITY: uncaught-render divergence (seed %d, iter %d)\nprogram:\n%s\n--- eval ---\n%s\n--- vm ---\n%s",
				seed, i, src, evErr.Error(), vmErr.Error())
		}
		if evOut != vmOut {
			t.Fatalf("PARITY: stdout divergence (seed %d, iter %d)\nprogram:\n%s\neval: %q\nvm:   %q",
				seed, i, src, evOut, vmOut)
		}
	}
}
