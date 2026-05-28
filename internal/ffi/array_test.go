package ffi

import (
	"bytes"
	"math"
	"testing"
)

func TestSizeOf(t *testing.T) {
	cases := map[Type]int{
		Int8: 1, Uint8: 1,
		Int16: 2, Uint16: 2,
		Int32: 4, Uint32: 4, Float32: 4,
		Int64: 8, Uint64: 8, Float64: 8, Ptr: 8,
	}
	for ty, want := range cases {
		got, err := SizeOf(ty)
		if err != nil {
			t.Errorf("SizeOf(%s): %v", ty, err)
			continue
		}
		if got != want {
			t.Errorf("SizeOf(%s) = %d, want %d", ty, got, want)
		}
	}
}

func TestSizeOfInvalid(t *testing.T) {
	if _, err := SizeOf(CString); err == nil {
		t.Errorf("expected SizeOf(CString) to fail")
	}
	if _, err := SizeOf(Bytes); err == nil {
		t.Errorf("expected SizeOf(Bytes) to fail")
	}
}

func TestWriteReadInt32Array(t *testing.T) {
	ptr, _ := Alloc(16)
	defer Free(ptr)
	values := []any{int64(1), int64(-2), int64(3), int64(-4)}
	if err := WriteArray(ptr, Int32, values); err != nil {
		t.Fatalf("WriteArray: %v", err)
	}
	got, err := ReadArray(ptr, Int32, 4)
	if err != nil {
		t.Fatalf("ReadArray: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("length = %d, want 4", len(got))
	}
	for i, want := range []int64{1, -2, 3, -4} {
		if got[i].(int64) != want {
			t.Errorf("element %d = %v, want %d", i, got[i], want)
		}
	}
}

func TestWriteReadFloat64Array(t *testing.T) {
	ptr, _ := Alloc(24)
	defer Free(ptr)
	values := []any{1.5, -2.25, math.Pi}
	if err := WriteArray(ptr, Float64, values); err != nil {
		t.Fatalf("WriteArray: %v", err)
	}
	got, err := ReadArray(ptr, Float64, 3)
	if err != nil {
		t.Fatalf("ReadArray: %v", err)
	}
	for i, want := range []float64{1.5, -2.25, math.Pi} {
		if got[i].(float64) != want {
			t.Errorf("element %d = %v, want %v", i, got[i], want)
		}
	}
}

func TestReadArrayZeroLength(t *testing.T) {
	got, err := ReadArray(0, Int32, 0)
	if err != nil {
		t.Errorf("zero-length read: %v", err)
	}
	if got != nil && len(got) != 0 {
		t.Errorf("expected nil/empty, got %v", got)
	}
}

func TestReadArrayRejectsNullWithLength(t *testing.T) {
	if _, err := ReadArray(0, Int32, 4); err == nil {
		t.Errorf("expected NULL ptr + nonzero length to fail")
	}
}

func TestBytesViewAliasesMemory(t *testing.T) {
	ptr, _ := Alloc(4)
	defer Free(ptr)
	if err := WriteBytes(ptr, []byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	view, err := BytesView(ptr, 4)
	if err != nil {
		t.Fatalf("BytesView: %v", err)
	}
	if !bytes.Equal(view, []byte{1, 2, 3, 4}) {
		t.Errorf("view = %v, want [1 2 3 4]", view)
	}
	// Mutating the view must reach the C memory.
	view[0] = 99
	readback, _ := ReadBytes(ptr, 4)
	if readback[0] != 99 {
		t.Errorf("view write did not reach C memory: %v", readback)
	}
}

func TestBytesViewNullWithLengthRejected(t *testing.T) {
	if _, err := BytesView(0, 8); err == nil {
		t.Errorf("expected NULL ptr + nonzero length to fail")
	}
}

func TestBytesViewZeroLength(t *testing.T) {
	got, err := BytesView(0, 0)
	if err != nil {
		t.Errorf("zero-length view: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}
