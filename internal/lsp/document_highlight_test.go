package lsp

import (
	"testing"
)

func TestDocumentHighlightReturnsAllOccurrences(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "let foo = 1;\nfoo = foo + 1;\nio.println(foo);\n"
	s.docs[uri] = source
	result := s.documentHighlight(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 0, Character: 4},
	})
	highlights, ok := result.([]DocumentHighlight)
	if !ok {
		t.Fatalf("expected []DocumentHighlight, got %T", result)
	}
	if len(highlights) != 4 {
		t.Fatalf("expected 4 highlights for foo, got %d: %+v", len(highlights), highlights)
	}
	wantRanges := []Range{
		{Start: Position{Line: 0, Character: 4}, End: Position{Line: 0, Character: 7}},
		{Start: Position{Line: 1, Character: 0}, End: Position{Line: 1, Character: 3}},
		{Start: Position{Line: 1, Character: 6}, End: Position{Line: 1, Character: 9}},
		{Start: Position{Line: 2, Character: 11}, End: Position{Line: 2, Character: 14}},
	}
	for i, want := range wantRanges {
		if highlights[i].Range != want {
			t.Fatalf("highlight %d range: got %+v want %+v", i, highlights[i].Range, want)
		}
		if highlights[i].Kind != 1 {
			t.Fatalf("highlight %d kind: got %d want 1", i, highlights[i].Kind)
		}
	}
}

func TestDocumentHighlightIgnoresSubstringMatches(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "let foo = 1;\nlet foobar = 2;\nio.println(foo);\n"
	s.docs[uri] = source
	result := s.documentHighlight(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 0, Character: 4},
	})
	highlights := result.([]DocumentHighlight)
	if len(highlights) != 2 {
		t.Fatalf("expected 2 highlights for foo (not foobar), got %d: %+v", len(highlights), highlights)
	}
}

func TestDocumentHighlightEmptyOnWhitespace(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "let foo = 1;\n"
	s.docs[uri] = source
	result := s.documentHighlight(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 0, Character: 8}, // "=" between "foo" and "1"
	})
	highlights, ok := result.([]DocumentHighlight)
	if !ok {
		t.Fatalf("expected []DocumentHighlight, got %T", result)
	}
	if len(highlights) != 0 {
		t.Fatalf("expected 0 highlights on whitespace, got %d: %+v", len(highlights), highlights)
	}
}

func TestDocumentHighlightEmptyForMissingDocument(t *testing.T) {
	s, _ := newTestServer()
	result := s.documentHighlight(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///tmp/missing.gb"},
		Position:     Position{Line: 0, Character: 0},
	})
	highlights, ok := result.([]DocumentHighlight)
	if !ok {
		t.Fatalf("expected []DocumentHighlight, got %T", result)
	}
	if len(highlights) != 0 {
		t.Fatalf("expected 0 highlights for missing document, got %d: %+v", len(highlights), highlights)
	}
}
