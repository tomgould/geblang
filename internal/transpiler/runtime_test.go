package transpiler_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"geblang/internal/transpiler"
)

func TestEndToEndGoldenFixturesMatchInterpreter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end go-build test in -short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("end-to-end shell harness assumes POSIX")
	}

	repoRoot := repoRootFromTest(t)
	geblangBin := findGeblangBinary(t, repoRoot)

	fixtures, err := discoverFixtures()
	if err != nil {
		t.Fatalf("discover fixtures: %v", err)
	}
	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			runFixtureParity(t, repoRoot, geblangBin, fx)
		})
	}
}

func runFixtureParity(t *testing.T, repoRoot, geblangBin string, fx goldenFixture) {
	t.Helper()
	absEntry, err := filepath.Abs(fx.entryPath)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}

	wantOut, err := runCapture(repoRoot, geblangBin, absEntry)
	if err != nil {
		t.Fatalf("geblang run: %v\noutput: %s", err, wantOut)
	}

	out, diags, err := transpiler.Transpile(transpiler.Input{
		Modules: fx.modules,
		Sources: fx.sources,
	}, transpiler.Options{EntryModule: fx.entryCanonical})
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

	gotOut, _, err := goBuildAndRun(t, work, nil)
	if err != nil {
		t.Fatalf("go build/run: %v\noutput: %s", err, gotOut)
	}

	if string(wantOut) != string(gotOut) {
		t.Fatalf("transpiled output does not match interpreter\n--- want ---\n%q\n--- got ---\n%q", string(wantOut), string(gotOut))
	}
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func findGeblangBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	for _, candidate := range []string{
		filepath.Join(repoRoot, "geblang"),
		"/usr/local/bin/geblang",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Skip("geblang binary not found; build it first (make build)")
	return ""
}

func runCapture(dir, bin string, args ...string) ([]byte, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return append(stdout.Bytes(), stderr.Bytes()...), err
	}
	return stdout.Bytes(), nil
}

func writeOutputTree(t *testing.T, root string, out transpiler.Output) {
	t.Helper()
	for relPath, contents := range out.Files {
		full := filepath.Join(root, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, contents, 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

// writeGoMod points the transpiled module at the live geblang source so go
// build resolves the transpilert runtime package against this checkout. It
// reuses the repo go.mod/go.sum so geblang's full dependency graph is pinned
// and the build needs no network fetch or tidy.
func writeGoMod(t *testing.T, root, repoRoot string) {
	t.Helper()
	repoMod, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		t.Fatalf("read repo go.mod: %v", err)
	}
	mod := strings.Replace(string(repoMod), "module geblang\n", "module geblang_transpiled\n", 1)
	mod += "\nrequire geblang v0.0.0\n\nreplace geblang => " + repoRoot + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	sum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read repo go.sum: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), sum, 0o644); err != nil {
		t.Fatalf("write go.sum: %v", err)
	}
}

// goBuildAndRun builds the transpiled module offline and runs it with the given
// args. A build failure returns a non-nil error; a successful build returns the
// program's stdout and process exit code (a thrown Geblang error exits 1).
func goBuildAndRun(t *testing.T, root string, progArgs []string) ([]byte, int, error) {
	t.Helper()
	goEnv := append(os.Environ(), "GOCACHE=/tmp/geblang-go-cache", "GOFLAGS=-mod=mod", "GOPROXY=off")
	binPath := filepath.Join(root, "out_bin")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, "./main")
	build.Dir = root
	build.Env = goEnv
	if out, err := build.CombinedOutput(); err != nil {
		return out, 0, err
	}

	run := exec.Command(binPath, progArgs...)
	var stdout, stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr
	err := run.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		return append(stdout.Bytes(), stderr.Bytes()...), 0, err
	}
	return stdout.Bytes(), code, nil
}
