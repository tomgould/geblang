package ffi

import (
	"bytes"
	"strings"
	"testing"
	"unsafe"
)

func TestAllocFreeRoundTrip(t *testing.T) {
	ptr, err := Alloc(64)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if ptr == 0 {
		t.Fatalf("Alloc returned NULL")
	}
	if err := Free(ptr); err != nil {
		t.Fatalf("Free: %v", err)
	}
}

func TestFreeNullIsNoop(t *testing.T) {
	if err := Free(0); err != nil {
		t.Fatalf("Free(0): %v", err)
	}
}

func TestAllocNegativeRejected(t *testing.T) {
	if _, err := Alloc(-1); err == nil {
		t.Fatalf("expected error allocating negative size")
	}
}

func TestWriteReadBytesRoundTrip(t *testing.T) {
	ptr, err := Alloc(8)
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	defer Free(ptr)

	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	if err := WriteBytes(ptr, data); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	got, err := ReadBytes(ptr, 8)
	if err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("round-trip mismatch: got %v, want %v", got, data)
	}
}

func TestReadBytesNullRejected(t *testing.T) {
	if _, err := ReadBytes(0, 4); err == nil {
		t.Fatalf("expected error reading from NULL")
	}
}

func TestReadBytesZeroLength(t *testing.T) {
	got, err := ReadBytes(0, 0)
	if err != nil {
		t.Fatalf("ReadBytes(0, 0): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestNewCStringRoundTrip(t *testing.T) {
	ptr, err := NewCString("hello")
	if err != nil {
		t.Fatalf("NewCString: %v", err)
	}
	defer Free(ptr)

	got, err := ReadCString(ptr)
	if err != nil {
		t.Fatalf("ReadCString: %v", err)
	}
	if got != "hello" {
		t.Errorf("CString round-trip: got %q, want %q", got, "hello")
	}
}

func TestNewCStringEmpty(t *testing.T) {
	ptr, err := NewCString("")
	if err != nil {
		t.Fatalf("NewCString(\"\"): %v", err)
	}
	defer Free(ptr)
	got, err := ReadCString(ptr)
	if err != nil {
		t.Fatalf("ReadCString: %v", err)
	}
	if got != "" {
		t.Errorf("CString round-trip: got %q, want empty", got)
	}
}

func TestNewCStringTerminatesProperly(t *testing.T) {
	ptr, err := NewCString("abc")
	if err != nil {
		t.Fatalf("NewCString: %v", err)
	}
	defer Free(ptr)
	// Manual peek at the four bytes: 'a', 'b', 'c', 0.
	got := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), 4)
	want := []byte{'a', 'b', 'c', 0}
	if !bytes.Equal(got, want) {
		t.Errorf("layout: got %v, want %v", got, want)
	}
}

func TestReadCStringNullRejected(t *testing.T) {
	if _, err := ReadCString(0); err == nil {
		t.Fatalf("expected error reading from NULL")
	}
}

// TestCStringThroughLibc round-trips a string through libc's strlen
// to validate CSTRING in marshalling against a real C function.
func TestCStringThroughLibc(t *testing.T) {
	lib, err := Open(libcPath(t))
	if err != nil {
		t.Fatalf("Open libc: %v", err)
	}
	defer lib.Close()

	strlen, err := lib.Symbol("strlen", []Type{CString}, Uint64)
	if err != nil {
		t.Fatalf("Symbol strlen: %v", err)
	}
	result, err := strlen.Call([]any{"hello world"})
	if err != nil {
		t.Fatalf("strlen: %v", err)
	}
	if u := result.(uint64); u != 11 {
		t.Errorf("strlen(\"hello world\") = %d, want 11", u)
	}
}

// TestCStringReturnThroughLibc exercises CSTRING as a return type by
// calling strerror, which returns a pointer to a static internal
// string. We compare against a known errno value.
func TestCStringReturnThroughLibc(t *testing.T) {
	lib, err := Open(libcPath(t))
	if err != nil {
		t.Fatalf("Open libc: %v", err)
	}
	defer lib.Close()

	strerror, err := lib.Symbol("strerror", []Type{Int32}, CString)
	if err != nil {
		t.Fatalf("Symbol strerror: %v", err)
	}
	// EINVAL = 22 on Linux. macOS uses the same value.
	result, err := strerror.Call([]any{int64(22)})
	if err != nil {
		t.Fatalf("strerror: %v", err)
	}
	msg, ok := result.(string)
	if !ok {
		t.Fatalf("strerror returned %T, want string", result)
	}
	if !strings.Contains(strings.ToLower(msg), "invalid") {
		t.Errorf("strerror(22) = %q; expected a message mentioning \"invalid\"", msg)
	}
}

// TestBytesArgThroughLibc exercises BYTES in-marshalling: pass two
// Go byte slices to memcmp and verify the comparison runs in C.
func TestBytesArgThroughLibc(t *testing.T) {
	lib, err := Open(libcPath(t))
	if err != nil {
		t.Fatalf("Open libc: %v", err)
	}
	defer lib.Close()

	memcmp, err := lib.Symbol("memcmp", []Type{Bytes, Bytes, Uint64}, Int32)
	if err != nil {
		t.Fatalf("Symbol memcmp: %v", err)
	}
	a := []byte("hello world")
	b := []byte("hello world")
	c := []byte("hello WORLD")

	eq, err := memcmp.Call([]any{a, b, uint64(len(a))})
	if err != nil {
		t.Fatalf("memcmp(equal): %v", err)
	}
	if eq.(int64) != 0 {
		t.Errorf("memcmp on equal slices returned %d, want 0", eq)
	}

	diff, err := memcmp.Call([]any{a, c, uint64(len(a))})
	if err != nil {
		t.Fatalf("memcmp(differ): %v", err)
	}
	if diff.(int64) == 0 {
		t.Errorf("memcmp on differing slices returned 0, want non-zero")
	}
}

func TestErrnoAfterFailedCall(t *testing.T) {
	lib, err := Open(libcPath(t))
	if err != nil {
		t.Fatalf("Open libc: %v", err)
	}
	defer lib.Close()

	// open("/path/that/does/not/exist", O_RDONLY) returns -1 and sets errno.
	open, err := lib.Symbol("open", []Type{CString, Int32}, Int32)
	if err != nil {
		t.Fatalf("Symbol open: %v", err)
	}
	result, err := open.Call([]any{"/definitely-not-a-real-path-zxyq", int64(0)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	fd := result.(int64)
	if fd != -1 {
		t.Errorf("open of bogus path returned %d, want -1", fd)
	}
	e, err := Errno()
	if err != nil {
		t.Skipf("Errno not available on this platform: %v", err)
	}
	if e == 0 {
		t.Errorf("errno should be set after failed open, got 0")
	}
}
