package ffi

import (
	"math"
	"testing"
)

func TestStructLayoutSimple(t *testing.T) {
	layout, err := NewStruct([]StructField{
		{Name: "x", Type: Int32},
		{Name: "y", Type: Int32},
	})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	if layout.Size != 8 {
		t.Errorf("size = %d, want 8", layout.Size)
	}
	if layout.Align != 4 {
		t.Errorf("align = %d, want 4", layout.Align)
	}
	if off, _, _ := layout.FieldOffset("x"); off != 0 {
		t.Errorf("x offset = %d, want 0", off)
	}
	if off, _, _ := layout.FieldOffset("y"); off != 4 {
		t.Errorf("y offset = %d, want 4", off)
	}
}

func TestStructLayoutAlignment(t *testing.T) {
	// struct { int8 a; int32 b; int8 c; int64 d; } on x86_64 SystemV:
	// a@0, pad to 4 for b => b@4, c@8, pad to 8 for d => d@16,
	// total padded to align(8) = 24.
	layout, err := NewStruct([]StructField{
		{Name: "a", Type: Int8},
		{Name: "b", Type: Int32},
		{Name: "c", Type: Int8},
		{Name: "d", Type: Int64},
	})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	checks := map[string]int{"a": 0, "b": 4, "c": 8, "d": 16}
	for name, want := range checks {
		off, _, _ := layout.FieldOffset(name)
		if off != want {
			t.Errorf("%s offset = %d, want %d", name, off, want)
		}
	}
	if layout.Size != 24 {
		t.Errorf("size = %d, want 24", layout.Size)
	}
}

func TestStructDuplicateFieldRejected(t *testing.T) {
	_, err := NewStruct([]StructField{
		{Name: "x", Type: Int32},
		{Name: "x", Type: Int32},
	})
	if err == nil {
		t.Fatalf("expected duplicate-field error")
	}
}

func TestStructEmptyRejected(t *testing.T) {
	_, err := NewStruct(nil)
	if err == nil {
		t.Fatalf("expected empty-struct error")
	}
}

func TestStructUnsupportedFieldType(t *testing.T) {
	_, err := NewStruct([]StructField{
		{Name: "s", Type: CString},
	})
	if err == nil {
		t.Fatalf("expected unsupported-field error")
	}
}

func TestStructReadWriteRoundTrip(t *testing.T) {
	layout, err := NewStruct([]StructField{
		{Name: "i8", Type: Int8},
		{Name: "u32", Type: Uint32},
		{Name: "f64", Type: Float64},
		{Name: "p", Type: Ptr},
	})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	ptr, err := Alloc(layout.Size)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	defer Free(ptr)

	if err := layout.Set(ptr, "i8", int64(-7)); err != nil {
		t.Fatalf("set i8: %v", err)
	}
	if err := layout.Set(ptr, "u32", int64(4_000_000_000)); err != nil {
		t.Fatalf("set u32: %v", err)
	}
	if err := layout.Set(ptr, "f64", float64(math.Pi)); err != nil {
		t.Fatalf("set f64: %v", err)
	}
	if err := layout.Set(ptr, "p", uintptr(0xdeadbeef)); err != nil {
		t.Fatalf("set p: %v", err)
	}

	if v, _ := layout.Get(ptr, "i8"); v.(int64) != -7 {
		t.Errorf("i8 = %v, want -7", v)
	}
	if v, _ := layout.Get(ptr, "u32"); v.(uint64) != 4_000_000_000 {
		t.Errorf("u32 = %v, want 4000000000", v)
	}
	if v, _ := layout.Get(ptr, "f64"); v.(float64) != math.Pi {
		t.Errorf("f64 = %v, want pi", v)
	}
	if v, _ := layout.Get(ptr, "p"); v.(uintptr) != 0xdeadbeef {
		t.Errorf("p = %v, want 0xdeadbeef", v)
	}
}

func TestStructGetSetNullPtrRejected(t *testing.T) {
	layout, _ := NewStruct([]StructField{{Name: "x", Type: Int32}})
	if _, err := layout.Get(0, "x"); err == nil {
		t.Errorf("expected NULL get to fail")
	}
	if err := layout.Set(0, "x", int64(1)); err == nil {
		t.Errorf("expected NULL set to fail")
	}
}

func TestStructUnknownField(t *testing.T) {
	layout, _ := NewStruct([]StructField{{Name: "x", Type: Int32}})
	ptr, _ := Alloc(layout.Size)
	defer Free(ptr)
	if _, err := layout.Get(ptr, "missing"); err == nil {
		t.Errorf("expected unknown-field get to fail")
	}
	if err := layout.Set(ptr, "missing", int64(1)); err == nil {
		t.Errorf("expected unknown-field set to fail")
	}
}
