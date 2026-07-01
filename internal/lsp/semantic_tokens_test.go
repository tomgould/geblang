package lsp

import "testing"

// decodedToken is the reconstructed absolute-coordinate form of one
// semantic token, produced by decoding the delta-encoded flat array
// the handler returns. Tests decode rather than assert on raw deltas
// so assertions read naturally in (line, char, length, type) terms.
type decodedToken struct {
	Line      int
	StartChar int
	Length    int
	TypeIndex int
}

func decodeSemanticTokens(data []int) []decodedToken {
	var out []decodedToken
	line, char := 0, 0
	for i := 0; i+4 < len(data); i += 5 {
		deltaLine := data[i]
		deltaStartChar := data[i+1]
		length := data[i+2]
		typeIndex := data[i+3]

		line += deltaLine
		if deltaLine == 0 {
			char += deltaStartChar
		} else {
			char = deltaStartChar
		}
		out = append(out, decodedToken{Line: line, StartChar: char, Length: length, TypeIndex: typeIndex})
	}
	return out
}

func legendIndex(t *testing.T, name string) int {
	t.Helper()
	for i, n := range semanticTokenTypes {
		if n == name {
			return i
		}
	}
	t.Fatalf("legend has no token type %q", name)
	return -1
}

func findDecoded(toks []decodedToken, line, startChar int) (decodedToken, bool) {
	for _, tok := range toks {
		if tok.Line == line && tok.StartChar == startChar {
			return tok, true
		}
	}
	return decodedToken{}, false
}

func TestSemanticTokensFullClassifiesRepresentativeSnippet(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "@route(\"GET\")\n" +
		"func greet(string name): string {\n" +
		"    # says hello\n" +
		"    let count = 42;\n" +
		"    return io.format(name);\n" +
		"}\n"
	s.docs[uri] = source

	result := s.semanticTokensFull(SemanticTokensParams{TextDocument: TextDocumentIdentifier{URI: uri}})
	tokens, ok := result.(SemanticTokens)
	if !ok {
		t.Fatalf("expected SemanticTokens, got %T", result)
	}
	if len(tokens.Data)%5 != 0 {
		t.Fatalf("data length %d is not a multiple of 5", len(tokens.Data))
	}

	decoded := decodeSemanticTokens(tokens.Data)

	keywordIdx := legendIndex(t, "keyword")
	typeIdx := legendIndex(t, "type")
	functionIdx := legendIndex(t, "function")
	stringIdx := legendIndex(t, "string")
	numberIdx := legendIndex(t, "number")
	commentIdx := legendIndex(t, "comment")
	decoratorIdx := legendIndex(t, "decorator")

	// Line 0: `@route("GET")` -> decorator "@", decorator "route", string "GET"
	if tok, ok := findDecoded(decoded, 0, 0); !ok || tok.Length != 1 || tok.TypeIndex != decoratorIdx {
		t.Fatalf("expected decorator '@' at (0,0), got %+v ok=%v", tok, ok)
	}
	if tok, ok := findDecoded(decoded, 0, 1); !ok || tok.Length != 5 || tok.TypeIndex != decoratorIdx {
		t.Fatalf("expected decorator 'route' at (0,1) length 5, got %+v ok=%v", tok, ok)
	}
	if tok, ok := findDecoded(decoded, 0, 7); !ok || tok.Length != 5 || tok.TypeIndex != stringIdx {
		t.Fatalf("expected string \"GET\" at (0,7) length 5, got %+v ok=%v", tok, ok)
	}

	// Line 1: `func greet(string name): string {`
	if tok, ok := findDecoded(decoded, 1, 0); !ok || tok.Length != 4 || tok.TypeIndex != keywordIdx {
		t.Fatalf("expected keyword 'func' at (1,0), got %+v ok=%v", tok, ok)
	}
	if tok, ok := findDecoded(decoded, 1, 5); !ok || tok.Length != 5 || tok.TypeIndex != functionIdx {
		t.Fatalf("expected function 'greet' at (1,5), got %+v ok=%v", tok, ok)
	}
	if tok, ok := findDecoded(decoded, 1, 11); !ok || tok.Length != 6 || tok.TypeIndex != typeIdx {
		t.Fatalf("expected type 'string' at (1,11), got %+v ok=%v", tok, ok)
	}

	// Line 2: `    # says hello` -> comment from column 4 to end of line
	if tok, ok := findDecoded(decoded, 2, 4); !ok || tok.TypeIndex != commentIdx || tok.Length != len("# says hello") {
		t.Fatalf("expected comment at (2,4) length %d, got %+v ok=%v", len("# says hello"), tok, ok)
	}

	// Line 3: `    let count = 42;` -> keyword "let", variable "count", number "42"
	if tok, ok := findDecoded(decoded, 3, 4); !ok || tok.Length != 3 || tok.TypeIndex != keywordIdx {
		t.Fatalf("expected keyword 'let' at (3,4), got %+v ok=%v", tok, ok)
	}
	if tok, ok := findDecoded(decoded, 3, 16); !ok || tok.Length != 2 || tok.TypeIndex != numberIdx {
		t.Fatalf("expected number '42' at (3,16), got %+v ok=%v", tok, ok)
	}

	// Line 4: `    return io.format(name);` -> function "format" (io. prefixed call)
	if tok, ok := findDecoded(decoded, 4, 14); !ok || tok.Length != 6 || tok.TypeIndex != functionIdx {
		t.Fatalf("expected function 'format' at (4,14), got %+v ok=%v", tok, ok)
	}
}

func TestSemanticTokensFullEmptyDocumentReturnsEmptyData(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/empty.gb"
	s.docs[uri] = ""
	result := s.semanticTokensFull(SemanticTokensParams{TextDocument: TextDocumentIdentifier{URI: uri}})
	tokens, ok := result.(SemanticTokens)
	if !ok {
		t.Fatalf("expected SemanticTokens, got %T", result)
	}
	if tokens.Data == nil {
		t.Fatalf("expected non-nil empty slice, got nil")
	}
	if len(tokens.Data) != 0 {
		t.Fatalf("expected 0 data entries, got %d", len(tokens.Data))
	}
}

func TestSemanticTokensFullMissingDocumentReturnsEmptyData(t *testing.T) {
	s, _ := newTestServer()
	result := s.semanticTokensFull(SemanticTokensParams{TextDocument: TextDocumentIdentifier{URI: "file:///tmp/missing.gb"}})
	tokens, ok := result.(SemanticTokens)
	if !ok {
		t.Fatalf("expected SemanticTokens, got %T", result)
	}
	if len(tokens.Data) != 0 {
		t.Fatalf("expected 0 data entries for missing document, got %d", len(tokens.Data))
	}
}

// TestSemanticTokensFullSplitsMultiLineBlockComment proves that a block
// comment spanning multiple source lines (geblang supports `/* ... */`
// spanning lines - see lexer_test.go's
// TestLexerSkipsCommentsAndReadsStrings) decodes to exactly one
// "comment" token per line it covers, each confined to that line's
// column range, never crossing a line boundary.
func TestSemanticTokensFullSplitsMultiLineBlockComment(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/block.gb"
	source := "let x = 1;\n" +
		"/* first\n" +
		"second\n" +
		"third */\n" +
		"let y = 2;\n"
	s.docs[uri] = source

	result := s.semanticTokensFull(SemanticTokensParams{TextDocument: TextDocumentIdentifier{URI: uri}})
	tokens := result.(SemanticTokens)
	decoded := decodeSemanticTokens(tokens.Data)
	commentIdx := legendIndex(t, "comment")

	var commentToks []decodedToken
	for _, tok := range decoded {
		if tok.TypeIndex == commentIdx {
			commentToks = append(commentToks, tok)
		}
	}
	if len(commentToks) != 3 {
		t.Fatalf("expected 3 comment tokens (one per line), got %d: %+v", len(commentToks), commentToks)
	}

	want := []decodedToken{
		{Line: 1, StartChar: 0, Length: len("/* first"), TypeIndex: commentIdx},
		{Line: 2, StartChar: 0, Length: len("second"), TypeIndex: commentIdx},
		{Line: 3, StartChar: 0, Length: len("third */"), TypeIndex: commentIdx},
	}
	for i, w := range want {
		if commentToks[i] != w {
			t.Fatalf("comment token %d: got %+v, want %+v", i, commentToks[i], w)
		}
	}
}

// TestBlockCommentTokensSplitHelperDirect is a direct unit test of the
// splitting helper itself (in addition to the end-to-end test above),
// using a synthetic multi-line comment to pin down the per-line
// boundary behaviour independently of the rest of the pipeline.
func TestBlockCommentTokensSplitHelperDirect(t *testing.T) {
	lines := []string{
		"before",
		"/** one",
		" * two",
		" * three */",
		"after",
	}
	source := ""
	for i, l := range lines {
		source += l
		if i != len(lines)-1 {
			source += "\n"
		}
	}

	toks := classifySource(source, lines)
	commentIdx := legendIndex(t, "comment")
	var commentToks []semanticToken
	for _, tok := range toks {
		if tok.TypeIndex == commentIdx {
			commentToks = append(commentToks, tok)
		}
	}
	if len(commentToks) != 3 {
		t.Fatalf("expected 3 comment tokens, got %d: %+v", len(commentToks), commentToks)
	}
	if commentToks[0].Line != 1 || commentToks[0].StartChar != 0 {
		t.Fatalf("first comment line token: got %+v", commentToks[0])
	}
	if commentToks[1].Line != 2 || commentToks[1].StartChar != 0 {
		t.Fatalf("second comment line token: got %+v", commentToks[1])
	}
	if commentToks[2].Line != 3 || commentToks[2].StartChar != 0 {
		t.Fatalf("third comment line token: got %+v", commentToks[2])
	}
}
