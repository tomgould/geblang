package lsp

import (
	"sync"

	"geblang/internal/evaluator"
)

var (
	nativeSymbolsOnce  sync.Once
	nativeSymbolsCache map[string]map[string]struct{}

	engineSymbolsOnce  sync.Once
	engineSymbolsCache map[string]map[string]struct{}
)

// EngineNativeSymbols returns the authoritative per-module member sets
// from the engine, used for the cross-module symbol check. Cached.
func EngineNativeSymbols() map[string]map[string]struct{} {
	engineSymbolsOnce.Do(func() {
		engineSymbolsCache = evaluator.NativeModuleSymbols()
	})
	return engineSymbolsCache
}

// CatalogNativeSymbols returns the export set per native module, built
// from the LSP catalog so callers don't need to know that detail.
// Cached after first call. Shared with the CLI `check` command.
func CatalogNativeSymbols() map[string]map[string]struct{} {
	nativeSymbolsOnce.Do(func() {
		out := make(map[string]map[string]struct{}, len(stdlibCatalog))
		for moduleName, doc := range stdlibCatalog {
			set := make(map[string]struct{}, len(doc.functions)+len(doc.classes))
			for fn := range doc.functions {
				set[fn] = struct{}{}
			}
			for cls := range doc.classes {
				set[cls] = struct{}{}
			}
			out[moduleName] = set
		}
		nativeSymbolsCache = out
	})
	return nativeSymbolsCache
}

// nativeModuleNames returns the canonical names of every native
// (stdlib) module. Used by the code-action quick-fix suggester.
func nativeModuleNames() []string {
	cat := CatalogNativeSymbols()
	names := make([]string, 0, len(cat))
	for name := range cat {
		names = append(names, name)
	}
	return names
}
