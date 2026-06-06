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
}
