package bytecode_test

// Parity tests run each snippet through both the evaluator and the VM and
// assert that the printed output matches.  They pin the intersection of
// features that both execution paths are expected to support identically.

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/native"
	"geblang/internal/parser"
)

// stdlibModuleLoader is a minimal bytecode-side module loader used by
// parity tests for source-distributed stdlib modules (time.scheduler,
// async.rate, etc.). It resolves canonical module paths via the standard
// resolver (which walks ancestors to find stdlib/), compiles each
// imported source module to bytecode on demand, and caches the result
// inside the loader struct.

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

func TestParityReflectFunctionResolvesNativeModuleFunctions(t *testing.T) {
	runParity(t, `import io;
import reflect;
import math;
import math as m2;
let f = reflect.function("math.sqrt");
io.println(f != null);
io.println(f(16.0));
let g = reflect.function(math.abs);
io.println(g != null);
io.println(g(-3));
let h = reflect.function("m2.sqrt");
io.println(h != null);
io.println(reflect.function("math.noSuchFn") == null);
io.println("${reflect.parameters(f)}");
io.println("${reflect.location(f)}");
io.println("${reflect.returnType(f)}");
`, "true\n4\ntrue\n3\ntrue\ntrue\n[]\nnull\nvoid\n")
}

// Bare reflect.* without `import reflect` is ambient on both backends,
// for ALL reflect functions (the evaluator previously special-cased only
// function/class/module/classes).
func TestParityAmbientReflectWithoutImport(t *testing.T) {
	runParity(t, `import io;
import math;
let f = reflect.function("math.sqrt");
io.println(f != null);
io.println("${reflect.parameters(f)}");
io.println(reflect.typeOf(3));
`, "true\n[]\nint\n")
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

// TestParityReflectNestedClassFromInstance pins the fix for an eval/VM
// divergence: reflect.fields / methods / className / decorators over a
// nested reflect.class(<instance>) (a non-literal argument) errored on
// the VM ("name must be a string literal in bytecode") while the
// evaluator resolved it at runtime. The nested form must now behave the
// same as the two-step form on both backends.
func TestParityReflectNestedClassFromInstance(t *testing.T) {
	runParity(t, `import io;
import reflect;

class Doc {
    @Assert.notBlank string title;
    func Doc(string t) { this.title = t; }
    func render(): string { return this.title; }
}

let d = Doc("");
io.println(reflect.fields(reflect.class(d))[0]["name"]);
io.println(reflect.methods(reflect.class(d))[0]);
io.println(reflect.className(reflect.class(d)));
let decs = reflect.fields(reflect.class(d))[0]["decorators"] as list<any>;
io.println((decs[0] as dict<string, any>)["name"]);
`, "title\nrender\nDoc\nAssert.notBlank\n")
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
