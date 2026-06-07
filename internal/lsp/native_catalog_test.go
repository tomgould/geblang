package lsp

import (
	"sort"
	"strings"
	"testing"

	"geblang/internal/evaluator"
)

// internalCatalogModules are engine-native modules that are
// implementation bridges, not part of the public API. They are not
// surfaced in editor completion, so the catalog need not list them.
var internalCatalogModules = map[string]bool{
	"ffinative": true, "procnative": true, "sshnative": true,
	"imagenative": true,
}

// isInternalMember reports members that exist on the engine but are
// internal plumbing rather than documented public API.
func isInternalMember(module, name string) bool {
	if strings.HasSuffix(name, "Interface") {
		return true // internal protocol interface type names
	}
	if module == "sys" && strings.HasPrefix(name, "process") {
		return true // low-level primitives wrapped by the process module
	}
	return false
}

// The LSP catalog must list every user-facing native member so editor
// completion and hover are complete. Internal bridges and plumbing are
// excluded; everything else the engine exposes must be catalogued.
func TestCatalogCoversUserFacingEngineSymbols(t *testing.T) {
	eng := evaluator.NativeModuleSymbols()
	cat := CatalogNativeSymbols()
	missing := []string{}
	for module, members := range eng {
		if internalCatalogModules[module] {
			continue
		}
		catSet := cat[module]
		for name := range members {
			if isInternalMember(module, name) {
				continue
			}
			if _, ok := catSet[name]; !ok {
				missing = append(missing, module+"."+name)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("user-facing engine members missing from LSP catalog (%d):\n%s",
			len(missing), strings.Join(missing, "\n"))
	}
}
