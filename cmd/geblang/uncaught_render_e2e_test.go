package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const e2eThrowProgram = `import io;
import errors;

func inner(int x): int {
    throw errors.new("ValueError", "boom");
}

func middle(int x): int {
    let r = inner(x);
    return r;
}

io.println(middle(5));
`

const e2eThrowWant = `uncaught ValueError: boom
  at inner (line 5)
  at middle (line 9)
  at <top level> (line 13)
`

func runExpectingExit1(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("run %v: want exit 1, got %v (stderr %q)", args, err, stderr.String())
	}
	return stderr.String()
}

// TestUncaughtRenderE2E proves the canonical uncaught rendering is
// byte-identical across a cold run, a cache-hit run, both backends,
// and a self-contained built binary.
func TestUncaughtRenderE2E(t *testing.T) {
	bin := buildGeblangBinary(t, false)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.gb")
	if err := os.WriteFile(src, []byte(e2eThrowProgram), 0o644); err != nil {
		t.Fatal(err)
	}

	cold := runExpectingExit1(t, bin, src)
	if cold != e2eThrowWant {
		t.Fatalf("cold run:\n--- got ---\n%s--- want ---\n%s", cold, e2eThrowWant)
	}
	if warm := runExpectingExit1(t, bin, src); warm != cold {
		t.Fatalf("cache-hit run differs:\n--- warm ---\n%s--- cold ---\n%s", warm, cold)
	}
	if eval := runExpectingExit1(t, bin, "--disable-vm", src); eval != cold {
		t.Fatalf("evaluator run differs:\n--- eval ---\n%s--- vm ---\n%s", eval, cold)
	}

	appDir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(appDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("geblang.yaml", "name: throwapp\nversion: 0.0.1\n")
	write("app.gb", `module app;
import io;
import errors;

func inner(int x): int {
    throw errors.new("ValueError", "boom");
}

func middle(int x): int {
    let r = inner(x);
    return r;
}

export func main(list<string> args): int {
    io.println(middle(5));
    return 0;
}
`)
	out := filepath.Join(appDir, "out", "throwapp")
	if buildOut, err := exec.Command(bin, "build", "--entry", "app", "--out", out, appDir).CombinedOutput(); err != nil {
		t.Fatalf("geblang build: %v\n%s", err, buildOut)
	}
	got := runExpectingExit1(t, out)
	for _, want := range []string{
		"uncaught ValueError: boom",
		"  at inner (line 6)",
		"  at middle (line 10)",
		"  at main (line 15)",
		"  at <top level>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("built binary stderr missing %q:\n%s", want, got)
		}
	}
}

// TestUncaughtRenderCrossModuleE2E proves cross-module frames render
// identically on both backends through the real module loader.
func TestUncaughtRenderCrossModuleE2E(t *testing.T) {
	bin := buildGeblangBinary(t, false)
	dir := t.TempDir()
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("helper.gb", `module helper;
import errors;

export func explode(int x): int {
    throw errors.new("ValueError", "cross boom");
}
`)
	write("main.gb", `import io;
import helper;

func relay(int x): int {
    return helper.explode(x);
}

io.println(relay(2));
`)
	src := filepath.Join(dir, "main.gb")
	vm := runExpectingExit1(t, bin, src)
	eval := runExpectingExit1(t, bin, "--disable-vm", src)
	if vm != eval {
		t.Fatalf("cross-module divergence:\n--- vm ---\n%s--- eval ---\n%s", vm, eval)
	}
	for _, want := range []string{"uncaught ValueError: cross boom", "  at explode (line ", "  at relay (line 5)", "  at <top level> (line 8)"} {
		if !strings.Contains(vm, want) {
			t.Fatalf("cross-module output missing %q:\n%s", want, vm)
		}
	}
}

// TestRunnerFailureShowsTrace locks the geblang-test failure format:
// a thrown error inside a test method reports the canonical trace.
func TestRunnerFailureShowsTrace(t *testing.T) {
	bin := buildGeblangBinary(t, false)
	dir := t.TempDir()
	testFile := filepath.Join(dir, "boom_test.gb")
	if err := os.WriteFile(testFile, []byte(`import test;
import errors;

func explode(): int {
    throw errors.new("ValueError", "runner boom");
}

class BoomTest extends test.Test {
    @test
    func throwsUncaught(): void {
        explode();
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "test", testFile)
	outBytes, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("geblang test: want failure exit, got success:\n%s", outBytes)
	}
	got := string(outBytes)
	for _, want := range []string{
		"FAIL BoomTest: throwsUncaught: ValueError: runner boom",
		"  at explode (line 5)",
		"  at BoomTest.throwsUncaught (line 11)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("runner output missing %q:\n%s", want, got)
		}
	}
}
