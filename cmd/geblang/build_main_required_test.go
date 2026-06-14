package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildRequiresExportedMain confirms geblang build fails at build time, and
// writes no binary, when the entry module does not export a main function.
func TestBuildRequiresExportedMain(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "geblang")
	if buildOut, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build geblang: %v\n%s", err, buildOut)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "geblang.yaml"), []byte("name: app\nsource: src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "app.gb"),
		[]byte("module app;\nexport func helper(): int { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "out", "app")
	cmd := exec.Command(bin, "build", "--entry", "app", "--out", out, ".")
	cmd.Dir = dir
	combined, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected build to fail without an exported main; got success:\n%s", combined)
	}
	if !strings.Contains(string(combined), "does not export a main function") {
		t.Fatalf("expected a missing-main build error, got:\n%s", combined)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatalf("build wrote a binary despite the error: %s", out)
	}
}
