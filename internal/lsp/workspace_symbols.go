package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
)

// workspaceSymbols handles workspace/symbol queries. Returns matches
// from the workspace indexer plus the in-memory open documents (so
// freshly-edited unsaved files still surface).
func (s *server) workspaceSymbols(query string) any {
	out := []WorkspaceSymbol{}
	for _, sym := range s.workspace.query(query, 200) {
		out = append(out, indexedSymbolToWorkspace(sym))
	}
	for uri, source := range s.openDocsCopy() {
		path := uriToPath(uri)
		for _, sym := range extractSymbols(source) {
			if query != "" && !strings.Contains(strings.ToLower(sym.name), strings.ToLower(query)) {
				continue
			}
			loc := Location{URI: uri, Range: lineColRange(sym.line, 1)}
			if path == "" {
				out = append(out, WorkspaceSymbol{Name: sym.name, Kind: sym.kind, Location: loc, ContainerName: ""})
				continue
			}
			out = append(out, WorkspaceSymbol{Name: sym.name, Kind: sym.kind, Location: loc, ContainerName: filepath.Base(path)})
		}
	}
	return out
}

func indexedSymbolToWorkspace(sym indexedSymbol) WorkspaceSymbol {
	uri := pathToURI(sym.path)
	return WorkspaceSymbol{
		Name:          sym.name,
		Kind:          sym.kind,
		Location:      Location{URI: uri, Range: lineColRange(sym.line, 1)},
		ContainerName: filepath.Base(sym.path),
	}
}

func pathToURI(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String()
}

func (s *server) openDocsCopy() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.docs))
	for k, v := range s.docs {
		out[k] = v
	}
	return out
}
