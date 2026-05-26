package lsp

import (
	"testing"
)

func TestReferencesReturnsAllOccurrences(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "let foo = 1;\nfoo = foo + 1;\nio.println(foo);\n"
	s.docs[uri] = source
	result := s.references(ReferenceParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
			Position:     Position{Line: 0, Character: 4},
		},
		Context: ReferenceContext{IncludeDeclaration: true},
	})
	locs, ok := result.([]Location)
	if !ok {
		t.Fatalf("expected []Location, got %T", result)
	}
	if len(locs) != 4 {
		t.Fatalf("expected 4 references to foo, got %d: %+v", len(locs), locs)
	}
}

func TestReferencesIgnoresSubstringMatches(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "let foo = 1;\nlet foobar = 2;\nio.println(foo);\n"
	s.docs[uri] = source
	result := s.references(ReferenceParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
			Position:     Position{Line: 0, Character: 4},
		},
	})
	locs := result.([]Location)
	if len(locs) != 2 {
		t.Fatalf("expected 2 references to foo (not foobar), got %d: %+v", len(locs), locs)
	}
}

func TestRenameEmitsWorkspaceEdit(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "let foo = 1;\nfoo = foo + 1;\n"
	s.docs[uri] = source
	result := s.rename(RenameParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
			Position:     Position{Line: 0, Character: 4},
		},
		NewName: "bar",
	})
	edit, ok := result.(WorkspaceEdit)
	if !ok {
		t.Fatalf("expected WorkspaceEdit, got %T", result)
	}
	edits := edit.Changes[uri]
	if len(edits) != 3 {
		t.Fatalf("expected 3 text edits, got %d: %+v", len(edits), edits)
	}
	for _, e := range edits {
		if e.NewText != "bar" {
			t.Fatalf("edit newText: got %q want bar", e.NewText)
		}
	}
}

func TestPrepareRenameReturnsRangeForIdentifier(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	s.docs[uri] = "let myVar = 1;\n"
	result := s.prepareRename(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 0, Character: 6},
	})
	r, ok := result.(Range)
	if !ok {
		t.Fatalf("expected Range, got %T", result)
	}
	if r.Start.Character != 4 || r.End.Character != 9 {
		t.Fatalf("range for myVar: got %+v want chars 4-9", r)
	}
}

func TestCodeActionSuggestsNearImportMatch(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "import bytse;\n"
	s.docs[uri] = source
	actions := s.codeAction(CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Range:        Range{Start: Position{Line: 0}, End: Position{Line: 0}},
		Context: CodeActionContext{
			Diagnostics: []Diagnostic{{
				Range:    Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 13}},
				Severity: 1,
				Code:     "import",
				Message:  "cannot resolve import bytse",
			}},
		},
	}).([]CodeAction)
	if len(actions) == 0 {
		t.Fatalf("expected at least one quick-fix for 'bytse'")
	}
	found := false
	for _, a := range actions {
		if a.Kind != "quickfix" {
			t.Fatalf("expected quickfix kind, got %q", a.Kind)
		}
		for _, edits := range a.Edit.Changes {
			for _, e := range edits {
				if e.NewText == "bytes" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("expected 'bytes' as a candidate replacement, got %+v", actions)
	}
}

func TestCodeActionEmptyWhenNoImportDiagnostic(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	s.docs[uri] = "let x = 1;\n"
	actions := s.codeAction(CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Context: CodeActionContext{
			Diagnostics: []Diagnostic{{Severity: 1, Code: "semantic", Message: "noop"}},
		},
	}).([]CodeAction)
	if len(actions) != 0 {
		t.Fatalf("expected no actions, got %+v", actions)
	}
}

func TestWorkspaceSymbolsIncludesOpenDocSymbols(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/proj/main.gb"
	s.docs[uri] = "func runThing(): void {}\nclass Service {}\n"
	out := s.workspaceSymbols("Service").([]WorkspaceSymbol)
	found := false
	for _, sym := range out {
		if sym.Name == "Service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected to find Service symbol, got %+v", out)
	}
}

func TestLevenshteinDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"bytes", "bytse", 1},
		{"foo", "foo", 0},
		{"abc", "xyz", 3},
		{"", "abc", 3},
		{"abc", "", 3},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Fatalf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
