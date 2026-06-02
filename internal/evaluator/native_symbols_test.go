package evaluator

import (
	"sort"
	"testing"

	"geblang/internal/native"
)

// Every module the engine exposes natively must be recognised by
// native.NativeModuleNames so the bundler and `geblang check` treat its
// imports as native rather than unresolved source files.
func TestNativeModuleNamesCoversEngine(t *testing.T) {
	missing := []string{}
	for module := range NativeModuleSymbols() {
		if !native.IsNativeModule(module) {
			missing = append(missing, module)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("engine-native modules missing from NativeModuleNames: %v", missing)
	}
}
