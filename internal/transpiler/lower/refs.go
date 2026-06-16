package lower

import "geblang/internal/ast"

// referencesFree reports whether `name` occurs free in node - as an identifier not
// shadowed by a parameter, local declaration, loop/catch/with binding, or nested
// function-literal parameter. Used to decide entry-module hoisting: only a binding
// genuinely read by a sibling function is hoisted to package scope.
func referencesFree(node ast.Node, name string) bool {
	if name == "" || name == "_" {
		return false
	}
	s := &freeScanner{name: name}
	s.walk(node, map[string]bool{})
	return s.found
}

// functionReadsFree reports whether fn reads `name` as a free variable - a
// parameter of that name shadows the whole body and so reads nothing module-level.
func functionReadsFree(fn *ast.FunctionStatement, name string) bool {
	if fn.Body == nil {
		return false
	}
	for _, p := range fn.Parameters {
		if p.Name != nil && p.Name.Value == name {
			return false
		}
	}
	return referencesFree(fn.Body, name)
}

type freeScanner struct {
	name  string
	found bool
}

func extendBound(bound map[string]bool, names ...string) map[string]bool {
	next := make(map[string]bool, len(bound)+len(names))
	for k := range bound {
		next[k] = true
	}
	for _, n := range names {
		if n != "" {
			next[n] = true
		}
	}
	return next
}

func paramNamesOf(params []ast.Parameter) []string {
	out := make([]string, 0, len(params))
	for _, p := range params {
		if p.Name != nil {
			out = append(out, p.Name.Value)
		}
	}
	return out
}

// walkBlock threads bindings through a block in order: each declaration's
// initializer sees the scope before it, then the name shadows the rest.
func (s *freeScanner) walkBlock(stmts []ast.Statement, bound map[string]bool) {
	inner := extendBound(bound)
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.DeclarationStatement:
			s.walk(n.Value, inner)
			inner = extendBound(inner, n.Name.Value)
		case *ast.DestructuringStatement:
			s.walk(n.Value, inner)
			names := make([]string, 0, len(n.Names))
			for _, id := range n.Names {
				names = append(names, id.Value)
			}
			inner = extendBound(inner, names...)
		default:
			s.walk(stmt, inner)
		}
	}
}

func (s *freeScanner) walk(node ast.Node, bound map[string]bool) {
	if s.found || node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.Identifier:
		if n.Value == s.name && !bound[s.name] {
			s.found = true
		}
	case *ast.BlockStatement:
		if n != nil {
			s.walkBlock(n.Statements, bound)
		}
	case *ast.ExpressionStatement:
		s.walk(n.Expression, bound)
	case *ast.DeclarationStatement:
		s.walk(n.Value, bound)
	case *ast.DestructuringStatement:
		s.walk(n.Value, bound)
	case *ast.ReturnStatement:
		s.walk(n.Value, bound)
	case *ast.YieldStatement:
		s.walk(n.Value, bound)
	case *ast.IfStatement:
		s.walk(n.Condition, bound)
		s.walk(n.Consequence, bound)
		for _, ei := range n.ElseIfs {
			s.walk(ei.Condition, bound)
			s.walk(ei.Body, bound)
		}
		s.walk(n.Alternative, bound)
	case *ast.WhileStatement:
		s.walk(n.Condition, bound)
		s.walk(n.Body, bound)
	case *ast.ForStatement:
		s.walk(n.Iterable, bound)
		loopVars := []string{}
		if n.VarName != nil {
			loopVars = append(loopVars, n.VarName.Value)
		}
		for _, id := range n.VarNames {
			loopVars = append(loopVars, id.Value)
		}
		inner := extendBound(bound, loopVars...)
		if decl, ok := n.Init.(*ast.DeclarationStatement); ok && decl != nil {
			s.walk(decl.Value, inner)
			inner = extendBound(inner, decl.Name.Value)
		} else {
			s.walk(n.Init, inner)
		}
		s.walk(n.Condition, inner)
		s.walk(n.Update, inner)
		s.walk(n.Step, inner)
		s.walk(n.Body, inner)
	case *ast.WithStatement:
		s.walk(n.Value, bound)
		s.walk(n.Body, extendBound(bound, identName(n.Name)))
	case *ast.TryStatement:
		s.walk(n.Body, bound)
		for _, c := range n.Catches {
			s.walk(c.Body, extendBound(bound, identName(c.Name)))
		}
		s.walk(n.Finally, bound)
	case *ast.SimpleStatement:
		s.walk(n.Value, bound)
	case *ast.InitStatement:
		s.walk(n.Body, bound)
	case *ast.MatchStatement:
		s.walkMatch(n.Expr, n.Cases, bound)
	case *ast.MatchExpression:
		s.walkMatch(n.Expr, n.Cases, bound)
	case *ast.FunctionLiteral:
		s.walk(n.Body, extendBound(bound, paramNamesOf(n.Parameters)...))
	case *ast.CallExpression:
		s.walk(n.Callee, bound)
		for _, a := range n.Arguments {
			s.walk(a.Value, bound)
		}
	case *ast.SelectorExpression:
		s.walk(n.Object, bound)
	case *ast.IndexExpression:
		s.walk(n.Left, bound)
		s.walk(n.Index, bound)
	case *ast.InfixExpression:
		s.walk(n.Left, bound)
		s.walk(n.Right, bound)
	case *ast.PrefixExpression:
		s.walk(n.Right, bound)
	case *ast.PostfixExpression:
		s.walk(n.Left, bound)
	case *ast.AssignmentExpression:
		s.walk(n.Left, bound)
		s.walk(n.Value, bound)
	case *ast.InterpolatedString:
		for _, p := range n.Parts {
			s.walk(p, bound)
		}
	case *ast.ListLiteral:
		for _, el := range n.Elements {
			s.walk(el, bound)
		}
	case *ast.DictLiteral:
		for _, entry := range n.Entries {
			s.walk(entry.Key, bound)
			s.walk(entry.Value, bound)
		}
	case *ast.SetLiteral:
		for _, el := range n.Elements {
			s.walk(el, bound)
		}
	case *ast.RangeExpression:
		s.walk(n.Start, bound)
		s.walk(n.End, bound)
		s.walk(n.Step, bound)
	case *ast.TernaryExpression:
		s.walk(n.Condition, bound)
		s.walk(n.ThenExpr, bound)
		s.walk(n.ElseExpr, bound)
	case *ast.CastExpression:
		s.walk(n.Value, bound)
	case *ast.AwaitExpression:
		s.walk(n.Value, bound)
	case *ast.PipeExpression:
		s.walk(n.Left, bound)
		s.walk(n.Right, bound)
	}
}

func (s *freeScanner) walkMatch(subject ast.Expression, cases []ast.MatchCase, bound map[string]bool) {
	s.walk(subject, bound)
	for _, c := range cases {
		inner := extendBound(bound, identName(c.Name))
		s.walk(c.Guard, inner)
		s.walk(c.Body, inner)
		s.walk(c.Value, inner)
	}
}

func identName(id *ast.Identifier) string {
	if id == nil {
		return ""
	}
	return id.Value
}

func referencesName(node ast.Node, name string) bool {
	if name == "" || name == "_" {
		return false
	}
	r := &refScanner{name: name}
	r.visitNode(node)
	return r.found
}

type refScanner struct {
	name  string
	found bool
}

func (r *refScanner) visitNode(node ast.Node) {
	if r.found || node == nil {
		return
	}
	switch n := node.(type) {
	case *ast.Identifier:
		if n.Value == r.name {
			r.found = true
		}
	case *ast.BlockStatement:
		for _, s := range n.Statements {
			r.visitNode(s)
		}
	case *ast.ExpressionStatement:
		r.visitNode(n.Expression)
	case *ast.DeclarationStatement:
		r.visitNode(n.Value)
	case *ast.ReturnStatement:
		r.visitNode(n.Value)
	case *ast.IfStatement:
		r.visitNode(n.Condition)
		r.visitNode(n.Consequence)
		for _, ei := range n.ElseIfs {
			r.visitNode(ei.Condition)
			r.visitNode(ei.Body)
		}
		if n.Alternative != nil {
			r.visitNode(n.Alternative)
		}
	case *ast.WhileStatement:
		r.visitNode(n.Condition)
		r.visitNode(n.Body)
	case *ast.ForStatement:
		r.visitNode(n.Init)
		r.visitNode(n.Condition)
		r.visitNode(n.Update)
		r.visitNode(n.Iterable)
		r.visitNode(n.Step)
		r.visitNode(n.Body)
	case *ast.CallExpression:
		r.visitNode(n.Callee)
		for _, a := range n.Arguments {
			r.visitNode(a.Value)
		}
	case *ast.SelectorExpression:
		r.visitNode(n.Object)
	case *ast.IndexExpression:
		r.visitNode(n.Left)
		r.visitNode(n.Index)
	case *ast.InfixExpression:
		r.visitNode(n.Left)
		r.visitNode(n.Right)
	case *ast.PrefixExpression:
		r.visitNode(n.Right)
	case *ast.PostfixExpression:
		r.visitNode(n.Left)
	case *ast.AssignmentExpression:
		r.visitNode(n.Left)
		r.visitNode(n.Value)
	case *ast.InterpolatedString:
		for _, p := range n.Parts {
			r.visitNode(p)
		}
	case *ast.ListLiteral:
		for _, el := range n.Elements {
			r.visitNode(el)
		}
	case *ast.DictLiteral:
		for _, entry := range n.Entries {
			r.visitNode(entry.Key)
			r.visitNode(entry.Value)
		}
	case *ast.SetLiteral:
		for _, el := range n.Elements {
			r.visitNode(el)
		}
	case *ast.RangeExpression:
		r.visitNode(n.Start)
		r.visitNode(n.End)
		r.visitNode(n.Step)
	case *ast.TernaryExpression:
		r.visitNode(n.Condition)
		r.visitNode(n.ThenExpr)
		r.visitNode(n.ElseExpr)
	case *ast.CastExpression:
		r.visitNode(n.Value)
	}
}
