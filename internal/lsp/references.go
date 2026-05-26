package lsp

import (
	"strings"
	"unicode"
)

// references handles textDocument/references with single-file scope.
// Returns every occurrence of the identifier under the cursor in the
// current document. Matches whole-word boundaries to avoid spuriously
// hitting substrings.
func (s *server) references(params ReferenceParams) any {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return []Location{}
	}
	word := wordAtPosition(source, params.Position.Line, params.Position.Character)
	if word == "" {
		return []Location{}
	}
	return identifierOccurrences(params.TextDocument.URI, source, word)
}

// identifierOccurrences scans source for every whole-word match of
// `word` and returns each as an LSP Location.
func identifierOccurrences(uri, source, word string) []Location {
	out := []Location{}
	lines := strings.Split(source, "\n")
	for lineIdx, line := range lines {
		start := 0
		for {
			idx := strings.Index(line[start:], word)
			if idx < 0 {
				break
			}
			absolute := start + idx
			if isWordBoundary(line, absolute, len(word)) {
				out = append(out, Location{
					URI: uri,
					Range: Range{
						Start: Position{Line: lineIdx, Character: absolute},
						End:   Position{Line: lineIdx, Character: absolute + len(word)},
					},
				})
			}
			start = absolute + len(word)
		}
	}
	return out
}

func isWordBoundary(line string, start, length int) bool {
	if start > 0 && isIdentChar(rune(line[start-1])) {
		return false
	}
	end := start + length
	if end < len(line) && isIdentChar(rune(line[end])) {
		return false
	}
	return true
}

func isIdentChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
