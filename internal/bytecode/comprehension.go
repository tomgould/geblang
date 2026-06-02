package bytecode

import (
	"geblang/internal/ast"
	"geblang/internal/token"
)

const comprehensionAccName = "__compAcc"

// desugarListComprehension rewrites `[body for x in xs (if cond)*]` as an
// IIFE that accumulates results via in-place list.append. The evaluator
// handles ListComprehension directly; the bytecode compiler uses this
// lowering so parity falls out for free without adding new opcodes.
func desugarListComprehension(c *ast.ListComprehension) ast.Expression {
	tok := c.Token
	accIdent := &ast.Identifier{Token: tok, Value: comprehensionAccName}
	bodyStmt := wrapInExpressionStatement(tok, &ast.CallExpression{
		Token: tok,
		Callee: &ast.SelectorExpression{
			Token:  tok,
			Object: accIdent,
			Name:   &ast.Identifier{Token: tok, Value: "append"},
		},
		Arguments: []ast.CallArgument{{Value: c.Body}},
	})
	loopBody := wrapClauses(c.Clauses, bodyStmt)
	return buildComprehensionIIFE(tok, &ast.ListLiteral{Token: tok}, loopBody, accIdent)
}

// desugarSetComprehension rewrites `{body for x in xs (if cond)*}` as an
// IIFE that accumulates results via list.append, then casts to set at the
// end (relying on `list as set` dedup semantics).
func desugarSetComprehension(c *ast.SetComprehension) ast.Expression {
	tok := c.Token
	accIdent := &ast.Identifier{Token: tok, Value: comprehensionAccName}
	bodyStmt := wrapInExpressionStatement(tok, &ast.CallExpression{
		Token: tok,
		Callee: &ast.SelectorExpression{
			Token:  tok,
			Object: accIdent,
			Name:   &ast.Identifier{Token: tok, Value: "append"},
		},
		Arguments: []ast.CallArgument{{Value: c.Body}},
	})
	loopBody := wrapClauses(c.Clauses, bodyStmt)
	cast := &ast.CastExpression{
		Token: tok,
		Value: accIdent,
		Type:  &ast.TypeRef{Token: tok, Name: "set"},
	}
	return buildComprehensionIIFE(tok, &ast.ListLiteral{Token: tok}, loopBody, cast)
}

// desugarDictComprehension rewrites `{k: v for x in xs (if cond)*}` as an
// IIFE that accumulates via dict.set (in-place since 1.5.2).
func desugarDictComprehension(c *ast.DictComprehension) ast.Expression {
	tok := c.Token
	accIdent := &ast.Identifier{Token: tok, Value: comprehensionAccName}
	bodyStmt := wrapInExpressionStatement(tok, &ast.CallExpression{
		Token: tok,
		Callee: &ast.SelectorExpression{
			Token:  tok,
			Object: accIdent,
			Name:   &ast.Identifier{Token: tok, Value: "set"},
		},
		Arguments: []ast.CallArgument{{Value: c.KeyBody}, {Value: c.ValueBody}},
	})
	loopBody := wrapClauses(c.Clauses, bodyStmt)
	return buildComprehensionIIFE(tok, &ast.DictLiteral{Token: tok}, loopBody, accIdent)
}

func wrapClauses(clauses []ast.ComprehensionClause, inner ast.Statement) ast.Statement {
	for i := len(clauses) - 1; i >= 0; i-- {
		inner = wrapClause(clauses[i], inner)
	}
	return inner
}

func wrapClause(clause ast.ComprehensionClause, inner ast.Statement) ast.Statement {
	switch c := clause.(type) {
	case *ast.ComprehensionFor:
		return &ast.ForStatement{
			Token:    c.Token,
			VarType:  c.VarType,
			VarName:  c.VarName,
			VarNames: c.VarNames,
			Iterable: c.Iterable,
			Body:     blockOf(inner),
		}
	case *ast.ComprehensionIf:
		return &ast.IfStatement{
			Token:       c.Token,
			Condition:   c.Filter,
			Consequence: blockOf(inner),
		}
	}
	return inner
}

func blockOf(stmt ast.Statement) *ast.BlockStatement {
	if b, ok := stmt.(*ast.BlockStatement); ok {
		return b
	}
	return &ast.BlockStatement{Statements: []ast.Statement{stmt}}
}

func wrapInExpressionStatement(tok token.Token, expr ast.Expression) ast.Statement {
	return &ast.ExpressionStatement{Token: tok, Expression: expr}
}

func buildComprehensionIIFE(tok token.Token, accInit ast.Expression, loopBody ast.Statement, result ast.Expression) ast.Expression {
	accDecl := &ast.DeclarationStatement{
		Token: tok,
		Kind:  "let",
		Name:  &ast.Identifier{Token: tok, Value: comprehensionAccName},
		Value: accInit,
	}
	ret := &ast.ReturnStatement{Token: tok, Value: result}
	body := &ast.BlockStatement{
		Token:      tok,
		Statements: []ast.Statement{accDecl, loopBody, ret},
	}
	iife := &ast.FunctionLiteral{Token: tok, Body: body}
	return &ast.CallExpression{Token: tok, Callee: iife}
}
