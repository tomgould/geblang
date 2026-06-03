package bytecode_test

// Parity tests run each snippet through both the evaluator and the VM and
// assert that the printed output matches.  They pin the intersection of
// features that both execution paths are expected to support identically.

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/native"
	"geblang/internal/parser"
	"geblang/internal/runtime"
	"geblang/internal/semantic"

	"golang.org/x/crypto/ssh"
)

// stdlibModuleLoader is a minimal bytecode-side module loader used by
// parity tests for source-distributed stdlib modules (time.scheduler,
// async.rate, etc.). It resolves canonical module paths via the standard
// resolver (which walks ancestors to find stdlib/), compiles each
// imported source module to bytecode on demand, and caches the result
// inside the loader struct.
type stdlibModuleLoader struct {
	stdout      io.Writer
	stateful    bytecode.StatefulNativeCaller
	modulePaths []string
	modules     map[string]*runtime.Module
	chunks      map[string]bytecode.Chunk
	globals     map[string][]runtime.Value
	decorators  map[string]bytecode.FunctionDecoratorState
	loading     map[string]bool
	// mainChunk lets sub-VMs (stdlib modules) call back into the
	// entry chunk when a foreign instance's method needs dispatch,
	// e.g. streams.copy invoking __read on a user-defined class.
	mainChunk    bytecode.Chunk
	hasMainChunk bool
}

func newStdlibModuleLoader(stdout io.Writer, stateful bytecode.StatefulNativeCaller) *stdlibModuleLoader {
	return &stdlibModuleLoader{
		stdout:     stdout,
		stateful:   stateful,
		modules:    map[string]*runtime.Module{},
		chunks:     map[string]bytecode.Chunk{},
		globals:    map[string][]runtime.Value{},
		decorators: map[string]bytecode.FunctionDecoratorState{},
		loading:    map[string]bool{},
	}
}

func (l *stdlibModuleLoader) lookupBuiltin(canonical, alias string) *runtime.Module {
	if e, ok := l.stateful.(*evaluator.Evaluator); ok {
		return e.BuiltinModule(canonical, alias)
	}
	return nil
}

func (l *stdlibModuleLoader) LoadModule(canonical, alias string) (*runtime.Module, error) {
	if module, ok := l.modules[canonical]; ok {
		return module, nil
	}
	resolver := modules.NewResolver(l.modulePaths)
	path, err := resolver.Resolve(canonical)
	if err != nil {
		if native := l.lookupBuiltin(canonical, alias); native != nil {
			return native, nil
		}
		return nil, err
	}
	if l.loading[path] {
		if native := l.lookupBuiltin(canonical, alias); native != nil {
			return native, nil
		}
		return nil, fmt.Errorf("circular import detected for %s", canonical)
	}
	l.loading[path] = true
	defer delete(l.loading, path)

	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read module %s: %w", canonical, err)
	}
	p := parser.New(lexer.New(string(source)))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("parse module %s: %s", canonical, strings.Join(p.Errors(), "\n"))
	}
	if diagnostics := semantic.New().Analyze(program); len(diagnostics) > 0 {
		messages := make([]string, 0, len(diagnostics))
		for _, d := range diagnostics {
			messages = append(messages, d.Message)
		}
		return nil, fmt.Errorf("analyze module %s: %s", canonical, strings.Join(messages, "\n"))
	}
	chunk, err := bytecode.Compile(program, source, canonical)
	if err != nil {
		return nil, fmt.Errorf("compile module %s: %w", canonical, err)
	}
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(canonical)
	vm.SetModulePaths(l.modulePaths)
	if l.stateful != nil {
		vm.SetStatefulNativeCaller(l.stateful)
	}
	if err := vm.Run(); err != nil {
		return nil, fmt.Errorf("evaluate module %s: %w", canonical, err)
	}
	exports, err := vm.Exports()
	if err != nil {
		return nil, fmt.Errorf("export module %s: %w", canonical, err)
	}
	module := &runtime.Module{Name: alias, Canonical: canonical, Exports: exports}
	for name, value := range module.Exports {
		if function, ok := value.(runtime.BytecodeFunction); ok {
			function.Module = canonical
			module.Exports[name] = function
		}
		if class, ok := value.(runtime.BytecodeClass); ok {
			class.Module = canonical
			module.Exports[name] = class
		}
	}
	l.modules[canonical] = module
	l.chunks[canonical] = chunk
	l.globals[canonical] = vm.GlobalsSnapshot()
	l.decorators[canonical] = vm.FunctionDecoratorState()
	return module, nil
}

// newSubVM returns a VM bound to either a stdlib module's chunk or
// the main chunk (when moduleName == "" and mainChunk is registered).
// Errors when no matching chunk exists.
func (l *stdlibModuleLoader) newSubVM(moduleName string) (*bytecode.VM, error) {
	var chunk bytecode.Chunk
	if moduleName == "" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("entry-script chunk not registered with loader")
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[moduleName]
		if !ok {
			return nil, fmt.Errorf("module %s is not loaded", moduleName)
		}
		chunk = c
	}
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(moduleName)
	vm.SetModulePaths(l.modulePaths)
	if l.stateful != nil {
		vm.SetStatefulNativeCaller(l.stateful)
	}
	vm.RestoreGlobals(l.globals[moduleName])
	vm.RestoreFunctionDecoratorState(l.decorators[moduleName])
	return vm, nil
}

func (l *stdlibModuleLoader) CallModuleFunction(function runtime.BytecodeFunction, args []runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(function.Module)
	if err != nil {
		return nil, err
	}
	return vm.CallFunction(function.Index, args)
}

func (l *stdlibModuleLoader) CallModuleClosure(closure runtime.BytecodeClosure, args []runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(closure.Module)
	if err != nil {
		return nil, err
	}
	return vm.CallClosure(closure, args)
}

func (l *stdlibModuleLoader) ConstructModuleClass(class runtime.BytecodeClass, args []runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(class.Module)
	if err != nil {
		return nil, err
	}
	return vm.ConstructClass(class.Index, args)
}

func (l *stdlibModuleLoader) DeserializeModuleClass(class runtime.BytecodeClass, value runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(class.Module)
	if err != nil {
		return nil, err
	}
	return vm.DeserializeIntoChunkClass(class, value)
}

func (l *stdlibModuleLoader) ConstructorsForModuleClass(class runtime.BytecodeClass) (runtime.Value, error) {
	vm, err := l.newSubVM(class.Module)
	if err != nil {
		return nil, err
	}
	return vm.ReflectConstructorsForChunkClass(class)
}

func (l *stdlibModuleLoader) FieldsForModuleClass(class runtime.BytecodeClass) (runtime.Value, error) {
	if class.Module == "" || class.Module == "parity" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("reflect.fields %s: main chunk not registered in test loader", class.Name)
		}
		vm := bytecode.NewVMWithModuleLoader(l.mainChunk, l.stdout, l)
		vm.SetModuleName(class.Module)
		vm.SetModulePaths(l.modulePaths)
		if l.stateful != nil {
			vm.SetStatefulNativeCaller(l.stateful)
		}
		return vm.ReflectFieldsForChunkClass(class)
	}
	vm, err := l.newSubVM(class.Module)
	if err != nil {
		return nil, err
	}
	return vm.ReflectFieldsForChunkClass(class)
}

func (l *stdlibModuleLoader) registerMainChunk(chunk bytecode.Chunk) {
	l.mainChunk = chunk
	l.hasMainChunk = true
}

func (l *stdlibModuleLoader) CallModuleStaticMethod(class runtime.BytecodeClass, methodName string, args []runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(class.Module)
	if err != nil {
		return nil, err
	}
	return vm.CallStaticMethod(class.Index, methodName, args)
}

func (l *stdlibModuleLoader) CallModuleMethod(module string, className string, methodName string, instance *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(module)
	if err != nil {
		return nil, err
	}
	return vm.CallInstanceMethod(instance, methodName, args)
}

func (l *stdlibModuleLoader) CallParentInModule(module string, className string, methodName string, instance *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(module)
	if err != nil {
		return nil, err
	}
	return vm.CallMethodAs(className, instance, methodName, args)
}

func (l *stdlibModuleLoader) ListAllClasses() []runtime.Value {
	out := []runtime.Value{}
	for module, chunk := range l.chunks {
		for i, classInfo := range chunk.Classes {
			out = append(out, runtime.BytecodeClass{
				Name: classInfo.Name, Index: int64(i), Module: module,
				Decorators:       classInfo.Decorators,
				MethodDecorators: classInfo.MethodDecorators,
			})
		}
	}
	return out
}

func (l *stdlibModuleLoader) LookupModuleInterface(module, name string) (bytecode.InterfaceInfo, bool) {
	chunk, ok := l.chunks[module]
	if !ok {
		return bytecode.InterfaceInfo{}, false
	}
	for _, iface := range chunk.Interfaces {
		if strings.EqualFold(iface.Name, name) {
			return iface, true
		}
	}
	return bytecode.InterfaceInfo{}, false
}

func (l *stdlibModuleLoader) FindFunctionByName(name string) (runtime.Value, bool) {
	for _, module := range l.modules {
		if module == nil {
			continue
		}
		if v, ok := module.Exports[name]; ok {
			switch v := v.(type) {
			case runtime.Function, runtime.OverloadedFunction, runtime.BytecodeFunction, runtime.DecoratorTarget:
				return v, true
			default:
				_ = v
			}
		}
	}
	return nil, false
}

func (l *stdlibModuleLoader) FindClassByName(name string) (runtime.Value, bool) {
	for module, chunk := range l.chunks {
		for idx, classInfo := range chunk.Classes {
			if classInfo.Name == name {
				return runtime.BytecodeClass{
					Name:             classInfo.Name,
					Doc:              classInfo.Doc,
					Index:            int64(idx),
					Module:           module,
					Parent:           classInfo.ParentName,
					Fields:           append([]string(nil), classInfo.FieldNames...),
					Interfaces:       append([]string(nil), classInfo.Implements...),
					Decorators:       classInfo.Decorators,
					MethodDecorators: classInfo.MethodDecorators,
					StaticDecorators: classInfo.StaticDecorators,
				}, true
			}
		}
	}
	return nil, false
}

// runParityWithStdlib is like runParityStateful but additionally wires
// a bytecode-side module loader so source-distributed stdlib modules
// (time.scheduler, async.rate, ...) can be imported in the test source.
func runParityWithStdlib(t *testing.T, source string, want string) {
	t.Helper()

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	if _, err := evaluator.New(&evOut).Eval(program); err != nil {
		t.Fatalf("evaluator error: %v", err)
	}

	src := []byte(source)
	chunk, err := bytecode.Compile(program, src, "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	stateful := evaluator.New(&vmOut)
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.mainChunk = chunk
	loader.hasMainChunk = true
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetStatefulNativeCaller(stateful)
	/* Without the dispatcher wiring, the evaluator can't reach
	 * back into the VM for test.mock + RunTestClass. Production
	 * (cmd/geblang/main.go) sets this; the parity harness
	 * mirrors that so feature-parity coverage is honest. */
	stateful.SetMethodDispatcher(vm)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}

	if evOut.String() != vmOut.String() {
		t.Errorf("output mismatch:\n  evaluator: %q\n  vm:        %q", evOut.String(), vmOut.String())
	}
	if want != "" && evOut.String() != want {
		t.Errorf("wrong output: got %q, want %q", evOut.String(), want)
	}
}

// runParity compiles and runs source through both the evaluator and the VM,
// then checks that their outputs agree.  If want is non-empty it also asserts
// the exact expected output.
// runErrorParity runs source through both the evaluator and VM, expects both
// to produce errors, and checks that each error contains all substrings in
// wantSubstrings.  Use this for error-path tests where the two engines format
// messages differently but must include the same key information.
func runErrorParity(t *testing.T, source string, wantSubstrings ...string) {
	t.Helper()

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	_, evErr := evaluator.New(&evOut).Eval(program)
	if evErr == nil {
		t.Fatal("evaluator: expected error, got nil")
	}

	src := []byte(source)
	chunk, err := bytecode.Compile(program, src, "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	vmErr := bytecode.NewVM(chunk, &vmOut).Run()
	if vmErr == nil {
		t.Fatal("vm: expected error, got nil")
	}

	for _, sub := range wantSubstrings {
		if !strings.Contains(evErr.Error(), sub) {
			t.Errorf("evaluator error missing %q: %v", sub, evErr)
		}
		if !strings.Contains(vmErr.Error(), sub) {
			t.Errorf("vm error missing %q: %v", sub, vmErr)
		}
	}
}

func runParity(t *testing.T, source string, want string) {
	t.Helper()

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	if _, err := evaluator.New(&evOut).Eval(program); err != nil {
		t.Fatalf("evaluator error: %v", err)
	}

	src := []byte(source)
	chunk, err := bytecode.Compile(program, src, "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	if err := bytecode.NewVM(chunk, &vmOut).Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}

	if evOut.String() != vmOut.String() {
		t.Errorf("output mismatch:\n  evaluator: %q\n  vm:        %q", evOut.String(), vmOut.String())
	}
	if want != "" && evOut.String() != want {
		t.Errorf("wrong output: got %q, want %q", evOut.String(), want)
	}
}

// TestParityContainerInspectIsJSONLike pins the Inspect output for
// dicts, lists, and sets across both backends. Strings inside a
// container are JSON-quoted; top-level strings stay unquoted to
// match the existing io.println contract. Dict entries appear in
// insertion order (since 1.5.1).
func TestParityContainerInspectIsJSONLike(t *testing.T) {
	runParity(t, `import io;
io.println({"name": "Ada", "age": 36});
io.println(["a", "b", 1, true, null]);
io.println({"nested": {"a": [1, 2], "b": "x"}});
io.println("plain");
io.println(42);
io.println(["with \"quote\""]);
`, `{"name": "Ada", "age": 36}
["a", "b", 1, true, null]
{"nested": {"a": [1, 2], "b": "x"}}
plain
42
["with \"quote\""]
`)
}

func TestParityArithmetic(t *testing.T) {
	runParity(t, `import io;
io.println(2 + 3);
io.println(10 - 4);
io.println(3 * 7);
io.println(15 // 4);
io.println(17 % 5);
io.println(2 ** 8);
io.println(-5 + 2);
`, "5\n6\n21\n3\n2\n256\n-3\n")
}

func TestParityDecimalArithmetic(t *testing.T) {
	// Decimal values print with 10 decimal places via FloatString(10).
	runParity(t, `import io;
decimal d = 1.5;
let n = 5;
io.println(d + 0.5);
io.println(d * 2.0);
io.println(d.format(0));
io.println(d.format(3));
io.println(d.toString(1));
io.println(n.toDecimal().format(2));
io.println("42".toInt() + 1);
io.println("2.5".toFloat());
io.println("true".toBool());
`, "2.0000000000\n3.0000000000\n2\n1.500\n1.5\n5.00\n43\n2.5\ntrue\n")
}

func TestParityNumericMethods(t *testing.T) {
	// Value-keeping rounding (returns same type), toDecimal(places),
	// and the sign/clamp/isEven/isOdd helpers across both backends.
	runParity(t, `import io;
io.println((2.567).round(2));
io.println((2.5).round());
io.println((-2.5).round());
io.println((2.9).floor());
io.println((2.1).ceil());
io.println((2.99).truncate(1));
io.println((3.14159f).round(2));
io.println((2.9f).floor());
io.println((7).toDecimal(2));
io.println((3.14159f).toDecimal(3));
io.println("12.3456".toDecimal(2));
io.println((-7).sign());
io.println((0).sign());
io.println((4.2).sign());
io.println((12).clamp(0, 10));
io.println((-3).clamp(0, 10));
io.println((5).clamp(0, 10));
io.println((19.99).clamp(0, 5));
io.println((4).isEven());
io.println((7).isOdd());
io.println((-4).isEven());
`, "2.5700000000\n3.0000000000\n-3.0000000000\n2.0000000000\n3.0000000000\n2.9000000000\n3.14\n2\n7.0000000000\n3.1420000000\n12.3500000000\n-1\n0\n1\n10\n0\n5\n5.0000000000\ntrue\ntrue\ntrue\n")
}

func TestParityDirBuiltin(t *testing.T) {
	// dir(value) returns the sorted method-name list for a value; both
	// backends must produce identical output AND it must match the
	// authoritative registry (expected is derived from it, so a wrong
	// list can't be silently re-encoded - the original dir-phantom bug).
	cases := []struct{ literal, typeName string }{
		{`[1, 2, 3]`, "list"},
		{`{"a": 1}`, "dict"},
		{`[1, 2] as set`, "set"},
		{`42`, "int"},
		{`3.5`, "decimal"},
		{`"x"`, "string"},
	}
	src := "import io;\n"
	want := ""
	for _, c := range cases {
		src += `io.println("${dir(` + c.literal + `)}");` + "\n"
		want += formatPrimitiveMethodList(c.typeName) + "\n"
	}
	runParity(t, src, want)
}

func TestParityDumpBuiltin(t *testing.T) {
	// dump(value) renders a type-annotated debug string; identical on
	// both backends (was evaluator-only before R1).
	runParity(t, `import io;
io.println(dump(42));
io.println(dump("hi"));
io.println(dump([1, 2]));
io.println(dump({"a": 1}));
io.println(dump(true));
`, "int(42)\nstring(\"hi\")\nlist[int(1), int(2)]\ndict{string(\"a\"): int(1)}\nbool(true)\n")
}

func TestParityProfilerModule(t *testing.T) {
	// profiler must work on both backends (it was VM-only before being
	// wired into the evaluator). Values are non-deterministic, so assert
	// the result shape rather than contents.
	runParity(t, `import io;
import profiler;
io.println(typeof(profiler.snapshot()));
io.println(typeof(profiler.memory()));
io.println(typeof(profiler.cpu()));
io.println(typeof(profiler.peak()));
io.println(typeof(profiler.delta(profiler.snapshot())));
`, "dict\ndict\ndict\ndict\ndict\n")
}

func TestParityTestingAssertions(t *testing.T) {
	runParity(t, `import io;
import test;

class AssertionTest extends test.Test {
    @test
    func assertions(): void {
        this.equal(2 + 2, 4);
        this.assertEquals(4, 2 + 2);
        this.assertNotEquals(5, 2 + 2);
        this.assertTrue(4 > 3);
        this.assertFalse(3 > 4);
        this.assertNull(null);
        this.assertNotNull("ok");
        this.assertContains("hello Geblang", "Geb");
        this.assertNotContains("hello Geblang", "PHP");
        this.assertContains([1, 2, 3], 2);
        this.assertContains({"name": "Ada"}, "name");
        this.assertEmpty([]);
        this.assertNotEmpty(["ok"]);
        this.assertGreaterThan(3, 4);
        this.assertGreaterThanOrEqual(4, 4);
        this.assertLessThan(5, 4);
        this.assertLessThanOrEqual(4, 4);
    }
}

let instance = AssertionTest();
instance.assertions();
io.println("ok");
`, "ok\n")
}

func TestParityDatabaseStandardBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parity.sqlite")
	runParityStateful(t, `import io;
import db;

let conn = db.Connection({
    "driver": "sqlite",
    "path": `+strconv.Quote(path)+`
});
defer conn.close();

conn.exec("drop table if exists users");
conn.exec("create table users (id integer primary key, name text, email text)");
conn.exec(
    "insert into users (name, email) values (:name, :email)",
    {"name": "Ada", "email": "ada@example.com"}
);
conn.exec(
    "insert into users (name, email) values (?, ?)",
    ["Grace", "grace@example.com"]
);

let rows = conn.query("select email from users where name = :name", {"name": "Ada"});
defer rows.close();
io.println(rows.first()["email"]);

let stmt = conn.prepare("select name from users where email = :email");
let prepared = stmt.query({"email": "grace@example.com"});
defer prepared.close();
io.println(prepared.first()["name"]);
stmt.close();
`, "ada@example.com\nGrace\n")
}

func TestParityLogInterface(t *testing.T) {
	runParityStateful(t, `import io;
import log;

class Capture implements log.LogInterface {
    string last = "";

    func handle(string level, string message, dict<string, any> fields): void {
        this.last = level + ":" + message + ":" + fields["id"];
    }
}

let capture = Capture();
let logger = log.custom(capture);
log.error(logger, "custom", {"id": "3"});
io.println(capture.last);
io.println(capture instanceof log.LogInterface);
`, "error:custom:3\ntrue\n")
}

func TestParityFloatArithmetic(t *testing.T) {
	// Float + decimal operands produce decimal-formatted output in both paths.
	runParity(t, `import io;
float f = 2.5;
io.println(f + 1.5);
io.println(f * 2.0);
`, "4.0000000000\n5.0000000000\n")
}

func TestParityStringOperations(t *testing.T) {
	runParity(t, `import io;
string s = "hello";
io.println(s + " world");
io.println(s.length());
io.println(s.upper());
io.println(s.contains("ell"));
io.println(s.replace("l", "r"));
io.println(s[1..<3]);
`, "hello world\n5\nHELLO\ntrue\nherro\nel\n")
}

func TestParityListOperations(t *testing.T) {
	runParity(t, `import io;
list items = [10, 20, 30, 40];
io.println(items[0]);
io.println(items.length());
io.println(items.isEmpty());
io.println(items[1..<3]);
io.println(items.get(2));
`, "10\n4\nfalse\n[20, 30]\n30\n")
}

func TestParityDictOperations(t *testing.T) {
	runParity(t, `import io;
dict d = {"a": 1};
io.println(d["a"]);
io.println(d.length());
io.println(d.isEmpty());
io.println(d.keys().length());
d["b"] = 2;
io.println(d.length());
`, "1\n1\nfalse\n1\n2\n")
}

func TestParityFunctions(t *testing.T) {
	runParity(t, `import io;
func add(int a, int b): int {
    return a + b;
}
func greet(string name, string prefix = "Hello"): string {
    return prefix + ", " + name + "!";
}
io.println(add(3, 4));
io.println(greet("World"));
io.println(greet("Alice", "Hi"));
`, "7\nHello, World!\nHi, Alice!\n")
}

func TestParityReflectDecoratorMetadata(t *testing.T) {
	runParity(t, `import io;
import reflect;

@route("GET", "/users", name: "users")
func index(): string {
    return "ok";
}

@service(name: "users")
class Controller {
    @route("POST", "/users")
    static func create(): string {
        return "created";
    }

    @route("GET", "/users")
    func list(): string {
        return "list";
    }
}

let fn = reflect.decorators(index);
io.println(fn[0]["name"]);
io.println(fn[0]["target"]);
io.println(fn[0]["args"][0]);
io.println(fn[0]["namedArgs"]["name"]);
io.println(reflect.hasDecorator(index, "ROUTE"));

let cls = reflect.decorators(Controller);
io.println(cls[0]["name"]);
io.println(cls[0]["target"]);

let method = reflect.method(Controller, "list");
io.println(reflect.decorator(method, "route")["target"]);
io.println(reflect.decorator(method, "route")["args"][0]);

let staticMethod = reflect.staticMethod(Controller, "create");
io.println(reflect.decorators(staticMethod)[0]["target"]);
io.println(reflect.decorators(staticMethod)[0]["args"][0]);
`, "route\nfunction\nGET\nusers\ntrue\nservice\nclass\nmethod\nGET\nstaticMethod\nPOST\n")
}

func TestParityReflectCallableBoundMethods(t *testing.T) {
	runParity(t, `import io;
import reflect;

class Greeter {
    string name;

    func Greeter(string name) {
        this.name = name;
    }

    @route("GET", "/hello")
    func greet(string prefix): string {
        return prefix + this.name;
    }

    static func label(string name): string {
        return "kind:" + name;
    }
}

let greeter = Greeter("Ada");
let method = reflect.method(greeter, "greet");
io.println(method("hi "));
io.println(reflect.decorators(method)[0]["name"]);

let staticMethod = reflect.staticMethod(Greeter, "label");
io.println(staticMethod("user"));
`, "hi Ada\nroute\nkind:user\n")
}

func TestParityCallableFunctionDecorators(t *testing.T) {
	runParity(t, `import io;
import reflect;

func suffix(any next, string mark): any {
    return func(string name): string { return next(name) + mark; };
}

func prefix(any next, string label): any {
    return func(string name): string { return label + next(name); };
}

@prefix(label: "Hello, ")
@suffix("!")
func greet(string name): string {
    return name;
}

io.println(greet("Ada"));
io.println(reflect.decorators(greet)[0]["name"]);
io.println(reflect.decorators(greet)[1]["name"]);
`, "Hello, Ada!\nprefix\nsuffix\n")
}

func TestParityCallableFunctionDecoratorOverloads(t *testing.T) {
	runParity(t, `import io;

func label(any next, string prefix): any {
    return func(string name): string { return prefix + next(name); };
}

func label(any next, int count): any {
    return func(string name): string {
        let out = next(name);
        for (let int i = 0; i < count; i++) {
            out = out + "!";
        }
        return out;
    };
}

@label("Hello, ")
func greet(string name): string {
    return name;
}

@label(3)
func cheer(string name): string {
    return name;
}

io.println(greet("Ada"));
io.println(cheer("Bob"));
`, "Hello, Ada\nBob!!!\n")
}

func TestParityCallableDecoratorsOnOverloadedTargets(t *testing.T) {
	runParity(t, `import io;

func prefixInt(any next, string label): any {
    return func(int value): string { return label + next(value); };
}

func prefixString(any next, string label): any {
    return func(string value): string { return label + next(value); };
}

@prefixInt("int:")
func describe(int value): string {
    return value as string;
}

@prefixString("string:")
func describe(string value): string {
    return value;
}

io.println(describe(7));
io.println(describe("Ada"));
`, "int:7\nstring:Ada\n")
}

func TestParityCallableMethodDecorators(t *testing.T) {
	runParity(t, `import io;
import reflect;

func suffix(any next, string mark): any {
    return func(): string {
        return next() + mark;
    };
}

func prefix(any next, string label): any {
    return func(string name): string {
        return label + next(name);
    };
}

class Greeter {
    string name;

    func Greeter(string name) {
        this.name = name;
    }

    @suffix("!")
    func greet(): string {
        return this.name;
    }

    @prefix("kind:")
    static func label(string name): string {
        return name;
    }
}

let greeter = Greeter("Ada");
io.println(greeter.greet());
io.println(Greeter.label("user"));
let method = reflect.method(Greeter, "greet");
io.println(reflect.decorators(method)[0]["name"]);
io.println(reflect.decorators(method)[0]["target"]);
let staticMethod = reflect.staticMethod(Greeter, "label");
io.println(reflect.decorators(staticMethod)[0]["name"]);
io.println(reflect.decorators(staticMethod)[0]["target"]);
`, "Ada!\nkind:user\nsuffix\nmethod\nprefix\nstaticMethod\n")
}

func TestParityCallableMethodDecoratorOverloadsHaveDistinctDecorators(t *testing.T) {
	runParity(t, `import io;

func tag(any next, string label): any {
    return func(string s): string { return label + ":" + next(s); };
}
func tagInt(any next, string label): any {
    return func(int n): string { return label + ":" + next(n); };
}

class Formatter {
    func Formatter() {}

    @tag("str")
    func format(string s): string {
        return s;
    }

    @tagInt("int")
    func format(int n): string {
        return n.toString();
    }
}

let f = Formatter();
io.println(f.format("hello"));
io.println(f.format(42));
`, "str:hello\nint:42\n")
}

func TestParityCallableMethodDecoratorMultipleDecoratorsCallNext(t *testing.T) {
	runParity(t, `import io;

func wrap(any next, string before, string after): any {
    return func(): string {
        return before + next() + after;
    };
}

class Service {
    string name;

    func Service(string name) {
        this.name = name;
    }

    @wrap("[", "]")
    @wrap("{", "}")
    func describe(): string {
        return this.name;
    }
}

let s = Service("core");
io.println(s.describe());
`, "[{core}]\n")
}

func TestParityCallableClassDecorators(t *testing.T) {
	runParity(t, `import io;
import reflect;

dict<string, any> registry = {};

func service(any cls, string name): any {
    registry[name] = cls;
    return cls;
}

@service("users")
class UserService {
    func greeting(): string {
        return "hello from users";
    }
}

let svc = UserService();
io.println(svc.greeting());
io.println(reflect.decorators(UserService)[0]["name"]);
io.println(reflect.decorators(UserService)[0]["args"][0]);
io.println(registry.hasKey("users"));
`, "hello from users\nservice\nusers\ntrue\n")
}

func TestParityCallableClassDecoratorsApplyBottomUp(t *testing.T) {
	runParity(t, `import io;

func tagA(any cls): any {
    io.println("A");
    return cls;
}

func tagB(any cls): any {
    io.println("B");
    return cls;
}

@tagA
@tagB
class Widget {}
`, "B\nA\n")
}

func TestParityUnknownClassDecoratorsAreMetadataOnly(t *testing.T) {
	runParity(t, `import io;
import reflect;

@controller("/api")
class ApiController {
    func ping(): string {
        return "pong";
    }
}

let c = ApiController();
io.println(c.ping());
io.println(reflect.decorators(ApiController)[0]["name"]);
io.println(reflect.decorators(ApiController)[0]["args"][0]);
`, "pong\ncontroller\n/api\n")
}

func TestParityReflectNamedFunctionAndClassHandles(t *testing.T) {
	runParity(t, `import io;
import reflect;

@route("GET", "/users")
func index(): string {
    return "ok";
}

@service(name: "users")
class Controller {}

let fn = reflect.function("index");
io.println(reflect.decorators(fn)[0]["target"]);
io.println(reflect.decorators(reflect.function("index"))[0]["args"][0]);

let cls = reflect.class("Controller");
io.println(reflect.decorators(cls)[0]["target"]);
io.println(reflect.decorators(reflect.class("Controller"))[0]["namedArgs"]["name"]);
io.println(reflect.function("missing") == null);
io.println(reflect.class("missing") == null);
`, "function\nGET\nclass\nusers\ntrue\ntrue\n")
}

func TestParityReflectFunctionSignatureMetadata(t *testing.T) {
	runParity(t, `import io;
	import reflect;

func index(string name, int limit = 10): string {
    return name;
}

func collect(string ...tags): string {
    return "ok";
}

class Controller {
    func list(string prefix): string {
        return prefix;
    }
}

let params = reflect.parameters(index);
io.println(params.length());
io.println(params[0]["name"]);
io.println(params[0]["type"]);
io.println(params[1]["hasDefault"]);
io.println(reflect.returnType(index));
io.println(reflect.parameters(collect)[0]["variadic"]);

let method = reflect.method(Controller, "list");
let methodParams = reflect.parameters(method);
io.println(methodParams[0]["name"]);
io.println(methodParams[0]["type"]);
io.println(reflect.returnType(method));
	`, "2\nname\nstring\ntrue\nstring\ntrue\nprefix\nstring\nstring\n")
}

func TestParityReflectDocblocks(t *testing.T) {
	runParity(t, `import io;
	import reflect;

	## Handles index requests.
	## Returns a status label.
	func index(): string {
	    return "ok";
	}

	func empty(): string {
	    return "empty";
	}

	/**
	 * Controller doc.
	 * Used by routing.
	 */
	class Controller {
	    ## Lists records.
	    func list(): string {
	        return "list";
	    }
	}

	## Describes named values.
	interface Named {
	    ## Returns display name.
	    func name(): string;
	}

	io.println(reflect.doc(index));
	io.println(reflect.doc(Controller));
	io.println(reflect.doc(reflect.method(Controller, "list")));
		io.println(reflect.doc(Named));
		io.println(reflect.interfaceMethods(Named)[0]["doc"]);
		let docs = reflect.docs(index);
		io.println(docs["summary"]);
		io.println(docs["body"]);
		io.println(docs["lines"][1]);
		io.println(reflect.docs(empty) == null);
		io.println(reflect.doc(empty) == null);
		`, "Handles index requests.\nReturns a status label.\nController doc.\nUsed by routing.\nLists records.\nDescribes named values.\nReturns display name.\nHandles index requests.\nReturns a status label.\nReturns a status label.\ntrue\ntrue\n")
}

func TestParityReflectClassShapeMetadata(t *testing.T) {
	runParity(t, `import io;
	import reflect;

interface Named {
    func name(): string;
}

class Base {}

class Controller extends Base implements Named {
    string prefix;
    int count;

    func name(): string {
        return this.prefix;
    }

    func list(): string {
        return this.prefix;
    }

    static func create(): string {
        return "created";
    }
}

io.println(reflect.parent(Controller));
io.println(reflect.interfaces(Controller)[0]);
io.println(reflect.fields(Controller)[0]["name"]);
io.println(reflect.fields(Controller)[1]["name"]);
io.println(reflect.methods(Controller)[0]);
io.println(reflect.methods(Controller)[1]);
io.println(reflect.staticMethods(Controller)[0]);
	`, "Base\nNamed\ncount\nprefix\nlist\nname\ncreate\n")
}

func TestParityReflectInterfaceMetadata(t *testing.T) {
	runParity(t, `import io;
	import reflect;

	interface Base {}
	interface Animal extends Base {
	    func name(): string;
	    func sound(string prefix = "raw"): string;
	}

	let parents = reflect.interfaceParents(Animal);
	io.println(parents.length());
	io.println(parents[0]);

	let methods = reflect.interfaceMethods(Animal);
	io.println(methods.length());
	io.println(methods[0]["name"]);
	io.println(methods[0]["returnType"]);
	io.println(methods[1]["name"]);
	io.println(methods[1]["parameters"][0]["name"]);
	io.println(methods[1]["parameters"][0]["type"]);
	io.println(methods[1]["parameters"][0]["hasDefault"]);
	`, "1\nBase\n2\nname\nstring\nsound\nprefix\nstring\ntrue\n")
}

func TestParityReflectOverloadedDecoratorMetadata(t *testing.T) {
	runParity(t, `import io;
	import reflect;

@tag("int")
func describe(int value): string {
    return "int";
}

@tag("string")
func describe(string value): string {
    return "string";
}

let decorators = reflect.decorators(describe);
let tags = reflect.decorators(describe, "TAG");
io.println(decorators.length());
io.println(decorators[0]["args"][0]);
io.println(decorators[0]["overload"]);
io.println(decorators[1]["args"][0]);
io.println(decorators[1]["overload"]);
io.println(tags.length());
io.println(tags[0]["args"][0]);
io.println(tags[1]["args"][0]);
`, "2\nint\n0\nstring\n1\n2\nint\nstring\n")
}

func TestParityReflectConstructors(t *testing.T) {
	runParity(t, `import io;
import reflect;

class Point {
    int x;
    int y;
    func Point(int x, int y) {
        this.x = x;
        this.y = y;
    }
}

let ctors = reflect.constructors(Point);
io.println(ctors.length());
io.println(ctors[0][0]["name"]);
io.println(ctors[0][0]["type"]);
io.println(ctors[0][1]["name"]);
`, "1\nx\nint\ny\n")
}

func TestParityReflectTypeBindings(t *testing.T) {
	runParity(t, `import io;
import reflect;

class Box<T> {
    T value;
    func Box(T v) { this.value = v; }
}

Box<string> b = Box("hello");
Box<int> n = Box(42);
io.println(reflect.typeBindings(b)["T"]);
io.println(reflect.typeBindings(n)["T"]);
`, "string\nint\n")
}

func TestParityRecursion(t *testing.T) {
	runParity(t, `import io;
func fact(int n): int {
    if (n <= 1) { return 1; }
    return n * fact(n - 1);
}
io.println(fact(6));
`, "720\n")
}

func TestParityFunctionOverloads(t *testing.T) {
	runParity(t, `import io;
func describe(int x): string {
    return "int:" + (x as string);
}
func describe(string x): string {
    return "str:" + x;
}
io.println(describe(42));
io.println(describe("hello"));
`, "int:42\nstr:hello\n")
}

func TestParityOverloadNestedExpectedType(t *testing.T) {
	runParity(t, `import io;
func wrap(string s): string {
    return "[" + s + "]";
}
func describe(int x): string {
    return "int:" + (x as string);
}
func describe(string x): string {
    return "str:" + x;
}
func printWrapped(string s): void {
    io.println(s);
}
string r = describe(42);
io.println(r);
printWrapped(describe("hello"));
io.println(wrap(describe(99)));
`, "int:42\nstr:hello\n[int:99]\n")
}

func TestParityClosureNoCapture(t *testing.T) {
	runParity(t, `import io;
let double = func(int x): int { return x * 2; };
io.println(double(5));
io.println(double(21));
`, "10\n42\n")
}

func TestParityClosureCapture(t *testing.T) {
	runParity(t, `import io;
func makeAdder(int n): any {
    return func(int x): int { return x + n; };
}
let add10 = makeAdder(10);
io.println(add10(5));
io.println(add10(32));
`, "15\n42\n")
}

func TestParityClosureMultipleCaptures(t *testing.T) {
	runParity(t, `import io;
func makeLinear(int a, int b): any {
    return func(int x): int { return a * x + b; };
}
let f = makeLinear(3, 1);
io.println(f(0));
io.println(f(4));
`, "1\n13\n")
}

func TestParityClosureMutableCapture(t *testing.T) {
	runParity(t, `import io;
func makeCounter(): any {
    int n = 0;
    return func(): int {
        n++;
        return n;
    };
}
let counter = makeCounter();
io.println(counter());
io.println(counter());
		`, "1\n2\n")
}

func TestParityCallFunctionLiteralDirectly(t *testing.T) {
	runParity(t, `import io;
io.println((func(int value): int {
    return value * 2;
})(21));
	`, "42\n")
}

func TestParityCallReturnedCallableDirectly(t *testing.T) {
	runParity(t, `import io;
func makeAdder(int amount): callable {
    return func(int value): int {
        return value + amount;
    };
}

io.println(makeAdder(5)(7));
io.println(makeAdder(10)(value: 3));
	`, "12\n13\n")
}

func TestParityGeneratorFunctionYieldsValues(t *testing.T) {
	runParity(t, `import io;
func numbers(): any {
    yield 1;
    yield 2;
    yield 3;
}
let total = 0;
for (n in numbers()) {
    total = total + n;
}
io.println(total);
	`, "6\n")
}

func TestParityGeneratorFunctionYieldsFromLoop(t *testing.T) {
	runParity(t, `import io;
func upTo(int max): any {
    for (let int i = 1; i <= max; i++) {
        yield i;
    }
}
for (n in upTo(3)) {
    io.println(n);
}
	`, "1\n2\n3\n")
}

func TestParityGeneratorLiteralCapturesValues(t *testing.T) {
	runParity(t, `import io;
func make(int start): callable {
    return func(): any {
        yield start;
        yield start + 1;
    };
}
let gen = make(4);
let out = 0;
for (n in gen()) {
    out = out + n;
}
io.println(out);
	`, "9\n")
}

func TestParityGeneratorTypeHintsAndEarlyBreak(t *testing.T) {
	runParity(t, `import io;
func naturals(): generator<int> {
    int n = 0;
    while (true) {
        yield n;
        n++;
    }
}

iterable<int> values = naturals();
for (n in values) {
    io.println(n);
    break;
}
io.println("done");
	`, "0\ndone\n")
}

func TestParityGenericGeneratorKeepsTypeBindings(t *testing.T) {
	runParity(t, `import io;
func checks<T>(T value): generator<bool> {
    yield value instanceof T;
}

for (ok in checks("hello")) {
    io.println(ok);
}
for (ok in checks(42)) {
    io.println(ok);
}
`, "true\ntrue\n")
}

func TestParityIfElse(t *testing.T) {
	runParity(t, `import io;
int x = 7;
if (x > 10) {
    io.println("big");
} elseif (x > 3) {
    io.println("medium");
} else {
    io.println("small");
}
`, "medium\n")
}

func TestParityWhileLoop(t *testing.T) {
	runParity(t, `import io;
int i = 0;
int sum = 0;
while (i < 5) {
    sum = sum + i;
    i = i + 1;
}
io.println(sum);
`, "10\n")
}

func TestParityForLoop(t *testing.T) {
	runParity(t, `import io;
int total = 0;
for (int i = 1; i <= 5; i++) {
    total = total + i;
}
io.println(total);
`, "15\n")
}

func TestParityForInList(t *testing.T) {
	runParity(t, `import io;
list nums = [10, 20, 30];
int sum = 0;
for (int n in nums) {
    sum = sum + n;
}
io.println(sum);
`, "60\n")
}

func TestParityForInRange(t *testing.T) {
	runParity(t, `import io;
int sum = 0;
for (int i in 1..5) {
    sum = sum + i;
}
io.println(sum);
`, "15\n")
}

func TestParityMatchExpression(t *testing.T) {
	runParity(t, `import io;
func classify(int n): string {
    return match(n) {
        case 1 => "one";
        case 2 => "two";
        default => "other";
    };
}
io.println(classify(1));
io.println(classify(2));
io.println(classify(99));
`, "one\ntwo\nother\n")
}

func TestParityMatchTypeBinding(t *testing.T) {
	runParity(t, `import io;
func describe(any v): string {
    return match(v) {
        case int n => "int:" + (n as string);
        case string s => "str:" + s;
        default => "other";
    };
}
io.println(describe(42));
io.println(describe("hi"));
io.println(describe(true));
`, "int:42\nstr:hi\nother\n")
}

func TestParityExceptionHandling(t *testing.T) {
	runParity(t, `import io;
try {
    throw Error("oops");
} catch (Error e) {
    io.println(e);
}
io.println("after");
`, "Error: oops\nafter\n")
}

func TestParityFinallyBlock(t *testing.T) {
	runParity(t, `import io;
func withFinally(): int {
    try {
        return 7;
    } finally {
        io.println("finally");
    }
}
io.println(withFinally());
`, "finally\n7\n")
}

func TestParityDefer(t *testing.T) {
	runParity(t, `import io;
func run(): void {
    defer io.println("deferred");
    io.println("body");
}
run();
`, "body\ndeferred\n")
}

func TestParityDeferMultiple(t *testing.T) {
	runParity(t, `import io;
func run(): void {
    defer io.println("first");
    defer io.println("second");
    defer io.println("third");
    io.println("body");
}
run();
`, "body\nthird\nsecond\nfirst\n")
}

func TestParityDeferOnReturn(t *testing.T) {
	runParity(t, `import io;
func run(bool early): void {
    defer io.println("deferred");
    if (early) {
        io.println("early");
        return;
    }
    io.println("normal");
}
run(true);
run(false);
`, "early\ndeferred\nnormal\ndeferred\n")
}

func TestParityDeferUserFunc(t *testing.T) {
	runParity(t, `import io;
func cleanup(): void {
    io.println("cleanup");
}
func run(): void {
    defer cleanup();
    io.println("body");
}
run();
`, "body\ncleanup\n")
}

func TestParityDeferUserFuncWithArgs(t *testing.T) {
	runParity(t, `import io;
func log(string msg): void {
    io.println(msg);
}
func run(): void {
    defer log("done");
    io.println("running");
}
run();
`, "running\ndone\n")
}

func TestParityDeferUserFuncLIFO(t *testing.T) {
	runParity(t, `import io;
func log(string msg): void {
    io.println(msg);
}
func run(): void {
    defer log("first");
    defer log("second");
    defer log("third");
    io.println("body");
}
run();
`, "body\nthird\nsecond\nfirst\n")
}

func TestParityDeferUserFuncOnReturn(t *testing.T) {
	runParity(t, `import io;
func cleanup(): void {
    io.println("cleanup");
}
func run(bool early): void {
    defer cleanup();
    if (early) {
        io.println("early");
        return;
    }
    io.println("normal");
}
run(true);
run(false);
`, "early\ncleanup\nnormal\ncleanup\n")
}

func TestParityDeferMethodCall(t *testing.T) {
	runParity(t, `import io;
class Resource {
    string name;
    func Resource(string name) {
        this.name = name;
    }
    func close(): void {
        io.println("closing " + this.name);
    }
}
func run(): void {
    Resource r = Resource("db");
    defer r.close();
    io.println("working");
}
run();
`, "working\nclosing db\n")
}

func TestParityDeferMethodCallWithArgs(t *testing.T) {
	runParity(t, `import io;
class Logger {
    func Logger() {}
    func log(string msg): void {
        io.println(msg);
    }
}
func run(): void {
    Logger l = Logger();
    defer l.log("done");
    io.println("start");
}
run();
`, "start\ndone\n")
}

func TestParityDeferMethodCallOnReturn(t *testing.T) {
	runParity(t, `import io;
class Resource {
    string name;
    func Resource(string name) {
        this.name = name;
    }
    func close(): void {
        io.println("closed " + this.name);
    }
}
func run(bool early): void {
    Resource r = Resource("conn");
    defer r.close();
    if (early) {
        io.println("early exit");
        return;
    }
    io.println("normal exit");
}
run(true);
run(false);
`, "early exit\nclosed conn\nnormal exit\nclosed conn\n")
}

func TestParityDeferCallableVar(t *testing.T) {
	runParity(t, `import io;
func run(): void {
    let cleanup = func(): void { io.println("cleaned up"); };
    defer cleanup();
    io.println("running");
}
run();
`, "running\ncleaned up\n")
}

func TestParityDeferCallableVarWithArgs(t *testing.T) {
	runParity(t, `import io;
func run(): void {
    let log = func(string msg): void { io.println(msg); };
    defer log("done");
    io.println("working");
}
run();
`, "working\ndone\n")
}

func TestParityBasicClass(t *testing.T) {
	runParity(t, `import io;
class Point {
    int x;
    int y;
    func Point(int x, int y) {
        this.x = x;
        this.y = y;
    }
    func sum(): int {
        return this.x + this.y;
    }
}
Point p = Point(3, 4);
io.println(p.sum());
io.println(p.x);
`, "7\n3\n")
}

func TestParityClassInheritance(t *testing.T) {
	runParity(t, `import io;
class Animal {
    string name;
    func Animal(string name) {
        this.name = name;
    }
    func speak(): string {
        return this.name + " says something";
    }
}
class Dog extends Animal {
    func Dog(string name) {
        parent(name);
    }
    func speak(): string {
        return this.name + " says woof";
    }
}
Dog d = Dog("Rex");
io.println(d.speak());
io.println(d.name);
`, "Rex says woof\nRex\n")
}

func TestParityClassEqMagicMethod(t *testing.T) {
	runParity(t, `import io;
class Vec {
    int x;
    func Vec(int x) { this.x = x; }
    func __eq(any other): bool {
        if (other instanceof Vec) {
            return this.x == other.x;
        }
        return false;
    }
}
Vec a = Vec(5);
Vec b = Vec(5);
Vec c = Vec(9);
io.println(a == b);
io.println(a == c);
`, "true\nfalse\n")
}

func TestParityClassArithmeticMagicMethods(t *testing.T) {
	runParity(t, `import io;
class Num {
    int v;
    func Num(int v) { this.v = v; }
    func __add(any other): Num { return Num(this.v + other.v); }
    func __mul(any other): Num { return Num(this.v * other.v); }
    func inspect(): int { return this.v; }
}
Num a = Num(3);
Num b = Num(4);
io.println((a + b).inspect());
io.println((a * b).inspect());
`, "7\n12\n")
}

func TestParityClassComparisonMagicMethods(t *testing.T) {
	runParity(t, `import io;
class Box {
    int v;
    func Box(int v) { this.v = v; }
    func __lt(any other): bool { return this.v < other.v; }
    func __gt(any other): bool { return this.v > other.v; }
}
Box a = Box(3);
Box b = Box(5);
io.println(a < b);
io.println(b < a);
io.println(b > a);
`, "true\nfalse\ntrue\n")
}

func TestParityNumericLessEqualGreaterEqual(t *testing.T) {
	runParity(t, `import io;
io.println(3 <= 5);
io.println(5 <= 5);
io.println(6 <= 5);
io.println(5 >= 3);
io.println(5 >= 5);
io.println(3 >= 5);
`, "true\ntrue\nfalse\ntrue\ntrue\nfalse\n")
}

func TestParityClassLteGteMagicMethods(t *testing.T) {
	runParity(t, `import io;
class Weight {
    int g;
    func Weight(int g) { this.g = g; }
    func __lte(any other): bool { return this.g <= other.g; }
    func __gte(any other): bool { return this.g >= other.g; }
}
Weight a = Weight(3);
Weight b = Weight(5);
Weight c = Weight(3);
io.println(a <= b);
io.println(b <= a);
io.println(a <= c);
io.println(b >= a);
io.println(a >= b);
io.println(a >= c);
`, "true\nfalse\ntrue\ntrue\nfalse\ntrue\n")
}

func TestParityClassBitwiseMagicMethods(t *testing.T) {
	runParity(t, `import io;
class Flags {
    int v;
    func Flags(int v) { this.v = v; }
    func __bitand(any other): Flags { return Flags(this.v & other.v); }
    func __bitor(any other): Flags { return Flags(this.v | other.v); }
    func __bitxor(any other): Flags { return Flags(this.v ^ other.v); }
    func __lshift(any other): Flags { return Flags(this.v << other.v); }
    func __rshift(any other): Flags { return Flags(this.v >> other.v); }
    func __bitnot(): Flags { return Flags(~this.v); }
    func value(): int { return this.v; }
}
Flags a = Flags(12);
Flags b = Flags(10);
io.println((a & b).value());
io.println((a | b).value());
io.println((a ^ b).value());
io.println((a << Flags(1)).value());
io.println((a >> Flags(1)).value());
Flags c = Flags(0);
io.println((~c).value());
`, "8\n14\n6\n24\n6\n-1\n")
}

func TestParityClassGetSetMethods(t *testing.T) {
	runParity(t, `import io;
class Config {
    dict data;
    func Config() { this.data = {}; }
    func __get(string key): any { return this.data[key]; }
    func __set(string key, any value): void { this.data[key] = value; }
}
Config c = Config();
c.foo = "bar";
c.num = 42;
io.println(c.foo);
io.println(c.num);
`, "bar\n42\n")
}

func TestParityClassInvokeMagicMethod(t *testing.T) {
	runParity(t, `import io;
class Multiplier {
    int factor;
    func Multiplier(int factor) { this.factor = factor; }
    func __invoke(int x): int { return this.factor * x; }
}
Multiplier m = Multiplier(7);
io.println(m(6));
`, "42\n")
}

func TestParityInterface(t *testing.T) {
	runParity(t, `import io;
interface Greeter {
    func greet(): string;
}
class English implements Greeter {
    func greet(): string { return "Hello"; }
}
class Spanish implements Greeter {
    func greet(): string { return "Hola"; }
}
English e = English();
Spanish s = Spanish();
io.println(e.greet());
io.println(s.greet());
io.println(e instanceof Greeter);
`, "Hello\nHola\ntrue\n")
}

func TestParityStaticMembers(t *testing.T) {
	runParity(t, `import io;
class Named {
    static const prefix = "N";
    static func label(string name): string {
        return Named.prefix + ":" + name;
    }
}
io.println(Named.prefix);
io.println(Named.label("Ada"));
`, "N\nN:Ada\n")
}

func TestParityTypeOf(t *testing.T) {
	runParity(t, `import io;
import reflect;
io.println(typeof(42));
io.println(typeof("hello"));
io.println(typeof(true));
io.println(typeof(null));
io.println(reflect.typeOf([1, 2]));
io.println(reflect.typeOf({"a": 1}));
`, "int\nstring\nbool\nnull\nlist\ndict\n")
}

func TestParityTypeEquality(t *testing.T) {
	runParity(t, `import io;
class Foo {}
Foo f = Foo();
io.println(typeof(f) == Foo);
io.println(typeof(f) == string);
io.println(typeof(42) == int);
io.println(typeof("hi") == string);
io.println(typeof(true) == bool);
io.println(typeof(3.14) == decimal);
io.println(typeof(3.14f) == float);
io.println(int == typeof(42));
io.println(string == typeof("world"));
io.println(typeof(f) == f.type);
io.println(f.type == Foo);
io.println(42.type == int);
io.println("hi".type == string);
io.println(typeof(Foo));
`, "true\nfalse\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\nFoo\n")
}

func TestParityInstanceOf(t *testing.T) {
	runParity(t, `import io;
class Foo {}
class Bar extends Foo {}
Bar b = Bar();
io.println(b instanceof Bar);
io.println(b instanceof Foo);
`, "true\ntrue\n")
}

func TestParityScalarCasts(t *testing.T) {
	runParity(t, `import io;
io.println(("42" as int));
io.println((99 as string));
io.println((true as string));
io.println((false as string));
`, "42\n99\ntrue\nfalse\n")
}

func TestParityPrimitiveStringMethods(t *testing.T) {
	runParity(t, `import io;
string s = "  hello world  ";
io.println(s.trim());
io.println(s.trim().split(" ").length());
io.println("abc".startsWith("ab"));
io.println("abc".endsWith("bc"));
io.println("abc".indexOf("b"));
`, "hello world\n2\ntrue\ntrue\n1\n")
}

func TestParityPrimitiveNumericMethods(t *testing.T) {
	runParity(t, `import io;
int n = -7;
io.println(n.abs());
io.println(n.toString());
io.println(n.isNegative());
io.println(n.isPositive());
io.println(n.isZero());
`, "7\n-7\ntrue\nfalse\nfalse\n")
}

func TestParityBreakContinue(t *testing.T) {
	runParity(t, `import io;
int sum = 0;
for (int i = 0; i < 10; i++) {
    if (i == 3) { continue; }
    if (i == 7) { break; }
    sum = sum + i;
}
io.println(sum);
`, "18\n")
}

func TestParityNullHandling(t *testing.T) {
	runParity(t, `import io;
?string s = null;
io.println(s == null);
s = "hello";
io.println(s == null);
io.println(s);
`, "true\nfalse\nhello\n")
}

func TestParityBooleanOperators(t *testing.T) {
	runParity(t, `import io;
io.println(true && false);
io.println(true || false);
io.println(true xor true);
io.println(true xor false);
io.println(!true);
io.println(!false);
`, "false\ntrue\nfalse\ntrue\nfalse\ntrue\n")
}

func TestParityMathStdlib(t *testing.T) {
	runParity(t, `import io;
import math;
io.println(math.abs(-5));
io.println(math.max(3, 7));
io.println(math.min(3, 7));
io.println(math.floor(3.7));
io.println(math.ceil(3.2));
io.println(math.sqrt(16.0));
`, "5\n7\n3\n3\n4\n4\n")
}

func TestParityJSONStdlib(t *testing.T) {
	runParity(t, `import io;
import json;
dict parsed = json.parse("{\"x\": 1}");
io.println(parsed["x"]);
io.println(json.stringify({"name": "geb"}));
`, "1\n{\"name\":\"geb\"}\n")
}

func TestParityTOMLStdlib(t *testing.T) {
	runParity(t, `import io;
import toml;
dict parsed = toml.parse("name = \"geb\"\nversion = 1\n");
io.println(parsed["name"]);
io.println(parsed["version"]);
`, "geb\n1\n")
}

func TestParityBytesStdlib(t *testing.T) {
	runParity(t, `import io;
import bytes;
io.println(bytes.fromHex("48656c6c6f").toString());
io.println(bytes.fromString("hi").toHex());
`, "Hello\n6869\n")
}

func TestParityIntBaseConversion(t *testing.T) {
	runParity(t, `import io;
io.println((255).toString(16));
io.println((255).toString(2));
io.println((-13).toString(2));
io.println("ff".toInt(16));
io.println("-101".toInt(2));
`, "ff\n11111111\n-1101\n255\n-5\n")
}

func TestParityBytesBase64Url(t *testing.T) {
	runParity(t, `import io;
import bytes;
let raw = bytes.fromHex("fbff");
io.println(raw.toBase64Url());
io.println(bytes.toBase64Url(raw));
io.println(bytes.toHex(bytes.fromBase64Url("-_8")));
`, "-_8\n-_8\nfbff\n")
}

func TestParityCryptHashAcceptsBytes(t *testing.T) {
	runParity(t, `import io;
import bytes;
import crypt;
let raw = bytes.fromString("hi");
io.println(crypt.sha256(raw));
io.println(crypt.sha256("hi"));
io.println(crypt.md5(raw));
io.println(crypt.sha1(bytes.fromHex("00ff")));
`, "8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4\n8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4\n49f68a5c8493ec2c0bf489821c21fc3b\naa3e5dcdd77b153f2e59bd0d8794fde33cb4e486\n")
}

func TestParityFromImportNative(t *testing.T) {
	runParity(t, `import io;
from crypt import passwordHash, passwordVerify as verify;
let h = passwordHash("hunter2", {"algorithm": "bcrypt", "cost": 4});
io.println(h.startsWith("$2y$04$"));
io.println(verify("hunter2", h));
io.println(verify("wrong", h));
`, "true\ntrue\nfalse\n")
}

func TestParityPasswordHashRoundTrip(t *testing.T) {
	runParity(t, `import io;
import crypt;
let h = crypt.passwordHash("hunter2", {"algorithm": "bcrypt", "cost": 4});
io.println(h.startsWith("$2y$04$"));
io.println(crypt.passwordVerify("hunter2", h));
io.println(crypt.passwordVerify("wrong", h));
io.println(crypt.passwordVerify("hunter2", "$2y$04$cAkpTnTMpIpo0m80Unnoc.XTtHYSLKMe1xOUiV8i7MRi.q6noRl3y"));
`, "true\ntrue\nfalse\ntrue\n")
}

func TestParityBinaryPack(t *testing.T) {
	runParity(t, `import io;
import binary;
import bytes;
let buf = binary.pack(">IH", 3735928559, 1024);
io.println(bytes.toHex(buf));
let parts = binary.unpack(">IH", buf);
io.println(parts);
io.println(binary.size(">IH"));
`, "deadbeef0400\n[3735928559, 1024]\n6\n")
}

func TestParityExceptionTypes(t *testing.T) {
	runParity(t, `import io;
try {
    io.println(10);
    throw ValueError("negative");
} catch (ValueError e) {
    io.println(e);
} catch (Error e) {
    io.println(e);
}
`, "10\nValueError: negative\n")
}

func TestParityExceptionFromCalledFunction(t *testing.T) {
	runParity(t, `import io;
func risky(int n): int {
    if (n < 0) {
        throw ValueError("negative input");
    }
    return n * 2;
}
try {
    let r = risky(-1);
    io.println("unreachable");
} catch (ValueError e) {
    io.println(e);
}
io.println("done");
`, "ValueError: negative input\ndone\n")
}

func TestParityNestedFunctions(t *testing.T) {
	runParity(t, `import io;
func outer(int x): int {
    func inner(int y): int {
        return x + y;
    }
    return inner(10);
}
io.println(outer(5));
io.println(outer(20));
`, "15\n30\n")
}

func TestParityNamedArguments(t *testing.T) {
	runParity(t, `import io;
func connect(string host, int port = 80, bool tls = false): string {
    string result = host + ":" + (port as string);
    if (tls) { result = result + "s"; }
    return result;
}
io.println(connect("example.com"));
io.println(connect("example.com", port: 443, tls: true));
io.println(connect("api.com", tls: true, port: 8443));
`, "example.com:80\nexample.com:443s\napi.com:8443s\n")
}

func TestParityPostfixIncrement(t *testing.T) {
	runParity(t, `import io;
int x = 5;
x++;
io.println(x);
x--;
x--;
io.println(x);
`, "6\n4\n")
}

func TestParityTopLevelReturn(t *testing.T) {
	runParity(t, `import io;
io.println("before");
return;
io.println("after");
`, "before\n")
}

func TestParityConditionalExpression(t *testing.T) {
	runParity(t, `import io;
int x = 5;
string size = "small";
if (x > 3) { size = "big"; }
io.println(size);
string label = "other";
if (x == 5) { label = "five"; }
io.println(label);
`, "big\nfive\n")
}

func TestParityStringInterpolationViaCast(t *testing.T) {
	runParity(t, `import io;
int n = 42;
bool b = true;
io.println("n=" + (n as string) + " b=" + (b as string));
`, "n=42 b=true\n")
}

func TestParityDestructuringForIn(t *testing.T) {
	runParity(t, `import io;
let pairs = [["a", 1], ["b", 2], ["c", 3]];
for (name, value in pairs) {
    io.println(name + ":" + (value as string));
}
`, "a:1\nb:2\nc:3\n")
}

func TestParityOptionalChaining(t *testing.T) {
	runParity(t, `import io;
class User {
    string name;
    func User(string name) { this.name = name; }
    func greet() { return "hello " + this.name; }
}
let u = User("Alice");
let n = null;
io.println(u?.name);
io.println(u?.greet());
let result = n?.name;
if (result == null) { io.println("null"); } else { io.println(result); }
`, "Alice\nhello Alice\nnull\n")
}

func TestParityDictDelete(t *testing.T) {
	runParity(t, `import io;
let d = {"a": 1, "b": 2, "c": 3};
d.delete("b");
io.println(d.length() as string);
io.println(d.hasKey("b") as string);
io.println(d.hasKey("a") as string);
`, "2\nfalse\ntrue\n")
}

func TestParityNullCoalesce(t *testing.T) {
	runParity(t, `import io;
let a = null;
let b = "hello";
io.println(a ?? "default");
io.println(b ?? "default");
`, "default\nhello\n")
}

func TestParityStringMethods(t *testing.T) {
	runParity(t, `import io;
let s = "  hello  ";
io.println(s.trimStart());
io.println(s.trimEnd());
io.println("ha".repeat(3));
io.println("x".padStart(5));
io.println("x".padEnd(5, "-"));
let chars = "abc".chars();
io.println(chars.length() as string);
io.println(chars.get(0));
io.println("A".codePointAt(0) as string);
`, "hello  \n  hello\nhahaha\n    x\nx----\n3\na\n65\n")
}

func TestParityListHigherOrder(t *testing.T) {
	runParity(t, `import io;
let nums = [1, 2, 3, 4, 5];
let doubled = nums.map(func(int x): int { return x * 2; });
io.println(doubled.get(0) as string);
let evens = nums.filter(func(int x): bool { return x % 2 == 0; });
io.println(evens.length() as string);
let sum = nums.reduce(func(int acc, int x): int { return acc + x; }, 0);
io.println(sum as string);
let found = nums.find(func(int x): bool { return x > 3; });
io.println(found as string);
io.println(nums.any(func(int x): bool { return x > 4; }) as string);
io.println(nums.all(func(int x): bool { return x > 0; }) as string);
io.println(nums.count(func(int x): bool { return x % 2 == 0; }) as string);
let sorted = [3,1,2].sorted();
io.println(sorted.get(0) as string);
let nested = [[1,2],[3,4]].flatten();
io.println(nested.length() as string);
let uniq = [1,2,2,3,1].unique();
io.println(uniq.length() as string);
let zipped = [1,2,3].zip([4,5,6]);
io.println(zipped.length() as string);
`, "2\n2\n15\n4\ntrue\ntrue\n2\n1\n4\n3\n3\n")
}

func TestParityReModule(t *testing.T) {
	runParity(t, `import io;
import re;
io.println(re.test("\\d+", "abc123") as string);
io.println(re.test("\\d+", "abc") as string);
io.println(re.find("\\d+", "abc123def"));
let all = re.findAll("\\d+", "a1b22c333");
io.println(all.length() as string);
io.println(all.get(0));
io.println(all.get(1));
io.println(all.get(2));
let groups = re.match("(?P<name>[A-Za-z]+)([0-9]+)", "Ada123");
io.println(groups["text"]);
io.println(groups["groups"][1]);
io.println(groups["groups"][2]);
io.println(groups["named"]["name"]);
io.println(re.replace("o+", "0", "foobar"));
let parts = re.split(",\\s*", "a, b, c");
io.println(parts.length() as string);
`, "true\nfalse\n123\n3\n1\n22\n333\nAda123\nAda\n123\nAda\nf0bar\n3\n")
}

func TestParityMarkdownModule(t *testing.T) {
	runParity(t, "import io;\nimport markdown;\n\nlet source = \"# Title\\n\\nHello **Geblang** and `code`.\\n\\n- one\\n- two\\n\\n```gb\\nio.println(1);\\n```\";\nlet blocks = markdown.parse(source);\nio.println(blocks.length());\nio.println(blocks[0][\"type\"]);\nio.println(blocks[0][\"level\"]);\nio.println(blocks[1][\"text\"]);\nio.println(blocks[2][\"items\"][1]);\nlet html = markdown.renderHtml(source);\nio.println(html.contains(\"<h1 id=\\\"title\\\">Title</h1>\"));\nio.println(html.contains(\"<strong>Geblang</strong>\"));\nio.println(markdown.stripText(source).contains(\"io.println\"));\n", "4\nheading\n1\nHello Geblang and code.\ntwo\ntrue\ntrue\nfalse\n")
}

func TestParityEncodingModule(t *testing.T) {
	runParity(t, `import io;
import encoding;
let encoded = encoding.base64Encode("hello");
io.println(encoded);
io.println(encoding.base64Decode(encoded));
io.println(encoding.urlEncode("a b&c=d"));
io.println(encoding.htmlEscape("<b>hi</b>"));
`, "aGVsbG8=\nhello\na+b%26c%3Dd\n&lt;b&gt;hi&lt;/b&gt;\n")
}

func TestParityURLModule(t *testing.T) {
	runParity(t, `import io;
import url;
let parts = url.parse("https://example.test:8443/api/v1/items?tag=a&tag=b&q=hello+world#top");
io.println(parts["scheme"]);
io.println(parts["host"]);
io.println(parts["port"]);
io.println(parts["path"]);
io.println(parts["query"]["q"]);
io.println(parts["query"]["tag"].length() as string);
io.println(parts["fragment"]);
io.println(url.encode("a b&c=d"));
io.println(url.decode("a+b%26c%3Dd"));
io.println(url.joinPath("https://example.test/api/", "/v1/", "items"));
io.println(url.stringify({
    "scheme": "https",
    "host": "example.test",
    "path": "/search",
    "query": {"q": "hello world", "tag": ["a", "b"]},
    "fragment": "top"
}));
let parsed = url.URL("https://example.test/api/v1/items?tag=a&tag=b&q=hello+world#top");
io.println(parsed.scheme());
io.println(parsed.host());
io.println(parsed.path());
io.println(parsed.query()["tag"].length() as string);
io.println(parsed.withPath("/api/v2/items").withQuery({"page": "2"}).toString());
io.println(parsed.resolve("../users/42").toString());
io.println(url.URL({"scheme": "https", "host": "example.test", "path": "/built"}).toString());
io.println(url.URL("https://example.test/a/../b?z=3&a=1").normalize().toString());
`, "https\nexample.test\n8443\n/api/v1/items\nhello world\n2\ntop\na+b%26c%3Dd\na b&c=d\nhttps://example.test/api/v1/items\nhttps://example.test/search?q=hello+world&tag=a&tag=b#top\nhttps\nexample.test\n/api/v1/items\n2\nhttps://example.test/api/v2/items?page=2#top\nhttps://example.test/api/users/42\nhttps://example.test/built\nhttps://example.test/b?a=1&z=3\n")
}

func TestParityDatetimeAdditions(t *testing.T) {
	runParity(t, `import io;
import datetime;
let parsed = datetime.parse("1970-01-01T00:00:00Z");
io.println(datetime.addDays(parsed, 1) as string);
io.println(datetime.addMonths(parsed, 1) as string);
io.println(datetime.addYears(parsed, 1) as string);
let delta = datetime.diff(parsed, datetime.addSeconds(parsed, 90061));
io.println(delta["days"] as string);
io.println(delta["hours"] as string);
io.println(delta["minutes"] as string);
io.println(delta["seconds"] as string);
io.println(datetime.toLocal(parsed, "UTC"));
io.println(datetime.toUtc(parsed));
let now = datetime.now();
io.println(now.hasKey("year") as string);
`, "86400\n2678400\n31536000\n1\n1\n1\n1\n1970-01-01T00:00:00Z\n1970-01-01T00:00:00Z\ntrue\n")
}

func TestParityDatetimeValueClasses(t *testing.T) {
	runParity(t, `import io;
import datetime;
let start = datetime.Instant("1970-01-01T00:00:00Z");
let duration = datetime.Duration(90061);
let later = start.add(duration);
io.println(start.unix() as string);
io.println(later.toString());
io.println(later.format("2006-01-02"));
let diff = start.diff(later);
io.println(diff.seconds() as string);
let parts = diff.toDict();
io.println(parts["days"] as string);
io.println(parts["hours"] as string);
io.println(parts["minutes"] as string);
io.println(parts["seconds"] as string);
let utc = datetime.Zone("UTC");
io.println(utc.name());
io.println(later.toLocal(utc));
io.println(utc.offsetAt(later) as string);
io.println(start.addDays(1).unix() as string);
io.println(start.addMonths(1).unix() as string);
io.println(start.addYears(1).unix() as string);
`, "0\n1970-01-02T01:01:01Z\n1970-01-02\n90061\n1\n1\n1\n1\nUTC\n1970-01-02T01:01:01Z\n0\n86400\n2678400\n31536000\n")
}

func TestParityUUIDModule(t *testing.T) {
	runParity(t, `import io;
import uuid;
let a = uuid.v4();
let b = uuid.v7();
io.println(a.length() as string);
io.println(a.get(8));
io.println(a.get(13));
io.println(a.get(14));
io.println(a.get(18));
io.println(a.get(23));
io.println(b.length() as string);
io.println(b.get(14));
`, "36\n-\n-\n4\n-\n-\n36\n7\n")
}

func TestParityUUIDExtended(t *testing.T) {
	runParity(t, `import io;
import uuid;
# nil UUID
io.println(uuid.nil());
# namespace constants are stable UUIDs
io.println(uuid.isValid(uuid.namespaceDNS()));
io.println(uuid.isValid(uuid.namespaceURL()));
# isValid
io.println(uuid.isValid("not-a-uuid"));
io.println(uuid.isValid("f47ac10b-58cc-4372-a567-0e02b2c3d479"));
# parse normalises to lowercase
let norm = uuid.parse("F47AC10B-58CC-4372-A567-0E02B2C3D479");
io.println(norm);
# v5 is deterministic
let id5a = uuid.v5(uuid.namespaceDNS(), "example.com");
let id5b = uuid.v5(uuid.namespaceDNS(), "example.com");
io.println(id5a == id5b);
# v3 is deterministic
let id3a = uuid.v3(uuid.namespaceDNS(), "example.com");
let id3b = uuid.v3(uuid.namespaceDNS(), "example.com");
io.println(id3a == id3b);
# v1 produces a valid UUID
io.println(uuid.isValid(uuid.v1()));
# toBytes / fromBytes round-trip
let id = uuid.v4();
let raw = uuid.toBytes(id);
io.println(uuid.fromBytes(raw) == id);
# ULID is 26 characters
let ul = uuid.ulid();
io.println(ul.length() as string);
`, "00000000-0000-0000-0000-000000000000\ntrue\ntrue\nfalse\ntrue\nf47ac10b-58cc-4372-a567-0e02b2c3d479\ntrue\ntrue\ntrue\ntrue\n26\n")
}

func TestParityTemplateModule(t *testing.T) {
	runParity(t, `import io;
import template;
io.println(template.renderString("<p>{{.name}}</p>", {"name": "<Ada>"}));
let tmpl = template.Template("Hello {{.name}}", "greeting");
io.println(tmpl.name());
io.println(tmpl.render({"name": "Grace"}));
let engine = template.Engine("templates");
io.println(engine.dir());
`, "<p>&lt;Ada&gt;</p>\ngreeting\nHello Grace\ntemplates\n")
}

func TestParityListMethods(t *testing.T) {
	runParity(t, `import io;
let a = [1, 2, 3];
io.println(a.first() as string);
io.println(a.last() as string);
io.println(a.indexOf(2) as string);
io.println(a.contains(3) as string);
let b = a.push(4);
io.println(b.length() as string);
let c = b.pop();
io.println(c.length() as string);
let d = a.insert(1, 99);
io.println(d.get(1) as string);
let e = a.removeAt(1);
io.println(e.length() as string);
let f = a.concat([4, 5]);
io.println(f.length() as string);
`, "1\n3\n1\ntrue\n4\n3\n99\n2\n5\n")
}

func TestParityCollectionsModule(t *testing.T) {
	runParity(t, `import io;
import collections;
let a = [1, 2, 3, 4, 5];
io.println(collections.length(a) as string);
io.println(collections.isEmpty(a) as string);
io.println(collections.isEmpty([]) as string);
io.println(collections.contains(a, 3) as string);
io.println(collections.contains(a, 9) as string);
let rev = collections.reverse(a);
io.println(rev.get(0) as string);
let sorted = collections.sort([3,1,2]);
io.println(sorted.get(0) as string);
io.println(collections.join(["a","b","c"], ","));
let doubled = collections.map(a, func(int x): int { return x * 2; });
io.println(doubled.get(0) as string);
let evens = collections.filter(a, func(int x): bool { return x % 2 == 0; });
io.println(evens.length() as string);
let sum = collections.reduce(a, func(int acc, int x): int { return acc + x; }, 0);
io.println(sum as string);
let found = collections.find(a, func(int x): bool { return x > 3; });
io.println(found as string);
io.println(collections.any(a, func(int x): bool { return x > 4; }) as string);
io.println(collections.all(a, func(int x): bool { return x > 0; }) as string);
let flat = collections.flatten([[1,2],[3,4]]);
io.println(flat.length() as string);
let uniq = collections.unique([1,2,2,3,1]);
io.println(uniq.length() as string);
let zipped = collections.zip([1,2], [3,4]);
io.println(zipped.length() as string);
let ns = collections.sorted([3,1,2]);
io.println(ns.get(0) as string);
	`, "5\nfalse\ntrue\ntrue\nfalse\n5\n1\na,b,c\n2\n2\n15\n4\ntrue\ntrue\n4\n3\n2\n1\n")
}

func TestParityCollectionsLazyHelpers(t *testing.T) {
	runParity(t, `import io;
import collections;
let stream = collections.lazyMap(collections.range(1, 10), func(int x): int {
    return x * 2;
});
let evens = collections.lazyFilter(stream, func(int x): bool {
    return x % 4 == 0;
});
for (n in collections.take(evens, 3)) {
    io.println(n as string);
}
for (n in collections.range(5, 0, -2)) {
    io.println(n as string);
}
for (n in collections.take([7, 8, 9], 2)) {
    io.println(n as string);
}
	`, "4\n8\n12\n5\n3\n1\n7\n8\n")
}

func TestParitySetLiteralAndMethods(t *testing.T) {
	runParity(t, `import io;
import collections;
let a = {1, 2, 2, 3};
io.println(typeof(a));
io.println(a.length() as string);
io.println(a.isEmpty() as string);
io.println(a.contains(2) as string);
io.println(a.contains(9) as string);
io.println(collections.length(a) as string);
io.println(collections.contains(a, 3) as string);
let b = a.add(4);
io.println(b.length() as string);
let c = b.remove(2);
io.println(c.contains(2) as string);
io.println(c.toList().length() as string);
let u = {1, 2}.union({2, 3});
io.println(u.length() as string);
let i = {1, 2}.intersection({2, 3});
io.println(i.contains(2) as string);
io.println(i.contains(1) as string);
let d = {1, 2, 3}.difference({2});
io.println(d.contains(2) as string);
io.println(d.length() as string);
io.println({1, 2} == {2, 1});
`, "set\n3\nfalse\ntrue\nfalse\n3\ntrue\n4\nfalse\n3\n3\ntrue\nfalse\nfalse\n2\ntrue\n")
}

func TestParityDestructuring(t *testing.T) {
	runParity(t, `import io;
let [a, b, c] = [10, 20, 30];
io.println(a as string);
io.println(b as string);
io.println(c as string);
let {name, age} = {"name": "Alice", "age": 42};
io.println(name);
io.println(age as string);
func coords(): list { return [7, 8]; }
let [x, y] = coords();
io.println(x as string);
io.println(y as string);
let first = 0;
let second = 0;
[first, second] = [100, 200];
io.println(first as string);
io.println(second as string);
name = "";
age = 0;
{name, age} = {"name": "Bob", "age": 51};
io.println(name);
io.println(age as string);
`, "10\n20\n30\nAlice\n42\n7\n8\n100\n200\nBob\n51\n")
}

func TestParityTypeAliases(t *testing.T) {
	runParity(t, `import io;
type UserId = string;
type Money = decimal;
type Numbers = int[];
UserId id = "u-1";
Money price = 12.5;
Numbers nums = [1, 2, 3];
func label(UserId value): UserId { return "id:" + value; }
io.println(label(id));
io.println(price.format(2));
io.println(nums.length() as string);
io.println(("42" as UserId).toInt() + 1);
`, "id:u-1\n12.50\n3\n43\n")
}

func TestParityCryptModule(t *testing.T) {
	runParity(t, `import io;
import crypt;
io.println(crypt.sha256("hello"));
io.println(crypt.sha512("hello").length() as string);
io.println(crypt.md5("hello"));
io.println(crypt.sha1("hello"));
io.println(crypt.sha3_256("hello").length() as string);
io.println(crypt.blake2b("hello").length() as string);
io.println(crypt.crc32("hello") > 0);
io.println(crypt.hmacSha256("key", "data").length() as string);
io.println(crypt.base64Encode("hi"));
io.println(crypt.base64Decode(crypt.base64Encode("hi")));
`, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824\n128\n5d41402abc4b2a76b9719d911017c592\naaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d\n64\n64\ntrue\n64\naGk=\nhi\n")
}

func TestParityStringFormat(t *testing.T) {
	runParity(t, `import io;
io.println("Hello, %s!".format("World"));
io.println("x=%d, y=%d".format(3, 7));
io.println("pi=%.2f".format(3.14159));
io.println("%x".format(255));
io.println("%05d".format(42));
`, "Hello, World!\nx=3, y=7\npi=3.14\nff\n00042\n")
}

func TestParityBcrypt(t *testing.T) {
	runParity(t, `import io;
import crypt;
let hash = crypt.bcryptHash("secret");
io.println(crypt.bcryptVerify("secret", hash) as string);
io.println(crypt.bcryptVerify("wrong", hash) as string);
let argon = crypt.argon2idHash("secret", {"memory": 64, "time": 1, "parallelism": 1, "keyLength": 16, "saltLength": 8});
io.println(argon.startsWith("$argon2id$") as string);
io.println(crypt.argon2idVerify("secret", argon) as string);
io.println(crypt.argon2idVerify("wrong", argon) as string);
`, "true\nfalse\ntrue\ntrue\nfalse\n")
}

func TestParityCompressModule(t *testing.T) {
	runParity(t, `import io;
import compress;
import bytes;
let original = bytes.fromString("hello world");
let compressed = compress.gzip(original);
let decompressed = compress.gunzip(compressed);
io.println(bytes.toString(decompressed));
io.println(compressed.length() > 0 as string);
`, "hello world\ntrue\n")
}

func TestParityVariadic(t *testing.T) {
	runParity(t, `import io;
func sum(int ...values): int {
    let total = 0;
    for (int v in values) {
        total = total + v;
    }
    return total;
}
io.println(sum() as string);
io.println(sum(1) as string);
io.println(sum(1, 2, 3) as string);
io.println(sum(10, 20, 30, 40) as string);
`, "0\n1\n6\n100\n")
}

func TestParityVariadicWithRequired(t *testing.T) {
	runParity(t, `import io;
func greet(string prefix, string ...names): string {
    let result = prefix;
    for (string n in names) {
        result = result + " " + n;
    }
    return result;
}
io.println(greet("Hello"));
io.println(greet("Hi", "Alice"));
io.println(greet("Hey", "Bob", "Carol", "Dave"));
`, "Hello\nHi Alice\nHey Bob Carol Dave\n")
}

func TestParitySpreadList(t *testing.T) {
	runParity(t, `import io;
let a = [1, 2, 3];
let b = [4, 5, 6];
let c = [...a, ...b];
io.println(c.length() as string);
io.println(c[0] as string);
io.println(c[5] as string);
let d = [0, ...a, 4];
io.println(d.length() as string);
io.println(d[0] as string);
io.println(d[4] as string);
`, "6\n1\n6\n5\n0\n4\n")
}

func TestParitySpreadCall(t *testing.T) {
	runParity(t, `import io;
func add(int a, int b, int c): int {
    return a + b + c;
}
let args = [10, 20, 30];
io.println(add(...args) as string);
io.println(add(...[1, 2, 3]) as string);
`, "60\n6\n")
}

func TestParityJWT(t *testing.T) {
	runParity(t, `import io;
import crypt;
let payload = {"sub": "user-1", "role": "admin"};
let secret = "supersecret";
let token = crypt.jwtSign(payload, secret);
io.println(token.contains(".") as string);
let verified = crypt.jwtVerify(token, secret);
io.println(verified["sub"]);
io.println(verified["role"]);
let bad = crypt.jwtVerify(token, "wrongsecret");
io.println((bad == null) as string);
let decoded = crypt.jwtDecode(token);
io.println(decoded["header"]["alg"]);
io.println(decoded["payload"]["sub"]);
`, "true\nuser-1\nadmin\ntrue\nHS256\nuser-1\n")
}

func TestParityRSAKeyAndJWT(t *testing.T) {
	runParity(t, `import io;
import crypt;
let privPem = crypt.generateRsaKey(2048);
let pubPem = crypt.publicKey(privPem);
io.println(privPem.contains("PRIVATE KEY") as string);
io.println(pubPem.contains("PUBLIC KEY") as string);
let payload = {"sub": "alice", "iss": "test"};
let token = crypt.jwtSignRS256(payload, privPem);
io.println(token.contains(".") as string);
let verified = crypt.jwtVerifyRS256(token, pubPem);
io.println(verified["sub"]);
let bad = crypt.jwtVerifyRS256(token, crypt.publicKey(crypt.generateRsaKey(2048)));
io.println((bad == null) as string);
let decoded = crypt.jwtDecode(token);
io.println(decoded["header"]["alg"]);
`, "true\ntrue\ntrue\nalice\ntrue\nRS256\n")
}

func TestParityECKeyAndJWT(t *testing.T) {
	runParity(t, `import io;
import crypt;
let privPem = crypt.generateEcKey("P-256");
let pubPem = crypt.publicKey(privPem);
io.println(privPem.contains("PRIVATE KEY") as string);
io.println(pubPem.contains("PUBLIC KEY") as string);
let payload = {"sub": "bob", "role": "user"};
let token = crypt.jwtSignES256(payload, privPem);
io.println(token.contains(".") as string);
let verified = crypt.jwtVerifyES256(token, pubPem);
io.println(verified["sub"]);
let bad = crypt.jwtVerifyES256(token, crypt.publicKey(crypt.generateEcKey("P-256")));
io.println((bad == null) as string);
let decoded = crypt.jwtDecode(token);
io.println(decoded["header"]["alg"]);
`, "true\ntrue\ntrue\nbob\ntrue\nES256\n")
}

func TestParityEd25519Key(t *testing.T) {
	runParity(t, `import io;
import crypt;
let privPem = crypt.generateEd25519Key();
let pubPem = crypt.publicKey(privPem);
io.println(privPem.contains("PRIVATE KEY") as string);
io.println(pubPem.contains("PUBLIC KEY") as string);
`, "true\ntrue\n")
}

func TestParitySelfSignedCert(t *testing.T) {
	runParity(t, `import io;
import crypt;
let result = crypt.generateSelfSignedCert({
    "subject": {"commonName": "test.local", "organization": "TestOrg"},
    "dnsNames": ["test.local", "localhost"],
    "validDays": 30,
    "keyType": "EC-P256"
});
io.println(result["cert"].contains("CERTIFICATE") as string);
io.println(result["key"].contains("PRIVATE KEY") as string);
let parsed = crypt.parseCert(result["cert"]);
io.println(parsed["subject"]["commonName"]);
io.println(parsed["subject"]["organization"]);
io.println(parsed["keyType"]);
io.println(parsed["isCA"] as string);
`, "true\ntrue\ntest.local\nTestOrg\nEC\ntrue\n")
}

func TestParityGenerateCsr(t *testing.T) {
	runParity(t, `import io;
import crypt;
let key = crypt.generateEcKey("P-384");
let csr = crypt.generateCsr({
    "key": key,
    "subject": {"commonName": "example.com"},
    "dnsNames": ["example.com", "www.example.com"]
});
io.println(csr.contains("CERTIFICATE REQUEST") as string);
`, "true\n")
}

func TestParityAsyncFuncAndAwait(t *testing.T) {
	runParity(t, `import io;
async func double(int x): int {
    return x * 2;
}
let task = double(21);
io.println(typeof(task));
io.println(await task);
`, "Task\n42\n")
}

func TestParityCallableDecoratorOnAsyncFunction(t *testing.T) {
	runParity(t, `import io;
import reflect;

func passthrough(any next): any {
    return func(string name): any {
        return next(name);
    };
}

@passthrough
async func greet(string name): string {
    return "hello " + name;
}

let task = greet("Ada");
io.println(typeof(task));
io.println(await task);
io.println(reflect.decorators(greet)[0]["name"]);
`, "Task\nhello Ada\npassthrough\n")
}

func TestParityAsyncFuncOnReturn(t *testing.T) {
	runParity(t, `import io;
async func compute(): int {
    return 7;
}
let t1 = compute();
let t2 = compute();
io.println(await t1);
io.println(await t2);
`, "7\n7\n")
}

func TestParityAsyncRunAndAwait(t *testing.T) {
	runParity(t, `import io;
import async;
let task = async.run(func(): int {
    return 99;
});
io.println(await task);
`, "99\n")
}

func TestParityAsyncAwaitExpr(t *testing.T) {
	runParity(t, `import io;
async func greet(): string {
    return "hello";
}
io.println(await greet());
`, "hello\n")
}

func TestParityAsyncTaskDoneMethod(t *testing.T) {
	runParity(t, `import io;
async func noop(): void {}
let t = noop();
io.println(await t);
io.println(t.done() as string);
`, "null\ntrue\n")
}

func TestParityAsyncFuncLiteral(t *testing.T) {
	runParity(t, `import io;
import async;
let fn = async func(): int { return 5; };
let t = async.run(fn);
io.println(await t);
`, "5\n")
}

func TestParityGenericAsyncFunctionKeepsTypeBindings(t *testing.T) {
	runParity(t, `import io;
async func check<T>(T value): bool {
    return value instanceof T;
}

io.println(await check("hello"));
io.println(await check(42));
`, "true\ntrue\n")
}

func TestParityDecoratedGenericAsyncFunction(t *testing.T) {
	runParity(t, `import io;
import reflect;

func logged(any next): any {
    return func(any value): any {
        io.println("calling");
        return next(value);
    };
}

@logged
async func identify<T>(T value): bool {
    return value instanceof T;
}

io.println(await identify("hello"));
io.println(await identify(42));
io.println(reflect.decorators(identify)[0]["name"]);
`, "calling\ntrue\ncalling\ntrue\nlogged\n")
}

func TestParityDecoratedGenericAsyncMethod(t *testing.T) {
	runParity(t, `import io;

func traced(any f): any {
    return func(): any {
        io.println("trace");
        return f();
    };
}

class Box<T> {
    T val;
    func Box(T v) { this.val = v; }

    @traced
    async func isT(): bool {
        return this.val instanceof T;
    }
}

Box<string> b = Box("hi");
io.println(await b.isT());
Box<int> bi = Box(7);
io.println(await bi.isT());
`, "trace\ntrue\ntrace\ntrue\n")
}

func TestParityMathExtensions(t *testing.T) {
	runParity(t, `import io;
import math;
io.println(math.log2(8.0));
io.println(math.trunc(3.9));
io.println(math.trunc(-3.9));
io.println(math.sign(-5));
io.println(math.sign(0));
io.println(math.sign(7));
io.println(math.cbrt(27.0));
io.println(math.hypot(3.0, 4.0));
io.println(math.isNaN(math.nan()));
io.println(math.isInf(math.inf()));
io.println(math.isNaN(1.0));
io.println(math.isInf(1.0));
`, "3\n3\n-3\n-1\n0\n1\n3\n5\ntrue\ntrue\nfalse\nfalse\n")
}

// TestParityMathIsPrime covers the small / known-prime cases plus
// negative, zero, one, and the edge between Int and SmallInt.
func TestParityMathIsPrime(t *testing.T) {
	runParity(t, `import io;
import math;
io.println(math.isPrime(2));
io.println(math.isPrime(3));
io.println(math.isPrime(4));
io.println(math.isPrime(17));
io.println(math.isPrime(97));
io.println(math.isPrime(1));
io.println(math.isPrime(0));
io.println(math.isPrime(-7));
io.println(math.isPrime(1000003));
io.println(math.isPrime(1000004));
`, "true\ntrue\nfalse\ntrue\ntrue\nfalse\nfalse\nfalse\ntrue\nfalse\n")
}

func TestParitySysInfo(t *testing.T) {
	runParity(t, `import io;
import sys;
let h = sys.hostname();
let p = sys.pid();
let pl = sys.platform();
let ar = sys.arch();
let td = sys.tmpdir();
io.println(h.length() > 0);
io.println(p > 0);
io.println(pl.length() > 0);
io.println(ar.length() > 0);
io.println(td.length() > 0);
`, "true\ntrue\ntrue\ntrue\ntrue\n")
}

func TestParityCollectionsChunk(t *testing.T) {
	runParity(t, `import io;
import collections;
let chunks = collections.chunk([1, 2, 3, 4, 5], 2);
io.println(chunks.length());
io.println(chunks[0][0]);
io.println(chunks[0][1]);
io.println(chunks[1][0]);
io.println(chunks[1][1]);
io.println(chunks[2][0]);
`, "3\n1\n2\n3\n4\n5\n")
}

func TestParityCollectionsPartition(t *testing.T) {
	runParity(t, `import io;
import collections;
let parts = collections.partition([1, 2, 3, 4, 5], func(int x): bool { return x % 2 == 0; });
io.println(parts[0]);
io.println(parts[1]);
`, "[2, 4]\n[1, 3, 5]\n")
}

func TestParityCollectionsGroupBy(t *testing.T) {
	runParity(t, `import io;
import collections;
let groups = collections.groupBy(["a", "bb", "c", "dd"], func(string s): int { return s.length(); });
io.println(groups[1].length());
io.println(groups[2].length());
`, "2\n2\n")
}

// runParityStateful is like runParity but wires an evaluator as the VM's
// StatefulNativeCaller so modules like schema and serde work in both paths.
func runParityStateful(t *testing.T, source string, want string) {
	t.Helper()

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	if _, err := evaluator.New(&evOut).Eval(program); err != nil {
		t.Fatalf("evaluator error: %v", err)
	}

	src := []byte(source)
	chunk, err := bytecode.Compile(program, src, "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	vm := bytecode.NewVM(chunk, &vmOut)
	vm.SetStatefulNativeCaller(evaluator.New(&vmOut))
	if err := vm.Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}

	if evOut.String() != vmOut.String() {
		t.Errorf("output mismatch:\n  evaluator: %q\n  vm:        %q", evOut.String(), vmOut.String())
	}
	if want != "" && evOut.String() != want {
		t.Errorf("wrong output: got %q, want %q", evOut.String(), want)
	}
}

// runParityStatefulWithFile writes fileContent to a temp file, substitutes
// its path into source (replacing the placeholder "TMPFILE"), then runs
// through both evaluator and VM in stateful mode.
func runParityStatefulWithFile(t *testing.T, source string, fileContent string, want string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "parity_*.txt")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(fileContent); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	source = strings.ReplaceAll(source, "TMPFILE", f.Name())
	runParityStateful(t, source, want)
}

func TestParityFileReadClose(t *testing.T) {
	runParityStatefulWithFile(t, `import io;
let h = io.open("TMPFILE", "r");
let line = io.readLine(h);
io.close(h);
io.println(line);
`, "hello file\n", "hello file\n")
}

func TestParityFileReadAllLines(t *testing.T) {
	runParityStatefulWithFile(t, `import io;
let h = io.open("TMPFILE", "r");
let lines = io.readLines(h);
io.close(h);
io.println(lines.length());
io.println(lines[0]);
io.println(lines[1]);
`, "alpha\nbeta\n", "2\nalpha\nbeta\n")
}

func TestParityFileWriteAndRead(t *testing.T) {
	runParityStatefulWithFile(t, `import io;
let path = "TMPFILE";
let w = io.open(path, "w");
io.write(w, "written line");
io.close(w);
let r = io.open(path, "r");
let content = io.readLine(r);
io.close(r);
io.println(content);
`, "", "written line\n")
}

func TestParityIOStreamsAndCapture(t *testing.T) {
	runParityStateful(t, `import io;
let mem = io.memory("seed");
io.write(mem, "-text");
io.println(io.toString(mem));
let captured = io.captureStdout();
io.println("hidden");
let text = io.toString(captured);
io.close(captured);
io.print(text);
let redirected = io.memory();
let restore = io.redirectStdout(redirected);
io.println("redirected");
restore();
io.print(io.toString(redirected));
`, "seed-text\nhidden\nredirected\n")
}

func TestParityDictContains(t *testing.T) {
	runParity(t, `import io;
let d = {"a": 1, "b": 2};
io.println(d.contains("a"));
io.println(d.contains("c"));
io.println(d.hasKey("b"));
io.println(d.hasKey("z"));
`, "true\nfalse\ntrue\nfalse\n")
}

func TestParityBytesContains(t *testing.T) {
	runParity(t, `import io;
import bytes;
let b = bytes.fromString("hello");
io.println(b.contains(104));
io.println(b.contains(122));
`, "true\nfalse\n")
}

func TestParityBytesSlice(t *testing.T) {
	runParity(t, `import io;
import bytes;
let b = bytes.fromHex("0102030405060708090a0b0c0d0e0f10");
io.println(bytes.toHex(b.slice(0, 5)));
io.println(bytes.toHex(b.slice(5)));
io.println(bytes.toHex(b.slice(2, 7)));
io.println(bytes.toHex(b.slice(-3)));
io.println(bytes.toHex(b.slice(0, 0)));
io.println(bytes.toHex(b.slice(100)));
io.println(bytes.toHex(b.slice(-100, 3)));
`, "0102030405\n060708090a0b0c0d0e0f10\n0304050607\n0e0f10\n\n\n010203\n")
}

func TestParityInstanceofListAnyAndUnion(t *testing.T) {
	runParity(t, `import io;
let mixed = [1, "f", true];
let strs = ["a", "b", "c"];
io.println(mixed instanceof list<any>);
io.println(strs instanceof list<any>);
io.println(mixed instanceof list<string|bool|int>);
io.println(mixed instanceof list<string>);
io.println(strs instanceof list<string>);
io.println([] instanceof list<any>);
io.println({"a": 1, "b": 2} instanceof dict<string, any>);
io.println({"a": 1, "b": "x"} instanceof dict<string, int|string>);
`, "true\ntrue\ntrue\nfalse\ntrue\ntrue\ntrue\ntrue\n")
}

func TestParityInstanceofTaggedSatisfiesAnyAndUnion(t *testing.T) {
	runParity(t, `import io;
list<int> xs = [1, 2, 3];
io.println(xs instanceof list<int>);
io.println(xs instanceof list<any>);
io.println(xs instanceof list<int|string>);
io.println(xs instanceof list<string>);
`, "true\ntrue\ntrue\nfalse\n")
}

func TestParityDictKeysInsertionOrder(t *testing.T) {
	runParity(t, `import io;
dict<string, int> d = {};
d["zeta"] = 1;
d["mu"] = 2;
d["alpha"] = 3;
io.println(d.keys());
io.println(d.values());
let lit = {"first": 1, "second": 2, "third": 3};
io.println(lit.keys());
`, "[\"zeta\", \"mu\", \"alpha\"]\n[1, 2, 3]\n[\"first\", \"second\", \"third\"]\n")
}

func TestParityYamlPreservesMappingOrder(t *testing.T) {
	runParity(t, `import io;
import yaml;
let p = yaml.parse("services:\n  Gamma: {}\n  Alpha: {}\n  Beta: {}\n") as dict<string, any>;
let svcs = p["services"] as dict<string, any>;
io.println(svcs.keys());
`, "[\"Gamma\", \"Alpha\", \"Beta\"]\n")
}

func TestParityDefaultArgInReturnPosition(t *testing.T) {
	runParity(t, `import io;
func helper(string greeting = "hi"): string {
    return greeting;
}
func caller(): string {
    return helper();
}
func callerWithArg(): string {
    return helper("howdy");
}
io.println(caller());
io.println(callerWithArg());
`, "hi\nhowdy\n")
}

func TestParityChainedMethodOnSmallIntResult(t *testing.T) {
	runParity(t, `import io;
let s = "hello";
io.println(s.length().toString());
let xs = [1, 2, 3];
io.println(xs.length().toString());
let d = {"a": 1, "b": 2};
io.println(d.length().toString());
let n = "42".length();
io.println(n.toString());
`, "5\n3\n2\n2\n")
}

func TestParityDefaultArgInExpressionContexts(t *testing.T) {
	runParity(t, `import io;
func two(int a, int b = 10): int {
    return a + b;
}
io.println(two(5));
io.println(two(5, 20));
let xs = [two(1), two(1, 1)];
io.println(xs);
`, "15\n25\n[11, 2]\n")
}

func TestParityXML(t *testing.T) {
	runParity(t, `import io;
import xml;
let doc = xml.parse("<root><item>hello</item><item>world</item></root>");
io.println(doc["name"]);
io.println(doc["children"][0]["text"]);
io.println(doc["children"][1]["text"]);
`, "root\nhello\nworld\n")
}

func TestParityYAML(t *testing.T) {
	runParity(t, `import io;
import yaml;
let text = "name: alice\nage: 30\n";
let parsed = yaml.parse(text);
io.println(parsed["name"]);
io.println(parsed["age"]);
let out = yaml.stringify({"x": 1});
io.println(out.contains("x"));
`, "alice\n30\ntrue\n")
}

func TestParitySecrets(t *testing.T) {
	runParity(t, `import io;
import secrets;
let b = secrets.randomBytes(8);
io.println(b.length());
let h = secrets.randomHex(4);
io.println(h.length());
let n = secrets.randomInt(1, 100);
io.println(n >= 1);
io.println(n <= 100);
let a = secrets.randomBase64(6);
io.println(a.length() > 0);
`, "8\n8\ntrue\ntrue\ntrue\n")
}

func TestParityDatetimeCore(t *testing.T) {
	runParity(t, `import io;
import datetime;
let ts = datetime.nowUnix();
io.println(ts > 0);
let formatted = datetime.unix(ts);
io.println(formatted.length() > 0);
let back = datetime.parse(formatted);
io.println(back > 0);
let d = datetime.format(ts, "2006-01-02");
io.println(d.length() == 10);
`, "true\ntrue\ntrue\ntrue\n")
}

func TestParityDatetimeMake(t *testing.T) {
	runParity(t, `import io;
import datetime;
let ts = datetime.make(2024, 1, 15);
let d = datetime.formatDate(ts);
io.println(d);
let ts2 = datetime.make(2024, 6, 1, 12, 30, 0);
let t2 = datetime.formatTime(ts2);
io.println(t2);
let r = datetime.formatRFC3339(ts);
io.println(r.length() > 0);
let back = datetime.parseRFC3339(r);
io.println(back == ts);
`, "2024-01-15\n12:30:00\ntrue\ntrue\n")
}

func TestParityDatetimeHelpers(t *testing.T) {
	runParity(t, `import io;
import datetime;
io.println(datetime.weekdayName(0));
io.println(datetime.weekdayName(1));
io.println(datetime.weekdayName(6));
io.println(datetime.monthName(1));
io.println(datetime.monthName(12));
`, "Sunday\nMonday\nSaturday\nJanuary\nDecember\n")
}

func TestParitySchemaValidate(t *testing.T) {
	runParityStateful(t, `import io;
import schema;
let s = {"type": "string"};
let r1 = schema.validate("hello", s);
io.println(r1["valid"]);
let r2 = schema.validate(42, s);
io.println(r2["valid"]);
io.println(r2["errors"].length() > 0);
`, "true\nfalse\ntrue\n")
}

func TestParitySerdeRoundtrip(t *testing.T) {
	runParityStateful(t, `import io;
import serde;
let data = {"name": "bob", "score": 99};
let json_str = serde.stringify("json", data);
io.println(json_str.contains("bob"));
let parsed = serde.parse("json", json_str);
io.println(parsed["name"]);
io.println(parsed["score"]);
`, "true\nbob\n99\n")
}

func TestParityArgsParse(t *testing.T) {
	runParity(t, `import io;
import args;
let schema = {
    "verbose": {"type": "bool", "short": "v", "default": false, "help": "Verbose mode"},
    "output":  {"type": "string", "short": "o", "default": "out.txt", "help": "Output file"},
    "count":   {"type": "int", "default": 1, "help": "Count"},
};
let r = args.parse(["--verbose", "--output", "foo.txt", "--count", "3", "pos1", "pos2"], schema);
io.println(r["verbose"]);
io.println(r["output"]);
io.println(r["count"]);
io.println(r["_"][0]);
io.println(r["_"][1]);
io.println(r["error"] == null);
`, "true\nfoo.txt\n3\npos1\npos2\ntrue\n")
}

func TestParityArgsShortFlags(t *testing.T) {
	runParity(t, `import io;
import args;
let schema = {
    "verbose": {"type": "bool", "short": "v"},
    "output":  {"type": "string", "short": "o", "default": ""},
};
let r = args.parse(["-v", "-o", "bar.txt"], schema);
io.println(r["verbose"]);
io.println(r["output"]);
io.println(r["_"].length());
`, "true\nbar.txt\n0\n")
}

func TestParityArgsInlineValue(t *testing.T) {
	runParity(t, `import io;
import args;
let schema = {
    "name": {"type": "string", "default": ""},
    "count": {"type": "int", "default": 0},
};
let r = args.parse(["--name=alice", "--count=5"], schema);
io.println(r["name"]);
io.println(r["count"]);
`, "alice\n5\n")
}

func TestParityArgsDefaults(t *testing.T) {
	runParity(t, `import io;
import args;
let schema = {
    "flag":  {"type": "bool", "default": false},
    "value": {"type": "string", "default": "hello"},
    "num":   {"type": "int", "default": 42},
};
let r = args.parse([], schema);
io.println(r["flag"]);
io.println(r["value"]);
io.println(r["num"]);
io.println(r["_"].length());
`, "false\nhello\n42\n0\n")
}

func TestParityArgsHelp(t *testing.T) {
	runParity(t, `import io;
import args;
let schema = {
    "verbose": {"type": "bool", "short": "v", "help": "Enable verbose mode"},
};
let h = args.help("mytool", schema);
io.println(h.contains("mytool"));
io.println(h.contains("verbose"));
io.println(h.contains("-v"));
`, "true\ntrue\ntrue\n")
}

func TestParityDotenvParse(t *testing.T) {
	runParityStateful(t, `import io;
import dotenv;
let text = "KEY=value\nNAME=alice\nEMPTY=\n# comment\nQUOTED=\"hello world\"\nSINGLE='raw value'\n";
let env = dotenv.parse(text);
io.println(env["KEY"]);
io.println(env["NAME"]);
io.println(env["EMPTY"]);
io.println(env["QUOTED"]);
io.println(env["SINGLE"]);
`, "value\nalice\n\nhello world\nraw value\n")
}

func TestParityDotenvParseExport(t *testing.T) {
	runParityStateful(t, `import io;
import dotenv;
let env = dotenv.parse("export FOO=bar\nexport BAZ=qux\n");
io.println(env["FOO"]);
io.println(env["BAZ"]);
`, "bar\nqux\n")
}

func TestParityDotenvParseInlineComment(t *testing.T) {
	runParityStateful(t, `import io;
import dotenv;
let env = dotenv.parse("KEY=value # this is a comment\n");
io.println(env["KEY"]);
`, "value\n")
}

func TestParityMatchErrorIncludesValue(t *testing.T) {
	runParity(t, `import io;
try {
    match (42) {
        case 1: io.println("one");
        case 2: io.println("two");
    };
} catch (MatchError e) {
    io.println(e.message.contains("42"));
    io.println(e.message.contains("int"));
    io.println(e.message.contains("default"));
}
`, "true\ntrue\ntrue\n")
}

func TestParityMatchErrorWithDefault(t *testing.T) {
	runParity(t, `import io;
let x = match (7) {
    case 1 => "one";
    case 7 => "seven";
    default => "other";
};
io.println(x);
`, "seven\n")
}

func TestParityMatchGuardedCaseError(t *testing.T) {
	runParity(t, `import io;
try {
    match ("hello") {
        case string s if (s.length() > 10): io.println("long");
    };
} catch (MatchError e) {
    io.println(e.message.contains("hello"));
    io.println(e.message.contains("string"));
    io.println(e.message.contains("default"));
}
`, "true\ntrue\ntrue\n")
}

func TestParityGenericClassWithTypeErasure(t *testing.T) {
	runParity(t, `import io;

class Box<T> {
    T value;

    func Box(T v) {
        this.value = v;
    }

    func get(): T {
        return this.value;
    }

    func set(T v) {
        this.value = v;
    }
}

Box<string> b = Box("hello");
io.println(b.get());
b.set("world");
io.println(b.get());

Box<int> n = Box(42);
io.println(n.get());
`, "hello\nworld\n42\n")
}

func TestParityGenericClassMultipleTypeParams(t *testing.T) {
	runParity(t, `import io;

class Pair<A, B> {
    A first;
    B second;

    func Pair(A a, B b) {
        this.first = a;
        this.second = b;
    }

    func getFirst(): A {
        return this.first;
    }

    func getSecond(): B {
        return this.second;
    }

    func mapFirst<C>(func f): C {
        return f(this.first);
    }
}

Pair<string, int> p = Pair("abc", 3);
io.println(p.getFirst());
io.println(p.getSecond());
io.println(p.mapFirst(func(string s): string { return s + "!"; }));
`, "abc\n3\nabc!\n")
}

func TestParityGenericFunctionInstanceofTypeParam(t *testing.T) {
	runParity(t, `import io;

func check<T>(T value): bool {
    return value instanceof T;
}

io.println(check("hello"));
io.println(check(42));
io.println(check(true));
`, "true\ntrue\ntrue\n")
}

func TestParityGenericClassMethodInstanceofTypeParam(t *testing.T) {
	runParity(t, `import io;

class Container<T> {
    T item;

    func Container(T v) {
        this.item = v;
    }

    func isT(): bool {
        return this.item instanceof T;
    }
}

Container<string> cs = Container("hello");
io.println(cs.isT());

Container<int> ci = Container(42);
io.println(ci.isT());
`, "true\ntrue\n")
}

func TestParityGenericDecoratedMethodInheritsTypeBindings(t *testing.T) {
	runParity(t, `import io;

func id(any f): any {
    return func(): any { return f(); };
}

class Container<T> {
    T item;

    func Container(T v) {
        this.item = v;
    }

    @id
    func isT(): bool {
        return this.item instanceof T;
    }
}

Container<string> cs = Container("hello");
io.println(cs.isT());

Container<int> ci = Container(42);
io.println(ci.isT());
`, "true\ntrue\n")
}

func TestParityGenericMultiParamClassInstanceofTypeParams(t *testing.T) {
	runParity(t, `import io;

class Pair<A, B> {
    A first;
    B second;

    func Pair(A a, B b) {
        this.first = a;
        this.second = b;
    }

    func firstIsA(): bool {
        return this.first instanceof A;
    }

    func secondIsB(): bool {
        return this.second instanceof B;
    }
}

Pair<string, int> p = Pair("abc", 3);
io.println(p.firstIsA());
io.println(p.secondIsB());
`, "true\ntrue\n")
}

func TestParityGenericDeclarationAnnotationWinsOverInference(t *testing.T) {
	runParity(t, `import io;

class Box<T> {
    T value;

    func Box(T v) {
        this.value = v;
    }

    func isT(): bool {
        return this.value instanceof T;
    }
}

Box<int> n = Box(42);
io.println(n.isT());

Box<string> s = Box("hello");
io.println(s.isT());

Box<int> wrong = Box("not an int");
io.println(wrong.isT());
`, "true\ntrue\nfalse\n")
}

func TestParityGenericListParamInfersElementType(t *testing.T) {
	runParity(t, `import io;

func firstIs<T>(list<T> xs): bool {
    return xs[0] instanceof T;
}

io.println(firstIs(["hello", "world"]));
io.println(firstIs([1, 2, 3]));
io.println(firstIs([true, false]));
`, "true\ntrue\ntrue\n")
}

func TestParityEnumSimpleVariants(t *testing.T) {
	runParity(t, `import io;

enum Color { Red, Green, Blue }

Color c = Color.Red;
io.println(c);
io.println(Color.Green);
io.println(c == Color.Red);
io.println(c == Color.Blue);
`, "Color.Red\nColor.Green\ntrue\nfalse\n")
}

func TestParityEnumTaggedVariants(t *testing.T) {
	runParity(t, `import io;

enum Result { Ok(string), Err(string) }

Result r = Result.Ok("hello");
io.println(r);
Result e = Result.Err("oops");
io.println(e);
io.println(r == Result.Ok("hello"));
io.println(r == Result.Err("hello"));
`, "Result.Ok(hello)\nResult.Err(oops)\ntrue\nfalse\n")
}

func TestParityEnumInstanceof(t *testing.T) {
	runParity(t, `import io;

enum Color { Red, Green, Blue }
enum Result { Ok(string), Err(string) }

Color c = Color.Green;
Result r = Result.Ok("hi");
Result e = Result.Err("bad");

io.println(c instanceof Color);
io.println(r instanceof Result);
io.println(r instanceof Result.Ok);
io.println(e instanceof Result.Ok);
io.println(e instanceof Result.Err);
`, "true\ntrue\ntrue\nfalse\ntrue\n")
}

func TestParityEnumMatchSimpleVariants(t *testing.T) {
	runParity(t, `import io;

enum Direction { North, South, East, West }

func describe(Direction d): string {
    return match (d) {
        case Direction.North => "up";
        case Direction.South => "down";
        case Direction.East => "right";
        default => "left";
    };
}

io.println(describe(Direction.North));
io.println(describe(Direction.East));
io.println(describe(Direction.West));
`, "up\nright\nleft\n")
}

func TestParityEnumMatchTaggedVariants(t *testing.T) {
	runParity(t, `import io;

enum Result { Ok(string), Err(string) }

func handle(Result r): string {
    return match (r) {
        case Result.Ok(string msg) => "ok: " + msg;
        case Result.Err(string msg) => "err: " + msg;
    };
}

io.println(handle(Result.Ok("hello")));
io.println(handle(Result.Err("oops")));
`, "ok: hello\nerr: oops\n")
}

func TestParityGenericConstraintAcceptsImplementor(t *testing.T) {
	runParity(t, `import io;

interface Printable {
    func print(): string;
}

class Dog implements Printable {
    func print(): string {
        return "woof";
    }
}

func show<T implements Printable>(T item): string {
    return item.print();
}

Dog d = Dog();
io.println(show(d));
`, "woof\n")
}

func TestParityGenericConstraintRejectsNonImplementor(t *testing.T) {
	t.Helper()
	source := `import io;

interface Printable {
    func print(): string;
}

class Cat {
    func meow(): string {
        return "meow";
    }
}

func show<T implements Printable>(T item): string {
    return item.print();
}

Cat c = Cat();
show(c);
`
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	_, evErr := evaluator.New(&evOut).Eval(program)
	if evErr == nil {
		t.Error("evaluator: expected error for non-implementor, got nil")
	}

	src := []byte(source)
	chunk, err := bytecode.Compile(program, src, "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	vmErr := bytecode.NewVM(chunk, &vmOut).Run()
	if vmErr == nil {
		t.Error("vm: expected error for non-implementor, got nil")
	}
}

func TestParityGenericConstraintRejectsPrimitive(t *testing.T) {
	t.Helper()
	source := `interface Printable {
    func print(): string;
}

func show<T implements Printable>(T item): void {
}

show("not printable");
`
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	_, evErr := evaluator.New(&evOut).Eval(program)
	if evErr == nil {
		t.Error("evaluator: expected error for primitive, got nil")
	}

	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	vmErr := bytecode.NewVM(chunk, &vmOut).Run()
	if vmErr == nil {
		t.Error("vm: expected error for primitive, got nil")
	}
}

func TestParityClassGenericConstraintRejectsNonImplementor(t *testing.T) {
	t.Helper()
	source := `interface Printable {
    func print(): string;
}

class Dog implements Printable {
    func print(): string { return "woof"; }
}

class Cat {
}

class Box<T implements Printable> {
    T value;
    func Box(T value) {
        this.value = value;
    }
}

Dog d = Dog();
Box<Dog> ok = Box(d);
Cat c = Cat();
Box<Cat> bad = Box(c);
`
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	_, evErr := evaluator.New(&evOut).Eval(program)
	if evErr == nil {
		t.Error("evaluator: expected error for non-implementor, got nil")
	}

	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	vmErr := bytecode.NewVM(chunk, &vmOut).Run()
	if vmErr == nil {
		t.Error("vm: expected error for non-implementor, got nil")
	}
}

func TestParityGenericConstraintPrecedenceMatchesEvaluator(t *testing.T) {
	t.Helper()
	source := `interface A {
    func a(): string;
}
interface B {
    func b(): string;
}
interface C {
    func c(): string;
}

class OnlyC implements C {
    func c(): string { return "c"; }
}

func accept<T implements A & B | C>(T item): void {
}

OnlyC c = OnlyC();
accept(c);
`
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	_, evErr := evaluator.New(&evOut).Eval(program)
	if evErr == nil {
		t.Error("evaluator: expected precedence constraint error, got nil")
	}

	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	vmErr := bytecode.NewVM(chunk, &vmOut).Run()
	if vmErr == nil {
		t.Error("vm: expected precedence constraint error, got nil")
	}
}

func TestParityGenericUnionConstraintAcceptsEither(t *testing.T) {
	runParity(t, `import io;

interface Swimmer {
    func swim(): string;
}

interface Runner {
    func run(): string;
}

class Fish implements Swimmer {
    func swim(): string { return "splash"; }
}

class Horse implements Runner {
    func run(): string { return "gallop"; }
}

func move<T implements Swimmer | Runner>(T animal): void {
    io.println("ok");
}

Fish f = Fish();
Horse h = Horse();
move(f);
move(h);
`, "ok\nok\n")
}

func TestParityGenericSetParamInfersElementType(t *testing.T) {
	runParity(t, `import io;

func isSetOf<T>(set<T> s, any val): bool {
    return val instanceof T;
}

io.println(isSetOf({"hello", "world"}, "x"));
io.println(isSetOf({1, 2, 3}, 42));
io.println(isSetOf({"hello"}, 99));
`, "true\ntrue\nfalse\n")
}

func TestParityGenericDictParamInfersKeyValueTypes(t *testing.T) {
	runParity(t, `import io;

func keyIs<K, V>(dict<K, V> d, any val): bool {
    return val instanceof K;
}

func valIs<K, V>(dict<K, V> d, any val): bool {
    return val instanceof V;
}

io.println(keyIs({"a": 1, "b": 2}, "x"));
io.println(keyIs({"a": 1}, 42));
io.println(valIs({"a": 1, "b": 2}, 99));
io.println(valIs({"a": 1}, "hello"));
`, "true\nfalse\ntrue\nfalse\n")
}

func TestParityGenericCommaConstraints(t *testing.T) {
	runParity(t, `import io;

interface Printable {
    func print(): string;
}

interface Saveable {
    func save(): string;
}

class Document implements Printable, Saveable {
    func print(): string { return "printing"; }
    func save(): string { return "saving"; }
}

func process<T implements Printable, Saveable>(T item): void {
    io.println(item.print());
    io.println(item.save());
}

Document d = Document();
process(d);
`, "printing\nsaving\n")
}

func TestParityGenericInterfaceWithTypeParams(t *testing.T) {
	runParity(t, `import io;

interface Container<T> {
    func get(): T;
}

class Box<T> implements Container<T> {
    T value;

    func Box(T v) {
        this.value = v;
    }

    func get(): T {
        return this.value;
    }
}

Box<string> b = Box("hello");
io.println(b.get());
`, "hello\n")
}

func TestParityCollectionsFindLast(t *testing.T) {
	runParity(t, `import io;
import collections;
let x = collections.findLast([1, 3, 5, 2, 4], func(int n): bool { return n % 2 == 1; });
io.println(x);
let none = collections.findLast([2, 4, 6], func(int n): bool { return n % 2 == 1; });
io.println(none);
`, "5\nnull\n")
}

func TestParityCollectionsContainsBy(t *testing.T) {
	runParity(t, `import io;
import collections;
io.println(collections.containsBy(["alice", "bob", "carol"], "bob", func(string s): string { return s; }));
io.println(collections.containsBy(["alice", "bob", "carol"], "dave", func(string s): string { return s; }));
`, "true\nfalse\n")
}

func TestParityCollectionsIndexBy(t *testing.T) {
	runParity(t, `import io;
import collections;
io.println(collections.indexBy([10, 20, 30, 40], func(int n): bool { return n > 25; }));
io.println(collections.indexBy([1, 2, 3], func(int n): bool { return n > 10; }));
`, "2\n-1\n")
}

func TestParityCollectionsBinarySearch(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = [1, 3, 5, 7, 9, 11];
io.println(collections.binarySearch(xs, 7));
io.println(collections.binarySearch(xs, 1));
io.println(collections.binarySearch(xs, 11));
io.println(collections.binarySearch(xs, 4));
`, "3\n0\n5\n-1\n")
}

func TestParityCollectionsLowerBound(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = [1, 3, 5, 7, 9];
io.println(collections.lowerBound(xs, 5));
io.println(collections.lowerBound(xs, 4));
io.println(collections.lowerBound(xs, 0));
io.println(collections.lowerBound(xs, 10));
`, "2\n2\n0\n5\n")
}

func TestParityCollectionsUpperBound(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = [1, 3, 5, 7, 9];
io.println(collections.upperBound(xs, 5));
io.println(collections.upperBound(xs, 4));
io.println(collections.upperBound(xs, 0));
io.println(collections.upperBound(xs, 10));
`, "3\n2\n0\n5\n")
}

func TestParityCollectionsMinBy(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = [3, 1, 4, 1, 5];
io.println(collections.minBy(xs, func(int n): int { return n; }));
io.println(collections.minBy([], func(int n): int { return n; }));
`, "1\nnull\n")
}

func TestParityCollectionsMaxBy(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = [3, 1, 4, 1, 5];
io.println(collections.maxBy(xs, func(int n): int { return n; }));
io.println(collections.maxBy([], func(int n): int { return n; }));
`, "5\nnull\n")
}

func TestParityCollectionsSortBy(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = [3, 1, 4, 1, 5];
let s = collections.sortBy(xs, func(int n): int { return n; });
io.println(s[0]);
io.println(s[4]);
io.println(collections.sortBy(xs, func(int n): int { return -n; })[0]);
`, "1\n5\n5\n")
}

func TestParityCollectionsTopBy(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = [3, 1, 4, 1, 5, 9, 2, 6];
let top3 = collections.topBy(xs, func(int n): int { return n; }, 3);
io.println(top3[0]);
io.println(top3[1]);
io.println(top3[2]);
io.println(top3.length());
`, "9\n6\n5\n3\n")
}

func TestParityCollectionsSumBy(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = [3, 7, 10];
io.println(collections.sumBy(xs, func(int n): int { return n; }));
io.println(collections.sumBy([], func(int n): int { return n; }));
`, "20\n0\n")
}

func TestParityCollectionsAverageBy(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = [1, 2, 3];
io.println(collections.averageBy(xs, func(int n): int { return n; }));
io.println(collections.averageBy([], func(int n): int { return n; }));
`, "2\nnull\n")
}

func TestParityCollectionsTopKBottomK(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = [3, 1, 4, 1, 5, 9, 2, 6];
let top = collections.topK(xs, 3);
let bottom = collections.bottomK(xs, 2);
io.println(top);
io.println(bottom);
io.println(collections.topK(xs, -1).length());
`, "[9, 6, 5]\n[1, 1]\n0\n")
}

func TestParityCollectionsFrequenciesAndMode(t *testing.T) {
	runParity(t, `import io;
import collections;
let counts = collections.frequencies(["a", "b", "a", "c", "b", "a"]);
io.println(counts["a"]);
io.println(counts["b"]);
io.println(counts["missing"]);
io.println(collections.mode(["a", "b", "a", "c"]));
io.println(collections.mode([]));
`, "3\n2\nnull\na\nnull\n")
}

func TestParityCollectionsDifferenceIntersection(t *testing.T) {
	runParity(t, `import io;
import collections;
let a = [1, 2, 3, 4, 5];
let b = [2, 4, 6];
io.println(collections.difference(a, b));
io.println(collections.intersection(a, b));
io.println(collections.difference([], b));
io.println(collections.intersection([], b));
`, "[1, 3, 5]\n[2, 4]\n[]\n[]\n")
}

func TestParityCollectionsDifferenceByIntersectionBy(t *testing.T) {
	runParity(t, `import io;
import collections;
let words = ["apple", "banana", "cherry", "avocado"];
let exclude = ["a", "c"];
let diff = collections.differenceBy(words, exclude, func(string s): string { return s[0]; });
let inter = collections.intersectionBy(words, exclude, func(string s): string { return s[0]; });
io.println(diff);
io.println(inter);
`, "[\"banana\"]\n[\"apple\", \"cherry\", \"avocado\"]\n")
}

func TestParityCollectionsZipWith(t *testing.T) {
	runParity(t, `import io;
import collections;
let a = [1, 2, 3];
let b = [10, 20, 30];
let sums = collections.zipWith(a, b, func(int x, int y): int { return x + y; });
io.println(sums);
let shorter = collections.zipWith([1, 2], [10, 20, 30], func(int x, int y): int { return x + y; });
io.println(shorter);
`, "[11, 22, 33]\n[11, 22]\n")
}

func TestParityStackTraceUncaughtError(t *testing.T) {
	runErrorParity(t, `
func inner() {
    throw RuntimeError("boom");
}
func outer() {
    inner();
}
outer();
`,
		"RuntimeError: boom",
		"at inner",
		"at outer",
		"at <top level>",
	)
}

func TestParityStackTraceCaughtErrorHasNoTrace(t *testing.T) {
	runParity(t, `import io;
func inner() {
    throw RuntimeError("oops");
}
try {
    inner();
} catch (RuntimeError e) {
    io.println(e.message);
}
`, "oops\n")
}

func TestParityStructuredErrorStackTrace(t *testing.T) {
	runParity(t, `import errors;
import io;

func inner() {
    throw RuntimeError("boom");
}

func outer() {
    inner();
}

try {
    outer();
} catch (RuntimeError e) {
    let trace = e.stackTrace();
    let first = trace.first();
    let frames = errors.frames(e);
    io.println(typeof(trace));
    io.println(trace.length() > 0);
    io.println(first.function());
    io.println(first.line() > 0);
    io.println(frames[0]["function"]);
    io.println(errors.hasStackTrace(e));
}
`, "errors.StackTrace\ntrue\ninner\ntrue\ninner\ntrue\n")
}

func TestParityCollectionsBFS(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b", "c"], "b": ["d"], "c": ["d"], "d": []};
let r = collections.bfs(g, "a");
io.println(r);
`, "[\"a\", \"b\", \"c\", \"d\"]\n")
}

func TestParityCollectionsBFSChain(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b"], "b": ["c"], "c": []};
io.println(collections.bfs(g, "a"));
io.println(collections.bfs(g, "c"));
`, "[\"a\", \"b\", \"c\"]\n[\"c\"]\n")
}

func TestParityCollectionsDFS(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b", "c"], "b": ["d"], "c": ["d"], "d": []};
let r = collections.dfs(g, "a");
io.println(r);
`, "[\"a\", \"b\", \"d\", \"c\"]\n")
}

func TestParityCollectionsDFSChain(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b"], "b": ["c"], "c": []};
io.println(collections.dfs(g, "a"));
`, "[\"a\", \"b\", \"c\"]\n")
}

func TestParityCollectionsTopologicalSort(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b", "c"], "b": ["d"], "c": ["d"], "d": []};
let r = collections.topologicalSort(g);
io.println(r);
`, "[\"a\", \"b\", \"c\", \"d\"]\n")
}

func TestParityCollectionsTopologicalSortChain(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b"], "b": ["c"], "c": []};
io.println(collections.topologicalSort(g));
`, "[\"a\", \"b\", \"c\"]\n")
}

func TestParityCollectionsTopologicalSortCycleError(t *testing.T) {
	runErrorParity(t, `import collections;
let g = {"a": ["b"], "b": ["c"], "c": ["a"]};
collections.topologicalSort(g);
`, "cycle detected")
}

func TestParityCollectionsShortestPath(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b", "c"], "b": ["d"], "c": ["d"], "d": []};
io.println(collections.shortestPath(g, "a", "d"));
io.println(collections.shortestPath(g, "a", "a"));
`, "[\"a\", \"b\", \"d\"]\n[\"a\"]\n")
}

func TestParityCollectionsShortestPathUnreachable(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b"], "b": [], "c": ["d"], "d": []};
io.println(collections.shortestPath(g, "a", "c"));
io.println(collections.shortestPath(g, "d", "a"));
`, "null\nnull\n")
}

func TestParityCollectionsShortestPathChain(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b"], "b": ["c"], "c": []};
io.println(collections.shortestPath(g, "a", "c"));
`, "[\"a\", \"b\", \"c\"]\n")
}

func TestParityMarkdownRenderHtml(t *testing.T) {
	runParity(t, `import io;
import markdown;
let src = "# Hello\n\nA paragraph.\n\n- alpha\n- beta\n\n`+"```"+`go\nx := 1\n`+"```"+`";
let h = markdown.renderHtml(src);
io.println(h.contains("<h1"));
io.println(h.contains("Hello</h1>"));
io.println(h.contains("<p>"));
io.println(h.contains("<ul>"));
io.println(h.contains("<li>alpha</li>"));
io.println(h.contains("<pre><code"));
`, "true\ntrue\ntrue\ntrue\ntrue\ntrue\n")
}

func TestParityMarkdownRenderHtmlGFM(t *testing.T) {
	runParity(t, `import io;
import markdown;
let src = "| a | b |\n|---|---|\n| 1 | 2 |\n\n~~strike~~\n\n- [x] done\n- [ ] todo";
let h = markdown.renderHtml(src);
io.println(h.contains("<table>"));
io.println(h.contains("<th>"));
io.println(h.contains("<del>strike</del>"));
io.println(h.contains("<input"));
`, "true\ntrue\ntrue\ntrue\n")
}

func TestParityMarkdownRenderHtmlBlockquote(t *testing.T) {
	runParity(t, `import io;
import markdown;
let src = "> quoted text\n\n---";
let h = markdown.renderHtml(src);
io.println(h.contains("<blockquote>"));
io.println(h.contains("<hr"));
`, "true\ntrue\n")
}

func TestParityMarkdownParseBasic(t *testing.T) {
	runParity(t, `import io;
import markdown;
let src = "## Section\n\nHello world.\n\n- x\n- y\n\n`+"```"+`js\nconsole.log(1);\n`+"```"+`";
let blocks = markdown.parse(src);
io.println(blocks.length());
io.println(blocks[0]["type"]);
io.println(blocks[0]["level"]);
io.println(blocks[0]["text"]);
io.println(blocks[1]["type"]);
io.println(blocks[1]["text"]);
io.println(blocks[2]["type"]);
io.println(blocks[2]["items"][0]);
io.println(blocks[3]["type"]);
io.println(blocks[3]["lang"]);
`, "4\nheading\n2\nSection\nparagraph\nHello world.\nlist\nx\ncode\njs\n")
}

func TestParityMarkdownParseTable(t *testing.T) {
	runParity(t, `import io;
import markdown;
let src = "| Name | Score |\n|------|-------|\n| Ada | 10 |\n| Grace | 12 |";
let blocks = markdown.parse(src);
io.println(blocks.length());
io.println(blocks[0]["type"]);
io.println(blocks[0]["headers"][0]);
io.println(blocks[0]["headers"][1]);
io.println(blocks[0]["rows"][0][0]);
io.println(blocks[0]["rows"][1][1]);
`, "1\ntable\nName\nScore\nAda\n12\n")
}

func TestParityMarkdownParseTaskList(t *testing.T) {
	runParity(t, `import io;
import markdown;
let src = "- [x] done\n- [ ] todo";
let blocks = markdown.parse(src);
io.println(blocks.length());
io.println(blocks[0]["type"]);
io.println(blocks[0]["items"][0]["text"]);
io.println(blocks[0]["items"][0]["checked"]);
io.println(blocks[0]["items"][1]["text"]);
io.println(blocks[0]["items"][1]["checked"]);
`, "1\ntask_list\ndone\ntrue\ntodo\nfalse\n")
}

func TestParityMarkdownParseOrderedList(t *testing.T) {
	runParity(t, `import io;
import markdown;
let src = "1. first\n2. second\n3. third";
let blocks = markdown.parse(src);
io.println(blocks.length());
io.println(blocks[0]["type"]);
io.println(blocks[0]["items"][0]);
io.println(blocks[0]["items"][2]);
`, "1\nordered_list\nfirst\nthird\n")
}

func TestParityMarkdownParseBlockquote(t *testing.T) {
	runParity(t, `import io;
import markdown;
let src = "> important note";
let blocks = markdown.parse(src);
io.println(blocks.length());
io.println(blocks[0]["type"]);
io.println(blocks[0]["text"]);
`, "1\nblockquote\nimportant note\n")
}

func TestParityMarkdownParseHR(t *testing.T) {
	runParity(t, `import io;
import markdown;
let src = "---";
let blocks = markdown.parse(src);
io.println(blocks.length());
io.println(blocks[0]["type"]);
`, "1\nhr\n")
}

func TestParityMarkdownStripText(t *testing.T) {
	runParity(t, `import io;
import markdown;
let src = "# Title\n\nHello **world**.\n\n`+"```"+`\ncode here\n`+"```"+`";
let stripped = markdown.stripText(src);
io.println(stripped.contains("Title"));
io.println(stripped.contains("Hello"));
io.println(stripped.contains("world"));
io.println(stripped.contains("code here"));
`, "true\ntrue\ntrue\nfalse\n")
}

func TestParityTernaryBasic(t *testing.T) {
	runParity(t, `import io;
io.println(true ? "yes" : "no");
io.println(false ? "yes" : "no");
`, "yes\nno\n")
}

func TestParityTernaryWithExpressions(t *testing.T) {
	runParity(t, `import io;
let x = 5;
let y = x > 3 ? "big" : "small";
io.println(y);
let z = x < 3 ? "big" : "small";
io.println(z);
`, "big\nsmall\n")
}

func TestParityTernaryNested(t *testing.T) {
	runParity(t, `import io;
io.println(true ? (false ? "a" : "b") : "c");
io.println(false ? "a" : (true ? "b" : "c"));
`, "b\nb\n")
}

func TestParityTernaryInExpression(t *testing.T) {
	runParity(t, `import io;
io.println("val: " + (true ? "one" : "two"));
let x = 10;
io.println((x > 5 ? "pos" : "neg") + "!");
`, "val: one\npos!\n")
}

func TestParityNullableChainMultiHop(t *testing.T) {
	runParity(t, `import io;
class Inner {
    string value;
    func Inner(string v) { this.value = v; }
    func upper() { return this.value.upper(); }
}
class Outer {
    Inner inner;
    func Outer(Inner i) { this.inner = i; }
    func getInner() { return this.inner; }
}
let o = Outer(Inner("hello"));
io.println(o?.inner?.value);
io.println(o?.getInner()?.upper());
`, "hello\nHELLO\n")
}

func TestParityNullableChainShortCircuit(t *testing.T) {
	runParity(t, `import io;
class Node {
    string name;
    func Node(string n) { this.name = n; }
    func upper() { return this.name.upper(); }
}
let n = null;
let result = n?.upper();
if (result == null) { io.println("null"); } else { io.println(result); }
`, "null\n")
}

func TestParityNullableChainMidNull(t *testing.T) {
	runParity(t, `import io;
class Wrapper {
    func Wrapper() {}
    func getNull() { return null; }
}
let w = Wrapper();
let result = w?.getNull()?.upper();
if (result == null) { io.println("null"); } else { io.println(result); }
`, "null\n")
}

func TestParityCompoundAssignArithmetic(t *testing.T) {
	runParity(t, `import io;
let x = 10;
x += 5;
io.println(x as string);
x -= 3;
io.println(x as string);
x *= 4;
io.println(x as string);
x %= 7;
io.println(x as string);
`, "15\n12\n48\n6\n")
}

func TestParityCompoundAssignIntDiv(t *testing.T) {
	runParity(t, `import io;
let x = 20;
x //= 3;
io.println(x as string);
`, "6\n")
}

func TestParityCompoundAssignPower(t *testing.T) {
	runParity(t, `import io;
let x = 3;
x **= 3;
io.println(x as string);
`, "27\n")
}

func TestParityCompoundAssignBitwise(t *testing.T) {
	runParity(t, `import io;
let a = 12;
a &= 10;
io.println(a as string);
let b = 5;
b |= 10;
io.println(b as string);
let c = 15;
c ^= 9;
io.println(c as string);
let d = 3;
d <<= 2;
io.println(d as string);
let e = 20;
e >>= 2;
io.println(e as string);
`, "8\n15\n6\n12\n5\n")
}

func TestParityCompoundAssignNullCoalesce(t *testing.T) {
	runParity(t, `import io;
let n = null;
n ??= "default";
io.println(n);
let m = "existing";
m ??= "other";
io.println(m);
`, "default\nexisting\n")
}

func TestParityCompoundAssignField(t *testing.T) {
	runParity(t, `import io;
class Counter {
    int value;
    func Counter(int v) { this.value = v; }
}
let c = Counter(10);
c.value += 5;
io.println(c.value as string);
c.value *= 2;
io.println(c.value as string);
`, "15\n30\n")
}

func TestParityCompoundAssignIndex(t *testing.T) {
	runParity(t, `import io;
let arr = [10, 20, 30];
arr[1] += 5;
io.println(arr[1] as string);
arr[0] *= 3;
io.println(arr[0] as string);
`, "25\n30\n")
}

func TestParityUserErrorBasic(t *testing.T) {
	runParity(t, `import io;
class AppError extends Error {}
try {
    throw AppError("something failed");
} catch (AppError e) {
    io.println(e.message);
}
`, "something failed\n")
}

func TestParityUserErrorCatchParent(t *testing.T) {
	runParity(t, `import io;
class AppError extends Error {}
try {
    throw AppError("caught by parent");
} catch (Error e) {
    io.println(e.message);
}
`, "caught by parent\n")
}

func TestParityUserErrorCatchAll(t *testing.T) {
	runParity(t, `import io;
class AppError extends Error {}
try {
    throw AppError("caught by catch-all");
} catch (Error e) {
    io.println(e.message);
}
`, "caught by catch-all\n")
}

func TestParityUserErrorChain(t *testing.T) {
	runParity(t, `import io;
class NetworkError extends RuntimeError {}
try {
    throw NetworkError("timeout");
} catch (Error e) {
    io.println(e.class + ": " + e.message);
}
`, "NetworkError: timeout\n")
}

func TestParityUserErrorMultiCatch(t *testing.T) {
	runParity(t, `import io;
class AppError extends Error {}
class NetworkError extends RuntimeError {}
try {
    throw NetworkError("timeout");
} catch (AppError e) {
    io.println("app: " + e.message);
} catch (NetworkError e) {
    io.println("network: " + e.message);
} catch (Error e) {
    io.println("generic: " + e.message);
}
`, "network: timeout\n")
}

func TestParityUserErrorFinally(t *testing.T) {
	runParity(t, `import io;
class AppError extends Error {}
try {
    throw AppError("oops");
} catch (AppError e) {
    io.println("caught: " + e.message);
} finally {
    io.println("finally");
}
`, "caught: oops\nfinally\n")
}

func TestParityUserErrorNoMessage(t *testing.T) {
	runParity(t, `import io;
class AppError extends Error {}
try {
    throw AppError();
} catch (AppError e) {
    io.println(e.class);
}
`, "AppError\n")
}

func TestParityInterpolateVar(t *testing.T) {
	runParity(t, `import io;
let name = "world";
io.println("Hello ${name}!");
`, "Hello world!\n")
}

func TestParityInterpolateExpr(t *testing.T) {
	runParity(t, `import io;
io.println("${1 + 2}");
`, "3\n")
}

func TestParityInterpolateMultiple(t *testing.T) {
	runParity(t, `import io;
let a = 3;
let b = 4;
io.println("${a} + ${b} = ${a + b}");
`, "3 + 4 = 7\n")
}

func TestParityInterpolateInt(t *testing.T) {
	runParity(t, `import io;
let n = 42;
io.println("n = ${n}");
`, "n = 42\n")
}

func TestParityInterpolateNested(t *testing.T) {
	runParity(t, `import io;
func greet(string who): string {
    return "Hello, ${who}!";
}
io.println(greet("Geblang"));
`, "Hello, Geblang!\n")
}

func TestParityInterpolateLiteralOnly(t *testing.T) {
	runParity(t, `import io;
io.println("no interpolation");
`, "no interpolation\n")
}

func TestParityStringInterpolationDoubleQuoted(t *testing.T) {
	runParity(t, `import io;
io.println("Hello ${"world"}");
io.println("Sum: ${1 + 2}");
io.println("Brace: ${"has}brace"}");
io.println("Open: ${"has{brace"}");
io.println("Dict: ${{"key": "val"}["key"]}");
let d = {"x": 42};
io.println("Lookup: ${d["x"]}");
`, "Hello world\nSum: 3\nBrace: has}brace\nOpen: has{brace\nDict: val\nLookup: 42\n")
}

func TestParityForByStep(t *testing.T) {
	runParity(t, `import io;
for (i in 0..10 by 2) {
    io.print(i as string + " ");
}
io.println("");
`, "0 2 4 6 8 10 \n")
}

func TestParityForByStepExclusive(t *testing.T) {
	runParity(t, `import io;
for (i in 0..<10 by 3) {
    io.print(i as string + " ");
}
io.println("");
`, "0 3 6 9 \n")
}

func TestParityForByStepNegative(t *testing.T) {
	runParity(t, `import io;
for (i in 10..0 by -2) {
    io.print(i as string + " ");
}
io.println("");
`, "10 8 6 4 2 0 \n")
}

func TestParityForByStepExpr(t *testing.T) {
	runParity(t, `import io;
let step = 4;
for (i in 0..12 by step) {
    io.print(i as string + " ");
}
io.println("");
`, "0 4 8 12 \n")
}

func TestParityRangeLength(t *testing.T) {
	runParity(t, `import io;
let r = 0..9;
io.println(r.length() as string);
`, "10\n")
}

func TestParityRangeLengthByStep(t *testing.T) {
	runParity(t, `import io;
let r = 0..10 by 2;
io.println(r.length() as string);
`, "6\n")
}

func TestParityRangeLengthExclusive(t *testing.T) {
	runParity(t, `import io;
let r = 0..<10;
io.println(r.length() as string);
`, "10\n")
}

func TestParityRangeIsEmpty(t *testing.T) {
	runParity(t, `import io;
let r = 5..3;
io.println(r.isEmpty() as string);
`, "true\n")
}

func TestParityRangeContains(t *testing.T) {
	runParity(t, `import io;
let r = 0..10 by 2;
io.println(r.contains(4) as string);
io.println(r.contains(3) as string);
io.println(r.contains(10) as string);
`, "true\nfalse\ntrue\n")
}

func TestParityRangeFirst(t *testing.T) {
	runParity(t, `import io;
let r = 5..20 by 5;
io.println(r.first() as string);
`, "5\n")
}

func TestParityRangeLast(t *testing.T) {
	runParity(t, `import io;
let r = 0..10 by 3;
io.println(r.last() as string);
`, "9\n")
}

func TestParityRangeToList(t *testing.T) {
	runParity(t, `import io;
let r = 1..5;
let list = r.toList();
for (n in list) {
    io.print(n as string + " ");
}
io.println("");
`, "1 2 3 4 5 \n")
}

func TestParityRangeInspect(t *testing.T) {
	runParity(t, `import io;
let r = 0..10 by 2;
io.println(r as string);
`, "0..10 by 2\n")
}

func TestParityRangeFieldAccess(t *testing.T) {
	runParity(t, `import io;
let r = 3..15 by 4;
io.println(r.start as string);
io.println(r.end as string);
io.println(r.step as string);
`, "3\n15\n4\n")
}

func TestParityErrorsIsBuiltin(t *testing.T) {
	runParity(t, `import io;
import errors;
try {
    throw ValueError("oops");
} catch (Error e) {
    io.println(errors.is(e, "ValueError") as string);
    io.println(errors.is(e, "Error") as string);
    io.println(errors.is(e, "IOError") as string);
}
`, "true\ntrue\nfalse\n")
}

func TestParityNativeBindConflictIsCatchableIOError(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local TCP sockets unavailable: %v", err)
	}
	_ = probe.Close()

	runParityStateful(t, `import io;
import net;

let listener = net.listenTcp("127.0.0.1:0");
let addr = net.localAddr(listener);
try {
    let other = net.listenTcp(addr);
    net.close(other);
    io.println("not caught");
} catch (IOError e) {
    io.println(e.name);
} finally {
    net.close(listener);
}
`, "IOError\n")
}

func TestParityErrorsIsUserHierarchy(t *testing.T) {
	runParity(t, `import io;
import errors;
class AppError extends RuntimeError {}
class NotFoundError extends AppError {}
try {
    throw NotFoundError("not found");
} catch (Error e) {
    io.println(errors.is(e, "NotFoundError") as string);
    io.println(errors.is(e, "AppError") as string);
    io.println(errors.is(e, "RuntimeError") as string);
    io.println(errors.is(e, "Error") as string);
    io.println(errors.is(e, "IOError") as string);
}
`, "true\ntrue\ntrue\ntrue\nfalse\n")
}

func TestParityErrorCustomFields(t *testing.T) {
	runParity(t, `import io;
class HttpError extends RuntimeError {
    int code;
    func HttpError(int code, string msg) {
        parent(msg);
        this.code = code;
    }
}
try {
    throw HttpError(404, "not found");
} catch (Error e) {
    io.println(e.message);
    io.println(e.code as string);
}
`, "not found\n404\n")
}

func TestParityErrorCustomFieldsIs(t *testing.T) {
	runParity(t, `import io;
import errors;
class ApiError extends IOError {
    int status;
    func ApiError(int status, string msg) {
        parent(msg);
        this.status = status;
    }
}
try {
    throw ApiError(500, "server error");
} catch (Error e) {
    io.println(errors.is(e, "ApiError") as string);
    io.println(errors.is(e, "IOError") as string);
    io.println(errors.is(e, "Error") as string);
    io.println(e.status as string);
}
`, "true\ntrue\ntrue\n500\n")
}

func TestParityProcessModule(t *testing.T) {
	runParityStateful(t, `
import process;
import io;
let r = process.run("printf", ["hello"]);
io.println(r.isOk());
io.println(r.code());
io.println(r.stdout());
io.println(r.timedOut());
`, "true\n0\nhello\nfalse\n")
}

func TestParityProcessStart(t *testing.T) {
	runParityStateful(t, `
import process;
import io;
let proc = process.start("cat", []);
proc.write("piped\n");
proc.closeStdin();
io.print(proc.readStdout());
io.println(proc.wait());
`, "piped\n0\n")
}

func TestParityHTTPClientConstruct(t *testing.T) {
	runParityStateful(t, `
import http;
import io;
let c = http.newClient();
let b = http.build("https://example.com");
let j = http.newCookieJar();
io.println(typeof(c));
io.println(typeof(b));
io.println(typeof(j));
`, "Client\nBuilder\nCookieJar\n")
}

func TestParityHTTPClientConfig(t *testing.T) {
	runParityStateful(t, `
import http;
import io;
let c = http.newClient({"baseUrl": "https://example.com", "timeoutMs": 5000});
let j = c.cookieJar();
io.println(typeof(c));
io.println(typeof(j));
let j2 = http.newCookieJar();
c.attachCookieJar(j2);
io.println(typeof(c));
`, "Client\nCookieJar\nClient\n")
}

// TestParityHTTPClientNewOptions verifies http.newClient accepts the
// cookieJar (instance and auto), keepAlive, maxIdleConns, proxy, and
// proxyFromEnv options on both backends.
// TestParityHTTPServerTLS exercises the HTTPS surface end to end on both
// backends: a self-signed server, a client trusting it via caCerts, an
// insecure client, and the default client rejecting the untrusted cert.
func TestParityHTTPServerTLS(t *testing.T) {
	runParityStateful(t, `
import http;
import io;
let server = http.listen("127.0.0.1:0", func(dict<string, any> req): dict<string, any> {
    return {"status": 200, "body": "secure"};
}, {"tls": {"selfSigned": true}});
let url = "https://" + http.serverAddr(server) + "/";
let cert = http.serverCert(server);
io.println(cert != null);
io.println(http.newClient({"tls": {"caCerts": cert}}).get(url)["body"] as string);
io.println(http.newClient({"tls": {"verify": false}}).get(url)["body"] as string);
try { http.newClient({}).get(url); io.println("strict-ok"); } catch (Error e) { io.println("strict-rejected"); }
http.close(server);
`, "true\nsecure\nsecure\nstrict-rejected\n")
}

func TestParityHTTPClientNewOptions(t *testing.T) {
	runParityStateful(t, `
import http;
import io;
let jar = http.newCookieJar();
let withJar = http.newClient({"cookieJar": jar, "keepAlive": true, "maxIdleConns": 8});
io.println(typeof(withJar));
let autoJar = http.newClient({"cookieJar": true});
io.println(typeof(autoJar));
let proxied = http.newClient({"proxy": "http://proxy.example.com:3128", "timeoutMs": 1000});
io.println(typeof(proxied));
let envProxied = http.newClient({"proxyFromEnv": true});
io.println(typeof(envProxied));
`, "Client\nClient\nClient\nClient\n")
}

// TestParityHTTPCookieJarInspect verifies the CookieJar's cookies(url) /
// setCookies(url, list) / clear() round trip on both backends.
func TestParityHTTPCookieJarInspect(t *testing.T) {
	runParityStateful(t, `
import http;
import io;
let jar = http.newCookieJar();
jar.setCookies("https://example.com/", [
    {"name": "sid", "value": "abc", "path": "/"},
    {"name": "csrf", "value": "xyz", "secure": true}
]);
let cookies = jar.cookies("https://example.com/");
io.println(cookies.length);
io.println(cookies[0]["name"] + "=" + cookies[0]["value"]);
io.println(cookies[1]["name"] + "=" + cookies[1]["value"]);
jar.clear();
io.println(jar.cookies("https://example.com/").length);
`, "2\nsid=abc\ncsrf=xyz\n0\n")
}

func TestParityHTTPBuilderChain(t *testing.T) {
	runParityStateful(t, `
import http;
import io;
let b = http.build("https://example.com")
    .method("POST")
    .header("Content-Type", "application/json")
    .body("{}")
    .timeout(1000);
io.println(typeof(b));
`, "Builder\n")
}

func TestParityHTTPFetchStreamEmpty(t *testing.T) {
	runParityStateful(t, `
import http;
import io;
let stream = http.fetchStream([]);
io.println(typeof(stream));
io.println(stream.done());
io.println(stream.remaining());
io.println(stream.next());
`, "FetchStream\ntrue\n0\nnull\n")
}

func TestParityHTTPFetchAllEmpty(t *testing.T) {
	runParityStateful(t, `
import http;
import io;
import async;
let results = await http.fetchAll([]);
io.println(results.length());
`, "0\n")
}

func TestParityHTTPClientBatchMethods(t *testing.T) {
	runParityStateful(t, `
import http;
import io;
import async;
let c = http.newClient({"timeoutMs": 5000});
let results = await c.fetchAll([]);
io.println(results.length());
let stream = c.fetchStream([]);
io.println(typeof(stream));
io.println(stream.done());
io.println(stream.remaining());
`, "0\nFetchStream\ntrue\n0\n")
}

func TestParityCollectionElementTypeEnforcement(t *testing.T) {
	runParity(t, `
import io;
func sumInts(list<int> items): int {
    let total = 0;
    for (item in items) { total = total + item; }
    return total;
}
io.println(sumInts([1, 2, 3]));
`, "6\n")

	runErrorParity(t, `
func sumInts(list<int> items): int {
    return 0;
}
sumInts(["a", "b"]);
`, "expects list<int>")

	runErrorParity(t, `
func takeStrings(list<string> items): void {}
takeStrings([1, 2, 3]);
`, "expects list<string>")

	runErrorParity(t, `
func takeDictStringInt(dict<string, int> d): void {}
takeDictStringInt({"a": "not-an-int"});
`, "expects dict<string")

	runErrorParity(t, `
func takeSetInt(set<int> s): void {}
takeSetInt({"a", "b"});
`, "expects set<int>")

	runParity(t, `
import io;
func nested(list<dict<string, int>> rows): int {
    return rows[0]["count"];
}
io.println(nested([{"count": 7}]));
`, "7\n")

	runErrorParity(t, `
func nested(list<dict<string, int>> rows): void {}
nested([{"count": "bad"}]);
`, "expects list<dict<string")
}

func TestParityDeclarationElementTypeEnforcement(t *testing.T) {
	// Valid typed declarations must compile and run without error.
	runParity(t, `import io;
list<int> nums = [1, 2, 3];
list<string> strs = ["a", "b"];
io.println(nums.length());
io.println(strs.length());
`, "3\n2\n")

	// Wrong element type in list declaration - error names the offending element.
	runErrorParity(t, `
list<int> bad = ["a", "b", "c"];
`, "cannot assign", "list<int>", "element at index 0 is string")

	// Wrong element type in dict declaration.
	runErrorParity(t, `
dict<string, int> scores = {"alice": "not-an-int"};
`, "cannot assign")

	// Wrong element type in set declaration.
	runErrorParity(t, `
set<int> s = {"a", "b"};
`, "cannot assign")

	// Wrong collection kind (dict to list).
	runErrorParity(t, `
list<int> bad = {"a": 1};
`, "cannot assign")

	// T[] alias syntax: valid.
	runParity(t, `import io;
int[] nums = [1, 2, 3];
io.println(nums.length());
`, "3\n")

	// T[] alias syntax: wrong element type.
	runErrorParity(t, `
int[] bad = ["a", "b"];
`, "cannot assign")

	// Heterogeneous list: error message identifies the offending element.
	runErrorParity(t, `
list<int> bad = [1, 2, "oops"];
`, "element at index 2 is string")
}

// TestParityListInPlaceAppend covers the new in-place list growth
// methods (append, extend, clear) that mutate the receiver and
// participate in reference semantics.
func TestParityListInPlaceAppend(t *testing.T) {
	// append mutates in place and is observable through aliases.
	runParity(t, `import io;
let xs = [1, 2];
let ys = xs;
xs.append(3);
io.println(ys);
`, "[1, 2, 3]\n")

	// extend appends every element of another list.
	runParity(t, `import io;
let xs = [1, 2];
xs.extend([3, 4, 5]);
io.println(xs);
`, "[1, 2, 3, 4, 5]\n")

	// clear empties the list in place.
	runParity(t, `import io;
let xs = [1, 2, 3];
xs.clear();
io.println(xs);
io.println(xs.length());
`, "[]\n0\n")

	// dict.clear empties the dict in place.
	runParity(t, `import io;
let d = {"a": 1, "b": 2};
d.clear();
io.println(d);
io.println(d.length());
`, "{}\n0\n")

	// append on a frozen list raises ImmutableError.
	runErrorParity(t, `import freeze;
let xs = freeze.shallow([1, 2]);
xs.append(3);
`, "ImmutableError")

	// extend on a frozen list raises ImmutableError.
	runErrorParity(t, `import freeze;
let xs = freeze.shallow([1, 2]);
xs.extend([3]);
`, "ImmutableError")

	// clear on a frozen list raises ImmutableError.
	runErrorParity(t, `import freeze;
let xs = freeze.shallow([1, 2]);
xs.clear();
`, "ImmutableError")

	// clear on a frozen dict raises ImmutableError.
	runErrorParity(t, `import freeze;
let d = freeze.shallow({"a": 1});
d.clear();
`, "ImmutableError")

	// Typed-list append rejects wrong element type at runtime when
	// the typed list flows through an any-channel.
	runParity(t, `import io;
func bad(any container, any item): void {
    try {
        (container as list<int>).append(item);
    } catch (TypeError e) {
        io.println(e.message);
    }
}
list<int> xs = [1, 2];
bad(xs, "oops");
io.println(xs);
`, "cannot append string to list<int>\n[1, 2]\n")

	// Typed-list extend rejects wrong element type.
	runParity(t, `import io;
func bad(any container, any items): void {
    try {
        (container as list<int>).extend(items as list<any>);
    } catch (TypeError e) {
        io.println(e.message);
    }
}
list<int> xs = [1, 2];
bad(xs, [3, "oops"]);
io.println(xs);
`, "cannot extend list<int> with string at index 1\n[1, 2]\n")

	// append returns null (in-place methods do not return the receiver).
	runParity(t, `import io;
let xs = [1, 2];
let r = xs.append(3);
io.println(r);
`, "null\n")

	// extend rejects non-list argument.
	runErrorParity(t, `let xs = [1, 2]; xs.extend(99);`, "list.extend expects a list argument")
}

func TestParityListCopyMethods(t *testing.T) {
	// reverse returns a new list; original unchanged.
	runParity(t, `import io;
let xs = [1, 2, 3];
io.println(xs.reverse());
io.println(xs);
`, "[3, 2, 1]\n[1, 2, 3]\n")

	// reversed is an alias of reverse.
	runParity(t, `import io;
io.println([1, 2, 3].reversed());
`, "[3, 2, 1]\n")

	// reverse on empty.
	runParity(t, `import io; io.println(([] as list<int>).reverse());`, "[]\n")

	// reverse chains after sorted.
	runParity(t, `import io;
io.println([3, 1, 4, 1, 5].sorted().reverse());
`, "[5, 4, 3, 1, 1]\n")

	// prepend returns a new list with value at the front.
	runParity(t, `import io;
let xs = [2, 3];
io.println(xs.prepend(1));
io.println(xs);
`, "[1, 2, 3]\n[2, 3]\n")

	// unshift is an alias of prepend.
	runParity(t, `import io;
io.println([2, 3].unshift(1));
`, "[1, 2, 3]\n")

	// remove drops the first matching element.
	runParity(t, `import io;
let xs = [3, 1, 4, 1, 5];
io.println(xs.remove(1));
io.println(xs);
`, "[3, 4, 1, 5]\n[3, 1, 4, 1, 5]\n")

	// remove with no match returns an equivalent list.
	runParity(t, `import io;
io.println([1, 2, 3].remove(99));
`, "[1, 2, 3]\n")
}

func TestParitySecureRandom(t *testing.T) {
	// Deterministic seed; both backends must produce identical draws + outcomes.
	const seedHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	runParity(t, `import io;
import secureRandom;
let s = secureRandom.fromSeed("`+seedHex+`", "Alice");
io.println(secureRandom.commitment(s));
io.println(secureRandom.uintRange(s, 1, 7));
io.println(secureRandom.uintRange(s, 0, 52));
io.println(secureRandom.bool(s));
io.println(secureRandom.float(s));
io.println(secureRandom.choice(s, ["A", "K", "Q", "J"]));
io.println(secureRandom.shuffle(s, [1, 2, 3, 4, 5, 6]));
io.println(secureRandom.weightedChoice(s, ["x", "y", "z"], [1.0, 2.0, 3.0]));
`, "4884fdaafea47c29fea7159d0daddd9c085d6200e1359e85bb81736af6b7c837\n1\n41\ntrue\n0.5284162290333269\nQ\n[5, 4, 6, 1, 3, 2]\ny\n")

	// Verifier: commitment passes for the right seed, fails for the wrong one.
	runParity(t, `import io;
import secureRandom;
let s = secureRandom.fromSeed("`+seedHex+`", "Bob");
let commit = secureRandom.commitment(s);
let seed = secureRandom.reveal(s);
io.println(secureRandom.verifyCommitment(commit, seed));
io.println(secureRandom.verifyCommitment(commit, "0000000000000000000000000000000000000000000000000000000000000000"));
`, "true\nfalse\n")

	// Replay reproduces the same outcome from raw inputs.
	runParity(t, `import io;
import secureRandom;
let s = secureRandom.fromSeed("`+seedHex+`", "Carol");
let v1 = secureRandom.uintRange(s, 100, 1000);
let v2 = secureRandom.uintRange(s, 100, 1000);
let seed = secureRandom.reveal(s);
let r1 = secureRandom.replay(seed, "Carol", 0, "uintRange", [100, 1000]);
let r2 = secureRandom.replay(seed, "Carol", 1, "uintRange", [100, 1000]);
io.println(v1 == r1);
io.println(v2 == r2);
`, "true\ntrue\n")
}

// Dual-name modules: stdlib `async.sync` wraps native `async.sync`.
// External callers see classes and native free functions on one alias.
func TestParityDualNameModule(t *testing.T) {
	runParityWithStdlib(t, `import io;
import async.sync as sync;
let m = sync.Mutex();
m.lock();
io.println("class works");
m.unlock();
let h = sync.mutexNew();
sync.mutexLock(h);
io.println("native works");
sync.mutexUnlock(h);
`, "class works\nnative works\n")
}

func TestParitySelect(t *testing.T) {
	// Default fires when no case is ready.
	runParityWithStdlib(t, `import io;
import async.channel as ch;
let c = ch.Channel<int>(0);
select {
    case let v = c.recv(): { io.println("recv: " + (v as string)); }
    default: { io.println("default"); }
}
`, "default\n")

	// recv from a buffered channel fires immediately.
	runParityWithStdlib(t, `import io;
import async.channel as ch;
let c = ch.Channel<int>(2);
c.send(7);
select {
    case let v = c.recv(): { io.println("got: " + (v as string)); }
    default: { io.println("default"); }
}
`, "got: 7\n")

	// send to a buffered channel fires immediately when space is available.
	runParityWithStdlib(t, `import io;
import async.channel as ch;
let c = ch.Channel<int>(2);
select {
    case c.send(42): { io.println("sent"); }
    default: { io.println("blocked"); }
}
io.println(c.recv());
`, "sent\n42\n")

	// Multi-case picks the only ready one.
	runParityWithStdlib(t, `import io;
import async.channel as ch;
let a = ch.Channel<int>(1);
let b = ch.Channel<int>(1);
b.send(99);
select {
    case let v = a.recv(): { io.println("a: " + (v as string)); }
    case let v = b.recv(): { io.println("b: " + (v as string)); }
}
`, "b: 99\n")

	// recv without binding works.
	runParityWithStdlib(t, `import io;
import async.channel as ch;
let c = ch.Channel<int>(1);
c.send(5);
select {
    case c.recv(): { io.println("drained"); }
    default: { io.println("nope"); }
}
`, "drained\n")
}

func TestParityAsyncChannel(t *testing.T) {
	// Buffered: send three values, then drain.
	runParityWithStdlib(t, `import io;
import async.channel as ch;
let c = ch.Channel<int>(3);
c.send(1); c.send(2); c.send(3);
io.println(c.recv());
io.println(c.recv());
io.println(c.recv());
`, "1\n2\n3\n")

	// trySend / tryRecv behaviour.
	runParityWithStdlib(t, `import io;
import async.channel as ch;
let c = ch.Channel<int>(2);
io.println(c.trySend(10));
io.println(c.trySend(20));
io.println(c.trySend(30));
io.println(c.tryRecv());
io.println(c.tryRecv());
io.println(c.tryRecv() == null);
`, "true\ntrue\nfalse\n10\n20\ntrue\n")

	// Close + drain + null on closed-empty + send-on-closed throws.
	runParityWithStdlib(t, `import io;
import async.channel as ch;
let c = ch.Channel<int>(2);
c.send(7);
c.close();
io.println(c.isClosed());
io.println(c.recv());
io.println(c.recv() == null);
try { c.send(8); } catch (Error e) { io.println("send on closed throws"); }
try { c.close(); } catch (Error e) { io.println("double close throws"); }
`, "true\n7\ntrue\nsend on closed throws\ndouble close throws\n")

	// Producer + consumer via async.run.
	runParityWithStdlib(t, `import io;
import async;
import async.channel as ch;
let c = ch.Channel<int>(0);
async.run(func(): void {
    for (let int i = 1; i <= 4; i++) { c.send(i * 11); }
    c.close();
});
let total = 0;
for (var v in c) { total = total + v; }
io.println(total);
`, "110\n")
}

func TestParityAsyncSync(t *testing.T) {
	// Mutex tryLock semantics + atomics arithmetic round-trip.
	runParityWithStdlib(t, `import io;
import async.sync as sync;
import async.atomic as atomic;

let m = sync.Mutex();
m.lock();
io.println(m.tryLock());
m.unlock();
io.println(m.tryLock());
m.unlock();

let c = atomic.AtomicInt(0);
io.println(c.load());
io.println(c.add(5));
io.println(c.add(-2));
io.println(c.compareAndSwap(3, 42));
io.println(c.compareAndSwap(99, 0));
io.println(c.load());
`, "false\ntrue\n0\n5\n3\ntrue\nfalse\n42\n")

	// Semaphore acquire/tryAcquire/release.
	runParityWithStdlib(t, `import io;
import async.sync as sync;
let s = sync.Semaphore(2);
io.println(s.tryAcquire());
io.println(s.tryAcquire());
io.println(s.tryAcquire());
s.release();
io.println(s.tryAcquire());
`, "true\ntrue\nfalse\ntrue\n")

	// RWMutex: multiple read locks + a write lock.
	runParityWithStdlib(t, `import io;
import async.sync as sync;
let rw = sync.RWMutex();
io.println(rw.tryRLock());
io.println(rw.tryRLock());
io.println(rw.tryLock());
rw.rUnlock();
rw.rUnlock();
io.println(rw.tryLock());
rw.unlock();
`, "true\ntrue\nfalse\ntrue\n")

	// AtomicBool basics.
	runParityWithStdlib(t, `import io;
import async.atomic as atomic;
let f = atomic.AtomicBool();
io.println(f.load());
f.store(true);
io.println(f.load());
io.println(f.compareAndSwap(true, false));
io.println(f.compareAndSwap(true, false));
io.println(f.load());
`, "false\ntrue\ntrue\nfalse\nfalse\n")
}

func TestParityCron(t *testing.T) {
	// Parse + field values, then validate + special.
	runParity(t, `import io;
import cron;
let p = cron.parse("0 9 * * 1-5");
io.println(p["minute"]);
io.println(p["hour"]);
io.println(p["dayOfWeek"]);
io.println(p["special"]);
io.println(cron.isValid("0 9 * * 1-5"));
io.println(cron.isValid("nope"));
io.println(cron.isValid("@daily"));
let d = cron.parse("@daily");
io.println(d["special"]);
io.println(d["minute"]);
io.println(d["hour"]);
`, "[0]\n[9]\n[1, 2, 3, 4, 5]\nnull\ntrue\nfalse\ntrue\n@daily\n[0]\n[0]\n")

	// nextAfter: 2025-02-01T00:00:00Z (Sunday) -> 2025-02-03T09:00:00Z.
	runParity(t, `import io;
import cron;
io.println(cron.nextAfter("0 9 * * 1-5", 1738368000));
`, "1738573200\n")

	// nextN returns the next N occurrences.
	runParity(t, `import io;
import cron;
let firings = cron.nextN("0 * * * *", 0, 3);
io.println(firings);
`, "[3600, 7200, 10800]\n")

	// Named months and days work case-insensitively.
	runParity(t, `import io;
import cron;
let p = cron.parse("0 0 * jan,jul mon");
io.println(p["month"]);
io.println(p["dayOfWeek"]);
`, "[1, 7]\n[1]\n")

	// Step expressions.
	runParity(t, `import io;
import cron;
let p = cron.parse("*/15 0 * * *");
io.println(p["minute"]);
`, "[0, 15, 30, 45]\n")
}

func TestParityNetIP(t *testing.T) {
	// parseIp + parseCidr core fields.
	runParityStateful(t, `import io;
import net;
let ip = net.parseIp("10.0.0.1");
io.println(ip["version"]);
io.println(ip["address"]);

let c = net.parseCidr("10.0.0.0/8");
io.println(c["network"]);
io.println(c["prefixLen"]);
io.println(c["version"]);
io.println(c["first"]);
io.println(c["last"]);
io.println(c["count"]);
`, "4\n10.0.0.1\n10.0.0.0\n8\n4\n10.0.0.0\n10.255.255.255\n16777216\n")

	// cidrContains positive + negative.
	runParityStateful(t, `import io;
import net;
io.println(net.cidrContains("10.0.0.0/8", "10.5.5.5"));
io.println(net.cidrContains("10.0.0.0/8", "11.0.0.0"));
io.println(net.cidrContains("192.168.0.0/16", "192.168.42.1"));
io.println(net.cidrContains("192.168.0.0/16", "10.0.0.1"));
`, "true\nfalse\ntrue\nfalse\n")

	// IPv6 CIDR + count overflows int64 -> Int.
	runParityStateful(t, `import io;
import net;
let c = net.parseCidr("2001:db8::/32");
io.println(c["version"]);
io.println(c["first"]);
io.println(c["count"]);
`, "6\n2001:db8::\n79228162514264337593543950336\n")

	// Classification helpers.
	runParityStateful(t, `import io;
import net;
io.println(net.isIpv4("192.168.1.1"));
io.println(net.isIpv4("::1"));
io.println(net.isIpv4("not-an-ip"));
io.println(net.isIpv6("::1"));
io.println(net.isIpv6("192.168.1.1"));
`, "true\nfalse\nfalse\ntrue\nfalse\n")

	// Bytes round trip.
	runParityStateful(t, `import io;
import net;
import bytes;
let b = net.ipToBytes("192.168.1.1");
io.println(b.length());
io.println(bytes.toHex(b));
io.println(net.ipFromBytes(b));
`, "4\nc0a80101\n192.168.1.1\n")
}

func TestParityUnicode(t *testing.T) {
	// Round-trip NFC <-> NFD on a single accented character.
	// NFC e-acute = U+00E9 (1 code point); NFD = U+0065 + U+0301 (2).
	runParity(t, `import io;
import unicode;
let nfc = "é";
let nfd = "é";
io.println(unicode.normalize(nfd, "NFC") == nfc);
io.println(unicode.normalize(nfc, "NFD") == nfd);
io.println(nfc.length());
io.println(nfd.length());
`, "true\ntrue\n1\n2\n")

	// isNormalized reports both directions.
	runParity(t, `import io;
import unicode;
io.println(unicode.isNormalized("é", "NFC"));
io.println(unicode.isNormalized("é", "NFC"));
io.println(unicode.isNormalized("é", "NFD"));
io.println(unicode.isNormalized("é", "NFD"));
`, "true\nfalse\nfalse\ntrue\n")

	// Compatibility decomposition: ligature fi (U+FB01) -> "fi" under NFKC / NFKD.
	runParity(t, `import io;
import unicode;
let lig = "ﬁ";
io.println(unicode.normalize(lig, "NFKC"));
io.println(unicode.normalize(lig, "NFKD"));
io.println(unicode.normalize(lig, "NFC") == lig);
`, "fi\nfi\ntrue\n")

	// Unknown form name throws.
	runParity(t, `import io;
import unicode;
try {
    unicode.normalize("x", "BAD");
} catch (Error e) {
    io.println("caught");
}
`, "caught\n")
}

func TestParityMsgpack(t *testing.T) {
	// Primitives encode to the spec-fixed byte sequences.
	runParity(t, `import io;
import msgpack;
import bytes;
io.println(bytes.toHex(msgpack.encode(null)));
io.println(bytes.toHex(msgpack.encode(true)));
io.println(bytes.toHex(msgpack.encode(false)));
io.println(bytes.toHex(msgpack.encode(0)));
io.println(bytes.toHex(msgpack.encode(127)));
io.println(bytes.toHex(msgpack.encode(-1)));
io.println(bytes.toHex(msgpack.encode(-32)));
io.println(bytes.toHex(msgpack.encode("hello")));
io.println(bytes.toHex(msgpack.encode([])));
io.println(bytes.toHex(msgpack.encode([1, 2, 3])));
io.println(bytes.toHex(msgpack.encode({})));
`, "c0\nc3\nc2\n00\n7f\nff\ne0\na568656c6c6f\n90\n93010203\n80\n")

	// Round trip for nested structures.
	runParity(t, `import io;
import msgpack;
let v = {"items": [1, 2, 3], "meta": {"k": "v"}};
let b = msgpack.encode(v);
let back = msgpack.decode(b);
io.println(back["items"]);
io.println(back["meta"]["k"]);
`, "[1, 2, 3]\nv\n")

	// Float is preserved as float; decimal as a lossless string.
	runParity(t, `import io;
import msgpack;
let f = (1.5 as float);
io.println(msgpack.decode(msgpack.encode(f)));
let d = 1.5;
io.println(msgpack.decode(msgpack.encode(d)));
`, "1.5\n1.5000000000\n")

	// validate + tryDecode behaviours.
	runParity(t, `import io;
import msgpack;
import bytes;
io.println(msgpack.validate(msgpack.encode([1, 2, 3])));
io.println(msgpack.validate(bytes.fromHex("ff80")));
io.println(msgpack.tryDecode(bytes.fromHex("ff80")));
io.println(msgpack.tryDecode(bytes.fromHex("a3616263")));
`, "true\nfalse\nnull\nabc\n")

	// int boundaries spill into the larger integer tags.
	runParity(t, `import io;
import msgpack;
import bytes;
io.println(bytes.toHex(msgpack.encode(128)));
io.println(bytes.toHex(msgpack.encode(1000)));
io.println(bytes.toHex(msgpack.encode(-1000)));
io.println(msgpack.decode(msgpack.encode(128)));
io.println(msgpack.decode(msgpack.encode(1000)));
io.println(msgpack.decode(msgpack.encode(-1000)));
`, "d10080\nd103e8\nd1fc18\n128\n1000\n-1000\n")

	// Bytes round-trip.
	runParity(t, `import io;
import msgpack;
import bytes;
let b = bytes.fromHex("deadbeef");
let enc = msgpack.encode(b);
io.println(bytes.toHex(enc));
io.println(bytes.toHex(msgpack.decode(enc)));
`, "c404deadbeef\ndeadbeef\n")
}

func TestParityLruCache(t *testing.T) {
	// Basic put / get with eviction order.
	runParityWithStdlib(t, `import io;
import lrucache;
let c = lrucache.LruCache<string, int>(3);
c.put("a", 1); c.put("b", 2); c.put("c", 3);
io.println(c.length());
io.println(c.get("a"));
c.put("d", 4);
io.println(c.get("a"));
io.println(c.get("b"));
io.println(c.get("c"));
io.println(c.get("d"));
`, "3\n1\n1\nnull\n3\n4\n")

	// has() does not bump LRU order; delete() removes.
	runParityWithStdlib(t, `import io;
import lrucache;
let c = lrucache.LruCache<string, int>(2);
c.put("x", 1);
c.put("y", 2);
io.println(c.has("x"));
c.put("z", 3);
io.println(c.has("x"));
io.println(c.delete("y"));
io.println(c.delete("missing"));
io.println(c.length());
`, "true\nfalse\ntrue\nfalse\n1\n")

	// Stats counters - field access avoids dict display-order
	// divergence between backends.
	runParityWithStdlib(t, `import io;
import lrucache;
let c = lrucache.LruCache<string, int>(2);
c.put("a", 1);
c.put("b", 2);
c.get("a");
c.get("a");
c.get("missing");
c.put("c", 3);
let s = c.stats();
io.println(s["hits"]);
io.println(s["misses"]);
io.println(s["evictions"]);
io.println(s["expirations"]);
`, "2\n1\n1\n0\n")

	// Capacity must be at least 1.
	runParityWithStdlib(t, `import io;
import lrucache;
try {
    let c = lrucache.LruCache<string, int>(0);
} catch (ValueError e) {
    io.println("caught");
}
`, "caught\n")
}

// Regression: the VM's user-iterator dispatch looked the iterator's
// class up via the running chunk's classInfo, which fails for an
// instance whose class is defined in another module. Iteration of
// any stdlib class that implements __iter / __done / __next reported
// "is not an iterator" even though the trampoline table populated at
// import time exposed the methods. Fix routes the presence check
// through iter.Class.Methods for foreign classes and lets thrown
// errors flow back through propagateModuleError so try / catch
// around the loop still fires.
func TestParityIterAcrossStdlibBoundary(t *testing.T) {
	runParityWithStdlib(t, `import io;
import deque;
let d = deque.Deque<int>();
d.pushBack(1); d.pushBack(2); d.pushBack(3);
for (var x in d) {
    io.println(x);
}
`, "1\n2\n3\n")
}

// Regression: the VM's foreign-class method dispatch was wrapping
// the native trampoline's error via runtimeError, which stripped the
// vmThrownError chain and prevented a try/catch in the calling module
// from catching exceptions thrown inside a stdlib class method.
func TestParityCatchAcrossStdlibBoundary(t *testing.T) {
	runParityWithStdlib(t, `import io;
import option;
let o = option.Option(false, 0);
try {
    o.unwrap();
} catch (ValueError e) {
    io.println("caught: " + e.message);
}
io.println("done");
`, "caught: Option.unwrap called on None\ndone\n")
}

func TestParityPriorityQueue(t *testing.T) {
	// Natural-order min-heap drains in ascending order.
	runParityWithStdlib(t, `import io;
import priorityq;
let q = priorityq.PriorityQueue<int>();
q.push(3); q.push(1); q.push(4); q.push(1); q.push(5); q.push(9); q.push(2); q.push(6);
io.println(q.length());
io.println(q.peek());
while (!q.isEmpty()) {
    io.println(q.pop());
}
`, "8\n1\n1\n1\n2\n3\n4\n5\n6\n9\n")

	// Custom comparator reverses the order.
	runParityWithStdlib(t, `import io;
import priorityq;
let q = priorityq.PriorityQueue<int>(func(int a, int b): int { return b - a; });
q.push(2); q.push(7); q.push(1); q.push(5);
io.println(q.pop());
io.println(q.pop());
io.println(q.pop());
io.println(q.pop());
`, "7\n5\n2\n1\n")

	// pushPop, drain, clear, empty errors.
	runParityWithStdlib(t, `import io;
import priorityq;
let q = priorityq.PriorityQueue<int>();
q.push(5); q.push(10); q.push(1);
io.println(q.pushPop(0));
io.println(q.pushPop(7));
io.println(q.drain());
io.println(q.isEmpty());

q.push(3); q.push(2);
q.clear();
io.println(q.length());

try { q.pop(); } catch (ValueError e) { io.println("empty"); }
`, "0\n1\n[5, 7, 10]\ntrue\n0\nempty\n")
}

func TestParityAssert(t *testing.T) {
	// Truthy assert is a no-op; falsy assert throws AssertionError.
	runParity(t, `import io;
assert(1 + 1 == 2);
io.println("ok");
try {
    assert(1 == 2);
} catch (AssertionError e) {
    io.println("caught: " + e.message);
}
`, "ok\ncaught: assertion failed: (1 == 2)\n")

	// Explicit message overrides the default source-text rendering.
	runParity(t, `import io;
try {
    assert(false, "custom message");
} catch (AssertionError e) {
    io.println(e.message);
}
`, "custom message\n")

	// AssertionError is a subclass of Error.
	runParity(t, `import io;
try {
    assert(false, "boom");
} catch (Error e) {
    io.println(e.class);
}
`, "AssertionError\n")
}

func TestParityFStringFormatSpecs(t *testing.T) {
	runParity(t, `import io;
let pi = 3.14159;
io.println("${pi:.2f}");
io.println("${pi:.4f}");
`, "3.14\n3.1416\n")

	runParity(t, `import io;
io.println("${100000:,}");
io.println("${1234567:,}");
`, "100,000\n1,234,567\n")

	runParity(t, `import io;
io.println("${42:>5}|");
io.println("${42:<5}|");
io.println("${42:^5}|");
io.println("${42:05}");
`, "   42|\n42   |\n 42  |\n00042\n")

	runParity(t, `import io;
io.println("${255:x}");
io.println("${255:X}");
io.println("${255:o}");
io.println("${15:b}");
`, "ff\nFF\n377\n1111\n")

	runParity(t, `import io;
io.println("${0.5:%}");
io.println("${42:+d}");
io.println("${-42:+d}");
`, "50.000000%\n+42\n-42\n")

	runParity(t, `import io;
let name = "Ada";
io.println("${name:>10}|");
io.println("${name:<10}|");
io.println("${name:^10}|");
io.println("${name:.2}");
`, "       Ada|\nAda       |\n   Ada    |\nAd\n")

	// Spec separator inside a ternary should not be confused for format-spec.
	runParity(t, `import io;
let x = 5;
io.println("${true ? x : 0}");
io.println("${(true ? x : 0):03d}");
`, "5\n005\n")
}

func TestParityDecimalFormatSpec(t *testing.T) {
	// A decimal formats from its exact value, not a float64 round-trip,
	// so :f matches toString and shows no binary noise.
	runParity(t, `import io;
let d = (3.1415926536 as decimal);
io.println("${d:.13f}");
io.println(d.toString(13));
io.println("${(2.567 as decimal):.2f}");
io.println("${(-2.5 as decimal):.3f}");
io.println("${(0.125 as decimal):.1%}");
io.println("${(1234.5 as decimal):,.2f}");
`, "3.1415926536000\n3.1415926536000\n2.57\n-2.500\n12.5%\n1,234.50\n")
}

func TestParityOrPatterns(t *testing.T) {
	// Literal alternation.
	runParity(t, `import io;
func describe(int x): string {
    return match (x) {
        case 1 | 2 | 3 => "low";
        case 10 | 20 | 30 => "med";
        default => "other";
    };
}
io.println(describe(1));
io.println(describe(2));
io.println(describe(20));
io.println(describe(50));
`, "low\nlow\nmed\nother\n")

	// Bare-type alternation (union type form is parsed as Type).
	runParity(t, `import io;
func anyNumeric(any v): string {
    return match (v) {
        case int | float | decimal => "numeric";
        case string => "text";
        default => "other";
    };
}
io.println(anyNumeric(5));
io.println(anyNumeric(3.14));
io.println(anyNumeric("hi"));
io.println(anyNumeric(true));
`, "numeric\nnumeric\ntext\nother\n")

	// Enum-no-payload alternation.
	runParity(t, `import io;
enum Color { Red, Green, Blue }
func warm(Color c): bool {
    return match (c) {
        case Color.Red | Color.Blue => true;
        case Color.Green => false;
    };
}
io.println(warm(Color.Red));
io.println(warm(Color.Blue));
io.println(warm(Color.Green));
`, "true\ntrue\nfalse\n")

	// Guard applies to the whole or-pattern.
	runParity(t, `import io;
func withGuard(int x): string {
    return match (x) {
        case 1 | 2 | 3 if (x > 1) => "pass";
        case 1 | 2 | 3 => "fail";
        default => "other";
    };
}
io.println(withGuard(1));
io.println(withGuard(2));
io.println(withGuard(99));
`, "fail\npass\nother\n")
}

func TestParityLiteralSpread(t *testing.T) {
	// List literal spread (already worked before L3; regression guard).
	runParity(t, `import io;
let xs = [1, 2, 3];
io.println([0, ...xs, 4]);
`, "[0, 1, 2, 3, 4]\n")

	// Dict literal spread - probe via content (length + key lookups) to
	// sidestep a pre-existing parity bug in dict iteration order display.
	runParity(t, `import io;
let d1 = {"a": 1, "b": 2};
let d2 = {"x": 0, ...d1, "y": 4};
io.println(d2.length());
io.println(d2["x"]);
io.println(d2["a"]);
io.println(d2["b"]);
io.println(d2["y"]);
`, "4\n0\n1\n2\n4\n")

	// Last-write-wins on key collision.
	runParity(t, `import io;
let d1 = {"a": 1, "b": 2};
let d3 = {...d1, "b": 99};
io.println(d3.length());
io.println(d3["a"]);
io.println(d3["b"]);
`, "2\n1\n99\n")

	// Multiple spread sources, later wins.
	runParity(t, `import io;
let a = {"x": 1, "y": 2};
let b = {"y": 99, "z": 3};
let m = {...a, ...b};
io.println(m.length());
io.println(m["x"]);
io.println(m["y"]);
io.println(m["z"]);
`, "3\n1\n99\n3\n")

	// Set literal spread.
	runParity(t, `import io;
let s1 = {1, 2, 3};
let s2 = {0, ...s1, 4};
io.println(s2.length());
io.println(s2.contains(0));
io.println(s2.contains(2));
io.println(s2.contains(4));
`, "5\ntrue\ntrue\ntrue\n")

	// Set spread from list source.
	runParity(t, `import io;
let xs = [10, 20];
let s = {...xs, 30};
io.println(s.length());
io.println(s.contains(10));
io.println(s.contains(30));
`, "3\ntrue\ntrue\n")

}

func TestParityPipeOperator(t *testing.T) {
	runParity(t, `import io;
func double(int x): int { return x * 2; }
io.println(5 |> double());
`, "10\n")

	runParity(t, `import io;
func double(int x): int { return x * 2; }
io.println(5 |> double);
`, "10\n")

	runParity(t, `import io;
func add(int a, int b): int { return a + b; }
io.println(5 |> add(3));
`, "8\n")

	runParity(t, `import io;
func double(int x): int { return x * 2; }
func add(int a, int b): int { return a + b; }
io.println(5 |> double() |> add(1));
`, "11\n")

	runParity(t, `import io;
func double(int x): int { return x * 2; }
func add(int a, int b): int { return a + b; }
io.println(10 |> add(5) |> double);
`, "30\n")

	runParity(t, `import io;
class S { static func tag(string s, string suffix): string { return s + ":" + suffix; } }
io.println("hi" |> S.tag("end"));
`, "hi:end\n")
}

func TestParityComprehensions(t *testing.T) {
	runParity(t, `import io;
io.println([x * 2 for x in [1, 2, 3, 4, 5]]);
`, "[2, 4, 6, 8, 10]\n")

	runParity(t, `import io;
io.println([x for x in [1, 2, 3, 4, 5] if x % 2 == 0]);
`, "[2, 4]\n")

	runParity(t, `import io;
io.println([x for x in [1, 2, 3, 4, 5] if x > 1 if x < 5]);
`, "[2, 3, 4]\n")

	runParity(t, `import io;
io.println([x * y for x in [1, 2, 3] for y in [10, 20, 30]]);
`, "[10, 20, 30, 20, 40, 60, 30, 60, 90]\n")

	runParity(t, `import io;
io.println({x * x for x in [1, 2, 3, 2, 1]});
`, "set{1, 4, 9}\n")

	runParity(t, `import io;
io.println({x: x * x for x in [1, 2, 3]});
`, "{1: 1, 2: 4, 3: 9}\n")

	runParity(t, `import io;
io.println([x * 2 for int x in [1, 2, 3]]);
`, "[2, 4, 6]\n")

	runParity(t, `import io;
let mul = 100;
io.println([x * mul for x in [1, 2, 3]]);
`, "[100, 200, 300]\n")

	runParity(t, `import io;
io.println([x * y for x in [1, 2, 3] if x > 1 for y in [10, 20] if y > 10]);
`, "[40, 60]\n")

	runParity(t, `import io;
io.println([[y for y in range(0, x)] for x in range(0, 4)]);
`, "[[0], [0, 1], [0, 1, 2], [0, 1, 2, 3], [0, 1, 2, 3, 4]]\n")

	runParity(t, `import io;
io.println([x for x in []]);
`, "[]\n")
}

func TestParityJumpIfModZero(t *testing.T) {
	runParity(t, `import io;
int total = 0;
for (int i = 0; i < 12; i++) {
    if (i % 3 == 0) { total = total + i; }
    else            { total = total - 1; }
}
io.println(total);
`, "10\n")

	runParity(t, `import io;
int n = 0;
for (int i = 0; i < 10; i++) {
    if (i % 2 != 0) { n = n + 1; }
}
io.println(n);
`, "5\n")

	// 0 == i % d reversed form takes the same fused opcode.
	runParity(t, `import io;
int n = 0;
for (int i = 0; i < 9; i++) {
    if (0 == i % 4) { n = n + 1; }
}
io.println(n);
`, "3\n")

	// Negative dividend exercises the modulo-correction branch.
	runParity(t, `import io;
int n = 0;
for (int i = -6; i <= 0; i++) {
    if (i % 3 == 0) { n = n + 1; }
}
io.println(n);
`, "3\n")
}

func TestParityDictSpreadIgnoresExtras(t *testing.T) {
	runParity(t, `import io;
func greet(string name, int age, bool active = true): string {
    return name + "/" + (age as string) + "/" + (active as string);
}
let d = {"name": "bob", "age": 43, "extra": 9.9, "active": false};
io.println(greet(...d));
`, "bob/43/false\n")

	runParity(t, `import io;
func greet(string name, int age, bool active = true): string {
    return name + "/" + (age as string) + "/" + (active as string);
}
let d = {"name": "alice", "age": 30, "ignored": 1};
io.println(greet(...d));
`, "alice/30/true\n")

	runParity(t, `import io;
func greet(string name, int age, bool active = true): string {
    return name + "/" + (age as string) + "/" + (active as string);
}
let d = {"age": 60, "active": false, "junk": "x"};
io.println(greet("frank", ...d));
`, "frank/60/false\n")
}

func TestParityDictAliases(t *testing.T) {
	// entries is an alias of items.
	runParity(t, `import io;
io.println({"a": 1, "b": 2}.entries());
`, "[[\"a\", 1], [\"b\", 2]]\n")

	// insert is an alias of set; both mutate in place and return null.
	runParity(t, `import io;
let d = {"a": 1};
let r = d.insert("b", 2);
io.println(r);
io.println(d);
`, "null\n{\"a\": 1, \"b\": 2}\n")

	// remove is an alias of delete on dicts.
	runParity(t, `import io;
let d = {"a": 1, "b": 2};
d.remove("a");
io.println(d);
`, "{\"b\": 2}\n")
}

func TestParityFreeze(t *testing.T) {
	// freeze.shallow prevents list index mutation.
	runErrorParity(t, `import freeze; let x = freeze.shallow([1,2,3]); x[0] = 99;`, "ImmutableError")

	// freeze.shallow prevents dict mutation.
	runErrorParity(t, `import freeze; let x = freeze.shallow({"a": 1}); x["a"] = 2;`, "ImmutableError")

	// freeze.isFrozen returns false for unfrozen collection.
	runParity(t, `import freeze; import io; io.println(freeze.isFrozen([1,2]));`, "false\n")

	// freeze.isFrozen returns true after freeze.shallow.
	runParity(t, `import freeze; import io; let x = freeze.shallow([1,2]); io.println(freeze.isFrozen(x));`, "true\n")

	// primitives are always considered frozen.
	runParity(t, `import freeze; import io; io.println(freeze.isFrozen(42));`, "true\n")

	// const shallow-freezes a list.
	runErrorParity(t, `const x = [1,2,3]; x[0] = 99;`, "ImmutableError")

	// const shallow-freezes a dict.
	runErrorParity(t, `const x = {"a": 1}; x["a"] = 2;`, "ImmutableError")

	// .copy() returns a mutable copy of a frozen list.
	runParity(t, `import freeze; import io; let x = freeze.shallow([1,2,3]); let y = x.copy(); y[0] = 99; io.println(y[0]);`, "99\n")

	// .copy() returns a mutable copy of a frozen dict.
	runParity(t, `import freeze; import io; let x = freeze.shallow({"a": 1}); let y = x.copy(); y["a"] = 2; io.println(y["a"]);`, "2\n")

	// ImmutableError is catchable.
	runParity(t, `import freeze; import io;
let x = freeze.shallow([1]);
try { x[0] = 99; } catch (ImmutableError e) { io.println("caught"); }
`, "caught\n")

	// freeze.deep freezes nested collections.
	runErrorParity(t, `import freeze;
let x = freeze.deep([[1,2],[3,4]]); x[0][0] = 99;`, "ImmutableError")

	// @immutable class instance cannot have fields mutated after construction.
	runErrorParity(t, `
@immutable class Point {
    int x;
    int y;
    func Point(int x, int y) { this.x = x; this.y = y; }
}
let p = Point(1, 2); p.x = 99;
`, "ImmutableError")

	// @immutable instance fields are readable.
	runParity(t, `import io;
@immutable class Point {
    int x;
    int y;
    func Point(int x, int y) { this.x = x; this.y = y; }
}
let p = Point(3, 4); io.println(p.x);
`, "3\n")
}

// TestParitySmallIntArithmetic verifies that integer arithmetic works correctly
// for values in the int64 range (SmallInt fast path in VM) and produces correct
// results visible at the Geblang level regardless of internal representation.
func TestParitySmallIntArithmetic(t *testing.T) {
	// Basic arithmetic
	runParity(t, `import io; io.println(2 + 3);`, "5\n")
	runParity(t, `import io; io.println(10 - 4);`, "6\n")
	runParity(t, `import io; io.println(3 * 7);`, "21\n")
	runParity(t, `import io; io.println(15 / 4);`, "3.7500000000\n")
	runParity(t, `import io; io.println(15 // 4);`, "3\n")
	runParity(t, `import io; io.println(17 % 5);`, "2\n")

	// Comparisons between int literals
	runParity(t, `import io; io.println(4 == 4);`, "true\n")
	runParity(t, `import io; io.println(4 != 5);`, "true\n")
	runParity(t, `import io; io.println(3 < 4);`, "true\n")
	runParity(t, `import io; io.println(4 <= 4);`, "true\n")
	runParity(t, `import io; io.println(5 > 4);`, "true\n")
	runParity(t, `import io; io.println(4 >= 4);`, "true\n")

	// Arithmetic with variables
	runParity(t, `import io; let x = 10; let y = 3; io.println(x + y);`, "13\n")
	runParity(t, `import io; let x = 10; let y = 3; io.println(x * y);`, "30\n")
	runParity(t, `import io; let x = 10; let y = 3; io.println(x % y);`, "1\n")

	// Negation
	runParity(t, `import io; let x = 5; io.println(-x);`, "-5\n")
	runParity(t, `import io; io.println(-(3 + 2));`, "-5\n")

	// Loop accumulation (typical integer fast-path scenario)
	runParity(t, `import io;
let sum = 0;
for (let i = 0; i < 100; i = i + 1) {
    sum = sum + i;
}
io.println(sum);
`, "4950\n")

	// Type name is "int" for integer expressions
	runParity(t, `import io; io.println(typeof(42));`, "int\n")
	runParity(t, `import io; io.println(typeof(1 + 1));`, "int\n")
}

// TestParitySmallIntComparisons verifies comparison between integer values
// including those returned from native functions (which may return SmallInt).
func TestParitySmallIntComparisons(t *testing.T) {
	// secrets.randomInt returns a SmallInt; compare against literal (also SmallInt in VM)
	runParity(t, `import io; import secrets;
let n = secrets.randomInt(1, 100);
io.println(n >= 1);
io.println(n <= 100);
`, "true\ntrue\n")

	// Comparison in sorted/ordered context
	runParity(t, `import io;
let nums = [5, 3, 1, 4, 2];
let sorted = nums.sorted();
io.println(sorted[0]);
io.println(sorted[4]);
`, "1\n5\n")

	// Range with int bounds (1..5 is inclusive, gives 5 iterations)
	runParity(t, `import io;
let count = 0;
for (n in 1..5) { count = count + 1; }
io.println(count);
`, "5\n")
}

// TestParitySmallIntOverflow verifies that arithmetic overflowing int64 promotes
// correctly to arbitrary-precision Int rather than wrapping or erroring.
func TestParitySmallIntOverflow(t *testing.T) {
	// 2^62 * 4 overflows int64 (max is ~9.2e18); result must still be correct
	runParity(t, `import io;
let a = 4611686018427387904;
let b = a * 4;
io.println(b > 0);
`, "true\n")

	// Adding large values
	runParity(t, `import io;
let big = 9223372036854775807;
let bigger = big + 1;
io.println(bigger > big);
`, "true\n")
}

// TestParitySmallIntWithNativeFunctions verifies that functions accepting int
// arguments work correctly when passed SmallInt values (the VM literal path).
func TestParitySmallIntWithNativeFunctions(t *testing.T) {
	// list.chunk with int size
	runParity(t, `import io;
let parts = [1,2,3,4,5].chunk(2);
io.println(parts.length());
`, "3\n")

	// String repeat
	runParity(t, `import io; io.println("ab".repeat(3));`, "ababab\n")

	// list.topK
	runParity(t, `import io;
let top = [3,1,4,1,5,9,2,6].topK(3);
io.println(top.length());
`, "3\n")

	// Bitwise operations
	runParity(t, `import io; io.println(6 & 3);`, "2\n")
	runParity(t, `import io; io.println(6 | 3);`, "7\n")
	runParity(t, `import io; io.println(6 ^ 3);`, "5\n")
	runParity(t, `import io; io.println(1 << 4);`, "16\n")
	runParity(t, `import io; io.println(16 >> 2);`, "4\n")
}

// TestParitySliceStep covers the J1 Python-style xs[a:b:step] syntax
// including negative step for reversed iteration.
func TestParitySliceStep(t *testing.T) {
	runParity(t, `import io;
let xs = [10, 20, 30, 40, 50];
io.println(xs[::-1]);
io.println(xs[::2]);
io.println(xs[1:4]);
io.println("hello"[::-1]);
`, "[50, 40, 30, 20, 10]\n[10, 30, 50]\n[20, 30, 40]\nolleh\n")
}

// TestParityMultiAssign covers the J2 `a, b = b, a` multi-target form.
func TestParityMultiAssign(t *testing.T) {
	runParity(t, `import io;
let a = 1;
let b = 2;
a, b = b, a;
io.println(a);
io.println(b);
let x = 0;
let y = 0;
let z = 0;
x, y, z = [10, 20, 30];
io.println(x);
io.println(y);
io.println(z);
`, "2\n1\n10\n20\n30\n")
}

// TestParityCastBool covers the J7 numeric -> bool cast.
func TestParityCastBool(t *testing.T) {
	runParity(t, `import io;
io.println(1 as bool);
io.println(0 as bool);
io.println(-7 as bool);
io.println(3.14 as bool);
io.println(0.0 as bool);
io.println("true" as bool);
io.println("false" as bool);
io.println(null as bool);
`, "true\nfalse\ntrue\ntrue\nfalse\ntrue\nfalse\nfalse\n")
}

// TestParityNullableGeneric covers the fix for `?list<int> = null` which
// previously panicked on the VM and threw on the evaluator.
func TestParityNullableGeneric(t *testing.T) {
	runParity(t, `import io;
?list<int> xs = null;
io.println(xs);
xs = [1, 2, 3];
io.println(xs);
`, "null\n[1, 2, 3]\n")
}

// TestParityParentInheritedConstructor covers the evaluator parent() fix
// where multi-level inherited constructors recursed infinitely.
func TestParityParentInheritedConstructor(t *testing.T) {
	runParity(t, `import io;
class A extends Error { func A(string msg) { parent(msg); } }
class B extends A { func B(string msg) { parent(msg); } }
let e = B("kaboom");
io.println(e.message);
let caught = false;
try { throw B("kaboom"); } catch (A x) { caught = true; }
io.println(caught);
`, "kaboom\ntrue\n")
}

// TestParityVariadicClosure covers the fix where the bytecode compiler did
// not set FunctionInfo.Variadic for closure literals (only for top-level
// function statements). Variadic packing in startFunctionWithValidation
// silently passed the first variadic arg as a non-list, breaking any
// closure with `any ...args` semantics.
func TestParityVariadicClosure(t *testing.T) {
	runParity(t, `import io;
let f = func(any ...args): int { return args.length(); };
io.println(f());
io.println(f(1));
io.println(f(1, 2, 3));
`, "0\n1\n3\n")
}

// TestParitySpreadOnCallable covers the fix where the bytecode compiler
// did not emit a spread-aware opcode for calls of the form `fn(...args)`
// when fn is a local/global value (not a known top-level function). The
// spread list was passed as a single arg instead of being expanded.
func TestParitySpreadOnCallable(t *testing.T) {
	runParity(t, `import io;
func add(int a, int b, int c): int { return a + b + c; }
let fn = add;
let xs = [1, 2, 3];
io.println(fn(...xs));
let curry = func(any ...prefix): func {
  return func(any ...rest): any {
    let all = prefix.concat(rest);
    return fn(...all);
  };
};
let g = curry(10);
io.println(g(20, 30));
`, "6\n60\n")
}

// TestParityGenericInnerLambda covers the fix where a lambda declared
// inside a generic function's body could not reference the outer
// function's type parameter T directly. The captured type bindings
// now flow from the outer call frame into the closure value and into
// the lambda's call frame so the inner `T x` parameter resolves
// against the bound concrete type.
func TestParityGenericInnerLambda(t *testing.T) {
	runParity(t, `import io;
import collections;

interface Scored { func score(): int; }

class Player implements Scored {
    int rating;
    func Player(int r) { this.rating = r; }
    func score(): int { return this.rating; }
}

func topBy<T implements Scored>(list<T> items): T {
    return collections.maxBy(items, func(T x): int { return x.score(); });
}

let players = [Player(1), Player(3), Player(2)];
io.println(topBy(players).score());
`, "3\n")
}

// TestParityGenericNamedFunctionReference covers the symmetric case:
// passing a generic function as a value (via OpMakeClosure with zero
// upvalues) should still let it pick up the call-site type bindings of
// the surrounding generic frame.
func TestParityGenericNamedFunctionReference(t *testing.T) {
	runParity(t, `import io;

func identity<T>(T x): T { return x; }

func apply<T>(T value, func fn): T {
    return fn(value);
}

io.println(apply(42, identity));
io.println(apply("hi", identity));
`, "42\nhi\n")
}

// TestParityGenericTypeMismatch is a regression guard: when the
// substituted concrete type for T cannot accept the argument, an error
// must surface on both backends. Today the mismatch is detected by
// the runtime parameter-type check; this test pins that behaviour so
// the closure type-binding plumbing doesn't accidentally accept
// anything for T.
func TestParityGenericTypeMismatch(t *testing.T) {
	runErrorParity(t, `import io;

func box<T>(T x): T { return x; }

let asInt = func(int n): int { return n; };
io.println(asInt(box("hello")));
`, "expects int")
}

// TestParityStaticLetMutation verifies that `static let` class members
// hold mutable state and survive across calls to the class's static
// methods.
func TestParityStaticLetMutation(t *testing.T) {
	runParity(t, `import io;
class Counter {
    static let count = 0;
    static func bump(): int {
        Counter.count = Counter.count + 1;
        return Counter.count;
    }
}
io.println(Counter.bump());
io.println(Counter.bump());
io.println(Counter.bump());
`, "1\n2\n3\n")
}

// TestParityAsyncAll verifies async.all returns results in original
// order once every input task completes.
func TestParityAsyncAll(t *testing.T) {
	runParity(t, `import async;
import io;
let a = async.run(func(): int { async.await(async.sleep(20)); return 1; });
let b = async.run(func(): int { async.await(async.sleep(10)); return 2; });
let c = async.run(func(): int { return 3; });
let results = async.await(async.all([a, b, c]));
io.println(results[0]);
io.println(results[1]);
io.println(results[2]);
`, "1\n2\n3\n")
}

// TestParityAsyncRace verifies async.race resolves with the first
// completing task's value and cancels the others.
func TestParityAsyncRace(t *testing.T) {
	runParity(t, `import async;
import io;
let fast = async.run(func(): string { async.await(async.sleep(10)); return "fast"; });
let slow = async.run(func(): string { async.await(async.sleep(500)); return "slow"; });
io.println(async.await(async.race([fast, slow])));
`, "fast\n")
}

// TestParityAsyncTimeout verifies async.timeout rejects when the
// inner task takes longer than the deadline.
func TestParityAsyncTimeout(t *testing.T) {
	runErrorParity(t, `import async;
import io;
let slow = async.run(func(): string {
    async.await(async.sleep(500));
    return "never";
});
io.println(async.await(async.timeout(slow, 30)));
`, "timeout")
}

// TestParityTaskCancel verifies a cancelled task reports cancelled
// and Await surfaces the cancellation error.
func TestParityTaskCancel(t *testing.T) {
	runParity(t, `import async;
import io;
let t = async.run(func(): int {
    async.await(async.sleep(500));
    return 42;
});
async.cancel(t);
io.println(t.cancelled);
`, "true\n")
}

// TestParityAesRoundTrip verifies AES-256-GCM encrypts then decrypts
// to the original plaintext on both backends.
func TestParityAesRoundTrip(t *testing.T) {
	runParity(t, `import crypt;
import io;
import bytes;
let key = bytes.fromString("0123456789abcdef0123456789abcdef");
let enc = crypt.aesEncrypt(key, "hello aes");
let dec = crypt.aesDecrypt(key, enc["nonce"], enc["ciphertext"]);
io.println(dec.toString());
`, "hello aes\n")
}

// TestParityChaCha20RoundTrip verifies XChaCha20-Poly1305 encrypts
// then decrypts to the original plaintext on both backends.
func TestParityChaCha20RoundTrip(t *testing.T) {
	runParity(t, `import crypt;
import io;
import bytes;
let key = bytes.fromString("0123456789abcdef0123456789abcdef");
let enc = crypt.chacha20Encrypt(key, "hello chacha");
let dec = crypt.chacha20Decrypt(key, enc["nonce"], enc["ciphertext"]);
io.println(dec.toString());
`, "hello chacha\n")
}

// TestParityAesWrongKeyRejected verifies AES-GCM authentication rejects
// a ciphertext when the decryption key differs from the encryption key.
func TestParityAesWrongKeyRejected(t *testing.T) {
	runErrorParity(t, `import crypt;
import bytes;
let key1 = bytes.fromString("0123456789abcdef0123456789abcdef");
let key2 = bytes.fromString("ABCDEF0123456789ABCDEF0123456789");
let enc = crypt.aesEncrypt(key1, "hello aes");
crypt.aesDecrypt(key2, enc["nonce"], enc["ciphertext"]);
`, "authentication failed")
}

// TestParityAesBadKeySize verifies the 32-byte key requirement is
// enforced on both backends.
func TestParityAesBadKeySize(t *testing.T) {
	runErrorParity(t, `import crypt;
import bytes;
crypt.aesEncrypt(bytes.fromString("short"), "x");
`, "32-byte AES-256 key")
}

// TestParityRegexNumberedGroups verifies re.match exposes numbered
// capture groups via the "groups" list on both backends.
func TestParityRegexNumberedGroups(t *testing.T) {
	runParity(t, `import re;
import io;
let m = re.match("([a-z]+)([0-9]+)", "abc123");
io.println(m["text"]);
io.println(m["groups"][0]);
io.println(m["groups"][1]);
io.println(m["groups"][2]);
`, "abc123\nabc123\nabc\n123\n")
}

// TestParityRegexNamedGroups verifies re.match exposes named capture
// groups via the "named" dict on both backends.
func TestParityRegexNamedGroups(t *testing.T) {
	runParity(t, `import re;
import io;
let m = re.match("(?P<word>[a-z]+)(?P<num>[0-9]+)", "abc123");
io.println(m["named"]["word"]);
io.println(m["named"]["num"]);
`, "abc\n123\n")
}

// TestParityRegexMatchAll verifies re.matchAll iterates non-overlapping
// matches and returns a dict per match on both backends.
func TestParityRegexMatchAll(t *testing.T) {
	runParity(t, `import re;
import io;
let all = re.matchAll("([a-z]+)=([0-9]+)", "x=1 y=22 z=333");
io.println(all.length);
io.println(all[0]["groups"][1]);
io.println(all[1]["groups"][2]);
io.println(all[2]["text"]);
`, "3\nx\n22\nz=333\n")
}

// TestParityRegexNoMatch verifies re.match returns null and re.matchAll
// returns an empty list when the pattern does not match.
func TestParityRegexNoMatch(t *testing.T) {
	runParity(t, `import re;
import io;
io.println(re.match("xyz", "abc") == null);
io.println(re.matchAll("xyz", "abc").length);
`, "true\n0\n")
}

// TestParityFuncAsFieldType verifies `func` is accepted as a class
// field type (parser disambiguates `func NAME ;` and `func NAME = ...`
// as a typed declaration vs `func NAME (` as a method definition).
func TestParityFuncAsFieldType(t *testing.T) {
	runParity(t, `import io;
class Holder {
    func cb;
    func Holder(func cb) { this.cb = cb; }
    func invoke(): int {
        let fn = this.cb;
        return fn();
    }
}
let h = Holder(func(): int { return 7; });
io.println(h.invoke());
`, "7\n")
}

// TestParityInitBlockRunsInOrder verifies that an init block executes
// at module-load time, in source order with the surrounding top-level
// code, on both backends.
func TestParityInitBlockRunsInOrder(t *testing.T) {
	runParity(t, `import io;
io.println("before");
init {
    io.println("init");
}
io.println("after");
`, "before\ninit\nafter\n")
}

// TestParityInitBlockSeesAndMutatesTopLevelState verifies init can
// read and write top-level declarations declared above it.
func TestParityInitBlockSeesAndMutatesTopLevelState(t *testing.T) {
	runParity(t, `import io;
int count = 0;
init {
    count = count + 5;
}
io.println(count);
`, "5\n")
}

// TestParityModuleTopLevelRuleRejectsFreeStandingStatement verifies
// the module-top-level discipline at import time: both the evaluator
// and the bytecode VM run semantic.Analyze on imported source
// modules, so a violating module fails to load on either backend with
// the same diagnostic.
func TestParityModuleTopLevelRuleRejectsFreeStandingStatement(t *testing.T) {
	// Write the violating module to a fresh dir we add to the
	// resolver's module path. The module is `loud.gb` containing
	// `module loud; import io; io.println("..."); ` - a free-standing
	// expression statement that the rule should reject.
	dir := t.TempDir()
	badModule := filepath.Join(dir, "loud.gb")
	if err := os.WriteFile(badModule, []byte(`module loud;
import io;
io.println("ran at import time");
`), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}

	source := `import loud;
`

	// Evaluator path - manually drive it so we can inject the
	// modulePath, mirroring what evaluator.NewWithArgsAndModulePaths does.
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	_, evErr := ev.Eval(program)
	if evErr == nil {
		t.Fatalf("evaluator: expected error importing violating module, got nil")
	}
	if !strings.Contains(evErr.Error(), "free-standing top-level") {
		t.Fatalf("evaluator error should describe the rule: %q", evErr.Error())
	}

	// VM path - use the stdlib loader, but seed it with our temp dir
	// as the search path.
	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var vmOut bytes.Buffer
	stateful := evaluator.NewWithArgsAndModulePaths(&vmOut, nil, []string{dir})
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	vmErr := vm.Run()
	if vmErr == nil {
		t.Fatalf("vm: expected error importing violating module, got nil")
	}
	if !strings.Contains(vmErr.Error(), "free-standing top-level") {
		t.Fatalf("vm error should describe the rule: %q", vmErr.Error())
	}
}

// TestParityModuleTopLevelRuleAcceptsInitBlock is the positive
// counterpart: the same imperative work moved into an init block is
// accepted by both backends.
func TestParityModuleTopLevelRuleAcceptsInitBlock(t *testing.T) {
	dir := t.TempDir()
	goodModule := filepath.Join(dir, "quiet.gb")
	if err := os.WriteFile(goodModule, []byte(`module quiet;
import io;
export int loaded = 0;
init {
    loaded = 1;
    io.println("loaded once");
}
`), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}

	source := `import io;
import quiet;
io.println(quiet.loaded);
`

	// Evaluator path.
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("evaluator: unexpected error: %v", err)
	}
	if evOut.String() != "loaded once\n1\n" {
		t.Fatalf("evaluator output: got %q", evOut.String())
	}

	// VM path.
	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var vmOut bytes.Buffer
	stateful := evaluator.NewWithArgsAndModulePaths(&vmOut, nil, []string{dir})
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: unexpected error: %v", err)
	}
	if vmOut.String() != "loaded once\n1\n" {
		t.Fatalf("vm output: got %q", vmOut.String())
	}
}

// TestParityCrossModuleClassExtension exercises `class B extends mod.A`
// patterns: B in the main script extends a class A declared in another
// .gb module. The fixture covers both `parent(args)` (constructor) and
// `parent.method(args)` dispatch into A, plus a field-write inside A's
// constructor that mutates the (subclass) instance.
func TestParityCrossModuleClassExtension(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.gb"), []byte(`module base;
export class Greeter {
    string name;
    func Greeter(string name) {
        this.name = name;
    }
    func greet(): string {
        return "hello from " + this.name;
    }
}
`), 0o644); err != nil {
		t.Fatalf("write base module: %v", err)
	}

	source := `import io;
import base;

class Loud extends base.Greeter {
    func Loud(string name) {
        parent(name);
    }
    func shout(): string {
        return parent.greet().upper();
    }
}

let l = Loud("ada");
io.println(l.greet());
io.println(l.shout());
io.println(l.name);
`

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	want := "hello from ada\nHELLO FROM ADA\nada\n"

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
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
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

// TestParityClassDecoratorPatterns covers the three class-decorator
// shapes: register-in-place (return cls), swap-to-another-class
// (return DifferentClass), and wrap-as-callable (return a closure
// that becomes the new constructor). Prior to the fix the VM
// silently ignored swaps and both engines rejected callable returns.
func TestParityClassDecoratorPatterns(t *testing.T) {
	runParity(t, `import io;
import reflect;

func register(any cls): any {
    io.println("[register] " + reflect.className(cls));
    return cls;
}

@register
class Service {
    string name;
    func Service(string n) { this.name = n; }
}

io.println(Service("alpha").name);

class Replacement {
    string label;
    func Replacement(string n) { this.label = "replaced:" + n; }
    func describe(): string { return this.label; }
}

func swap(any cls): any { return Replacement; }

@swap
class Original {
    func Original(string n) {}
    func describe(): string { return "original"; }
}

io.println(Original("ada").describe());

func wrap(any cls): any {
    return func(string n): any {
        io.println("[wrap] " + n);
        return cls(n + "!");
    };
}

@wrap
class Greeter {
    string greeting;
    func Greeter(string n) { this.greeting = "Hello, " + n; }
    func say(): string { return this.greeting; }
}

io.println(Greeter("Ada").say());
`, `[register] Service
alpha
replaced:ada
[wrap] Ada
Hello, Ada!
`)
}

func TestParityUnknownMethodThrowsCatchableRuntimeError(t *testing.T) {
	runParity(t, `import io;

class Plain {
    func Plain() {}
    func known(): string { return "ok"; }
}

let p = Plain();
io.println(p.known());

try {
    p.notThere("argument");
    io.println("FAIL: no throw");
} catch (RuntimeError e) {
    io.println("caught: " + e.message);
}

io.println("after try");
`, "ok\ncaught: unknown method Plain.notThere\nafter try\n")
}

func TestParityUnknownMethodOnForeignInstanceIsCatchable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "remote.gb"), []byte(`module remote;
export class Remote {
    func Remote() {}
    func known(): string { return "remote-ok"; }
}
`), 0o644); err != nil {
		t.Fatalf("write remote: %v", err)
	}

	source := `import io;
import remote;

let r = remote.Remote();
io.println(r.known());

try {
    r.missing("x");
    io.println("FAIL: no throw");
} catch (RuntimeError e) {
    io.println("caught: " + e.message);
}

io.println("after try");
`

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse: %v", p.Errors())
	}
	want := "remote-ok\ncaught: unknown method Remote.missing\nafter try\n"

	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("evaluator: %v", err)
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
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	loader.mainChunk = chunk
	loader.hasMainChunk = true
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	stateful.SetMethodDispatcher(vm)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: %v", err)
	}
	if vmOut.String() != want {
		t.Fatalf("vm output: got %q, want %q", vmOut.String(), want)
	}
}

func TestParityParameterDecoratorsAreSurfacedByReflect(t *testing.T) {
	runParity(t, `import io;
import reflect;

@Get("/x")
func handler(
    @PathParam("id") string id,
    @QueryParam("limit") int limit = 10,
    @Header("X-Api-Key") @Inject string apiKey
): void { }

for (p in reflect.parameters(handler)) {
    let pd = p as dict<string, any>;
    string name = pd["name"] as string;
    if (!pd.contains("decorators")) {
        io.println(name + " no-decorators");
        continue;
    }
    let decs = pd["decorators"] as list<any>;
    let names = [];
    for (d in decs) {
        let dd = d as dict<string, any>;
        names = names.push(dd["name"] as string);
    }
    io.println(name + " " + names.join(","));
}
`, "id PathParam\nlimit QueryParam\napiKey Header,Inject\n")
}

func TestParityReflectClassesEnumeratesEveryUserClass(t *testing.T) {
	runParity(t, `import io;
import reflect;

@Service
class A { func A() {} }

@Controller
class B { func B() {} }

class C { func C() {} }

let names = [];
for (cls in reflect.classes()) {
    let n = reflect.className(cls);
    if (n != null) {
        let s = n as string;
        if (s == "A" || s == "B" || s == "C") {
            names = names.push(s);
        }
    }
}
io.println(names.join(","));
`, "A,B,C\n")
}

func TestParityTwoHopCrossModuleMethodDispatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.gb"), []byte(`module base;
export class Root {
    func Root() {}
    func describe(): string { return "from-root"; }
}
`), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mid.gb"), []byte(`module mid;
import base;
export class Middle extends base.Root {
    func Middle() { parent(); }
}
`), 0o644); err != nil {
		t.Fatalf("write mid: %v", err)
	}

	source := `import io;
import mid;
class Leaf extends mid.Middle {
    func Leaf() { parent(); }
}
io.println(Leaf().describe());
`

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse: %v", p.Errors())
	}
	want := "from-root\n"

	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("evaluator: %v", err)
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
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	loader.mainChunk = chunk
	loader.hasMainChunk = true
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	stateful.SetMethodDispatcher(vm)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: %v", err)
	}
	if vmOut.String() != want {
		t.Fatalf("vm output: got %q, want %q", vmOut.String(), want)
	}
}

func TestParityCrossModuleReflectFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probe.gb"), []byte(`module probe;
import io;
import reflect;
export func dump(any cls): void {
    for (f in reflect.fields(cls)) {
        let fd = f as dict<string, any>;
        io.println((fd["name"] as string) + ":" + (fd["type"] as string));
    }
}
`), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	source := `import probe;
class GreetingDTO {
    string name;
    ?string greeting;
}
probe.dump(GreetingDTO);
`

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	want := "greeting:?string\nname:string\n"

	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("evaluator: %v", err)
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
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	loader.registerMainChunk(chunk)
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: %v", err)
	}
	if vmOut.String() != want {
		t.Fatalf("vm output: got %q, want %q", vmOut.String(), want)
	}
}

func TestParityCrossModuleInterfaceDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "iface.gb"), []byte(`module iface;
export interface Greetable {
    string name;
    int age;
    func greet(): string {
        return "hello, " + this.name + " (" + (this.age as string) + ")";
    }
}
`), 0o644); err != nil {
		t.Fatalf("write iface: %v", err)
	}

	source := `import io;
import iface;

class User implements iface.Greetable {
    func User(string n, int a) { this.name = n; this.age = a; }
}

class Loud implements iface.Greetable {
    func Loud(string n, int a) { this.name = n; this.age = a; }
    func greet(): string { return "HELLO, " + this.name; }
}

io.println(User("ada", 36).greet());
io.println(Loud("bo", 4).greet());
`

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	want := "hello, ada (36)\nHELLO, bo\n"

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
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
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

func TestParityInterfaceDefaults(t *testing.T) {
	runParity(t, `import io;

interface Greetable {
    string name;
    func greet(): string { return "hello, " + this.name; }
    func upper(): string;
}

class User implements Greetable {
    func User(string n) { this.name = n; }
    func upper(): string { return this.name.upper(); }
}

class Loud implements Greetable {
    func Loud(string n) { this.name = n; }
    func greet(): string { return "HELLO, " + this.name; }
    func upper(): string { return this.name.upper(); }
}

let u = User("ada");
io.println(u.greet());
io.println(u.upper());

let l = Loud("ada");
io.println(l.greet());
`, "hello, ada\nADA\nHELLO, ada\n")
}

func TestParityInterfaceDiamondConflict(t *testing.T) {
	source := `import io;
interface A { func foo(): string { return "A"; } }
interface B { func foo(): string { return "B"; } }
class C implements A, B {}
io.println(C().foo());
`
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	ev := evaluator.New(&evOut)
	if _, err := ev.Eval(program); err == nil {
		t.Fatalf("evaluator: expected ambiguous-default error, got nil")
	} else if !strings.Contains(err.Error(), "multiple defaults") {
		t.Fatalf("evaluator: error should mention multiple defaults: %v", err)
	}

	_, err := bytecode.Compile(program, []byte(source), "parity")
	if err == nil {
		t.Fatalf("bytecode: expected ambiguous-default error, got nil")
	}
	if !strings.Contains(err.Error(), "multiple defaults") {
		t.Fatalf("bytecode: error should mention multiple defaults: %v", err)
	}
}

func TestParityInterfaceDiamondOverride(t *testing.T) {
	runParity(t, `import io;
interface A { func foo(): string { return "A"; } }
interface B { func foo(): string { return "B"; } }
class C implements A, B {
    func foo(): string { return "C"; }
}
io.println(C().foo());
`, "C\n")
}

// TestParityFieldCallableDecorators covers callable field decorators:
// every assignment (including constructor) flows through the decorator
// chain bottom-up; transforms can reshape the value, or throw to reject.
func TestParityFieldCallableDecorators(t *testing.T) {
	runParity(t, `import io;

func upper(string v): string { return v.upper(); }
func prefix(string p, string v): string { return p + v; }
func minLen(int min, string v): string {
    if (v.length() < min) { throw RuntimeError("too short"); }
    return v;
}

class User {
    @prefix("hello-")
    @minLen(2)
    @upper
    string name;
    func User(string n) { this.name = n; }
}

let u = User("ada");
io.println(u.name);

u.name = "bo";
io.println(u.name);

try { u.name = "x"; io.println("FAIL"); }
catch (RuntimeError e) { io.println(e.message); }
`, "hello-ADA\nhello-BO\ntoo short\n")
}

// TestParityClassDecoratorTypedDelegation covers the typed-delegation
// pattern: when a wrap closure on @storage returns an instance of
// a different class, the returned instance still satisfies
// `instanceof OriginalClass` via an auto-extended type list.
func TestParityClassDecoratorTypedDelegation(t *testing.T) {
	runParity(t, `import io;

class JsonRepository {
    string a;
    func JsonRepository(string a) { this.a = a; }
    func describe(): string { return "json:" + this.a; }
}

func storage(any cls): any {
    return func(string a): any { return JsonRepository(a); };
}

@storage
class UserRepository {
    func UserRepository(string a) {}
}

let ur = UserRepository("ada");
io.println(ur instanceof UserRepository);
io.println(ur instanceof JsonRepository);
io.println(ur.describe());
`, "true\ntrue\njson:ada\n")
}

// TestParityAbstractDecorator covers the @abstract class/method
// decorator. A class is abstract when either:
//   - it carries @abstract on the class itself, or
//   - it (or an ancestor) has a method decorated @abstract and no
//     more-derived class provides a concrete override.
//
// Direct instantiation of an abstract class throws RuntimeError;
// concrete subclasses instantiate normally.
func TestParityAbstractDecorator(t *testing.T) {
	runParity(t, `import io;

@abstract
class AbstractBase {
    func name(): string { return "base"; }
}
class Derived extends AbstractBase {}

class Shape {
    @abstract
    func area(): int { return 0; }
}
class Circle extends Shape {
    int r;
    func Circle(int r) { this.r = r; }
    func area(): int { return 3 * this.r * this.r; }
}
class Square extends Shape {}

try { AbstractBase(); io.println("FAIL: abstract class instantiated"); }
catch (RuntimeError e) { io.println(e.message); }

io.println(Derived().name());

try { Shape(); io.println("FAIL: shape instantiated"); }
catch (RuntimeError e) { io.println(e.message); }

io.println(Circle(2).area());

try { Square(); io.println("FAIL: square instantiated"); }
catch (RuntimeError e) { io.println(e.message); }
`, `cannot instantiate abstract class AbstractBase
base
cannot instantiate Shape: abstract method Shape.area is not implemented
12
cannot instantiate Square: abstract method Shape.area is not implemented
`)
}

// TestParityCrossModuleInheritedThrow covers the fix where a method
// inherited from a parent class in another module threw an error
// that was silently swallowed by the bytecode cross-module dispatch
// fallback - the loader's error was treated as "method not found"
// rather than as a real throw, so try/catch never saw it.
func TestParityCrossModuleInheritedThrow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probemod.gb"), []byte(`module probemod;
export class Base {
    func Base() {}
    func ok(): int { return 42; }
    func boom(): void { throw RuntimeError("bang"); }
}
`), 0o644); err != nil {
		t.Fatalf("write probemod: %v", err)
	}

	source := `import io;
import probemod;

class Sub extends probemod.Base {
    func go(): void {
        io.println(this.ok());
        try {
            this.boom();
            io.println("after-try");
        } catch (Error e) {
            io.println("caught: " + e.message);
        }
        io.println("end");
    }
}
Sub().go();
`

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	want := "42\ncaught: bang\nend\n"

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
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
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

// TestParityAliasedNativeImport verifies the bytecode compiler now
// recognises aliased native imports - calls like `natpath.clean(...)`
// dispatch to the canonical `path.clean(...)` on both backends without
// the runtime fallback that previously papered over this gap.
// `path` is a stateful native module so this needs the stateful
// parity harness; that detail is incidental to the alias support.
func TestParityAliasedNativeImport(t *testing.T) {
	runParityStateful(t, `import io;
import path as natpath;
io.println(natpath.clean("/a/../b"));
`, "/b\n")
}

// TestParityAliasedNativeImportDeferred exercises the defer code path
// for an aliased native call (a separate compileDeferStatement branch
// from compileCallExpression).
func TestParityAliasedNativeImportDeferred(t *testing.T) {
	runParityStateful(t, `import io;
import path as natpath;
func work(): void {
    defer io.println(natpath.clean("/a/../b"));
    io.println("before defer fires");
}
work();
`, "before defer fires\n/b\n")
}

// TestParityAliasedNativeImportShadowedByLocal verifies that an
// aliased-import name shadowed by a local variable still dispatches to
// the local (selector becomes a method call on the value), preserving
// the established precedence.
func TestParityAliasedNativeImportShadowedByLocal(t *testing.T) {
	runParity(t, `import io;
import path as natpath;
func demo(): void {
    let natpath = "hello";
    io.println(natpath);
}
demo();
`, "hello\n")
}

// TestParityHTTPDefaultUserAgent verifies outgoing requests carry the
// Geblang default User-Agent header (and not Go's default).
func TestParityHTTPDefaultUserAgent(t *testing.T) {
	runParityStateful(t, `
import http;
import io;
func handle(dict<string, any> req): dict<string, any> {
    let ua = req["headers"].get("User-Agent");
    return {"status": 200, "body": ua as string};
}
let server = http.listen("127.0.0.1:0", handle);
let base = "http://" + http.serverAddr(server);
io.println(http.get(base + "/")["body"]);
http.shutdown(server, 100);
http.close(server);
`, "Geblang/1.0\n")
}

// TestParityNullCoalesceInAsync verifies the ?? operator works inside
// an async-run callback. Regression test for a bug where
// runWrapper / shiftInstructionOperands omitted OpNullCoalesce from
// the jump-target shift list, so the callback's OpNullCoalesce jumped
// back into the wrapper prologue and looped forever.
func TestParityNullCoalesceInAsync(t *testing.T) {
	runParity(t, `import io;
import async;
let t = async.run(func(): int {
    let v = 5 ?? 10;
    return v;
});
io.println(async.await(t));
`, "5\n")
}

// TestParityOptionalChainInAsync covers the matching OpOptionalChain
// shift fix (?. on a class instance inside an async-run callback).
func TestParityOptionalChainInAsync(t *testing.T) {
	runParity(t, `import io;
import async;
class Box { string name; func Box(string n) { this.name = n; } }
let t = async.run(func(): any {
    Box b = Box("hi");
    return b?.name;
});
io.println(async.await(t));
`, "hi\n")
}

// TestParityGenericInheritedBindings verifies that a subclass declared
// as `extends Base<T1, T2, ...>` propagates the concrete type
// arguments to instance TypeBindings on both backends.
func TestParityGenericInheritedBindings(t *testing.T) {
	runParity(t, `import io;
import reflect;
class Base<T> {
    T value;
    func Base(T v) { this.value = v; }
}
class StringBase extends Base<string> {
    func StringBase(string s) { parent(s); }
}
class GrandChild extends StringBase {
    func GrandChild(string s) { parent(s); }
}
let b = StringBase("hi");
io.println(reflect.typeBindings(b)["T"]);
let g = GrandChild("hi");
io.println(reflect.typeBindings(g)["T"]);
`, "string\nstring\n")
}

// TestParityGenericInheritedMultipleParams verifies a parent with two
// type parameters propagates both via the extends clause.
func TestParityGenericInheritedMultipleParams(t *testing.T) {
	runParity(t, `import io;
import reflect;
class Pair<A, B> {
    A first;
    B second;
    func Pair(A a, B b) { this.first = a; this.second = b; }
}
class IntStringPair extends Pair<int, string> {
    func IntStringPair(int a, string b) { parent(a, b); }
}
let p = IntStringPair(1, "hi");
let b = reflect.typeBindings(p);
io.println(b["A"]);
io.println(b["B"]);
`, "int\nstring\n")
}

// TestParityTimerFires verifies time.scheduler.Timer runs its callback
// once after the requested delay and reports didFire=true on both
// backends.
func TestParityTimerFires(t *testing.T) {
	runParityWithStdlib(t, `import time.scheduler as sched;
import async;
import io;
let fired = false;
let t = sched.Timer(20, func(): void { fired = true; });
async.await(t.wait());
io.println(fired);
io.println(t.didFire());
`, "true\ntrue\n")
}

// TestParityTimerCancelled verifies a Timer cancelled before its delay
// expires reports didFire=false and never invokes the callback.
func TestParityTimerCancelled(t *testing.T) {
	runParityWithStdlib(t, `import time.scheduler as sched;
import async;
import io;
let fired = false;
let t = sched.Timer(200, func(): void { fired = true; });
t.cancel();
async.await(async.sleep(60));
io.println(fired);
io.println(t.didFire());
`, "false\nfalse\n")
}

// TestParityTickerStops verifies time.scheduler.Ticker stops after
// stop() and ticks() reports the accumulated count.
func TestParityTickerStops(t *testing.T) {
	runParityWithStdlib(t, `import time.scheduler as sched;
import async;
import io;
let n = 0;
let ticker = sched.Ticker(20, func(): void { n = n + 1; });
async.await(async.sleep(75));
ticker.stop();
io.println(ticker.ticks() >= 2);
io.println(n >= 2);
`, "true\ntrue\n")
}

// TestParityBase32RoundTrip verifies base32 encode/decode round-trips
// a UTF-8 string identically on both backends.
func TestParityBase32RoundTrip(t *testing.T) {
	runParity(t, `import encoding;
import io;
let enc = encoding.base32Encode("hello world");
io.println(enc);
io.println(encoding.base32Decode(enc).toString());
`, "NBSWY3DPEB3W64TMMQ======\nhello world\n")
}

// TestParityBase58RoundTrip verifies base58 encode/decode round-trips
// a UTF-8 string identically on both backends.
func TestParityBase58RoundTrip(t *testing.T) {
	runParity(t, `import encoding;
import io;
let enc = encoding.base58Encode("hello world");
io.println(enc);
io.println(encoding.base58Decode(enc).toString());
`, "StV1DL6CwTryKyV\nhello world\n")
}

// TestParityBase58LeadingZeros verifies base58 preserves leading zero
// bytes (each becomes a leading "1" in the output, per the Bitcoin spec).
func TestParityBase58LeadingZeros(t *testing.T) {
	runParity(t, `import encoding;
import io;
import bytes;
let raw = bytes.fromHex("000000aa");
let enc = encoding.base58Encode(raw);
io.println(enc);
let dec = encoding.base58Decode(enc);
io.println(dec.toHex());
`, "1113w\n000000aa\n")
}

// TestParityLengthProperty verifies the .length property (no parens)
// returns the same result as the .length() method on every supported
// collection type, on both backends.
func TestParityLengthProperty(t *testing.T) {
	runParity(t, `import io;
import bytes;
io.println([1,2,3].length);
io.println("hello".length);
io.println({"a": 1, "b": 2}.length);
io.println(bytes.fromString("hi").length);
io.println((1..10).length);
`, "3\n5\n2\n2\n10\n")
}

// TestParityStaticTypedField verifies the `static <type> name = value`
// declaration syntax produces a mutable static class member.
func TestParityStaticTypedField(t *testing.T) {
	runParity(t, `import io;
class Stats {
    static int hits = 0;
    static string label = "items";
    static func record(): void {
        Stats.hits = Stats.hits + 1;
    }
}
Stats.record();
Stats.record();
io.println(Stats.hits);
io.println(Stats.label);
`, "2\nitems\n")
}

// TestParityWithEnterExit verifies that __enter__/__exit__ magic
// methods on an instance run at with-block entry/exit and that
// __enter__'s return value supplies the binding.
func TestParityWithEnterExit(t *testing.T) {
	runParity(t, `import io;
class Guarded {
    string label;
    func Guarded(string label) { this.label = label; }
    func __enter__(): string { io.println("enter " + this.label); return "bound-" + this.label; }
    func __exit__(): void { io.println("exit " + this.label); }
}
with (name = Guarded("ada")) {
    io.println("body sees " + name);
}
`, "enter ada\nbody sees bound-ada\nexit ada\n")
}

// TestParityWithoutBinding verifies the bare `with (expr) { ... }`
// form (no `name =`) calls __exit__ on the resource when defined.
func TestParityWithoutBinding(t *testing.T) {
	runParity(t, `import io;
class R {
    func R() { io.println("acq"); }
    func __exit__(): void { io.println("exit"); }
}
with (R()) {
    io.println("body");
}
`, "acq\nbody\nexit\n")
}

// TestParityDestructorAtProgramExit verifies destructors fire at
// the end-of-program sweep (after the user code finishes
// executing) and in reverse-creation order on both backends.
func TestParityDestructorAtProgramExit(t *testing.T) {
	runParity(t, `import io;
class R {
    string name;
    func R(string n) { this.name = n; io.println("acq " + n); }
    func ~R() { io.println("rel " + this.name); }
}
let a = R("first");
let b = R("second");
io.println("end of body");
`, "acq first\nacq second\nend of body\nrel second\nrel first\n")
}

// TestParityDestructorRunsOnce verifies the destructor does not
// fire twice when the same instance is reached via both a `del`
// and the program-exit sweep.
func TestParityDestructorRunsOnce(t *testing.T) {
	runParity(t, `import io;
class R {
    func R() {}
    func ~R() { io.println("once"); }
}
let r = R();
del r;
io.println("after del");
`, "once\nafter del\n")
}

// TestParityDelFiresDestructor verifies `del x` calls the
// destructor inline (not at program-exit time).
func TestParityDelFiresDestructor(t *testing.T) {
	runParity(t, `import io;
class R {
    string name;
    func R(string n) { this.name = n; }
    func ~R() { io.println("rel " + this.name); }
}
let r = R("a");
io.println("before del");
del r;
io.println("after del");
`, "before del\nrel a\nafter del\n")
}

// TestParityDelClearsBinding verifies that re-binding the same
// name after `del` produces a fresh value and the static analyzer
// accepts subsequent references.
func TestParityDelClearsBinding(t *testing.T) {
	runParity(t, `import io;
class R {
    string label;
    func R(string label) { this.label = label; }
    func ~R() { io.println("rel " + this.label); }
}
let r = R("first");
del r;
let r = R("second");
io.println("middle " + r.label);
`, "rel first\nmiddle second\nrel second\n")
}

// TestParityWithDoesNotInvokeDestructor verifies that a class
// destructor does NOT fire at with-block exit (only __exit__
// does). The destructor fires later via the program-exit sweep,
// after "after with" has already printed.
func TestParityWithDoesNotInvokeDestructor(t *testing.T) {
	runParity(t, `import io;
class R {
    func R() { io.println("acq"); }
    func __exit__(): void { io.println("exit"); }
    func ~R() { io.println("dtor"); }
}
with (R()) {
    io.println("body");
}
io.println("after with");
`, "acq\nbody\nexit\nafter with\ndtor\n")
}

// TestParityJSONStringifyInstance verifies that an instance with
// no __serialize__ override serialises its public (non-underscore)
// fields as a JSON object on both backends.
func TestParityJSONStringifyInstance(t *testing.T) {
	runParity(t, `import io;
import json;
class Point {
    int x;
    int y;
    int _hidden;
    func Point(int x, int y) { this.x = x; this.y = y; this._hidden = 99; }
}
io.println(json.stringify(Point(3, 4)));
`, "{\"x\":3,\"y\":4}\n")
}

// TestParityJSONSerializeOverride verifies a class can customise
// its serialisation by defining __serialize__().
func TestParityJSONSerializeOverride(t *testing.T) {
	runParity(t, `import io;
import json;
class Tagged {
    string label;
    func Tagged(string label) { this.label = label; }
    func __serialize__(): dict {
        return {"kind": "tagged", "label": this.label};
    }
}
io.println(json.stringify(Tagged("hi")));
`, "{\"kind\":\"tagged\",\"label\":\"hi\"}\n")
}

// TestParityJSONParseAsConstructor verifies json.parseAs uses the
// constructor's parameter names to map dict keys when the class
// has no __deserialize__ static method.
func TestParityJSONParseAsConstructor(t *testing.T) {
	runParity(t, `import io;
import json;
class Point {
    int x;
    int y;
    func Point(int x, int y) { this.x = x; this.y = y; }
}
let q = json.parseAs("{\"x\": 3, \"y\": 4}", Point);
io.println(q.x);
io.println(q.y);
`, "3\n4\n")
}

// TestParityJSONParseAsDeserialize verifies the static
// __deserialize__ hook is preferred when defined.
func TestParityJSONParseAsDeserialize(t *testing.T) {
	runParity(t, `import io;
import json;
class Tagged {
    string kind;
    string label;
    func Tagged(string kind, string label) { this.kind = kind; this.label = label; }
    static func __deserialize__(dict d): Tagged {
        return Tagged(d["kind"] + "-decoded", d["label"]);
    }
}
let t = json.parseAs("{\"kind\":\"x\",\"label\":\"hi\"}", Tagged);
io.println(t.kind);
io.println(t.label);
`, "x-decoded\nhi\n")
}

// TestParityJSONRoundTrip verifies stringify followed by parseAs
// reconstructs structurally-equal instances on both backends.
func TestParityJSONRoundTrip(t *testing.T) {
	runParity(t, `import io;
import json;
class Point {
    int x;
    int y;
    func Point(int x, int y) { this.x = x; this.y = y; }
}
let p = Point(10, 20);
let q = json.parseAs(json.stringify(p), Point);
io.println(q.x);
io.println(q.y);
io.println(p.x == q.x);
io.println(p.y == q.y);
`, "10\n20\ntrue\ntrue\n")
}

// TestParityExplicitGenericCallSyntax verifies that the call form
// `Identifier<Type>(args)` is recognised by the parser, threaded through the
// compiler, and produces equivalent output on both the evaluator and the VM.
// Before this was supported the parser misread the `<` as the less-than
// operator and the call evaluated as a chained comparison.
func TestParityExplicitGenericCallSyntax(t *testing.T) {
	runParity(t, `import io;
import reflect;

class Box<T> {
    func Box() {}
}

let b = Box<string>();
io.println(typeof(b) == Box);
io.println(reflect.typeBindings(b)["T"]);
`, "true\nstring\n")
}

// TestParityExplicitGenericCallOnFunction verifies the same call form on a
// generic free-standing function. Previously this parsed as a chained
// less-than comparison and crashed at runtime. The explicit binding here
// agrees with the inferred binding; the explicit-binding-overrides-inference
// case is covered by the class-construction parity test above.
func TestParityExplicitGenericCallOnFunction(t *testing.T) {
	runParity(t, `import io;

func identity<T>(T value): T {
    return value;
}

io.println(identity<string>("hello"));
io.println(identity<int>(42));
`, "hello\n42\n")
}

// TestParityGenericInvarianceRejectsMismatch verifies that both backends reject
// passing a generic instance whose reified bindings disagree with the
// parameter's declared bindings. The unsoundness this guards against is the
// classic one: widening Container<Sub> to Container<Base> and then having the
// callee insert a sibling Base subtype.
func TestParityGenericInvarianceRejectsMismatch(t *testing.T) {
	source := `class Base {}
class Sub extends Base {}
class Container<T> { func Container() {} }
func consume(Container<Base> c): void {}

let sub = Container<Sub>();
consume(sub);
`
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	// Evaluator must reject the call at runtime.
	var evOut bytes.Buffer
	_, evErr := evaluator.New(&evOut).Eval(program)
	if evErr == nil {
		t.Fatalf("evaluator: expected runtime error, got nil")
	}
	if !strings.Contains(evErr.Error(), "Container<Base>") {
		t.Fatalf("evaluator error did not mention parameter type: %v", evErr)
	}

	// VM must also reject.
	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	vmErr := bytecode.NewVM(chunk, &vmOut).Run()
	if vmErr == nil {
		t.Fatalf("vm: expected runtime error, got nil")
	}
	if !strings.Contains(vmErr.Error(), "Container<Base>") {
		t.Fatalf("vm error did not mention parameter type: %v", vmErr)
	}
}

// TestParityGenericInvarianceAcceptsExactMatch verifies the runtime check
// does NOT over-reject: when the caller's reified bindings exactly match the
// parameter's declared bindings, the call goes through normally.
func TestParityGenericInvarianceAcceptsExactMatch(t *testing.T) {
	runParity(t, `import io;
class Base {}
class Container<T> { func Container() {} }
func consume(Container<Base> c): void {
    io.println("ok");
}

let bases = Container<Base>();
consume(bases);
`, "ok\n")
}

// TestParityExplicitTypeArgOverridesInference verifies that an explicit
// `<TypeArgs>` clause on a generic function call binds T to the explicit
// type and the function body sees that binding, even when the arg's
// actual runtime type differs. This is the reified-type-test pattern -
// the function takes `any` so the parameter accepts any value, and `T`
// is used purely for the runtime check inside the body.
func TestParityExplicitTypeArgOverridesInference(t *testing.T) {
	runParity(t, `import io;

func assertIs<T>(any value): bool {
    return value instanceof T;
}

io.println(assertIs<string>("hello"));
io.println(assertIs<int>("hello"));
io.println(assertIs<int>(42));
io.println(assertIs<string>(42));
`, "true\nfalse\ntrue\nfalse\n")
}

// TestParityVarianceErrorMessageIncludesReifiedBindings verifies that when
// a generic-class invariance violation is detected at runtime, both the
// evaluator and the VM include the offending value's reified type
// arguments in the "got X" segment of the error message, not just the
// bare class name. Before this was fixed the message read "got Container",
// which was unhelpful for diagnosing variance bugs - "got Container<Sub>"
// makes the mismatch immediately obvious.
func TestParityVarianceErrorMessageIncludesReifiedBindings(t *testing.T) {
	source := `class Base {}
class Sub extends Base {}
class Container<T> { func Container() {} }
func use(Container<Base> c): void {}

let sub = Container<Sub>();
use(sub);
`
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	_, evErr := evaluator.New(&evOut).Eval(program)
	if evErr == nil {
		t.Fatalf("evaluator: expected runtime error, got nil")
	}
	if !strings.Contains(evErr.Error(), "got Container<Sub>") {
		t.Fatalf("evaluator error did not mention reified Container<Sub>: %v", evErr)
	}

	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	vmErr := bytecode.NewVM(chunk, &vmOut).Run()
	if vmErr == nil {
		t.Fatalf("vm: expected runtime error, got nil")
	}
	if !strings.Contains(vmErr.Error(), "got Container<Sub>") {
		t.Fatalf("vm error did not mention reified Container<Sub>: %v", vmErr)
	}
}

// TestParityRangeBuiltin verifies the top-level `range(start, end[, step])`
// shorthand produces identical inclusive lists on both backends.
func TestParityRangeBuiltin(t *testing.T) {
	runParity(t, `import io;
io.println(range(1, 5));
io.println(range(10, 2, -2));
io.println(range(5, 1));
`, "[1, 2, 3, 4, 5]\n[10, 8, 6, 4, 2]\n[5, 4, 3, 2, 1]\n")
}

// TestParityCharRange verifies `'a'..'z'` produces a list<string> on both
// backends and respects the exclusive variant.
func TestParityCharRange(t *testing.T) {
	runParity(t, `import io;
io.println('a'..'e');
io.println('a'..<'e');
`, "[\"a\", \"b\", \"c\", \"d\", \"e\"]\n[\"a\", \"b\", \"c\", \"d\"]\n")
}

// TestParityListToListNoop verifies list.toList() is a no-op pass-through,
// preserving order.
func TestParityListToListNoop(t *testing.T) {
	runParity(t, `import io;
io.println([1, 2, 3].toList());
`, "[1, 2, 3]\n")
}

// TestParityCollectionTypeBindings verifies reflect.typeBindings on tagged
// list/dict/set values reads the declared element types on both backends.
func TestParityCollectionTypeBindings(t *testing.T) {
	runParity(t, `import io;
import json;
import reflect;
let list<int> xs = [1, 2, 3];
io.println(json.stringify(reflect.typeBindings(xs)));
let dict<string, int> d = {"a": 1};
let b = reflect.typeBindings(d);
io.println(b["K"]);
io.println(b["V"]);
let untagged = [4, 5];
io.println(json.stringify(reflect.typeBindings(untagged)));
`, "{\"T\":\"int\"}\nstring\nint\n{}\n")
}

// TestParityInstanceofGenericCollection verifies `instanceof list<int>`
// parses and dispatches on both backends, honouring the tag when set and
// walking elements when the value is untagged.
func TestParityInstanceofGenericCollection(t *testing.T) {
	runParity(t, `import io;
let list<int> xs = [1, 2, 3];
io.println(xs instanceof list<int>);
io.println(xs instanceof list<string>);
io.println([1, 2, 3] instanceof list<int>);
io.println([1, 2, "three"] instanceof list<int>);
let dict<string, int> d = {"a": 1};
io.println(d instanceof dict<string, int>);
io.println(d instanceof dict<string, string>);
`, "true\nfalse\ntrue\nfalse\ntrue\nfalse\n")
}

// TestParityClosureCaptureCamelCase guards a 1.0.2 regression: the
// compiler's freeVarSet was lowercasing identifier names while local
// scope entries kept their original case, causing closures that
// captured a variable with uppercase letters in its name to silently
// miss the capture. The closure body then emitted OpGetLocal at the
// wrong slot and at runtime read whichever value happened to be
// there, producing wildly wrong type errors. Both backends must now
// resolve case-sensitively.
func TestParityClosureCaptureCamelCase(t *testing.T) {
	runParity(t, `import io;

func makeAdapter(list<string> pathParamNames): callable {
    return func(int x): void {
        io.println(typeof(pathParamNames));
    };
}

makeAdapter(["a", "b"])(42);
`, "list\n")
}

// TestParityReflectAcceptsInstancesAndStrings verifies the harmonised
// reflect.class / reflect.methods / reflect.fields API on both
// backends: instances, class values, and bare name strings all
// produce the same metadata. 1.0.2 closed several eval/VM
// divergences here.
func TestParityReflectAcceptsInstancesAndStrings(t *testing.T) {
	runParity(t, `import io;
import reflect;

class Foo {
    string name;
    func Foo(string n) { this.name = n; }
    func hello(): string { return this.name; }
}

let f = Foo("ada");
io.println(reflect.methods(f)[0]);
io.println(reflect.methods(Foo)[0]);
io.println(reflect.methods(reflect.class("Foo"))[0]);
io.println(reflect.fields(f)[0]["name"]);
io.println(reflect.fields(f)[0]["type"]);
`, "hello\nhello\nhello\nname\nstring\n")
}

// TestParityReflectPrimitives verifies reflect.methods works on
// built-in primitive values (list / dict / set / string / bytes /
// range) and returns a sorted method list.
func TestParityReflectPrimitives(t *testing.T) {
	// Expected first-method derived from the registry, not frozen.
	cases := []struct{ literal, typeName string }{
		{`[1,2,3]`, "list"},
		{`{"a":1}`, "dict"},
		{`"abc"`, "string"},
		{`1..5`, "range"},
	}
	src := "import io;\nimport reflect;\n"
	want := ""
	for _, c := range cases {
		src += "io.println(reflect.methods(" + c.literal + ")[0]);\n"
		names := append([]string(nil), native.PrimitiveMethods[c.typeName]...)
		sort.Strings(names)
		want += names[0] + "\n"
	}
	runParity(t, src, want)
}

// TestParityUserErrorParentChain verifies that user-defined error
// subclasses (BadRequestError extends HttpException extends
// RuntimeError) walk the full parent chain under instanceof and catch
// on both backends - the case that was diverging before 1.0.2.
func TestParityUserErrorParentChain(t *testing.T) {
	runParity(t, `import io;

class HttpException extends RuntimeError {
    int status;
    string detail;
    func HttpException(int s, string d) {
        parent("HTTP " + (s as string));
        this.status = s;
        this.detail = d;
    }
}

class BadRequestError extends HttpException {
    func BadRequestError(string d) {
        parent(400, d);
    }
}

let e = BadRequestError("missing");
io.println(e instanceof BadRequestError);
io.println(e instanceof HttpException);
io.println(e instanceof RuntimeError);

let caught = "";
try {
    throw BadRequestError("nope");
} catch (HttpException err) {
    caught = "http:" + err.detail;
}
io.println(caught);
`, "true\ntrue\ntrue\nhttp:nope\n")
}

// TestParityReflectClassByName verifies reflect.class("Name") returns
// the same class metadata on both backends when the class is declared
// in the local module.
func TestParityReflectClassByName(t *testing.T) {
	runParity(t, `import io;
import reflect;

class Box { string label; }

let cls = reflect.class("Box");
io.println(cls != null);
io.println(reflect.fields(cls)[0]["name"]);
io.println(reflect.fields(cls)[0]["type"]);
`, "true\nlabel\nstring\n")
}

// TestParityForwardFunctionReferences guards a compiler regression
// where a function calling a sibling declared later in the same
// file (`func a() { return b(); } func b() { ... }`) failed with
// "no matching overload" because the forward-declared FunctionInfo
// hadn't been populated with parameter / return-type metadata by
// the time the body of `a` was compiled. Pre-pass now records
// signatures up front; bodies fill in on the second pass.
func TestParityForwardFunctionReferences(t *testing.T) {
	runParity(t, `import io;

func a(): bool {
    return b(7);
}

func b(int x): bool {
    return x > 0;
}

io.println(a());
`, "true\n")
}

// TestParityUserClassIterator verifies the 1.0.6 user-class iterator
// protocol: `for (x in obj)` calls obj.__iter() to get an iterator,
// then drives __done()/__next() per step. Both backends must produce
// the same sequence. Also covers a class that implements __next()
// directly (no __iter() method), in which case the instance is its
// own iterator.
func TestParityUserClassIterator(t *testing.T) {
	runParity(t, `import io;

class Range {
    int from;
    int to;
    int cur;

    func Range(int from, int to) {
        this.from = from;
        this.to = to;
        this.cur = from;
    }

    func __iter(): Range {
        this.cur = this.from;
        return this;
    }

    func __done(): bool {
        return this.cur >= this.to;
    }

    func __next(): int {
        int v = this.cur;
        this.cur = this.cur + 1;
        return v;
    }
}

class Steps {
    int n;
    int seen;

    func Steps(int n) {
        this.n = n;
        this.seen = 0;
    }

    func __done(): bool {
        return this.seen >= this.n;
    }

    func __next(): int {
        this.seen = this.seen + 1;
        return this.seen * 10;
    }
}

for (n in Range(2, 5)) {
    io.println(n);
}

for (n in Steps(3)) {
    io.println(n);
}
`, "2\n3\n4\n10\n20\n30\n")
}

// TestParityNestedGenericInference pins the 1.0.6 fix where both
// backends walk parameter type-spec trees recursively to bind type
// params nested inside `list<dict<K, V>>` (etc). Previously only the
// outermost container level was inspected, leaving inner type params
// unbound.
func TestParityNestedGenericInference(t *testing.T) {
	runParity(t, `import io;
import reflect;

class Box<K, V> {
    list<dict<K, V>> rows;
    func Box(list<dict<K, V>> rows) { this.rows = rows; }
}

let b = Box([{"a": 1}]);
let bindings = reflect.typeBindings(b);
io.println(bindings["K"]);
io.println(bindings["V"]);
`, "string\nint\n")
}

// TestParityDeferNamedArgs pins the 1.0.6 fix that lifted the
// "named arguments in defer" rejection for instance method and
// callable-variable defers. Both backends must order the deferred
// arguments by name (not source order) when running the queue.
func TestParityDeferNamedArgs(t *testing.T) {
	runParity(t, `import io;

class Logger {
    string prefix;
    func Logger(string prefix) { this.prefix = prefix; }
    func log(string head, string tail): void {
        io.println(this.prefix + ":" + head + "-" + tail);
    }
}

func run(): void {
    let logger = Logger("M");
    defer logger.log(tail: "end", head: "start");
    let cb = func(string left, string right): void {
        io.println("cb:" + left + "+" + right);
    };
    defer cb(right: "R", left: "L");
    io.println("before");
}

run();
`, "before\ncb:L+R\nM:start-end\n")
}

// TestParityReflectLocation pins the 1.0.6 reflect.location surface:
// the evaluator and VM must agree on the {module, line, column}
// shape and the recorded line/column for both function and class
// declarations.
func TestParityReflectLocation(t *testing.T) {
	runParity(t, `import io;
import reflect;

func one(): int { return 1; }

class A {}

io.println(reflect.location(one)["line"]);
io.println(reflect.location(one)["column"]);
io.println(reflect.location(A)["line"]);
io.println(reflect.location(A)["column"]);
`, "4\n1\n6\n1\n")
}

// TestVMTailCallElimination exercises 1.0.6's OpTailCall path under
// the VM. The evaluator's call-depth limit caps mutual recursion at
// ~10k frames; the VM with TCE collapses the chain into a single
// frame and finishes whatever depth the loop runs to. We assert the
// VM reaches a depth (100_000) the evaluator could not.
func TestVMTailCallElimination(t *testing.T) {
	source := `import io;

func loop(int n, int acc): int {
    if (n == 0) {
        return acc;
    }
    return loop(n - 1, acc + 1);
}

io.println(loop(100000, 0));
`
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	chunk, err := bytecode.Compile(program, []byte(source), "tce")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var out bytes.Buffer
	if err := bytecode.NewVM(chunk, &out).Run(); err != nil {
		t.Fatalf("vm: %v", err)
	}
	if got, want := out.String(), "100000\n"; got != want {
		t.Fatalf("output mismatch: got %q want %q", got, want)
	}
}

// TestParityFloorDivOnDecimalAndFloat verifies the `//` floor-
// division operator handles decimal / float operands (eval used
// to error with "unsupported decimal operator //"). Floor toward
// negative infinity matches Python's `//` and Geblang's
// established int//int behaviour.
func TestParityFloorDivOnDecimalAndFloat(t *testing.T) {
	runParity(t, `import io;

io.println(5 // 2);
io.println(-7 // 2);
io.println(7.5 // 2.0);
io.println((-7.5) // 2.0);
io.println(10.0 % 3.0);
io.println((-10.0) % 3.0);
`, "2\n-4\n3.0000000000\n-4.0000000000\n1.0000000000\n2.0000000000\n")
}

// TestParityCastTruncatesDecimalAndFloat guards the cast policy
// agreed for 1.0.2: `decimal as int` and `float as int` truncate
// toward zero instead of erroring on non-integer values. Matches
// the C/Java/Go integer-cast convention.
func TestParityCastTruncatesDecimalAndFloat(t *testing.T) {
	runParity(t, `import io;

io.println(2.7 as int);
io.println(-2.7 as int);
io.println(2.0 as int);
io.println(true as int);
io.println(false as int);
io.println((3.99 as decimal) as int);
io.println(((-3.99) as decimal) as int);
`, "2\n-2\n2\n1\n0\n3\n-3\n")
}

// TestParityFunctionCallDoesNotBindToCaseFoldedClass guards a
// bytecode-compiler regression where `view(args)` in a module that
// also exports a `View` class was dispatching to the class
// constructor because identifier lookup was case-insensitive. The
// evaluator was always case-sensitive; the VM is now too.
func TestParityFunctionCallDoesNotBindToCaseFoldedClass(t *testing.T) {
	runParity(t, `import io;

class View {
    string label;
    func View(string label, list<int> nums) {
        this.label = label + ":" + (nums.length() as string);
    }
}

func view(string a, string b, dict<string, any> opts = {}): string {
    return "fn:" + a + ":" + b + ":" + (opts.keys().length() as string);
}

func wrap(string a, string b): string {
    return view(a, b, {"k": 1});
}

io.println(wrap("x", "y"));
io.println(View("hello", [1, 2, 3]).label);
`, "fn:x:y:1\nhello:3\n")
}

// TestParitySmallIntCastsToDecimalAndFloat guards a regression where
// the evaluator's `int as decimal` and `int as float` paths only
// handled runtime.Int and rejected runtime.SmallInt with
// "cannot cast int to decimal". Method results that produce SmallInt
// (list.length, string.length, range iterators, ...) are the common
// trigger.
func TestParitySmallIntCastsToDecimalAndFloat(t *testing.T) {
	runParity(t, `import io;
let xs = ["a", "b", "c"];
io.println(xs.length() as decimal);
io.println(xs.length() as float);
any v = xs.length();
io.println(v as decimal);
io.println(v as float);
`, "3.0000000000\n3\n3.0000000000\n3\n")
}

// TestParityCrossTypeCastsForBytesAndCollections guards the 1.0.2
// cast extensions: `string <-> bytes` round-trip UTF-8 (errors on
// invalid byte sequences); `list as set<T>` de-duplicates; and
// `set as list<T>` materializes. Pre-1.0.2 each raised "cannot
// cast X to Y" on both backends; the runtime parity was already
// good (both errored identically), the change is the behaviour.
func TestParityCrossTypeCastsForBytesAndCollections(t *testing.T) {
	runParity(t, `import io;

let b = "hello" as bytes;
io.println(b.length);
io.println(b as string);

let u = "résumé" as bytes;
io.println(u.length);
io.println(u as string);

let dedup = [1, 1, 2, 3, 3] as set<int>;
io.println(dedup.length);

let materialized = {1, 2, 3} as list<int>;
io.println(materialized.length);
`, "5\nhello\n8\nrésumé\n3\n3\n")
}

// TestParityFuncAsCallable guards that a function value casts to
// `callable` / `func` / `function` on both backends. Pre-1.0.3 the
// evaluator's castValue rejected the cast with "cannot cast func
// to callable" while the VM accepted it (via the value's TypeName
// being "func", which matches the target). Both backends now
// route through `runtime.IsCallableValue` for the callable family.
func TestParityFuncAsCallable(t *testing.T) {
	runParity(t, `import io;

let f = func(int n): int { return n + 1; };
let c = f as callable;
io.println(c(5));

any g = func(string s): string { return s + "!"; };
let c2 = g as callable;
io.println(c2("hi"));
`, "6\nhi!\n")
}

// TestParityNullAsNullableType guards `null as ?T` working on
// both backends. The evaluator's cast path used to drop the
// nullable bit from the target TypeRef before calling castValue,
// so the cast rejected null on the eval side while the VM
// accepted it after the 1.0.2 cast-error catchability work. The
// eval path now special-cases a nullable target ahead of the
// class-chain match.
func TestParityNullAsNullableType(t *testing.T) {
	runParity(t, `import io;

class Box {
    int x;
    func Box(int x) { this.x = x; }
}

let n = null;
let b = n as ?Box;
io.println(b == null);
let n2 = null as ?int;
io.println(n2 == null);
let n3 = null as ?string;
io.println(n3 == null);
`, "true\ntrue\ntrue\n")
}

// TestParityStringModule guards the new `string` module
// introduced in 1.0.2 - a namespace for static / factory
// functions that don't fit as instance methods on a string
// value. Pairs with the existing `.codePointAt(i)` instance
// method (round-trips through fromCodePoint).
func TestParityStringModule(t *testing.T) {
	runParity(t, `import io;
import string;

io.println(string.fromCodePoint(65));
io.println(string.fromCodePoint(8364));
io.println(string.fromCodePoint("€".codePointAt(0)));
io.println(string.fromCodePoints([72, 105, 33]));
io.println(string.compare("apple", "banana"));
io.println(string.compare("banana", "apple"));
io.println(string.compare("same", "same"));
io.println(string.equalsFold("Hello", "HELLO"));
io.println(string.equalsFold("abc", "abd"));
`, "A\n€\n€\nHi!\n-1\n1\n0\ntrue\nfalse\n")
}

// TestParityErrorGetMessageAndGetClass guards the Java/PHP-style
// accessor methods on built-in error values. Pre-1.0.2 only the
// `.message` field was exposed - calls like `e.getMessage()`
// (the convention everywhere else) errored with "X has no method
// getMessage". Both names now resolve consistently on eval + VM.
func TestParityErrorGetMessageAndGetClass(t *testing.T) {
	runParity(t, `import io;

try {
    throw RuntimeError("boom");
} catch (RuntimeError e) {
    io.println(e.message);
    io.println(e.getMessage());
    io.println(e.getClass());
}
`, "boom\nboom\nRuntimeError\n")
}

// TestParityCastErrorIsCatchable guards that a failed `x as Y`
// raises a catchable RuntimeError on both backends instead of
// escaping as an uncatchable "bytecode runtime error" (VM
// divergence pre-1.0.2: the VM emitted vm.runtimeError directly,
// so a surrounding try/catch never saw the failure). Uses
// list->int which has no defined cast.
func TestParityCastErrorIsCatchable(t *testing.T) {
	runParity(t, `import io;

try {
    let n = [1, 2, 3] as int;
    io.println("unreached");
} catch (RuntimeError e) {
    io.println("caught: " + e.message);
}
io.println("after");
`, "caught: cannot cast list to int\nafter\n")
}

// TestParityCrossChunkInstanceFields guards a regression where
// `reflect.fields(instance)` on a value handed to a sub-module
// returned an empty list because the originating chunk's
// ClassInfo wasn't reachable from the sub-VM's classIndex. The
// instance's runtime.Class.Fields is now populated at
// construction time and carries the field decorators through any
// module boundary, so framework code (`@Groups` filtering in the
// gebweb framework, similar reflection-driven helpers) sees the
// originating class's annotations.
func TestParityCrossChunkInstanceFields(t *testing.T) {
	runParity(t, `import io;
import reflect;

class Box {
    @Groups("read", "admin") string id;
    string label;
}

func surfaceFieldsFromOtherFunction(any value): int {
    let fields = reflect.fields(value);
    int withGroups = 0;
    for (entry in fields) {
        let f = entry as dict<string, any>;
        if (!f.contains("decorators")) {
            continue;
        }
        for (d in f["decorators"] as list<any>) {
            let dec = d as dict<string, any>;
            if ((dec["name"] as string) == "Groups") {
                withGroups = withGroups + 1;
            }
        }
    }
    return withGroups;
}

let b = Box();
b.id = "b1";
b.label = "demo";
io.println(reflect.fields(b).length());
io.println(surfaceFieldsFromOtherFunction(b));
`, "2\n1\n")
}

// TestParityReflectGetFieldSetField covers the native dynamic-field
// accessors. `reflect.getField(instance, name)` reads a field by
// runtime-known name; `reflect.setField(instance, name, value)`
// writes one. Framework code (Gebweb's @Assert / PATCH handlers)
// needs these to drive validation and partial updates without a
// json.parse(json.stringify(x)) round-trip.
func TestParityReflectGetFieldSetField(t *testing.T) {
	runParity(t, `import io;
import reflect;

class User {
    string name;
    int age;
}

let u = User();
u.name = "Ada";
u.age = 30;

io.println(reflect.getField(u, "name"));
io.println(reflect.getField(u, "age"));
io.println(reflect.getField(u, "missing"));

reflect.setField(u, "name", "Grace");
reflect.setField(u, "age", 40);
io.println(u.name);
io.println(u.age);
`, "Ada\n30\nnull\nGrace\n40\n")
}

// TestParityLocalShadowsBuiltinModule guards a regression where a
// `name.method(...)` call against a local variable whose identifier
// happens to match a built-in stdlib module would dispatch to the
// module instead of invoking the method on the local. Lexical scope
// wins: a local in scope is checked first.
func TestParityLocalShadowsBuiltinModule(t *testing.T) {
	runParity(t, `import io;
import errors;

func f(): int {
    let errors = [1, 2, 3];
    errors = errors.push(4);
    return errors.length();
}
io.println(f());
`, "4\n")
}

// TestParityTrailingCommaInListLiteral guards a parser regression
// where a trailing comma in a list literal raised "expected
// expression, got ]". Trailing commas are legal in dict/set literals
// already; list literals now agree.
func TestParityTrailingCommaInListLiteral(t *testing.T) {
	runParity(t, `import io;

let a = [1, 2, 3,];
let b = [
    "x",
    "y",
];
io.println(a.length());
io.println(b.length());
`, "3\n2\n")
}

// TestParityBareReturnInVoidFunction guards an analyzer regression
// where a bare `return;` inside a function declared as returning
// `void` raised "cannot return null from F returning void". Early
// exits should be legal: there is no value being returned, only an
// early termination of the body. Surfaced while wiring @Assert
// validation through the Gebweb dispatch path.
func TestParityBareReturnInVoidFunction(t *testing.T) {
	runParity(t, `import io;

func early(int n): void {
    if (n < 0) {
        return;
    }
    io.println(n);
}

early(-5);
early(7);
io.println("done");
`, "7\ndone\n")
}

// TestParityFieldDecoratorsAndDottedNames covers two language
// additions that together let frameworks attach annotations to
// class fields: dotted decorator names (`@Assert.email`,
// `@Foo.bar.baz`) parse as a single composite identifier, and
// `@`-prefixed decorators on field declarations inside a class
// body persist into `reflect.fields(class)` as a per-field
// `decorators` list. Field decorators are pure metadata; the
// runtime never executes them automatically.
func TestParityFieldDecoratorsAndDottedNames(t *testing.T) {
	runParity(t, `import io;
import reflect;

class User {
    @Assert.email
    string email;

    @Assert.minLength(2)
    @Assert.maxLength(64)
    string name;

    string id;
}

for (entry in reflect.fields(User)) {
    let field = entry as dict<string, any>;
    io.println(field["name"]);
    if (field.contains("decorators")) {
        for (d in field["decorators"] as list<any>) {
            let dec = d as dict<string, any>;
            io.print("  @" + (dec["name"] as string));
            let args = dec["args"] as list<any>;
            if (args.length() > 0) {
                io.print("(" + (args[0] as string) + ")");
            }
            io.println("");
        }
    }
}
`, "email\n  @Assert.email\nid\nname\n  @Assert.minLength(2)\n  @Assert.maxLength(64)\n")
}

// TestParityReflectClassName verifies that `reflect.className` returns
// the class's own identifier for a class value, an instance, and a
// primitive. Symmetric with `reflect.class(name)` going the other way.
// Required by gebweb's DI container to produce useful error messages
// without instantiating the class.
func TestParityReflectClassName(t *testing.T) {
	runParity(t, `import io;
import reflect;

class User { string name; }
let u = User();

io.println(reflect.className(User));
io.println(reflect.className(u));
io.println(reflect.className("hello"));
io.println(reflect.className(42));
`, "User\nUser\nstring\nint\n")
}

// TestParityClassRefRuntimeConstruction guards a VM-only regression
// where `classRef()` via a variable (e.g. a class value carried
// through a function parameter or obtained from `reflect.class`)
// dispatched as a static-method call on `__invoke` rather than as
// construction. Required by gebweb's DI container which holds class
// refs in a dict and constructs them at resolve time.
func TestParityClassRefRuntimeConstruction(t *testing.T) {
	runParity(t, `import io;

class Box {
    int n;
    func Box(int n) { this.n = n; }
}

func makeOne(any cls): any {
    return cls(7);
}

let b = makeOne(Box) as Box;
io.println(b.n);
`, "7\n")
}

// TestParityParenthesizedSelectorInvokesValue verifies that a
// parenthesized field-access on a method-call target invokes the
// VALUE of the field (a callable) rather than dispatching as a
// method call. Surfaced while caching `appmod.dispatcher(app)` on
// a TestClient field: `(this.dispatch)(request)` previously parsed
// the same as `this.dispatch(request)` so the VM/eval looked up a
// `dispatch` method on TestClient (which doesn't exist).
func TestParityParenthesizedSelectorInvokesValue(t *testing.T) {
	runParity(t, `import io;

class C {
    callable fn;
    func C(callable f) { this.fn = f; }
    func via(int x): int {
        return (this.fn)(x);
    }
}

let c = C(func(int n): int { return n * 3; });
io.println(c.via(7));
`, "21\n")
}

// TestParityReflectMethodPreservesModuleAccess guards an eval-only
// regression where a method body invoked through reflect.method()()
// couldn't resolve imported modules because the bound Native closure
// ran on a fresh stub Evaluator with no module loader. The fix
// captures the live host Evaluator on the bound method's closure.
func TestParityReflectMethodPreservesModuleAccess(t *testing.T) {
	runParityWithStdlib(t, `import io;
import reflect;
import json;

class Ctl {
    func produce(): string {
        return json.stringify({"k": 1});
    }
}

let c = Ctl();
let m = reflect.method(c, "produce");
io.println(m());
`, "{\"k\":1}\n")
}

// TestParityCrossModuleThrowCatch guards a VM-only regression where a
// throw originating in a sub-module (or in a callback dispatched from a
// sub-module) collapsed to "uncaught RuntimeError" at the VM boundary,
// losing the original class + parent chain. The fix wraps the
// underlying runtime.Error in a vmThrownError so the calling VM can
// recover it via errors.As and re-throw it as a typed pendingThrow.
// Surfaced building the Gebweb adapter: stdlib catch (errors.HttpException)
// failed to match user-script throws.
func TestParityCrossModuleThrowCatch(t *testing.T) {
	runParity(t, `import io;

class HttpException extends RuntimeError {
    int status;
    func HttpException(int s, string m) { parent(m); this.status = s; }
}
class NotFoundError extends HttpException {
    func NotFoundError(string m) { parent(404, m); }
}

func wrap(callable fn): string {
    try {
        fn();
        return "no throw";
    } catch (HttpException e) {
        return "caught " + (e.status as string) + ": " + e.message;
    }
}

let userFn = func(): void {
    throw NotFoundError("missing widget");
};

io.println(wrap(userFn));
`, "caught 404: missing widget\n")
}

// TestParitySingleOverloadMethodDispatch guards an `OpAdd`-style
// fast-path on the VM's `selectRuntimeFunction` for methods that
// have exactly one declared overload. Most user classes hit this
// case on every dispatch (50000+ times on the `class_dispatch`
// benchmark); the fast path skips the matches-slice allocation +
// the post-loop "ambiguous overload" check. Behaviour is unchanged.
func TestParitySingleOverloadMethodDispatch(t *testing.T) {
	runParity(t, `import io;

class Counter {
    int value;
    func Counter(int start) { this.value = start; }
    func step(int delta): int {
        this.value = this.value + delta;
        return this.value;
    }
}

let c = Counter(0);
io.println(c.step(1));
io.println(c.step(5));
io.println(c.step(-2));

/* Single-overload methods on inheriting classes still dispatch
 * correctly; the parent's method runs when the child doesn't
 * redeclare it. */
class Base { func name(): string { return "base"; } }
class Child extends Base { }
io.println(Child().name());
`, "1\n6\n4\nbase\n")
}

// TestParityDictKeyFastPath guards the VM's `dictKeyFor` helper
// (a fast-path wrapper around native.DictKey for the common String
// and SmallInt key types). Hot dict ops (`dict[\"k\"]`, `dict.get`,
// `dict.contains`) use it; mixed key types still produce the
// canonical key string via native.DictKey.
func TestParityDictKeyFastPath(t *testing.T) {
	runParity(t, `import io;

dict<string, int> d = {};
for (int i = 0; i < 5; i++) {
    let k = "k" + (i as string);
    d[k] = i;
}
for (int i = 0; i < 5; i++) {
    let k = "k" + (i as string);
    io.println(d.contains(k));
    io.println(d.get(k));
}

dict<int, string> ints = {1: "one", 2: "two"};
io.println(ints.contains(1));
io.println(ints.get(2));

dict<any, int> mixed = {};
mixed["k"] = 1;
mixed[42] = 2;
mixed[true] = 3;
io.println(mixed.contains("k"));
io.println(mixed.contains(42));
io.println(mixed.contains(true));
io.println(mixed.length());
`, "true\n0\ntrue\n1\ntrue\n2\ntrue\n3\ntrue\n4\ntrue\ntwo\ntrue\ntrue\ntrue\n3\n")
}

// TestParityOpAddStringStaticTyping guards the compile-time
// `OpAddString` specialisation: when both operands of `+` are
// statically typed `string`, the compiler emits a type-specialised
// opcode that skips the runtime type switch + magic-method dispatch.
// Verifies the static-string detection works through identifiers,
// literals, and nested concatenations.
func TestParityOpAddStringStaticTyping(t *testing.T) {
	runParity(t, `import io;

string a = "hello";
string b = " ";
string c = "world";
io.println(a + b + c);
io.println("foo" + "bar");

func greet(string name): string {
    return "hi " + name;
}
io.println(greet("ada"));

/* Untyped local with a string value still flows through the
 * generic OpAdd; the specialiser only fires for STATICALLY typed
 * operands. Both backends produce the same output regardless. */
let s = "x";
io.println(s + "y");
`, "hello world\nfoobar\nhi ada\nxy\n")
}

// TestParityMethodLookupCache guards the single-slot method-lookup
// cache the VM uses to skip the `classInfo.Methods` map access on
// the second-and-later dispatches to the same method on the same
// class. A tight loop calling `Counter.step` repeatedly hits the
// cache after the first call. Switching to a different method on
// the same class refills the cache; calls to the parent's method
// continue to walk the parent chain on a miss.
func TestParityMethodLookupCache(t *testing.T) {
	runParity(t, `import io;

class Counter {
    int value;
    func Counter() { this.value = 0; }
    func step(int n): int { this.value = this.value + n; return this.value; }
    func double(): int { this.value = this.value * 2; return this.value; }
}

let c = Counter();
for (int i = 0; i < 5; i++) {
    c.step(1);
}
io.println(c.value);
io.println(c.double());
io.println(c.step(10));

class Base {
    func tag(): string { return "base"; }
}
class Child extends Base { }
let ch = Child();
io.println(ch.tag());
io.println(ch.tag());
io.println(ch.tag());
`, "5\n10\n20\nbase\nbase\nbase\n")
}

// TestParityEmptyContainerDefaults guards the lifted compiler parity
// gap: `dict opts = {}`, `list xs = []`, and `set s = set()`-shaped
// parameter defaults now compile directly to bytecode. Empty
// containers go into the constant pool and the VM clones at fill
// time so each call sees a fresh empty container - avoiding the
// Python-style mutable-default shared-state trap.
func TestParityEmptyContainerDefaults(t *testing.T) {
	runParity(t, `import io;

func use_opts(dict<string, any> opts = {}): int { return opts.length(); }
func use_list(list<int> xs = []): int { return xs.length(); }

io.println(use_opts());
io.println(use_opts({"a": 1, "b": 2}));
io.println(use_list());
io.println(use_list([1, 2, 3]));

/* Mutable-default isolation: each call without args sees a fresh
 * empty dict, NOT the same instance accumulating state. */
func incr(dict<string, int> d = {}): int {
    d["n"] = (d["n"] ?? 0) + 1;
    return d["n"];
}
io.println(incr());
io.println(incr());
io.println(incr());
`, "0\n2\n0\n3\n1\n1\n1\n")
}

// TestParityStaticFunctionLifted guards the lifted compiler parity
// gap: `static func` declarations now compile directly to bytecode
// instead of falling back to the evaluator. Static methods can be
// called via `ClassName.method(...)`; static-const class members
// (a 1.0.2-era feature) work alongside them.
func TestParityStaticFunctionLifted(t *testing.T) {
	runParity(t, `import io;

class Counter {
    static const VERSION = "1.0";

    static func make(int start): Counter {
        let c = Counter();
        c.value = start;
        return c;
    }

    int value;
    func Counter() { this.value = 0; }

    func double(): int { this.value = this.value * 2; return this.value; }
}

let c = Counter.make(7);
io.println(c.double());
io.println(Counter.VERSION);

class Registry {
    static func register(string name): string { return "registered:" + name; }
}

io.println(Registry.register("widget"));
`, "14\n1.0\nregistered:widget\n")
}

// TestParityStringAddFastPath guards an `OpAdd` reorder in the VM:
// `string + string` is fast-pathed before the binary-operator-method
// detour (`callBinaryOperatorMethod` does nothing useful when the
// left operand is a `runtime.String` since the built-in string type
// has no `__add` magic method). Verifies the common path AND that an
// instance with `__add` on the LEFT still routes through method
// dispatch (the fast path only matches when both operands are
// strings).
func TestParityStringAddFastPath(t *testing.T) {
	runParity(t, `import io;

io.println("hello " + "world");
io.println("" + "x");
io.println("a" + "" + "b" + "c");

class Adder {
    int n;
    func Adder(int n) { this.n = n; }
    func __add(int other): int { return this.n + other; }
}

let a = Adder(10);
io.println(a + 5);
`, "hello world\nx\nabc\n15\n")
}

// TestParityUserClassNamedTaskNoCollision guards an evaluator-only
// regression where a user class named `Task` was unconditionally
// rejected by the overload / parameter type-matcher: the evaluator
// short-circuited on `typeName == "Task"` and required the value to
// be a *runtime.Task (the async Task primitive). User-declared Task
// classes now match their own instances. The VM was already correct
// because its type-name dispatch routes through `vmTypeKindForBase`
// and never hard-codes the `Task` string.
// Surfaced building the gebweb Tasks example app, where every CRUD
// handler called `repo.save(Task entity)`.
func TestParityUserClassNamedTaskNoCollision(t *testing.T) {
	runParity(t, `import io;

class Task {
    string id;
    string title;
    func Task() { this.id = ""; this.title = ""; }
}

class Store {
    list<Task> items;
    func Store() { this.items = []; }
    func add(Task t): Task { this.items = this.items.push(t); return t; }
    func adopt(?Task t): bool {
        if (t == null) { return false; }
        this.add(t as Task);
        return true;
    }
}

let s = Store();
let t = Task();
t.id = "t-1";
t.title = "hello";
io.println(s.add(t).title);
io.println(s.adopt(t));
io.println(s.adopt(null));
io.println(s.items.length());
io.println(t instanceof Task);
`, "hello\ntrue\nfalse\n2\ntrue\n")
}

// TestParityWebParseMultipart guards the new `web.parseMultipart` native:
// both backends parse a `multipart/form-data` body into a
// `{fields, files}` dict, where each file is `{filename, contentType,
// bytes}`. The native is stateful so the VM dispatches through the
// evaluator, but the parity test still documents the public shape.
func TestParityWebParseMultipart(t *testing.T) {
	runParityStateful(t, `import io;
import web;

let boundary = "----parity1";
let body = "------parity1\r\n" +
    "Content-Disposition: form-data; name=\"name\"\r\n\r\n" +
    "alice\r\n" +
    "------parity1\r\n" +
    "Content-Disposition: form-data; name=\"avatar\"; filename=\"a.png\"\r\n" +
    "Content-Type: image/png\r\n\r\n" +
    "PNG_BYTES\r\n" +
    "------parity1--\r\n";

let r = {
    "method": "POST",
    "path": "/u",
    "headers": {"Content-Type": "multipart/form-data; boundary=" + boundary},
    "body": body,
};

let parsed = web.parseMultipart(r) as dict<string, any>;
let fields = parsed["fields"] as dict<string, any>;
let files = parsed["files"] as dict<string, any>;
let avatar = files["avatar"] as dict<string, any>;

io.println(fields["name"]);
io.println(avatar["filename"]);
io.println(avatar["contentType"]);
io.println((avatar["bytes"] as bytes) as string);
`, "alice\na.png\nimage/png\nPNG_BYTES\n")
}

// TestParityImportAliasDoesNotCollideAcrossFiles guards a VM-only-correct
// regression: the evaluator kept a process-wide `importNames` map that
// recorded the LAST `import X as Y` to use alias `Y`. Two files that both
// used the same alias for different canonical modules (e.g. a user file
// `import web.websocket as websocket;` while stdlib `import websocket;`
// keeps the native) collided - whichever import ran last won, and stdlib
// code that wanted the native ended up dispatching against the user
// module, surfacing as "module websocket has no export upgrade".
// The VM was already correct because each compiled chunk owns its own
// globals; the evaluator now consults the env-local Module's
// `Canonical` field first and only falls back to the shared map.
func TestParityImportAliasDoesNotCollideAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	donorModule := filepath.Join(dir, "alias_donor.gb")
	/* The donor module has its own `import websocket;` that should
	 * resolve to the NATIVE websocket regardless of what aliases
	 * the caller registers. We can't call native upgrade() from
	 * the donor scope without actually upgrading, so the donor
	 * exposes a tiny shim that calls a function we KNOW only the
	 * native module exports (`websocket.upgrade` returns a dict
	 * with a `websocket` key). */
	if err := os.WriteFile(donorModule, []byte(`module alias_donor;

import websocket;

export func makeUpgrade(callable handler): dict<string, any> {
    return websocket.upgrade(handler);
}
`), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}

	source := `import io;
import alias_donor as donor;
/* Alias web.websocket to the same identifier the donor's
 * import websocket uses. The donor must continue to resolve to the
 * native websocket regardless of this user-side alias. */
import web.websocket as websocket;

let r = donor.makeUpgrade(func(any conn): void { });
io.println(r.contains("websocket"));
/* Plus verify our local alias still resolves to web.websocket: the
 * wrapped version also returns a dict containing "websocket". */
let local = websocket.upgrade(func(any conn): void { });
io.println(local.contains("websocket"));
`

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	want := "true\ntrue\n"

	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("evaluator: %v", err)
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
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: %v", err)
	}
	if vmOut.String() != want {
		t.Fatalf("vm output: got %q, want %q", vmOut.String(), want)
	}
}

// TestParityListSortAliasesSorted guards the `list.sort()` -> `list.sorted()`
// alias: the LSP catalog (`internal/lsp/catalog.go:142-143`) advertised both
// names but only `sorted` was dispatched at runtime, so any user code reading
// the documented surface and writing `xs.sort()` failed with
// `list has no method sort`. Both backends now accept both names.
func TestParityListSortAliasesSorted(t *testing.T) {
	runParity(t, `import io;
let xs = [3, 1, 4, 1, 5, 9, 2, 6];
io.println(xs.sort());
io.println(xs.sorted());
let desc = xs.sort(func(int a, int b): bool { return a > b; });
io.println(desc);
`, "[1, 1, 2, 3, 4, 5, 6, 9]\n[1, 1, 2, 3, 4, 5, 6, 9]\n[9, 6, 5, 4, 3, 2, 1, 1]\n")
}

// TestParityCrossModuleImplements guards a VM-only regression where the
// bytecode compiler rejected `class C implements mod.Iface { ... }` for
// any interface declared in a different module - including
// `gebweb.repository.Repository<T>`, the canonical case that motivated
// the fix. The evaluator's `resolveTypeValue` already walked imports;
// the compiler's local-only `c.interfaces` lookup did not, mirroring
// the parent-class case that was already allowed at compiler.go:891.
// Verifies `instanceof` matches against the dotted name AND against the
// trailing identifier on both backends.
func TestParityCrossModuleImplements(t *testing.T) {
	dir := t.TempDir()
	donorModule := filepath.Join(dir, "iface_donor.gb")
	if err := os.WriteFile(donorModule, []byte(`module iface_donor;

export interface Pingable {
    func ping(): string;
}

export interface Countable {
    func count(): int;
}
`), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}

	source := `import io;
import iface_donor as donor;

class Pinger implements donor.Pingable {
    func ping(): string { return "ok"; }
}

class Tally implements donor.Pingable, donor.Countable {
    int n;
    func Tally(int start) { this.n = start; }
    func ping(): string { return "tally"; }
    func count(): int { this.n = this.n + 1; return this.n; }
}

let p = Pinger();
io.println(p.ping());
io.println(p instanceof donor.Pingable);
io.println(p instanceof Pingable);
io.println(p instanceof donor.Countable);

let t = Tally(0);
io.println(t.ping());
io.println(t.count());
io.println(t instanceof donor.Pingable);
io.println(t instanceof donor.Countable);
`

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	want := "ok\ntrue\ntrue\nfalse\ntally\n1\ntrue\ntrue\n"

	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("evaluator: %v", err)
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
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: %v", err)
	}
	if vmOut.String() != want {
		t.Fatalf("vm output: got %q, want %q", vmOut.String(), want)
	}
}

// TestParityCastWidensToParentClass guards a VM-only regression where the
// `as` operator rejected widening an error-derived value (or instance)
// to a parent class declared in another module. Surfaced while writing
// the Gebweb hello-world example: the framework adapter does `e as
// errors.HttpException` against a thrown NotFoundError. Evaluator
// already walked the parent chain; VM did not.
func TestParityCastWidensToParentClass(t *testing.T) {
	runParity(t, `import io;

class A extends RuntimeError {
    func A(string m) { parent(m); }
}
class B extends A {
    func B(string m) { parent(m); }
}

let b = B("nope");
let a = b as A;
io.println(a instanceof A);
io.println(a instanceof B);

class P {}
class C extends P {}
let c = C();
let p = c as P;
io.println(p instanceof P);
io.println(p instanceof C);
`, "true\ntrue\ntrue\ntrue\n")
}

// TestParityNullMatchesAnyParam guards a VM regression where method
// overload resolution rejected a null argument flowing into an `any`-typed
// parameter. The evaluator always accepted null for any; the VM tripped
// on the early null check in matchValueToTypeSpec before the vmTypeAny
// short-circuit. Surfaced while writing the Gebweb hello-world example
// (TestClient.send accepts `any body` and was being called with null).
func TestParityNullMatchesAnyParam(t *testing.T) {
	runParity(t, `import io;

class TestClient {
    func send(string method, any body): int {
        return 99;
    }
    func get(string path): int {
        return this.send("GET", null);
    }
}

let c = TestClient();
io.println(c.get("/"));
io.println(c.send("POST", {"k": 1}));
io.println(c.send("PUT", "raw body"));
`, "99\n99\n99\n")
}

// TestParityCallableSpread guards the lifted compiler parity gap:
// spread arguments on a callable VALUE (parenthesized selector
// expression like `(obj.fn)(...args)`, or any complex callable
// expression like `arr[i](...args)` or `getFn()(...args)`) used to
// route to the evaluator. The VM now compiles both forms directly,
// emitting OpMethodCallSpread with `__invoke`.
func TestParityCallableSpread(t *testing.T) {
	runParity(t, `import io;

class Holder {
    callable adder;
    func Holder() {
        this.adder = func(int a, int b, int c): int { return a + b + c; };
    }
}

let h = Holder();
let args = [1, 2, 3];
/* parenthesized selector callable spread */
io.println((h.adder)(...args));

/* complex callable spread: indexed list element */
let fns = [func(int a, int b): int { return a * b; }];
io.println(fns[0](...[4, 5]));

/* complex callable spread: function-call result */
func getMul(): callable {
    return func(int a, int b, int c): int { return a * b * c; };
}
io.println(getMul()(...[2, 3, 4]));
`, "6\n20\n24\n")
}

// TestParityCallResolvedMethod exercises the OpCallResolvedMethod
// specialised opcode the compiler emits when the receiver's class is
// statically known and the method resolves to a single non-decorated
// overload with no subclass overrides.
func TestParityCallResolvedMethod(t *testing.T) {
	runParity(t, `import io;

class Counter {
    int value;
    func Counter(int start) {
        this.value = start;
    }
    func step(int delta): int {
        this.value = this.value + delta;
        return this.value;
    }
    func double(): int {
        this.value = this.value * 2;
        return this.value;
    }
}

let c = Counter(10);
io.println(c.step(5));    /* 15 */
io.println(c.step(7));    /* 22 */
io.println(c.double());   /* 44 */
io.println(c.step(-4));   /* 40 */
`, "15\n22\n44\n40\n")
}

// TestParityAddStringConst exercises the OpAddStringConst opcode
// emitted when one operand of `+` is a static string literal.
func TestParityAddStringConst(t *testing.T) {
	runParity(t, `import io;

string acc = "";
for (int i = 0; i < 5; i++) {
    if (i % 3 == 0) {
        acc = acc + "a";
    } else if (i % 2 == 0) {
        acc = acc + "bc";
    } else {
        acc = acc + "1";
    }
}
io.println(acc);
`, "a1bcabc\n")
}

// TestParityCastDunders exercises __string/__int/__float/__bool/__decimal/__bytes
// cast-overload dunders. Both backends call the dunder when the
// receiver is an instance and the target is a built-in primitive.
func TestParityStringAccumulatorLoop(t *testing.T) {
	runParity(t, `import io;

string acc = "";
for (int i = 0; i < 200; i++) {
    acc = acc + "x";
}
io.println(acc.length());
io.println(acc.substring(0, 3));
io.println(acc.substring(197, 200));
`, "200\nxxx\nxxx\n")
}

func TestParityStringAccumulatorInterleavedRead(t *testing.T) {
	runParity(t, `import io;

string acc = "";
for (int i = 0; i < 10; i++) {
    acc = acc + "ab";
    if (i == 4) {
        io.println(acc);
    }
}
io.println(acc);
`, "ababababab\nabababababababababab\n")
}

func TestParityConstantFolding(t *testing.T) {
	runParity(t, `import io;

io.println(3 + 5);
io.println(2 * 10);
io.println(20 - 4);
io.println(13 // 5);
io.println(-7 // 3);
io.println(7 % -3);
io.println(5 == 5);
io.println(5 == 6);
io.println(2 < 3);
io.println(1.5 + 2.5);
io.println(2.0 * 3.5);
io.println(0.5 < 1.0);
io.println("foo" + "bar");
io.println("a" == "b");
io.println(true == true);
io.println(true != false);
`, "8\n20\n16\n2\n-3\n-2\ntrue\nfalse\ntrue\n4.0000000000\n7.0000000000\ntrue\nfoobar\nfalse\ntrue\ntrue\n")
}

func TestParityStringRegexMethods(t *testing.T) {
	runParity(t, `import io;

io.println("foo,bar,baz".splitRegex(",").length);
io.println("foo, bar; baz".replaceRegex("[,;] *", "|"));
io.println("foo123".matchesRegex("[a-z]+[0-9]+"));
io.println("only-letters".matchesRegex("[0-9]+"));
`, "3\nfoo|bar|baz\ntrue\nfalse\n")
}

func TestParityMathStats(t *testing.T) {
	runParity(t, `import io;
import math;

let xs = [0, 10, 20, 30, 40];
io.println(math.median(xs));
io.println(math.percentile(xs, 25));
io.println(math.percentile(xs, 75));
io.println(math.mode([1, 1, 2, 2, 3]));
`, "20\n10\n30\n1\n")
}

func TestParityCSVParseAndStringify(t *testing.T) {
	runParity(t, `import io;
import csv;

let text = "a,b,c\n1,2,3\n4,5,6";
let rows = csv.parse(text);
io.println(rows.length);
io.println(rows[1][1]);

let dicts = csv.parseDict(text);
io.println(dicts[0]["b"]);
io.println(dicts[1]["c"]);
`, "3\n2\n2\n6\n")
}

func TestParityCSVCustomDelimiter(t *testing.T) {
	runParity(t, `import io;
import csv;

let text = "a;b;c\n1;2;3";
let rows = csv.parse(text, {"delimiter": ";"});
io.println(rows.length);
io.println(rows[1][2]);
`, "2\n3\n")
}

func TestParityFieldLookupCacheAcrossClasses(t *testing.T) {
	runParity(t, `import io;

class A {
    int x;
    func A(int x) { this.x = x; }
}

class B {
    int x;
    func B(int x) { this.x = x; }
}

let a = A(1);
let b = B(2);
for (int i = 0; i < 50; i++) {
    a.x = a.x + 1;
    b.x = b.x + 10;
}
io.println(a.x);
io.println(b.x);
`, "51\n502\n")
}

func TestParityFieldLookupCacheWithGetMagic(t *testing.T) {
	runParity(t, `import io;

class WithGet {
    int n;
    func WithGet(int n) { this.n = n; }
    func __get(string name): int {
        return this.n * 100;
    }
}

let w = WithGet(3);
io.println(w.n);
io.println(w.dynamic);
io.println(w.other);
`, "3\n300\n300\n")
}

func TestParityFieldLookupCacheWithSetMagic(t *testing.T) {
	runParity(t, `import io;

class WithSet {
    dict<string, any> extras;
    func WithSet() { this.extras = {}; }
    func __set(string name, any value): void {
        this.extras[name] = value;
    }
}

let w = WithSet();
w.foo = 1;
w.bar = 2;
io.println(w.extras["foo"]);
io.println(w.extras["bar"]);
`, "1\n2\n")
}

func TestParityStringAccumulatorEscapesAssignment(t *testing.T) {
	runParity(t, `import io;

string acc = "";
for (int i = 0; i < 5; i++) {
    acc = acc + "ab";
}
string copy = acc;
acc = acc + "cd";
io.println(copy);
io.println(acc);
`, "ababababab\nabababababcd\n")
}

func TestParityCastDunders(t *testing.T) {
	runParity(t, `import io;

class Box {
    int n;
    func Box(int n) { this.n = n; }
    func __string(): string { return "Box(" + (this.n as string) + ")"; }
    func __int(): int { return this.n; }
    func __float(): float { return (this.n as float); }
    func __bool(): bool { return this.n != 0; }
    func __decimal(): decimal { return (this.n as decimal); }
    func __bytes(): bytes { return ("Box(" + (this.n as string) + ")") as bytes; }
}

let b = Box(42);
io.println(b as string);
io.println(b as int);
io.println(b as float);
io.println(b as bool);
io.println(b as decimal);
io.println((b as bytes) as string);
io.println(Box(0) as bool);
`, "Box(42)\n42\n42\ntrue\n42.0000000000\nBox(42)\nfalse\n")
}

func TestParityGeneratorMethodOnClass(t *testing.T) {
	runParity(t, `import io;

class Box {
    int max;
    func Box(int m) { this.max = m; }
    func nums(): generator<int> {
        for (let int i = 0; i < this.max; i++) {
            yield i;
        }
    }
}

let b = Box(3);
for (n in b.nums()) {
    io.println(n);
}
`, "0\n1\n2\n")
}

func TestParityStreamsMemoryReadLine(t *testing.T) {
	runParityWithStdlib(t, `import io;
import streams;

let mem = streams.memory("alpha\nbeta\ngamma\n");
io.println(mem.readLine());
io.println(mem.readLine());
io.println(mem.readLine());
io.println(mem.readLine());
`, "alpha\nbeta\ngamma\nnull\n")
}

func TestParityStreamsForInIteratesLines(t *testing.T) {
	runParityWithStdlib(t, `import io;
import streams;

let mem = streams.memory("a\nb\nc\n");
for (line in mem) {
    io.println(line);
}
`, "a\nb\nc\n")
}

func TestParityStreamsCopyViaDunders(t *testing.T) {
	runParityWithStdlib(t, `import io;
import streams;

let src = streams.memory("the quick brown fox");
let dst = streams.memory();
let n = streams.copy(src, dst);
io.println(n);
io.println(dst.toString());
`, "19\nthe quick brown fox\n")
}

func TestParityStreamsUserDunderProtocol(t *testing.T) {
	runParityWithStdlib(t, `import io;
import streams;

class Chunked {
    int step;
    func Chunked() { this.step = 0; }
    func __read(int n): string {
        if (this.step == 0) { this.step = 1; return "hello "; }
        if (this.step == 1) { this.step = 2; return "world"; }
        return "";
    }
}

let dst = streams.memory();
streams.copy(Chunked(), dst);
io.println(dst.toString());
io.println(streams.readAll(Chunked()));
`, "hello world\nhello world\n")
}

// TestParityUserClassOpOverloadInStreamReduce exercises user-class
// operator overloads (__add, __eq) inside a streams.reduce / .anyMatch
// pipeline where the reducer closure is created in the main chunk and
// fired from inside the streams sub-VM. The 1.0.6 cross-chunk closure
// dispatch fix routes the closure back to its declaring chunk so the
// __add / __eq dispatches happen in the main-chunk VM, where the user
// class lives.
//
// Companion to TestParityStreamsUserDunderProtocol, which covers the
// methodCall cross-chunk path (`vm.go:~10465`). The prefix-op, __eq,
// and binary-op guards at vm.go:~2520 / ~2647 / ~3307 are also
// relaxed to `instance.Class.Module != vm.moduleName` for symmetry,
// but no current stdlib code path evaluates a magic op directly on a
// user-class instance from inside a sub-VM. The relaxation is
// defensive; this test locks in the closure-dispatch route that real
// stream pipelines exercise.
func TestParityUserClassOpOverloadInStreamReduce(t *testing.T) {
	runParityWithStdlib(t, `import io;
import streams;

class Money {
    int cents;
    func Money(int c) { this.cents = c; }
    func __add(any other): Money {
        return Money(this.cents + (other as Money).cents);
    }
    func __eq(any other): bool {
        if (!(other instanceof Money)) { return false; }
        return this.cents == (other as Money).cents;
    }
    func __string(): string {
        return "$" + (this.cents as string);
    }
}

let total = streams.of([Money(100), Money(250), Money(75)])
    .reduce(Money(0), func(any acc, any x): any {
        return (acc as Money) + (x as Money);
    });
io.println(total as string);

let hasExact = streams.of([Money(10), Money(20), Money(30)])
    .anyMatch(func(any x): bool { return (x as Money) == Money(20); });
io.println(hasExact);

let missing = streams.of([Money(10), Money(20), Money(30)])
    .anyMatch(func(any x): bool { return (x as Money) == Money(999); });
io.println(missing);
`, "$425\ntrue\nfalse\n")
}

// TestParityWatchStartStopFires exercises the F5 watch.start /
// watch.stop callback path. The fsnotify watcher fires the event
// callback on a goroutine; the eval-side child evaluator and the
// VM-side wrap-bridge both have to propagate the module-level
// `kinds` mutation back to the parent before the assertion reads it.
// watch.stop waits for the dispatch goroutine to drain so the read
// happens-after the last callback write.
func TestParityWatchStartStopFires(t *testing.T) {
	runParityWithStdlib(t, `import io;
import watch;
import sys;
import path;

let p = path.join(sys.tmpdir(), "geb_parity_watch.txt");
io.writeText(p, "v1");

list<string> kinds = [];
let h = watch.start(p, func(dict<string, any> e): void {
    kinds = kinds.push(e["type"] as string);
});

sys.sleep(50);
io.writeText(p, "v2");
sys.sleep(150);

watch.stop(h);
io.remove(p);
io.println(kinds.contains("write"));
`, "true\n")
}

// TestParityProcSpawnEcho exercises F4 subprocess streaming on
// both backends: spawn echo, read stdout to EOF, wait for exit.
// Reuses the IOStream wrapper from F3 - proc.spawn returns a
// process whose stdout/stderr/stdin are IOStream-shaped.
func TestParityProcSpawnEcho(t *testing.T) {
	runParityWithStdlib(t, `import io;
import proc;

let p = proc.spawn("echo", ["hello", "world"]);
let out = p.stdout.readAll();
let code = p.wait();
io.print(out);
io.println(code);
`, "hello world\n0\n")
}

func TestParityProcStdinPipe(t *testing.T) {
	runParityWithStdlib(t, `import io;
import proc;

let p = proc.spawn("cat", []);
p.stdin.write("ping\n");
p.stdin.close();
let out = p.stdout.readAll();
let code = p.wait();
io.print(out);
io.println(code);
`, "ping\n0\n")
}

// TestParitySocketsEchoRoundTrip exercises the F3-shaped sockets
// stdlib wrapper on both backends: a sockets.serve handler receives
// a Socket, the client dials, writes a line, and reads back the
// echo. Server.close drains the accept goroutine so the read on
// the parent goroutine happens-after the last callback write.
func TestParitySocketsEchoRoundTrip(t *testing.T) {
	runParityWithStdlib(t, `import io;
import sockets;
import streams;
import sys;

list<string> received = [];
let server = sockets.serve("127.0.0.1", 0, func(dict<string, any> raw): void {
    let stream = streams.IOStream(raw["stream"]);
    while (true) {
        let line = stream.readLine();
        if (line == null) { break; }
        received = received.push(line as string);
        stream.writeln("echo: " + (line as string));
    }
    stream.close();
});

let port = (server.localAddr().split(":")[1] as string) as int;
let client = sockets.dial("127.0.0.1", port);
client.writeln("ping");
let reply = client.readLine();
client.close();
sys.sleep(100 as int);
server.close();

io.println(reply);
io.println(received.length());
`, "echo: ping\n1\n")
}

// sshTestServer runs an in-process SSH server for hermetic SSH
// parity tests. The server accepts password "secret" for any
// username, signs an ephemeral RSA host key, and handles "exec"
// requests by running a tiny set of canned commands in a
// goroutine. Returns (addr, hostKeyPath, stopFn). The
// hostKeyPath is a temporary file containing the server's host
// key in known_hosts format so the test can verify without
// relying on insecureSkipHostKey.
type sshTestServer struct {
	listener net.Listener
	hostKey  ssh.Signer
	wg       sync.WaitGroup
	stopCh   chan struct{}
}

func startSSHTestServer(t *testing.T) *sshTestServer {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("ssh server rsa key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatalf("ssh server signer: %v", err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected")
		},
	}
	cfg.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ssh server listen: %v", err)
	}
	srv := &sshTestServer{listener: listener, hostKey: signer, stopCh: make(chan struct{})}
	srv.wg.Add(1)
	go srv.acceptLoop(cfg)
	return srv
}

func (s *sshTestServer) acceptLoop(cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn, cfg)
	}
}

func (s *sshTestServer) handleConn(rawConn net.Conn, cfg *ssh.ServerConfig) {
	defer rawConn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(rawConn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		ch, reqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(ch, reqs)
	}
}

func (s *sshTestServer) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "exec":
			cmd := string(req.Payload[4:])
			_ = req.Reply(true, nil)
			s.runCommand(ch, cmd)
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			return
		case "shell":
			_ = req.Reply(true, nil)
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *sshTestServer) runCommand(ch ssh.Channel, cmd string) {
	// Tiny canned responses so the test doesn't depend on a real
	// shell. The expectations stay readable in the parity test
	// source.
	switch {
	case cmd == "echo hello":
		_, _ = ch.Write([]byte("hello\n"))
	case cmd == "echo err 1>&2":
		_, _ = ch.Stderr().Write([]byte("err\n"))
	case strings.HasPrefix(cmd, "cat"):
		// Stream stdin to stdout so spawn/stdin tests can pipe.
		_, _ = io.Copy(ch, ch)
	}
}

func (s *sshTestServer) addr() string {
	return s.listener.Addr().String()
}

func (s *sshTestServer) port() string {
	_, port, _ := net.SplitHostPort(s.addr())
	return port
}

func (s *sshTestServer) stop() {
	_ = s.listener.Close()
	s.wg.Wait()
}

func TestParitySSHExec(t *testing.T) {
	srv := startSSHTestServer(t)
	defer srv.stop()
	runParityWithStdlib(t, fmt.Sprintf(`import io;
import ssh;

let c = ssh.connect("alice@127.0.0.1", {
    "port": %s,
    "password": "secret",
    "insecureSkipHostKey": true,
});
let r = c.exec("echo hello");
io.print(r.stdout);
io.println(r.exitCode);
c.close();
`, srv.port()), "hello\n0\n")
}

func TestParitySSHSpawnEcho(t *testing.T) {
	srv := startSSHTestServer(t)
	defer srv.stop()
	runParityWithStdlib(t, fmt.Sprintf(`import io;
import ssh;

let c = ssh.connect("alice@127.0.0.1", {
    "port": %s,
    "password": "secret",
    "insecureSkipHostKey": true,
});
let s = c.spawn("cat");
s.stdin.write("ping\n");
s.stdin.close();
io.print(s.stdout.readAll());
io.println(s.wait());
c.close();
`, srv.port()), "ping\n0\n")
}

func TestParityHttpStreamingBody(t *testing.T) {
	runParityWithStdlib(t, `import io;
import http;
import streams;
import sys;

let server = http.listen("127.0.0.1:0", func(dict<string, any> req): dict<string, any> {
    return {"status": 200, "body": "got: " + (req["body"] as string)};
});
let port = (http.serverAddr(server).split(":")[1] as string) as int;
sys.sleep(20 as int);

let body = streams.memory("streamed-payload");
let r = http.post("http://127.0.0.1:" + (port as string) + "/u", body);
io.println(r["status"]);
io.println(r["body"]);
http.shutdown(server);
`, "200\ngot: streamed-payload\n")
}

// TestParityPCREBasic exercises the pcre module's PHP-compatible
// regex engine. Most patterns here also work with re/RE2; the
// PCRE-only features are covered by the lookahead / lookbehind /
// backref tests below.
func TestParityPCREBasic(t *testing.T) {
	runParity(t, `import pcre;
import io;
io.println(pcre.test("\\d+", "abc123") as string);
io.println(pcre.test("\\d+", "abc") as string);
io.println(pcre.find("[A-Z]+", "hello WORLD") ?? "null");
let all = pcre.findAll("\\d+", "1 two 3 four 56");
for (let i = 0; i < all.length(); i = i + 1) {
    io.println(all[i] as string);
}
`, "true\nfalse\nWORLD\n1\n3\n56\n")
}

// TestParityPCRELookahead verifies PCRE-only lookahead assertions,
// which RE2 does not support.
func TestParityPCRELookahead(t *testing.T) {
	runParity(t, `import pcre;
import io;
io.println(pcre.find("\\w+(?=ing\\b)", "swimming and running") ?? "null");
io.println(pcre.test("foo(?!bar)", "foobaz") as string);
io.println(pcre.test("foo(?!bar)", "foobar") as string);
`, "swimm\ntrue\nfalse\n")
}

// TestParityPCRELookbehind verifies PCRE-only lookbehind, which
// RE2 does not support.
func TestParityPCRELookbehind(t *testing.T) {
	runParity(t, `import pcre;
import io;
io.println(pcre.find("(?<=\\$)\\d+", "price is $42 plus tax") ?? "null");
`, "42\n")
}

// TestParityPCREBackref verifies backreferences in the pattern,
// which RE2 does not support.
func TestParityPCREBackref(t *testing.T) {
	runParity(t, `import pcre;
import io;
io.println(pcre.test("(\\w+)\\s+\\1", "hello hello") as string);
io.println(pcre.test("(\\w+)\\s+\\1", "hello world") as string);
`, "true\nfalse\n")
}

// TestParityPCREFlags verifies the imsx modifier letters.
func TestParityPCREFlags(t *testing.T) {
	runParity(t, `import pcre;
import io;
io.println(pcre.test("hello", "HELLO", "i") as string);
io.println(pcre.test("hello", "HELLO", "") as string);
io.println(pcre.find("a.b", "a\nb", "s") ?? "null");
io.println(pcre.find("^bar", "foo\nbar\nbaz", "m") ?? "null");
`, "true\nfalse\na\nb\nbar\n")
}

// TestParityPCREReplaceBackref verifies $1 / $2 backref expansion
// in replacements.
func TestParityPCREReplaceBackref(t *testing.T) {
	runParity(t, `import pcre;
import io;
io.println(pcre.replace("(\\w+) (\\w+)", "$2 $1", "hello world"));
io.println(pcre.replace("(\\d+)", "[$1]", "x=42 y=99"));
`, "world hello\nx=[42] y=[99]\n")
}

// TestParityPCREMatchDict verifies the {text, groups, named}
// shape matches the re.match contract.
func TestParityPCREMatchDict(t *testing.T) {
	runParity(t, `import pcre;
import io;
let m = pcre.match("(?P<word>[a-z]+)(?P<num>[0-9]+)", "abc123");
io.println(m["text"] as string);
io.println(m["groups"][1] as string);
io.println(m["groups"][2] as string);
io.println(m["named"]["word"] as string);
io.println(m["named"]["num"] as string);
`, "abc123\nabc\n123\nabc\n123\n")
}

// TestParityPCRESplitAndQuote verifies pcre.split and pcre.quote.
func TestParityPCRESplitAndQuote(t *testing.T) {
	runParity(t, `import pcre;
import io;
let parts = pcre.split("\\s*,\\s*", "a, b ,c,  d");
for (let i = 0; i < parts.length(); i = i + 1) {
    io.println(parts[i] as string);
}
io.println(pcre.quote("a.b+c"));
`, "a\nb\nc\nd\na\\.b\\+c\n")
}

// TestParityPCREBadFlag verifies unknown flag letters error out
// on both backends rather than getting silently dropped.
func TestParityPCREBadFlag(t *testing.T) {
	runErrorParity(t, `import pcre;
pcre.test("foo", "foobar", "q");
`, "unknown pcre flag")
}

// TestParityStringRelational verifies lexicographic string
// comparison via <, <=, >, >= works on both backends. Previously
// the VM dispatched relational ops only through NumericCompare,
// rejecting strings with "comparison expects compatible numeric
// operands"; the evaluator's compareValues handled them via
// runtime.String. Now both share native.NumericCompare which
// covers strings too.
func TestParityStringRelational(t *testing.T) {
	runParity(t, `import io;
io.println(("apple" < "banana") as string);
io.println(("apple" <= "apple") as string);
io.println(("zebra" > "apple") as string);
io.println(("zebra" >= "zebra") as string);
io.println(("apple" > "zebra") as string);
let ch = "5";
io.println((ch >= "0" && ch <= "9") as string);
let letter = "m";
io.println((letter >= "a" && letter <= "z") as string);
`, "true\ntrue\ntrue\ntrue\nfalse\ntrue\ntrue\n")
}

// TestParityTestMock verifies test.mock works identically on
// both engines: a patched stdlib function dispatches to the
// user-supplied callable rather than the real native.
// Uses runParityWithStdlib because test.mock dispatches through
// the evaluator's stateful native bridge.
func TestParityTestMock(t *testing.T) {
	runParityWithStdlib(t, `import test;
import crypt;
import io;
test.mock("crypt", {"sha256": func(string s): string { return "mocked-" + s; }});
io.println(crypt.sha256("hello"));
test.restoreAll();
let real = crypt.sha256("hello");
/* Real sha256 of "hello" is well-known; we just confirm it
 * is no longer "mocked-hello" after restoreAll. */
io.println((real != "mocked-hello") as string);
`, "mocked-hello\ntrue\n")
}

// TestParityCryptS3WalkthroughCertAndJwe walks the three S3
// follow-up surfaces (signCertificate, jweEncrypt+Decrypt for
// dir, jweEncrypt+Decrypt for RSA-OAEP-256) on both backends so
// any future divergence in the native dispatch shape is caught.
func TestParityCryptS3WalkthroughCertAndJwe(t *testing.T) {
	runParity(t, `import io;
import crypt;
import bytes;

let caKey = crypt.generateEcKey("P-256");
let caBundle = crypt.generateSelfSignedCert({
    "subject": {"commonName": "ParityCA"},
    "key": caKey
});
let leafKey = crypt.generateEcKey("P-256");
let csr = crypt.generateCsr({
    "key": leafKey,
    "subject": {"commonName": "parity.leaf"}
});
let signed = crypt.signCertificate({
    "csr": csr,
    "caCert": caBundle["cert"],
    "caKey": caKey
});
let parsed = crypt.parseCert(signed);
io.println(parsed["issuer"]["commonName"] as string);
io.println(parsed["subject"]["commonName"] as string);

let cek = bytes.fromHex(crypt.randomHex(32));
let dirTok = crypt.jweEncrypt("dir-payload", cek, {"alg": "dir", "enc": "A256GCM"});
io.println(dirTok.split(".").length as string);
io.println(bytes.toString(crypt.jweDecrypt(dirTok, cek)));

let rsaKey = crypt.generateRsaKey(2048);
let rsaPub = crypt.publicKey(rsaKey);
let rsaTok = crypt.jweEncrypt("rsa-payload", rsaPub, {"alg": "RSA-OAEP-256", "enc": "A256GCM"});
io.println(bytes.toString(crypt.jweDecrypt(rsaTok, rsaKey)));
`, "ParityCA\nparity.leaf\n5\ndir-payload\nrsa-payload\n")
}

// TestParityUnionTypeAcceptsAndRejects walks T | U | V at
// parameter and return positions on both backends. Each accept
// path returns the matching branch's value; the reject path
// goes through xs[0] (statically opaque element type) and is
// caught by a user-level try/catch - confirming the VM throws
// param-validation errors as catchable RuntimeError (matching
// the evaluator) rather than fatally aborting.
func TestParityUnionTypeAcceptsAndRejects(t *testing.T) {
	runParity(t, `import io;

func tag(int | string | bool v): string {
    if (v instanceof int)    { return "int "  + (v as string); }
    if (v instanceof string) { return "str "  + (v as string); }
    return "bool " + (v as string);
}

func pickInt(): int | string { return 1; }
func pickStr(): int | string { return "x"; }

io.println(tag(42));
io.println(tag("ada"));
io.println(tag(true));
io.println(pickInt() as string);
io.println(pickStr() as string);

let xs = [1.5];
try {
    io.println(tag(xs[0]));
} catch (RuntimeError e) {
    io.println("rejected");
}
`, "int 42\nstr ada\nbool true\n1\nx\nrejected\n")
}

// TestParityMatchListPatterns exercises tuple-shape patterns in
// match: structural shape check, per-element type guard, _
// wildcard, length-mismatch fall-through, and the
// non-list-value case. Both backends must produce identical
// output for each branch.
func TestParityMatchListPatterns(t *testing.T) {
	runParity(t, `import io;

let pair = [3, 7];
io.println(match (pair) {
    case [int x, int y] if (x > y) => "first";
    case [int x, int y] if (x == y) => "tie";
    case [int x, int y] => "second";
    default => "n/a";
});

let mixed = ["ada", 37];
io.println(match (mixed) {
    case [int a, int b] => "two ints";
    case [string s, int n] => s + "=" + (n as string);
    default => "other";
});

let triple = [1, 2, 3];
io.println(match (triple) {
    case [int a, int b] => "two";
    case [int a, _, int c] => "wild-mid:" + ((a + c) as string);
    default => "other";
});

let notAList = "scalar";
io.println(match (notAList) {
    case [int a, int b] => "list";
    case string s => "string:" + s;
    default => "other";
});

let empty = [];
io.println(match (empty) {
    case [] => "empty";
    case [int a] => "one";
    default => "other";
});
`, "second\nada=37\nwild-mid:4\nstring:scalar\nempty\n")
}

// TestParityJwtUnifiedSurface exercises the alg-dispatching
// crypt.jwtSign / crypt.jwtVerify pair across HMAC and asymmetric
// algorithms; the assertion that both backends produce a token
// that the other can verify proves the dispatch and key handling
// line up. The allowedAlgs guard against alg-confusion is also
// hit so a divergence in the dispatch table is caught.
func TestParityJwtUnifiedSurface(t *testing.T) {
	runParity(t, `import crypt;
import io;

let hs = crypt.jwtSign({"u": "ada"}, "shh", {"alg": "HS512"});
let claims = crypt.jwtVerify(hs, "shh");
io.println(claims["u"] as string);

let priv = crypt.generateEcKey("P-256");
let pub = crypt.publicKey(priv);
let es = crypt.jwtSign({"u": "ec"}, priv, {"alg": "ES256"});
io.println(crypt.jwtVerify(es, pub)["u"] as string);

let blocked = crypt.jwtVerify(hs, "shh", {"allowedAlgs": ["RS256"]});
if (blocked == null) {
    io.println("blocked");
} else {
    io.println("leaked");
}

let unsigned = crypt.jwtSign({"u": "n"}, "", {"alg": "none", "allowedAlgs": ["none"]});
let defaultVerify = crypt.jwtVerify(unsigned, "");
if (defaultVerify == null) {
    io.println("none-default-blocked");
} else {
    io.println("none-default-leaked");
}
let optedIn = crypt.jwtVerify(unsigned, "", {"allowedAlgs": ["none"]});
io.println(optedIn["u"] as string);
`, "ada\nec\nblocked\nnone-default-blocked\nn\n")
}

// TestParityArchiveZipRoundTrip exercises the archive.zip{Read,Write}
// pair on both backends with the same source: write a two-entry
// archive, read it back, print the names and decoded text. The
// expected output is identical regardless of which engine ran it.
func TestParityArchiveZipRoundTrip(t *testing.T) {
	runParity(t, `import archive;
import bytes;
import io;
let raw = archive.zipWrite([
    {"name": "a.txt", "data": "alpha"},
    {"name": "b.txt", "data": "beta"}
]);
let entries = archive.zipRead(raw);
io.println(entries.length as string);
io.println(entries[0]["name"] as string);
io.println(bytes.toString(entries[0]["data"] as bytes));
io.println(entries[1]["name"] as string);
io.println(bytes.toString(entries[1]["data"] as bytes));
`, "2\na.txt\nalpha\nb.txt\nbeta\n")
}

// TestParityArchiveTarGzRoundTrip covers the gzip-wrapped tar
// helpers; tar writers sort by name for determinism so the entry
// order is stable across backends.
func TestParityArchiveTarGzRoundTrip(t *testing.T) {
	runParity(t, `import archive;
import bytes;
import io;
let raw = archive.tarGzWrite([
    {"name": "second", "data": "two"},
    {"name": "first", "data": "one"}
]);
let entries = archive.tarGzRead(raw);
io.println(entries[0]["name"] as string);
io.println(bytes.toString(entries[0]["data"] as bytes));
io.println(entries[1]["name"] as string);
io.println(bytes.toString(entries[1]["data"] as bytes));
`, "first\none\nsecond\ntwo\n")
}

// TestParityHmacSha256Bytes verifies the raw-bytes HMAC variant
// produces the AWS sigv4 reference kDate value when fed the
// documented test inputs (AWS docs sample for
// "AWS4SECRET" + "20150830" -> kDate is a known hex digest).
// This guards against accidental signature drift between the
// evaluator and the VM and confirms the Bytes-typed return.
func TestParityHmacSha256Bytes(t *testing.T) {
	// Reference: AWS Signature V4 "Examples of How to Derive a
	// Signing Key" - kDate when secretAccessKey is
	// "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY" and dateStamp is
	// "20150830". Expected kDate hex:
	//   0138c7a6cbd60aa727b2f653a522567439dfb9f3e72b21f9b25941a42f04a7cd
	runParity(t, `import crypt;
import bytes;
import io;
let kDate = crypt.hmacSha256Bytes("AWS4wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20150830");
io.println(bytes.toHex(kDate));
`, "0138c7a6cbd60aa727b2f653a522567439dfb9f3e72b21f9b25941a42f04a7cd\n")
}

// Regression: cross-module facade `class X extends mod.X` failed in the
// evaluator at construction because parent() routed through
// applyOverloadedFunction with a label matching this.Class.Name.
func TestParityFacadeSubclassSameName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.gb"), []byte(`module base;
export class Tenant {
    string id;
    string label;
    func Tenant(string id, string label = "") {
        this.id = id;
        this.label = label;
    }
}
`), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "facade.gb"), []byte(`module facade;
import base as basemod;
export class Tenant extends basemod.Tenant {
    func Tenant(string id, string label = "") {
        parent(id, label);
    }
}
`), 0o644); err != nil {
		t.Fatalf("write facade: %v", err)
	}
	source := `import io;
import facade;
let t = facade.Tenant("acme");
let u = facade.Tenant("beta", "Beta Co");
io.println(t.id);
io.println(u.label);
`
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("evaluator: %v", err)
	}
	if evOut.String() != "acme\nBeta Co\n" {
		t.Fatalf("evaluator output: %q", evOut.String())
	}
	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var vmOut bytes.Buffer
	stateful := evaluator.NewWithArgsAndModulePaths(&vmOut, nil, []string{dir})
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: %v", err)
	}
	if vmOut.String() != "acme\nBeta Co\n" {
		t.Fatalf("vm output: %q", vmOut.String())
	}
}

// Regression: both backends must reject `import X; func X(...)`.
func TestParityImportNameCollisionWithFunction(t *testing.T) {
	source := `import secrets;
func secrets(): string { return "x"; }
`
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	_, vmErr := bytecode.Compile(program, []byte(source), "parity")
	if vmErr == nil {
		t.Fatalf("vm compile: expected name-collision error")
	}
	if !strings.Contains(vmErr.Error(), "already declared") {
		t.Fatalf("vm compile error should mention already-declared: %q", vmErr.Error())
	}
	var evOut bytes.Buffer
	ev := evaluator.NewWithArgs(&evOut, nil)
	_, evErr := ev.Eval(program)
	if evErr == nil {
		t.Fatalf("evaluator: expected name-collision error")
	}
	if !strings.Contains(evErr.Error(), "already declared") {
		t.Fatalf("evaluator error should mention already-declared: %q", evErr.Error())
	}
}

// Both backends must agree on top-level redeclaration: only func+func
// (overloads), idempotent re-import of one module, and re-bind after `del`
// are allowed; every other same-name declaration is rejected. Mirrors the
// evaluator's Environment.Define/DefineFunction. Reject cases are tested at
// the imported-module level (the evaluator gates a main program's value
// redeclarations through the semantic analyzer, but module loading on both
// backends goes straight to Define/the compiler - the surface gebweb hit).
func TestParityGlobalRedeclarationRule(t *testing.T) {
	reject := map[string]string{
		"var_var":               "let x = 1;\nlet x = 2;\n",
		"const_func":            "const C = 5;\nexport func C(): int { return 1; }\n",
		"enum_class":            "export enum E { A }\nexport class E {}\n",
		"enum_enum":             "export enum E { A }\nexport enum E { B }\n",
		"interface_func":        "export interface I { func a(): int; }\nexport func I(): int { return 1; }\n",
		"import_class":          "import secrets;\nexport class secrets {}\n",
		"import_var":            "import secrets;\nlet secrets = 5;\n",
		"fromimport_var":        "from math import abs;\nlet abs = 5;\n",
		"fromimport_func":       "from math import abs;\nexport func abs(): int { return 1; }\n",
		"fromimport_func_first": "export func abs(): int { return 1; }\nfrom math import abs;\n",
	}
	for name, body := range reject {
		t.Run("reject_"+name, func(t *testing.T) {
			assertImportedModuleRejected(t, body)
		})
	}
	accept := map[string]struct{ src, want string }{
		"func_overload":         {"import io;\nfunc f(int x): int { return x; }\nfunc f(string s): int { return s.length(); }\nio.println(f(3));\nio.println(f(\"ab\"));\n", "3\n2\n"},
		"idempotent_reimport":   {"import io;\nimport math;\nimport math;\nio.println(math.abs(-2));\n", "2\n"},
		"del_then_rebind":       {"import io;\nlet x = 1;\ndel x;\nlet x = 2;\nio.println(x);\n", "2\n"},
		"fromimport_used":       {"import io;\nfrom math import abs;\nio.println(abs(-3));\n", "3\n"},
		"fromimport_idempotent": {"import io;\nfrom math import abs;\nfrom math import abs;\nio.println(abs(-4));\n", "4\n"},
	}
	for name, tc := range accept {
		t.Run("accept_"+name, func(t *testing.T) {
			runParity(t, tc.src, tc.want)
		})
	}
}

// A native module function can be referenced as a first-class value (with
// the module imported) and passed as a callback, on both backends.
func TestParityNativeModuleFnAsValue(t *testing.T) {
	runParity(t, `import io;
import math;
let g = math.abs;
io.println(g(-7));
io.println([-3, 1, -2].map(math.abs));
`, "7\n[3, 1, 2]\n")
}

// Grapheme cluster methods segment by user-perceived character (UAX #29):
// an emoji ZWJ sequence and a base+combining-mark each count as one, while
// length()/codePoints() stay code-point based. Identical on both backends.
func TestParityGraphemes(t *testing.T) {
	runParity(t, `import io;
let family = "\u{1F468}\u{200D}\u{1F469}\u{200D}\u{1F467}";
io.println(family.length());
io.println(family.graphemeLength());
io.println("e\u{301}llo".graphemeLength());
io.println("abcd".truncateGraphemes(2));
io.println("xy".graphemes());
`, "5\n1\n4\nab\n[\"x\", \"y\"]\n")
}

// Datetime value-method ergonomics: unix accessors, part accessors, ISO
// weekday, comparisons, duration arithmetic, friendly format/parse layouts,
// and zone offset - identical on both backends.
func TestParityDateTimeErgonomics(t *testing.T) {
	runParity(t, `import io;
import datetime;
let a = datetime.Instant(1700000000);
let b = datetime.Instant(1700000100);
io.println(a.year());
io.println(a.month());
io.println(a.day());
io.println(a.weekday());
io.println(a.dayOfYear());
io.println(a.isWeekend());
io.println(a.toUnixMillis());
io.println(a.isBefore(b));
io.println(a.equals(a));
io.println(a.diff(b).inSeconds());
io.println(a.sub(datetime.Duration(100)).toUnix());
io.println(datetime.Duration(-90).abs().negate().seconds());
io.println(datetime.Duration(60).add(datetime.Duration(30)).inMillis());
io.println(a.format("%Y-%m-%d"));
io.println(a.format("datetime"));
io.println(a.formatHTTP());
io.println(datetime.parse("2023-11-14", "%Y-%m-%d"));
io.println(datetime.Zone("UTC").offsetAt(a));
`, "2023\n11\n14\n2\n318\nfalse\n1700000000000\ntrue\ntrue\n100\n1699999900\n-90\n90000\n2023-11-14\n2023-11-14 22:13:20\nTue, 14 Nov 2023 22:13:20 GMT\n1699920000\n0\n")
}

// Sorting/searching ergonomics: dual-mode sort callbacks, sort(string.compare),
// sortBy descending, binarySearchBy, type statics as values, and slicing -
// identical on both backends.
func TestParitySortingAndSearching(t *testing.T) {
	runParity(t, `import io;
io.println([3,1,2].sort(func(int a, int b): bool { return a < b; }));
io.println([3,1,2].sort(func(int a, int b): int { return b - a; }));
io.println(["banana","apple","cherry"].sort(string.compare));
io.println([{"n":3},{"n":1},{"n":2}].sortBy(func(dict<string,any> x): any { return x["n"]; }, true));
io.println([1,3,5,7].binarySearch(5));
io.println([{"n":1},{"n":3},{"n":5}].binarySearchBy(func(dict<string,any> x): any { return x["n"]; }, 3));
io.println([1,2,3,4,5][::-1]);
io.println([1,2,3,4,5][::2]);
let cmp = string.compare;
io.println(cmp("a","b"));
`, "[1, 2, 3]\n[3, 2, 1]\n[\"apple\", \"banana\", \"cherry\"]\n[{\"n\": 3}, {\"n\": 2}, {\"n\": 1}]\n2\n1\n[5, 4, 3, 2, 1]\n[1, 3, 5]\n-1\n")
}

// Escape sequences (\n, \t, \u{...}) are decoded inside interpolated
// strings, identically on both backends.
func TestParityInterpolatedStringEscapes(t *testing.T) {
	runParity(t, `import io;
let name = "world";
io.println("hi\t${name}\nbye \u{1F600}");
`, "hi\tworld\nbye \U0001F600\n")
}

// An invalid \u{...} escape is rejected at parse time (shared lexer/parser,
// so both backends fail identically) in plain and interpolated strings.
func TestParityInvalidUnicodeEscapeRejected(t *testing.T) {
	for _, src := range []string{
		"import io;\nio.println(\"\\u{110000}\");\n",
		"import io;\nio.println(\"\\u{D800}\");\n",
		"import io;\nlet x = 1;\nio.println(\"v \\u{} ${x}\");\n",
	} {
		if _, _, _, _, compileErr := fuzzRunBoth(src); compileErr == nil {
			t.Fatalf("expected invalid unicode escape to be rejected:\n%s", src)
		}
	}
}

// Builtin type static methods (bytes.fromString, string.fromCodePoint, ...)
// resolve without an import on both backends.
func TestParityTypeStaticsWithoutImport(t *testing.T) {
	runParity(t, `import io;
io.println(bytes.fromString("a") as string);
io.println(bytes.fromList([97, 98, 99]) as string);
io.println(string.fromCodePoint(65));
io.println(string.fromCodePoints([72, 105]));
`, "a\nabc\nA\nHi\n")
}

// Type-conversion surface: string.codePoints, bytes.toList, bytes.fromList
// must produce identical results on both backends.
func TestParityTypeConversions(t *testing.T) {
	runParity(t, `import io;
import bytes;
io.println("abc".codePoints());
io.println(("abc" as bytes).toList());
io.println(bytes.fromList([97, 98, 99]) as string);
io.println(bytes.fromList(("hi" as bytes).toList()) as string);
`, "[97, 98, 99]\n[97, 98, 99]\nabc\nhi\n")
}

// del operates on variables; deleting a class/func/enum/interface
// declaration is rejected identically on both backends. (Variable and
// instance del, and del+rebind, are covered by TestParityDelClearsBinding
// and TestParityDelFiresDestructor.)
func TestParityDelRejectsDeclarations(t *testing.T) {
	cases := map[string]string{
		"class":     "export class C {}\ndel C;\n",
		"func":      "export func f(): int { return 1; }\ndel f;\n",
		"enum":      "export enum E { A }\ndel E;\n",
		"interface": "export interface I { func a(): int; }\ndel I;\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) { assertImportedModuleRejected(t, body) })
	}
}

// assertImportedModuleRejected writes moduleBody as module `lib`, imports it
// from a main program, and asserts both backends fail to load it.
func assertImportedModuleRejected(t *testing.T, moduleBody string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.gb"), []byte("module lib;\n"+moduleBody), 0o644); err != nil {
		t.Fatalf("write lib: %v", err)
	}
	source := "import io;\nimport lib;\nio.println(\"loaded\");\n"
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err == nil {
		t.Fatalf("evaluator accepted a colliding module: %q", evOut.String())
	}
	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("main compile: %v", err)
	}
	var vmOut bytes.Buffer
	stateful := evaluator.NewWithArgsAndModulePaths(&vmOut, nil, []string{dir})
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	if err := vm.Run(); err == nil {
		t.Fatalf("vm accepted a colliding module: %q", vmOut.String())
	}
}
