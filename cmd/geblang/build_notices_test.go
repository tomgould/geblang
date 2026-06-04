package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildWritesNoticesSidecar verifies `geblang build` writes the
// third-party NOTICES alongside the output binary (a sidecar file, not a
// binary flag, so it cannot clash with a `licenses` arg the built program may
// define).
func TestBuildWritesNoticesSidecar(t *testing.T) {
	// Build from source so the test always reflects the current code, never a
	// stale binary on PATH or in the repo root.
	bin := filepath.Join(t.TempDir(), "geblang")
	if buildOut, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build geblang: %v\n%s", err, buildOut)
	}

	dir := t.TempDir()
	app := "module app;\nimport io;\nexport func main(list<string> args): int {\n    io.println(\"hi\");\n    return 0;\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "app.gb"), []byte(app), 0o644); err != nil {
		t.Fatalf("write app.gb: %v", err)
	}
	out := filepath.Join(dir, "out", "app")
	cmd := exec.Command(bin, "build", "--entry", "app", "--out", out, ".")
	cmd.Dir = dir
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("geblang build failed: %v\n%s", err, combined)
	}

	data, err := os.ReadFile(out + ".NOTICES.txt")
	if err != nil {
		t.Fatalf("notices sidecar missing: %v", err)
	}
	if !strings.Contains(string(data), "Third-Party") {
		t.Errorf("notices sidecar missing expected content; got %d bytes", len(data))
	}

	// The built binary must not intercept the program's own `licenses` arg.
	run := exec.Command(out, "licenses")
	combined, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run built binary: %v\n%s", err, combined)
	}
	if strings.TrimSpace(string(combined)) != "hi" {
		t.Errorf("built binary hijacked `licenses` arg; output: %q", string(combined))
	}
}
