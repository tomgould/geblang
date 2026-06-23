package ffi

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Alloc requests n bytes from the system allocator (libc malloc on
// Linux/macOS, msvcrt on Windows) and returns the pointer as
// uintptr. The caller is responsible for handing the value back to
// Free; Geblang's GC does not track FFI-owned memory.
//
// Errors with the alloc reason rather than panicking when n == 0 or
// malloc returns NULL, so policy code can map them onto a catchable
// Geblang RuntimeError.
func Alloc(n int) (uintptr, error) {
	if n < 0 {
		return 0, fmt.Errorf("ffi.Alloc: negative size %d", n)
	}
	fn, err := libcMalloc()
	if err != nil {
		return 0, err
	}
	ptr := fn(uint64(n))
	if ptr == 0 {
		return 0, fmt.Errorf("ffi.Alloc(%d): malloc returned NULL", n)
	}
	return ptr, nil
}

// Free releases memory previously allocated by Alloc (or by any
// other code path that promises libc-compatible allocator
// ownership). Calling Free(0) is a no-op, matching libc's free(NULL).
func Free(ptr uintptr) error {
	if ptr == 0 {
		return nil
	}
	fn, err := libcFree()
	if err != nil {
		return err
	}
	fn(ptr)
	return nil
}

// ReadBytes copies n bytes starting at ptr into a fresh Go byte
// slice. The caller is responsible for asserting that the region is
// readable; on Linux a bad pointer triggers SIGSEGV that this
// function cannot catch.
func ReadBytes(ptr uintptr, n int) ([]byte, error) {
	if n < 0 {
		return nil, fmt.Errorf("ffi.ReadBytes: negative length %d", n)
	}
	if n == 0 {
		return []byte{}, nil
	}
	if ptr == 0 {
		return nil, fmt.Errorf("ffi.ReadBytes: NULL pointer")
	}
	src := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), n)
	out := make([]byte, n)
	copy(out, src)
	return out, nil
}

// WriteBytes copies data into the C-side buffer at ptr. The caller
// is responsible for the region being writable and large enough.
func WriteBytes(ptr uintptr, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if ptr == 0 {
		return fmt.Errorf("ffi.WriteBytes: NULL pointer")
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(data))
	copy(dst, data)
	return nil
}

// ReadCString reads bytes starting at ptr up to the first null
// terminator and returns the result as a Go string. The terminator
// is consumed but not included.
func ReadCString(ptr uintptr) (string, error) {
	if ptr == 0 {
		return "", fmt.Errorf("ffi.ReadCString: NULL pointer")
	}
	// Walk byte-by-byte; this layer cannot trust a length hint from
	// callers. Cap at 1 MiB so a missing terminator does not loop
	// forever; 1 MiB is well above any realistic C-string return.
	const maxLen = 1 << 20
	base := unsafe.Pointer(ptr)
	for i := 0; i < maxLen; i++ {
		b := *(*byte)(unsafe.Add(base, i))
		if b == 0 {
			out := unsafe.Slice((*byte)(base), i)
			result := make([]byte, i)
			copy(result, out)
			return string(result), nil
		}
	}
	return "", fmt.Errorf("ffi.ReadCString: no null terminator within %d bytes", maxLen)
}

// NewCString allocates a null-terminated copy of s on the libc heap
// and returns the pointer. The caller must Free it; Geblang's GC
// does not track FFI-owned memory.
func NewCString(s string) (uintptr, error) {
	n := len(s) + 1
	ptr, err := Alloc(n)
	if err != nil {
		return 0, err
	}
	if len(s) > 0 {
		if err := WriteBytes(ptr, []byte(s)); err != nil {
			Free(ptr)
			return 0, err
		}
	}
	// Null-terminate.
	*(*byte)(unsafe.Pointer(ptr + uintptr(len(s)))) = 0
	return ptr, nil
}

// Errno returns the value of the C `errno` variable on the calling
// OS thread. Reading errno meaningfully requires the immediately
// preceding FFI call to have set it; the value is not stable across
// goroutine scheduling boundaries.
func Errno() (int, error) {
	loc, err := errnoLocation()
	if err != nil {
		return 0, err
	}
	return int(*(*int32)(unsafe.Pointer(loc))), nil
}

// libc lookups are cached behind a sync.Once so the dlopen happens
// once per process. We bind to whichever shared object exposes the
// allocator + errno location on each supported platform.
var (
	libcOnce   sync.Once
	libcOpen   *Library
	libcErr    error
	mallocFn   func(uint64) uintptr
	freeFn     func(uintptr)
	errnoLocFn func() uintptr
)

func libcInit() {
	libcOnce.Do(func() {
		path := libcShared()
		if path == "" {
			libcErr = fmt.Errorf("ffi: libc binding not configured for %s", runtime.GOOS)
			return
		}
		lib, err := Open(path)
		if err != nil {
			libcErr = err
			return
		}
		libcOpen = lib

		mallocAddr, err := dlSym(lib.handle, "malloc")
		if err != nil {
			libcErr = fmt.Errorf("ffi: dlsym malloc: %w", err)
			return
		}
		purego.RegisterFunc(&mallocFn, mallocAddr)

		freeAddr, err := dlSym(lib.handle, "free")
		if err != nil {
			libcErr = fmt.Errorf("ffi: dlsym free: %w", err)
			return
		}
		purego.RegisterFunc(&freeFn, freeAddr)

		errnoSym := errnoLocSymbol()
		if errnoSym != "" {
			errnoAddr, err := dlSym(lib.handle, errnoSym)
			if err == nil {
				purego.RegisterFunc(&errnoLocFn, errnoAddr)
			}
		}
	})
}

func libcMalloc() (func(uint64) uintptr, error) {
	libcInit()
	if libcErr != nil {
		return nil, libcErr
	}
	return mallocFn, nil
}

func libcFree() (func(uintptr), error) {
	libcInit()
	if libcErr != nil {
		return nil, libcErr
	}
	return freeFn, nil
}

func errnoLocation() (uintptr, error) {
	libcInit()
	if libcErr != nil {
		return 0, libcErr
	}
	if errnoLocFn == nil {
		return 0, fmt.Errorf("ffi.Errno: errno location not available on %s", runtime.GOOS)
	}
	return errnoLocFn(), nil
}

// libcShared picks the shared object that hosts malloc/free + errno
// location on each supported platform. Linux uses libc.so.6, macOS
// folds libc into libSystem, Windows uses msvcrt.
func libcShared() string {
	switch runtime.GOOS {
	case "linux":
		return "libc.so.6"
	case "darwin":
		return "/usr/lib/libSystem.B.dylib"
	case "windows":
		return "msvcrt.dll"
	}
	return ""
}

// errnoLocSymbol returns the per-platform symbol name that resolves
// to a function returning the address of the thread-local errno.
// Linux: __errno_location. macOS / FreeBSD: __error. Windows
// exposes _errno directly.
func errnoLocSymbol() string {
	switch runtime.GOOS {
	case "linux":
		return "__errno_location"
	case "darwin", "freebsd":
		return "__error"
	case "windows":
		return "_errno"
	}
	return ""
}
