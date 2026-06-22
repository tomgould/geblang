package bytecode_test

import "testing"

func TestParityPartialPositional(t *testing.T) {
	runParity(t, `import io;
func add(int a, int b): int { return a + b; }
let add10 = add(_, 10);
io.println(add10(3));
io.println(add(1, _)(9));
`, "13\n10\n")
}

func TestParityPartialNamedHole(t *testing.T) {
	runParity(t, `import io;
func tag(string label, string body): string { return "[" + label + "]" + body; }
let wrapBody = tag(label: "x", body: _);
io.println(wrapBody("hi"));
`, "[x]hi\n")
}

func TestParityPartialMultipleHoles(t *testing.T) {
	runParity(t, `import io;
func wrap(string a, string b, string c): string { return a + b + c; }
let f = wrap(_, "-", _);
io.println(f("L", "R"));
`, "L-R\n")
}

func TestParityPartialGenericTypeArgs(t *testing.T) {
	runParity(t, `import io;
func pick<T>(T a, T b): T { return a; }
let f = pick<int>(_, 99);
io.println(f(5));
`, "5\n")
}

func TestParityPartialConstructorAndMethod(t *testing.T) {
	runParity(t, `import io;
class Box { int v; func Box(int a, int b) { this.v = a + b; } }
class Calc { int base; func Calc(int b) { this.base = b; } func addTo(int x, int y): int { return this.base + x + y; } }
let make = Box(10, _);
io.println(make(3).v);
let c = Calc(100);
let f = c.addTo(_, 1);
io.println(f(5));
`, "13\n106\n")
}

func TestParityPartialStaticMethod(t *testing.T) {
	runParity(t, `import io;
class Calc { static func mul(int a, int b): int { return a * b; } }
let triple = Calc.mul(3, _);
io.println(triple(7));
`, "21\n")
}

func TestParityPartialInvokeObject(t *testing.T) {
	runParity(t, `import io;
class Greeter {
    string prefix;
    func Greeter(string p) { this.prefix = p; }
    func __invoke(string a, string b): string { return this.prefix + a + b; }
}
let g = Greeter(">");
let f = g(_, "!");
io.println(f("hi"));
`, ">hi!\n")
}

func TestParityPartialNativeBuiltin(t *testing.T) {
	runParity(t, `import io; import math;
let atLeast0 = math.max(0, _);
io.println(atLeast0(5));
io.println(atLeast0(-2));
`, "5\n0\n")
}

func TestParityPartialEagerCaptureRunsOnce(t *testing.T) {
	runParity(t, `import io;
func add(int a, int b): int { return a + b; }
int calls = 0;
let bump = func(): int { calls = calls + 1; return calls; };
let f = add(bump(), _);
io.println(calls);
f(10);
f(20);
io.println(calls);
`, "1\n1\n")
}

func TestParityPartialCalleeResolvedAtApplication(t *testing.T) {
	runParity(t, `import io;
func add(int a, int b): int { return a + b; }
let g = add;
let p = g(_, 1);
g = func(int a, int b): int { return a * b; };
io.println(p(2));
`, "2\n")
}
