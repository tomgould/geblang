package lower

import "geblang/internal/ast"

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
