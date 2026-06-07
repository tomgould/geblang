package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAutoInvokeExportedMain checks that running a file directly auto-invokes an
// exported `main`, on both the VM and the evaluator, with the return value used
// as the exit code. A file with no exported main runs as a plain script.
func TestAutoInvokeExportedMain(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "geblang")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build geblang: %v\n%s", err, out)
	}

	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	intMain := write("intmain.gb", `module main;
import io;
export func main(list<string> args): int {
    io.println("args=" + (args.length() as string));
    return 3;
}
`)
	voidMain := write("voidmain.gb", `import io;
export func main(): void { io.println("void ran"); }
`)
	script := write("script.gb", `import io;
io.println("script ran");
`)

	// Regression (F2): auto-injected main() merges into a pre-existing init that
	// precedes the declarations; the evaluator used to crash before hoisting.
	existingInit := write("existinginit.gb", `module main;
import io;
init { io.println("init:" + helper()); }
export func main(): int { io.println("main ran"); return 7; }
func helper(): string { return "h"; }
`)

	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		out, err := cmd.CombinedOutput()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("run %v: %v\n%s", args, err, out)
		}
		return string(out), code
	}

	// int main: auto-invoked, args forwarded, return becomes exit code. Both backends.
	for _, mode := range [][]string{{intMain, "a", "b"}, {"--disable-vm", intMain, "a", "b"}} {
		out, code := run(mode...)
		if !strings.Contains(out, "args=2") {
			t.Errorf("%v: main not auto-invoked with args; got %q", mode, out)
		}
		if code != 3 {
			t.Errorf("%v: exit code = %d, want 3", mode, code)
		}
	}

	// void main: auto-invoked on both backends.
	for _, mode := range [][]string{{voidMain}, {"--disable-vm", voidMain}} {
		out, _ := run(mode...)
		if !strings.Contains(out, "void ran") {
			t.Errorf("%v: void main not auto-invoked; got %q", mode, out)
		}
	}

	// No exported main: plain script still runs.
	if out, _ := run(script); !strings.Contains(out, "script ran") {
		t.Errorf("script without main did not run; got %q", out)
	}

	// F2: existing init before declarations, on both backends. main runs once.
	for _, mode := range [][]string{{existingInit}, {"--disable-vm", existingInit}} {
		out, code := run(mode...)
		if !strings.Contains(out, "init:h") || !strings.Contains(out, "main ran") {
			t.Errorf("%v: init/main output missing; got %q", mode, out)
		}
		if n := strings.Count(out, "main ran"); n != 1 {
			t.Errorf("%v: main ran %d times, want 1; got %q", mode, n, out)
		}
		if code != 7 {
			t.Errorf("%v: exit code = %d, want 7", mode, code)
		}
	}
}
