package runtime

// FreezeShallowCopy returns a frozen shallow copy, leaving the original
// mutable. Backs `const` parameters; primitives are returned unchanged.
func FreezeShallowCopy(v Value) Value {
	switch val := v.(type) {
	case *List:
		elems := make([]Value, len(val.Elements))
		copy(elems, val.Elements)
		return &List{Elements: elems, Frozen: true, ElementTypes: append([]string(nil), val.ElementTypes...)}
	case Dict:
		entries := make(map[string]DictEntry, len(val.Entries))
		for k, e := range val.Entries {
			entries[k] = e
		}
		var order *[]string
		if val.Order != nil {
			o := append([]string(nil), *val.Order...)
			order = &o
		}
		return Dict{Entries: entries, Order: order, Frozen: true, ElementTypes: append([]string(nil), val.ElementTypes...)}
	case Set:
		elements := make(map[string]SetEntry, len(val.Elements))
		for k, e := range val.Elements {
			elements[k] = e
		}
		return Set{Elements: elements, Frozen: true, ElementTypes: append([]string(nil), val.ElementTypes...)}
	case *Instance:
		if val == nil {
			return v
		}
		fields := make(map[string]Value, len(val.Fields))
		for k, fv := range val.Fields {
			fields[k] = fv
		}
		return &Instance{Class: val.Class, Fields: fields, TypeBindings: val.TypeBindings, Frozen: true, ExtraTypeNames: val.ExtraTypeNames}
	default:
		return v
	}
}

// ShallowFreeze returns v with Frozen set to true for mutable collection and
// instance types. Primitive types are already immutable; they are returned
// unchanged. The returned value shares internal data (slice / map) with the
// original, so mutations attempted through the original are blocked once it is
// also frozen.
func ShallowFreeze(v Value) Value {
	switch val := v.(type) {
	case *List:
		val.Frozen = true
		return val
	case Dict:
		val.Frozen = true
		return val
	case Set:
		val.Frozen = true
		return val
	case *Instance:
		if val != nil {
			val.Frozen = true
		}
		return val
	default:
		return v
	}
}

// DeepFreeze recursively freezes v and all mutable values nested within it.
func DeepFreeze(v Value) Value {
	switch val := v.(type) {
	case *List:
		elems := make([]Value, len(val.Elements))
		for i, e := range val.Elements {
			elems[i] = DeepFreeze(e)
		}
		return &List{Elements: elems, Frozen: true}
	case Dict:
		entries := make(map[string]DictEntry, len(val.Entries))
		for k, entry := range val.Entries {
			entries[k] = DictEntry{Key: entry.Key, Value: DeepFreeze(entry.Value)}
		}
		return Dict{Entries: entries, Frozen: true}
	case Set:
		elements := make(map[string]SetEntry, len(val.Elements))
		for k, entry := range val.Elements {
			elements[k] = SetEntry{Value: DeepFreeze(entry.Value)}
		}
		return Set{Elements: elements, Frozen: true}
	case *Instance:
		if val != nil {
			for k, fv := range val.Fields {
				val.Fields[k] = DeepFreeze(fv)
			}
			val.Frozen = true
		}
		return val
	default:
		return v
	}
}

// IsFrozen returns true if v is frozen or is a primitive (always immutable).
func IsFrozen(v Value) bool {
	switch val := v.(type) {
	case *List:
		return val.Frozen
	case Dict:
		return val.Frozen
	case Set:
		return val.Frozen
	case *Instance:
		return val != nil && val.Frozen
	case Null, Bool, Int, SmallInt, Float, Decimal, String, Bytes:
		return true
	default:
		return true
	}
}
