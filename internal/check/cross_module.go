package check

import (
	"fmt"
	"os"
	"sync"
	"time"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// ModuleCache memoises parsed module sources so successive
// cross-module checks don't re-parse the same file on every keystroke.
// Entries are invalidated by mtime; the cache is safe for concurrent
// reads via the embedded sync.Mutex.
type ModuleCache struct {
	mu      sync.Mutex
	entries map[string]moduleCacheEntry
}

type moduleCacheEntry struct {
	mtime   time.Time
	program *ast.Program
	exports map[string]struct{}
}

// NewModuleCache returns an empty cache ready to use.
func NewModuleCache() *ModuleCache {
	return &ModuleCache{entries: map[string]moduleCacheEntry{}}
}

func (c *ModuleCache) load(path string) (*ast.Program, map[string]struct{}, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	if entry, ok := c.entries[path]; ok && entry.mtime.Equal(info.ModTime()) {
		c.mu.Unlock()
		return entry.program, entry.exports, nil
	}
	c.mu.Unlock()
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	p := parser.New(lexer.New(string(source)))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return program, collectExports(program), nil
	}
	exports := collectExports(program)
	c.mu.Lock()
	c.entries[path] = moduleCacheEntry{mtime: info.ModTime(), program: program, exports: exports}
	c.mu.Unlock()
	return program, exports, nil
}

// collectExports returns the set of names a module exposes to importers:
// every `export` declaration, every top-level `class` / `interface` /
// `enum`, and every top-level `func` whose name does not begin with `_`.
func collectExports(program *ast.Program) map[string]struct{} {
	exports := map[string]struct{}{}
	if program == nil {
		return exports
	}
	add := func(name string) {
		if name == "" || name[0] == '_' {
			return
		}
		exports[name] = struct{}{}
	}
	for _, stmt := range program.Statements {
		switch s := stmt.(type) {
		case *ast.ExportStatement:
			add(exportedName(s.Statement))
		case *ast.FunctionStatement:
			if s.Name != nil {
				add(s.Name.Value)
			}
		case *ast.ClassStatement:
			if s.Name != nil {
				add(s.Name.Value)
			}
		case *ast.InterfaceStatement:
			if s.Name != nil {
				add(s.Name.Value)
			}
		case *ast.EnumStatement:
			if s.Name != nil {
				add(s.Name.Value)
			}
		case *ast.DeclarationStatement:
			if s.Name != nil {
				add(s.Name.Value)
			}
		}
	}
	return exports
}

func exportedName(stmt ast.Statement) string {
	switch s := stmt.(type) {
	case *ast.FunctionStatement:
		if s.Name != nil {
			return s.Name.Value
		}
	case *ast.ClassStatement:
		if s.Name != nil {
			return s.Name.Value
		}
	case *ast.InterfaceStatement:
		if s.Name != nil {
			return s.Name.Value
		}
	case *ast.EnumStatement:
		if s.Name != nil {
			return s.Name.Value
		}
	case *ast.DeclarationStatement:
		if s.Name != nil {
			return s.Name.Value
		}
	}
	return ""
}

// checkCrossModuleSymbols flags `foo.bar()` where `foo` is a resolved
// import but `bar` is not exported by `foo`'s module. Native modules
// look up their export set in opts.NativeSymbols; source modules load
// their AST via opts.Resolver and ModuleCache.
func checkCrossModuleSymbols(file string, program *ast.Program, opts Options) []Diagnostic {
	aliases := collectImportAliases(program)
	if len(aliases) == 0 {
		return nil
	}
	cache := opts.ModuleCache
	if cache == nil {
		cache = NewModuleCache()
	}
	diags := []Diagnostic{}
	for _, stmt := range program.Statements {
		if _, ok := stmt.(*ast.ImportStatement); ok {
			continue
		}
		diags = append(diags, walkStatementForCrossModule(stmt, aliases, opts, cache)...)
	}
	return diagsWithFile(diags, file)
}

func diagsWithFile(diags []Diagnostic, file string) []Diagnostic {
	for i := range diags {
		if diags[i].File == "" {
			diags[i].File = file
		}
	}
	return diags
}

type importAlias struct {
	canonical string
	native    bool
}

func collectImportAliases(program *ast.Program) map[string]importAlias {
	out := map[string]importAlias{}
	for _, stmt := range program.Statements {
		imp, ok := stmt.(*ast.ImportStatement)
		if !ok {
			continue
		}
		alias := imp.ModuleName()
		canonical := joinPath(imp.Path)
		out[alias] = importAlias{canonical: canonical, native: IsNativeImport(canonical)}
	}
	return out
}

func joinPath(path []string) string {
	if len(path) == 0 {
		return ""
	}
	out := path[0]
	for _, p := range path[1:] {
		out += "." + p
	}
	return out
}

func walkStatementForCrossModule(stmt ast.Statement, aliases map[string]importAlias, opts Options, cache *ModuleCache) []Diagnostic {
	collector := &crossModuleCollector{aliases: aliases, opts: opts, cache: cache}
	walkStmt(stmt, collector)
	return collector.diags
}

type crossModuleCollector struct {
	aliases map[string]importAlias
	opts    Options
	cache   *ModuleCache
	diags   []Diagnostic
}

func (c *crossModuleCollector) visit(expr ast.Expression) {
	sel, ok := expr.(*ast.SelectorExpression)
	if !ok {
		return
	}
	ident, ok := sel.Object.(*ast.Identifier)
	if !ok {
		return
	}
	alias, ok := c.aliases[ident.Value]
	if !ok {
		return
	}
	if sel.Name == nil {
		return
	}
	name := sel.Name.Value
	if name == "" {
		return
	}
	if c.symbolExists(alias, name) {
		return
	}
	c.diags = append(c.diags, Diagnostic{
		Line:     sel.Name.Token.Line,
		Column:   sel.Name.Token.Column,
		Severity: SeverityError,
		Rule:     "import",
		Message:  fmt.Sprintf("%s has no exported member %s", alias.canonical, name),
	})
}

func (c *crossModuleCollector) symbolExists(alias importAlias, name string) bool {
	if alias.native {
		if c.opts.NativeSymbols == nil {
			return true
		}
		symbols, ok := c.opts.NativeSymbols[alias.canonical]
		if !ok {
			return true
		}
		_, exists := symbols[name]
		return exists
	}
	if c.opts.Resolver == nil {
		return true
	}
	path, err := c.opts.Resolver.Resolve(alias.canonical)
	if err != nil {
		return true
	}
	_, exports, err := c.cache.load(path)
	if err != nil {
		return true
	}
	_, exists := exports[name]
	return exists
}

// walkStmt drives expression visits across statement shapes; mirrors
// the recursion the lint pass uses, kept narrow to selector expressions.
func walkStmt(stmt ast.Statement, c *crossModuleCollector) {
	switch s := stmt.(type) {
	case *ast.ExportStatement:
		walkStmt(s.Statement, c)
	case *ast.DeclarationStatement:
		walkExpr(s.Value, c)
	case *ast.DestructuringStatement:
		walkExpr(s.Value, c)
	case *ast.ExpressionStatement:
		walkExpr(s.Expression, c)
	case *ast.ReturnStatement:
		walkExpr(s.Value, c)
	case *ast.YieldStatement:
		walkExpr(s.Value, c)
	case *ast.SimpleStatement:
		walkExpr(s.Value, c)
	case *ast.IfStatement:
		walkExpr(s.Condition, c)
		walkBlock(s.Consequence, c)
		for _, clause := range s.ElseIfs {
			walkExpr(clause.Condition, c)
			walkBlock(clause.Body, c)
		}
		walkBlock(s.Alternative, c)
	case *ast.WhileStatement:
		walkExpr(s.Condition, c)
		walkBlock(s.Body, c)
	case *ast.ForStatement:
		walkStmt(s.Init, c)
		walkExpr(s.Condition, c)
		walkStmt(s.Update, c)
		walkExpr(s.Iterable, c)
		walkBlock(s.Body, c)
	case *ast.FunctionStatement:
		walkBlock(s.Body, c)
	case *ast.ClassStatement:
		for _, member := range s.Members {
			walkStmt(member, c)
		}
	case *ast.TryStatement:
		walkBlock(s.Body, c)
		for _, clause := range s.Catches {
			walkBlock(clause.Body, c)
		}
		walkBlock(s.Finally, c)
	case *ast.MatchStatement:
		walkExpr(s.Expr, c)
		for _, clause := range s.Cases {
			walkExpr(clause.Guard, c)
			walkExpr(clause.Value, c)
			walkBlock(clause.Body, c)
		}
	case *ast.InitStatement:
		walkBlock(s.Body, c)
	}
}

func walkBlock(block *ast.BlockStatement, c *crossModuleCollector) {
	if block == nil {
		return
	}
	for _, stmt := range block.Statements {
		walkStmt(stmt, c)
	}
}

func walkExpr(expr ast.Expression, c *crossModuleCollector) {
	if expr == nil {
		return
	}
	c.visit(expr)
	switch e := expr.(type) {
	case *ast.SpreadExpression:
		walkExpr(e.Value, c)
	case *ast.InterpolatedString:
		for _, part := range e.Parts {
			walkExpr(part, c)
		}
	case *ast.PrefixExpression:
		walkExpr(e.Right, c)
	case *ast.PostfixExpression:
		walkExpr(e.Left, c)
	case *ast.InfixExpression:
		walkExpr(e.Left, c)
		walkExpr(e.Right, c)
	case *ast.AssignmentExpression:
		walkExpr(e.Left, c)
		walkExpr(e.Value, c)
	case *ast.SelectorExpression:
		walkExpr(e.Object, c)
	case *ast.CallExpression:
		walkExpr(e.Callee, c)
		for _, arg := range e.Arguments {
			walkExpr(arg.Value, c)
		}
	case *ast.IndexExpression:
		walkExpr(e.Left, c)
		walkExpr(e.Index, c)
	case *ast.ListLiteral:
		for _, element := range e.Elements {
			walkExpr(element, c)
		}
	case *ast.DictLiteral:
		for _, pair := range e.Entries {
			walkExpr(pair.Key, c)
			walkExpr(pair.Value, c)
		}
	case *ast.SetLiteral:
		for _, element := range e.Elements {
			walkExpr(element, c)
		}
	case *ast.RangeExpression:
		walkExpr(e.Start, c)
		walkExpr(e.End, c)
		walkExpr(e.Step, c)
	case *ast.FunctionLiteral:
		walkBlock(e.Body, c)
	case *ast.MatchExpression:
		walkExpr(e.Expr, c)
		for _, clause := range e.Cases {
			walkExpr(clause.Guard, c)
			walkExpr(clause.Value, c)
			walkBlock(clause.Body, c)
		}
	case *ast.AwaitExpression:
		walkExpr(e.Value, c)
	case *ast.CastExpression:
		walkExpr(e.Value, c)
	case *ast.TernaryExpression:
		walkExpr(e.Condition, c)
		walkExpr(e.ThenExpr, c)
		walkExpr(e.ElseExpr, c)
	}
}
