package lower

import (
	"geblang/internal/ast"
	"geblang/internal/transpiler/emit"
	"geblang/internal/transpiler/types"
)

// nullableValuePtr reports whether a slot of type t holds a nullable value-type
// (int/float/bool/string), which lowers to a Go pointer so nil represents null.
func nullableValuePtr(t *types.Type) bool {
	return t != nil && t.Nullable && (nullableValueKind(t.Kind) || (t.Kind == types.KindEnum && t.EnumScalar))
}

func nullableValueKind(k types.Kind) bool {
	switch k {
	case types.KindInt, types.KindFloat, types.KindBool, types.KindString:
		return true
	}
	return false
}

// lowerIntoNullable lowers value for assignment/initialisation into a slot of
// type target, taking the address of a concrete value when the slot is a
// nullable value-type pointer.
func (l *Lowerer) lowerIntoNullable(target *types.Type, value ast.Expression) {
	if isNullLiteral(value) {
		l.lowerTypedNil(target)
		return
	}
	if nullableValuePtr(target) {
		valTy := l.inferExpressionType(value)
		if valTy != nil && valTy.Nullable {
			// Already a pointer (another nullable slot); pass through.
			l.lowerExpression(value)
			return
		}
		l.Module.RequireHelper("gbPtrOf")
		l.w.WriteString("gbPtrOf(")
		l.lowerExpressionAsElem(target, value)
		l.w.WriteString(")")
		return
	}
	l.lowerExpression(value)
}

// lowerExpressionAsElem lowers value coerced to the slot's underlying element
// Go type (e.g. int literals to int64) so the pointer has the right type.
func (l *Lowerer) lowerExpressionAsElem(target *types.Type, value ast.Expression) {
	if target.Kind == types.KindInt && l.Module.IntMode != types.IntModeBigInt {
		l.w.WriteString("int64(")
		l.lowerExpression(value)
		l.w.WriteString(")")
		return
	}
	l.lowerExpression(value)
}

// lowerOptionalCall lowers obj?.method(args): a guarded nil-check yielding nil
// when the receiver is null, else the method result.
func (l *Lowerer) lowerOptionalCall(sel *ast.SelectorExpression, e *ast.CallExpression) {
	l.w.WriteString("func() any { __r := ")
	l.lowerExpression(sel.Object)
	l.w.WriteString("; if __r == nil { return nil }; return __r.")
	l.w.WriteString(emit.MangleIdent(sel.Name.Value))
	if !l.requirePositionalArgs(e.Arguments, sel.Token, "an optional method call") {
		l.w.WriteString("() }()")
		return
	}
	l.emitPositionalArgs(e.Arguments)
	l.w.WriteString(" }()")
}

// lowerDisplay lowers expr for printing through transpilert.Show, which renders
// every value (scalars, nullables, collections, instances) in the interpreter's
// top-level format: bare strings, "null" for nil, floats via %g, decimals to 10
// places, nested container strings quoted.
func (l *Lowerer) lowerDisplay(expr ast.Expression) {
	l.Module.AddImport(types.OrderedDictImport)
	l.w.WriteString("transpilert.Show(")
	l.lowerExpression(expr)
	l.w.WriteString(")")
}

// lowerTypedNil emits a typed nil for a nullable slot so printing and Go type
// inference behave; bare nil is fine where the target type is already known.
func (l *Lowerer) lowerTypedNil(target *types.Type) {
	if target == nil || target.Kind == types.KindUnknown || target.Kind == types.KindAny {
		l.w.WriteString("nil")
		return
	}
	goTy := types.ToGo(target, l.Module.IntMode)
	l.Module.AddTypeImports(goTy)
	l.w.WriteString("(")
	l.w.WriteString(goTy.Source)
	l.w.WriteString(")(nil)")
}
