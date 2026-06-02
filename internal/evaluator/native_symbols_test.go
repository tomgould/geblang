package evaluator

import (
	"sort"
	"strings"
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

// Every pure builtin in native.Registry (the VM's fast-path source) must
// appear in NativeModuleSymbols (the surface the evaluator, dir, and
// geblang check rely on). Otherwise the VM could recognise a call the
// analyzer would flag as unknown - a backend/tooling divergence.
func TestNativeRegistryCoveredByModuleSymbols(t *testing.T) {
	symbols := NativeModuleSymbols()
	missing := []string{}
	for _, key := range native.NewBuiltinRegistry().Keys() {
		module, name, ok := strings.Cut(key, ".")
		if !ok {
			continue
		}
		members, present := symbols[module]
		if !present {
			missing = append(missing, key)
			continue
		}
		if _, ok := members[name]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("native.Registry builtins missing from NativeModuleSymbols (VM/analyzer divergence): %v", missing)
	}
}
