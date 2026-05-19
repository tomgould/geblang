package bytecode_test

// Parity tests run each snippet through both the evaluator and the VM and
// assert that the printed output matches.  They pin the intersection of
// features that both execution paths are expected to support identically.

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/parser"
	"geblang/internal/runtime"
	"geblang/internal/semantic"
)

// stdlibModuleLoader is a minimal bytecode-side module loader used by
// parity tests for source-distributed stdlib modules (time.scheduler,
// async.rate, etc.). It resolves canonical module paths via the standard
// resolver (which walks ancestors to find stdlib/), compiles each
// imported source module to bytecode on demand, and caches the result
// inside the loader struct.
type stdlibModuleLoader struct {
	stdout     io.Writer
	stateful   bytecode.StatefulNativeCaller
	modulePaths []string
	modules    map[string]*runtime.Module
	chunks     map[string]bytecode.Chunk
	globals    map[string][]runtime.Value
	decorators map[string]bytecode.FunctionDecoratorState
	loading    map[string]bool
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

func (l *stdlibModuleLoader) LoadModule(canonical, alias string) (*runtime.Module, error) {
	if module, ok := l.modules[canonical]; ok {
		return module, nil
	}
	resolver := modules.NewResolver(l.modulePaths)
	path, err := resolver.Resolve(canonical)
	if err != nil {
		return nil, err
	}
	if l.loading[path] {
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
	module := &runtime.Module{Name: alias, Exports: exports}
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

func (l *stdlibModuleLoader) newSubVM(moduleName string) *bytecode.VM {
	chunk := l.chunks[moduleName]
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(moduleName)
	vm.SetModulePaths(l.modulePaths)
	if l.stateful != nil {
		vm.SetStatefulNativeCaller(l.stateful)
	}
	vm.RestoreGlobals(l.globals[moduleName])
	vm.RestoreFunctionDecoratorState(l.decorators[moduleName])
	return vm
}

func (l *stdlibModuleLoader) CallModuleFunction(function runtime.BytecodeFunction, args []runtime.Value) (runtime.Value, error) {
	if _, ok := l.chunks[function.Module]; !ok {
		return nil, fmt.Errorf("module %s is not loaded", function.Module)
	}
	return l.newSubVM(function.Module).CallFunction(function.Index, args)
}

func (l *stdlibModuleLoader) CallModuleClosure(closure runtime.BytecodeClosure, args []runtime.Value) (runtime.Value, error) {
	if _, ok := l.chunks[closure.Module]; !ok {
		return nil, fmt.Errorf("module %s is not loaded", closure.Module)
	}
	return l.newSubVM(closure.Module).CallClosure(closure, args)
}

func (l *stdlibModuleLoader) ConstructModuleClass(class runtime.BytecodeClass, args []runtime.Value) (runtime.Value, error) {
	if _, ok := l.chunks[class.Module]; !ok {
		return nil, fmt.Errorf("module %s is not loaded", class.Module)
	}
	return l.newSubVM(class.Module).ConstructClass(class.Index, args)
}

func (l *stdlibModuleLoader) CallModuleStaticMethod(class runtime.BytecodeClass, methodName string, args []runtime.Value) (runtime.Value, error) {
	if _, ok := l.chunks[class.Module]; !ok {
		return nil, fmt.Errorf("module %s is not loaded", class.Module)
	}
	return l.newSubVM(class.Module).CallStaticMethod(class.Index, methodName, args)
}

func (l *stdlibModuleLoader) CallModuleMethod(module string, className string, methodName string, instance *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if _, ok := l.chunks[module]; !ok {
		return nil, fmt.Errorf("module %s is not loaded", module)
	}
	return l.newSubVM(module).CallInstanceMethod(instance, methodName, args)
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
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetStatefulNativeCaller(stateful)
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
io.println(reflect.fields(Controller)[0]);
io.println(reflect.fields(Controller)[1]);
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
`, "[banana]\n[apple, cherry, avocado]\n")
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
`, "[a, b, c, d]\n")
}

func TestParityCollectionsBFSChain(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b"], "b": ["c"], "c": []};
io.println(collections.bfs(g, "a"));
io.println(collections.bfs(g, "c"));
`, "[a, b, c]\n[c]\n")
}

func TestParityCollectionsDFS(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b", "c"], "b": ["d"], "c": ["d"], "d": []};
let r = collections.dfs(g, "a");
io.println(r);
`, "[a, b, d, c]\n")
}

func TestParityCollectionsDFSChain(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b"], "b": ["c"], "c": []};
io.println(collections.dfs(g, "a"));
`, "[a, b, c]\n")
}

func TestParityCollectionsTopologicalSort(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b", "c"], "b": ["d"], "c": ["d"], "d": []};
let r = collections.topologicalSort(g);
io.println(r);
`, "[a, b, c, d]\n")
}

func TestParityCollectionsTopologicalSortChain(t *testing.T) {
	runParity(t, `import io;
import collections;
let g = {"a": ["b"], "b": ["c"], "c": []};
io.println(collections.topologicalSort(g));
`, "[a, b, c]\n")
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
`, "[a, b, d]\n[a]\n")
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
`, "[a, b, c]\n")
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

	// Wrong element type in list declaration — error names the offending element.
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
