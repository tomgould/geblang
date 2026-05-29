package ast

import (
	"strings"

	"geblang/internal/token"
)

type Node interface {
	TokenLiteral() string
	String() string
}

type Statement interface {
	Node
	statementNode()
}

type Expression interface {
	Node
	expressionNode()
}

type Program struct {
	Statements []Statement
}

func (p *Program) TokenLiteral() string {
	if len(p.Statements) == 0 {
		return ""
	}
	return p.Statements[0].TokenLiteral()
}

func (p *Program) String() string {
	var out strings.Builder
	for _, stmt := range p.Statements {
		out.WriteString(stmt.String())
	}
	return out.String()
}

type TypeRef struct {
	Token     token.Token
	Name      string
	Nullable  bool
	Arguments []*TypeRef
	ListAlias bool
	Left      *TypeRef
	Operator  string
	Right     *TypeRef
}

func (t *TypeRef) String() string {
	if t == nil {
		return ""
	}
	if t.Operator != "" {
		return t.Left.String() + " " + t.Operator + " " + t.Right.String()
	}
	var out strings.Builder
	if t.Nullable {
		out.WriteByte('?')
	}
	out.WriteString(t.Name)
	if len(t.Arguments) > 0 {
		args := make([]string, 0, len(t.Arguments))
		for _, arg := range t.Arguments {
			args = append(args, arg.String())
		}
		out.WriteString("<")
		out.WriteString(strings.Join(args, ", "))
		out.WriteString(">")
	}
	if t.ListAlias {
		out.WriteString("[]")
	}
	return out.String()
}

type Parameter struct {
	Name     *Identifier
	Type     *TypeRef
	Default  Expression
	Variadic bool
}

func (p Parameter) String() string {
	parts := []string{}
	if p.Type != nil {
		parts = append(parts, p.Type.String())
	}
	if p.Variadic {
		parts = append(parts, "...")
	}
	if p.Name != nil {
		parts = append(parts, p.Name.String())
	}
	out := strings.Join(parts, " ")
	if p.Default != nil {
		out += " = " + p.Default.String()
	}
	return out
}

type SpreadExpression struct {
	Token token.Token
	Value Expression
}

func (*SpreadExpression) expressionNode()        {}
func (s *SpreadExpression) TokenLiteral() string { return s.Token.Literal }
func (s *SpreadExpression) String() string       { return "..." + s.Value.String() }

type BlockStatement struct {
	Token      token.Token
	Statements []Statement
}

func (*BlockStatement) statementNode()         {}
func (s *BlockStatement) TokenLiteral() string { return s.Token.Literal }
func (s *BlockStatement) String() string {
	var out strings.Builder
	out.WriteString("{")
	for _, stmt := range s.Statements {
		out.WriteString(stmt.String())
	}
	out.WriteString("}")
	return out.String()
}

type ModuleStatement struct {
	Token token.Token
	Path  []string
}

func (*ModuleStatement) statementNode()         {}
func (s *ModuleStatement) TokenLiteral() string { return s.Token.Literal }
func (s *ModuleStatement) String() string       { return "module " + strings.Join(s.Path, ".") + ";" }

type ImportStatement struct {
	Token token.Token
	Path  []string
	Alias *Identifier
}

func (*ImportStatement) statementNode()         {}
func (s *ImportStatement) TokenLiteral() string { return s.Token.Literal }
func (s *ImportStatement) ModuleName() string {
	if s.Alias != nil {
		return s.Alias.Value
	}
	if len(s.Path) == 0 {
		return ""
	}
	return s.Path[len(s.Path)-1]
}
func (s *ImportStatement) String() string {
	out := "import " + strings.Join(s.Path, ".")
	if s.Alias != nil {
		out += " as " + s.Alias.String()
	}
	return out + ";"
}

type FromImportName struct {
	Name  *Identifier
	Alias *Identifier
}

func (n FromImportName) Local() string {
	if n.Alias != nil {
		return n.Alias.Value
	}
	if n.Name != nil {
		return n.Name.Value
	}
	return ""
}

type FromImportStatement struct {
	Token token.Token
	Path  []string
	Names []FromImportName
}

func (*FromImportStatement) statementNode()         {}
func (s *FromImportStatement) TokenLiteral() string { return s.Token.Literal }
func (s *FromImportStatement) String() string {
	out := "from " + strings.Join(s.Path, ".") + " import "
	parts := make([]string, 0, len(s.Names))
	for _, n := range s.Names {
		piece := ""
		if n.Name != nil {
			piece = n.Name.String()
		}
		if n.Alias != nil {
			piece += " as " + n.Alias.String()
		}
		parts = append(parts, piece)
	}
	return out + strings.Join(parts, ", ") + ";"
}

type ExportStatement struct {
	Token     token.Token
	Statement Statement
}

func (*ExportStatement) statementNode()         {}
func (s *ExportStatement) TokenLiteral() string { return s.Token.Literal }
func (s *ExportStatement) String() string       { return "export " + s.Statement.String() }

type InitStatement struct {
	Token token.Token
	Body  *BlockStatement
}

func (*InitStatement) statementNode()         {}
func (s *InitStatement) TokenLiteral() string { return s.Token.Literal }
func (s *InitStatement) String() string       { return "init " + s.Body.String() }

type TypeAliasStatement struct {
	Token token.Token
	Name  *Identifier
	Type  *TypeRef
}

func (*TypeAliasStatement) statementNode()         {}
func (s *TypeAliasStatement) TokenLiteral() string { return s.Token.Literal }
func (s *TypeAliasStatement) String() string {
	return "type " + s.Name.String() + " = " + s.Type.String() + ";"
}

type DeclarationStatement struct {
	Token token.Token
	Kind  string
	Type  *TypeRef
	Name  *Identifier
	Value Expression
	// Decorators applies only to declarations that live inside a
	// class body (`@Assert.email string x;`). It is metadata only -
	// pure annotations consulted by reflection, never executed
	// automatically on field access or assignment. Empty for
	// declarations outside a class.
	Decorators []Decorator
}

func (*DeclarationStatement) statementNode()         {}
func (s *DeclarationStatement) TokenLiteral() string { return s.Token.Literal }
func (s *DeclarationStatement) String() string {
	parts := []string{}
	if s.Kind != "" {
		parts = append(parts, s.Kind)
	}
	if s.Type != nil {
		parts = append(parts, s.Type.String())
	}
	if s.Name != nil {
		parts = append(parts, s.Name.String())
	}
	out := strings.Join(parts, " ")
	if s.Value != nil {
		out += " = " + s.Value.String()
	}
	return out + ";"
}

type DestructuringStatement struct {
	Token  token.Token
	Names  []*Identifier
	Keys   []string // for dict pattern: keys to extract (parallel to Names)
	IsList bool     // true = list pattern [a,b], false = dict pattern {a,b}
	Define bool     // true for let destructuring, false for assignment
	Value  Expression
}

func (*DestructuringStatement) statementNode()         {}
func (s *DestructuringStatement) TokenLiteral() string { return s.Token.Literal }
func (s *DestructuringStatement) String() string {
	names := make([]string, len(s.Names))
	for i, n := range s.Names {
		names[i] = n.Value
	}
	joined := strings.Join(names, ", ")
	prefix := ""
	if s.Define {
		prefix = "let "
	}
	if s.IsList {
		return prefix + "[" + joined + "] = " + s.Value.String() + ";"
	}
	return prefix + "{" + joined + "} = " + s.Value.String() + ";"
}

type ExpressionStatement struct {
	Token      token.Token
	Expression Expression
}

func (*ExpressionStatement) statementNode()         {}
func (s *ExpressionStatement) TokenLiteral() string { return s.Token.Literal }
func (s *ExpressionStatement) String() string {
	if s.Expression == nil {
		return ";"
	}
	return s.Expression.String() + ";"
}

type ReturnStatement struct {
	Token token.Token
	Value Expression
}

func (*ReturnStatement) statementNode()         {}
func (s *ReturnStatement) TokenLiteral() string { return s.Token.Literal }
func (s *ReturnStatement) String() string {
	if s.Value == nil {
		return "return;"
	}
	return "return " + s.Value.String() + ";"
}

type YieldStatement struct {
	Token token.Token
	Value Expression
}

func (*YieldStatement) statementNode()         {}
func (s *YieldStatement) TokenLiteral() string { return s.Token.Literal }
func (s *YieldStatement) String() string {
	if s.Value == nil {
		return "yield;"
	}
	return "yield " + s.Value.String() + ";"
}

type SimpleStatement struct {
	Token token.Token
	Kind  string
	Value Expression
}

func (*SimpleStatement) statementNode()         {}
func (s *SimpleStatement) TokenLiteral() string { return s.Token.Literal }
func (s *SimpleStatement) String() string {
	if s.Value == nil {
		return s.Kind + ";"
	}
	return s.Kind + " " + s.Value.String() + ";"
}

type IfStatement struct {
	Token       token.Token
	Condition   Expression
	Consequence *BlockStatement
	ElseIfs     []ElseIfClause
	Alternative *BlockStatement
}

type ElseIfClause struct {
	Condition Expression
	Body      *BlockStatement
}

func (*IfStatement) statementNode()         {}
func (s *IfStatement) TokenLiteral() string { return s.Token.Literal }
func (s *IfStatement) String() string {
	return "if (" + s.Condition.String() + ") " + s.Consequence.String()
}

type WhileStatement struct {
	Token     token.Token
	Condition Expression
	Body      *BlockStatement
}

func (*WhileStatement) statementNode()         {}
func (s *WhileStatement) TokenLiteral() string { return s.Token.Literal }
func (s *WhileStatement) String() string {
	return "while (" + s.Condition.String() + ") " + s.Body.String()
}

// DelStatement represents `del x;` — a binding terminator that
// invokes the destructor of the value bound to `x` (if its class
// declares one) and removes the binding from the current scope.
// Subsequent references to `x` in the same control-flow path are
// rejected by the semantic analyzer. Only identifiers are
// supported in 1.0; `del a.b` / `del a[i]` are parse errors.
type DelStatement struct {
	Token  token.Token
	Target *Identifier
}

func (*DelStatement) statementNode()         {}
func (s *DelStatement) TokenLiteral() string { return s.Token.Literal }
func (s *DelStatement) String() string {
	if s.Target == nil {
		return "del"
	}
	return "del " + s.Target.String()
}

// WithStatement represents `with (expr) { ... }` or
// `with (name = expr) { ... }`. At block exit (normal completion,
// exception, return, break, or continue) the runtime invokes the
// bound value's `__exit__()` magic method when present, otherwise
// the destructor (`~ClassName()`) of the value's class. The
// optional `__enter__()` magic method's return value is what gets
// bound to Name when present.
type WithStatement struct {
	Token token.Token
	Name  *Identifier
	Value Expression
	Body  *BlockStatement
}

func (*WithStatement) statementNode()         {}
func (s *WithStatement) TokenLiteral() string { return s.Token.Literal }
func (s *WithStatement) String() string {
	bind := ""
	if s.Name != nil {
		bind = s.Name.String() + " = "
	}
	return "with (" + bind + s.Value.String() + ") " + s.Body.String()
}

type ForStatement struct {
	Token     token.Token
	Init      Statement
	Condition Expression
	Update    Statement
	VarType   *TypeRef
	VarName   *Identifier
	VarNames  []*Identifier
	Iterable  Expression
	Step      Expression
	Body      *BlockStatement
}

func (*ForStatement) statementNode()         {}
func (s *ForStatement) TokenLiteral() string { return s.Token.Literal }
func (s *ForStatement) String() string       { return "for (...) " + s.Body.String() }

type TypeParam struct {
	Name       *Identifier
	Constraint *TypeRef
}

type FunctionStatement struct {
	Token      token.Token
	Async      bool
	Static     bool
	Doc        string
	Decorators []Decorator
	Name       *Identifier
	Generics   []*TypeParam
	Parameters []Parameter
	ReturnType *TypeRef
	Body       *BlockStatement
}

func (*FunctionStatement) statementNode()         {}
func (s *FunctionStatement) TokenLiteral() string { return s.Token.Literal }
func (s *FunctionStatement) String() string {
	prefix := "func "
	if s.Async {
		prefix = "async " + prefix
	}
	if s.Static {
		prefix = "static " + prefix
	}
	return prefix + s.Name.String() + "(...) " + s.Body.String()
}

type ClassStatement struct {
	Token      token.Token
	Doc        string
	Decorators []Decorator
	Name       *Identifier
	Generics   []*TypeParam
	Extends    *TypeRef
	Implements []*TypeRef
	Members    []Statement
	// Destructor is the optional `func ~ClassName()` declaration.
	// Stored alongside Members so the executor can invoke it
	// specifically at instance cleanup (with-block exit, for now).
	Destructor *FunctionStatement
	// FieldDecorators[i] is the slice of decorators that prefixed
	// the i-th *DeclarationStatement* in Members. Stored on the
	// parent so reflection over fields can surface the per-field
	// metadata without touching the DeclarationStatement node
	// type (which is reused for top-level variable declarations
	// too).
	FieldDecorators map[string][]Decorator
}

func (*ClassStatement) statementNode()         {}
func (s *ClassStatement) TokenLiteral() string { return s.Token.Literal }
func (s *ClassStatement) String() string       { return "class " + s.Name.String() + " {...}" }

type Decorator struct {
	Token     token.Token
	Name      *Identifier
	Arguments []CallArgument
}

type InterfaceStatement struct {
	Token    token.Token
	Doc      string
	Name     *Identifier
	Generics []*TypeParam
	Parents  []*TypeRef
	Methods  []*FunctionSignature
	// Default method implementations. Inherited as-is by classes
	// that implement the interface and don't override the method.
	Defaults []*FunctionStatement
	// Property declarations. Auto-added as fields on every class
	// that implements the interface.
	Fields []*DeclarationStatement
}

func (*InterfaceStatement) statementNode()         {}
func (s *InterfaceStatement) TokenLiteral() string { return s.Token.Literal }
func (s *InterfaceStatement) String() string       { return "interface " + s.Name.String() + " {...}" }

type FunctionSignature struct {
	Token      token.Token
	Doc        string
	Name       *Identifier
	Generics   []*TypeParam
	Parameters []Parameter
	ReturnType *TypeRef
}

type TryStatement struct {
	Token   token.Token
	Body    *BlockStatement
	Catches []CatchClause
	Finally *BlockStatement
}

type CatchClause struct {
	Type *TypeRef
	Name *Identifier
	Body *BlockStatement
}

func (*TryStatement) statementNode()         {}
func (s *TryStatement) TokenLiteral() string { return s.Token.Literal }
func (s *TryStatement) String() string       { return "try " + s.Body.String() }

type EnumVariantDef struct {
	Name       *Identifier
	FieldTypes []*TypeRef
}

type EnumStatement struct {
	Token    token.Token
	Name     *Identifier
	Variants []EnumVariantDef
}

func (*EnumStatement) statementNode()         {}
func (s *EnumStatement) TokenLiteral() string { return s.Token.Literal }
func (s *EnumStatement) String() string       { return "enum " + s.Name.String() + " {...}" }

type EnumPayloadParam struct {
	Type *TypeRef
	Name *Identifier
}

type EnumVariantPattern struct {
	Token   token.Token
	Enum    *Identifier
	Variant *Identifier
	Params  []EnumPayloadParam
}

type MatchCase struct {
	Token       token.Token
	Pattern     Expression
	Type        *TypeRef
	Name        *Identifier
	Guard       Expression
	Body        *BlockStatement
	Value       Expression
	Default     bool
	EnumVariant *EnumVariantPattern
	ListPattern *ListPatternMatch
}

// ListPatternMatch describes a list-shape match pattern such as
// `case [int x, int y] => ...`. Bindings are positional; each may
// declare an optional type guard and a name to bind into the case
// scope. The name "_" suppresses binding (wildcard slot).
type ListPatternMatch struct {
	Token    token.Token
	Bindings []ListPatternBinding
}

type ListPatternBinding struct {
	Type *TypeRef    // optional element type guard
	Name *Identifier // required; "_" means "don't bind"
}

type MatchStatement struct {
	Token token.Token
	Expr  Expression
	Cases []MatchCase
}

func (*MatchStatement) statementNode()         {}
func (s *MatchStatement) TokenLiteral() string { return "match" }
func (s *MatchStatement) String() string       { return "match (" + s.Expr.String() + ") {...}" }

type Identifier struct {
	Token token.Token
	Value string
}

func (*Identifier) expressionNode()        {}
func (e *Identifier) TokenLiteral() string { return e.Token.Literal }
func (e *Identifier) String() string       { return e.Value }

type Literal struct {
	Token token.Token
	Value any
}

func (*Literal) expressionNode()        {}
func (e *Literal) TokenLiteral() string { return e.Token.Literal }
func (e *Literal) String() string       { return e.Token.Literal }

type StringLiteral struct {
	Token  token.Token
	Value  string
	Raw    string
	Quote  byte
	Triple bool
}

func (*StringLiteral) expressionNode()        {}
func (e *StringLiteral) TokenLiteral() string { return e.Token.Literal }
func (e *StringLiteral) String() string {
	quote := e.Quote
	if quote == 0 {
		quote = '"'
	}
	if e.Triple {
		return strings.Repeat(string(quote), 3) + e.Raw + strings.Repeat(string(quote), 3)
	}
	return string(quote) + e.Raw + string(quote)
}

type InterpolatedString struct {
	Token token.Token
	Parts []Expression // mix of *StringLiteral (literal segments) and arbitrary expressions
}

func (*InterpolatedString) expressionNode()        {}
func (e *InterpolatedString) TokenLiteral() string { return e.Token.Literal }
func (e *InterpolatedString) String() string {
	var sb strings.Builder
	sb.WriteRune('"')
	for _, p := range e.Parts {
		if sl, ok := p.(*StringLiteral); ok {
			sb.WriteString(sl.Raw)
		} else {
			sb.WriteString("${")
			sb.WriteString(p.String())
			sb.WriteString("}")
		}
	}
	sb.WriteRune('"')
	return sb.String()
}

type IntegerLiteral struct {
	Token token.Token
	Value string
}

func (*IntegerLiteral) expressionNode()        {}
func (e *IntegerLiteral) TokenLiteral() string { return e.Token.Literal }
func (e *IntegerLiteral) String() string       { return e.Token.Literal }

type DecimalLiteral struct {
	Token token.Token
	Value string
}

func (*DecimalLiteral) expressionNode()        {}
func (e *DecimalLiteral) TokenLiteral() string { return e.Token.Literal }
func (e *DecimalLiteral) String() string       { return e.Token.Literal }

type FloatLiteral struct {
	Token token.Token
	Value string
}

func (*FloatLiteral) expressionNode()        {}
func (e *FloatLiteral) TokenLiteral() string { return e.Token.Literal }
func (e *FloatLiteral) String() string       { return e.Token.Literal }

type PrefixExpression struct {
	Token    token.Token
	Operator string
	Right    Expression
}

func (*PrefixExpression) expressionNode()        {}
func (e *PrefixExpression) TokenLiteral() string { return e.Token.Literal }
func (e *PrefixExpression) String() string       { return "(" + e.Operator + e.Right.String() + ")" }

type PostfixExpression struct {
	Token    token.Token
	Left     Expression
	Operator string
}

func (*PostfixExpression) expressionNode()        {}
func (e *PostfixExpression) TokenLiteral() string { return e.Token.Literal }
func (e *PostfixExpression) String() string       { return "(" + e.Left.String() + e.Operator + ")" }

type InfixExpression struct {
	Token    token.Token
	Left     Expression
	Operator string
	Right    Expression
}

func (*InfixExpression) expressionNode()        {}
func (e *InfixExpression) TokenLiteral() string { return e.Token.Literal }
func (e *InfixExpression) String() string {
	return "(" + e.Left.String() + " " + e.Operator + " " + e.Right.String() + ")"
}

type AssignmentExpression struct {
	Token token.Token
	Left  Expression
	Value Expression
}

func (*AssignmentExpression) expressionNode()        {}
func (e *AssignmentExpression) TokenLiteral() string { return e.Token.Literal }
func (e *AssignmentExpression) String() string {
	return e.Left.String() + " = " + e.Value.String()
}

type SelectorExpression struct {
	Token    token.Token
	Object   Expression
	Name     *Identifier
	Optional bool
	// Parenthesized is set when the parser sees a literal `(obj.x)`
	// group enclosing this selector. At call time `(obj.x)(args)`
	// invokes the value of obj.x rather than dispatching as a
	// method call on obj. Without parens `obj.x(args)` keeps its
	// usual method-call interpretation.
	Parenthesized bool
}

func (*SelectorExpression) expressionNode()        {}
func (e *SelectorExpression) TokenLiteral() string { return e.Token.Literal }
func (e *SelectorExpression) String() string {
	if e.Optional {
		return e.Object.String() + "?." + e.Name.String()
	}
	return e.Object.String() + "." + e.Name.String()
}

type CallArgument struct {
	Name   *Identifier
	Value  Expression
	Spread bool
}

type CallExpression struct {
	Token token.Token
	// TypeArguments are the explicit generic type arguments written between
	// the callee and the argument list, e.g. `Box<int>(...)` or
	// `assertIs<string>("hi")`. nil when no explicit `<...>` clause is
	// present (the call may still be a call to a generic function/class —
	// inference fills in the bindings).
	TypeArguments []*TypeRef
	Callee        Expression
	Arguments     []CallArgument
}

func (*CallExpression) expressionNode()        {}
func (e *CallExpression) TokenLiteral() string { return e.Token.Literal }
func (e *CallExpression) String() string {
	args := make([]string, 0, len(e.Arguments))
	for _, arg := range e.Arguments {
		if arg.Name != nil {
			args = append(args, arg.Name.String()+": "+arg.Value.String())
		} else {
			args = append(args, arg.Value.String())
		}
	}
	typeArgs := ""
	if len(e.TypeArguments) > 0 {
		parts := make([]string, 0, len(e.TypeArguments))
		for _, t := range e.TypeArguments {
			parts = append(parts, t.String())
		}
		typeArgs = "<" + strings.Join(parts, ", ") + ">"
	}
	return e.Callee.String() + typeArgs + "(" + strings.Join(args, ", ") + ")"
}

type IndexExpression struct {
	Token token.Token
	Left  Expression
	Index Expression
}

func (*IndexExpression) expressionNode()        {}
func (e *IndexExpression) TokenLiteral() string { return e.Token.Literal }
func (e *IndexExpression) String() string       { return e.Left.String() + "[" + e.Index.String() + "]" }

type ListLiteral struct {
	Token    token.Token
	Elements []Expression
}

func (*ListLiteral) expressionNode()        {}
func (e *ListLiteral) TokenLiteral() string { return e.Token.Literal }
func (e *ListLiteral) String() string {
	parts := make([]string, 0, len(e.Elements))
	for _, el := range e.Elements {
		parts = append(parts, el.String())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

type DictEntry struct {
	Key   Expression
	Value Expression
}

type DictLiteral struct {
	Token   token.Token
	Entries []DictEntry
}

func (*DictLiteral) expressionNode()        {}
func (e *DictLiteral) TokenLiteral() string { return e.Token.Literal }
func (e *DictLiteral) String() string       { return "{...}" }

type SetLiteral struct {
	Token    token.Token
	Elements []Expression
}

func (*SetLiteral) expressionNode()        {}
func (e *SetLiteral) TokenLiteral() string { return e.Token.Literal }
func (e *SetLiteral) String() string {
	parts := make([]string, 0, len(e.Elements))
	for _, el := range e.Elements {
		parts = append(parts, el.String())
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

type RangeExpression struct {
	Token     token.Token
	Start     Expression
	End       Expression
	Exclusive bool
	Step      Expression
}

func (*RangeExpression) expressionNode()        {}
func (e *RangeExpression) TokenLiteral() string { return e.Token.Literal }
func (e *RangeExpression) String() string       { return "range" }

type FunctionLiteral struct {
	Token      token.Token
	Async      bool
	Parameters []Parameter
	ReturnType *TypeRef
	Body       *BlockStatement
}

func (*FunctionLiteral) expressionNode()        {}
func (e *FunctionLiteral) TokenLiteral() string { return e.Token.Literal }
func (e *FunctionLiteral) String() string       { return "func(...) " + e.Body.String() }

type MatchExpression struct {
	Token token.Token
	Expr  Expression
	Cases []MatchCase
}

func (*MatchExpression) expressionNode()        {}
func (e *MatchExpression) TokenLiteral() string { return e.Token.Literal }
func (e *MatchExpression) String() string       { return "match (" + e.Expr.String() + ") {...}" }

type AwaitExpression struct {
	Token token.Token
	Value Expression
}

func (*AwaitExpression) expressionNode()        {}
func (e *AwaitExpression) TokenLiteral() string { return e.Token.Literal }
func (e *AwaitExpression) String() string       { return "await " + e.Value.String() }

type CastExpression struct {
	Token token.Token
	Value Expression
	Type  *TypeRef
}

func (*CastExpression) expressionNode()        {}
func (e *CastExpression) TokenLiteral() string { return e.Token.Literal }
func (e *CastExpression) String() string       { return e.Value.String() + " as " + e.Type.String() }

type TernaryExpression struct {
	Token     token.Token
	Condition Expression
	ThenExpr  Expression
	ElseExpr  Expression
}

func (*TernaryExpression) expressionNode()        {}
func (e *TernaryExpression) TokenLiteral() string { return e.Token.Literal }
func (e *TernaryExpression) String() string {
	return "(" + e.Condition.String() + " ? " + e.ThenExpr.String() + " : " + e.ElseExpr.String() + ")"
}
