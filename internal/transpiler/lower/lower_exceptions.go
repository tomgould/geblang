package lower

import (
	"strconv"

	"geblang/internal/ast"
	"geblang/internal/transpiler/emit"
	"geblang/internal/transpiler/types"
)

// emitErrorClass lowers a class transitively extending Error to a constructor
// returning *transpilert.Error, mirroring the interpreter's collapse of error
// subclasses into a single Error value carrying class + message + parent chain.
func (l *Lowerer) emitErrorClass(s *ast.ClassStatement) {
	className := emit.MangleIdent(s.Name.Value)
	l.Module.AddImport(types.OrderedDictImport)

	var constructor *ast.FunctionStatement
	for _, m := range s.Members {
		if fn, ok := m.(*ast.FunctionStatement); ok && fn.Name.Value == s.Name.Value {
			constructor = fn
			break
		}
	}

	params := []ast.Parameter(nil)
	if constructor != nil {
		params = constructor.Parameters
		l.Module.RegisterCalleeParams("New"+className, paramNames(params))
	}

	msgExpr := errorMessageExpr(constructor, params)

	decls := l.Module.TopDecls()
	decls.WriteString("func New" + className + "(")
	for i, p := range params {
		if i > 0 {
			decls.WriteString(", ")
		}
		decls.WriteString(emit.MangleIdent(p.Name.Value))
		decls.WriteString(" ")
		goTy := types.ToGo(l.resolveTypeRef(p.Type), l.Module.IntMode)
		l.Module.AddTypeImports(goTy)
		decls.WriteString(goTy.Source)
	}
	decls.WriteLine(") *transpilert.Error {")
	decls.Indent()
	decls.WriteString("return &transpilert.Error{Class: ")
	decls.WriteString(strconv.Quote(s.Name.Value))
	decls.WriteString(", Message: ")
	if msgExpr != nil {
		saved := l.w
		l.w = decls
		l.withChildScope(func() {
			for _, p := range params {
				l.scope.Define(&types.Binding{Name: p.Name.Value, Type: l.resolveTypeRef(p.Type), Mutable: true, IsParam: true})
			}
			l.lowerExpression(msgExpr)
		})
		l.w = saved
	} else {
		decls.WriteString(`""`)
	}
	decls.WriteString(", Parents: ")
	emitStringSlice(decls, l.Module.ErrorParentChain(s.Name.Value))
	decls.WriteLine("}")
	decls.Dedent()
	decls.WriteLine("}")
	decls.Newline()
}

// errorMessageExpr finds the message argument: the parent(...) call inside the
// constructor, else the constructor's first string parameter.
func errorMessageExpr(ctor *ast.FunctionStatement, params []ast.Parameter) ast.Expression {
	if ctor != nil && ctor.Body != nil {
		if expr := parentCallArg(ctor.Body.Statements); expr != nil {
			return expr
		}
	}
	for _, p := range params {
		if p.Type != nil && p.Type.Name == "string" {
			return &ast.Identifier{Value: p.Name.Value}
		}
	}
	return nil
}

func parentCallArg(stmts []ast.Statement) ast.Expression {
	for _, st := range stmts {
		es, ok := st.(*ast.ExpressionStatement)
		if !ok {
			continue
		}
		call, ok := es.Expression.(*ast.CallExpression)
		if !ok {
			continue
		}
		if id, ok := call.Callee.(*ast.Identifier); ok && id.Value == "parent" && len(call.Arguments) > 0 {
			return call.Arguments[0].Value
		}
	}
	return nil
}

// isBuiltinErrorConstructor matches a direct call to the Error base or an
// engine error class that the user did not redeclare as a class.
func (l *Lowerer) isBuiltinErrorConstructor(name string) bool {
	if l.Module.IsClass(name) {
		return false
	}
	if name == "Error" {
		return true
	}
	_, ok := builtinErrorClasses[name]
	return ok
}

func (l *Lowerer) lowerBuiltinErrorConstructor(name string, e *ast.CallExpression) {
	l.Module.AddImport(types.OrderedDictImport)
	l.w.WriteString("&transpilert.Error{Class: ")
	l.w.WriteString(strconv.Quote(name))
	l.w.WriteString(", Message: ")
	if len(e.Arguments) > 0 {
		l.lowerExpression(e.Arguments[0].Value)
	} else {
		l.w.WriteString(`""`)
	}
	l.w.WriteString(", Parents: ")
	emitStringSlice(l.w, l.Module.ErrorParentChain(name))
	l.w.WriteString("}")
}

func emitStringSlice(w *emit.Writer, items []string) {
	w.WriteString("[]string{")
	for i, s := range items {
		if i > 0 {
			w.WriteString(", ")
		}
		w.WriteString(strconv.Quote(s))
	}
	w.WriteString("}")
}
