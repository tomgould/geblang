package lsp

import "sync"

var (
	nativeSymbolsOnce  sync.Once
	nativeSymbolsCache map[string]map[string]struct{}
)

// catalogNativeSymbols returns the export set per native module, built
// from the LSP catalog so callers don't need to know that detail.
// Cached after first call.
func catalogNativeSymbols() map[string]map[string]struct{} {
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
	cat := catalogNativeSymbols()
	names := make([]string, 0, len(cat))
	for name := range cat {
		names = append(names, name)
	}
	return names
}
