package token

import "strconv"

type Type int

const (
	Illegal Type = iota
	EOF

	Ident
	Int
	Decimal
	Float
	String

	Assign
	Plus
	Minus
	Bang
	Asterisk
	Slash
	IntDiv
	Percent
	Power

	LT
	GT
	LTE
	GTE
	Eq
	NotEq
	And
	Or
	BitAnd
	BitOr
	BitXor
	BitNot
	LShift
	RShift
	Inc
	Dec
	Pipe

	Dot
	Ellipsis
	OptionalChain
	Range
	RangeExcl
	Comma
	Colon
	Semicolon
	Question
	NullCoalesce
	PlusAssign
	MinusAssign
	MulAssign
	DivAssign
	IntDivAssign
	ModAssign
	PowerAssign
	BitAndAssign
	BitOrAssign
	BitXorAssign
	LShiftAssign
	RShiftAssign
	NullCoalesceAssign
	Arrow
	At

	LParen
	RParen
	LBrace
	RBrace
	LBracket
	RBracket

	As
	Async
	Await
	Bool
	Break
	Case
	Catch
	Class
	Const
	Continue
	Default
	Defer
	Else
	ElseIf
	Extends
	Export
	False
	Finally
	For
	Func
	If
	Implements
	Import
	In
	Init
	InstanceOf
	Interface
	Is
	Let
	Match
	Module
	Not
	Null
	Parent
	Return
	Static
	This
	Throw
	TypeKw
	True
	Try
	While
	Xor
	Yield
	By
	Enum
	With
	Del
	Select
)

var typeNames = map[Type]string{
	Illegal: "ILLEGAL",
	EOF:     "EOF",

	Ident:   "IDENT",
	Int:     "INT",
	Decimal: "DECIMAL",
	Float:   "FLOAT",
	String:  "STRING",

	Assign:   "=",
	Plus:     "+",
	Minus:    "-",
	Bang:     "!",
	Asterisk: "*",
	Slash:    "/",
	IntDiv:   "//",
	Percent:  "%",
	Power:    "**",

	LT:     "<",
	GT:     ">",
	LTE:    "<=",
	GTE:    ">=",
	Eq:     "==",
	NotEq:  "!=",
	And:    "&&",
	Or:     "||",
	BitAnd: "&",
	BitOr:  "|",
	BitXor: "^",
	BitNot: "~",
	LShift: "<<",
	RShift: ">>",
	Inc:    "++",
	Dec:    "--",
	Pipe:   "|>",

	Dot:           ".",
	Ellipsis:      "...",
	OptionalChain: "?.",
	Range:         "..",
	RangeExcl:     "..<",
	Comma:         ",",
	Colon:         ":",
	Semicolon:     ";",
	Question:      "?",
	NullCoalesce:       "??",
	PlusAssign:         "+=",
	MinusAssign:        "-=",
	MulAssign:          "*=",
	DivAssign:          "/=",
	IntDivAssign:       "//=",
	ModAssign:          "%=",
	PowerAssign:        "**=",
	BitAndAssign:       "&=",
	BitOrAssign:        "|=",
	BitXorAssign:       "^=",
	LShiftAssign:       "<<=",
	RShiftAssign:       ">>=",
	NullCoalesceAssign: "??=",
	Arrow:              "=>",
	At:            "@",

	LParen:   "(",
	RParen:   ")",
	LBrace:   "{",
	RBrace:   "}",
	LBracket: "[",
	RBracket: "]",

	As:         "AS",
	Async:      "ASYNC",
	Await:      "AWAIT",
	Bool:       "BOOL",
	Break:      "BREAK",
	Case:       "CASE",
	Catch:      "CATCH",
	Class:      "CLASS",
	Const:      "CONST",
	Continue:   "CONTINUE",
	Default:    "DEFAULT",
	Defer:      "DEFER",
	Else:       "ELSE",
	ElseIf:     "ELSEIF",
	Extends:    "EXTENDS",
	Export:     "EXPORT",
	False:      "FALSE",
	Finally:    "FINALLY",
	For:        "FOR",
	Func:       "FUNC",
	If:         "IF",
	Implements: "IMPLEMENTS",
	Import:     "IMPORT",
	In:         "IN",
	Init:       "INIT",
	InstanceOf: "INSTANCEOF",
	Interface:  "INTERFACE",
	Is:         "IS",
	Let:        "LET",
	Match:      "MATCH",
	Module:     "MODULE",
	Not:        "NOT",
	Null:       "NULL",
	Parent:     "PARENT",
	Return:     "RETURN",
	Static:     "STATIC",
	This:       "THIS",
	Throw:      "THROW",
	TypeKw:     "TYPE",
	True:       "TRUE",
	Try:        "TRY",
	While:      "WHILE",
	Xor:        "XOR",
	Yield:      "YIELD",
	By:         "BY",
	With:       "WITH",
	Del:        "DEL",
	Select:     "SELECT", // contextual; not in keywords map
	Enum:       "ENUM",
}

func (t Type) String() string {
	if name, ok := typeNames[t]; ok {
		return name
	}
	return "TOKEN(" + strconv.Itoa(int(t)) + ")"
}

type Token struct {
	Type         Type
	Literal      string
	Raw          string
	Quote        byte
	Triple       bool
	Interpolated bool
	Line         int
	Column       int
	Doc          string
}

var keywords = map[string]Type{
	"as":         As,
	"async":      Async,
	"await":      Await,
	"bool":       Bool,
	"break":      Break,
	"by":         By,
	"case":       Case,
	"catch":      Catch,
	"class":      Class,
	"const":      Const,
	"continue":   Continue,
	"default":    Default,
	"defer":      Defer,
	"else":       Else,
	"elseif":     ElseIf,
	"extends":    Extends,
	"export":     Export,
	"false":      False,
	"finally":    Finally,
	"for":        For,
	"func":       Func,
	"if":         If,
	"implements": Implements,
	"import":     Import,
	"in":         In,
	"init":       Init,
	"instanceof": InstanceOf,
	"interface":  Interface,
	"is":         Is,
	"let":        Let,
	"match":      Match,
	"module":     Module,
	"not":        Not,
	"null":       Null,
	"parent":     Parent,
	"return":     Return,
	"static":     Static,
	"this":       This,
	"throw":      Throw,
	"type":       TypeKw,
	"true":       True,
	"try":        Try,
	"while":      While,
	"xor":        Xor,
	"yield":      Yield,
	"enum":       Enum,
	"with":       With,
	"del":        Del,
}

func LookupIdent(literal string) Type {
	if tok, ok := keywords[literal]; ok {
		return tok
	}
	return Ident
}

// Keywords returns the canonical set of reserved keyword literals. It is
// the single source of truth for keyword identity; the editor grammar's
// keyword highlighting is guarded against it.
func Keywords() []string {
	out := make([]string, 0, len(keywords))
	for k := range keywords {
		out = append(out, k)
	}
	return out
}
