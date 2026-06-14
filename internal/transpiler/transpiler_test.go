package transpiler_test

import (
	"testing"

	"geblang/internal/ast"
	"geblang/internal/transpiler"
	"geblang/internal/transpiler/types"
)

func TestTranspileWithNilModulesReturnsEmptyOutput(t *testing.T) {
	out, diags, err := transpiler.Transpile(transpiler.Input{}, transpiler.Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
	if len(out.Files) != 0 {
		t.Errorf("expected empty Files, got %d entries", len(out.Files))
	}
}

func TestTranspileWithEmptyModulesReturnsEmptyOutput(t *testing.T) {
	in := transpiler.Input{
		Modules: map[string]*ast.Program{},
		Sources: map[string]string{},
	}
	out, _, err := transpiler.Transpile(in, transpiler.Options{IntMode: types.IntModeFast})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Files == nil {
		t.Fatalf("expected non-nil Files map")
	}
}

func TestOptionsIntModeStringRoundtrip(t *testing.T) {
	cases := map[types.IntMode]string{
		types.IntModeFast:   "fast",
		types.IntModeBigInt: "bigint",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("IntMode(%d).String() = %q, want %q", m, got, want)
		}
	}
}
