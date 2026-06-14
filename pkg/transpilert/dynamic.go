package transpilert

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Dynamic indexing and as-casts over any-typed values (e.g. json.parse /
// xml.parse results). Semantics match the interpreter's index runtime and
// castValue byte for byte: dict-miss yields null, out-of-range and wrong
// key-type raise the same RuntimeError, and the boxed shapes parsers produce
// (int64/*big.Int, *big.Rat, string, bool, []any, *OrderedDict) are unwrapped
// to the transpiler's concrete Go types.

// GbTypeName reports a value's Geblang type name for diagnostics. An untagged
// enum prints as `Enum.Variant` via String(), so its type is the part before
// the dot; everything else defers to gbTypeName.
func GbTypeName(v any) string {
	if s, ok := v.(fmt.Stringer); ok {
		if rendered := s.String(); rendered != "" {
			if dot := strings.IndexByte(rendered, '.'); dot > 0 {
				return rendered[:dot]
			}
		}
	}
	return gbTypeName(v)
}

// gbTypeName reports the Geblang type name of a boxed dynamic value, matching
// runtime.Value.TypeName() for the shapes any-typed values hold.
func gbTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case int, int64, *big.Int, Int:
		return "int"
	case *big.Rat:
		return "decimal"
	case float64:
		return "float"
	case string:
		return "string"
	case []byte:
		return "bytes"
	case []any:
		return "list"
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		return "list"
	case reflect.Pointer:
		if !rv.IsNil() && hasPrefix(rv.Type().Elem().Name(), "OrderedDict[") {
			return "dict"
		}
	}
	return "any"
}

// Index returns v[key] for an any-typed receiver, matching the interpreter:
// dict -> value or null on miss, list/string/bytes -> integer index with
// negative-from-end support and out-of-range RuntimeError.
func Index(v any, key any) any {
	switch recv := v.(type) {
	case []any:
		return indexList(recv, key)
	case string:
		return indexString(recv, key)
	case []byte:
		i := indexInt(key)
		if i < 0 {
			i += len(recv)
		}
		if i < 0 || i >= len(recv) {
			panic(NewError("RuntimeError", "bytes index out of range"))
		}
		return FromInt64(int64(recv[i]))
	case nil:
		panic(NewError("RuntimeError", "null is not indexable"))
	}
	rv := reflect.ValueOf(v)
	if d, ok := orderedDictGet(rv, key); ok {
		return d
	}
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		return indexReflectSlice(rv, key)
	}
	panic(NewError("RuntimeError", gbTypeName(v)+" is not indexable"))
}

// IndexSet performs recv[key] = value for an any-typed receiver, mutating in
// place and matching the interpreter's index-assignment runtime: dict key
// add-or-overwrite (a new key appends, an existing key keeps its position),
// list element set with negative-from-end support and out-of-range
// RuntimeError, and the same "not indexable"/"does not support index
// assignment" errors. The receiver owns its backing storage (a parser result),
// so in-place mutation is safe and matches the interpreter.
func IndexSet(recv any, key any, value any) {
	switch r := recv.(type) {
	case []any:
		i := indexInt(key)
		if i < 0 {
			i += len(r)
		}
		if i < 0 || i >= len(r) {
			panic(NewError("RuntimeError", "list index out of range"))
		}
		r[i] = value
		return
	case nil:
		panic(NewError("RuntimeError", "null does not support index assignment"))
	}
	rv := reflect.ValueOf(recv)
	if orderedDictSet(rv, key, value) {
		return
	}
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		setReflectSlice(rv, key, value)
		return
	}
	panic(NewError("RuntimeError", gbTypeName(recv)+" does not support index assignment"))
}

func setReflectSlice(rv reflect.Value, key any, value any) {
	i := indexInt(key)
	n := rv.Len()
	if i < 0 {
		i += n
	}
	if i < 0 || i >= n {
		panic(NewError("RuntimeError", "list index out of range"))
	}
	rv.Index(i).Set(reflect.ValueOf(value).Convert(rv.Type().Elem()))
}

// orderedDictSet calls *OrderedDict.Set(key, value) via reflection, matching the
// interpreter's dict key-set (new key appends, existing key overwrites in
// place). Reports whether recv was an ordered dict.
func orderedDictSet(rv reflect.Value, key any, value any) bool {
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return false
	}
	elem := rv.Type().Elem()
	if elem.Kind() != reflect.Struct || !hasPrefix(elem.Name(), "OrderedDict[") {
		return false
	}
	setM := rv.MethodByName("Set")
	if !setM.IsValid() {
		return false
	}
	keyType := setM.Type().In(0)
	kv := reflect.ValueOf(unwrapKey(key))
	if !kv.IsValid() || !kv.Type().AssignableTo(keyType) {
		panic(NewError("RuntimeError", "cannot use "+gbTypeName(key)+" key in "+gbTypeName(rv.Interface())))
	}
	valType := setM.Type().In(1)
	vv := reflect.ValueOf(value)
	if !vv.IsValid() {
		vv = reflect.Zero(valType)
	} else if !vv.Type().AssignableTo(valType) {
		vv = vv.Convert(valType)
	}
	setM.Call([]reflect.Value{kv, vv})
	return true
}

func indexList(xs []any, key any) any {
	i := indexInt(key)
	if i < 0 {
		i += len(xs)
	}
	if i < 0 || i >= len(xs) {
		panic(NewError("RuntimeError", "list index out of range"))
	}
	return xs[i]
}

func indexReflectSlice(rv reflect.Value, key any) any {
	i := indexInt(key)
	n := rv.Len()
	if i < 0 {
		i += n
	}
	if i < 0 || i >= n {
		panic(NewError("RuntimeError", "list index out of range"))
	}
	return rv.Index(i).Interface()
}

func indexString(s string, key any) any {
	rs := []rune(s)
	i := indexInt(key)
	if i < 0 {
		i += len(rs)
	}
	if i < 0 || i >= len(rs) {
		panic(NewError("RuntimeError", "string index out of range"))
	}
	return string(rs[i])
}

// orderedDictGet looks key up in an *OrderedDict via its Get method; missing
// keys (and key types that cannot match the dict's key type) yield null, never
// an error, matching the interpreter's dict-miss semantics.
func orderedDictGet(rv reflect.Value, key any) (any, bool) {
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, false
	}
	elem := rv.Type().Elem()
	if elem.Kind() != reflect.Struct || !hasPrefix(elem.Name(), "OrderedDict[") {
		return nil, false
	}
	getM := rv.MethodByName("Get")
	if !getM.IsValid() {
		return nil, false
	}
	keyType := getM.Type().In(0)
	kv := reflect.ValueOf(unwrapKey(key))
	if !kv.IsValid() || !kv.Type().AssignableTo(keyType) {
		return nil, true // wrong key type for this dict: a guaranteed miss -> null
	}
	res := getM.Call([]reflect.Value{kv})
	if !res[1].Bool() {
		return nil, true
	}
	return res[0].Interface(), true
}

// unwrapKey normalizes an integer key to int64 so it matches an
// OrderedDict[int64, ...] key type; other key shapes pass through.
func unwrapKey(key any) any {
	switch k := key.(type) {
	case Int:
		if k.Big == nil {
			return k.I64
		}
		return key
	case int:
		return int64(k)
	}
	return key
}

// indexInt coerces an index key to int, matching the interpreter's indexInt
// (only int values are valid indices).
func indexInt(key any) int {
	switch k := key.(type) {
	case int64:
		return int(k)
	case int:
		return k
	case Int:
		if k.Big == nil {
			return int(k.I64)
		}
		if !k.Big.IsInt64() {
			panic(NewError("RuntimeError", "index is out of range"))
		}
		return int(k.Big.Int64())
	case *big.Int:
		if !k.IsInt64() {
			panic(NewError("RuntimeError", "index is out of range"))
		}
		return int(k.Int64())
	}
	panic(NewError("RuntimeError", "index must be int, got "+gbTypeName(key)))
}

func hasPrefix(s, prefix string) bool { return strings.HasPrefix(s, prefix) }

func utf8Valid(b []byte) bool { return utf8.Valid(b) }

// AsString casts an any-typed value to string, matching castValue: a string
// passes through, bytes decode UTF-8 (error on invalid), everything else uses
// the canonical Show form.
func AsString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		if !utf8Valid(x) {
			panic(NewError("RuntimeError", "bytes value is not valid UTF-8"))
		}
		return string(x)
	}
	return Show(v)
}

// AsInt casts an any-typed value to the BigInt-mode int representation,
// matching castValue: int passes through, decimal/float truncate toward zero,
// string parses, bool -> 1/0.
func AsInt(v any) Int {
	switch x := v.(type) {
	case int64:
		return FromInt64(x)
	case int:
		return FromInt64(int64(x))
	case *big.Int:
		return FromBig(x)
	case Int:
		return x
	case *big.Rat:
		q := new(big.Int).Quo(new(big.Int).Set(x.Num()), x.Denom())
		return FromBig(q)
	case float64:
		return FromInt64(int64(math.Trunc(x)))
	case bool:
		if x {
			return FromInt64(1)
		}
		return FromInt64(0)
	case string:
		i, err := parseIntLiteral(x)
		if err != nil {
			panic(NewError("RuntimeError", err.Error()))
		}
		return i
	}
	panic(castError(v, "int"))
}

// AsIntFast is AsInt for the default fast int mode, returning a plain int64.
func AsIntFast(v any) int64 {
	r := AsInt(v)
	if r.Big != nil && !r.Big.IsInt64() {
		// Fast mode has no big-int fallback; overflow truncates like int64(x).
		return r.Big.Int64()
	}
	return r.I64
}

// AsFloat casts an any-typed value to float64, matching castValue.
func AsFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case *big.Int:
		f, _ := new(big.Rat).SetInt(x).Float64()
		return f
	case Int:
		f, _ := new(big.Rat).SetInt(x.big()).Float64()
		return f
	case *big.Rat:
		f, _ := x.Float64()
		return f
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			panic(NewError("RuntimeError", err.Error()))
		}
		return f
	}
	panic(castError(v, "float"))
}

// AsBool casts an any-typed value to bool, matching castValue.
func AsBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case int:
		return x != 0
	case *big.Int:
		return x.Sign() != 0
	case Int:
		return x.I64 != 0 || (x.Big != nil && x.Big.Sign() != 0)
	case float64:
		return x != 0
	case *big.Rat:
		return x.Sign() != 0
	case nil:
		return false
	case string:
		switch x {
		case "true":
			return true
		case "false":
			return false
		}
	}
	panic(castError(v, "bool"))
}

// AsList casts an any-typed value to []any so a navigated value can be iterated
// or further indexed; a []any passes through, other slices are copied element-
// wise. Matches valueMatchesType (a list matches `list`/`list<any>`).
func AsList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = rv.Index(i).Interface()
		}
		return out
	}
	panic(castError(v, "list"))
}

// AsDict casts an any-typed value to *OrderedDict[string, any] so a navigated
// object can be iterated or indexed with a concrete type. A string-keyed
// ordered dict passes through; other shapes are an error.
func AsDict(v any) *OrderedDict[string, any] {
	if d, ok := v.(*OrderedDict[string, any]); ok {
		return d
	}
	panic(castError(v, "dict"))
}

func castError(v any, target string) *Error {
	return NewError("RuntimeError", fmt.Sprintf("cannot cast %s to %s", gbTypeName(v), target))
}

// parseIntLiteral mirrors ast.ParseIntLiteral: underscore separators and
// 0b/0o/0x base prefixes, error text quoting the original literal.
func parseIntLiteral(lit string) (Int, error) {
	digits := lit
	if strings.ContainsRune(digits, '_') {
		digits = strings.ReplaceAll(digits, "_", "")
	}
	base := 10
	if len(digits) > 2 && digits[0] == '0' {
		switch digits[1] {
		case 'b', 'B':
			base, digits = 2, digits[2:]
		case 'o', 'O':
			base, digits = 8, digits[2:]
		case 'x', 'X':
			base, digits = 16, digits[2:]
		}
	}
	if digits == "" {
		return Int{}, fmt.Errorf("invalid integer literal %q", lit)
	}
	v, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return Int{}, fmt.Errorf("invalid integer literal %q", lit)
	}
	return FromBig(v), nil
}
