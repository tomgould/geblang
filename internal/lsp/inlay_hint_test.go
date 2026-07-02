package lsp

import "testing"

// TestInlayHintAnnotatesCallArguments verifies the core case: a
// same-file function declaration followed by a call gets one
// parameter-name hint per positional argument, at the argument's
// start position.
func TestInlayHintAnnotatesCallArguments(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "func add(int a, int b): int {\n    return a + b;\n}\nadd(1, 2);\n"
	s.docs[uri] = source

	result := s.inlayHint(InlayHintParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 3, Character: 10},
		},
	})
	hints, ok := result.([]InlayHint)
	if !ok {
		t.Fatalf("expected []InlayHint, got %T", result)
	}
	if len(hints) != 2 {
		t.Fatalf("expected 2 hints, got %d: %+v", len(hints), hints)
	}

	// Line 3: `add(1, 2);` - "1" starts at column 4, "2" at column 7.
	want := []InlayHint{
		{Position: Position{Line: 3, Character: 4}, Label: "a:", Kind: inlayHintKindParameter, PaddingRight: true},
		{Position: Position{Line: 3, Character: 7}, Label: "b:", Kind: inlayHintKindParameter, PaddingRight: true},
	}
	for i, w := range want {
		if hints[i] != w {
			t.Fatalf("hint %d: got %+v want %+v", i, hints[i], w)
		}
	}
}

// TestInlayHintUnknownCalleeYieldsNoHints ensures the resolver fails
// closed: a call to a name with no matching declaration or stdlib
// entry emits nothing rather than a guess.
func TestInlayHintUnknownCalleeYieldsNoHints(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "mystery(1, 2);\n"
	s.docs[uri] = source

	result := s.inlayHint(InlayHintParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 0, Character: 20},
		},
	})
	hints, ok := result.([]InlayHint)
	if !ok {
		t.Fatalf("expected []InlayHint, got %T", result)
	}
	if len(hints) != 0 {
		t.Fatalf("expected 0 hints for unknown callee, got %d: %+v", len(hints), hints)
	}
}

// TestInlayHintExcludesCallsOutsideRange ensures a call whose position
// falls outside the requested range is not annotated, while a call
// inside the range still is.
func TestInlayHintExcludesCallsOutsideRange(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "func add(int a, int b): int {\n    return a + b;\n}\nadd(1, 2);\nadd(3, 4);\n"
	s.docs[uri] = source

	// Range covers only line 3 (the first call site), not line 4.
	result := s.inlayHint(InlayHintParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Range: Range{
			Start: Position{Line: 3, Character: 0},
			End:   Position{Line: 3, Character: 10},
		},
	})
	hints, ok := result.([]InlayHint)
	if !ok {
		t.Fatalf("expected []InlayHint, got %T", result)
	}
	if len(hints) != 2 {
		t.Fatalf("expected 2 hints for the in-range call only, got %d: %+v", len(hints), hints)
	}
	for _, h := range hints {
		if h.Position.Line != 3 {
			t.Fatalf("expected hint on line 3 only, got %+v", h)
		}
	}
}

// TestInlayHintResolvesStdlibModuleCall verifies module-member calls
// (e.g. io.println) resolve parameter names via the native catalog.
func TestInlayHintResolvesStdlibModuleCall(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "import io;\nio.println(42);\n"
	s.docs[uri] = source

	result := s.inlayHint(InlayHintParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 1, Character: 20},
		},
	})
	hints, ok := result.([]InlayHint)
	if !ok {
		t.Fatalf("expected []InlayHint, got %T", result)
	}
	if len(hints) != 1 {
		t.Fatalf("expected 1 hint for io.println(42), got %d: %+v", len(hints), hints)
	}
	if hints[0].Label != "value:" || hints[0].Kind != inlayHintKindParameter {
		t.Fatalf("unexpected hint: %+v", hints[0])
	}
}

// TestInlayHintSkipsNamedArguments ensures a named argument
// (`name: value`) never gets a redundant/incorrect hint.
func TestInlayHintSkipsNamedArguments(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "func add(int a, int b): int {\n    return a + b;\n}\nadd(a: 1, b: 2);\n"
	s.docs[uri] = source

	result := s.inlayHint(InlayHintParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 3, Character: 20},
		},
	})
	hints, ok := result.([]InlayHint)
	if !ok {
		t.Fatalf("expected []InlayHint, got %T", result)
	}
	if len(hints) != 0 {
		t.Fatalf("expected 0 hints for named arguments, got %d: %+v", len(hints), hints)
	}
}

// TestInlayHintMissingDocumentReturnsEmpty matches the established
// pattern (documentHighlight, references, ...) of returning an empty,
// non-nil slice for a document the server doesn't have open.
func TestInlayHintMissingDocumentReturnsEmpty(t *testing.T) {
	s, _ := newTestServer()
	result := s.inlayHint(InlayHintParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///tmp/missing.gb"},
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 0, Character: 0},
		},
	})
	hints, ok := result.([]InlayHint)
	if !ok {
		t.Fatalf("expected []InlayHint, got %T", result)
	}
	if len(hints) != 0 {
		t.Fatalf("expected 0 hints for missing document, got %d: %+v", len(hints), hints)
	}
}

// TestInlayHintStopsAtParamCountForExtraArguments verifies a call with
// more positional arguments than declared parameters only annotates
// up to the known parameter count, never indexing past it.
func TestInlayHintStopsAtParamCountForExtraArguments(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "func add(int a, int b): int {\n    return a + b;\n}\nadd(1, 2, 3);\n"
	s.docs[uri] = source

	result := s.inlayHint(InlayHintParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 3, Character: 20},
		},
	})
	hints, ok := result.([]InlayHint)
	if !ok {
		t.Fatalf("expected []InlayHint, got %T", result)
	}
	if len(hints) != 2 {
		t.Fatalf("expected 2 hints (extra arg unannotated), got %d: %+v", len(hints), hints)
	}
}
