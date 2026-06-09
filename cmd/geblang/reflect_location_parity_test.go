package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reflect.location(class).module must report the declaring module's
// canonical name identically on both backends ("" for the root program).
func TestReflectLocationParity(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "mymod.gb"), []byte(
		"module mymod;\n"+
			"export func helper(int n): int { return n; }\n"+
			"export class Thing {\n"+
			"    int v;\n"+
			"    func Thing(int v) { this.v = v; }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import reflect;\n"+
			"import io;\n"+
			"import mymod;\n"+
			"class Local {\n"+
			"    int x;\n"+
			"    func Local(int x) { this.x = x; }\n"+
			"}\n"+
			"io.println(\"thing=\" + reflect.location(reflect.class(\"Thing\"))[\"module\"]);\n"+
			"io.println(\"local=\" + reflect.location(reflect.class(\"Local\"))[\"module\"]);\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "reflect.location module", vm, eval, vmErr, evalErr)
	if !strings.Contains(vm, "thing=mymod") {
		t.Fatalf("expected module class location module 'mymod', got: %q", vm)
	}
	if !strings.Contains(vm, "local=\n") && !strings.HasSuffix(strings.TrimRight(vm, "\n"), "local=") {
		t.Fatalf("expected local class location module empty, got: %q", vm)
	}
}

// reflect.function over an imported module's exported function must
// resolve to the same reflectable function (location/parameters/return
// type) on both backends. The evaluator previously returned null for the
// bare-name lookup because it had no cross-module function registry.
func TestReflectFunctionCrossModuleParity(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "mymod.gb"), []byte(
		"module mymod;\n"+
			"export func helper(int n): int { return n; }\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import reflect;\n"+
			"import io;\n"+
			"import mymod;\n"+
			"let f = reflect.function(\"helper\");\n"+
			"io.println(\"found=\" + \"${f != null}\");\n"+
			"io.println(\"loc=\" + \"${reflect.location(f)}\");\n"+
			"io.println(\"params=\" + \"${reflect.parameters(f)}\");\n"+
			"io.println(\"ret=\" + reflect.returnType(f));\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "reflect.function cross-module", vm, eval, vmErr, evalErr)
	if !strings.Contains(vm, "found=true") {
		t.Fatalf("expected imported function to resolve, got: %q", vm)
	}
	if !strings.Contains(vm, "\"module\": \"mymod\"") {
		t.Fatalf("expected function location module 'mymod', got: %q", vm)
	}
}

// A nested (locally-declared) function must not leak into the reflect
// global-function registry: reflect.function("inner") is null on both
// backends. Only top-level function declarations are reflectable by bare name.
func TestReflectNestedFunctionNotGlobal(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import reflect;\n"+
			"import io;\n"+
			"func outer(): void { func inner(): int { return 1; } inner(); }\n"+
			"outer();\n"+
			"let i = reflect.function(\"inner\");\n"+
			"let o = reflect.function(\"outer\");\n"+
			"io.println(\"inner=\" + \"${i}\");\n"+
			"io.println(\"outer=\" + \"${o != null}\");\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "reflect nested function not global", vm, eval, vmErr, evalErr)
	if !strings.Contains(vm, "inner=null") {
		t.Fatalf("expected nested function to be null, got: %q", vm)
	}
	if !strings.Contains(vm, "outer=true") {
		t.Fatalf("expected top-level function to resolve, got: %q", vm)
	}
}

// reflect.location of a qualified-name class lookup (reflect.class("mod.Thing"))
// must report the declaring module and source position on both backends.
// The VM previously returned null because its module-export class value
// carried no location fields.
func TestReflectQualifiedClassLocationParity(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "mymod.gb"), []byte(
		"module mymod;\n"+
			"export class Thing {\n"+
			"    int v;\n"+
			"    func Thing(int v) { this.v = v; }\n"+
			"}\n"), 0644)

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import reflect;\n"+
			"import io;\n"+
			"import mymod;\n"+
			"let c = reflect.class(\"mymod.Thing\");\n"+
			"io.println(\"loc=\" + \"${reflect.location(c)}\");\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "reflect.class qualified location", vm, eval, vmErr, evalErr)
	if strings.Contains(vm, "loc=null") {
		t.Fatalf("expected qualified-class location, got null: %q", vm)
	}
	if !strings.Contains(vm, "\"module\": \"mymod\"") {
		t.Fatalf("expected qualified-class location module 'mymod', got: %q", vm)
	}
}
