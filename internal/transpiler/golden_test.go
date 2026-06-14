package transpiler_test

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"geblang/internal/ast"
	"geblang/internal/bundle"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/parser"
	"geblang/internal/transpiler"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/golden/*.expected files")

func TestGoldenFixtures(t *testing.T) {
	fixtures, err := discoverFixtures()
	if err != nil {
		t.Fatalf("discover fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Skip("no golden fixtures in testdata/golden")
	}

	for _, fx := range fixtures {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			runGoldenCase(t, fx)
		})
	}
}

type goldenFixture struct {
	name           string
	entryCanonical string
	entryPath      string
	modules        map[string]*ast.Program
	sources        map[string]string
	expectedPath   string
}

func discoverFixtures() ([]goldenFixture, error) {
	root := filepath.Join("testdata", "golden")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []goldenFixture
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() {
			mainPath := filepath.Join(root, name, "main.gb")
			if _, err := os.Stat(mainPath); err != nil {
				continue
			}
			fx, err := loadMultiModuleFixture(root, name, mainPath)
			if err != nil {
				return nil, err
			}
			out = append(out, fx)
			continue
		}
		if !strings.HasSuffix(name, ".gb") {
			continue
		}
		fx, err := loadSingleFileFixture(root, name)
		if err != nil {
			return nil, err
		}
		out = append(out, fx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func loadSingleFileFixture(root, fname string) (goldenFixture, error) {
	srcPath := filepath.Join(root, fname)
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return goldenFixture{}, err
	}
	p := parser.New(lexer.New(string(srcBytes)))
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) != 0 {
		return goldenFixture{}, &parseError{srcPath: srcPath, errs: errs}
	}
	module := strings.TrimSuffix(fname, ".gb")
	return goldenFixture{
		name:           fname,
		entryCanonical: module,
		entryPath:      srcPath,
		modules:        map[string]*ast.Program{module: prog},
		sources:        map[string]string{module: srcPath},
		expectedPath:   strings.TrimSuffix(srcPath, ".gb") + ".go.expected",
	}, nil
}

func loadMultiModuleFixture(root, dir, mainPath string) (goldenFixture, error) {
	absRoot, err := filepath.Abs(filepath.Join(root, dir))
	if err != nil {
		return goldenFixture{}, err
	}
	resolver := modules.NewResolver([]string{absRoot})
	entryCanonical := "main"
	allModules, err := bundle.WalkImports(entryCanonical, mainPath, resolver, func(c string) bool {
		_, native := stdlibCanonicals[c]
		return native
	})
	if err != nil {
		return goldenFixture{}, err
	}
	modulesAst := make(map[string]*ast.Program, len(allModules))
	sources := make(map[string]string, len(allModules))
	for canonical, absPath := range allModules {
		src, err := os.ReadFile(absPath)
		if err != nil {
			return goldenFixture{}, err
		}
		p := parser.New(lexer.New(string(src)))
		prog := p.ParseProgram()
		if errs := p.Errors(); len(errs) != 0 {
			return goldenFixture{}, &parseError{srcPath: absPath, errs: errs}
		}
		modulesAst[canonical] = prog
		sources[canonical] = absPath
	}
	return goldenFixture{
		name:           dir + "/",
		entryCanonical: entryCanonical,
		entryPath:      mainPath,
		modules:        modulesAst,
		sources:        sources,
		expectedPath:   filepath.Join(root, dir, "main.go.expected"),
	}, nil
}

type parseError struct {
	srcPath string
	errs    []string
}

func (e *parseError) Error() string {
	return "parse " + e.srcPath + ": " + strings.Join(e.errs, "; ")
}

var stdlibCanonicals = map[string]struct{}{
	"args": {}, "async": {}, "bytes": {}, "cli": {}, "collections": {}, "compress": {},
	"crypt": {}, "csv": {}, "datetime": {}, "db": {}, "dotenv": {}, "encoding": {},
	"errors": {}, "ext": {}, "freeze": {}, "http": {}, "io": {}, "json": {}, "log": {},
	"markdown": {}, "math": {}, "metrics": {}, "net": {}, "path": {}, "process": {},
	"random": {}, "re": {}, "reflect": {}, "string": {}, "sys": {}, "task": {},
	"templates": {}, "time": {}, "toml": {}, "uuid": {}, "xml": {}, "yaml": {},
}

func runGoldenCase(t *testing.T, fx goldenFixture) {
	t.Helper()
	out, diags, err := transpiler.Transpile(transpiler.Input{
		Modules: fx.modules,
		Sources: fx.sources,
	}, transpiler.Options{EntryModule: fx.entryCanonical})
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics for %s: %v", fx.entryPath, diags)
	}

	got := renderOutputForGolden(out)

	if *updateGolden {
		if err := os.WriteFile(fx.expectedPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write expected: %v", err)
		}
		return
	}

	wantBytes, err := os.ReadFile(fx.expectedPath)
	if err != nil {
		t.Fatalf("read expected %s: %v (run with -update to create)", fx.expectedPath, err)
	}
	if string(wantBytes) != got {
		t.Fatalf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", fx.entryPath, string(wantBytes), got)
	}
}

func renderOutputForGolden(out transpiler.Output) string {
	paths := make([]string, 0, len(out.Files))
	for p := range out.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var sb strings.Builder
	for _, p := range paths {
		sb.WriteString("// === ")
		sb.WriteString(p)
		sb.WriteString(" ===\n")
		sb.Write(out.Files[p])
		if !strings.HasSuffix(string(out.Files[p]), "\n") {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
