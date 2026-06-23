// Package ffi is the Geblang in-process FFI dispatch layer. It loads
// C-ABI shared libraries via dlopen + dlsym and calls into them
// through purego's RegisterFunc bridge, with a per-signature cache so
// repeated calls re-use the trampoline.
//
// This package is the Go-side foundation; the user-facing Geblang
// surface (the `ffi` stdlib module) lands in stdlib/ffi.gb in a later
// phase and dispatches into here via the native-module registration
// path used by other stateful stdlib modules.
package ffi

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
)

// Type names a C-ABI type for argument and return marshalling. The
// integer ordering is not load-bearing; do not persist Type values
// across releases.
type Type uint8

const (
	Void Type = iota
	Int8
	Int16
	Int32
	Int64
	Uint8
	Uint16
	Uint32
	Uint64
	Float32
	Float64
	Ptr
	CString
	Bytes
)

// String renders a Type for diagnostics. Mirrors the names users
// will write in Geblang (`ffi.INT32`, `ffi.PTR`, ...).
func (t Type) String() string {
	switch t {
	case Void:
		return "VOID"
	case Int8:
		return "INT8"
	case Int16:
		return "INT16"
	case Int32:
		return "INT32"
	case Int64:
		return "INT64"
	case Uint8:
		return "UINT8"
	case Uint16:
		return "UINT16"
	case Uint32:
		return "UINT32"
	case Uint64:
		return "UINT64"
	case Float32:
		return "FLOAT"
	case Float64:
		return "DOUBLE"
	case Ptr:
		return "PTR"
	case CString:
		return "CSTRING"
	case Bytes:
		return "BYTES"
	}
	return fmt.Sprintf("Type(%d)", t)
}

// Library is an opened shared library plus a cache of resolved
// symbols. Close releases the dlopen handle; the per-symbol cache
// becomes unusable afterwards.
type Library struct {
	Path string

	mu      sync.Mutex
	handle  uintptr
	closed  bool
	symbols map[symbolKey]*Symbol
}

// Open loads the shared library at path. Path resolution is the
// caller's responsibility; this layer does no policy checking.
// Policy enforcement (the capability-gating allow-list) lives in
// internal/ffi/policy.go and is applied by the Geblang surface
// before reaching Open.
func Open(path string) (*Library, error) {
	handle, err := dlOpen(path)
	if err != nil {
		return nil, fmt.Errorf("ffi.Open %q: %w", path, err)
	}
	return &Library{
		Path:    path,
		handle:  handle,
		symbols: map[symbolKey]*Symbol{},
	}, nil
}

// Close releases the dlopen handle. Subsequent Symbol calls on
// this Library return ErrClosed.
func (l *Library) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return dlClose(l.handle)
}

// Symbol resolves name in the library and prepares a callable
// Symbol bound to the given signature. Repeated calls with the
// same (name, argTypes, retType) reuse a cached entry.
func (l *Library) Symbol(name string, argTypes []Type, retType Type) (*Symbol, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrClosed
	}
	key := makeKey(name, argTypes, retType)
	if cached, ok := l.symbols[key]; ok {
		return cached, nil
	}
	addr, err := dlSym(l.handle, name)
	if err != nil {
		return nil, fmt.Errorf("ffi.Symbol %q: %w", name, err)
	}
	if addr == 0 {
		return nil, fmt.Errorf("ffi.Symbol %q: symbol resolved to nil address", name)
	}
	sym, err := newSymbol(name, addr, argTypes, retType)
	if err != nil {
		return nil, err
	}
	l.symbols[key] = sym
	return sym, nil
}

// ErrClosed is returned by Library.Symbol after Close has run.
var ErrClosed = fmt.Errorf("ffi: library is closed")

// symbolKey identifies a (name, signature) pair in the per-library
// cache. Including the signature in the key lets callers register
// the same C function under different declared signatures - useful
// when wrappers want a strict vs loose interpretation.
type symbolKey struct {
	name string
	sig  string
}

func makeKey(name string, argTypes []Type, retType Type) symbolKey {
	buf := make([]byte, 0, 1+len(argTypes))
	buf = append(buf, byte(retType))
	for _, t := range argTypes {
		buf = append(buf, byte(t))
	}
	return symbolKey{name: name, sig: string(buf)}
}

// Symbol is a resolved function ready to call.
type Symbol struct {
	Name     string
	ArgTypes []Type
	RetType  Type

	addr   uintptr
	caller reflect.Value
}

func newSymbol(name string, addr uintptr, argTypes []Type, retType Type) (*Symbol, error) {
	caller, err := makeCaller(addr, argTypes, retType)
	if err != nil {
		return nil, fmt.Errorf("ffi.Symbol %q: %w", name, err)
	}
	return &Symbol{
		Name:     name,
		ArgTypes: append([]Type(nil), argTypes...),
		RetType:  retType,
		addr:     addr,
		caller:   caller,
	}, nil
}

// Call invokes the symbol with the supplied Geblang-level args.
// The caller is responsible for converting Geblang runtime values
// into the Go-native types this layer expects; that conversion
// lives in the marshalling layer (Phase 1b). At this layer the
// args slice must already match the declared signature.
func (s *Symbol) Call(args []any) (any, error) {
	if len(args) != len(s.ArgTypes) {
		return nil, fmt.Errorf("ffi.Call %s: expected %d args, got %d", s.Name, len(s.ArgTypes), len(args))
	}
	in := make([]reflect.Value, len(args))
	for i, t := range s.ArgTypes {
		v, err := goValueFor(t, args[i])
		if err != nil {
			return nil, fmt.Errorf("ffi.Call %s: arg %d: %w", s.Name, i, err)
		}
		in[i] = v
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	out := s.caller.Call(in)
	CaptureErrno()
	if s.RetType == Void {
		return nil, nil
	}
	return goValueOut(s.RetType, out[0]), nil
}
