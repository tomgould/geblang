package lsp

import "testing"

// hasFoldingRange reports whether ranges contains an entry matching
// start/end/kind exactly.
func hasFoldingRange(ranges []FoldingRange, start, end int, kind string) bool {
	for _, r := range ranges {
		if r.StartLine == start && r.EndLine == end && r.Kind == kind {
			return true
		}
	}
	return false
}

// hasFoldingRangeStartingAt reports whether any range in ranges starts
// on the given 0-based line, regardless of its end line or kind.
func hasFoldingRangeStartingAt(ranges []FoldingRange, start int) bool {
	for _, r := range ranges {
		if r.StartLine == start {
			return true
		}
	}
	return false
}

func TestFoldingRangeBracesAndBlockComment(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "func f(int a): int {\n" +
		"    if (a > 0) {\n" +
		"        let x = a + 1;\n" +
		"        return x;\n" +
		"    }\n" +
		"    let y = 0;\n" +
		"    return y;\n" +
		"}\n" +
		"\n" +
		"/* this is a\n" +
		"   multi-line\n" +
		"   block comment */\n" +
		"\n" +
		"func g(): int {}\n"
	s.docs[uri] = source

	result := s.foldingRange(FoldingRangeParams{TextDocument: TextDocumentIdentifier{URI: uri}})
	ranges, ok := result.([]FoldingRange)
	if !ok {
		t.Fatalf("expected []FoldingRange, got %T", result)
	}

	// Outer function body: `{` on line 1, `}` on line 8 (1-based) -> 0-based 0..7.
	if !hasFoldingRange(ranges, 0, 7, "") {
		t.Fatalf("expected outer function-body range (0,7,\"\"), got %+v", ranges)
	}

	// Nested if-block: `{` on line 2, `}` on line 5 (1-based) -> 0-based 1..4.
	if !hasFoldingRange(ranges, 1, 4, "") {
		t.Fatalf("expected nested if-block range (1,4,\"\"), got %+v", ranges)
	}

	// Block comment spans lines 10-12 (1-based) -> 0-based 9..11, kind "comment".
	if !hasFoldingRange(ranges, 9, 11, "comment") {
		t.Fatalf("expected block comment range (9,11,\"comment\"), got %+v", ranges)
	}

	// The single-line `{}` on line 14 (1-based, 0-based line 13) must not fold.
	if hasFoldingRangeStartingAt(ranges, 13) {
		t.Fatalf("did not expect a folding range starting at line 13 (single-line block), got %+v", ranges)
	}
}

func TestFoldingRangeEmptyForMissingDocument(t *testing.T) {
	s, _ := newTestServer()
	result := s.foldingRange(FoldingRangeParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///tmp/missing.gb"},
	})
	ranges, ok := result.([]FoldingRange)
	if !ok {
		t.Fatalf("expected []FoldingRange, got %T", result)
	}
	if len(ranges) != 0 {
		t.Fatalf("expected 0 ranges for missing document, got %d: %+v", len(ranges), ranges)
	}
}

func TestFoldingRangeNoFoldableRangesReturnsEmptySlice(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/flat.gb"
	source := "let x = 1;\nlet y = 2;\n"
	s.docs[uri] = source

	result := s.foldingRange(FoldingRangeParams{TextDocument: TextDocumentIdentifier{URI: uri}})
	ranges, ok := result.([]FoldingRange)
	if !ok {
		t.Fatalf("expected []FoldingRange, got %T", result)
	}
	if ranges == nil {
		t.Fatalf("expected non-nil empty slice, got nil")
	}
	if len(ranges) != 0 {
		t.Fatalf("expected 0 ranges, got %d: %+v", len(ranges), ranges)
	}
}

func TestFoldingRangeUnbalancedBracesDoesNotPanic(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/unbalanced.gb"
	// A stray closing brace with no matching open, followed by a
	// legitimately matched multi-line block, then a trailing unclosed
	// open brace at EOF.
	source := "}\n" +
		"func f(): int {\n" +
		"    let x = 1;\n" +
		"    return x;\n" +
		"}\n" +
		"func g(): int {\n" +
		"    let y = 2;\n"
	s.docs[uri] = source

	result := s.foldingRange(FoldingRangeParams{TextDocument: TextDocumentIdentifier{URI: uri}})
	ranges, ok := result.([]FoldingRange)
	if !ok {
		t.Fatalf("expected []FoldingRange, got %T", result)
	}

	// The one legitimately matched pair (lines 2-5, 1-based -> 0-based
	// 1..4) must still be reported; the stray `}` and the trailing
	// unclosed `{` must simply be ignored, not cause a panic.
	if !hasFoldingRange(ranges, 1, 4, "") {
		t.Fatalf("expected matched range (1,4,\"\") to survive unbalanced input, got %+v", ranges)
	}
}
