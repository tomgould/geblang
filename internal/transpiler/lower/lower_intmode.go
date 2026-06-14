package lower

import (
	"geblang/internal/ast"
	"geblang/internal/transpiler/types"
)

// Safe-mode (IntModeBigInt) lowering: int values are transpilert.Int, which
// promotes to *big.Int on overflow. transpilert provides AddInt/SubInt/MulInt/
// NegInt only; division, modulo, comparisons, and printing have no helper yet,
// so those constructs hard-fail with a diagnostic rather than emit wrong code.

func (l *Lowerer) bothIntOperands(left, right ast.Expression) bool {
	lt := l.inferExpressionType(left)
	rt := l.inferExpressionType(right)
	return lt != nil && lt.Kind == types.KindInt && rt != nil && rt.Kind == types.KindInt
}

func (l *Lowerer) lowerSafeIntInfix(e *ast.InfixExpression) bool {
	fn := map[string]string{"+": "AddInt", "-": "SubInt", "*": "MulInt"}[e.Operator]
	if fn == "" {
		l.errAt(e.Token.Line, e.Token.Column,
			"safe int mode does not yet support the "+e.Operator+" operator",
			"transpilert.Int has no helper for this; division, modulo, and comparisons are deferred")
		l.w.WriteString("transpilert.Int{}")
		return true
	}
	l.Module.AddImport(types.OrderedDictImport)
	l.w.WriteString("transpilert.")
	l.w.WriteString(fn)
	l.w.WriteString("(")
	l.lowerExpression(e.Left)
	l.w.WriteString(", ")
	l.lowerExpression(e.Right)
	l.w.WriteString(")")
	return true
}
