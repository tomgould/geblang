package semantic

import (
	"strings"

	"geblang/internal/native"
)

// primitiveMethodSets indexes native.PrimitiveMethods (the authoritative
// per-type method lists, runtime-guarded) plus the universal conversion
// helpers, for fast membership checks.
var primitiveMethodSets = buildPrimitiveMethodSets()

func buildPrimitiveMethodSets() map[string]map[string]bool {
	conversions := map[string]bool{}
	for _, name := range native.PrimitiveConversionMethods {
		conversions[strings.ToLower(name)] = true
	}
	out := make(map[string]map[string]bool, len(native.PrimitiveMethods))
	for typeName, methods := range native.PrimitiveMethods {
		set := make(map[string]bool, len(methods)+len(conversions))
		for _, name := range methods {
			set[strings.ToLower(name)] = true
		}
		for name := range conversions {
			set[name] = true
		}
		out[typeName] = set
	}
	return out
}

// primitiveMethodLookup reports whether typeName is a known primitive
// and, if so, whether method is a recognised method on it.
func primitiveMethodLookup(typeName, method string) (isPrimitive bool, exists bool) {
	set, ok := primitiveMethodSets[typeName]
	if !ok {
		return false, false
	}
	return true, set[strings.ToLower(method)]
}
