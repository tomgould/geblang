package bytecode_test

import "testing"

// TestParityNamedArgsToNatives: named arguments bind to a native's declared parameter names in any order, identically on both backends, and unknown names error identically.
func TestParityNamedArgsToNatives(t *testing.T) {
	runParity(t, `import io;
import math;
io.println(math.pow(base: 2.0, exponent: 3.0));
io.println(math.pow(exponent: 3.0, base: 2.0));
io.println(math.max(right: 5, left: 3));
let r = "ok";
try { math.pow(base: 2.0, zzz: 3.0); } catch (Error e) { r = "${e}"; }
io.println(r);
`, "8\n8\n5\nRuntimeError: pow has no parameter zzz\n")
}
