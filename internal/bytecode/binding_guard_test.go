package bytecode_test

import (
	"fmt"
	"strings"
	"testing"
)

// Argument-binding guard (the B1-family drift defense). One table
// covering signature shapes x call shapes x dispatch contexts, run on
// BOTH backends:
//
//   - valid cells assert identical stdout;
//   - invalid cells in dynamic contexts (callable values) assert both
//     backends fail catchably with the same error class;
//   - invalid cells in static contexts assert both backends reject the
//     program (the VM at compile time, the evaluator at analysis or
//     run time - the static-analysis contract makes early rejection
//     authoritative).
//
// With the shared binder (internal/binding) consumed by all three
// implementations, dynamic invalid cells assert full message parity:
// both backends must produce the identical canonical error text.

type bindingSig struct {
	name   string
	params string // parameter list
	expr   string // result expression over a/b/rest
}

var bindingSigs = []bindingSig{
	{"fixed", "int a", `"${a}"`},
	{"defaulted", "int a, int b = 5", `"${a}|${b}"`},
	{"variadic", "int a, int ...rest", `"${a}|${rest}"`},
	{"defvar", "int a, int b = 5, int ...rest", `"${a}|${b}|${rest}"`},
}

// expected results per signature; "" means the call is invalid for
// that signature.
type bindingCall struct {
	name string
	args string
	want map[string]string
}

var bindingCalls = []bindingCall{
	{"empty", "()", map[string]string{}},
	{"one", "(1)", map[string]string{
		"fixed": "1", "defaulted": "1|5", "variadic": "1|[]", "defvar": "1|5|[]"}},
	{"two", "(1, 2)", map[string]string{
		"defaulted": "1|2", "variadic": "1|[2]", "defvar": "1|2|[]"}},
	{"three", "(1, 2, 3)", map[string]string{
		"variadic": "1|[2, 3]", "defvar": "1|2|[3]"}},
	{"namedReorder", "(b: 9, a: 1)", map[string]string{
		"defaulted": "1|9", "defvar": "1|9|[]"}},
	{"mixedNamed", "(1, b: 9)", map[string]string{
		"defaulted": "1|9", "defvar": "1|9|[]"}},
	{"namedOnly", "(a: 1)", map[string]string{
		"fixed": "1", "defaulted": "1|5", "variadic": "1|[]", "defvar": "1|5|[]"}},
	{"listSpread", "(...[1, 2])", map[string]string{
		"defaulted": "1|2", "variadic": "1|[2]", "defvar": "1|2|[]"}},
	// Dict spread binds matching parameter names and silently ignores
	// extra keys on BOTH backends (unlike an explicit unknown named
	// argument, which errors). Pinned as current behaviour; whether the
	// leniency is intended is an open design question.
	{"dictSpread", `(...{"a": 1, "b": 9})`, map[string]string{
		"fixed": "1", "defaulted": "1|9", "variadic": "1|[]", "defvar": "1|9|[]"}},
	{"unknownName", "(c: 1)", map[string]string{}},
	{"duplicate", "(1, a: 2)", map[string]string{}},
	// Positional after named fills the next unassigned slot, and named
	// matching is case-insensitive - the evaluator diverged on both
	// until it consumed the shared binder.
	{"positionalAfterNamed", "(b: 9, 1)", map[string]string{
		"defaulted": "1|9", "defvar": "1|9|[]"}},
	{"namedCaseInsensitive", "(A: 1)", map[string]string{
		"fixed": "1", "defaulted": "1|5", "variadic": "1|[]", "defvar": "1|5|[]"}},
}

type bindingContext struct {
	name string
	// static contexts are rejected before execution when the call is
	// invalid; dynamic contexts must fail catchably at runtime.
	static  bool
	program func(sig bindingSig, args string, catch bool) string
}

func wrapCatch(call string, catch bool) string {
	if catch {
		return "try { io.println(" + call + `); } catch (Error e) { io.println("CAUGHT " + e.class + ": " + e.message); }`
	}
	return "io.println(" + call + ");"
}

var bindingContexts = []bindingContext{
	{"directFunc", true, func(s bindingSig, args string, catch bool) string {
		return "import io;\nfunc f(" + s.params + "): string { return " + s.expr + "; }\n" + wrapCatch("f"+args, catch)
	}},
	{"funcValue", false, func(s bindingSig, args string, catch bool) string {
		return "import io;\nfunc f(" + s.params + "): string { return " + s.expr + "; }\nlet g = f;\n" + wrapCatch("g"+args, catch)
	}},
	{"lambdaValue", false, func(s bindingSig, args string, catch bool) string {
		return "import io;\nlet g = func(" + s.params + "): string { return " + s.expr + "; };\n" + wrapCatch("g"+args, catch)
	}},
	{"method", true, func(s bindingSig, args string, catch bool) string {
		return "import io;\nclass K {\n  func m(" + s.params + "): string { return " + s.expr + "; }\n}\n" + wrapCatch("K().m"+args, catch)
	}},
	{"staticMethod", true, func(s bindingSig, args string, catch bool) string {
		return "import io;\nclass K {\n  static func m(" + s.params + "): string { return " + s.expr + "; }\n}\n" + wrapCatch("K.m"+args, catch)
	}},
	{"constructor", true, func(s bindingSig, args string, catch bool) string {
		return "import io;\nclass K {\n  string out;\n  func K(" + s.params + ") { this.out = " + s.expr + "; }\n}\n" + wrapCatch("K"+args+".out", catch)
	}},
}

func TestBindingGuardMatrix(t *testing.T) {
	for _, ctx := range bindingContexts {
		for _, sig := range bindingSigs {
			for _, call := range bindingCalls {
				name := fmt.Sprintf("%s/%s/%s", ctx.name, sig.name, call.name)
				want, valid := call.want[sig.name]
				t.Run(name, func(t *testing.T) {
					src := ctx.program(sig, call.args, !ctx.static)
					evOut, evErr, vmOut, vmErr, compileErr := fuzzRunBoth(src)
					if valid {
						if compileErr != nil {
							t.Fatalf("valid call failed to compile: %v\n%s", compileErr, src)
						}
						if evErr != nil || vmErr != nil {
							t.Fatalf("valid call errored (eval: %v, vm: %v)\n%s", evErr, vmErr, src)
						}
						expect := want + "\n"
						if evOut != expect || vmOut != expect {
							t.Fatalf("output mismatch: eval %q vm %q want %q\n%s", evOut, vmOut, expect, src)
						}
						return
					}
					if ctx.static {
						// Static contexts: at least one stage must reject on
						// each backend, and the call must never succeed.
						vmRejected := compileErr != nil || vmErr != nil
						evRejected := compileErr != nil || evErr != nil
						if !vmRejected || !evRejected {
							t.Fatalf("invalid call not rejected (eval out %q err %v, vm out %q err %v)\n%s",
								evOut, evErr, vmOut, vmErr, src)
						}
						return
					}
					// Dynamic contexts: both backends fail catchably with
					// the same class, printed by the catch arm.
					if compileErr != nil {
						t.Fatalf("dynamic invalid call must compile (got %v)\n%s", compileErr, src)
					}
					if evErr != nil || vmErr != nil {
						t.Fatalf("dynamic invalid call must be catchable (eval: %v, vm: %v)\n%s", evErr, vmErr, src)
					}
					if !strings.HasPrefix(evOut, "CAUGHT ") || !strings.HasPrefix(vmOut, "CAUGHT ") {
						t.Fatalf("dynamic invalid call not caught: eval %q vm %q\n%s", evOut, vmOut, src)
					}
					if evOut != vmOut {
						t.Fatalf("caught error diverges: eval %q vm %q\n%s", evOut, vmOut, src)
					}
				})
			}
		}
	}
}
