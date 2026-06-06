package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildEmbedsAndServesResource builds a project whose geblang.yaml lists a
// resources glob, then runs the binary and confirms it reads the embedded file
// from sys.bundleDir() (no source tree present at run time).
func TestBuildEmbedsAndServesResource(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "geblang")
	if buildOut, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build geblang: %v\n%s", err, buildOut)
	}

	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("geblang.yaml", "name: app\nversion: 0.0.0\nresources:\n  - templates\n")
	write("templates/page.html", "EMBEDDED-OK")
	write("app.gb", `module app;
import io;
import sys;

export func main(list<string> args): int {
    let base = sys.bundleDir();
    if (base == "") { base = "."; }
    io.println(io.readText(base + "/templates/page.html"));
    return 0;
}
`)

	out := filepath.Join(dir, "out", "app")
	cmd := exec.Command(bin, "build", "--entry", "app", "--out", out, ".")
	cmd.Dir = dir
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("geblang build failed: %v\n%s", err, combined)
	}

	// Run from a clean cwd so only the embedded copy can satisfy the read.
	run := exec.Command(out)
	run.Dir = t.TempDir()
	combined, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run built binary: %v\n%s", err, combined)
	}
	if !strings.Contains(string(combined), "EMBEDDED-OK") {
		t.Errorf("built binary did not serve embedded resource; output: %q", string(combined))
	}
}
