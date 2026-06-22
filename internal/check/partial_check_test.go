package check

import (
	"path/filepath"
	"testing"

	"geblang/internal/modules"
)

func partialCheckOpts(dir string) Options {
	return Options{Resolver: modules.NewResolver([]string{dir})}
}

func hasError(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

func TestCheckValidPartialNoDiagnostics(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	src := `
func add(int a, int b): int { return a + b; }
let f = add(_, 10);
let result = f(1);
`
	_, diags := Source(file, src, partialCheckOpts(dir))
	if len(diags) != 0 {
		t.Fatalf("valid partial produced diagnostics: %v", diags)
	}
}

func TestCheckPartialAnalyzerVisitsBoundArg(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	src := `
func add(int a, int b): int { return a + b; }
let x = 5;
del x;
let f = add(x, _);
`
	_, diags := Source(file, src, partialCheckOpts(dir))
	if !hasError(diags) {
		t.Fatalf("expected 'use of destroyed binding' error for del'd arg in partial, got %v", diags)
	}
}

func TestCheckHoleWithSpreadIsError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.gb")
	src := `
func log(string a, string b): void {}
let f = log(_, ...rest);
`
	_, diags := Source(file, src, partialCheckOpts(dir))
	if !hasError(diags) {
		t.Fatalf("expected an error for _ mixed with spread, got %v", diags)
	}
}
