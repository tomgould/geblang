package ffi

import (
	"fmt"
	"math"
	"unsafe"
)

type StructField struct {
	Name   string
	Type   Type
	Offset int
}

type StructLayout struct {
	Fields []StructField
	Size   int
	Align  int

	index map[string]int
}

func NewStruct(fieldsInOrder []StructField) (*StructLayout, error) {
	if len(fieldsInOrder) == 0 {
		return nil, fmt.Errorf("ffi.Struct: at least one field required")
	}
	layout := &StructLayout{index: make(map[string]int, len(fieldsInOrder))}
	off := 0
	for _, f := range fieldsInOrder {
		if _, dup := layout.index[f.Name]; dup {
			return nil, fmt.Errorf("ffi.Struct: duplicate field %q", f.Name)
		}
		sz, err := typeSize(f.Type)
		if err != nil {
			return nil, fmt.Errorf("ffi.Struct: field %q: %w", f.Name, err)
		}
		align := sz
		off = alignUp(off, align)
		f.Offset = off
		layout.index[f.Name] = len(layout.Fields)
		layout.Fields = append(layout.Fields, f)
		off += sz
		if align > layout.Align {
			layout.Align = align
		}
	}
	layout.Size = alignUp(off, layout.Align)
	return layout, nil
}

func (s *StructLayout) FieldOffset(name string) (int, Type, bool) {
	i, ok := s.index[name]
	if !ok {
		return 0, 0, false
	}
	return s.Fields[i].Offset, s.Fields[i].Type, true
}

func (s *StructLayout) Get(ptr uintptr, name string) (any, error) {
	if ptr == 0 {
		return nil, fmt.Errorf("ffi.Struct.get: NULL pointer")
	}
	offset, t, ok := s.FieldOffset(name)
	if !ok {
		return nil, fmt.Errorf("ffi.Struct.get: unknown field %q", name)
	}
	addr := unsafe.Pointer(ptr + uintptr(offset))
	return readPrimitive(addr, t)
}

func (s *StructLayout) Set(ptr uintptr, name string, value any) error {
	if ptr == 0 {
		return fmt.Errorf("ffi.Struct.set: NULL pointer")
	}
	offset, t, ok := s.FieldOffset(name)
	if !ok {
		return fmt.Errorf("ffi.Struct.set: unknown field %q", name)
	}
	addr := unsafe.Pointer(ptr + uintptr(offset))
	return writePrimitive(addr, t, value)
}

func typeSize(t Type) (int, error) {
	switch t {
	case Int8, Uint8:
		return 1, nil
	case Int16, Uint16:
		return 2, nil
	case Int32, Uint32, Float32:
		return 4, nil
	case Int64, Uint64, Float64, Ptr:
		return 8, nil
	}
	return 0, fmt.Errorf("type %s is not valid as a struct field", t)
}

func alignUp(off, align int) int {
	if align <= 1 {
		return off
	}
	rem := off % align
	if rem == 0 {
		return off
	}
	return off + (align - rem)
}

func readPrimitive(addr unsafe.Pointer, t Type) (any, error) {
	switch t {
	case Int8:
		return int64(*(*int8)(addr)), nil
	case Int16:
		return int64(*(*int16)(addr)), nil
	case Int32:
		return int64(*(*int32)(addr)), nil
	case Int64:
		return *(*int64)(addr), nil
	case Uint8:
		return uint64(*(*uint8)(addr)), nil
	case Uint16:
		return uint64(*(*uint16)(addr)), nil
	case Uint32:
		return uint64(*(*uint32)(addr)), nil
	case Uint64:
		return *(*uint64)(addr), nil
	case Float32:
		bits := *(*uint32)(addr)
		return float64(math.Float32frombits(bits)), nil
	case Float64:
		bits := *(*uint64)(addr)
		return math.Float64frombits(bits), nil
	case Ptr:
		return *(*uintptr)(addr), nil
	}
	return nil, fmt.Errorf("read: unsupported type %s", t)
}

func writePrimitive(addr unsafe.Pointer, t Type, value any) error {
	switch t {
	case Int8:
		i, err := asInt64(value)
		if err != nil {
			return err
		}
		*(*int8)(addr) = int8(i)
	case Int16:
		i, err := asInt64(value)
		if err != nil {
			return err
		}
		*(*int16)(addr) = int16(i)
	case Int32:
		i, err := asInt64(value)
		if err != nil {
			return err
		}
		*(*int32)(addr) = int32(i)
	case Int64:
		i, err := asInt64(value)
		if err != nil {
			return err
		}
		*(*int64)(addr) = i
	case Uint8:
		u, err := asUint64(value)
		if err != nil {
			return err
		}
		*(*uint8)(addr) = uint8(u)
	case Uint16:
		u, err := asUint64(value)
		if err != nil {
			return err
		}
		*(*uint16)(addr) = uint16(u)
	case Uint32:
		u, err := asUint64(value)
		if err != nil {
			return err
		}
		*(*uint32)(addr) = uint32(u)
	case Uint64:
		u, err := asUint64(value)
		if err != nil {
			return err
		}
		*(*uint64)(addr) = u
	case Float32:
		f, err := asFloat64(value)
		if err != nil {
			return err
		}
		*(*uint32)(addr) = math.Float32bits(float32(f))
	case Float64:
		f, err := asFloat64(value)
		if err != nil {
			return err
		}
		*(*uint64)(addr) = math.Float64bits(f)
	case Ptr:
		u, err := asUintptr(value)
		if err != nil {
			return err
		}
		*(*uintptr)(addr) = u
	default:
		return fmt.Errorf("write: unsupported type %s", t)
	}
	return nil
}

