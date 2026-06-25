package lexer_test

import (
	"testing"

	"geblang/internal/lexer"
	"geblang/internal/token"
)

func TestLexerSkipsCommentsAndReadsStrings(t *testing.T) {
	input := `# line
/* block
comment */
import io;
io.print("Hello\n\u{1F600}");
io.print('Hello\n'); # line
let half = 5 // 2;
`

	tests := []struct {
		tokenType token.Type
		literal   string
		raw       string
		quote     byte
	}{
		{token.Import, "import", "", 0},
		{token.Ident, "io", "", 0},
		{token.Semicolon, ";", ";", 0},
		{token.Ident, "io", "", 0},
		{token.Dot, ".", ".", 0},
		{token.Ident, "print", "", 0},
		{token.LParen, "(", "(", 0},
		{token.String, "Hello\n😀", `Hello\n\u{1F600}`, '"'},
		{token.RParen, ")", ")", 0},
		{token.Semicolon, ";", ";", 0},
		{token.Ident, "io", "", 0},
		{token.Dot, ".", ".", 0},
		{token.Ident, "print", "", 0},
		{token.LParen, "(", "(", 0},
		{token.String, `Hello\n`, `Hello\n`, '\''},
		{token.RParen, ")", ")", 0},
		{token.Semicolon, ";", ";", 0},
		{token.Let, "let", "", 0},
		{token.Ident, "half", "", 0},
		{token.Assign, "=", "=", 0},
		{token.Int, "5", "5", 0},
		{token.IntDiv, "//", "//", 0},
		{token.Int, "2", "2", 0},
		{token.Semicolon, ";", ";", 0},
		{token.EOF, "", "", 0},
	}

	l := lexer.New(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.tokenType {
			t.Fatalf("tests[%d] token type: got %q, want %q", i, tok.Type, tt.tokenType)
		}
		if tok.Literal != tt.literal {
			t.Fatalf("tests[%d] literal: got %q, want %q", i, tok.Literal, tt.literal)
		}
		if tt.raw != "" && tok.Raw != tt.raw {
			t.Fatalf("tests[%d] raw: got %q, want %q", i, tok.Raw, tt.raw)
		}
		if tok.Quote != tt.quote {
			t.Fatalf("tests[%d] quote: got %q, want %q", i, tok.Quote, tt.quote)
		}
	}
}

func TestLexerReadsScientificNotationLiterals(t *testing.T) {
	input := `let a = 1e308; let b = 1.5e-3; let c = 2E8; let d = 1e10f;`
	tests := []struct {
		tokenType token.Type
		literal   string
	}{
		{token.Let, "let"},
		{token.Ident, "a"},
		{token.Assign, "="},
		{token.Decimal, "1e308"},
		{token.Semicolon, ";"},
		{token.Let, "let"},
		{token.Ident, "b"},
		{token.Assign, "="},
		{token.Decimal, "1.5e-3"},
		{token.Semicolon, ";"},
		{token.Let, "let"},
		{token.Ident, "c"},
		{token.Assign, "="},
		{token.Decimal, "2E8"},
		{token.Semicolon, ";"},
		{token.Let, "let"},
		{token.Ident, "d"},
		{token.Assign, "="},
		{token.Float, "1e10f"},
		{token.Semicolon, ";"},
		{token.EOF, ""},
	}
	l := lexer.New(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.tokenType {
			t.Fatalf("tests[%d] token type: got %q, want %q", i, tok.Type, tt.tokenType)
		}
		if tok.Literal != tt.literal {
			t.Fatalf("tests[%d] literal: got %q, want %q", i, tok.Literal, tt.literal)
		}
	}
}

func TestLexerTracksUnicodeColumns(t *testing.T) {
	input := "let café = \"😀\";\n"
	l := lexer.New(input)

	tests := []struct {
		tokenType token.Type
		literal   string
		column    int
	}{
		{token.Let, "let", 1},
		{token.Ident, "café", 5},
		{token.Assign, "=", 10},
		{token.String, "😀", 12},
		{token.Semicolon, ";", 15},
	}

	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.tokenType {
			t.Fatalf("tests[%d] token type: got %q, want %q", i, tok.Type, tt.tokenType)
		}
		if tok.Literal != tt.literal {
			t.Fatalf("tests[%d] literal: got %q, want %q", i, tok.Literal, tt.literal)
		}
		if tok.Column != tt.column {
			t.Fatalf("tests[%d] column: got %d, want %d", i, tok.Column, tt.column)
		}
	}
}

func TestLexerAttachesDocCommentsToNextToken(t *testing.T) {
	input := `## Adds values.
## Returns the total.
func add() {}

/**
 * Greets users.
 * Handles display names.
 */
class Greeter {}
`
	l := lexer.New(input)

	first := l.NextToken()
	if first.Type != token.Func {
		t.Fatalf("first token: got %s, want FUNC", first.Type)
	}
	if first.Doc != "Adds values.\nReturns the total." {
		t.Fatalf("function doc: got %q", first.Doc)
	}
	for first.Type != token.Class && first.Type != token.EOF {
		first = l.NextToken()
	}
	if first.Type != token.Class {
		t.Fatalf("missing class token")
	}
	if first.Doc != "Greets users.\nHandles display names." {
		t.Fatalf("class doc: got %q", first.Doc)
	}
}

func TestLexerReadsBasePrefixedIntegerLiterals(t *testing.T) {
	input := "let b = 0b11; let o = 0o644; let h = 0x1F;"
	l := lexer.New(input)

	var literals []string
	for {
		tok := l.NextToken()
		if tok.Type == token.EOF {
			break
		}
		if tok.Type == token.Int {
			literals = append(literals, tok.Literal)
		}
	}
	want := []string{"0b11", "0o644", "0x1F"}
	if len(literals) != len(want) {
		t.Fatalf("integer literals: got %v, want %v", literals, want)
	}
	for i := range want {
		if literals[i] != want[i] {
			t.Fatalf("integer literals: got %v, want %v", literals, want)
		}
	}
}

func TestLexerHandlesEmptyInput(t *testing.T) {
	tok := lexer.New("").NextToken()
	if tok.Type != token.EOF {
		t.Fatalf("token type: got %q, want %q", tok.Type, token.EOF)
	}
}

func TestLexerReportsUnterminatedString(t *testing.T) {
	tok := lexer.New("\"unterminated").NextToken()
	if tok.Type != token.Illegal {
		t.Fatalf("token type: got %q, want %q", tok.Type, token.Illegal)
	}
	if tok.Line != 1 || tok.Column != 1 {
		t.Fatalf("location: got %d:%d, want 1:1", tok.Line, tok.Column)
	}
}
