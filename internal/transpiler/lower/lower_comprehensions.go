package lower

import (
	"geblang/internal/ast"
	"geblang/internal/transpiler/emit"
	"geblang/internal/transpiler/types"
)

// withComprehensionScope binds each clause's loop variables (with their inferred
// element types) in a child scope so the body type can be inferred. It does not
// emit code; lowering uses emitComprehensionClauses.
func (l *Lowerer) withComprehensionScope(clauses []ast.ComprehensionClause, fn func()) {
	l.withChildScope(func() {
		for _, c := range clauses {
			cf, ok := c.(*ast.ComprehensionFor)
			if !ok {
				continue
			}
			l.bindComprehensionVars(cf)
		}
		fn()
	})
}

func (l *Lowerer) bindComprehensionVars(cf *ast.ComprehensionFor) {
	names := comprehensionNames(cf)
	keyTy, valTy, elemTy := l.comprehensionIterTypes(cf)
	if len(names) == 2 {
		l.scope.Define(&types.Binding{Name: names[0].Value, Type: keyTy})
		l.scope.Define(&types.Binding{Name: names[1].Value, Type: valTy})
		return
	}
	if len(names) == 1 {
		l.scope.Define(&types.Binding{Name: names[0].Value, Type: elemTy})
	}
}

func comprehensionNames(cf *ast.ComprehensionFor) []*ast.Identifier {
	if len(cf.VarNames) > 0 {
		return cf.VarNames
	}
	if cf.VarName != nil {
		return []*ast.Identifier{cf.VarName}
	}
	return nil
}

// comprehensionIterTypes returns (keyType, valType, elemType) for a for-clause:
// key/val for a dict.items() source, elem for a list/set/generator source.
func (l *Lowerer) comprehensionIterTypes(cf *ast.ComprehensionFor) (*types.Type, *types.Type, *types.Type) {
	if recv, ok := l.dictItemsReceiver(cf.Iterable); ok {
		if rt := l.inferExpressionType(recv); rt != nil && rt.Kind == types.KindDict {
			return rt.Key, rt.Value, nil
		}
	}
	if it := l.inferExpressionType(cf.Iterable); it != nil {
		switch it.Kind {
		case types.KindList, types.KindSet, types.KindGenerator:
			return nil, nil, it.Elem
		}
	}
	return nil, nil, types.Any()
}

func (l *Lowerer) lowerListComprehension(e *ast.ListComprehension) {
	ty := l.inferExpressionType(e)
	elemGo := types.ToGo(types.Any(), l.Module.IntMode)
	if ty != nil && ty.Kind == types.KindList && ty.Elem != nil {
		elemGo = types.ToGo(ty.Elem, l.Module.IntMode)
	}
	l.Module.AddTypeImports(elemGo)
	acc := l.nextTmp()
	l.w.WriteString("func() []" + elemGo.Source + " { " + acc + " := []" + elemGo.Source + "{}; ")
	l.withChildScope(func() {
		l.emitComprehensionClauses(e.Clauses, 0, func() {
			l.w.WriteString(acc + " = append(" + acc + ", ")
			l.lowerExpression(e.Body)
			l.w.WriteString("); ")
		})
	})
	l.w.WriteString("return " + acc + " }()")
}

func (l *Lowerer) lowerDictComprehension(e *ast.DictComprehension) {
	ty := l.inferExpressionType(e)
	keyGo := types.ToGo(&types.Type{Kind: types.KindString}, l.Module.IntMode)
	valGo := types.ToGo(types.Any(), l.Module.IntMode)
	if ty != nil && ty.Kind == types.KindDict {
		keyGo = types.ToGo(ty.Key, l.Module.IntMode)
		valGo = types.ToGo(ty.Value, l.Module.IntMode)
	}
	l.Module.AddImport(types.OrderedDictImport)
	l.Module.AddTypeImports(keyGo)
	l.Module.AddTypeImports(valGo)
	acc := l.nextTmp()
	dictType := "*transpilert.OrderedDict[" + keyGo.Source + ", " + valGo.Source + "]"
	l.w.WriteString("func() " + dictType + " { " + acc + " := transpilert.NewOrderedDict[" + keyGo.Source + ", " + valGo.Source + "](); ")
	l.withChildScope(func() {
		l.emitComprehensionClauses(e.Clauses, 0, func() {
			l.w.WriteString(acc + ".Set(")
			l.lowerExpression(e.KeyBody)
			l.w.WriteString(", ")
			l.lowerExpression(e.ValueBody)
			l.w.WriteString("); ")
		})
	})
	l.w.WriteString("return " + acc + " }()")
}

// emitComprehensionClauses recurses over clauses: a for-clause emits a Go range
// loop (binding its vars), an if-clause an inline guard; body runs innermost.
func (l *Lowerer) emitComprehensionClauses(clauses []ast.ComprehensionClause, idx int, body func()) {
	if idx >= len(clauses) {
		body()
		return
	}
	switch c := clauses[idx].(type) {
	case *ast.ComprehensionIf:
		l.w.WriteString("if ")
		l.lowerExpression(c.Filter)
		l.w.WriteString(" { ")
		l.emitComprehensionClauses(clauses, idx+1, body)
		l.w.WriteString(" }; ")
	case *ast.ComprehensionFor:
		l.emitComprehensionFor(c, func() {
			l.emitComprehensionClauses(clauses, idx+1, body)
		})
	}
}

func (l *Lowerer) emitComprehensionFor(cf *ast.ComprehensionFor, inner func()) {
	names := comprehensionNames(cf)
	keyTy, valTy, elemTy := l.comprehensionIterTypes(cf)
	l.withChildScope(func() {
		if recv, ok := l.dictItemsReceiver(cf.Iterable); ok && len(names) == 2 {
			l.scope.Define(&types.Binding{Name: names[0].Value, Type: keyTy})
			l.scope.Define(&types.Binding{Name: names[1].Value, Type: valTy})
			k := emit.MangleIdent(names[0].Value)
			v := emit.MangleIdent(names[1].Value)
			l.w.WriteString("for _, " + k + " := range ")
			l.lowerExpression(recv)
			l.w.WriteString(".Keys() { ")
			l.w.WriteString(v + ", _ := ")
			l.lowerExpression(recv)
			l.w.WriteString(".Get(" + k + "); _ = " + v + "; ")
			inner()
			l.w.WriteString(" }; ")
			return
		}
		name := "_"
		if len(names) == 1 {
			name = emit.MangleIdent(names[0].Value)
			l.scope.Define(&types.Binding{Name: names[0].Value, Type: elemTy})
		}
		isGen := false
		if it := l.inferExpressionType(cf.Iterable); it != nil && it.Kind == types.KindGenerator {
			isGen = true
		}
		if isGen {
			l.w.WriteString("for " + name + " := range ")
		} else {
			l.w.WriteString("for _, " + name + " := range ")
		}
		l.lowerExpression(cf.Iterable)
		l.w.WriteString(" { ")
		inner()
		l.w.WriteString(" }; ")
	})
}
