package ffi

import (
	"fmt"
	"reflect"

	"github.com/ebitengine/purego"
)

// reflectType returns the Go reflect.Type that purego.RegisterFunc
// expects for a given Geblang FFI Type. RegisterFunc uses these
// concrete Go types to pick the right ABI slot (int vs xmm register
// on x86_64 SystemV, etc.).
func reflectType(t Type) (reflect.Type, error) {
	switch t {
	case Int8:
		return reflect.TypeOf(int8(0)), nil
	case Int16:
		return reflect.TypeOf(int16(0)), nil
	case Int32:
		return reflect.TypeOf(int32(0)), nil
	case Int64:
		return reflect.TypeOf(int64(0)), nil
	case Uint8:
		return reflect.TypeOf(uint8(0)), nil
	case Uint16:
		return reflect.TypeOf(uint16(0)), nil
	case Uint32:
		return reflect.TypeOf(uint32(0)), nil
	case Uint64:
		return reflect.TypeOf(uint64(0)), nil
	case Float32:
		return reflect.TypeOf(float32(0)), nil
	case Float64:
		return reflect.TypeOf(float64(0)), nil
	case Ptr:
		return reflect.TypeOf(uintptr(0)), nil
	case CString:
		return reflect.TypeOf(""), nil
	case Bytes:
		return reflect.TypeOf([]byte(nil)), nil
	case Void:
		return nil, fmt.Errorf("VOID is only valid as a return type")
	}
	return nil, fmt.Errorf("unsupported FFI type %s", t)
}

// makeCaller builds a reflect.Value pointing at a callable bound to
// the native function at addr. Once registered via purego, the
// caller value can be invoked any number of times.
func makeCaller(addr uintptr, argTypes []Type, retType Type) (reflect.Value, error) {
	in := make([]reflect.Type, len(argTypes))
	for i, t := range argTypes {
		rt, err := reflectType(t)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("arg %d: %w", i, err)
		}
		in[i] = rt
	}
	var out []reflect.Type
	if retType != Void {
		rt, err := reflectType(retType)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("return: %w", err)
		}
		out = []reflect.Type{rt}
	}
	funcType := reflect.FuncOf(in, out, false)
	holder := reflect.New(funcType)
	purego.RegisterFunc(holder.Interface(), addr)
	return holder.Elem(), nil
}

// goValueFor converts a generic Geblang-side value to the
// reflect.Value the registered trampoline expects. This layer is
// strict about widths: passing a Go int64 where Int32 is declared
// is a compile-time mismatch from the marshalling perspective and
// gets explicitly narrowed here so the libffi slot fills correctly.
//
// Phase 1a's marshalling layer is Go-native: arg values arrive as
// plain Go scalars (int64 / float64 / uintptr / string / []byte).
// The Geblang-surface conversion that turns runtime.Value into
// these scalars lands in Phase 1d alongside the stdlib glue.
func goValueFor(t Type, v any) (reflect.Value, error) {
	switch t {
	case Int8:
		i, err := asInt64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(int8(i)), nil
	case Int16:
		i, err := asInt64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(int16(i)), nil
	case Int32:
		i, err := asInt64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(int32(i)), nil
	case Int64:
		i, err := asInt64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(i), nil
	case Uint8:
		u, err := asUint64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(uint8(u)), nil
	case Uint16:
		u, err := asUint64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(uint16(u)), nil
	case Uint32:
		u, err := asUint64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(uint32(u)), nil
	case Uint64:
		u, err := asUint64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(u), nil
	case Float32:
		f, err := asFloat64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(float32(f)), nil
	case Float64:
		f, err := asFloat64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(f), nil
	case Ptr:
		u, err := asUintptr(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(u), nil
	case CString:
		s, ok := v.(string)
		if !ok {
			return reflect.Value{}, fmt.Errorf("CSTRING expects string, got %T", v)
		}
		return reflect.ValueOf(s), nil
	case Bytes:
		b, ok := v.([]byte)
		if !ok {
			return reflect.Value{}, fmt.Errorf("BYTES expects []byte, got %T", v)
		}
		return reflect.ValueOf(b), nil
	}
	return reflect.Value{}, fmt.Errorf("unsupported FFI type %s", t)
}

// goValueOut converts a reflect.Value returned by the trampoline
// into a Go-native scalar. Geblang-side conversion (int64 -> Geblang
// int, float64 -> Geblang decimal, etc.) lives in the marshalling
// glue alongside the stdlib module.
func goValueOut(t Type, v reflect.Value) any {
	switch t {
	case Int8:
		return int64(v.Int())
	case Int16:
		return int64(v.Int())
	case Int32:
		return int64(v.Int())
	case Int64:
		return v.Int()
	case Uint8, Uint16, Uint32, Uint64:
		return v.Uint()
	case Float32:
		return float64(v.Float())
	case Float64:
		return v.Float()
	case Ptr:
		return uintptr(v.Uint())
	case CString:
		return v.String()
	case Bytes:
		return v.Bytes()
	}
	return nil
}

func asInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case uint:
		return int64(x), nil
	case uint8:
		return int64(x), nil
	case uint16:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint64:
		return int64(x), nil
	case uintptr:
		return int64(x), nil
	}
	return 0, fmt.Errorf("expected integer, got %T", v)
}

func asUint64(v any) (uint64, error) {
	switch x := v.(type) {
	case int:
		return uint64(x), nil
	case int8:
		return uint64(x), nil
	case int16:
		return uint64(x), nil
	case int32:
		return uint64(x), nil
	case int64:
		return uint64(x), nil
	case uint:
		return uint64(x), nil
	case uint8:
		return uint64(x), nil
	case uint16:
		return uint64(x), nil
	case uint32:
		return uint64(x), nil
	case uint64:
		return x, nil
	case uintptr:
		return uint64(x), nil
	}
	return 0, fmt.Errorf("expected integer, got %T", v)
}

func asFloat64(v any) (float64, error) {
	switch x := v.(type) {
	case float32:
		return float64(x), nil
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case int64:
		return float64(x), nil
	}
	return 0, fmt.Errorf("expected float, got %T", v)
}

func asUintptr(v any) (uintptr, error) {
	switch x := v.(type) {
	case uintptr:
		return x, nil
	case int:
		return uintptr(x), nil
	case int64:
		return uintptr(x), nil
	case uint64:
		return uintptr(x), nil
	}
	return 0, fmt.Errorf("expected pointer-sized integer, got %T", v)
}
