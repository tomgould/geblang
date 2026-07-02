package lsp

import (
	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/native"
	"geblang/internal/parser"
	"geblang/internal/token"
)

// inlayHintKindParameter is the LSP InlayHintKind for a parameter-name
// hint (InlayHintKind.Parameter = 2 per LSP 3.17 section 3.17.16).
const inlayHintKindParameter = 2

// InlayHintParams is the LSP InlayHintParams shape for
// textDocument/inlayHint: the document plus the visible range the
// client wants hints for.
type InlayHintParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

// InlayHint is an LSP InlayHint (the parameter-name-hint subset this
// server emits; label is always a plain string, never the structured
// InlayHintLabelPart form).
type InlayHint struct {
	Position     Position `json:"position"`
	Label        string   `json:"label"`
	Kind         int      `json:"kind,omitempty"`
	PaddingRight bool     `json:"paddingRight,omitempty"`
}

// inlayHint handles textDocument/inlayHint with single-file scope.
// It parses the document, walks every call expression within
// params.Range, and for each positional argument whose callee resolves
// cleanly to a known parameter list, emits a "<paramName>:" hint at the
// argument's start position.
//
// Resolution is deliberately conservative: a call is skipped entirely
// (no hints, not guessed hints) unless its callee is either a bare
// identifier naming a function declared in this file, or a
// `module.function` selector naming a stdlib catalog entry. Named
// arguments, spread arguments, and hole placeholders never get a hint
// since their position doesn't necessarily correspond to the
// like-positioned parameter. Extra positional arguments beyond the
// known parameter count (variadic calls, mismatched arity) are simply
// left unannotated rather than indexed out of range.
func (s *server) inlayHint(params InlayHintParams) any {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return []InlayHint{}
	}
	prog := parser.New(lexer.New(source)).ParseProgram()
	if prog == nil {
		return []InlayHint{}
	}

	funcs := collectFileFunctions(prog)

	var calls []*ast.CallExpression
	for _, stmt := range prog.Statements {
		collectCallsInStatement(stmt, &calls)
	}

	hints := []InlayHint{}
	for _, call := range calls {
		pos, ok := exprPosition(call)
		if !ok || !positionInRange(pos, params.Range) {
			continue
		}
		paramNames, ok := resolveCalleeParamNames(call.Callee, funcs)
		if !ok {
			continue
		}
		hints = append(hints, callArgumentHints(call, paramNames)...)
	}
	return hints
}

// collectFileFunctions indexes every top-level function declaration
// (including ones wrapped in `export`) by name, so bare-identifier
// calls can be resolved to their declared parameter names. Class
// methods are excluded: they are only ever reached through a selector
// (`self.foo()` / `obj.foo()`), never a bare identifier, so they can't
// be the target of this lookup.
func collectFileFunctions(prog *ast.Program) map[string]*ast.FunctionStatement {
	funcs := map[string]*ast.FunctionStatement{}
	var visit func(stmt ast.Statement)
	visit = func(stmt ast.Statement) {
		switch s := stmt.(type) {
		case *ast.FunctionStatement:
			if s.Name != nil {
				funcs[s.Name.Value] = s
			}
		case *ast.ExportStatement:
			visit(s.Statement)
		}
	}
	for _, stmt := range prog.Statements {
		visit(stmt)
	}
	return funcs
}

// resolveCalleeParamNames resolves a call's callee to its declared
// parameter names. Returns ok=false when the callee isn't one of the
// two supported shapes, or names when it's supported but no matching
// declaration/catalog entry exists - both cases mean "emit nothing".
func resolveCalleeParamNames(callee ast.Expression, funcs map[string]*ast.FunctionStatement) ([]string, bool) {
	switch c := callee.(type) {
	case *ast.Identifier:
		fn, ok := funcs[c.Value]
		if !ok {
			return nil, false
		}
		names := make([]string, 0, len(fn.Parameters))
		for _, p := range fn.Parameters {
			if p.Name == nil {
				return nil, false
			}
			names = append(names, p.Name.Value)
		}
		return names, true

	case *ast.SelectorExpression:
		obj, ok := c.Object.(*ast.Identifier)
		if !ok || c.Name == nil {
			return nil, false
		}
		return native.NativeParamNames(obj.Value, c.Name.Value)
	}
	return nil, false
}

// callArgumentHints builds one hint per positional argument of call,
// pairing arguments to paramNames by position. Named arguments, spread
// arguments, and hole placeholders are skipped (their position doesn't
// necessarily line up with the like-indexed parameter). Stops once
// either list is exhausted rather than indexing past the shorter one,
// so variadic/mismatched-arity calls simply go unannotated past the
// declared parameters.
func callArgumentHints(call *ast.CallExpression, paramNames []string) []InlayHint {
	hints := []InlayHint{}
	paramIdx := 0
	for _, arg := range call.Arguments {
		if paramIdx >= len(paramNames) {
			break
		}
		if arg.Name != nil || arg.Spread || arg.Hole {
			paramIdx++
			continue
		}
		pos, ok := exprPosition(arg.Value)
		if ok {
			hints = append(hints, InlayHint{
				Position:     pos,
				Label:        paramNames[paramIdx] + ":",
				Kind:         inlayHintKindParameter,
				PaddingRight: true,
			})
		}
		paramIdx++
	}
	return hints
}

// positionInRange reports whether pos falls within r, inclusive of
// both endpoints (matching LSP Range semantics).
func positionInRange(pos Position, r Range) bool {
	if pos.Line < r.Start.Line || (pos.Line == r.Start.Line && pos.Character < r.Start.Character) {
		return false
	}
	if pos.Line > r.End.Line || (pos.Line == r.End.Line && pos.Character > r.End.Character) {
		return false
	}
	return true
}

// collectCallsInStatement walks stmt and every nested statement/
// expression/block reachable from it, appending every *ast.CallExpression
// found to out. Coverage mirrors lintMarkExpressionIdentifiers /
// statementPosition in internal/check/lint.go - the existing idiom for
// generic AST walks in this codebase - extended to also descend into
// blocks so calls inside function/method/control-flow bodies are found.
func collectCallsInStatement(stmt ast.Statement, out *[]*ast.CallExpression) {
	if stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.ExportStatement:
		collectCallsInStatement(s.Statement, out)
	case *ast.DeclarationStatement:
		collectCallsInExpression(s.Value, out)
	case *ast.DestructuringStatement:
		collectCallsInExpression(s.Value, out)
	case *ast.ExpressionStatement:
		collectCallsInExpression(s.Expression, out)
	case *ast.ReturnStatement:
		collectCallsInExpression(s.Value, out)
	case *ast.YieldStatement:
		collectCallsInExpression(s.Value, out)
	case *ast.SimpleStatement:
		collectCallsInExpression(s.Value, out)
	case *ast.DelStatement:
		// Target is an *ast.Identifier only - nothing to descend into.
	case *ast.IfStatement:
		collectCallsInExpression(s.Condition, out)
		collectCallsInBlock(s.Consequence, out)
		for _, clause := range s.ElseIfs {
			collectCallsInExpression(clause.Condition, out)
			collectCallsInBlock(clause.Body, out)
		}
		collectCallsInBlock(s.Alternative, out)
	case *ast.WhileStatement:
		collectCallsInExpression(s.Condition, out)
		collectCallsInBlock(s.Body, out)
	case *ast.WithStatement:
		collectCallsInExpression(s.Value, out)
		collectCallsInBlock(s.Body, out)
	case *ast.ForStatement:
		collectCallsInStatement(s.Init, out)
		collectCallsInExpression(s.Condition, out)
		collectCallsInStatement(s.Update, out)
		collectCallsInExpression(s.Iterable, out)
		collectCallsInExpression(s.Step, out)
		collectCallsInBlock(s.Body, out)
	case *ast.FunctionStatement:
		collectCallsInBlock(s.Body, out)
	case *ast.ClassStatement:
		for _, member := range s.Members {
			collectCallsInStatement(member, out)
		}
		if s.Destructor != nil {
			collectCallsInBlock(s.Destructor.Body, out)
		}
	case *ast.InterfaceStatement:
		for _, def := range s.Defaults {
			collectCallsInBlock(def.Body, out)
		}
	case *ast.TryStatement:
		collectCallsInBlock(s.Body, out)
		for _, clause := range s.Catches {
			collectCallsInBlock(clause.Body, out)
		}
		collectCallsInBlock(s.Finally, out)
	case *ast.EnumStatement:
		for _, method := range s.Methods {
			collectCallsInBlock(method.Body, out)
		}
	case *ast.MatchStatement:
		collectCallsInExpression(s.Expr, out)
		for _, c := range s.Cases {
			collectCallsInMatchCase(c, out)
		}
	case *ast.SelectStatement:
		for _, c := range s.Cases {
			collectCallsInExpression(c.Channel, out)
			collectCallsInExpression(c.Value, out)
			collectCallsInBlock(c.Body, out)
		}
		collectCallsInBlock(s.Default, out)
	case *ast.BlockStatement:
		collectCallsInBlock(s, out)
	}
}

func collectCallsInMatchCase(c ast.MatchCase, out *[]*ast.CallExpression) {
	collectCallsInExpression(c.Guard, out)
	collectCallsInExpression(c.Value, out)
	collectCallsInBlock(c.Body, out)
}

func collectCallsInBlock(block *ast.BlockStatement, out *[]*ast.CallExpression) {
	if block == nil {
		return
	}
	for _, stmt := range block.Statements {
		collectCallsInStatement(stmt, out)
	}
}

// collectCallsInExpression walks expr and every nested expression
// reachable from it, appending every *ast.CallExpression found to out
// (including expr itself when it is a call). Coverage mirrors
// lintMarkExpressionIdentifiers in internal/check/lint.go.
func collectCallsInExpression(expr ast.Expression, out *[]*ast.CallExpression) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.CallExpression:
		*out = append(*out, e)
		collectCallsInExpression(e.Callee, out)
		for _, arg := range e.Arguments {
			collectCallsInExpression(arg.Value, out)
		}
	case *ast.SpreadExpression:
		collectCallsInExpression(e.Value, out)
	case *ast.InterpolatedString:
		for _, part := range e.Parts {
			collectCallsInExpression(part, out)
		}
	case *ast.FormattedInterpolation:
		collectCallsInExpression(e.Value, out)
	case *ast.PrefixExpression:
		collectCallsInExpression(e.Right, out)
	case *ast.PostfixExpression:
		collectCallsInExpression(e.Left, out)
	case *ast.InfixExpression:
		collectCallsInExpression(e.Left, out)
		collectCallsInExpression(e.Right, out)
	case *ast.AssignmentExpression:
		collectCallsInExpression(e.Left, out)
		collectCallsInExpression(e.Value, out)
	case *ast.SelectorExpression:
		collectCallsInExpression(e.Object, out)
	case *ast.IndexExpression:
		collectCallsInExpression(e.Left, out)
		collectCallsInExpression(e.Index, out)
	case *ast.ListLiteral:
		for _, el := range e.Elements {
			collectCallsInExpression(el, out)
		}
	case *ast.DictLiteral:
		for _, entry := range e.Entries {
			collectCallsInExpression(entry.Key, out)
			collectCallsInExpression(entry.Value, out)
		}
	case *ast.SetLiteral:
		for _, el := range e.Elements {
			collectCallsInExpression(el, out)
		}
	case *ast.ListComprehension:
		collectCallsInExpression(e.Body, out)
		collectCallsInComprehensionClauses(e.Clauses, out)
	case *ast.SetComprehension:
		collectCallsInExpression(e.Body, out)
		collectCallsInComprehensionClauses(e.Clauses, out)
	case *ast.DictComprehension:
		collectCallsInExpression(e.KeyBody, out)
		collectCallsInExpression(e.ValueBody, out)
		collectCallsInComprehensionClauses(e.Clauses, out)
	case *ast.RangeExpression:
		collectCallsInExpression(e.Start, out)
		collectCallsInExpression(e.End, out)
		collectCallsInExpression(e.Step, out)
	case *ast.PipeExpression:
		collectCallsInExpression(e.Left, out)
		collectCallsInExpression(e.Right, out)
	case *ast.PartialExpression:
		collectCallsInExpression(e.Callee, out)
		for _, arg := range e.Arguments {
			if !arg.Hole {
				collectCallsInExpression(arg.Value, out)
			}
		}
	case *ast.FunctionLiteral:
		collectCallsInBlock(e.Body, out)
	case *ast.MatchExpression:
		collectCallsInExpression(e.Expr, out)
		for _, c := range e.Cases {
			collectCallsInMatchCase(c, out)
		}
	case *ast.AwaitExpression:
		collectCallsInExpression(e.Value, out)
	case *ast.CastExpression:
		collectCallsInExpression(e.Value, out)
	case *ast.TernaryExpression:
		collectCallsInExpression(e.Condition, out)
		collectCallsInExpression(e.ThenExpr, out)
		collectCallsInExpression(e.ElseExpr, out)
	}
}

func collectCallsInComprehensionClauses(clauses []ast.ComprehensionClause, out *[]*ast.CallExpression) {
	for _, clause := range clauses {
		switch c := clause.(type) {
		case *ast.ComprehensionFor:
			collectCallsInExpression(c.Iterable, out)
		case *ast.ComprehensionIf:
			collectCallsInExpression(c.Filter, out)
		}
	}
}

// exprPosition returns the 0-based LSP position of expr's leading
// token. Reports ok=false only for a nil expression or an expression
// kind added to the AST that this switch hasn't been taught about yet
// (every expression kind that exists today carries a Token field).
func exprPosition(expr ast.Expression) (Position, bool) {
	if expr == nil {
		return Position{}, false
	}
	switch e := expr.(type) {
	case *ast.Identifier:
		return tokenPosition(e.Token), true
	case *ast.Literal:
		return tokenPosition(e.Token), true
	case *ast.StringLiteral:
		return tokenPosition(e.Token), true
	case *ast.InterpolatedString:
		return tokenPosition(e.Token), true
	case *ast.FormattedInterpolation:
		return tokenPosition(e.Token), true
	case *ast.IntegerLiteral:
		return tokenPosition(e.Token), true
	case *ast.DecimalLiteral:
		return tokenPosition(e.Token), true
	case *ast.FloatLiteral:
		return tokenPosition(e.Token), true
	case *ast.SpreadExpression:
		return tokenPosition(e.Token), true
	case *ast.PrefixExpression:
		return tokenPosition(e.Token), true
	case *ast.PostfixExpression:
		return tokenPosition(e.Token), true
	case *ast.InfixExpression:
		return tokenPosition(e.Token), true
	case *ast.AssignmentExpression:
		return tokenPosition(e.Token), true
	case *ast.SelectorExpression:
		return tokenPosition(e.Token), true
	case *ast.CallExpression:
		return tokenPosition(e.Token), true
	case *ast.IndexExpression:
		return tokenPosition(e.Token), true
	case *ast.ListLiteral:
		return tokenPosition(e.Token), true
	case *ast.DictLiteral:
		return tokenPosition(e.Token), true
	case *ast.SetLiteral:
		return tokenPosition(e.Token), true
	case *ast.ListComprehension:
		return tokenPosition(e.Token), true
	case *ast.SetComprehension:
		return tokenPosition(e.Token), true
	case *ast.DictComprehension:
		return tokenPosition(e.Token), true
	case *ast.PipeExpression:
		return tokenPosition(e.Token), true
	case *ast.PartialExpression:
		return tokenPosition(e.Token), true
	case *ast.RangeExpression:
		return tokenPosition(e.Token), true
	case *ast.FunctionLiteral:
		return tokenPosition(e.Token), true
	case *ast.MatchExpression:
		return tokenPosition(e.Token), true
	case *ast.AwaitExpression:
		return tokenPosition(e.Token), true
	case *ast.CastExpression:
		return tokenPosition(e.Token), true
	case *ast.TernaryExpression:
		return tokenPosition(e.Token), true
	}
	return Position{}, false
}

// tokenPosition converts a 1-based lexer token position to a 0-based
// LSP Position.
func tokenPosition(tok token.Token) Position {
	line := tok.Line - 1
	if line < 0 {
		line = 0
	}
	col := tok.Column - 1
	if col < 0 {
		col = 0
	}
	return Position{Line: line, Character: col}
}
