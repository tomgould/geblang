package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A binary copied somewhere with no stdlib on disk and no GEBLANG_STDLIB must still resolve source stdlib modules (llm, rag, ...) from the embedded copy.
func TestSelfContainedBinaryResolvesSourceStdlib(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "geblang")
	if o, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build geblang: %v\n%s", err, o)
	}

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "main.gb"), []byte("import llm;\nimport io;\nio.println(\"ok\");\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GEBLANG_STDLIB=") {
			env = append(env, e)
		}
	}
	env = append(env, "XDG_CACHE_HOME="+t.TempDir())

	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = work
		cmd.Env = env
		o, err := cmd.CombinedOutput()
		return string(o), err
	}

	if out, _ := run("check", "main.gb"); strings.Contains(out, "cannot resolve import") {
		t.Fatalf("source stdlib did not resolve from the embedded copy: %q", out)
	}
	if out, err := run("main.gb"); err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("run from embedded stdlib failed: %v\n%s", err, out)
	}
}
