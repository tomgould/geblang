package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A method-body type error must fail the compile path identically on the VM and
// the evaluator (they share the analyzer front-end).
func TestMethodBodyTypeErrorBothBackends(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"class Greeter {\n"+
			"    func greet(string name): string { return \"hi\"; }\n"+
			"    func bad(): string { return this.greet(42); }\n"+
			"}\n"+
			"let x = 1;\n"), 0644)
	for _, mode := range [][]string{{mainPath}, {"--disable-vm", mainPath}} {
		out, err := exec.Command(bin, mode...).CombinedOutput()
		if err == nil {
			t.Fatalf("mode %v: expected failure, got success: %s", mode, out)
		}
		if !strings.Contains(string(out), "no matching overload for Greeter.greet") {
			t.Fatalf("mode %v: expected method-body arg error, got: %s", mode, out)
		}
	}
}

// from-import inheritance must work on both backends: `from base import Base; class Sub extends Base`
// calling an inherited method must produce identical output.
func TestFromImportInheritanceBothBackends(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(
		"module base;\nexport class Base {\n    func tag(string s): string { return \"tag:\" + s; }\n}\n"), 0644)
	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"from base import Base;\n"+
			"import io;\n"+
			"class Sub extends Base {\n"+
			"    func go(): string { return this.tag(\"ok\"); }\n"+
			"}\n"+
			"io.println(Sub().go());\n"), 0644)
	var outputs []string
	for _, mode := range [][]string{{mainPath}, {"--disable-vm", mainPath}} {
		out, err := exec.Command(bin, mode...).CombinedOutput()
		if err != nil {
			t.Fatalf("mode %v: expected success, got error: %v\n%s", mode, err, out)
		}
		outputs = append(outputs, string(out))
	}
	if outputs[0] != outputs[1] {
		t.Fatalf("backend output differs:\nVM:   %q\neval: %q", outputs[0], outputs[1])
	}
	if !strings.Contains(outputs[0], "tag:ok") {
		t.Fatalf("expected inherited method output 'tag:ok', got: %q", outputs[0])
	}
}

// Aliased from-import as parent must resolve to the real class on both backends,
// not the alias name (regression: was yielding "base.B" instead of "base.Base").
func TestFromImportAliasedInheritanceBothBackends(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(
		"module base;\nexport class Base {\n    func tag(string s): string { return \"tag:\" + s; }\n}\n"), 0644)
	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"from base import Base as B;\n"+
			"import io;\n"+
			"class Sub extends B {\n"+
			"    func go(): string { return this.tag(\"ok\"); }\n"+
			"}\n"+
			"io.println(Sub().go());\n"), 0644)
	var outputs []string
	for _, mode := range [][]string{{mainPath}, {"--disable-vm", mainPath}} {
		out, err := exec.Command(bin, mode...).CombinedOutput()
		if err != nil {
			t.Fatalf("mode %v: expected success, got error: %v\n%s", mode, err, out)
		}
		outputs = append(outputs, string(out))
	}
	if outputs[0] != outputs[1] {
		t.Fatalf("backend output differs:\nVM:   %q\neval: %q", outputs[0], outputs[1])
	}
	if !strings.Contains(outputs[0], "tag:ok") {
		t.Fatalf("expected 'tag:ok', got: %q", outputs[0])
	}
}

// Cross-module inheritance via the qualified parent form (extends base.Base):
// a this-call to an inherited base method with a wrong-typed arg must fail
// identically on both backends (analysis-time, shared analyzer).
func TestMethodBodyTypeErrorCrossModuleQualified(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.gb"), []byte(
		"module base;\nexport class Base {\n    func tag(string s): string { return s; }\n}\n"), 0644)
	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import base;\n"+
			"class Sub extends base.Base {\n"+
			"    func go(): string { return this.tag(99); }\n"+
			"}\n"+
			"let x = 1;\n"), 0644)
	for _, mode := range [][]string{{mainPath}, {"--disable-vm", mainPath}} {
		out, err := exec.Command(bin, mode...).CombinedOutput()
		if err == nil {
			t.Fatalf("mode %v: expected failure, got success: %s", mode, out)
		}
		if !strings.Contains(string(out), "no matching overload") {
			t.Fatalf("mode %v: expected cross-module qualified-parent method error, got: %s", mode, out)
		}
	}
}

