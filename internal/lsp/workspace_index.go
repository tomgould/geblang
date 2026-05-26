package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// workspaceIndex tracks top-level symbols across every .gb file in the
// open workspace roots. Filled lazily on initialize and refreshed
// per-file on didSave. Safe for concurrent reads.
type workspaceIndex struct {
	mu      sync.RWMutex
	entries map[string]workspaceFileEntry // absolute path -> entry
	ready   bool
}

type workspaceFileEntry struct {
	mtime   time.Time
	symbols []indexedSymbol
}

type indexedSymbol struct {
	name   string
	kind   int
	line   int // 1-based
	path   string
	detail string
}

func newWorkspaceIndex() *workspaceIndex {
	return &workspaceIndex{entries: map[string]workspaceFileEntry{}}
}

// bootstrap walks each root and indexes every .gb file. Safe to call
// concurrently with reads; later refresh calls overlay newer entries.
func (w *workspaceIndex) bootstrap(roots []string) {
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if shouldSkipDir(entry.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".gb") {
				return nil
			}
			w.refreshFile(path)
			return nil
		})
	}
	w.mu.Lock()
	w.ready = true
	w.mu.Unlock()
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".geblang-cache", "build", "node_modules", ".git", "docs/site":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// refreshFile re-indexes a single file by absolute path. No-op if the
// file is gone or unreadable; entries for vanished files stay until
// the next bootstrap.
func (w *workspaceIndex) refreshFile(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	info, err := os.Stat(abs)
	if err != nil {
		w.mu.Lock()
		delete(w.entries, abs)
		w.mu.Unlock()
		return
	}
	w.mu.RLock()
	if entry, ok := w.entries[abs]; ok && entry.mtime.Equal(info.ModTime()) {
		w.mu.RUnlock()
		return
	}
	w.mu.RUnlock()
	source, err := os.ReadFile(abs)
	if err != nil {
		return
	}
	syms := extractSymbols(string(source))
	out := make([]indexedSymbol, 0, len(syms))
	for _, s := range syms {
		out = append(out, indexedSymbol{name: s.name, kind: s.kind, line: s.line, path: abs, detail: s.detail})
	}
	w.mu.Lock()
	w.entries[abs] = workspaceFileEntry{mtime: info.ModTime(), symbols: out}
	w.mu.Unlock()
}

// query returns symbols whose name contains the substring (case-insensitive).
// Empty query returns all indexed symbols, capped at limit for sanity.
func (w *workspaceIndex) query(needle string, limit int) []indexedSymbol {
	needle = strings.ToLower(needle)
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]indexedSymbol, 0, 64)
	for _, entry := range w.entries {
		for _, sym := range entry.symbols {
			if needle == "" || strings.Contains(strings.ToLower(sym.name), needle) {
				out = append(out, sym)
				if limit > 0 && len(out) >= limit {
					return out
				}
			}
		}
	}
	return out
}

// moduleNames returns the canonical (filename-stem) names of all
// indexed files. Used by the code-action suggester for project modules
// alongside the native module list.
func (w *workspaceIndex) moduleNames() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]string, 0, len(w.entries))
	for path := range w.entries {
		base := filepath.Base(path)
		stem := strings.TrimSuffix(base, ".gb")
		out = append(out, stem)
	}
	return out
}
