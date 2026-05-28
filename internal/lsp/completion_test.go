package lsp

import (
	"strings"
	"testing"
)

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

func TestCompletionOffersMagicMethods(t *testing.T) {
	s := &server{docs: map[string]string{"file:///main.gb": "__"}}

	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 0, Character: 2},
	}})

	for _, want := range []string{"__iter", "__done", "__next", "__invoke", "__enter", "__exit"} {
		if !hasCompletion(items, want) {
			t.Fatalf("expected magic method %s completion, got %#v", want, items)
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

// TestCompletionOffersTestBaseMethods verifies the new `this.<TAB>`
// inside a test.Test subclass surfaces the inherited assertion
// methods (assertEquals, assertThrows, etc.).
func TestCompletionOffersTestBaseMethods(t *testing.T) {
	src := "import test;\n\nclass MyTest extends test.Test {\n\t@test\n\tfunc check(): void {\n\t\tthis."
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 5, Character: 7},
	}})
	for _, want := range []string{"assertEquals", "assertTrue", "assertNull", "assertThrows", "fail"} {
		if !hasCompletion(items, want) {
			t.Fatalf("expected `%s` in test.Test this.<> completions, got %#v", want, items)
		}
	}
}

// TestCompletionOffersHttpRequestMethods verifies `<req>.<TAB>`
// when req is declared as `http.Request req;` surfaces the
// Request class methods (header, json, bodyText, etc.).
func TestCompletionOffersHttpRequestMethods(t *testing.T) {
	src := "http.Request req;\nreq."
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 4},
	}})
	for _, want := range []string{"header", "json", "bodyText", "bodyBytes", "toDict"} {
		if !hasCompletion(items, want) {
			t.Fatalf("expected `%s` in http.Request.<> completions, got %#v", want, items)
		}
	}
}

// TestCompletionOffersDbConnectionMethods covers db.Connection.<>.
func TestCompletionOffersDbConnectionMethods(t *testing.T) {
	src := "db.Connection conn;\nconn."
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 5},
	}})
	for _, want := range []string{"exec", "query", "begin", "prepare", "close"} {
		if !hasCompletion(items, want) {
			t.Fatalf("expected `%s` in db.Connection.<> completions, got %#v", want, items)
		}
	}
}

// TestCompletionOffersUrlURLMethods covers url.URL.<>.
func TestCompletionOffersUrlURLMethods(t *testing.T) {
	src := "url.URL u;\nu."
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 2},
	}})
	for _, want := range []string{"scheme", "host", "path", "query", "withScheme", "toString"} {
		if !hasCompletion(items, want) {
			t.Fatalf("expected `%s` in url.URL.<> completions, got %#v", want, items)
		}
	}
}

// TestCompletionOffersDatetimeInstantMethods covers datetime.Instant.<>.
func TestCompletionOffersDatetimeInstantMethods(t *testing.T) {
	src := "datetime.Instant moment;\nmoment."
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 7},
	}})
	for _, want := range []string{"toUnix", "formatRFC3339", "add", "diff", "inZone"} {
		if !hasCompletion(items, want) {
			t.Fatalf("expected `%s` in datetime.Instant.<> completions, got %#v", want, items)
		}
	}
}

// TestCompletionClassContextFallsThroughForUnknownClass verifies
// that an unrecognised qualified type (e.g. `mymod.Mine x;`) falls
// through to identifier completion rather than returning empty.
func TestCompletionClassContextFallsThroughForUnknownClass(t *testing.T) {
	src := "mymod.Mine x;\nx."
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 2},
	}})
	if hasCompletion(items, "header") {
		t.Fatalf("did not expect http.Request methods for an unknown class, got %#v", items)
	}
}

// TestCompletionThisOutsideTestClassFallsThrough verifies the
// path doesn't fire when `extends test.Test` isn't in the source -
// `this.` should not surface assertion methods in regular classes.
func TestCompletionThisOutsideTestClassFallsThrough(t *testing.T) {
	src := "class Plain {\n\tfunc check(): void {\n\t\tthis."
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	items := s.completions(CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 2, Character: 7},
	}})
	if hasCompletion(items, "assertEquals") {
		t.Fatalf("did not expect assertEquals outside test.Test subclass, got %#v", items)
	}
}

// TestSignatureHelpForClassMethod verifies signatureHelp resolves
// `<typedVar>.<method>(<cursor>` against the catalogued class
// method tables.
func TestSignatureHelpForClassMethod(t *testing.T) {
	src := "http.Request req;\nreq.header("
	s := &server{docs: map[string]string{"file:///main.gb": src}}
	help := s.signatureHelp(SignatureHelpParams{TextDocumentPositionParams: TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"},
		Position:     Position{Line: 1, Character: 11},
	}})
	if len(help.Signatures) != 1 {
		t.Fatalf("expected one signature for http.Request.header, got %d (%#v)", len(help.Signatures), help)
	}
	if !strings.Contains(help.Signatures[0].Label, "string name") || !strings.Contains(help.Signatures[0].Label, "?string") {
		t.Errorf("signature should reference `string name` param and `?string` return, got %q", help.Signatures[0].Label)
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
