package transpiler_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/transpiler"
)

// TestEndToEndUncaughtRendering verifies a throw that escapes to the top level
// renders the canonical uncaught format on stderr and exits non-zero, matching
// the interpreter. The golden e2e harness only covers exit-zero programs, so an
// uncaught path needs its own stderr+exit comparison.
func TestEndToEndUncaughtRendering(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end go-build test in -short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("end-to-end shell harness assumes POSIX")
	}

	repoRoot := repoRootFromTest(t)
	geblangBin := findGeblangBinary(t, repoRoot)

	src := `import io;

class MyError extends Error {
    func MyError(string m) {
        parent(m);
    }
}

func boom() {
    throw MyError("oops");
}

io.println("before");
boom();
io.println("after");
`
	entry := filepath.Join(t.TempDir(), "prog.gb")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	wantStdout, wantStderr, wantExit := runSplit(repoRoot, geblangBin, entry)

	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse: %v", errs)
	}
	out, diags, err := transpiler.Transpile(transpiler.Input{
		Modules: map[string]*ast.Program{"main": prog},
		Sources: map[string]string{"main": entry},
	}, transpiler.Options{EntryModule: "main"})
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	for _, d := range diags {
		if d.Severity == transpiler.SeverityError {
			t.Fatalf("transpile diagnostic: %s", d)
		}
	}

	work := t.TempDir()
	writeOutputTree(t, work, out)
	writeGoMod(t, work, repoRoot)
	gotStdout, gotStderr, gotExit := buildAndRunSplit(t, work)

	if gotStdout != wantStdout {
		t.Errorf("stdout mismatch\nwant %q\ngot  %q", wantStdout, gotStdout)
	}
	// The transpiled error value carries no call-stack frames (runtime trace
	// capture is out of Tier-1 scope), so we pin the canonical header line -
	// "uncaught <Class>: <message>" - which the renderer shares with the
	// interpreter, plus the non-zero exit.
	wantHeader := firstLine(wantStderr)
	gotHeader := firstLine(gotStderr)
	if gotHeader != wantHeader {
		t.Errorf("uncaught header mismatch\nwant %q\ngot  %q", wantHeader, gotHeader)
	}
	if !strings.HasPrefix(gotHeader, "uncaught ") {
		t.Errorf("expected canonical uncaught header, got %q", gotHeader)
	}
	if (gotExit == 0) != (wantExit == 0) {
		t.Errorf("exit-code class mismatch: want %d, got %d", wantExit, gotExit)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func runSplit(dir, bin string, args ...string) (string, string, int) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		exit = -1
	}
	return stdout.String(), stderr.String(), exit
}

func buildAndRunSplit(t *testing.T, root string) (string, string, int) {
	t.Helper()
	goEnv := append(os.Environ(), "GOCACHE=/tmp/geblang-go-cache", "GOFLAGS=-mod=mod", "GOPROXY=off")
	binPath := filepath.Join(root, "out_bin")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, "./main")
	build.Dir = root
	build.Env = goEnv
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	run := exec.Command(binPath)
	var stdout, stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr
	err := run.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		exit = -1
	}
	return stdout.String(), strings.TrimRight(stderr.String(), "\n") + "\n", exit
}
