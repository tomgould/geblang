package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const cmrBaseGb = "module base;\n" +
	"export class Base {\n" +
	"    func describe(): string { return \"from-base\"; }\n" +
	"}\n"

const cmrMainGb = "import base;\n" +
	"import io;\n" +
	"class Sub extends base.Base {}\n" +
	"io.println(Sub().describe());\n"

const cmrExpected = "from-base\n"

// An entry-file class instantiated + invoked reflectively from an imported module must resolve entry-file globals on both backends.
func TestEntryClassReflectDispatchResolvesEntryGlobals(t *testing.T) {
	bin := buildCMBinary(t)
	run := func(args ...string) string {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "di.gb"),
			[]byte("module di;\nimport reflect;\n"+
				"export func makeAndCall(any classRef): string {\n"+
				"    let instance = classRef();\n"+
				"    let handler = reflect.method(instance, \"label\");\n"+
				"    return handler();\n"+
				"}\n"), 0644)
		os.WriteFile(filepath.Join(dir, "main.gb"), []byte(
			"import io;\nimport di;\nimport reflect;\n"+
				"let salutation = \"TAG\";\n"+
				"class Widget { func Widget() {} func label(): string { return salutation; } }\n"+
				"io.println(di.makeAndCall(reflect.class(Widget())));\n"), 0644)
		cmd := exec.Command(bin, append(args, "main.gb")...)
		cmd.Dir = dir
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	vmOut := run("--vm-strict")
	evalOut := run("--disable-vm")
	if vmOut != evalOut {
		t.Fatalf("entry-class globals diverge across backends:\n  vm:   %q\n  eval: %q", vmOut, evalOut)
	}
	if vmOut != "TAG\n" {
		t.Fatalf("expected %q, got %q", "TAG\n", vmOut)
	}
}

// TestCrossModuleCacheHitIdenticalOutput runs main.gb twice from the same dir,
// ensuring the .gbc cache-hit path produces identical output to the cold run.
func TestCrossModuleCacheHitIdenticalOutput(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(cmrBaseGb), 0644)
	os.WriteFile(filepath.Join(dir, "main.gb"), []byte(cmrMainGb), 0644)

	run := func(label string) string {
		cmd := exec.Command(bin, "main.gb")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: run failed: %v\n%s", label, err, out)
		}
		return string(out)
	}

	coldOut := run("cold")
	if coldOut != cmrExpected {
		t.Fatalf("cold run: expected %q, got %q", cmrExpected, coldOut)
	}

	cacheDir := filepath.Join(dir, ".geblang-cache")
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		t.Fatalf("expected .geblang-cache to exist after cold run")
	}

	warmOut := run("warm")
	if warmOut != coldOut {
		t.Fatalf("cache-hit run output differs from cold run:\ncold: %q\nwarm: %q", coldOut, warmOut)
	}
}

// TestCrossModuleBuildBinaryIdenticalOutput builds a standalone binary from a
// cross-module package and asserts its output matches a direct geblang run.
func TestCrossModuleBuildBinaryIdenticalOutput(t *testing.T) {
	bin := buildCMBinary(t)

	// Package layout: geblang.yaml + src/cmrbuild/base.gb + src/cmrbuild/main.gb
	pkgDir := t.TempDir()
	srcDir := filepath.Join(pkgDir, "src")
	os.MkdirAll(srcDir, 0755)

	os.WriteFile(filepath.Join(pkgDir, "geblang.yaml"), []byte("name: cmrbuild\nversion: 0.1.0\nsource: src\n"), 0644)
	os.WriteFile(filepath.Join(srcDir, "base.gb"), []byte(
		"module cmrbuild.base;\n"+
			"export class Base {\n"+
			"    func describe(): string { return \"from-base\"; }\n"+
			"}\n"), 0644)
	os.WriteFile(filepath.Join(srcDir, "main.gb"), []byte(
		"module cmrbuild.main;\n"+
			"import cmrbuild.base as base;\n"+
			"import io;\n"+
			"class Sub extends base.Base {}\n"+
			"export func main(list<string> args): void {\n"+
			"    io.println(Sub().describe());\n"+
			"}\n"), 0644)

	outBin := filepath.Join(pkgDir, "cmrbuild")
	buildOut, err := exec.Command(bin, "build", "--entry", "cmrbuild.main", "--out", outBin, pkgDir).CombinedOutput()
	if err != nil {
		t.Fatalf("geblang build failed: %v\n%s", err, buildOut)
	}

	// Run the built binary.
	builtOut, err := exec.Command(outBin).CombinedOutput()
	if err != nil {
		t.Fatalf("built binary failed: %v\n%s", err, builtOut)
	}

	// Run geblang directly against the entry source for comparison.
	entryPath := filepath.Join(srcDir, "main.gb")
	cmd := exec.Command(bin, entryPath)
	cmd.Dir = pkgDir
	directOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("direct geblang run failed: %v\n%s", err, directOut)
	}

	if string(builtOut) != string(directOut) {
		t.Fatalf("built binary output differs from direct run:\nbuilt:  %q\ndirect: %q", builtOut, directOut)
	}
	if string(builtOut) != cmrExpected {
		t.Fatalf("expected %q, got %q", cmrExpected, builtOut)
	}
}

// TestCrossModuleBothBackendsIdenticalOutput runs main.gb on the VM and the
// evaluator and asserts identical stdout (divergence probe).
func TestCrossModuleBothBackendsIdenticalOutput(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(cmrBaseGb), 0644)
	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(cmrMainGb), 0644)

	var outputs [2]string
	modes := [][]string{{mainPath}, {"--disable-vm", mainPath}}
	labels := []string{"VM", "evaluator"}
	for i, mode := range modes {
		out, err := exec.Command(bin, mode...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: run failed: %v\n%s", labels[i], err, out)
		}
		outputs[i] = string(out)
	}

	if outputs[0] != outputs[1] {
		t.Fatalf("backend output differs:\nVM:        %q\nevaluator: %q", outputs[0], outputs[1])
	}
	if outputs[0] != cmrExpected {
		t.Fatalf("expected %q, got %q", cmrExpected, outputs[0])
	}
}
