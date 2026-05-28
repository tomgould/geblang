package ffi

import (
	"math"
	"runtime"
	"strings"
	"testing"
)

// libmPath picks the libm shared object for the running platform.
// libc is preferred when it exposes the math symbols directly, since
// some macOS / glibc combinations ship math in libSystem / libc.
func libmPath(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		return "/usr/lib/libSystem.B.dylib"
	case "linux":
		return "libm.so.6"
	}
	t.Skipf("ffi tests not configured for %s", runtime.GOOS)
	return ""
}

func libcPath(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		return "/usr/lib/libSystem.B.dylib"
	case "linux":
		return "libc.so.6"
	}
	t.Skipf("ffi tests not configured for %s", runtime.GOOS)
	return ""
}

func TestOpenClose(t *testing.T) {
	lib, err := Open(libmPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if lib.Path == "" {
		t.Fatalf("Library.Path empty")
	}
	if err := lib.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpenMissingLibrary(t *testing.T) {
	_, err := Open("/nonexistent/lib.so")
	if err == nil {
		t.Fatalf("expected error opening missing library")
	}
	if !strings.Contains(err.Error(), "ffi.Open") {
		t.Fatalf("error should be prefixed with ffi.Open: %v", err)
	}
}

func TestSymbolNotFound(t *testing.T) {
	lib, err := Open(libmPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()
	_, err = lib.Symbol("definitely_not_a_real_symbol", []Type{Float64}, Float64)
	if err == nil {
		t.Fatalf("expected error resolving unknown symbol")
	}
}

func TestSymbolAfterClose(t *testing.T) {
	lib, err := Open(libmPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	lib.Close()
	_, err = lib.Symbol("sin", []Type{Float64}, Float64)
	if err == nil {
		t.Fatalf("expected error after close")
	}
}

func TestCallSin(t *testing.T) {
	lib, err := Open(libmPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	sin, err := lib.Symbol("sin", []Type{Float64}, Float64)
	if err != nil {
		t.Fatalf("Symbol sin: %v", err)
	}
	cases := []struct {
		in   float64
		want float64
	}{
		{0.0, 0.0},
		{math.Pi, 0.0},
		{math.Pi / 2.0, 1.0},
	}
	for _, c := range cases {
		got, err := sin.Call([]any{c.in})
		if err != nil {
			t.Fatalf("Call sin(%v): %v", c.in, err)
		}
		f, ok := got.(float64)
		if !ok {
			t.Fatalf("Call sin returned %T, want float64", got)
		}
		if math.Abs(f-c.want) > 1e-9 {
			t.Errorf("sin(%v) = %v, want %v", c.in, f, c.want)
		}
	}
}

func TestCallSqrtMultipleArgs(t *testing.T) {
	lib, err := Open(libmPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	hypot, err := lib.Symbol("hypot", []Type{Float64, Float64}, Float64)
	if err != nil {
		t.Fatalf("Symbol hypot: %v", err)
	}
	got, err := hypot.Call([]any{3.0, 4.0})
	if err != nil {
		t.Fatalf("Call hypot(3, 4): %v", err)
	}
	f := got.(float64)
	if math.Abs(f-5.0) > 1e-9 {
		t.Errorf("hypot(3, 4) = %v, want 5.0", f)
	}
}

func TestSymbolCacheReuse(t *testing.T) {
	lib, err := Open(libmPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	first, err := lib.Symbol("cos", []Type{Float64}, Float64)
	if err != nil {
		t.Fatalf("Symbol cos: %v", err)
	}
	second, err := lib.Symbol("cos", []Type{Float64}, Float64)
	if err != nil {
		t.Fatalf("Symbol cos (cached): %v", err)
	}
	if first != second {
		t.Errorf("expected cached Symbol reuse: %p != %p", first, second)
	}
}

func TestSymbolCacheRespectsSignature(t *testing.T) {
	lib, err := Open(libmPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	a, err := lib.Symbol("cos", []Type{Float64}, Float64)
	if err != nil {
		t.Fatalf("Symbol cos[F64]: %v", err)
	}
	b, err := lib.Symbol("cos", []Type{Float32}, Float32)
	if err != nil {
		t.Fatalf("Symbol cos[F32]: %v", err)
	}
	if a == b {
		t.Errorf("different signatures should yield distinct Symbol entries")
	}
}

func TestCallArgCountMismatch(t *testing.T) {
	lib, err := Open(libmPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	sin, err := lib.Symbol("sin", []Type{Float64}, Float64)
	if err != nil {
		t.Fatalf("Symbol: %v", err)
	}
	_, err = sin.Call([]any{1.0, 2.0})
	if err == nil {
		t.Fatalf("expected arg-count mismatch error")
	}
	if !strings.Contains(err.Error(), "expected 1 args") {
		t.Errorf("error should describe the mismatch: %v", err)
	}
}

func TestCallVoidReturn(t *testing.T) {
	lib, err := Open(libcPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	// `free(NULL)` is a no-op on all glibc / musl / Darwin libc.
	free, err := lib.Symbol("free", []Type{Ptr}, Void)
	if err != nil {
		t.Fatalf("Symbol free: %v", err)
	}
	result, err := free.Call([]any{uintptr(0)})
	if err != nil {
		t.Fatalf("Call free(NULL): %v", err)
	}
	if result != nil {
		t.Errorf("VOID return should be nil, got %v", result)
	}
}

func TestCallIntegerReturn(t *testing.T) {
	lib, err := Open(libcPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	getpid, err := lib.Symbol("getpid", []Type{}, Int32)
	if err != nil {
		t.Fatalf("Symbol getpid: %v", err)
	}
	result, err := getpid.Call([]any{})
	if err != nil {
		t.Fatalf("Call getpid: %v", err)
	}
	pid, ok := result.(int64)
	if !ok {
		t.Fatalf("getpid returned %T, want int64", result)
	}
	if pid <= 0 {
		t.Errorf("getpid returned %d, expected positive", pid)
	}
}

func TestCallPointerReturn(t *testing.T) {
	lib, err := Open(libcPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lib.Close()

	// malloc(8) returns a non-null pointer; free returns it.
	mallocSym, err := lib.Symbol("malloc", []Type{Uint64}, Ptr)
	if err != nil {
		t.Fatalf("Symbol malloc: %v", err)
	}
	freeSym, err := lib.Symbol("free", []Type{Ptr}, Void)
	if err != nil {
		t.Fatalf("Symbol free: %v", err)
	}
	result, err := mallocSym.Call([]any{uint64(8)})
	if err != nil {
		t.Fatalf("Call malloc(8): %v", err)
	}
	ptr, ok := result.(uintptr)
	if !ok {
		t.Fatalf("malloc returned %T, want uintptr", result)
	}
	if ptr == 0 {
		t.Errorf("malloc(8) returned NULL")
	}
	if _, err := freeSym.Call([]any{ptr}); err != nil {
		t.Errorf("free: %v", err)
	}
}

func TestTypeString(t *testing.T) {
	cases := map[Type]string{
		Void:    "VOID",
		Int8:    "INT8",
		Int32:   "INT32",
		Uint64:  "UINT64",
		Float64: "DOUBLE",
		Ptr:     "PTR",
		CString: "CSTRING",
		Bytes:   "BYTES",
	}
	for ty, want := range cases {
		if got := ty.String(); got != want {
			t.Errorf("Type(%d).String() = %q, want %q", ty, got, want)
		}
	}
}

func TestMarshalIntegerWidthsAccepted(t *testing.T) {
	// Integer marshalling should accept any of the common Go integer
	// kinds and narrow them to the declared signature width. This
	// matters because Geblang ints will arrive as int64 from the
	// runtime layer.
	cases := []struct {
		t Type
		v any
	}{
		{Int8, int64(120)},
		{Int16, int64(30000)},
		{Int32, int64(2000000000)},
		{Int64, int64(1 << 40)},
		{Uint8, int64(200)},
		{Uint16, int64(60000)},
		{Uint32, int64(4000000000)},
		{Uint64, int64(1 << 40)},
	}
	for _, c := range cases {
		_, err := goValueFor(c.t, c.v)
		if err != nil {
			t.Errorf("goValueFor(%s, %v): %v", c.t, c.v, err)
		}
	}
}

func TestMarshalRejectsWrongType(t *testing.T) {
	if _, err := goValueFor(Int32, "not a number"); err == nil {
		t.Errorf("expected error passing string to INT32")
	}
	if _, err := goValueFor(Float64, "not a number"); err == nil {
		t.Errorf("expected error passing string to DOUBLE")
	}
	if _, err := goValueFor(CString, 42); err == nil {
		t.Errorf("expected error passing int to CSTRING")
	}
	if _, err := goValueFor(Bytes, "string-not-bytes"); err == nil {
		t.Errorf("expected error passing string to BYTES")
	}
}
