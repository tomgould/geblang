package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"geblang/internal/evaluator"
)

// paramWellFormed reports whether a catalog parameter string is a
// "type name" pair (optionally "type name = default"), or a variadic.
// The catalog renders these into signatures, so a malformed entry shows
// users a broken hint.
func paramWellFormed(p string) bool {
	if strings.Contains(p, "...") {
		return true
	}
	base := strings.SplitN(p, "=", 2)[0]
	return len(strings.Fields(base)) >= 2
}

// forEachCatalogDoc visits every functionDoc the catalog can surface,
// labelled by where it lives.
func forEachCatalogDoc(visit func(where, name string, f functionDoc)) {
	for n, f := range globalBuiltins {
		visit("global", n, f)
	}
	for n, f := range testBaseMethods {
		visit("test", n, f)
	}
	for ty, ms := range primitiveMethods {
		for n, f := range ms {
			visit("primitive."+ty, n, f)
		}
	}
	for mod, d := range stdlibCatalog {
		for n, f := range d.functions {
			visit(mod, n, f)
		}
		for cls, ms := range d.classMethods {
			for n, f := range ms {
				visit(mod+"."+cls, n, f)
			}
		}
	}
}

// TestCatalogSignaturesWellFormed guards catalog quality: every entry
// must carry a description and a result type (hover and signature help
// rely on both) and well-formed parameters. Names are guarded elsewhere;
// this guards the hand-written signature/doc payload the engine cannot
// supply.
func TestCatalogSignaturesWellFormed(t *testing.T) {
	bad := []string{}
	forEachCatalogDoc(func(where, name string, f functionDoc) {
		if strings.TrimSpace(f.doc) == "" {
			bad = append(bad, where+"."+name+": empty doc")
		}
		if strings.TrimSpace(f.result) == "" {
			bad = append(bad, where+"."+name+": empty result")
		}
		for _, p := range f.params {
			if !paramWellFormed(p) {
				bad = append(bad, where+"."+name+": malformed param "+strconv.Quote(p))
			}
		}
	})
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("catalog signature defects (%d):\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

// findStdlibRoot walks up to the stdlib/ source tree, or "" if absent.
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

func moduleHasStdlibSource(root, module string) bool {
	rel := strings.ReplaceAll(module, ".", string(filepath.Separator))
	if _, err := os.Stat(filepath.Join(root, rel+".gb")); err == nil {
		return true
	}
	if info, err := os.Stat(filepath.Join(root, rel)); err == nil && info.IsDir() {
		return true
	}
	return false
}

// TestCatalogHasNoPhantomNativeFunctions closes the reverse of the
// completeness guard for Go-native modules: a catalog function must
// exist on the engine, or completion suggests a call that fails at
// runtime. Class type names (catalogued for hover, not module exports)
// and source-backed modules (whose functions live in .gb, not the
// native surface) are excluded to stay false-positive-free.
func TestCatalogHasNoPhantomNativeFunctions(t *testing.T) {
	root := findStdlibRoot()
	if root == "" {
		t.Skip("stdlib source tree not found")
	}
	eng := evaluator.NativeModuleSymbols()
	phantom := []string{}
	for module, d := range stdlibCatalog {
		if internalCatalogModules[module] || moduleHasStdlibSource(root, module) {
			continue
		}
		engMembers := eng[module]
		if len(engMembers) == 0 {
			continue // not a Go-native surface
		}
		for name := range d.functions {
			if isInternalMember(module, name) {
				continue
			}
			if _, ok := engMembers[name]; !ok {
				phantom = append(phantom, module+"."+name)
			}
		}
	}
	if len(phantom) > 0 {
		sort.Strings(phantom)
		t.Fatalf("catalog lists functions the engine does not expose (%d):\n%s",
			len(phantom), strings.Join(phantom, "\n"))
	}
}
