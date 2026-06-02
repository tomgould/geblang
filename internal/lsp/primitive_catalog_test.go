package lsp

import (
	"sort"
	"strings"
	"testing"

	"geblang/internal/native"
)

// The LSP catalog must list every primitive method the engine exposes
// (native.PrimitiveMethods is the authoritative, runtime-guarded set),
// so completion and hover stay complete. Comparison is case-insensitive
// because dispatch is.
func TestCatalogCoversPrimitiveMethods(t *testing.T) {
	missing := []string{}
	for typeName, methods := range native.PrimitiveMethods {
		lowered := map[string]bool{}
		for name := range primitiveMethods[typeName] {
			lowered[strings.ToLower(name)] = true
		}
		for _, m := range methods {
			if !lowered[strings.ToLower(m)] {
				missing = append(missing, typeName+"."+m)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("primitive methods missing from LSP catalog (%d):\n%s",
			len(missing), strings.Join(missing, "\n"))
	}
}
