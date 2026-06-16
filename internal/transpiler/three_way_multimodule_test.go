package transpiler_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"geblang/internal/ast"
	"geblang/internal/bundle"
	"geblang/internal/check"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/parser"
	"geblang/internal/transpiler"
)

// TestThreeWayParityMultiModule runs every package under
// tests/transpile/multimodule/ three ways - VM, evaluator, and transpiled native
// binary - and asserts byte-identical stdout plus matching exit code. The
// single-file corpus never exercises a non-entry module's name prefix; this
// harness guards that path (same-module function calls, module-level let/const).
// Each package has a geblang.yaml whose `name` is the package root, src/main.gb
// exporting main, and at least one sibling module.
func TestThreeWayParityMultiModule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-way native parity in -short mode")
	}

	repoRoot := repoRootFromTest(t)
	geblangBin := findGeblangBinary(t, repoRoot)
	baseDir := filepath.Join(repoRoot, "tests", "transpile", "multimodule")

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Skipf("no multimodule fixtures: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(baseDir, e.Name()))
		}
	}
	if len(dirs) == 0 {
		t.Skip("no multimodule fixtures")
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			runThreeWayMultiModule(t, repoRoot, geblangBin, dir)
		})
	}
}

func runThreeWayMultiModule(t *testing.T, repoRoot, geblangBin, pkgDir string) {
	t.Helper()

	resolver := modules.NewResolver([]string{pkgDir})
	manifest, err := resolver.FindManifest(pkgDir)
	if err != nil || manifest == nil {
		t.Fatalf("find manifest in %s: %v", pkgDir, err)
	}
	entryCanonical := manifest.Name + ".main"
	entryPath, err := resolver.Resolve(entryCanonical)
	if err != nil {
		t.Fatalf("resolve entry %q: %v", entryCanonical, err)
	}

	vmOut, vmCode := runScript(t, repoRoot, geblangBin, entryPath, "--vm-strict", nil)
	evalOut, evalCode := runScript(t, repoRoot, geblangBin, entryPath, "--disable-vm", nil)

	if vmCode != evalCode {
		t.Fatalf("VM vs evaluator exit code differ: vm=%d eval=%d", vmCode, evalCode)
	}
	if string(vmOut) != string(evalOut) {
		t.Fatalf("VM vs evaluator stdout differ\n--- vm ---\n%q\n--- eval ---\n%q", vmOut, evalOut)
	}

	natOut, natCode := buildAndRunNativeMulti(t, repoRoot, entryCanonical, entryPath, resolver)

	if vmCode != natCode {
		t.Fatalf("VM vs native exit code differ: vm=%d native=%d\nnative output: %q", vmCode, natCode, natOut)
	}
	if string(vmOut) != string(natOut) {
		t.Fatalf("VM vs native stdout differ\n--- vm ---\n%q\n--- native ---\n%q", vmOut, natOut)
	}
}

// buildAndRunNativeMulti transpiles a multi-module package (entry plus every
// transitively imported user module) and builds + runs it offline against the
// live checkout, mirroring the single-file native leg.
func buildAndRunNativeMulti(t *testing.T, repoRoot, entryCanonical, entryPath string, resolver *modules.Resolver) ([]byte, int) {
	t.Helper()

	allModules, err := bundle.WalkImports(entryCanonical, entryPath, resolver, check.IsNativeImport)
	if err != nil {
		t.Fatalf("walk imports: %v", err)
	}

	modulesAst := make(map[string]*ast.Program, len(allModules))
	sources := make(map[string]string, len(allModules))
	for canonical, absPath := range allModules {
		src, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("read %s: %v", absPath, err)
		}
		p := parser.New(lexer.New(string(src)))
		prog := p.ParseProgram()
		if errs := p.Errors(); len(errs) != 0 {
			t.Fatalf("parse %s: %v", absPath, errs)
		}
		modulesAst[canonical] = prog
		sources[canonical] = absPath
	}

	out, diags, err := transpiler.Transpile(transpiler.Input{
		Modules: modulesAst,
		Sources: sources,
	}, transpiler.Options{EntryModule: entryCanonical})
	if err != nil {
		t.Fatalf("transpile %s: %v", entryCanonical, err)
	}
	for _, d := range diags {
		if d.Severity == transpiler.SeverityError {
			t.Fatalf("multimodule fixture %s diagnosed (not transpile-safe): %s", entryCanonical, d)
		}
	}

	work := t.TempDir()
	writeOutputTree(t, work, out)
	writeGoMod(t, work, repoRoot)

	gotOut, code, err := goBuildAndRun(t, work, nil)
	if err != nil {
		t.Fatalf("native build %s: %v\noutput: %s", entryCanonical, err, gotOut)
	}
	return gotOut, code
}
