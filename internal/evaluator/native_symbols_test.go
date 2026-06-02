package evaluator

import (
	"os"
	"path/filepath"
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

// findStdlibRoot walks up from the test directory to the stdlib/ source
// tree, or "" if absent (a checkout without source stdlib).
func findStdlibRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, "stdlib")
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// hasStdlibSource reports whether a dotted module name has a source
// stdlib file or package directory under root.
func hasStdlibSource(root, module string) bool {
	rel := strings.ReplaceAll(module, ".", string(filepath.Separator))
	if _, err := os.Stat(filepath.Join(root, rel+".gb")); err == nil {
		return true
	}
	if info, err := os.Stat(filepath.Join(root, rel)); err == nil && info.IsDir() {
		return true
	}
	return false
}

// TestNativeModuleNamesAreBacked guards against stale hand-list entries:
// every native.NativeModuleNames name must be backed by either a Go-
// native surface (functions or class exports) or a source stdlib module.
// A name with neither is dead weight that makes geblang check treat a
// bogus import as native.
func TestNativeModuleNamesAreBacked(t *testing.T) {
	root := findStdlibRoot()
	if root == "" {
		t.Skip("stdlib source tree not found")
	}
	symbols := NativeModuleSymbols()
	stale := []string{}
	for module := range native.NativeModuleNames {
		if len(symbols[module]) > 0 || hasStdlibSource(root, module) {
			continue
		}
		stale = append(stale, module)
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("NativeModuleNames entries with no Go-native surface and no stdlib source (stale): %v", stale)
	}
}
