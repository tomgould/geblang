package lower_test

import (
	"strings"
	"testing"

	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/transpiler/lower"
	"geblang/internal/transpiler/types"
)

func lowerSource(t *testing.T, src string) *lower.Lowerer {
	t.Helper()
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parser errors: %v", errs)
	}
	mod := lower.NewModule("main", true, types.IntModeFast)
	l := lower.NewLowerer(mod, lower.NewNativeBridge(), "test.gb")
	l.LowerProgram(prog)
	return l
}

// A user-class instance method's return type is inferred so a HOF on the result
// routes through the runtime instead of emitting a raw Go method call.
func TestClassMethodResultReceiverRoutesHof(t *testing.T) {
	src := `import io;
class Box {
    func Box() {}
    func items(): list<int> { return [1, 2, 3, 4]; }
}
let b = Box();
let ns = b.items();
io.println(ns.reduce(func(int a, int b): int { return a + b; }, 0));
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	if strings.Contains(got, "ns.reduce") {
		t.Fatalf("expected reduce routed through runtime, got raw method call:\n%s", got)
	}
	if !strings.Contains(got, "transpilert.Reduce") {
		t.Fatalf("expected transpilert.Reduce in output:\n%s", got)
	}
}

// The same inference walks an inherited method's declared return type.
func TestInheritedClassMethodResultReceiverRoutesHof(t *testing.T) {
	src := `import io;
class Base {
    func Base() {}
    func items(): list<int> { return [5, 6, 7]; }
}
class Derived extends Base {
    func Derived() { parent(); }
}
let d = Derived();
let xs = d.items();
io.println(xs.map(func(int x): int { return x * 2; }));
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	if strings.Contains(got, "xs.map") {
		t.Fatalf("expected map routed through runtime, got raw method call:\n%s", got)
	}
}

// A method call on a receiver whose type cannot be inferred diagnoses cleanly
// rather than emitting invalid Go that only fails at go build.
func TestUnresolvedMethodReceiverDiagnoses(t *testing.T) {
	src := `import io;
class Wrap {
    func Wrap() {}
    func raw() { return 5; }
}
let w = Wrap();
let r = w.raw();
io.println(r.somethingUnknown());
`
	l := lowerSource(t, src)
	errs := l.Errors()
	if len(errs) == 0 {
		t.Fatalf("expected an unresolved-receiver diagnostic, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "cannot resolve the type of the receiver") &&
			strings.Contains(e.Message, "somethingUnknown") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unresolved-receiver diagnostic for somethingUnknown, got: %v", errs)
	}
}
