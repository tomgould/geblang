package check

import (
	"fmt"
	"strings"

	"geblang/internal/ast"
)

// checkCrossModuleTypes flags a module-qualified type annotation
// `m.Nope` where `m` is a resolved import but `Nope` is not in its
// export set. It walks every type-bearing position and bails (stays
// silent) on any uncertainty: an unknown alias, an unresolvable module,
// or a facade. Membership uses the full export set, so a function name
// in type position is tolerated rather than flagged.
func checkCrossModuleTypes(file string, program *ast.Program, opts Options) []Diagnostic {
	aliases := collectImportAliases(program)
	for name := range collectDeclaredNames(program) {
		delete(aliases, name)
	}
	if len(aliases) == 0 {
		return nil
	}
	cache := opts.ModuleCache
	if cache == nil {
		cache = NewModuleCache()
	}
	c := &crossTypeCollector{aliases: aliases, opts: opts, cache: cache}
	for _, stmt := range program.Statements {
		collectTypeRefsStmt(stmt, c.visit)
	}
	return diagsWithFile(c.diags, file)
}

type crossTypeCollector struct {
	aliases map[string]importAlias
	opts    Options
	cache   *ModuleCache
	diags   []Diagnostic
}

func (c *crossTypeCollector) visit(ref *ast.TypeRef) {
	if ref == nil || ref.Name == "" {
		return
	}
	dot := strings.LastIndex(ref.Name, ".")
	if dot <= 0 || dot == len(ref.Name)-1 {
		return
	}
	aliasName := ref.Name[:dot]
	member := ref.Name[dot+1:]
	alias, ok := c.aliases[aliasName]
	if !ok {
		return
	}
	// A facade re-exports a surface beyond its static exports, so its
	// qualified types cannot be validated without false positives.
	if c.moduleReExports(alias) {
		return
	}
	exports, ok := resolveExportSet(alias.canonical, alias.native, c.opts, c.cache)
	if !ok {
		return
	}
	if _, exists := exports[member]; exists {
		return
	}
	c.diags = append(c.diags, Diagnostic{
		Line:   ref.Token.Line,
		Column: ref.Token.Column,
		// Advisory, not an error: an unknown qualified type annotation is
		// not rejected by either backend at runtime (it is treated as
		// unconstrained), so per the static-analysis contract it is a
		// warning, never a hard error.
		Severity: SeverityWarning,
		Rule:     "type",
		Message:  fmt.Sprintf("%s has no exported type %s", alias.canonical, member),
	})
}

// moduleReExports reports whether a source module contains any
// `from M import N` statement, marking it a facade whose qualified-type
// surface is broader than its static export set.
func (c *crossTypeCollector) moduleReExports(alias importAlias) bool {
	if c.opts.Resolver == nil {
		return false
	}
	path, err := c.opts.Resolver.Resolve(alias.canonical)
	if err != nil {
		return false
	}
	program, _, err := c.cache.load(path)
	if err != nil || program == nil {
		return false
	}
	for _, stmt := range program.Statements {
		if _, ok := stmt.(*ast.FromImportStatement); ok {
			return true
		}
	}
	return false
}

// collectTypeRefsStmt invokes fn for every TypeRef reachable from stmt,
// recursing into nested types, blocks, and function literals.
func collectTypeRefsStmt(stmt ast.Statement, fn func(*ast.TypeRef)) {
	switch s := stmt.(type) {
	case *ast.ExportStatement:
		collectTypeRefsStmt(s.Statement, fn)
	case *ast.TypeAliasStatement:
		collectTypeRef(s.Type, fn)
	case *ast.DeclarationStatement:
		collectTypeRef(s.Type, fn)
		collectTypeRefsExpr(s.Value, fn)
	case *ast.DestructuringStatement:
		collectTypeRefsExpr(s.Value, fn)
	case *ast.ExpressionStatement:
		collectTypeRefsExpr(s.Expression, fn)
	case *ast.ReturnStatement:
		collectTypeRefsExpr(s.Value, fn)
	case *ast.YieldStatement:
		collectTypeRefsExpr(s.Value, fn)
	case *ast.SimpleStatement:
		collectTypeRefsExpr(s.Value, fn)
	case *ast.IfStatement:
		collectTypeRefsExpr(s.Condition, fn)
		collectTypeRefsBlock(s.Consequence, fn)
		for _, clause := range s.ElseIfs {
			collectTypeRefsExpr(clause.Condition, fn)
			collectTypeRefsBlock(clause.Body, fn)
		}
		collectTypeRefsBlock(s.Alternative, fn)
	case *ast.WhileStatement:
		collectTypeRefsExpr(s.Condition, fn)
		collectTypeRefsBlock(s.Body, fn)
	case *ast.ForStatement:
		collectTypeRef(s.VarType, fn)
		collectTypeRefsStmt(s.Init, fn)
		collectTypeRefsExpr(s.Condition, fn)
		collectTypeRefsStmt(s.Update, fn)
		collectTypeRefsExpr(s.Iterable, fn)
		collectTypeRefsBlock(s.Body, fn)
	case *ast.FunctionStatement:
		collectTypeRefsParams(s.Parameters, fn)
		collectTypeRef(s.ReturnType, fn)
		collectTypeRefsBlock(s.Body, fn)
	case *ast.ClassStatement:
		collectTypeRef(s.Extends, fn)
		for _, impl := range s.Implements {
			collectTypeRef(impl, fn)
		}
		for _, member := range s.Members {
			collectTypeRefsStmt(member, fn)
		}
		if s.Destructor != nil {
			collectTypeRefsStmt(s.Destructor, fn)
		}
	case *ast.InterfaceStatement:
		for _, parent := range s.Parents {
			collectTypeRef(parent, fn)
		}
		for _, sig := range s.Methods {
			if sig != nil {
				collectTypeRefsParams(sig.Parameters, fn)
				collectTypeRef(sig.ReturnType, fn)
			}
		}
		for _, def := range s.Defaults {
			collectTypeRefsStmt(def, fn)
		}
		for _, field := range s.Fields {
			collectTypeRefsStmt(field, fn)
		}
	case *ast.EnumStatement:
		for _, variant := range s.Variants {
			for _, ft := range variant.FieldTypes {
				collectTypeRef(ft, fn)
			}
		}
	case *ast.TryStatement:
		collectTypeRefsBlock(s.Body, fn)
		for _, clause := range s.Catches {
			collectTypeRef(clause.Type, fn)
			collectTypeRefsBlock(clause.Body, fn)
		}
		collectTypeRefsBlock(s.Finally, fn)
	case *ast.MatchStatement:
		collectTypeRefsExpr(s.Expr, fn)
		for _, clause := range s.Cases {
			collectTypeRef(clause.Type, fn)
			collectTypeRefsExpr(clause.Guard, fn)
			collectTypeRefsExpr(clause.Value, fn)
			collectTypeRefsBlock(clause.Body, fn)
		}
	case *ast.InitStatement:
		collectTypeRefsBlock(s.Body, fn)
	}
}

func collectTypeRefsBlock(block *ast.BlockStatement, fn func(*ast.TypeRef)) {
	if block == nil {
		return
	}
	for _, stmt := range block.Statements {
		collectTypeRefsStmt(stmt, fn)
	}
}

func collectTypeRefsParams(params []ast.Parameter, fn func(*ast.TypeRef)) {
	for _, p := range params {
		collectTypeRef(p.Type, fn)
		collectTypeRefsExpr(p.Default, fn)
	}
}

// collectTypeRef recursively visits a TypeRef and its generic arguments,
// list-alias element, and union/intersection operands.
func collectTypeRef(ref *ast.TypeRef, fn func(*ast.TypeRef)) {
	if ref == nil {
		return
	}
	if ref.Operator != "" {
		collectTypeRef(ref.Left, fn)
		collectTypeRef(ref.Right, fn)
		return
	}
	fn(ref)
	for _, arg := range ref.Arguments {
		collectTypeRef(arg, fn)
	}
}

// collectTypeRefsExpr reaches type annotations embedded in expressions:
// cast targets, function-literal signatures, and nested bodies.
func collectTypeRefsExpr(expr ast.Expression, fn func(*ast.TypeRef)) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.CastExpression:
		collectTypeRef(e.Type, fn)
		collectTypeRefsExpr(e.Value, fn)
	case *ast.CallExpression:
		for _, ta := range e.TypeArguments {
			collectTypeRef(ta, fn)
		}
		collectTypeRefsExpr(e.Callee, fn)
		for _, arg := range e.Arguments {
			collectTypeRefsExpr(arg.Value, fn)
		}
	case *ast.FunctionLiteral:
		collectTypeRefsParams(e.Parameters, fn)
		collectTypeRef(e.ReturnType, fn)
		collectTypeRefsBlock(e.Body, fn)
	case *ast.SpreadExpression:
		collectTypeRefsExpr(e.Value, fn)
	case *ast.InterpolatedString:
		for _, part := range e.Parts {
			collectTypeRefsExpr(part, fn)
		}
	case *ast.PrefixExpression:
		collectTypeRefsExpr(e.Right, fn)
	case *ast.PostfixExpression:
		collectTypeRefsExpr(e.Left, fn)
	case *ast.InfixExpression:
		collectTypeRefsExpr(e.Left, fn)
		collectTypeRefsExpr(e.Right, fn)
	case *ast.AssignmentExpression:
		collectTypeRefsExpr(e.Left, fn)
		collectTypeRefsExpr(e.Value, fn)
	case *ast.SelectorExpression:
		collectTypeRefsExpr(e.Object, fn)
	case *ast.IndexExpression:
		collectTypeRefsExpr(e.Left, fn)
		collectTypeRefsExpr(e.Index, fn)
	case *ast.ListLiteral:
		for _, element := range e.Elements {
			collectTypeRefsExpr(element, fn)
		}
	case *ast.DictLiteral:
		for _, pair := range e.Entries {
			collectTypeRefsExpr(pair.Key, fn)
			collectTypeRefsExpr(pair.Value, fn)
		}
	case *ast.SetLiteral:
		for _, element := range e.Elements {
			collectTypeRefsExpr(element, fn)
		}
	case *ast.RangeExpression:
		collectTypeRefsExpr(e.Start, fn)
		collectTypeRefsExpr(e.End, fn)
		collectTypeRefsExpr(e.Step, fn)
	case *ast.MatchExpression:
		collectTypeRefsExpr(e.Expr, fn)
		for _, clause := range e.Cases {
			collectTypeRef(clause.Type, fn)
			collectTypeRefsExpr(clause.Guard, fn)
			collectTypeRefsExpr(clause.Value, fn)
			collectTypeRefsBlock(clause.Body, fn)
		}
	case *ast.AwaitExpression:
		collectTypeRefsExpr(e.Value, fn)
	case *ast.TernaryExpression:
		collectTypeRefsExpr(e.Condition, fn)
		collectTypeRefsExpr(e.ThenExpr, fn)
		collectTypeRefsExpr(e.ElseExpr, fn)
	}
}
