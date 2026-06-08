package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runBothBackends runs mainPath on VM and evaluator and returns both outputs.
// It does NOT fail the test on error; callers decide what to assert.
func runBothBackends(t *testing.T, bin, mainPath string) (vm string, eval string, vmErr error, evalErr error) {
	t.Helper()
	modes := [][]string{{mainPath}, {"--disable-vm", mainPath}}
	for i, mode := range modes {
		cmd := exec.Command(bin, mode...)
		out, err := cmd.CombinedOutput()
		if i == 0 {
			vm, vmErr = string(out), err
		} else {
			eval, evalErr = string(out), err
		}
	}
	return
}

// assertParitySuccess asserts both backends succeed and produce the same output.
func assertParitySuccess(t *testing.T, label, vm, eval string, vmErr, evalErr error) {
	t.Helper()
	if vmErr != nil {
		t.Fatalf("%s VM failed: %v\n%s", label, vmErr, vm)
	}
	if evalErr != nil {
		t.Fatalf("%s evaluator failed: %v\n%s", label, evalErr, eval)
	}
	if vm != eval {
		t.Fatalf("%s backend output differs:\nVM:   %q\neval: %q", label, vm, eval)
	}
}

// probe1: qualified inheritance template method (base.describe() calls overridden this.sound())
func TestCrossModuleParityQualifiedTemplateMethod(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(
		"module base;\n"+
			"export class Base {\n"+
			"    func sound(): string { return \"base-sound\"; }\n"+
			"    func describe(): string { return \"says:\" + this.sound(); }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import base;\n"+
			"import io;\n"+
			"class Dog extends base.Base {\n"+
			"    func sound(): string { return \"woof\"; }\n"+
			"}\n"+
			"io.println(Dog().describe());\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "qualified template method", vm, eval, vmErr, evalErr)
	if !strings.Contains(vm, "says:woof") {
		t.Fatalf("expected 'says:woof', got: %q", vm)
	}
}

// probe2: from-import inheritance, inherited+overridden result
func TestCrossModuleParityFromImportInheritance(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(
		"module base;\n"+
			"export class Base {\n"+
			"    func value(): string { return \"base\"; }\n"+
			"    func result(): string { return \"result:\" + this.value(); }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"from base import Base;\n"+
			"import io;\n"+
			"class Sub extends Base {\n"+
			"    func value(): string { return \"sub\"; }\n"+
			"}\n"+
			"io.println(Sub().result());\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "from-import inheritance", vm, eval, vmErr, evalErr)
	if !strings.Contains(vm, "result:sub") {
		t.Fatalf("expected 'result:sub', got: %q", vm)
	}
}

// probe3: multi-level chain across modules (C extends B, B extends mod.A)
func TestCrossModuleParityMultiLevelChain(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "mod.gb"), []byte(
		"module mod;\n"+
			"export class A {\n"+
			"    func label(): string { return \"A\"; }\n"+
			"    func chain(): string { return \"chain:\" + this.label(); }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import mod;\n"+
			"import io;\n"+
			"class B extends mod.A {\n"+
			"    func label(): string { return \"B\"; }\n"+
			"}\n"+
			"class C extends B {\n"+
			"    func label(): string { return \"C\"; }\n"+
			"}\n"+
			"let c = C();\n"+
			"io.println(c.label());\n"+
			"io.println(c.chain());\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "multi-level chain", vm, eval, vmErr, evalErr)
	if !strings.Contains(vm, "C") || !strings.Contains(vm, "chain:C") {
		t.Fatalf("expected 'C' and 'chain:C', got: %q", vm)
	}
}

// probe4: generic base class across module (Box<string> and Box<int>)
func TestCrossModuleParityGenericBase(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "mod.gb"), []byte(
		"module mod;\n"+
			"export class Box<T> {\n"+
			"    T item;\n"+
			"    func Box(T v) { this.item = v; }\n"+
			"    func get(): T { return this.item; }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import mod;\n"+
			"import io;\n"+
			"let bs = mod.Box(\"hello\");\n"+
			"let bi = mod.Box(42);\n"+
			"io.println(bs.get());\n"+
			"io.println(bi.get());\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "generic base", vm, eval, vmErr, evalErr)
	if !strings.Contains(vm, "hello") || !strings.Contains(vm, "42") {
		t.Fatalf("expected 'hello' and '42', got: %q", vm)
	}
}

// probe5: cross-module interface with default method; instanceof check
func TestCrossModuleParityInterfaceDefault(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "iface.gb"), []byte(
		"module iface;\n"+
			"export interface Greeter {\n"+
			"    func greet(): string;\n"+
			"    func tagged(): string { return \"[\" + this.greet() + \"]\"; }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import iface;\n"+
			"import io;\n"+
			"class Hello implements iface.Greeter {\n"+
			"    func Hello() {}\n"+
			"    func greet(): string { return \"hello\"; }\n"+
			"}\n"+
			"let h = Hello();\n"+
			"io.println(h.greet());\n"+
			"io.println(h.tagged());\n"+
			"io.println(h instanceof iface.Greeter);\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "interface default method", vm, eval, vmErr, evalErr)
	if !strings.Contains(vm, "hello") || !strings.Contains(vm, "[hello]") {
		t.Fatalf("expected 'hello' and '[hello]', got: %q", vm)
	}
	if !strings.Contains(vm, "true") {
		t.Fatalf("expected instanceof to be true, got: %q", vm)
	}
}

// probe6: cross-module inherited field read and mutation
func TestCrossModuleParityInheritedField(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(
		"module base;\n"+
			"export class Base {\n"+
			"    string name;\n"+
			"    func Base(string n) { this.name = n; }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import base;\n"+
			"import io;\n"+
			"class Child extends base.Base {\n"+
			"    func Child(string n) { parent(n); }\n"+
			"}\n"+
			"let c = Child(\"before\");\n"+
			"io.println(c.name);\n"+
			"c.name = \"after\";\n"+
			"io.println(c.name);\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "inherited field", vm, eval, vmErr, evalErr)
	if !strings.Contains(vm, "before") || !strings.Contains(vm, "after") {
		t.Fatalf("expected 'before' and 'after', got: %q", vm)
	}
}

// probe7 (compile-error parity): wrong-type arg to an inherited cross-module method
// must fail identically on both backends with the same diagnostic line.
func TestCrossModuleParityCompileErrorInheritedMethod(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(
		"module base;\n"+
			"export class Base {\n"+
			"    func greet(string name): string { return \"hi \" + name; }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	/* passes int where string is expected in an inherited method call inside a method body */
	os.WriteFile(mainPath, []byte(
		"import base;\n"+
			"class Sub extends base.Base {\n"+
			"    func bad(): string { return this.greet(99); }\n"+
			"}\n"+
			"let x = 1;\n"), 0644)

	extract := func(b []byte) string {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "no matching overload") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}

	vmOut, vmErr := exec.Command(bin, mainPath).CombinedOutput()
	evOut, evErr := exec.Command(bin, "--disable-vm", mainPath).CombinedOutput()

	if vmErr == nil {
		t.Fatalf("VM: expected compile failure, got success:\n%s", vmOut)
	}
	if evErr == nil {
		t.Fatalf("evaluator: expected compile failure, got success:\n%s", evOut)
	}

	vmDiag := extract(vmOut)
	evDiag := extract(evOut)
	if vmDiag == "" {
		t.Fatalf("VM: no 'no matching overload' diagnostic in output:\n%s", vmOut)
	}
	if vmDiag != evDiag {
		t.Fatalf("compile-error diagnostic differs across backends:\nVM:   %q\neval: %q", vmDiag, evDiag)
	}
}

// probe8: explicit parent.method() resolving up to a cross-module grandparent
// (C > B > mod.A), where the local intermediate B lacks the method. Covers a
// plain method, a cross-module template method (this.x() stays polymorphic),
// and runtime overload selection across the boundary.
func TestCrossModuleParityExplicitParentMethodMultiLevel(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "mod.gb"), []byte(
		"module mod;\n"+
			"export class A {\n"+
			"    string kind;\n"+
			"    func A(string kind) { this.kind = kind; }\n"+
			"    func label(): string { return \"A:\" + this.kind; }\n"+
			"    func sound(): string { return \"a-sound\"; }\n"+
			"    func describe(): string { return this.kind + \":\" + this.sound(); }\n"+
			"    func tag(string s): string { return \"tag1:\" + s; }\n"+
			"    func tag(string s, string t): string { return \"tag2:\" + s + t; }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import mod;\n"+
			"import io;\n"+
			"class B extends mod.A {\n"+
			"    func B() { parent(\"b\"); }\n"+
			"}\n"+
			"class C extends B {\n"+
			"    func C() { parent(); }\n"+
			"    func sound(): string { return \"c-sound\"; }\n"+
			"    func label(): string { return \"C:\" + parent.label(); }\n"+
			"    func desc(): string { return parent.describe(); }\n"+
			"    func t1(): string { return parent.tag(\"x\"); }\n"+
			"    func t2(): string { return parent.tag(\"x\", \"y\"); }\n"+
			"}\n"+
			"let c = C();\n"+
			"io.println(c.label());\n"+
			"io.println(c.desc());\n"+
			"io.println(c.t1());\n"+
			"io.println(c.t2());\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "explicit parent.method() multi-level", vm, eval, vmErr, evalErr)
	for _, want := range []string{"C:A:b", "b:c-sound", "tag1:x", "tag2:xy"} {
		if !strings.Contains(vm, want) {
			t.Fatalf("expected %q in output, got: %q", want, vm)
		}
	}
}

// probe9: instanceof against a cross-module parent. The VM's same-chunk
// ParentIndex walk stops at the module boundary, so this returned false on
// the VM while the evaluator's runtime chain returned true. Covers 2-level
// (B > mod.A), multi-level (C > B > mod.A), the qualified target (mod.A), and
// a negative (an unrelated class is not an instance).
func TestCrossModuleParityInstanceof(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "mod.gb"), []byte(
		"module mod;\n"+
			"export class A {}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import mod;\n"+
			"import io;\n"+
			"class B extends mod.A {}\n"+
			"class C extends B {}\n"+
			"class X {}\n"+
			"io.println(B() instanceof mod.A);\n"+
			"io.println(C() instanceof mod.A);\n"+
			"io.println(X() instanceof mod.A);\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "cross-module instanceof", vm, eval, vmErr, evalErr)
	if strings.TrimSpace(vm) != "true\ntrue\nfalse" {
		t.Fatalf("expected 'true\\ntrue\\nfalse', got: %q", vm)
	}
}

// writeStampable writes a contracts module with an abstract stamp() and a
// default label() built on stamp(), the canonical interface-default shape.
func writeStampable(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "contracts.gb"), []byte(
		"module contracts;\n"+
			"export interface Stampable {\n"+
			"    func stamp(): string;\n"+
			"    func label(): string { return \"L:\" + this.stamp(); }\n"+
			"}\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

// probe10: a subclass of a cross-module-interface implementer inherits the
// interface default. The VM's interface-default lookup keyed only the
// implementer's own class index, so the subclass missed the fallback while the
// evaluator's runtime chain found it.
func TestCrossModuleParityInterfaceDefaultSubclass(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	writeStampable(t, dir)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import contracts;\n"+
			"import io;\n"+
			"class Coin implements contracts.Stampable {\n"+
			"    func stamp(): string { return \"m\"; }\n"+
			"}\n"+
			"class BigCoin extends Coin {}\n"+
			"io.println(Coin().label());\n"+
			"io.println(BigCoin().label());\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "cross-module interface default subclass", vm, eval, vmErr, evalErr)
	if strings.TrimSpace(vm) != "L:m\nL:m" {
		t.Fatalf("expected 'L:m\\nL:m', got: %q", vm)
	}
}

// probe11: a from-imported interface name resolves and dispatches its default,
// for the direct implementer and a subclass. The VM previously rejected the
// from-import ("Stampable is not exported") because exported interfaces were
// not surfaced as module exports.
func TestCrossModuleParityFromImportInterfaceDefault(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	writeStampable(t, dir)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"from contracts import Stampable;\n"+
			"import io;\n"+
			"class Coin implements Stampable {\n"+
			"    func stamp(): string { return \"c\"; }\n"+
			"}\n"+
			"class BigCoin extends Coin {}\n"+
			"io.println(Coin().label());\n"+
			"io.println(BigCoin().label());\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "from-import interface default", vm, eval, vmErr, evalErr)
	if strings.TrimSpace(vm) != "L:c\nL:c" {
		t.Fatalf("expected 'L:c\\nL:c', got: %q", vm)
	}
}

// probe12: the same cross-module interface defaults must dispatch in a built
// binary. The bundle runs module function bodies through a wrapper sub-VM that
// did not carry the interface-fallback tables, so a `geblang build` binary
// failed where `geblang run` succeeded.
func TestCrossModuleBuiltBinaryInterfaceDefault(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	writeStampable(t, dir)

	os.WriteFile(filepath.Join(dir, "app.gb"), []byte(
		"from contracts import Stampable;\n"+
			"import io;\n"+
			"class Coin implements Stampable {\n"+
			"    func stamp(): string { return \"c\"; }\n"+
			"}\n"+
			"class BigCoin extends Coin {}\n"+
			"export func main(list<string> args): void {\n"+
			"    io.println(Coin().label());\n"+
			"    io.println(BigCoin().label());\n"+
			"}\n"), 0644)

	out := filepath.Join(dir, "appbin")
	build := exec.Command(bin, "build", "--entry", "app", "--out", out)
	build.Dir = dir
	if bo, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, bo)
	}
	run := exec.Command(out)
	run.Dir = dir
	got, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("built binary failed: %v\n%s", err, got)
	}
	if strings.TrimSpace(string(got)) != "L:c\nL:c" {
		t.Fatalf("built binary: expected 'L:c\\nL:c', got: %q", got)
	}
}

// probe13: a static value declared on a cross-module ancestor, read through a
// local direct subclass (Mid) and grandchild (Leaf). The VM walked ParentIndex
// chunk-locally only and could not see the static on the foreign base.
func TestCrossModuleParityStaticValue(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "shapes.gb"), []byte(
		"module shapes;\n"+
			"export class Shape {\n"+
			"    static const KIND = \"shape-kind\";\n"+
			"    static const COUNT = 3;\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import shapes;\n"+
			"import io;\n"+
			"class Mid extends shapes.Shape {}\n"+
			"class Leaf extends Mid {}\n"+
			"io.println(Mid.KIND);\n"+
			"io.println(Leaf.KIND);\n"+
			"io.println(\"${Leaf.COUNT}\");\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "cross-module static value", vm, eval, vmErr, evalErr)
	if strings.TrimSpace(vm) != "shape-kind\nshape-kind\n3" {
		t.Fatalf("expected 'shape-kind\\nshape-kind\\n3', got: %q", vm)
	}
}

// probe14: a local subclass shadowing a cross-module static must win over the
// inherited one; the boundary hop only fires when the same-chunk walk misses.
func TestCrossModuleParityStaticValueOverride(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "shapes.gb"), []byte(
		"module shapes;\n"+
			"export class Shape {\n"+
			"    static const KIND = \"base\";\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import shapes;\n"+
			"import io;\n"+
			"class Mid extends shapes.Shape {\n"+
			"    static const KIND = \"mid\";\n"+
			"}\n"+
			"class Leaf extends Mid {}\n"+
			"io.println(Mid.KIND);\n"+
			"io.println(Leaf.KIND);\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "cross-module static value override", vm, eval, vmErr, evalErr)
	if strings.TrimSpace(vm) != "mid\nmid" {
		t.Fatalf("expected 'mid\\nmid', got: %q", vm)
	}
}

// probe15: a static method declared on a cross-module ancestor, called through a
// local subclass and grandchild, including arg passing and a sibling static read.
func TestCrossModuleParityStaticMethod(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "shapes.gb"), []byte(
		"module shapes;\n"+
			"export class Shape {\n"+
			"    static const KIND = \"sh\";\n"+
			"    static func make(string n): string { return \"made:\" + n + \":\" + Shape.KIND; }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import shapes;\n"+
			"import io;\n"+
			"class Mid extends shapes.Shape {}\n"+
			"class Leaf extends Mid {}\n"+
			"io.println(Mid.make(\"x\"));\n"+
			"io.println(Leaf.make(\"y\"));\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "cross-module static method", vm, eval, vmErr, evalErr)
	if strings.TrimSpace(vm) != "made:x:sh\nmade:y:sh" {
		t.Fatalf("expected 'made:x:sh\\nmade:y:sh', got: %q", vm)
	}
}

// probe16: an @abstract method declared on a cross-module ancestor and left
// unoverridden by the local intermediate but implemented by the grandchild;
// the grandchild is concrete, the intermediate is not.
func TestCrossModuleParityAbstractSatisfiedAcrossChain(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(
		"module base;\n"+
			"export class Base {\n"+
			"    @abstract\n"+
			"    func foo(): string { return \"\"; }\n"+
			"    func describe(): string { return \"got:\" + this.foo(); }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import base;\n"+
			"import io;\n"+
			"class Mid extends base.Base {}\n"+
			"class Leaf extends Mid {\n"+
			"    func foo(): string { return \"leaf-foo\"; }\n"+
			"}\n"+
			"io.println(Leaf().describe());\n"+
			"try { let m = Mid(); io.println(\"FAIL\"); }\n"+
			"catch (RuntimeError e) { io.println(\"mid-rejected\"); }\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "cross-module abstract satisfied across chain", vm, eval, vmErr, evalErr)
	if strings.TrimSpace(vm) != "got:leaf-foo\nmid-rejected" {
		t.Fatalf("expected 'got:leaf-foo\\nmid-rejected', got: %q", vm)
	}
}

// probe17 (negative): an @abstract method on a cross-module ancestor that no
// local subclass implements must reject construction identically on both
// backends, with the same diagnostic. The VM previously constructed it.
func TestCrossModuleParityAbstractUnimplementedRejected(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(
		"module base;\n"+
			"export class Base {\n"+
			"    @abstract\n"+
			"    func foo(): string { return \"\"; }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import base;\n"+
			"import io;\n"+
			"class Mid extends base.Base {}\n"+
			"class Leaf extends Mid {}\n"+
			"try { let l = Leaf(); io.println(\"FAIL\"); }\n"+
			"catch (RuntimeError e) { io.println(\"rejected:\" + e.message); }\n"+
			"try { let m = Mid(); io.println(\"FAIL\"); }\n"+
			"catch (RuntimeError e) { io.println(\"rejected:\" + e.message); }\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "cross-module abstract unimplemented rejected", vm, eval, vmErr, evalErr)
	want := "rejected:cannot instantiate Leaf: abstract method Base.foo is not implemented\n" +
		"rejected:cannot instantiate Mid: abstract method Base.foo is not implemented"
	if strings.TrimSpace(vm) != want {
		t.Fatalf("expected %q, got: %q", want, vm)
	}
}

// probe18: cross-module statics and abstract enforcement must hold in a built
// binary, where module bodies run through wrapper sub-VMs.
func TestCrossModuleBuiltBinaryStaticAndAbstract(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "shapes.gb"), []byte(
		"module shapes;\n"+
			"export class Shape {\n"+
			"    static const KIND = \"sh\";\n"+
			"    static func tag(): string { return \"tag:\" + Shape.KIND; }\n"+
			"    @abstract\n"+
			"    func area(): int { return 0; }\n"+
			"}\n"), 0644)

	os.WriteFile(filepath.Join(dir, "app.gb"), []byte(
		"import shapes;\n"+
			"import io;\n"+
			"class Mid extends shapes.Shape {}\n"+
			"class Square extends Mid {\n"+
			"    func area(): int { return 4; }\n"+
			"}\n"+
			"export func main(list<string> args): void {\n"+
			"    io.println(Mid.KIND);\n"+
			"    io.println(Mid.tag());\n"+
			"    io.println(\"${Square().area()}\");\n"+
			"    try { let m = Mid(); io.println(\"FAIL\"); }\n"+
			"    catch (RuntimeError e) { io.println(\"rejected\"); }\n"+
			"}\n"), 0644)

	out := filepath.Join(dir, "appbin")
	build := exec.Command(bin, "build", "--entry", "app", "--out", out)
	build.Dir = dir
	if bo, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, bo)
	}
	run := exec.Command(out)
	run.Dir = dir
	got, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("built binary failed: %v\n%s", err, got)
	}
	if strings.TrimSpace(string(got)) != "sh\ntag:sh\n4\nrejected" {
		t.Fatalf("built binary: expected 'sh\\ntag:sh\\n4\\nrejected', got: %q", got)
	}
}
