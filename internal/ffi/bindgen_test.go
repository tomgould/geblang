package ffi

import (
	"strings"
	"testing"
)

func TestParseBindingManifest(t *testing.T) {
	src := []byte(`
module: sqlite
library: libsqlite3.so.0
functions:
  - name: sqlite3_open
    args: [CSTRING, PTR]
    returns: INT32
`)
	m, err := ParseBindingManifest(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Module != "sqlite" {
		t.Errorf("module = %q", m.Module)
	}
	if m.Library != "libsqlite3.so.0" {
		t.Errorf("library = %q", m.Library)
	}
	if len(m.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(m.Functions))
	}
	if m.Functions[0].Name != "sqlite3_open" {
		t.Errorf("fn name = %q", m.Functions[0].Name)
	}
}

func TestParseRequiresModule(t *testing.T) {
	if _, err := ParseBindingManifest([]byte(`library: x.so`)); err == nil {
		t.Errorf("expected missing-module error")
	}
}

func TestParseRequiresLibrary(t *testing.T) {
	if _, err := ParseBindingManifest([]byte(`module: x`)); err == nil {
		t.Errorf("expected missing-library error")
	}
}

func TestGenerateBindingsBasic(t *testing.T) {
	m := &BindingManifest{
		Module:  "math",
		Library: "libm.so.6",
		Functions: []BindingFunction{
			{Name: "sin", Args: []string{"DOUBLE"}, Returns: "DOUBLE"},
			{Name: "hypot", Args: []string{"DOUBLE", "DOUBLE"}, Returns: "DOUBLE"},
		},
	}
	got, err := GenerateBindings(m)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	checks := []string{
		"module math;",
		`ffi.dlopen("libm.so.6")`,
		`_lib.symbol("sin", [ffi.DOUBLE], ffi.DOUBLE)`,
		`_lib.symbol("hypot", [ffi.DOUBLE, ffi.DOUBLE], ffi.DOUBLE)`,
		"export func sin(decimal a0): decimal",
		"return _sin(a0) as decimal;",
		"export func hypot(decimal a0, decimal a1): decimal",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("generated output missing %q\n--- generated ---\n%s", want, got)
		}
	}
}

func TestGenerateBindingsVoidReturn(t *testing.T) {
	m := &BindingManifest{
		Module:  "libc",
		Library: "libc.so.6",
		Functions: []BindingFunction{
			{Name: "free", Args: []string{"PTR"}, Returns: "VOID"},
		},
	}
	got, _ := GenerateBindings(m)
	if !strings.Contains(got, "export func free(int a0): void {") {
		t.Errorf("void return not generated: %s", got)
	}
	if strings.Contains(got, "return _free") {
		t.Errorf("void function should not have `return`: %s", got)
	}
}

func TestGenerateBindingsStruct(t *testing.T) {
	m := &BindingManifest{
		Module:  "timeofday",
		Library: "libc.so.6",
		Structs: map[string]BindingStruct{
			"Timeval": {
				Fields: []BindingField{
					{Name: "tv_sec", Type: "INT64"},
					{Name: "tv_usec", Type: "INT64"},
				},
			},
		},
		Functions: []BindingFunction{
			{Name: "gettimeofday", Args: []string{"PTR", "PTR"}, Returns: "INT32"},
		},
	}
	got, err := GenerateBindings(m)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	checks := []string{
		"export let Timeval = ffi.StructOf([",
		`["tv_sec", ffi.INT64],`,
		`["tv_usec", ffi.INT64],`,
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}

func TestGenerateRejectsUnknownType(t *testing.T) {
	m := &BindingManifest{
		Module:  "x",
		Library: "x.so",
		Functions: []BindingFunction{
			{Name: "f", Args: []string{"UNKNOWN"}, Returns: "INT32"},
		},
	}
	if _, err := GenerateBindings(m); err == nil {
		t.Errorf("expected unknown-type error")
	}
}

func TestGenerateRejectsBadStructFieldType(t *testing.T) {
	m := &BindingManifest{
		Module:  "x",
		Library: "x.so",
		Structs: map[string]BindingStruct{
			"S": {Fields: []BindingField{{Name: "s", Type: "CSTRING"}}},
		},
	}
	if _, err := GenerateBindings(m); err == nil {
		t.Errorf("expected struct field type error (CSTRING not valid in struct)")
	}
}

func TestGenerateConstants(t *testing.T) {
	m := &BindingManifest{
		Module:  "x",
		Library: "x.so",
		Constants: []BindingConstant{
			{Name: "SQLITE_OK", Value: "0"},
			{Name: "SQLITE_ROW", Value: "100", Doc: "step produced a row"},
		},
		Functions: []BindingFunction{
			{Name: "f", Args: []string{}, Returns: "INT32"},
		},
	}
	got, _ := GenerateBindings(m)
	if !strings.Contains(got, "export const int SQLITE_OK = 0;") {
		t.Errorf("missing SQLITE_OK")
	}
	if !strings.Contains(got, "export const int SQLITE_ROW = 100;") {
		t.Errorf("missing SQLITE_ROW")
	}
}
