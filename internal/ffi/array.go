package ffi

import (
	"fmt"
	"math"
	"unsafe"
)

func SizeOf(t Type) (int, error) {
	return typeSize(t)
}

func WriteArray(ptr uintptr, t Type, values []any) error {
	if ptr == 0 && len(values) > 0 {
		return fmt.Errorf("ffi.writeArray: NULL pointer with %d values", len(values))
	}
	sz, err := typeSize(t)
	if err != nil {
		return fmt.Errorf("ffi.writeArray: %w", err)
	}
	for i, v := range values {
		addr := unsafe.Pointer(ptr + uintptr(i*sz))
		if err := writePrimitive(addr, t, v); err != nil {
			return fmt.Errorf("ffi.writeArray: element %d: %w", i, err)
		}
	}
	return nil
}

func ReadArray(ptr uintptr, t Type, length int) ([]any, error) {
	if length < 0 {
		return nil, fmt.Errorf("ffi.readArray: negative length %d", length)
	}
	if length == 0 {
		return nil, nil
	}
	if ptr == 0 {
		return nil, fmt.Errorf("ffi.readArray: NULL pointer with length %d", length)
	}
	sz, err := typeSize(t)
	if err != nil {
		return nil, fmt.Errorf("ffi.readArray: %w", err)
	}
	out := make([]any, length)
	for i := 0; i < length; i++ {
		addr := unsafe.Pointer(ptr + uintptr(i*sz))
		v, err := readPrimitive(addr, t)
		if err != nil {
			return nil, fmt.Errorf("ffi.readArray: element %d: %w", i, err)
		}
		out[i] = v
	}
	return out, nil
}

// BytesView constructs a Go byte slice that aliases the C-side
// buffer at ptr for len bytes. No copy is performed. The caller
// must guarantee that the underlying memory outlives every use of
// the returned slice; once the C side frees it, the slice is
// invalid and every read or write through it is undefined.
func BytesView(ptr uintptr, length int) ([]byte, error) {
	if length < 0 {
		return nil, fmt.Errorf("ffi.bytesView: negative length %d", length)
	}
	if length == 0 {
		return []byte{}, nil
	}
	if ptr == 0 {
		return nil, fmt.Errorf("ffi.bytesView: NULL pointer with length %d", length)
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(ptr)), length), nil
}

// Float64FromBits exists so test code can compare exact bit
// patterns. Trivially exported for symmetry with math.Float64bits.
func Float64FromBits(bits uint64) float64 {
	return math.Float64frombits(bits)
}
