package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A module identifier used as a value is rejected by the semantic analyzer,
// which gates both the VM run path and the --disable-vm evaluator path.
func TestModuleAsValueRejectedBothBackends(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import math;\n"+
			"let x = math;\n"+
			"io.println(\"unreachable\");\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	if vmErr == nil {
		t.Fatalf("VM accepted module-as-value (expected non-zero exit), output: %q", vm)
	}
	if evalErr == nil {
		t.Fatalf("evaluator accepted module-as-value (expected non-zero exit), output: %q", eval)
	}
	if !strings.Contains(vm, "is a module, not a value") {
		t.Fatalf("VM error missing 'is a module, not a value', got: %q", vm)
	}
	if !strings.Contains(eval, "is a module, not a value") {
		t.Fatalf("evaluator error missing 'is a module, not a value', got: %q", eval)
	}
}
