package lower

import (
	"testing"

	"geblang/internal/native"
	"geblang/internal/transpiler/types"
)

// kindToPrimitiveName maps a transpiler type kind to its native
// PrimitiveMethods key, so the guard can validate every table row.
var kindToPrimitiveName = map[types.Kind]string{
	types.KindString:  "string",
	types.KindList:    "list",
	types.KindDict:    "dict",
	types.KindSet:     "set",
	types.KindBytes:   "bytes",
	types.KindInt:     "int",
	types.KindFloat:   "float",
	types.KindDecimal: "decimal",
	types.KindBool:    "bool",
}

// TestBuiltinMethodTableHasNoPhantomRows guards that every (kind, method)
// row lowers a method the engine actually recognises on that primitive,
// counting the conversion helpers recognised on every primitive.
func TestBuiltinMethodTableHasNoPhantomRows(t *testing.T) {
	isConversion := func(name string) bool {
		for _, m := range native.PrimitiveConversionMethods {
			if m == name {
				return true
			}
		}
		return false
	}
	for key := range builtinMethodTable {
		primName, ok := kindToPrimitiveName[key.kind]
		if !ok {
			t.Fatalf("table row for unmapped kind %v (method %q)", key.kind, key.name)
		}
		if isConversion(key.name) {
			continue
		}
		methods := native.PrimitiveMethods[primName]
		found := false
		for _, m := range methods {
			if m == key.name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("phantom row: %q is not a native PrimitiveMethod of %s", key.name, primName)
		}
	}
}
