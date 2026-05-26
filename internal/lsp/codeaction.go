package lsp

import (
	"fmt"
	"sort"
	"strings"

	"geblang/internal/check"
)

// codeAction handles textDocument/codeAction. The first (and currently
// only) supported quick-fix replaces an unresolved `import x;` with the
// nearest matching module name from the native catalog or the
// workspace index.
func (s *server) codeAction(params CodeActionParams) any {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return []CodeAction{}
	}
	lines := strings.Split(source, "\n")
	actions := []CodeAction{}
	for _, diag := range params.Context.Diagnostics {
		if diag.Code != "import" {
			continue
		}
		bad := extractImportPath(lines, diag.Range.Start.Line)
		if bad == "" {
			continue
		}
		for _, candidate := range bestImportCandidates(bad, s.workspace) {
			edit := buildImportReplacement(lines, diag.Range.Start.Line, bad, candidate)
			if edit == nil {
				continue
			}
			actions = append(actions, CodeAction{
				Title:       fmt.Sprintf("Replace import with %q", candidate),
				Kind:        "quickfix",
				Diagnostics: []Diagnostic{diag},
				IsPreferred: len(actions) == 0,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						params.TextDocument.URI: {*edit},
					},
				},
			})
		}
	}
	return actions
}

func extractImportPath(lines []string, line int) string {
	if line < 0 || line >= len(lines) {
		return ""
	}
	text := strings.TrimSpace(lines[line])
	if !strings.HasPrefix(text, "import ") {
		return ""
	}
	rest := strings.TrimPrefix(text, "import ")
	rest = strings.TrimSuffix(rest, ";")
	rest = strings.TrimSpace(rest)
	if idx := strings.Index(rest, " "); idx >= 0 {
		rest = rest[:idx]
	}
	return rest
}

func buildImportReplacement(lines []string, line int, bad, good string) *TextEdit {
	if line < 0 || line >= len(lines) {
		return nil
	}
	src := lines[line]
	col := strings.Index(src, bad)
	if col < 0 {
		return nil
	}
	return &TextEdit{
		Range:   Range{Start: Position{Line: line, Character: col}, End: Position{Line: line, Character: col + len(bad)}},
		NewText: good,
	}
}

// bestImportCandidates ranks candidate module names by Levenshtein
// distance against the unresolved path. Returns up to three matches
// within distance 3.
func bestImportCandidates(bad string, ws *workspaceIndex) []string {
	pool := append([]string{}, check.NativeImportModules()...)
	if ws != nil {
		pool = append(pool, ws.moduleNames()...)
	}
	type scored struct {
		name string
		dist int
	}
	results := make([]scored, 0, len(pool))
	seen := map[string]struct{}{}
	for _, name := range pool {
		if name == "" || name == bad {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		d := levenshtein(bad, name)
		if d > 3 {
			continue
		}
		results = append(results, scored{name: name, dist: d})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].dist != results[j].dist {
			return results[i].dist < results[j].dist
		}
		return results[i].name < results[j].name
	})
	if len(results) > 3 {
		results = results[:3]
	}
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.name
	}
	return out
}

// levenshtein computes the Optimal String Alignment distance (a.k.a.
// restricted Damerau-Levenshtein): edit distance with insertion,
// deletion, substitution, and adjacent transposition all weighted 1.
// Transpositions matter because the most common import typos are
// adjacent swaps ("bytse" -> "bytes").
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	rows := len(a) + 1
	cols := len(b) + 1
	d := make([][]int, rows)
	for i := range d {
		d[i] = make([]int, cols)
		d[i][0] = i
	}
	for j := 0; j < cols; j++ {
		d[0][j] = j
	}
	for i := 1; i < rows; i++ {
		for j := 1; j < cols; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min3(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				if d[i-2][j-2]+1 < d[i][j] {
					d[i][j] = d[i-2][j-2] + 1
				}
			}
		}
	}
	return d[rows-1][cols-1]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
