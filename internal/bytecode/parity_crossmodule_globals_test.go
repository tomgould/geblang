package bytecode_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/runtime"
)

// Donor module-global persists across cross-module calls, byte-identical on both backends.
func TestParityCrossModuleModuleGlobalPersists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "counter.gb"), []byte(`module counter;
let total = 0;
export func add(int n): int {
    total = total + n;
    return total;
}
export func current(): int {
    return total;
}
`), 0o644); err != nil {
		t.Fatalf("write counter module: %v", err)
	}

	source := `import io;
import counter;

io.println(counter.add(2));
io.println(counter.add(3));
io.println(counter.current());
`

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	want := "2\n5\n5\n"

	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("evaluator: unexpected error: %v", err)
	}
	if evOut.String() != want {
		t.Fatalf("evaluator output: got %q, want %q", evOut.String(), want)
	}

	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var vmOut bytes.Buffer
	stateful := evaluator.NewWithArgsAndModulePaths(&vmOut, nil, []string{dir})
	loader := newHarnessLoader(&vmOut, stateful)
	loader.SetModulePaths([]string{dir})
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: unexpected error: %v", err)
	}
	if vmOut.String() != want {
		t.Fatalf("vm output: got %q, want %q", vmOut.String(), want)
	}
}

// Review #6 tail: with no loader, an instance method wrapper closes over the VM, so the VM must not be recyclable while the instance can still call it (the data-only wrapper's fallback path).
func TestNoLoaderInstanceWrapperEscapes(t *testing.T) {
	src := `class Box {
    int v;
    func Box(int v) { this.v = v; }
    func get(): int { return this.v; }
}
import io;
let b = Box(7);
io.println(b.get());
`
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	chunk, err := bytecode.Compile(program, []byte(src), "parity")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var out bytes.Buffer
	vm := bytecode.NewVM(chunk, &out)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: %v", err)
	}
	if vm.Recyclable() {
		t.Fatal("a no-loader VM whose instance method wrapper escaped should not be recyclable")
	}
}

// Followup #3: a write-only module-global assignment (no read) through a cross-module call persists, matching the evaluator (the OpSetGlobal fast path must still mark the slot dirty for write-back).
func TestParityCrossModuleGlobalPersistsWriteOnly(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"counter": "module counter;\nlet total = 0;\nexport func setTo(int n): void { total = n; }\nexport func current(): int { return total; }\n",
	}, `import io;
import counter;
counter.setTo(7);
io.println(counter.current());
`, "7\n")
}

// Followup #3 part 2 regression guard: a throw from a synchronous re-entrant call must unwind to the outer call's handler (not corrupt the shared worker's exception-handler stack) - caught in the outer fn, caught at top level, and re-entry across modules.
func TestParityCrossModuleReentryThrow(t *testing.T) {
	mods := map[string]string{
		"c6": "module c6;\nlet n = 0;\nexport func reset(): void { n = 0; }\nexport func current(): int { return n; }\nexport func outerCatches(callable cb): int {\n    n = 1;\n    try { cb(); } catch (Error e) { n = n + 10; }\n    return n;\n}\nexport func outerNoCatch(callable cb): void { n = 1; cb(); }\nexport func bumpThrows(): void { n = n + 100; throw RuntimeError(\"x\"); }\n",
	}
	// Caught inside the outer module function (n: 1 -> bump 101 -> catch +10 = 111).
	runMultiModuleParity(t, mods, `import io;
import c6;
io.println(c6.outerCatches(func(): void { c6.bumpThrows(); }));
`, "111\n")
	// Not caught in the module: the throw propagates out of the re-entrant call to the top level, and the pre-throw write persists.
	runMultiModuleParity(t, mods, `import io;
import c6;
c6.reset();
try {
    c6.outerNoCatch(func(): void { c6.bumpThrows(); });
} catch (Error e) {
    io.println("top: " + e.message);
}
io.println(c6.current());
`, "top: x\n101\n")
}

// Followup #3 part 2: synchronous re-entry into a module (host->module->callback->same module) sees the outer call's live module globals, matching the evaluator.
func TestParityCrossModuleGlobalReentry(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"counter": "module counter;\nlet total = 0;\nexport func bump(): void { total = total + 1; }\nexport func current(): int { return total; }\nexport func outerCb(callable cb): int {\n    total = 1;\n    cb();\n    return total;\n}\n",
	}, `import io;
import counter;
let r = counter.outerCb(func(): void { counter.bump(); });
io.println(r);
io.println(counter.current());
`, "2\n2\n")
}

// Re-entry threaded through a bridged callback: a module fn hands a callback to a native higher-order function (collections.map) which invokes it; the callback re-enters the module and shares the in-flight worker's live globals, matching the evaluator.
func TestParityCrossModuleReentryThroughBridgedCallback(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"bc": "module bc;\nimport collections;\nlet total = 0;\nexport func bump(): void { total = total + 1; }\nexport func current(): int { return total; }\nexport func mapBumping(int[] xs): int[] {\n    total = 0;\n    return collections.map(xs, func(int x): int { bump(); return x + total; });\n}\n",
	}, `import io;
import bc;
let out = bc.mapBumping([1, 2, 3]);
io.println(out);
io.println(bc.current());
`, "[2, 4, 6]\n3\n")
}

// Followup #3: a module-global write before a throw persists (matching the evaluator), not rolled back at the worker boundary.
func TestParityCrossModuleGlobalPersistsOnThrow(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"counter": "module counter;\nlet total = 0;\nexport func fail(): void {\n    total = 7;\n    throw RuntimeError(\"boom\");\n}\nexport func current(): int { return total; }\n",
	}, `import io;
import counter;
try {
    counter.fail();
} catch (Error e) { }
io.println(counter.current());
`, "7\n")
}

// Dispatching an already-loaded module while a fresh nested module chain loads is race-free (run under -race). The chain load augments the loader's nested-resolution paths; concurrent dispatch must not read that mutable state.
func TestConcurrentLoadAndDispatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.gb"), []byte("module base;\nexport func ping(): int { return 1; }\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	const chain = 20
	for i := 0; i <= chain; i++ {
		var src string
		if i < chain {
			src = fmt.Sprintf("module m%d;\nimport m%d;\nexport func v(): int { return m%d.v() + 1; }\n", i, i+1, i+1)
		} else {
			src = fmt.Sprintf("module m%d;\nexport func v(): int { return 0; }\n", i)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("m%d.gb", i)), []byte(src), 0o644); err != nil {
			t.Fatalf("write m%d: %v", i, err)
		}
	}

	var out bytes.Buffer
	stateful := evaluator.NewWithArgsAndModulePaths(&out, nil, []string{dir})
	loader := newHarnessLoader(&out, stateful)
	loader.SetModulePaths([]string{dir})

	baseMod, err := loader.LoadModule("base", "base")
	if err != nil {
		t.Fatalf("load base: %v", err)
	}
	ping, ok := baseMod.Exports["ping"].(runtime.BytecodeFunction)
	if !ok {
		t.Fatalf("base.ping is not a bytecode function")
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if _, err := loader.CallModuleFunction(ping, nil, nil); err != nil {
					t.Errorf("dispatch ping: %v", err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := loader.LoadModule("m0", "m0"); err != nil {
			t.Errorf("load chain: %v", err)
		}
	}()
	wg.Wait()
}

// Review #7 tail: a no-loader VM that produces an overloaded function value captures the VM in the value's closure, so the VM must not be recyclable while the value can still call into it.
func TestNoLoaderOverloadedValueEscapes(t *testing.T) {
	src := `import io;
func pick(int x): int { return x; }
func pick(string s): string { return s; }
let g = pick;
io.println(g(7));
`
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	chunk, err := bytecode.Compile(program, []byte(src), "parity")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var out bytes.Buffer
	vm := bytecode.NewVM(chunk, &out)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: %v", err)
	}
	if vm.Recyclable() {
		t.Fatal("a no-loader VM whose overloaded value escaped should not be recyclable")
	}
}

// Concurrent first-loads of the same and different modules are race-free and each module loads exactly once (run under the engine -race gate).
func TestConcurrentLoadModule(t *testing.T) {
	dir := t.TempDir()
	const nMods = 4
	for i := 0; i < nMods; i++ {
		name := "modx" + string(rune('0'+i))
		src := "module " + name + ";\nexport func id(): int { return " + string(rune('0'+i)) + "; }\n"
		if err := os.WriteFile(filepath.Join(dir, name+".gb"), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	var out bytes.Buffer
	stateful := evaluator.NewWithArgsAndModulePaths(&out, nil, []string{dir})
	loader := newHarnessLoader(&out, stateful)
	loader.SetModulePaths([]string{dir})

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "modx" + string(rune('0'+(i%nMods)))
			m, err := loader.LoadModule(name, name)
			if err != nil {
				t.Errorf("LoadModule(%s): %v", name, err)
				return
			}
			if m == nil {
				t.Errorf("LoadModule(%s): nil module", name)
			}
		}(i)
	}
	wg.Wait()
}

// Concurrent cross-module module-global writes are race-free (run under the engine -race gate). Contended writes have lost-update semantics, so the final count is only bounded by [1, N], not exact.
func TestCrossModuleGlobalConcurrentWriteback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "counter.gb"), []byte(`module counter;
let total = 0;
export func add(int n): int {
    total = total + n;
    return total;
}
export func current(): int {
    return total;
}
`), 0o644); err != nil {
		t.Fatalf("write counter module: %v", err)
	}

	var out bytes.Buffer
	stateful := evaluator.NewWithArgsAndModulePaths(&out, nil, []string{dir})
	loader := newHarnessLoader(&out, stateful)
	loader.SetModulePaths([]string{dir})
	module, err := loader.LoadModule("counter", "counter")
	if err != nil {
		t.Fatalf("load counter: %v", err)
	}
	add, ok := module.Exports["add"].(runtime.BytecodeFunction)
	if !ok {
		t.Fatalf("counter.add is not a bytecode function")
	}
	current, ok := module.Exports["current"].(runtime.BytecodeFunction)
	if !ok {
		t.Fatalf("counter.current is not a bytecode function")
	}

	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := loader.CallModuleFunction(add, []runtime.Value{runtime.SmallInt{Value: 1}}, nil); err != nil {
				t.Errorf("concurrent add: %v", err)
			}
		}()
	}
	wg.Wait()

	result, err := loader.CallModuleFunction(current, nil, nil)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	final, ok := result.(runtime.SmallInt)
	if !ok {
		t.Fatalf("current returned %T, want SmallInt", result)
	}
	if final.Value < 1 || final.Value > int64(n) {
		t.Fatalf("concurrent counter = %d, want within [1, %d] (lost-update bounded)", final.Value, n)
	}
}

// Followup adversarial: an overloaded callback with a module-qualified parameter type (animals.Animal) matches a same-module subclass arg on both backends (the VM's overload.Select previously lowercased the whole qualified name and never matched).
func TestParityCrossModuleOverloadQualified(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"animals": "module animals;\nexport class Animal {}\nexport class Dog extends Animal {}\n",
	}, `import io;
import animals;
func choose(animals.Animal a): string { return "animal"; }
func choose(string s): string { return "string"; }
let f = choose;
io.println(f(animals.Dog()));
io.println(f("hi"));
`, "animal\nstring\n")
}

// Followup adversarial cross-module hierarchy cluster: instanceof against a cross-module parent, an overloaded callback with a cross-module-parent param matching a subclass arg, and an inherited cross-module __serialize all behave identically on both backends (the runtime Class.Parent chain must cross the module boundary).
func TestParityCrossModuleHierarchy(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"base":    "module base;\nexport class Shape { func kind(): string { return \"shape\"; } }\n",
		"derived": "module derived;\nimport base;\nexport class Circle extends base.Shape { }\n",
	}, `import io;
import base;
import derived;
let c = derived.Circle();
io.println(c instanceof base.Shape);
func pick(base.Shape s): string { return "shape"; }
func pick(int n): string { return "int"; }
let f = pick;
io.println(f(c));
io.println(c.kind());
`, "true\nshape\nshape\n")

	runMultiModuleParity(t, map[string]string{
		"serbase":    "module serbase;\nexport class Entity {\n    int id;\n    func Entity(int id) { this.id = id; }\n    func __serialize(): dict<string, any> { return {\"entity_id\": this.id}; }\n}\n",
		"serderived": "module serderived;\nimport serbase;\nexport class User extends serbase.Entity {\n    string name;\n    func User(int id, string name) { parent(id); this.name = name; }\n}\n",
	}, `import io;
import json;
import serderived;
io.println(json.stringify(serderived.User(5, "alice")));
`, "{\"entity_id\":5}\n")
}

// Followup adversarial: a cross-module callee's defer runs when its throw propagates out to the caller (the VM previously dropped defers on the uncaught-propagate path), identically to the evaluator. Also covers a deferred write persisting.
func TestParityCrossModuleDeferOnThrow(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"dfm": "module dfm;\nimport io;\nlet total = 0;\nexport func current(): int { return total; }\nfunc cleanup(): void { io.println(\"CLEANUP\"); total = total + 50; }\nexport func withDefer(): void { defer cleanup(); total = 1; throw RuntimeError(\"boom\"); }\n",
	}, `import io;
import dfm;
try { dfm.withDefer(); } catch (Error e) { io.println("caught"); }
io.println(dfm.current());
`, "CLEANUP\ncaught\n51\n")
}

// Followup adversarial: a cross-module generator that mutates a module global writes the mutation back when consumed, matching the evaluator (the generator runs on an isolated worker whose dirty globals are now persisted on completion).
func TestParityCrossModuleGeneratorGlobalWriteback(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"gen": "module gen;\nlet seen = 0;\nexport func count(int n): generator<int> {\n    let i = 0;\n    while (i < n) { seen = seen + 1; yield i; i = i + 1; }\n}\nexport func seenCount(): int { return seen; }\n",
	}, `import io;
import gen;
let total = 0;
for (x in gen.count(5)) { total = total + x; }
io.println(total);
io.println(gen.seenCount());
`, "10\n5\n")
}
