package lsp

import (
	"strings"

	"geblang/internal/lexer"
	"geblang/internal/token"
)

// FoldingRangeParams is the LSP FoldingRangeParams shape: only the
// document identity is needed, no position or range.
type FoldingRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// FoldingRange is an LSP FoldingRange. Kind is omitted for brace-matched
// ranges (LSP defaults an absent kind to a generic "region" fold) and
// set to "comment" for block-comment ranges.
type FoldingRange struct {
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Kind      string `json:"kind,omitempty"`
}

// foldingRange handles textDocument/foldingRange with single-file scope.
// It lexes the document once to pair braces via a stack and once more
// pulls the lexer's captured comment list to fold multi-line block
// comments. Both sources of ranges use 0-based LSP line numbers, unlike
// the lexer's 1-based Line field.
func (s *server) foldingRange(params FoldingRangeParams) any {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return []FoldingRange{}
	}

	ranges := braceFoldingRanges(source)
	ranges = append(ranges, commentFoldingRanges(source)...)
	if ranges == nil {
		return []FoldingRange{}
	}
	return ranges
}

// braceFoldingRanges lexes source and pairs `{`/`}` tokens via a stack,
// emitting one FoldingRange per matched pair that spans more than one
// line. Nested blocks naturally produce nested (overlapping) ranges,
// which is correct LSP behavior.
//
// Unbalanced input is handled defensively: an extra `}` with an empty
// stack is skipped, and any `{` left on the stack at EOF (unclosed
// block) is discarded rather than emitted.
func braceFoldingRanges(source string) []FoldingRange {
	l := lexer.New(source)
	var stack []int // open-brace line numbers (1-based, as given by the lexer)
	var out []FoldingRange
	for {
		tok := l.NextToken()
		if tok.Type == token.EOF {
			break
		}
		switch tok.Type {
		case token.LBrace:
			stack = append(stack, tok.Line)
		case token.RBrace:
			if len(stack) == 0 {
				continue
			}
			startLine := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			endLine := tok.Line
			if startLine == endLine {
				continue
			}
			out = append(out, FoldingRange{StartLine: startLine - 1, EndLine: endLine - 1})
		}
	}
	return out
}

// commentFoldingRanges returns one FoldingRange per multi-line block
// comment captured by the lexer. Line comments (kinds without "block")
// are never folded here; consecutive-line-comment folding is an
// optional part of the LSP spec and is intentionally not implemented.
//
// A block comment's end line is derived from its captured Text: Text is
// the raw content between the opening marker (`/*` or `/**`) and the
// closing `*/`, so counting newlines in Text and adding to the starting
// Line gives the 1-based line the comment ends on.
func commentFoldingRanges(source string) []FoldingRange {
	l := lexer.New(source)
	for {
		tok := l.NextToken()
		if tok.Type == token.EOF {
			break
		}
	}

	var out []FoldingRange
	for _, c := range l.Comments() {
		if !strings.Contains(c.Kind, "block") {
			continue
		}
		endLine := c.Line + strings.Count(c.Text, "\n")
		if endLine == c.Line {
			continue
		}
		out = append(out, FoldingRange{StartLine: c.Line - 1, EndLine: endLine - 1, Kind: "comment"})
	}
	return out
}
