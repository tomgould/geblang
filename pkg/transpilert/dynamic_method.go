package transpilert

import (
	"math"
	"math/big"
	"reflect"
	"sort"
	"strings"
)

// Dynamic method dispatch over an any-typed receiver (e.g. a navigated
// json/xml result). CallMethod type-switches on recv's dynamic Go type and
// routes name to the same per-type helpers the concrete-typed lowering uses, so
// a `.length()` on a known list and on an any-list give identical results.
// Return values and unknown-method errors match the interpreter's evalMethodCall
// byte for byte. Higher-order methods (map/filter/reduce/...) take a Geblang
// closure lowered to a typed Go func that this dispatcher cannot invoke from an
// any value; those are diagnosed at lower time, never reached here.

// CallMethod dispatches name on an any-typed receiver. args are boxed to any.
func CallMethod(recv any, name string, args []any) any {
	switch v := recv.(type) {
	case string:
		return stringMethod(v, name, args)
	case []byte:
		return bytesMethod(v, name, args)
	case bool:
		return boolMethod(v, name, args)
	case float64:
		return floatMethod(v, name, args)
	case *big.Rat:
		return decimalMethod(v, name, args)
	case int64:
		return intMethod(FromInt64(v), name, args)
	case int:
		return intMethod(FromInt64(int64(v)), name, args)
	case *big.Int:
		return intMethod(FromBig(v), name, args)
	case Int:
		return intMethod(v, name, args)
	case []any:
		return listMethod(v, name, args)
	}
	rv := reflect.ValueOf(recv)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() && hasPrefix(rv.Type().Elem().Name(), "OrderedDict[") {
		return dictMethod(rv, name, args)
	}
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		return listMethod(reflectToList(rv), name, args)
	}
	panic(unknownMethod(recv, name))
}

func unknownMethod(recv any, name string) *Error {
	return NewError("RuntimeError", "unknown method "+gbTypeName(recv)+"."+name)
}

func reflectToList(rv reflect.Value) []any {
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out
}

func wantArgs(typ, name string, got, n int) {
	if got != n {
		word := "arguments"
		if n == 1 {
			word = "one argument"
		}
		if n == 0 {
			panic(NewError("RuntimeError", typ+"."+name+" expects no "+word))
		}
		panic(NewError("RuntimeError", typ+"."+name+" expects "+word))
	}
}

func stringMethod(s string, name string, args []any) any {
	switch name {
	case "length":
		wantArgs("string", name, len(args), 0)
		return StringLength(s)
	case "isEmpty":
		wantArgs("string", name, len(args), 0)
		return s == ""
	case "upper":
		wantArgs("string", name, len(args), 0)
		return strings.ToUpper(s)
	case "lower":
		wantArgs("string", name, len(args), 0)
		return strings.ToLower(s)
	case "trim":
		wantArgs("string", name, len(args), 0)
		return StringTrim(s)
	case "trimStart":
		wantArgs("string", name, len(args), 0)
		return StringTrimStart(s)
	case "trimEnd":
		wantArgs("string", name, len(args), 0)
		return StringTrimEnd(s)
	case "reverse":
		wantArgs("string", name, len(args), 0)
		return StringReverse(s)
	case "capitalize":
		wantArgs("string", name, len(args), 0)
		return StringCapitalize(s)
	case "title":
		wantArgs("string", name, len(args), 0)
		return StringTitle(s)
	case "isBlank":
		wantArgs("string", name, len(args), 0)
		return StringIsBlank(s)
	case "contains":
		wantArgs("string", name, len(args), 1)
		return strings.Contains(s, argString(args[0], "string.contains expects string"))
	case "startsWith":
		wantArgs("string", name, len(args), 1)
		return strings.HasPrefix(s, argString(args[0], "string.startsWith expects string"))
	case "endsWith":
		wantArgs("string", name, len(args), 1)
		return strings.HasSuffix(s, argString(args[0], "string.endsWith expects string"))
	case "containsIgnoreCase":
		wantArgs("string", name, len(args), 1)
		return StringContainsIgnoreCase(s, argString(args[0], "string.containsIgnoreCase expects string"))
	case "equalsIgnoreCase":
		wantArgs("string", name, len(args), 1)
		return strings.EqualFold(s, argString(args[0], "string.equalsIgnoreCase expects string"))
	case "removePrefix":
		wantArgs("string", name, len(args), 1)
		return strings.TrimPrefix(s, argString(args[0], "string.removePrefix expects string"))
	case "removeSuffix":
		wantArgs("string", name, len(args), 1)
		return strings.TrimSuffix(s, argString(args[0], "string.removeSuffix expects string"))
	case "indexOf":
		wantArgs("string", name, len(args), 1)
		return StringIndexOf(s, argString(args[0], "string.indexOf expects string"))
	case "lastIndexOf":
		wantArgs("string", name, len(args), 1)
		return StringLastIndexOf(s, argString(args[0], "string.lastIndexOf expects string"))
	case "count":
		wantArgs("string", name, len(args), 1)
		return StringCount(s, argString(args[0], "string.count expects string"))
	case "get":
		wantArgs("string", name, len(args), 1)
		return indexString(s, args[0])
	case "chars":
		wantArgs("string", name, len(args), 0)
		return strSliceToAny(StringChars(s))
	case "lines":
		wantArgs("string", name, len(args), 0)
		return strSliceToAny(StringLines(s))
	case "split":
		wantArgs("string", name, len(args), 1)
		return strSliceToAny(strings.Split(s, argString(args[0], "string.split expects string")))
	case "splitRegex":
		wantArgs("string", name, len(args), 1)
		return strSliceToAny(StringSplitRegex(s, argString(args[0], "string.splitRegex expects string")))
	case "codePoints":
		wantArgs("string", name, len(args), 0)
		return int64SliceToAny(StringCodePoints(s))
	case "repeat":
		wantArgs("string", name, len(args), 1)
		return StringRepeat(s, indexInt(args[0]))
	case "replace":
		if len(args) != 2 && len(args) != 3 {
			panic(NewError("RuntimeError", "string.replace expects old, new, and optional count"))
		}
		count := -1
		if len(args) == 3 {
			count = indexInt(args[2])
		}
		return strings.Replace(s, argString(args[0], "string.replace old value must be string"), argString(args[1], "string.replace new value must be string"), count)
	case "replaceRegex":
		wantArgs("string", name, len(args), 2)
		return StringReplaceRegex(s, argString(args[0], "string.replaceRegex expects string"), argString(args[1], "string.replaceRegex expects string"))
	case "matchesRegex":
		wantArgs("string", name, len(args), 1)
		return StringMatchesRegex(s, argString(args[0], "string.matchesRegex expects string"))
	case "padStart":
		return strPad(s, args, true)
	case "padEnd":
		return strPad(s, args, false)
	case "substring", "slice":
		return strSlice(s, args)
	case "toString":
		wantArgs("string", name, len(args), 0)
		return s
	case "toInt":
		return StringToInt(s)
	case "toFloat":
		return StringToFloat(s)
	case "toDecimal":
		return StringToDecimal(s)
	case "toBool":
		return StringToBool(s)
	}
	panic(NewError("RuntimeError", "unknown method string."+name))
}

func strPad(s string, args []any, atStart bool) string {
	if len(args) < 1 || len(args) > 2 {
		if atStart {
			panic(NewError("RuntimeError", "string.padStart expects (length[, pad])"))
		}
		panic(NewError("RuntimeError", "string.padEnd expects (length[, pad])"))
	}
	targetLen := indexInt(args[0])
	pad := " "
	if len(args) == 2 {
		p, ok := args[1].(string)
		if !ok || p == "" {
			label := "string.padStart"
			if !atStart {
				label = "string.padEnd"
			}
			panic(NewError("RuntimeError", label+": pad must be a non-empty string"))
		}
		pad = p
	}
	if atStart {
		return StringPadStart(s, targetLen, pad)
	}
	return StringPadEnd(s, targetLen, pad)
}

func strSlice(s string, args []any) string {
	if len(args) < 1 || len(args) > 2 {
		panic(NewError("RuntimeError", "string.slice expects (start[, end])"))
	}
	if len(args) == 2 {
		return StringSlice(s, indexInt(args[0]), indexInt(args[1]))
	}
	return StringSliceFrom(s, indexInt(args[0]))
}

func bytesMethod(b []byte, name string, args []any) any {
	switch name {
	case "length":
		wantArgs("bytes", name, len(args), 0)
		return int64(len(b))
	case "isEmpty":
		wantArgs("bytes", name, len(args), 0)
		return len(b) == 0
	case "get":
		wantArgs("bytes", name, len(args), 1)
		return BytesGet(b, indexInt(args[0]))
	case "toHex":
		wantArgs("bytes", name, len(args), 0)
		return BytesToHex(b)
	case "toBase64":
		wantArgs("bytes", name, len(args), 0)
		return BytesToBase64(b)
	case "toBase64Url":
		wantArgs("bytes", name, len(args), 0)
		return BytesToBase64Url(b)
	case "toList":
		wantArgs("bytes", name, len(args), 0)
		return int64SliceToAny(BytesToList(b))
	case "toString":
		switch len(args) {
		case 0:
			return string(b)
		case 1:
			return BytesToStringEncoding(b, argString(args[0], "bytes.toString encoding must be string"))
		}
		panic(NewError("RuntimeError", "bytes.toString expects optional encoding"))
	case "contains":
		wantArgs("bytes", name, len(args), 1)
		return BytesContains(b, args[0])
	case "slice":
		if len(args) < 1 || len(args) > 2 {
			panic(NewError("RuntimeError", "bytes.slice expects (start[, end])"))
		}
		if len(args) == 2 {
			return BytesSlice(b, true, indexInt(args[0]), indexInt(args[1]))
		}
		return BytesSlice(b, false, indexInt(args[0]), 0)
	}
	panic(NewError("RuntimeError", "unknown method bytes."+name))
}

func boolMethod(v bool, name string, args []any) any {
	switch name {
	case "not":
		wantArgs("bool", name, len(args), 0)
		return !v
	case "toString":
		wantArgs("bool", name, len(args), 0)
		return BoolToString(v)
	}
	panic(NewError("RuntimeError", "unknown method bool."+name))
}

func floatMethod(v float64, name string, args []any) any {
	switch name {
	case "abs":
		wantArgs("float", name, len(args), 0)
		return math.Abs(v)
	case "isZero":
		wantArgs("float", name, len(args), 0)
		return v == 0
	case "isPositive":
		wantArgs("float", name, len(args), 0)
		return v > 0
	case "isNegative":
		wantArgs("float", name, len(args), 0)
		return v < 0
	case "isNaN":
		wantArgs("float", name, len(args), 0)
		return math.IsNaN(v)
	case "isInf":
		wantArgs("float", name, len(args), 0)
		return math.IsInf(v, 0)
	case "sign":
		wantArgs("float", name, len(args), 0)
		return floatSign(v)
	case "toString":
		wantArgs("float", name, len(args), 0)
		return FloatToString(v)
	case "toInt":
		return FloatToInt(v)
	case "toFloat":
		return v
	case "toDecimal":
		return FloatToDecimal(v)
	case "toBool":
		return v != 0
	}
	panic(NewError("RuntimeError", "unknown method float."+name))
}

func floatSign(v float64) int64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

func decimalMethod(v *big.Rat, name string, args []any) any {
	switch name {
	case "abs":
		wantArgs("decimal", name, len(args), 0)
		return new(big.Rat).Abs(v)
	case "isZero":
		wantArgs("decimal", name, len(args), 0)
		return v.Sign() == 0
	case "isPositive":
		wantArgs("decimal", name, len(args), 0)
		return v.Sign() > 0
	case "isNegative":
		wantArgs("decimal", name, len(args), 0)
		return v.Sign() < 0
	case "sign":
		wantArgs("decimal", name, len(args), 0)
		return int64(v.Sign())
	case "toString":
		if len(args) == 0 {
			return DecimalToString(v)
		}
		if len(args) == 1 {
			return v.FloatString(indexInt(args[0]))
		}
		panic(NewError("RuntimeError", "decimal.toString expects optional scale"))
	case "toInt":
		return DecimalToInt(v)
	case "toFloat":
		return DecimalToFloat(v)
	case "toDecimal":
		return v
	case "toBool":
		return v.Sign() != 0
	}
	panic(NewError("RuntimeError", "unknown method decimal."+name))
}

func intMethod(v Int, name string, args []any) any {
	switch name {
	case "abs":
		wantArgs("int", name, len(args), 0)
		return FromBig(new(big.Int).Abs(v.big()))
	case "isZero":
		wantArgs("int", name, len(args), 0)
		return v.big().Sign() == 0
	case "isPositive":
		wantArgs("int", name, len(args), 0)
		return v.big().Sign() > 0
	case "isNegative":
		wantArgs("int", name, len(args), 0)
		return v.big().Sign() < 0
	case "sign":
		wantArgs("int", name, len(args), 0)
		return int64(v.big().Sign())
	case "isEven":
		wantArgs("int", name, len(args), 0)
		return v.big().Bit(0) == 0
	case "isOdd":
		wantArgs("int", name, len(args), 0)
		return v.big().Bit(0) == 1
	case "toString":
		if len(args) == 0 {
			return v.show()
		}
		panic(NewError("RuntimeError", "int.toString with a base is not supported in --native"))
	case "toInt":
		return v
	case "toFloat":
		f, _ := new(big.Rat).SetInt(v.big()).Float64()
		return f
	case "toDecimal":
		return new(big.Rat).SetInt(v.big())
	case "toBool":
		return v.big().Sign() != 0
	}
	panic(NewError("RuntimeError", "unknown method int."+name))
}

func listMethod(xs []any, name string, args []any) any {
	switch name {
	case "length":
		wantArgs("list", name, len(args), 0)
		return int64(len(xs))
	case "isEmpty":
		wantArgs("list", name, len(args), 0)
		return len(xs) == 0
	case "first":
		wantArgs("list", name, len(args), 0)
		if len(xs) == 0 {
			return nil
		}
		return xs[0]
	case "last":
		wantArgs("list", name, len(args), 0)
		if len(xs) == 0 {
			return nil
		}
		return xs[len(xs)-1]
	case "get":
		wantArgs("list", name, len(args), 1)
		return indexList(xs, args[0])
	case "contains":
		wantArgs("list", name, len(args), 1)
		for _, el := range xs {
			if dynEqual(el, args[0]) {
				return true
			}
		}
		return false
	case "indexOf":
		wantArgs("list", name, len(args), 1)
		for i, el := range xs {
			if dynEqual(el, args[0]) {
				return int64(i)
			}
		}
		return int64(-1)
	case "join":
		wantArgs("list", name, len(args), 1)
		sep := argString(args[0], "list.join separator must be a string")
		parts := make([]string, len(xs))
		for i, el := range xs {
			parts[i] = Show(el)
		}
		return strings.Join(parts, sep)
	case "slice":
		return listSlice(xs, args)
	case "reversed":
		wantArgs("list", name, len(args), 0)
		out := make([]any, len(xs))
		for i, el := range xs {
			out[len(xs)-1-i] = el
		}
		return out
	case "copy":
		wantArgs("list", name, len(args), 0)
		out := make([]any, len(xs))
		copy(out, xs)
		return out
	case "toList":
		wantArgs("list", name, len(args), 0)
		return xs
	case "sorted":
		wantArgs("list", name, len(args), 0)
		return listSorted(xs)
	case "map", "filter", "reduce", "find", "findLast", "any", "all", "count",
		"flatMap", "sortBy", "uniqueBy", "takeWhile", "dropWhile", "groupBy",
		"indexBy", "partition", "maxBy", "minBy", "sumBy", "averageBy", "scan",
		"zipWith", "containsBy", "differenceBy", "intersectionBy", "topBy",
		"binarySearchBy":
		panic(NewError("RuntimeError", "list."+name+" on an any value needs a typed list (cast to list<...> first) in --native"))
	}
	panic(NewError("RuntimeError", "unknown method list."+name))
}

func listSlice(xs []any, args []any) []any {
	if len(args) < 1 || len(args) > 2 {
		panic(NewError("RuntimeError", "list.slice expects (start[, end])"))
	}
	n := len(xs)
	start := indexInt(args[0])
	if start < 0 {
		start += n
	}
	if start < 0 {
		start = 0
	}
	if start > n {
		start = n
	}
	end := n
	if len(args) == 2 {
		end = indexInt(args[1])
		if end < 0 {
			end += n
		}
		if end < 0 {
			end = 0
		}
		if end > n {
			end = n
		}
	}
	if start >= end {
		return []any{}
	}
	out := make([]any, end-start)
	copy(out, xs[start:end])
	return out
}

func listSorted(xs []any) []any {
	out := make([]any, len(xs))
	copy(out, xs)
	sort.SliceStable(out, func(i, j int) bool { return dynLess(out[i], out[j]) })
	return out
}

func dictMethod(rv reflect.Value, name string, args []any) any {
	switch name {
	case "length":
		wantArgs("dict", name, len(args), 0)
		return int64(dictLen(rv))
	case "isEmpty":
		wantArgs("dict", name, len(args), 0)
		return dictLen(rv) == 0
	case "keys":
		wantArgs("dict", name, len(args), 0)
		return dictKeys(rv)
	case "values":
		wantArgs("dict", name, len(args), 0)
		return dictValues(rv)
	case "items", "entries":
		wantArgs("dict", name, len(args), 0)
		keys := dictKeys(rv)
		vals := dictValues(rv)
		out := make([]any, len(keys))
		for i := range keys {
			out[i] = []any{keys[i], vals[i]}
		}
		return out
	case "get":
		wantArgs("dict", name, len(args), 1)
		val, _ := orderedDictGet(rv, args[0])
		return val
	case "hasKey", "contains":
		wantArgs("dict", name, len(args), 1)
		_, present := orderedDictHas(rv, args[0])
		return present
	case "set", "insert", "delete", "remove", "clear", "merge", "copy", "deepCopy":
		panic(NewError("RuntimeError", "dict."+name+" on an any value needs a typed dict (cast to dict<...> first) in --native"))
	}
	panic(NewError("RuntimeError", "unknown method dict."+name))
}

// dictLen / dictKeys / dictValues reflect over an *OrderedDict[K,V] so dispatch
// needs no type parameters; the dict's insertion order is preserved.
func dictLen(rv reflect.Value) int {
	m := rv.MethodByName("Len")
	return int(m.Call(nil)[0].Int())
}

func dictKeys(rv reflect.Value) []any {
	ks := rv.MethodByName("Keys").Call(nil)[0]
	out := make([]any, ks.Len())
	for i := 0; i < ks.Len(); i++ {
		out[i] = ks.Index(i).Interface()
	}
	return out
}

func dictValues(rv reflect.Value) []any {
	vs := rv.MethodByName("Values").Call(nil)[0]
	out := make([]any, vs.Len())
	for i := 0; i < vs.Len(); i++ {
		out[i] = vs.Index(i).Interface()
	}
	return out
}

// orderedDictHas reports presence, treating a wrong-key-type as a guaranteed
// miss, matching the interpreter's hasKey/contains on a dict.
func orderedDictHas(rv reflect.Value, key any) (any, bool) {
	getM := rv.MethodByName("Get")
	keyType := getM.Type().In(0)
	kv := reflect.ValueOf(unwrapKey(key))
	if !kv.IsValid() || !kv.Type().AssignableTo(keyType) {
		return nil, false
	}
	res := getM.Call([]reflect.Value{kv})
	return res[0].Interface(), res[1].Bool()
}

// dynEqual mirrors the interpreter's value equality for the shapes any values
// hold: numbers compare by exact rational across int/decimal, strings/bools by
// value, null by identity, lists element-wise.
func dynEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if ra, oka := toRat(a); oka {
		if rb, okb := toRat(b); okb {
			return ra.Cmp(rb) == 0
		}
		return false
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !dynEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// dynLess gives the natural ordering for list.sorted on homogeneous numeric or
// string lists; mismatched types raise the interpreter's compare error.
func dynLess(a, b any) bool {
	if ra, oka := toRat(a); oka {
		if rb, okb := toRat(b); okb {
			return ra.Cmp(rb) < 0
		}
	}
	sa, oka := a.(string)
	sb, okb := b.(string)
	if oka && okb {
		return sa < sb
	}
	panic(NewError("RuntimeError", "cannot compare "+gbTypeName(a)+" and "+gbTypeName(b)))
}

// toRat converts a boxed int/decimal value to an exact rational; floats use
// their exact rational unless non-finite.
func toRat(v any) (*big.Rat, bool) {
	switch x := v.(type) {
	case int64:
		return new(big.Rat).SetInt64(x), true
	case int:
		return new(big.Rat).SetInt64(int64(x)), true
	case *big.Int:
		return new(big.Rat).SetInt(x), true
	case Int:
		return new(big.Rat).SetInt(x.big()), true
	case *big.Rat:
		return x, true
	case float64:
		if r := new(big.Rat).SetFloat64(x); r != nil {
			return r, true
		}
	}
	return nil, false
}

func argString(v any, msg string) string {
	s, ok := v.(string)
	if !ok {
		panic(NewError("RuntimeError", msg))
	}
	return s
}

func strSliceToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func int64SliceToAny(ns []int64) []any {
	out := make([]any, len(ns))
	for i, n := range ns {
		out[i] = n
	}
	return out
}
