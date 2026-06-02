package evaluator

import (
	"io"

	"geblang/internal/native"
	"geblang/internal/runtime"
)

// reflectDynamicMembers are reflect members dispatched ahead of the
// builtinModules table (so they are not enumerable from e.builtins).
var reflectDynamicMembers = []string{"function", "class", "module", "classes"}

// NativeModuleSymbols returns the authoritative set of valid member
// names per native module, taken from the engine itself rather than the
// hand-maintained LSP catalog. It is the source of truth for the
// cross-module symbol check (CLI and LSP). Built once by the caller.
func NativeModuleSymbols() map[string]map[string]struct{} {
	e := New(io.Discard)
	_ = e.installBuiltinTypes(runtime.NewEnvironment())

	out := make(map[string]map[string]struct{}, len(e.builtins))
	for module, fns := range e.builtins {
		set := make(map[string]struct{}, len(fns))
		for name := range fns {
			set[name] = struct{}{}
		}
		out[module] = set
	}

	// Class exports (e.g. http.Request) live in builtinModuleValue, not
	// the function table. Include them for every native module.
	for name := range native.NativeModuleNames {
		set, ok := out[name]
		if !ok {
			set = map[string]struct{}{}
			out[name] = set
		}
		for member := range e.builtinModuleValue(name, "").Exports {
			set[member] = struct{}{}
		}
	}

	if set, ok := out["reflect"]; ok {
		for _, m := range reflectDynamicMembers {
			set[m] = struct{}{}
		}
	}
	return out
}
