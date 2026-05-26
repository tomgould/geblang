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

func TestSourceReturnsParseDiagnostics(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	_, diags := Source(file, "func {", Options{})
	if len(diags) == 0 || diags[0].Rule != "parse" {
		t.Fatalf("expected parse diagnostic, got %+v", diags)
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
