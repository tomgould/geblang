package lower_test

import (
	"strings"
	"testing"

	"geblang/internal/transpiler/lower"
	"geblang/internal/transpiler/types"
)

func TestModuleRenderEntryEmitsMainFunc(t *testing.T) {
	mod := lower.NewModule("main", true, types.IntModeFast)
	got := string(mod.Render())

	for _, want := range []string{"package main", "func main() {", "}"} {
		if !strings.Contains(got, want) {
			t.Errorf("Render missing %q\noutput:\n%s", want, got)
		}
	}
}

func TestModuleRenderNonEntryOmitsMainFunc(t *testing.T) {
	mod := lower.NewModule("users", false, types.IntModeFast)
	got := string(mod.Render())

	if !strings.Contains(got, "package users") {
		t.Errorf("missing package header\noutput:\n%s", got)
	}
	if strings.Contains(got, "func main()") {
		t.Errorf("non-entry module should not emit func main()\noutput:\n%s", got)
	}
}

func TestModuleRenderEmitsImportsSorted(t *testing.T) {
	mod := lower.NewModule("main", true, types.IntModeFast)
	mod.AddImport("os")
	mod.AddImport("fmt")
	mod.AddImport("math/big")

	got := string(mod.Render())
	idxFmt := strings.Index(got, `"fmt"`)
	idxMath := strings.Index(got, `"math/big"`)
	idxOs := strings.Index(got, `"os"`)
	if idxFmt < 0 || idxMath < 0 || idxOs < 0 {
		t.Fatalf("imports missing from output:\n%s", got)
	}
	if !(idxFmt < idxMath && idxMath < idxOs) {
		t.Errorf("imports not in sorted order:\n%s", got)
	}
}

func TestModuleRenderDeduplicatesImports(t *testing.T) {
	mod := lower.NewModule("main", true, types.IntModeFast)
	mod.AddImport("fmt")
	mod.AddImport("fmt")
	mod.AddImport("fmt")

	got := string(mod.Render())
	if strings.Count(got, `"fmt"`) != 1 {
		t.Errorf("expected single fmt import, got\n%s", got)
	}
}

func TestModuleStdlibRegistry(t *testing.T) {
	mod := lower.NewModule("main", true, types.IntModeFast)
	if mod.IsStdlibModule("io") {
		t.Errorf("expected fresh module to know no stdlib modules")
	}
	mod.RegisterStdlibModule("io", "io")
	if !mod.IsStdlibModule("io") {
		t.Errorf("expected RegisterStdlibModule to record io")
	}
	if got := mod.StdlibCanonical("io"); got != "io" {
		t.Errorf("StdlibCanonical(io) = %q, want io", got)
	}
	mod.RegisterStdlibModule("native", "profiler")
	if got := mod.StdlibCanonical("native"); got != "profiler" {
		t.Errorf("StdlibCanonical(native) = %q, want profiler", got)
	}
}

func TestModuleMainBodyWrittenInsideMainFunc(t *testing.T) {
	mod := lower.NewModule("main", true, types.IntModeFast)
	mod.MainBody().WriteString("fmt.Println(42)")
	mod.AddImport("fmt")

	got := string(mod.Render())
	// The body must appear after "func main() {".
	mainStart := strings.Index(got, "func main()")
	bodyAt := strings.Index(got, "fmt.Println(42)")
	if mainStart < 0 || bodyAt < 0 || bodyAt < mainStart {
		t.Errorf("body not inside main()\noutput:\n%s", got)
	}
}
