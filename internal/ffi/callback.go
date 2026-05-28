package ffi

import (
	"fmt"
	"reflect"
	"sync/atomic"

	"github.com/ebitengine/purego"
)

type CallableInvoker func(token any, args []any) (any, error)

type invokerCell struct{ fn CallableInvoker }

var callbackInvoker atomic.Pointer[invokerCell]

func SetCallableInvoker(fn CallableInvoker) {
	callbackInvoker.Store(&invokerCell{fn: fn})
}

func currentInvoker() CallableInvoker {
	if c := callbackInvoker.Load(); c != nil {
		return c.fn
	}
	return nil
}

func NewCallback(token any, argTypes []Type, retType Type) (uintptr, error) {
	if invoker := currentInvoker(); invoker == nil {
		return 0, fmt.Errorf("ffi.callback: no callable invoker registered (runtime not initialised)")
	}
	in, err := callbackArgReflectTypes(argTypes)
	if err != nil {
		return 0, err
	}
	out, err := callbackReturnReflectTypes(retType)
	if err != nil {
		return 0, err
	}
	funcType := reflect.FuncOf(in, out, false)

	bridge := reflect.MakeFunc(funcType, func(args []reflect.Value) []reflect.Value {
		invoker := currentInvoker()
		goArgs := make([]any, len(args))
		for i, v := range args {
			goArgs[i] = reflectArgToGo(argTypes[i], v)
		}
		result, err := invoker(token, goArgs)
		if err != nil {
			return defaultReturnValues(funcType)
		}
		if retType == Void {
			return nil
		}
		return []reflect.Value{goReturnToReflect(retType, result, out[0])}
	})

	return purego.NewCallback(bridge.Interface()), nil
}

// callbackArgReflectTypes rejects types purego.NewCallback can't
// handle in its argument list (floats, strings, slices). They cross
// the boundary as PTR / INT* instead; users decode them on the
// Geblang side after dereferencing.
func callbackArgReflectTypes(argTypes []Type) ([]reflect.Type, error) {
	out := make([]reflect.Type, len(argTypes))
	for i, t := range argTypes {
		switch t {
		case Int8:
			out[i] = reflect.TypeOf(int8(0))
		case Int16:
			out[i] = reflect.TypeOf(int16(0))
		case Int32:
			out[i] = reflect.TypeOf(int32(0))
		case Int64:
			out[i] = reflect.TypeOf(int64(0))
		case Uint8:
			out[i] = reflect.TypeOf(uint8(0))
		case Uint16:
			out[i] = reflect.TypeOf(uint16(0))
		case Uint32:
			out[i] = reflect.TypeOf(uint32(0))
		case Uint64:
			out[i] = reflect.TypeOf(uint64(0))
		case Ptr:
			out[i] = reflect.TypeOf(uintptr(0))
		default:
			return nil, fmt.Errorf("ffi.callback: arg %d (%s) is not supported in callback signatures; use INT*, UINT*, or PTR", i, t)
		}
	}
	return out, nil
}

func callbackReturnReflectTypes(retType Type) ([]reflect.Type, error) {
	switch retType {
	case Void:
		return nil, nil
	case Int8:
		return []reflect.Type{reflect.TypeOf(int8(0))}, nil
	case Int16:
		return []reflect.Type{reflect.TypeOf(int16(0))}, nil
	case Int32:
		return []reflect.Type{reflect.TypeOf(int32(0))}, nil
	case Int64:
		return []reflect.Type{reflect.TypeOf(int64(0))}, nil
	case Uint8:
		return []reflect.Type{reflect.TypeOf(uint8(0))}, nil
	case Uint16:
		return []reflect.Type{reflect.TypeOf(uint16(0))}, nil
	case Uint32:
		return []reflect.Type{reflect.TypeOf(uint32(0))}, nil
	case Uint64:
		return []reflect.Type{reflect.TypeOf(uint64(0))}, nil
	case Ptr:
		return []reflect.Type{reflect.TypeOf(uintptr(0))}, nil
	}
	return nil, fmt.Errorf("ffi.callback: return type %s is not supported; use VOID, INT*, UINT*, or PTR", retType)
}

func reflectArgToGo(t Type, v reflect.Value) any {
	switch t {
	case Int8, Int16, Int32, Int64:
		return v.Int()
	case Uint8, Uint16, Uint32, Uint64:
		return v.Uint()
	case Ptr:
		return uintptr(v.Uint())
	}
	return nil
}

func goReturnToReflect(t Type, value any, target reflect.Type) reflect.Value {
	switch t {
	case Int8, Int16, Int32, Int64:
		var n int64
		switch x := value.(type) {
		case int64:
			n = x
		case uint64:
			n = int64(x)
		case uintptr:
			n = int64(x)
		}
		return reflect.ValueOf(n).Convert(target)
	case Uint8, Uint16, Uint32, Uint64:
		var u uint64
		switch x := value.(type) {
		case int64:
			u = uint64(x)
		case uint64:
			u = x
		case uintptr:
			u = uint64(x)
		}
		return reflect.ValueOf(u).Convert(target)
	case Ptr:
		var u uintptr
		switch x := value.(type) {
		case uintptr:
			u = x
		case int64:
			u = uintptr(x)
		case uint64:
			u = uintptr(x)
		}
		return reflect.ValueOf(u)
	}
	return reflect.Zero(target)
}

func defaultReturnValues(funcType reflect.Type) []reflect.Value {
	out := make([]reflect.Value, funcType.NumOut())
	for i := range out {
		out[i] = reflect.Zero(funcType.Out(i))
	}
	return out
}
