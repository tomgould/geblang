package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"geblang/internal/modules"
)

func TestSourceFlagsUnresolvedImport(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	source := "import nope.does.not.exist;\n"
	opts := Options{Resolver: modules.NewResolver([]string{dir})}
	_, diags := Source(file, source, opts)
	if !hasDiag(diags, "import", "cannot resolve import nope.does.not.exist") {
		t.Fatalf("expected unresolved-import diagnostic, got %+v", diags)
	}
}

func TestSourceTreatsNativeImportsAsResolved(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	source := "import binary;\nbinary.size(\">I\");\n"
	opts := Options{Resolver: modules.NewResolver([]string{dir})}
	_, diags := Source(file, source, opts)
	for _, d := range diags {
		if d.Rule == "import" {
			t.Fatalf("native import flagged: %+v", d)
		}
	}
}

func TestSourceUnusedImportWarning(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	source := "import io;\nimport bytes;\nbytes.fromString(\"hi\");\n"
	opts := Options{Lint: true, Resolver: modules.NewResolver([]string{dir})}
	_, diags := Source(file, source, opts)
	if !hasDiag(diags, "unused-import", "import io is not used") {
		t.Fatalf("expected unused-import warning for io, got %+v", diags)
	}
	for _, d := range diags {
		if d.Rule == "unused-import" && strings.Contains(d.Message, "bytes") {
			t.Fatalf("bytes import should not be flagged as unused: %+v", d)
		}
	}
}

func TestSourceCrossModuleFlagsMissingNativeSymbol(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	source := "import binary;\nbinary.absolutelyNotARealFunction();\n"
	opts := Options{
		Resolver:    modules.NewResolver([]string{dir}),
		CrossModule: true,
		NativeSymbols: map[string]map[string]struct{}{
			"binary": {"pack": {}, "unpack": {}, "size": {}, "unpackNamed": {}},
		},
	}
	_, diags := Source(file, source, opts)
	if !hasDiag(diags, "import", "binary has no exported member absolutelyNotARealFunction") {
		t.Fatalf("expected cross-module symbol diagnostic, got %+v", diags)
	}
}

func TestSourceCrossModuleAllowsKnownNativeSymbol(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	source := "import binary;\nbinary.pack(\">I\", 1);\n"
	opts := Options{
		Resolver:    modules.NewResolver([]string{dir}),
		CrossModule: true,
		NativeSymbols: map[string]map[string]struct{}{
			"binary": {"pack": {}},
		},
	}
	_, diags := Source(file, source, opts)
	for _, d := range diags {
		if d.Rule == "import" && strings.Contains(d.Message, "binary.pack") {
			t.Fatalf("known native symbol should not be flagged: %+v", d)
		}
	}
}

func TestSourceCrossModuleFlagsMissingProjectSymbol(t *testing.T) {
	dir := t.TempDir()
	depPath := filepath.Join(dir, "util.gb")
	depBody := "func helper(): int { return 1; }\n"
	if err := os.WriteFile(depPath, []byte(depBody), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "main.gb")
	mainBody := "import util;\nutil.missing();\n"
	opts := Options{Resolver: modules.NewResolver([]string{dir}), CrossModule: true}
	_, diags := Source(mainPath, mainBody, opts)
	if !hasDiag(diags, "import", "util has no exported member missing") {
		t.Fatalf("expected missing-export diagnostic, got %+v", diags)
	}
}

func TestSourceCrossModuleAllowsProjectSymbol(t *testing.T) {
	dir := t.TempDir()
	depPath := filepath.Join(dir, "util.gb")
	depBody := "func helper(): int { return 1; }\n"
	if err := os.WriteFile(depPath, []byte(depBody), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "main.gb")
	mainBody := "import util;\nutil.helper();\n"
	opts := Options{Resolver: modules.NewResolver([]string{dir}), CrossModule: true}
	_, diags := Source(mainPath, mainBody, opts)
	for _, d := range diags {
		if d.Rule == "import" && strings.Contains(d.Message, "util.helper") {
			t.Fatalf("known project symbol flagged: %+v", d)
		}
	}
}

func TestSourceCrossModuleIgnoresShadowedModule(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	// `errors` is imported but shadowed by a local list; `errors.push`
	// is the list method, not a module member.
	source := "import errors;\nlet errors = [1, 2];\nerrors = errors.push(3);\n"
	opts := Options{
		Resolver:      modules.NewResolver([]string{dir}),
		CrossModule:   true,
		NativeSymbols: map[string]map[string]struct{}{"errors": {"raise": {}}},
	}
	_, diags := Source(file, source, opts)
	for _, d := range diags {
		if d.Rule == "import" && strings.Contains(d.Message, "errors") {
			t.Fatalf("shadowed module member should not be flagged: %+v", d)
		}
	}
}

func TestSourceCrossModuleNativeFallsThroughToSource(t *testing.T) {
	dir := t.TempDir()
	// A bundled-source module named like a native (empty native symbols)
	// must resolve its real exports from the .gb file.
	if err := os.WriteFile(filepath.Join(dir, "streams.gb"),
		[]byte("module streams;\nexport func of(any x): int { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "main.gb")
	source := "import streams;\nstreams.of(5);\n"
	opts := Options{
		Resolver:      modules.NewResolver([]string{dir}),
		CrossModule:   true,
		NativeSymbols: map[string]map[string]struct{}{"streams": {}},
	}
	_, diags := Source(file, source, opts)
	for _, d := range diags {
		if d.Rule == "import" && strings.Contains(d.Message, "streams.of") {
			t.Fatalf("bundled-source member should resolve, got %+v", d)
		}
	}
}

func TestSourceImportEngineNativeNotFlaggedUnresolved(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	// A module present in NativeSymbols but absent from NativeModuleNames
	// is still recognised as native (the engine-aware fallback).
	source := "import zzengineonly;\n"
	opts := Options{
		Resolver:      modules.NewResolver([]string{dir}),
		CrossModule:   true,
		NativeSymbols: map[string]map[string]struct{}{"zzengineonly": {"go": {}}},
	}
	_, diags := Source(file, source, opts)
	for _, d := range diags {
		if d.Rule == "import" && strings.Contains(d.Message, "cannot resolve") {
			t.Fatalf("engine-native import should not be flagged unresolved: %+v", d)
		}
	}
}

func classCheckOpts(dir string) Options {
	return Options{Resolver: modules.NewResolver([]string{dir}), CrossModule: true}
}

func TestSourceFlagsUnknownMethodSameFileClass(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	src := "class Box {\n    func open(): void {}\n}\nBox b = Box();\nb.open();\nb.smash();\n"
	_, diags := Source(file, src, classCheckOpts(dir))
	if !hasDiag(diags, "semantic", "Box has no method smash") {
		t.Fatalf("expected unknown-method diagnostic, got %+v", diags)
	}
	for _, d := range diags {
		if strings.Contains(d.Message, "no method open") {
			t.Fatalf("real method flagged: %+v", d)
		}
	}
}

func TestSourceFlagsUnknownMethodCrossFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shapes.gb"),
		[]byte("module shapes;\nexport class Circle {\n    func area(): float { return 3.14; }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "main.gb")
	src := "from shapes import Circle;\nCircle c = Circle();\nc.area();\nc.bogus();\n"
	_, diags := Source(file, src, classCheckOpts(dir))
	if !hasDiag(diags, "semantic", "Circle has no method bogus") {
		t.Fatalf("expected cross-file unknown-method diagnostic, got %+v", diags)
	}
	for _, d := range diags {
		if strings.Contains(d.Message, "no method area") {
			t.Fatalf("real cross-file method flagged: %+v", d)
		}
	}
}

func TestSourceAllowsInheritedMethod(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	src := "class Base {\n    func ping(): void {}\n}\nclass Derived extends Base {\n    func pong(): void {}\n}\nDerived d = Derived();\nd.ping();\nd.pong();\n"
	_, diags := Source(file, src, classCheckOpts(dir))
	for _, d := range diags {
		if d.Rule == "semantic" && strings.Contains(d.Message, "no method") {
			t.Fatalf("inherited/own method flagged: %+v", d)
		}
	}
}

func TestSourceBailsOnCallClass(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	// A __call class dispatches dynamically; unknown methods must not flag.
	src := "class Proxy {\n    func __call(string m, list<any> a): any { return null; }\n}\nProxy p = Proxy();\np.anything();\n"
	_, diags := Source(file, src, classCheckOpts(dir))
	for _, d := range diags {
		if d.Rule == "semantic" && strings.Contains(d.Message, "no method") {
			t.Fatalf("__call class should bail, got %+v", d)
		}
	}
}

func TestSourceBailsOnDecoratedClass(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deco.gb"),
		[]byte("module deco;\nexport func Tag(any c): any { return c; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "main.gb")
	// Decorators may inject members; the class must bail.
	src := "from deco import Tag;\n@Tag\nclass Svc {\n    func run(): void {}\n}\nSvc s = Svc();\ns.injectedMaybe();\n"
	_, diags := Source(file, src, classCheckOpts(dir))
	for _, d := range diags {
		if d.Rule == "semantic" && strings.Contains(d.Message, "no method") {
			t.Fatalf("decorated class should bail, got %+v", d)
		}
	}
}

func TestSourceReturnsParseDiagnostics(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	_, diags := Source(file, "func {", Options{})
	if len(diags) == 0 || diags[0].Rule != "parse" {
		t.Fatalf("expected parse diagnostic, got %+v", diags)
	}
}

func TestSourceFromImportFlagsMissingNativeExport(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	source := "from crypt import passwordHash, notARealFunction;\n"
	opts := Options{
		Resolver:    modules.NewResolver([]string{dir}),
		CrossModule: true,
		NativeSymbols: map[string]map[string]struct{}{
			"crypt": {"passwordHash": {}, "passwordVerify": {}},
		},
	}
	_, diags := Source(file, source, opts)
	if !hasDiag(diags, "import", "crypt has no exported member notARealFunction") {
		t.Fatalf("expected missing-export diagnostic, got %+v", diags)
	}
	for _, d := range diags {
		if d.Rule == "import" && strings.Contains(d.Message, "passwordHash") {
			t.Fatalf("known export passwordHash should not be flagged: %+v", d)
		}
	}
}

func TestSourceFromImportFlagsMissingProjectSymbol(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "util.gb"), []byte("func helper(): int { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.gb")
	body := "from util import helper, missing;\n"
	opts := Options{Resolver: modules.NewResolver([]string{dir}), CrossModule: true}
	_, diags := Source(main, body, opts)
	if !hasDiag(diags, "import", "util has no exported member missing") {
		t.Fatalf("expected missing-export diagnostic, got %+v", diags)
	}
}

func TestSourceFromImportWarnsOnUnusedAlias(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	source := "from crypt import passwordHash as unused;\n"
	opts := Options{Lint: true, Resolver: modules.NewResolver([]string{dir})}
	_, diags := Source(file, source, opts)
	if !hasDiag(diags, "unused-import", "from crypt import passwordHash: unused is not used") {
		t.Fatalf("expected unused-from-import warning, got %+v", diags)
	}
}

func TestSourceFromImportMarksAliasAsUsed(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	source := "from crypt import passwordHash;\nlet x = passwordHash(\"hi\");\n"
	opts := Options{Lint: true, Resolver: modules.NewResolver([]string{dir})}
	_, diags := Source(file, source, opts)
	for _, d := range diags {
		if d.Rule == "unused-import" {
			t.Fatalf("referenced from-import flagged as unused: %+v", d)
		}
	}
}

func hasDiag(diags []Diagnostic, rule, contains string) bool {
	for _, d := range diags {
		if d.Rule == rule && strings.Contains(d.Message, contains) {
			return true
		}
	}
	return false
}
