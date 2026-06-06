package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildBundlesSourceBackedNativeModule guards a bundler bug where a module
// that is both natively registered and source-backed (e.g. async.sync, whose
// Mutex lives in stdlib/async/sync.gb) was skipped as "native" and excluded
// from the bundle, so the built binary failed at runtime. The build must embed
// the source and the binary must run.
func TestBuildBundlesSourceBackedNativeModule(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "geblang")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build geblang: %v\n%s", err, out)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("geblang.yaml", "name: sbn\nsource: src\n")
	write("src/main.gb", `module main;
import io;
import async.sync as sync;
export func main(list<string> args): int {
    let m = sync.Mutex();
    m.lock();
    io.println("locked ok");
    m.unlock();
    return 0;
}
`)

	// The temp-built binary is not beside the repo stdlib; point it there so
	// the source-backed async.sync resolves (mirrors an installed toolchain).
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "out", "app")
	build := exec.Command(bin, "build", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GEBLANG_STDLIB="+stdlib)
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("geblang build failed: %v\n%s", err, combined)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir() // clean cwd: only the bundle can satisfy the import
	combined, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run built binary: %v\n%s", err, combined)
	}
	if !strings.Contains(string(combined), "locked ok") {
		t.Errorf("built binary did not run source-backed native module; output: %q", combined)
	}
}
