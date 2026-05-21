package lsp

import "testing"

// TestCompletionOffersPrimitiveMethodsForDecimal verifies the new
// primitive-method completion path: typing `d.<TAB>` after declaring
// `decimal d = ...` surfaces the decimal methods (format, abs,
// toString, etc.).
func TestCompletionOffersPrimitiveMethodsForDecimal(t *testing.T) {
	src := "decimal d = 3.14;\nd."
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 2},
	}})
	if !hasCompletion(items, "format") {
		t.Fatalf("expected `format` in decimal.<> completions, got %#v", items)
	}
	if !hasCompletion(items, "toString") {
		t.Fatalf("expected `toString` in decimal.<> completions, got %#v", items)
	}
}

// TestCompletionOffersPrimitiveMethodsForString covers the string
// surface (the largest method table, and the common case of
// `<string>.<TAB>` discovery).
func TestCompletionOffersPrimitiveMethodsForString(t *testing.T) {
	src := "string greeting = \"hi\";\ngreeting."
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 9},
	}})
	for _, want := range []string{"length", "upper", "lower", "trim", "contains", "split"} {
		if !hasCompletion(items, want) {
			t.Fatalf("expected `%s` in string.<> completions, got %#v", want, items)
		}
	}
}

// TestCompletionPrimitiveContextIgnoresUnknownVariables verifies the
// completion path doesn't blow up when the receiver name isn't a
// typed variable - it should fall through to identifier-prefix
// completion instead.
func TestCompletionPrimitiveContextIgnoresUnknownVariables(t *testing.T) {
	src := "unknownName."
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 0, Character: 12},
	}})
	// Should not crash, should not return primitive methods.
	if hasCompletion(items, "format") {
		t.Fatalf("primitive methods leaked into completion for unknown variable: %#v", items)
	}
}

func TestCompletionOffersModuleMembers(t *testing.T) {
	s := &server{docs: map[string]string{"file:///main.gb": "import io;\nio."}}

	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 3},
	}})

	if !hasCompletion(items, "println") {
		t.Fatalf("expected io.println completion, got %#v", items)
	}
}

func TestCompletionOffersImportModules(t *testing.T) {
	s := &server{docs: map[string]string{"file:///main.gb": "import we"}}

	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 0, Character: 9},
	}})

	if !hasCompletion(items, "web") || !hasCompletion(items, "websocket") {
		t.Fatalf("expected web module completions, got %#v", items)
	}
}

func TestCompletionOffersStringBuilder(t *testing.T) {
	s := &server{docs: map[string]string{"file:///main.gb": "import strings;\nstrings."}}

	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 8},
	}})

	if !hasCompletion(items, "StringBuilder") {
		t.Fatalf("expected strings.StringBuilder completion, got %#v", items)
	}
}

func TestCompletionOffersStrbuilderPrimitives(t *testing.T) {
	s := &server{docs: map[string]string{"file:///main.gb": "import strbuilder;\nstrbuilder."}}

	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 11},
	}})

	for _, want := range []string{"new", "append", "appendLine", "build", "length", "clear", "dispose"} {
		if !hasCompletion(items, want) {
			t.Fatalf("expected strbuilder.%s completion, got %#v", want, items)
		}
	}
}

func TestSignatureHelpForModuleFunction(t *testing.T) {
	s := &server{docs: map[string]string{"file:///main.gb": "db.query(conn, "}}

	help := s.signatureHelp(SignatureHelpParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 0, Character: 15},
	}})

	if len(help.Signatures) != 1 {
		t.Fatalf("expected one signature, got %#v", help)
	}
	if help.Signatures[0].Label != "query(Connection conn, string sql, ...any args): list<dict<string, any>>" {
		t.Fatalf("unexpected signature label %q", help.Signatures[0].Label)
	}
	if help.ActiveParameter != 1 {
		t.Fatalf("expected active parameter 1, got %d", help.ActiveParameter)
	}
}

func hasCompletion(items []CompletionItem, label string) bool {
	for _, item := range items {
		if item.Label == label {
			return true
		}
	}
	return false
}
