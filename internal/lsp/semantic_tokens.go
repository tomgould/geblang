package lsp

import (
	"sort"
	"strings"

	"geblang/internal/lexer"
	"geblang/internal/token"
)

// semanticTokenTypes is the fixed, ordered legend advertised in the
// initialize response. Index into this slice is the tokenType value
// encoded in each semantic token's data quintuplet.
var semanticTokenTypes = []string{
	"keyword", "type", "function", "variable", "string",
	"number", "comment", "operator", "decorator",
}

// semanticTokenModifiers is intentionally empty - this server does not
// yet distinguish modifiers (e.g. readonly, static) on tokens.
var semanticTokenModifiers = []string{}

// semanticTokenTypeIndex maps a legend name to its index, so
// classification code never hardcodes a magic number.
var semanticTokenTypeIndex = func() map[string]int {
	m := make(map[string]int, len(semanticTokenTypes))
	for i, name := range semanticTokenTypes {
		m[name] = i
	}
	return m
}()

// SemanticTokensParams is the LSP SemanticTokensParams shape for the
// "full" request: only the document identity is needed, no position or
// range.
type SemanticTokensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// SemanticTokens is the LSP SemanticTokens result: a single flat,
// delta-encoded array as described in the LSP §3.17.6 spec.
type SemanticTokens struct {
	Data []int `json:"data"`
}

// semanticToken is an absolute-coordinate classified token before
// delta-encoding. Line and StartChar are 0-based, matching LSP Position.
type semanticToken struct {
	Line      int
	StartChar int
	Length    int
	TypeIndex int
}

// semanticTokensFull handles textDocument/semanticTokens/full with
// single-file scope. It lexes the document once to get the primary
// token stream, and again pulls the lexer's captured comment list
// (comments are stripped from the main token stream and aren't
// otherwise visible), classifies every lexeme into the fixed legend,
// and delta-encodes the result per the LSP spec.
//
// Operators and punctuation are omitted entirely rather than mapped to
// "operator" - editors fall back to syntax-grammar highlighting for
// them, and omitting keeps the data array focused on the tokens a
// grammar can't classify on its own (identifiers, comments, literals).
func (s *server) semanticTokensFull(params SemanticTokensParams) any {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return SemanticTokens{Data: []int{}}
	}
	lines := strings.Split(source, "\n")
	toks := classifySource(source, lines)
	return SemanticTokens{Data: encodeSemanticTokens(toks)}
}

// classifySource lexes source and returns every classified semantic
// token in absolute (0-based) coordinates, unsorted with respect to
// comments vs. the main token stream (encodeSemanticTokens sorts).
//
// The full token stream is buffered first (files are small enough that
// this is cheap) so that identifier classification can look one token
// ahead to detect function calls (`ident` immediately followed by `(`).
func classifySource(source string, lines []string) []semanticToken {
	l := lexer.New(source)
	var toks []token.Token
	for {
		tok := l.NextToken()
		if tok.Type == token.EOF {
			break
		}
		toks = append(toks, tok)
	}

	var out []semanticToken
	inDecorator := false // true while walking an `@name(.name)*` chain
	for i, tok := range toks {
		next := token.Token{}
		if i+1 < len(toks) {
			next = toks[i+1]
		}

		typeName, ok := classifyToken(tok, next)
		switch tok.Type {
		case token.At:
			inDecorator = true
		case token.Ident:
			if inDecorator {
				typeName, ok = "decorator", true
			}
			// A dotted decorator chain continues only through `.ident`;
			// anything else (e.g. `(` for arguments, or the next
			// statement) ends it.
			inDecorator = inDecorator && next.Type == token.Dot
		case token.Dot:
			// Preserve inDecorator across the dot itself; the dot
			// carries no semantic token of its own (operators are
			// omitted).
		default:
			inDecorator = false
		}

		if ok {
			out = append(out, semanticToken{
				Line:      tok.Line - 1,
				StartChar: tok.Column - 1,
				Length:    tokenRuneLength(tok),
				TypeIndex: semanticTokenTypeIndex[typeName],
			})
		}
	}

	for _, c := range l.Comments() {
		out = append(out, commentTokens(c, lines)...)
	}

	return out
}

// classifyToken maps a lexer token to a legend name, given the token
// that immediately follows it (used only for function-call detection).
// Reports ok=false for tokens intentionally omitted (operators,
// punctuation, illegal tokens). The caller (classifySource) overrides
// this result for identifiers inside a `@name(.name)*` decorator
// chain, reclassifying them as "decorator".
//
// Classification rules, in priority order:
//  1. `@` decorator marker -> "decorator"
//  2. identifier immediately followed by `(` -> "function"
//  3. built-in primitive type names, or any identifier starting with
//     an uppercase letter (heuristic - not semantic analysis) -> "type"
//  4. any other identifier -> "variable"
//  5. string / number literals -> "string" / "number"
//  6. everything else in the token enum that isn't an operator or
//     punctuation symbol is a reserved keyword -> "keyword"
func classifyToken(tok, next token.Token) (string, bool) {
	switch tok.Type {
	case token.EOF, token.Illegal:
		return "", false
	case token.Ident:
		if next.Type == token.LParen {
			return "function", true
		}
		return classifyIdent(tok.Literal), true
	case token.At:
		return "decorator", true
	case token.String:
		return "string", true
	case token.Int, token.Decimal, token.Float:
		return "number", true
	}
	if isOperatorOrPunctuation(tok.Type) {
		return "", false
	}
	return "keyword", true
}

func classifyIdent(literal string) string {
	if _, ok := primitiveTypeNames[literal]; ok {
		return "type"
	}
	if literal != "" && isUpperFirst(literal) {
		return "type"
	}
	return "variable"
}

func isUpperFirst(s string) bool {
	r := []rune(s)[0]
	return r >= 'A' && r <= 'Z'
}

// isOperatorOrPunctuation reports whether tok is a symbol this server
// omits from semantic tokens (see the doc comment on semanticTokensFull
// for the omit-vs-map-to-"operator" rationale).
func isOperatorOrPunctuation(t token.Type) bool {
	switch t {
	case token.Assign, token.Plus, token.Minus, token.Bang, token.Asterisk,
		token.Slash, token.IntDiv, token.Percent, token.Power,
		token.LT, token.GT, token.LTE, token.GTE, token.Eq, token.NotEq,
		token.And, token.Or, token.BitAnd, token.BitOr, token.BitXor, token.BitNot,
		token.LShift, token.RShift, token.Inc, token.Dec, token.Pipe,
		token.Dot, token.Ellipsis, token.OptionalChain, token.Range, token.RangeExcl,
		token.Comma, token.Colon, token.Semicolon, token.Question, token.NullCoalesce,
		token.PlusAssign, token.MinusAssign, token.MulAssign, token.DivAssign,
		token.IntDivAssign, token.ModAssign, token.PowerAssign,
		token.BitAndAssign, token.BitOrAssign, token.BitXorAssign,
		token.LShiftAssign, token.RShiftAssign, token.NullCoalesceAssign, token.Arrow,
		token.LParen, token.RParen, token.LBrace, token.RBrace, token.LBracket, token.RBracket:
		return true
	}
	return false
}

// tokenRuneLength returns the on-line rune width of tok's lexeme, for
// tokens whose Literal directly reflects source width. String tokens
// carry the unescaped value in Literal, so Raw (the original source
// slice between the quotes) is used instead, plus the quote runs on
// each side: 1 rune each for `"`/`'`, 3 runes each for `"""`.
func tokenRuneLength(tok token.Token) int {
	if tok.Type == token.String {
		quoteWidth := 1
		if tok.Triple {
			quoteWidth = 3
		}
		return len([]rune(tok.Raw)) + 2*quoteWidth
	}
	return len([]rune(tok.Literal))
}

// commentTokens converts one captured lexer comment into one semantic
// token per source line it covers. Line comments (`#`, `##`) are
// always single-line. Block comments (`/* */`, `/** */`) can span
// multiple lines and must be split - an LSP semantic token can never
// cross a line boundary.
func commentTokens(c lexer.Comment, lines []string) []semanticToken {
	lineIdx := c.Line - 1
	if lineIdx < 0 || lineIdx >= len(lines) {
		return nil
	}

	if !strings.HasSuffix(c.Kind, "block") {
		// Line comment: the marker ("#" or "##") starts wherever it
		// appears on its source line; the token covers marker through
		// end of line.
		marker := "#"
		text := lines[lineIdx]
		col := strings.Index(text, marker)
		if col < 0 {
			return nil
		}
		startChar := len([]rune(text[:col]))
		length := len([]rune(text)) - startChar
		return []semanticToken{{Line: lineIdx, StartChar: startChar, Length: length, TypeIndex: semanticTokenTypeIndex["comment"]}}
	}

	return blockCommentTokens(c, lines, lineIdx)
}

// blockCommentTokens splits a `/* ... */` or `/** ... */` comment into
// one token per line. c.Text is the exact raw text between the marker
// (after the doc `*` if present) and the closing `*/`, so its opening
// marker column is located textually on the start line and every
// subsequent line covers the full raw-content line up to (and
// including, for the last line) the closing `*/`.
func blockCommentTokens(c lexer.Comment, lines []string, startLineIdx int) []semanticToken {
	startText := lines[startLineIdx]
	col := strings.Index(startText, "/*")
	if col < 0 {
		return nil
	}
	startChar := len([]rune(startText[:col]))

	contentLines := strings.Split(c.Text, "\n")
	out := make([]semanticToken, 0, len(contentLines))

	// First line: from "/*" (or "/**") through end of that source line,
	// unless the comment also ends on the first line.
	if len(contentLines) == 1 {
		length := len([]rune(startText)) - startChar
		out = append(out, semanticToken{Line: startLineIdx, StartChar: startChar, Length: length, TypeIndex: semanticTokenTypeIndex["comment"]})
		return out
	}

	firstLen := len([]rune(startText)) - startChar
	out = append(out, semanticToken{Line: startLineIdx, StartChar: startChar, Length: firstLen, TypeIndex: semanticTokenTypeIndex["comment"]})

	for i := 1; i < len(contentLines); i++ {
		lineIdx := startLineIdx + i
		if lineIdx >= len(lines) {
			break
		}
		text := lines[lineIdx]
		length := len([]rune(text))
		if i == len(contentLines)-1 {
			// Last line: covers through the closing "*/" (end of the
			// source line covers it, since "*/" is the last thing the
			// lexer consumes before returning to normal scanning).
			if idx := strings.Index(text, "*/"); idx >= 0 {
				length = len([]rune(text[:idx])) + 2
			}
		}
		out = append(out, semanticToken{Line: lineIdx, StartChar: 0, Length: length, TypeIndex: semanticTokenTypeIndex["comment"]})
	}
	return out
}

// encodeSemanticTokens sorts classified tokens by (line, startChar) and
// delta-encodes them into the flat quintuplet array the LSP spec
// requires: [deltaLine, deltaStartChar, length, tokenType, tokenModifiers].
func encodeSemanticTokens(toks []semanticToken) []int {
	data := make([]int, 0, len(toks)*5)
	if len(toks) == 0 {
		return data
	}

	sort.Slice(toks, func(i, j int) bool {
		if toks[i].Line != toks[j].Line {
			return toks[i].Line < toks[j].Line
		}
		return toks[i].StartChar < toks[j].StartChar
	})

	prevLine, prevChar := 0, 0
	for _, t := range toks {
		deltaLine := t.Line - prevLine
		deltaStartChar := t.StartChar
		if deltaLine == 0 {
			deltaStartChar = t.StartChar - prevChar
		}
		data = append(data, deltaLine, deltaStartChar, t.Length, t.TypeIndex, 0)
		prevLine, prevChar = t.Line, t.StartChar
	}
	return data
}
