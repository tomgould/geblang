package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildCMBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "geblang")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "GOCACHE=/tmp/geblang-go-cache")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestCrossModuleTypeErrorFailsCompilePathBothBackends(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "shapes.gb"), []byte("class Circle {}\n"), 0644)
	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte("import shapes;\nfunc area(shapes.Nope c): int { return 0; }\nlet x = 1;\n"), 0644)
	for _, mode := range [][]string{{mainPath}, {"--disable-vm", mainPath}} {
		out, err := exec.Command(bin, mode...).CombinedOutput()
		if err == nil {
			t.Fatalf("mode %v: expected failure, got success: %s", mode, out)
		}
		if !strings.Contains(string(out), "has no exported type Nope") {
			t.Fatalf("mode %v: expected qualified-type error, got: %s", mode, out)
		}
	}
}

// TestCrossModuleTypeErrorParityBothBackends asserts the diagnostic is
// identical on the VM and the evaluator (they share the analyzer), so the
// compile-time check cannot diverge across backends.
func TestCrossModuleTypeErrorParityBothBackends(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "shapes.gb"), []byte("class Circle {}\n"), 0644)
	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte("import shapes;\nfunc area(shapes.Nope c): int { return 0; }\nlet x = 1;\n"), 0644)
	extract := func(b []byte) string {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "has no exported type") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}
	vmOut, _ := exec.Command(bin, mainPath).CombinedOutput()
	evOut, _ := exec.Command(bin, "--disable-vm", mainPath).CombinedOutput()
	vmDiag, evDiag := extract(vmOut), extract(evOut)
	if vmDiag == "" {
		t.Fatalf("no diagnostic from the VM backend: %s", vmOut)
	}
	if vmDiag != evDiag {
		t.Fatalf("diagnostic differs across backends:\nVM:   %q\neval: %q", vmDiag, evDiag)
	}
}

// TestCrossModuleAnalysisSkippedOnCacheHit proves the cross-module analysis is
// gated on the bytecode cache: a clean main is compiled+cached on the cold run,
// then an imported module is rewritten to drop a member main references only in
// uncalled code. Because main's source is unchanged the .gbc hits, so analysis
// is skipped and the now-stale reference is NOT reported. A control run with a
// cleared cache re-analyzes and does report it, confirming the skip is real.
func TestCrossModuleAnalysisSkippedOnCacheHit(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()
	modPath := filepath.Join(dir, "mod.gb")
	mainPath := filepath.Join(dir, "main.gb")
	// greet() is referenced only inside the uncalled function, so the cold
	// run's analysis passes and runtime never touches the reference.
	os.WriteFile(modPath, []byte("export func greet(): string { return \"hi\"; }\n"), 0644)
	os.WriteFile(mainPath, []byte("import mod;\nimport io;\nfunc unused(): string { return mod.greet(); }\nio.println(\"ok\");\n"), 0644)

	// The bytecode cache lives in .geblang-cache under the working directory.
	cacheDir := filepath.Join(dir, ".geblang-cache")
	run := func() (string, error) {
		cmd := exec.Command(bin, "main.gb")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run(); err != nil {
		t.Fatalf("cold run failed: %v\n%s", err, out)
	}
	// Drop greet so the reference in main becomes stale.
	os.WriteFile(modPath, []byte("export func other(): string { return \"x\"; }\n"), 0644)

	hitOut, hitErr := run()
	if strings.Contains(hitOut, "has no export") {
		t.Fatalf("cache hit should skip analysis, but reported a stale-export error:\n%s", hitOut)
	}
	if hitErr != nil {
		t.Fatalf("cache-hit run should still execute (greet uncalled): %v\n%s", hitErr, hitOut)
	}
	if !strings.Contains(hitOut, "ok") {
		t.Fatalf("cache-hit run did not reach execution: %s", hitOut)
	}

	// Control: clear the cache so analysis runs and catches the stale reference.
	os.RemoveAll(cacheDir)
	coldOut, coldErr := run()
	if coldErr == nil {
		t.Fatalf("control run with cleared cache should fail analysis, got success:\n%s", coldOut)
	}
	if !strings.Contains(coldOut, "has no export") {
		t.Fatalf("control run did not report the stale-export error:\n%s", coldOut)
	}
}
