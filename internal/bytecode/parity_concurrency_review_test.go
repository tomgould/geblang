package bytecode_test

import "testing"

// Concurrent cross-module constructors + a method on each new instance, each on its own exclusive worker (run under the engine -race gate).
func TestParityConcurrentCrossModuleConstructors(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"pt": "module pt;\nexport class Point {\n    int x;\n    func Point(int x) { this.x = x; }\n    func get(): int { return this.x; }\n}\n",
	}, `import io;
import async.tasks as task;
import pt;
let out = task.map([1, 2, 3, 4, 5, 6, 7, 8], func(int n): int {
    let p = pt.Point(n);
    return p.get();
}, {"concurrency": 4});
io.println(out);
`, "[1, 2, 3, 4, 5, 6, 7, 8]\n")
}

// Concurrent cross-module static methods.
func TestParityConcurrentCrossModuleStatics(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"calc": "module calc;\nexport class Calc {\n    static func sq(int n): int { return n * n; }\n}\n",
	}, `import io;
import async.tasks as task;
import calc;
let out = task.map([1, 2, 3, 4, 5, 6], func(int n): int {
    return calc.Calc.sq(n);
}, {"concurrency": 4});
io.println(out);
`, "[1, 4, 9, 16, 25, 36]\n")
}

// Concurrent cross-module closures with a captured cell, invoked per task.
func TestParityConcurrentCrossModuleClosures(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"mk": "module mk;\nexport func adder(int base): func {\n    return func(int x): int { return base + x; };\n}\n",
	}, `import io;
import async.tasks as task;
import mk;
let out = task.map([1, 2, 3, 4, 5, 6, 7, 8], func(int n): int {
    let add = mk.adder(n);
    return add(10);
}, {"concurrency": 4});
io.println(out);
`, "[11, 12, 13, 14, 15, 16, 17, 18]\n")
}

// #6 tail: a cross-module generator stepped manually (partial consume + close) and one that throws mid-iteration (caught).
func TestParityCrossModuleGeneratorStress(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"gen": "module gen;\nexport func count(int n): generator<int> {\n    let i = 1;\n    while (i <= n) { yield i; i = i + 1; }\n}\nexport func boom(int n): generator<int> {\n    let i = 1;\n    while (true) {\n        if (i > n) { throw RuntimeError(\"gen boom\"); }\n        yield i;\n        i = i + 1;\n    }\n}\n",
	}, `import io;
import gen;
let g = gen.count(5);
io.println(g.next());
io.println(g.next());
io.println(g.done());
g.close();
try {
    for (x in gen.boom(2)) { io.println(x); }
} catch (Error e) {
    io.println("caught: " + e.message);
}
`, "1\n2\nfalse\n1\n2\ncaught: gen boom\n")
}

// #6 tail: a cross-module function that throws inside an async task surfaces on await.
func TestParityCrossModuleAsyncThrow(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"calc": "module calc;\nexport func risky(int n): int {\n    if (n < 0) { throw RuntimeError(\"negative\"); }\n    return n * 2;\n}\n",
	}, `import io;
import async;
import calc;
let task = async.run(func(): int { return calc.risky(-1); });
try {
    io.println(async.await(task));
} catch (Error e) {
    io.println("caught: " + e.message);
}
io.println(async.await(async.run(func(): int { return calc.risky(5); })));
`, "caught: negative\n10\n")
}

// #6 tail: an async task whose cross-module call nests another cross-module call.
func TestParityCrossModuleAsyncNestedCallback(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"a": "module a;\nimport b;\nexport func compute(int n): int { return b.dbl(n) + 1; }\n",
		"b": "module b;\nexport func dbl(int n): int { return n * 2; }\n",
	}, `import io;
import async;
import a;
io.println(async.await(async.run(func(): int { return a.compute(5); })));
`, "11\n")
}

// High-concurrency cross-module calls saturate one module's worker pool.
func TestParityConcurrentPoolSaturation(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"svc": "module svc;\nexport func work(int n): int { return n * 2; }\n",
	}, `import io;
import async.tasks as task;
import svc;
let xs = [];
for (int i = 0; i < 64; i++) { xs = xs.push(i); }
let out = task.map(xs, func(int n): int { return svc.work(n); }, {"concurrency": 16});
let total = 0;
for (v in out) { total = total + (v as int); }
io.println(total);
`, "4032\n")
}

// Followup adversarial: a cross-module async function call returns a Task (not the raw value) on both backends - asserts the Task type before awaiting, the dimension that let the sync-dispatch divergence ship.
func TestParityCrossModuleAsyncReturnsTask(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"asyncmod": "module asyncmod;\nexport async func work(int x): int { return x * 2; }\n",
	}, `import io;
import async;
import asyncmod;
let t = asyncmod.work(5);
io.println(typeof(t));
io.println(async.await(t));
let pair = [asyncmod.work(1), asyncmod.work(2)];
io.println(async.await(pair[0]) + async.await(pair[1]));
`, "Task\n10\n6\n")
}

// Followup adversarial: an async function invoked as a value (let f = work; f(x)) or passed as a callback yields a Task on both backends - the same-module closure __invoke path ran the body synchronously on the VM.
func TestParityAsyncFunctionAsValueReturnsTask(t *testing.T) {
	runParity(t, `import io;
import async;
async func work(int x): int { return x * 3; }
func apply(callable f, int v): any { return f(v); }
let f = work;
io.println(typeof(f(4)));
io.println(typeof(apply(work, 5)));
io.println(async.await(f(4)) + async.await(apply(work, 5)));
`, "Task\nTask\n27\n")
}
