package bytecode_test

import (
	"strings"
	"testing"
)

// TestParityRuntimeFaultsCatchable proves that implicit runtime faults
// (division by zero, index out of range, conversion failure) are
// catchable as RuntimeError on BOTH backends. Before the error-model
// fix the VM let these escape try/catch and crash.
func TestParityRuntimeFaultsCatchable(t *testing.T) {
	runParity(t, `import io;
func catch_(callable f): string {
    try { f(); } catch (Error e) { return e.class; }
    return "no-throw";
}
io.println(catch_(func(): void { let x = 1 / 0; }));
io.println(catch_(func(): void { let xs = [1, 2]; let y = xs[9]; }));
io.println(catch_(func(): void { let n = "abc".toInt(); }));
`, "RuntimeError\nRuntimeError\nRuntimeError\n")
}

// TestParityFatalErrorUncatchable proves a FatalError bypasses every
// try/catch (even catch(any)) on both backends and propagates to the
// top as an uncaught fault.
func TestParityFatalErrorUncatchable(t *testing.T) {
	src := `import io;
try {
    throw FatalError("unrecoverable");
} catch (any e) {
    io.println("WRONGLY CAUGHT");
}
io.println("AFTER");
`
	evalErr := runOnEvaluator(src)
	vmErr := runOnVM(src)
	if evalErr == nil || !strings.Contains(evalErr.Error(), "FatalError") {
		t.Errorf("evaluator: FatalError should be uncaught, got %v", evalErr)
	}
	if vmErr == nil || !strings.Contains(vmErr.Error(), "FatalError") {
		t.Errorf("VM: FatalError should be uncaught, got %v", vmErr)
	}
}

// TestParityErrorInstanceOf proves instanceof resolves the built-in
// error hierarchy identically on both backends: a built-in error is an
// Error, FatalError is not, and subclass relationships hold.
func TestParityErrorInstanceOf(t *testing.T) {
	runParity(t, `import io;
io.println("${RuntimeError("x") instanceof Error}");
io.println("${ValueError("x") instanceof Error}");
io.println("${RuntimeError("x") instanceof RuntimeError}");
io.println("${RuntimeError("x") instanceof ValueError}");
io.println("${FatalError("x") instanceof Error}");
io.println("${FatalError("x") instanceof FatalError}");
`, "true\ntrue\ntrue\nfalse\nfalse\ntrue\n")
}

// TestParityMonotonicClock proves time.monotonic never decreases across
// a sleep on both backends (the wall clock can, which broke TTLs).
func TestParityMonotonicClock(t *testing.T) {
	runParity(t, `import io; import time;
let a = time.monotonic();
time.sleep(5);
let b = time.monotonic();
io.println("${typeof(a)}");
io.println("${b >= a}");
`, "int\ntrue\n")
}
