package lower_test

import (
	"strings"
	"testing"

	"geblang/internal/transpiler/lower"
)

// A supported conversion method lowers to the transpilert helper and pins the
// result type so a declaration consuming it type-checks.
func TestStringToIntLowers(t *testing.T) {
	src := `import io;
string s = "42";
int n = s.toInt();
io.println(n);
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	if !strings.Contains(got, "transpilert.StringToInt(s)") {
		t.Fatalf("expected transpilert.StringToInt, got:\n%s", got)
	}
	if strings.Contains(got, "s.toInt") {
		t.Fatalf("raw Go conversion leaked:\n%s", got)
	}
}

func TestNumericToStringLowers(t *testing.T) {
	src := `import io;
int x = 7;
io.println(x.toString());
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	if !strings.Contains(string(l.Module.Render()), "transpilert.IntToString(x)") {
		t.Fatalf("expected transpilert.IntToString:\n%s", l.Module.Render())
	}
}

// collections.filter/map/reduce module-call form routes to the same helpers as
// the method form.
func TestCollectionsFreeFnLowers(t *testing.T) {
	src := `import io;
import collections;
list<int> xs = [1, 2, 3, 4];
let ys = collections.filter(xs, func(int v): bool { return v > 1; });
let zs = collections.map(ys, func(int v): int { return v * 2; });
int total = collections.reduce(zs, func(int a, int b): int { return a + b; }, 0);
io.println(total);
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	for _, want := range []string{"transpilert.Filter(xs,", "transpilert.Map(ys,", "transpilert.Reduce(zs,"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output:\n%s", want, got)
		}
	}
	if strings.Contains(got, "collections_") {
		t.Fatalf("collections free fn mangled as a user-module call:\n%s", got)
	}
}

// An unlowered primitive method on a known receiver kind diagnoses rather than
// emitting raw Go.
func TestUnsupportedPrimitiveMethodDiagnoses(t *testing.T) {
	src := `import io;
string s = "hello world foo";
io.println(s.codePointAt(0));
`
	l := lowerSource(t, src)
	errs := l.Errors()
	if !diagnosed(errs, "does not yet support method", "codePointAt") {
		t.Fatalf("expected diagnostic for codePointAt, got: %v", errs)
	}
	if strings.Contains(string(l.Module.Render()), "s.codePointAt") {
		t.Fatalf("raw Go leaked for unsupported method:\n%s", l.Module.Render())
	}
}

// Untagged-enum methods + interface implementation now lower to Go methods on
// the int type; methods on a TAGGED enum still diagnose (its variants lower to
// distinct structs behind an interface, where dispatch needs later-phase work).
func TestTaggedEnumMethodsDiagnose(t *testing.T) {
	src := `enum Status {
	Active, Closed(string);
	func describe(): string { return match (this) { case Status.Active => "a"; default => "c"; }; }
}
`
	l := lowerSource(t, src)
	if !diagnosed(l.Errors(), "tagged enum") {
		t.Fatalf("expected tagged-enum-methods diagnostic, got: %v", l.Errors())
	}
}

// An unlowered property on a collection receiver (list.length without parens)
// diagnoses rather than emitting an invalid Go field access.
func TestUnsupportedListPropertyDiagnoses(t *testing.T) {
	src := `import io;
list<int> xs = [1, 2, 3];
io.println(xs.length);
`
	l := lowerSource(t, src)
	errs := l.Errors()
	if !diagnosed(errs, "does not support property", "length") {
		t.Fatalf("expected property diagnostic, got: %v", errs)
	}
	if strings.Contains(string(l.Module.Render()), "xs.length") {
		t.Fatalf("raw Go leaked for list property:\n%s", l.Module.Render())
	}
}

func diagnosed(errs []lower.Error, parts ...string) bool {
	for _, e := range errs {
		ok := true
		for _, p := range parts {
			if !strings.Contains(e.Message, p) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
