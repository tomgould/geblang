package lower

import (
	"geblang/internal/ast"
	"geblang/internal/transpiler/emit"
	"geblang/internal/transpiler/types"
)

// Classes in an inheritance hierarchy are emitted with a Go interface (the
// Geblang class name) plus a concrete impl struct so a parent method's
// this.m() late-binds to a subclass override. Each impl carries a `self` field
// typed as the class interface, set at construction to the most-derived
// instance; virtual this.m() calls route through it. parent.m() stays static.

func implName(class string) string { return class + "_impl" }

// methodSig describes a method for interface emission, innermost (most-derived)
// declaration winning on override.
type methodSig struct {
	gbName string
	fn     *ast.FunctionStatement
}

// ancestry lists a class and its user-class ancestors, derived-first.
func (l *Lowerer) ancestry(gbName string) []string {
	var chain []string
	for cur := gbName; ; {
		chain = append(chain, cur)
		parent, ok := l.Module.ClassParent(cur)
		if !ok {
			break
		}
		cur = parent
	}
	return chain
}

// methodSet returns the visible methods of gbName (own + inherited), with the
// most-derived declaration of each name.
func (l *Lowerer) methodSet(gbName string) []methodSig {
	seen := map[string]bool{}
	var out []methodSig
	for _, cls := range l.ancestry(gbName) {
		decl, ok := l.Module.ClassDecl(cls)
		if !ok {
			continue
		}
		for _, m := range decl.Members {
			fn, ok := m.(*ast.FunctionStatement)
			if !ok || fn.Static || fn.Name.Value == cls {
				continue
			}
			if seen[fn.Name.Value] {
				continue
			}
			seen[fn.Name.Value] = true
			out = append(out, methodSig{gbName: fn.Name.Value, fn: fn})
		}
	}
	return out
}

// fieldSet returns the visible fields of gbName (own + inherited), most-derived
// declaration winning.
func (l *Lowerer) fieldSet(gbName string) []*ast.DeclarationStatement {
	seen := map[string]bool{}
	var out []*ast.DeclarationStatement
	for _, cls := range l.ancestry(gbName) {
		decl, ok := l.Module.ClassDecl(cls)
		if !ok {
			continue
		}
		for _, m := range decl.Members {
			f, ok := m.(*ast.DeclarationStatement)
			if !ok || seen[f.Name.Value] {
				continue
			}
			seen[f.Name.Value] = true
			out = append(out, f)
		}
	}
	return out
}

func (l *Lowerer) emitHierarchyClass(s *ast.ClassStatement) {
	gbName := s.Name.Value
	iface := emit.MangleIdent(gbName)
	impl := implName(iface)
	l.Module.RegisterClass(gbName)

	parentGb := ""
	if p, ok := l.Module.ClassParent(gbName); ok {
		parentGb = p
		l.Module.RegisterClass(p)
	}

	var ownFields []*ast.DeclarationStatement
	var ownMethods []*ast.FunctionStatement
	var staticMethods []*ast.FunctionStatement
	var constructor *ast.FunctionStatement
	for _, m := range s.Members {
		switch n := m.(type) {
		case *ast.DeclarationStatement:
			ownFields = append(ownFields, n)
		case *ast.FunctionStatement:
			if n.Name.Value == gbName {
				constructor = n
			} else if n.Static {
				staticMethods = append(staticMethods, n)
			} else {
				ownMethods = append(ownMethods, n)
			}
		}
	}

	allMethods := l.methodSet(gbName)
	allFields := l.fieldSet(gbName)

	l.emitClassInterface(iface, allMethods, allFields)
	l.emitImplStruct(iface, impl, parentGb, ownFields)
	l.emitFieldAccessors(impl, ownFields)

	if constructor != nil {
		l.Module.RegisterCalleeParams("New"+iface, paramNames(constructor.Parameters))
	}
	for _, m := range staticMethods {
		l.Module.RegisterCalleeParams(iface+"_"+emit.MangleIdent(m.Name.Value), paramNames(m.Parameters))
	}

	l.emitHierarchyConstructor(iface, impl, gbName, parentGb, constructor, allFields)

	for _, f := range allFields {
		l.Module.RegisterClassField(gbName, f.Name.Value, f.Name.Value)
	}
	for _, m := range ownMethods {
		l.Module.RegisterClassMethod(gbName, m.Name.Value)
		l.emitVirtualMethod(iface, impl, gbName, parentGb, m)
	}
	for _, m := range staticMethods {
		l.emitHierarchyStaticMethod(iface, m)
	}
	l.emitGbStringWrapper(impl, "this.self", ownMethods)
	l.emitClassDecoratorsTable(iface, s.Decorators)
	l.emitClassMethodsTable(iface, ownMethods)
	l.emitClassFieldsTable(iface, ownFields)
	l.emitMethodDecoratorsTable(iface, ownMethods)
	l.emitFieldDecoratorsTable(iface, s.FieldDecorators)
}

func (l *Lowerer) emitClassInterface(iface string, methods []methodSig, fields []*ast.DeclarationStatement) {
	decls := l.Module.TopDecls()
	decls.WriteString("type " + iface + " interface {")
	decls.WriteLine("")
	decls.Indent()
	for _, m := range methods {
		decls.WriteString(emit.MangleIdent(m.gbName))
		decls.WriteString("(")
		for i, p := range m.fn.Parameters {
			if i > 0 {
				decls.WriteString(", ")
			}
			goTy := types.ToGo(l.resolveTypeRef(p.Type), l.Module.IntMode)
			l.Module.AddTypeImports(goTy)
			decls.WriteString(goTy.Source)
		}
		decls.WriteString(")")
		if m.fn.ReturnType != nil {
			goRet := types.ToGo(l.resolveTypeRef(m.fn.ReturnType), l.Module.IntMode)
			l.Module.AddTypeImports(goRet)
			decls.WriteString(" " + goRet.Source)
		}
		decls.WriteLine("")
	}
	for _, f := range fields {
		goTy := types.ToGo(l.resolveTypeRef(f.Type), l.Module.IntMode)
		l.Module.AddTypeImports(goTy)
		decls.WriteLine(fieldGetter(f.Name.Value) + "() " + goTy.Source)
		decls.WriteLine(fieldSetter(f.Name.Value) + "(" + goTy.Source + ")")
	}
	decls.Dedent()
	decls.WriteLine("}")
	decls.Newline()
}

func (l *Lowerer) emitImplStruct(iface, impl, parentGb string, ownFields []*ast.DeclarationStatement) {
	decls := l.Module.TopDecls()
	decls.WriteString("type " + impl + " struct {")
	decls.WriteLine("")
	decls.Indent()
	if parentGb != "" {
		decls.WriteLine("*" + implName(emit.MangleIdent(parentGb)))
	}
	// self points at the most-derived instance for virtual this.m() dispatch.
	decls.WriteLine("self " + iface)
	for _, f := range ownFields {
		goTy := types.ToGo(l.resolveTypeRef(f.Type), l.Module.IntMode)
		l.Module.AddTypeImports(goTy)
		decls.WriteLine(emit.MangleIdent(f.Name.Value) + " " + goTy.Source)
	}
	decls.Dedent()
	decls.WriteLine("}")
	decls.Newline()
}

func (l *Lowerer) emitFieldAccessors(impl string, ownFields []*ast.DeclarationStatement) {
	decls := l.Module.TopDecls()
	for _, f := range ownFields {
		goTy := types.ToGo(l.resolveTypeRef(f.Type), l.Module.IntMode)
		name := emit.MangleIdent(f.Name.Value)
		decls.WriteLine("func (this *" + impl + ") " + fieldGetter(f.Name.Value) + "() " + goTy.Source + " { return this." + name + " }")
		decls.WriteLine("func (this *" + impl + ") " + fieldSetter(f.Name.Value) + "(v " + goTy.Source + ") { this." + name + " = v }")
		decls.Newline()
	}
}

func (l *Lowerer) emitHierarchyConstructor(iface, impl, gbName, parentGb string, ctor *ast.FunctionStatement, allFields []*ast.DeclarationStatement) {
	decls := l.Module.TopDecls()
	body := emit.NewWriter()
	savedW, savedParent, savedCtor, savedIface, savedGb := l.w, l.parentClass, l.inConstructor, l.currentClassIface, l.currentClassGb
	l.w, l.parentClass, l.inConstructor, l.currentClassIface, l.currentClassGb = body, emit.MangleIdent(parentGb), true, iface, gbName
	l.withChildScope(func() {
		l.scope.Define(&types.Binding{Name: "this", Type: &types.Type{Kind: types.KindClass, Name: iface}, Mutable: true})
		body.WriteLine("this := &" + impl + "{}")
		body.WriteLine("this.self = this")
		// A parent with no explicit parent(...) call still needs storage.
		if parentGb != "" && !ctorCallsParent(ctor) {
			body.WriteLine("this." + implName(emit.MangleIdent(parentGb)) + " = " + parentDefaultBuild(emit.MangleIdent(parentGb)))
		}
		for _, f := range allFields {
			if f.Value == nil {
				continue
			}
			body.WriteString("this." + fieldSetter(f.Name.Value) + "(")
			l.withExpectedType(l.resolveTypeRef(f.Type), func() { l.lowerExpression(f.Value) })
			body.WriteLine(")")
		}
		if ctor != nil {
			for _, p := range ctor.Parameters {
				l.scope.Define(&types.Binding{Name: p.Name.Value, Type: l.resolveTypeRef(p.Type), Mutable: true, IsParam: true})
			}
			if ctor.Body != nil {
				l.lowerBlock(ctor.Body.Statements)
			}
		}
		// Wire ancestor self pointers to the concrete instance once the embedded
		// impls exist (after the parent(...) call has built them).
		l.emitAncestorSelfWiring(body, gbName)
		body.WriteLine("return this")
	})
	l.w, l.parentClass, l.inConstructor, l.currentClassIface, l.currentClassGb = savedW, savedParent, savedCtor, savedIface, savedGb

	decls.WriteString("func New" + iface + "(")
	if ctor != nil {
		for i, p := range ctor.Parameters {
			if i > 0 {
				decls.WriteString(", ")
			}
			goTy := types.ToGo(l.resolveTypeRef(p.Type), l.Module.IntMode)
			l.Module.AddTypeImports(goTy)
			decls.WriteString(emit.MangleIdent(p.Name.Value) + " " + goTy.Source)
		}
	}
	decls.WriteString(") " + iface + " {")
	decls.WriteLine("")
	decls.Indent()
	decls.WriteString(body.String())
	decls.Dedent()
	decls.WriteLine("}")
	decls.Newline()
}

// emitAncestorSelfWiring points each embedded ancestor impl's self field at the
// concrete instance so a base method reaches the most-derived override.
func (l *Lowerer) emitAncestorSelfWiring(body *emit.Writer, gbName string) {
	path := "this"
	cur := gbName
	for {
		parent, ok := l.Module.ClassParent(cur)
		if !ok {
			break
		}
		path += "." + implName(emit.MangleIdent(parent))
		body.WriteLine(path + ".self = this")
		cur = parent
	}
}

func (l *Lowerer) isVisibleMethod(gbName, methodName string) bool {
	for _, ms := range l.methodSet(gbName) {
		if ms.gbName == methodName {
			return true
		}
	}
	return false
}

func (l *Lowerer) emitVirtualMethod(iface, impl, gbName, parentGb string, m *ast.FunctionStatement) {
	decls := l.Module.TopDecls()
	body := emit.NewWriter()
	savedW, savedParent := l.w, l.parentClass
	l.w, l.parentClass = body, emit.MangleIdent(parentGb)
	methodRetGo := ""
	if m.ReturnType != nil {
		methodRetGo = types.ToGo(l.resolveTypeRef(m.ReturnType), l.Module.IntMode).Source
	}
	savedClass, savedGb := l.currentClassIface, l.currentClassGb
	l.currentClassIface, l.currentClassGb = iface, gbName
	l.withReturnGo(methodRetGo, func() {
		l.withChildScope(func() {
			l.scope.Define(&types.Binding{Name: "this", Type: &types.Type{Kind: types.KindClass, Name: iface}, Mutable: true})
			for _, p := range m.Parameters {
				l.scope.Define(&types.Binding{Name: p.Name.Value, Type: l.resolveTypeRef(p.Type), Mutable: true, IsParam: true})
			}
			if m.Body != nil {
				l.lowerBlock(m.Body.Statements)
			}
		})
		if methodRetGo != "" && bodyEndsWithTry(m.Body) {
			l.w.WriteLine("return " + zeroValue(methodRetGo))
		}
	})
	l.currentClassIface, l.currentClassGb = savedClass, savedGb
	l.w, l.parentClass = savedW, savedParent

	decls.WriteString("func (this *" + impl + ") " + emit.MangleIdent(m.Name.Value) + "(")
	for i, p := range m.Parameters {
		if i > 0 {
			decls.WriteString(", ")
		}
		goTy := types.ToGo(l.resolveTypeRef(p.Type), l.Module.IntMode)
		l.Module.AddTypeImports(goTy)
		decls.WriteString(emit.MangleIdent(p.Name.Value) + " " + goTy.Source)
	}
	decls.WriteString(") ")
	if m.ReturnType != nil {
		goRet := types.ToGo(l.resolveTypeRef(m.ReturnType), l.Module.IntMode)
		l.Module.AddTypeImports(goRet)
		decls.WriteString(goRet.Source + " ")
	}
	decls.WriteLine("{")
	decls.Indent()
	decls.WriteString(body.String())
	decls.Dedent()
	decls.WriteLine("}")
	decls.Newline()
}

func (l *Lowerer) emitHierarchyStaticMethod(iface string, m *ast.FunctionStatement) {
	l.emitStaticMethod(iface, m)
}

func ctorCallsParent(ctor *ast.FunctionStatement) bool {
	if ctor == nil || ctor.Body == nil {
		return false
	}
	for _, st := range ctor.Body.Statements {
		es, ok := st.(*ast.ExpressionStatement)
		if !ok {
			continue
		}
		if call, ok := es.Expression.(*ast.CallExpression); ok {
			if id, ok := call.Callee.(*ast.Identifier); ok && id.Value == "parent" {
				return true
			}
		}
	}
	return false
}

func parentDefaultBuild(parentIface string) string {
	return "New" + parentIface + "().(*" + implName(parentIface) + ")"
}

func fieldGetter(name string) string { return "GbGet_" + emit.MangleIdent(name) }
func fieldSetter(name string) string { return "GbSet_" + emit.MangleIdent(name) }
