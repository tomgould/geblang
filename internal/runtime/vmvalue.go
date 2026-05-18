package runtime

import (
	"math"
	"math/big"
)

// VMValue is the bytecode VM's internal value representation. It uses a tag
// plus inline payloads to avoid heap-allocating an interface for the common
// primitive cases (null, bool, SmallInt, Float) that dominate the hot loop.
//
// Non-primitive values (lists, dicts, classes, closures, big.Int, decimals,
// strings, bytes, etc.) fall through to the boxed field, which holds a
// regular runtime.Value interface. The current intent is to keep GC happy
// without unsafe pointer tricks; option (b) from the Phase 10 design — a
// segregated type-tagged heap — can layer on later if profiling demands it.
//
// Layout on amd64: 1 byte kind + 7 padding + 8 int64 + 16 interface = 32 B.
type VMValue struct {
	Kind  VMKind
	I64   int64 // SmallInt payload, bool (0/1), or math.Float64bits for VMKindFloat
	Boxed Value
}

// VMKind discriminates the active payload in VMValue.
type VMKind uint8

const (
	// VMKindUnset is the zero value: the slot has not been populated. Reads
	// against this kind return Null and writes overwrite it safely. Treat
	// it as functionally identical to VMKindNull on every public surface.
	VMKindUnset VMKind = iota
	VMKindNull
	VMKindBool
	VMKindSmallInt
	VMKindFloat
	// VMKindBoxed is the catch-all: Boxed carries a runtime.Value interface
	// for any type not handled by a dedicated kind (Int / Decimal / String /
	// Bytes / List / Dict / Set / Instance / Class / Closure / Function /
	// Generator / Error / etc.).
	VMKindBoxed
)

// VMValueNull is a shared zero-value used as a stack/locals/globals
// initialiser without re-allocating.
var VMValueNull = VMValue{Kind: VMKindNull}

// VMValueFromValue wraps a runtime.Value as a VMValue, unboxing primitives
// into dedicated kinds where possible. Identity is preserved for boxed
// reference types — the same Value pointer round-trips back out of
// VMValueToValue.
func VMValueFromValue(v Value) VMValue {
	switch x := v.(type) {
	case nil:
		return VMValueNull
	case Null:
		return VMValueNull
	case Bool:
		if x.Value {
			return VMValue{Kind: VMKindBool, I64: 1}
		}
		return VMValue{Kind: VMKindBool, I64: 0}
	case SmallInt:
		return VMValue{Kind: VMKindSmallInt, I64: x.Value}
	case Float:
		return VMValue{Kind: VMKindFloat, I64: int64(math.Float64bits(x.Value))}
	default:
		return VMValue{Kind: VMKindBoxed, Boxed: v}
	}
}

// VMValueToValue converts back to a runtime.Value, materialising the
// primitive types from their inline payload and otherwise returning the
// boxed reference unchanged.
func (v VMValue) ToValue() Value {
	switch v.Kind {
	case VMKindUnset, VMKindNull:
		return Null{}
	case VMKindBool:
		return Bool{Value: v.I64 != 0}
	case VMKindSmallInt:
		return SmallInt{Value: v.I64}
	case VMKindFloat:
		return Float{Value: math.Float64frombits(uint64(v.I64))}
	case VMKindBoxed:
		if v.Boxed == nil {
			return Null{}
		}
		return v.Boxed
	default:
		return Null{}
	}
}

// VMValueSmallInt constructs an int-kind value without allocation.
func VMValueSmallInt(n int64) VMValue {
	return VMValue{Kind: VMKindSmallInt, I64: n}
}

// VMValueBool constructs a bool-kind value without allocation.
func VMValueBool(b bool) VMValue {
	if b {
		return VMValue{Kind: VMKindBool, I64: 1}
	}
	return VMValue{Kind: VMKindBool, I64: 0}
}

// VMValueFloat constructs a float-kind value without allocation.
func VMValueFloat(f float64) VMValue {
	return VMValue{Kind: VMKindFloat, I64: int64(math.Float64bits(f))}
}

// AsSmallInt returns the int64 payload when Kind == SmallInt; ok is false
// otherwise (including when the value is a runtime.Int that overflowed
// into a *big.Int).
func (v VMValue) AsSmallInt() (int64, bool) {
	if v.Kind == VMKindSmallInt {
		return v.I64, true
	}
	return 0, false
}

// AsBool returns the boolean payload when Kind == Bool.
func (v VMValue) AsBool() (bool, bool) {
	if v.Kind == VMKindBool {
		return v.I64 != 0, true
	}
	return false, false
}

// AsFloat returns the float64 payload when Kind == Float.
func (v VMValue) AsFloat() (float64, bool) {
	if v.Kind == VMKindFloat {
		return math.Float64frombits(uint64(v.I64)), true
	}
	return 0, false
}

// IsNull reports whether the value represents null (Unset or Null kinds).
func (v VMValue) IsNull() bool {
	if v.Kind == VMKindUnset || v.Kind == VMKindNull {
		return true
	}
	if v.Kind == VMKindBoxed {
		_, ok := v.Boxed.(Null)
		return ok || v.Boxed == nil
	}
	return false
}

// AsIntAny returns the int64 payload from either a SmallInt or a boxed
// runtime.Int when the latter fits in int64. Hot opcode handlers use this
// to handle the unified "int" type the language exposes.
func (v VMValue) AsIntAny() (int64, bool) {
	switch v.Kind {
	case VMKindSmallInt:
		return v.I64, true
	case VMKindBoxed:
		if i, ok := v.Boxed.(Int); ok && i.Value != nil && i.Value.IsInt64() {
			return i.Value.Int64(), true
		}
		if si, ok := v.Boxed.(SmallInt); ok {
			return si.Value, true
		}
	}
	return 0, false
}

// AsBigInt returns a *big.Int view of the value when Kind is SmallInt or
// a boxed runtime.Int. The returned pointer aliases the existing Int's
// storage when boxed; callers must not mutate it.
func (v VMValue) AsBigInt() (*big.Int, bool) {
	switch v.Kind {
	case VMKindSmallInt:
		return big.NewInt(v.I64), true
	case VMKindBoxed:
		if i, ok := v.Boxed.(Int); ok {
			return i.Value, true
		}
		if si, ok := v.Boxed.(SmallInt); ok {
			return big.NewInt(si.Value), true
		}
	}
	return nil, false
}

// TypeName returns the surface type name a VMValue projects, mirroring
// runtime.Value.TypeName(). Convenient for the few VM sites that need a
// type label without first paying for ToValue's interface materialisation.
func (v VMValue) TypeName() string {
	switch v.Kind {
	case VMKindUnset, VMKindNull:
		return "null"
	case VMKindBool:
		return "bool"
	case VMKindSmallInt:
		return "int"
	case VMKindFloat:
		return "float"
	case VMKindBoxed:
		if v.Boxed == nil {
			return "null"
		}
		return v.Boxed.TypeName()
	default:
		return "unknown"
	}
}

// Inspect returns the debug rendering of the value, mirroring
// runtime.Value.Inspect().
func (v VMValue) Inspect() string {
	return v.ToValue().Inspect()
}
