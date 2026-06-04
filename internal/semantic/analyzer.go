package semantic

import (
	"fmt"
	"strings"

	"geblang/internal/ast"
	"geblang/internal/token"
)

// Severity is the importance of a semantic diagnostic. SeverityError
// is the default (zero value) so all existing call sites that haven't
// explicitly opted into a warning continue to be treated as errors.
type Severity int

const (
	SeverityError   Severity = 0
	SeverityWarning Severity = 1
)

// Diagnostic is a single semantic finding. Line and Column are 1-based,
// matching the parser's error positions. Zero values mean "position
// unknown" and downstream consumers (the LSP server) will surface
// them at (1, 1) as a fallback. Severity controls whether the
// finding blocks execution in `geblang run` (Error) or prints to
// stderr but allows the program to start (Warning).
type Diagnostic struct {
	Message  string
	Line     int
	Column   int
	Severity Severity
}

type Analyzer struct {
	diagnostics []Diagnostic
	scopes      []map[string]typeInfo
	functions   map[string][]methodInfo
	classes     map[string]classInfo
	interfaces  map[string]interfaceInfo
	aliases     map[string]typeInfo
	// classSurface, when set, returns the full member set (methods +
	// fields, walked across parents/interfaces and modules) for a class
	// name, plus whether the class was cleanly resolvable. Injected by
	// the check command to enable cross-module unknown-method detection;
	// nil in the execution (test/run) path, which disables the check.
	classSurface func(className string) (map[string]bool, bool)
	// methodChecks enables the unknown-method checks (primitive + class).
	// Off in the execution path; turned on by the check command.
	methodChecks bool
}

// SetClassSurfaceResolver installs the cross-module class member
// resolver used by the unknown-method check. Call before Analyze.
func (a *Analyzer) SetClassSurfaceResolver(fn func(string) (map[string]bool, bool)) {
	a.classSurface = fn
}

// EnableMethodChecks turns on the unknown-method diagnostics (primitive
// method typos and, when a class resolver is set, class methods). Call
// before Analyze.
func (a *Analyzer) EnableMethodChecks() {
	a.methodChecks = true
}

type typeInfo struct {
	name     string
	nullable bool
	known    bool
	// args carries generic type arguments. For `list<int>`, args = [int].
	// For `dict<string, User>`, args = [string, User]. For non-generic types,
	// args is nil. Element- and call-argument validation walks these.
	args []typeInfo
	// destroyed is set on a binding after a `del x` statement
	// retires it. Identifier references that resolve to a
	// destroyed binding emit a 'use of destroyed binding' error.
	// Re-binding (let / const / typed declaration) of the same
	// name creates a fresh entry with destroyed=false.
	destroyed bool
}

type classInfo struct {
	name       string
	parent     string
	implements []string
	methods    map[string][]methodInfo
}

type interfaceInfo struct {
	name    string
	parents []string
	methods map[string][]methodInfo
}

type methodInfo struct {
	name       string
	parameters []typeInfo
	// minArgs is the smallest positional arg count a caller may pass.
	// Defaults to len(parameters); lowered when trailing parameters
	// declare default values.
	minArgs    int
	returnType typeInfo
	typeParams []typeParam
}

// typeParam captures the name and optional constraint of a generic type
// parameter declared on a function/method/class signature. The name is the
// literal identifier (`T`); the constraint, if present, is the upper-bound
// type the inferred argument must be assignable to.
type typeParam struct {
	name       string
	constraint typeInfo
}

// builtinModuleNames is the set of names that resolve to a built-in
// stdlib module. Declaring a local variable with any of these names
// is legal (lexical scope: the local wins at the resolution site),
// but the analyzer still surfaces a warning so a reader skimming
// the code isn't misled by `json.parse(x)` on a shadowed local.
//
// Common parameter names like `args`, `path`, and `name` are
// intentionally NOT in the list - they collide too often with
// idiomatic Geblang variable naming to be useful warnings, and
// the runtime resolution rule eliminates the actual bug surface.
var builtinModuleNames = map[string]bool{
	"io": true, "sys": true, "process": true, "async": true,
	"json": true, "xml": true, "toml": true, "yaml": true, "csv": true,
	"collections": true, "secrets": true, "random": true,
	"strbuilder": true,
	"schema": true, "serde": true, "metrics": true,
	"trace": true, "profile": true, "crypt": true,
	"encoding": true, "compress": true, "template": true,
	"re": true, "pcre": true, "markdown": true, "datetime": true, "uuid": true,
	"dotenv": true, "cli": true, "http": true,
	"websocket": true, "smtp": true, "db": true,
	"reflect": true, "log": true,
	"watch": true, "errors": true, "freeze": true,
}

// checkBuiltinModuleShadow emits a warning when `name` would shadow
// a built-in stdlib module. We skip primitive-typed declarations
// (`string path`, `int errors` etc.) where the user is plainly not
// using the name as a module reference - the warning is reserved
// for collection / class / any-typed shadows where a `.method()`
// dispatch on the shadowed name would surface as the confusing
// "X is not a module" runtime error.
func (a *Analyzer) checkBuiltinModuleShadow(name *ast.Identifier, declaredType *ast.TypeRef) {
	if name == nil || name.Value == "" {
		return
	}
	if !builtinModuleNames[name.Value] {
		return
	}
	if declaredType != nil {
		switch strings.ToLower(declaredType.Name) {
		case "string", "int", "float", "decimal", "bool", "bytes":
			return
		}
	}
	a.warningAt(name.Token,
		"%q shadows the built-in stdlib module %q; identifier resolution may pick the module unexpectedly - rename the local",
		name.Value, name.Value)
}

func New() *Analyzer {
	return &Analyzer{scopes: []map[string]typeInfo{{}}, functions: map[string][]methodInfo{}, classes: map[string]classInfo{}, interfaces: map[string]interfaceInfo{}, aliases: map[string]typeInfo{}}
}

func (a *Analyzer) Analyze(program *ast.Program) []Diagnostic {
	a.collectTypeDeclarations(program.Statements)
	a.validateTopLevelOverloads()
	a.validateClassOverloads()
	a.validateInterfaceOverloads()
	a.validateInterfaceImplementations()
	a.validateCastDunderReturns(program.Statements)
	isModule := fileIsModule(program.Statements)
	seenInit := false
	for _, stmt := range program.Statements {
		a.analyzeStatement(stmt, nil)
		switch s := stmt.(type) {
		case *ast.InitStatement:
			if seenInit {
				a.errorAt(s.Token, "only one init block is allowed per file")
			}
			seenInit = true
		}
		if isModule && !isAllowedAtModuleTopLevel(stmt) {
			a.errorAt(statementToken(stmt),
				"free-standing top-level %s is not allowed in a module file; wrap imperative setup in an init { ... } block",
				moduleTopLevelKind(stmt))
		}
	}
	return a.diagnostics
}

// fileIsModule reports whether the program is a module file - i.e. its
// first non-comment top-level statement is `module name;`. Script
// files (no module declaration) keep their full top-level freedom.
func fileIsModule(stmts []ast.Statement) bool {
	for _, stmt := range stmts {
		if _, ok := stmt.(*ast.ModuleStatement); ok {
			return true
		}
		// First non-module statement decides; even if a later one is a
		// ModuleStatement (which would be a parse error in practice),
		// we treat the file as a script.
		return false
	}
	return false
}

// isAllowedAtModuleTopLevel returns true for the declarative
// statements that are permitted at the top level of a module file
// (declarations, type aliases, imports/exports, classes, interfaces,
// enums, functions, and the at-most-one init block). Anything else
// must live inside `init { ... }`.
func isAllowedAtModuleTopLevel(stmt ast.Statement) bool {
	switch s := stmt.(type) {
	case *ast.ModuleStatement,
		*ast.ImportStatement,
		*ast.FromImportStatement,
		*ast.InitStatement,
		*ast.TypeAliasStatement,
		*ast.DeclarationStatement,
		*ast.FunctionStatement,
		*ast.ClassStatement,
		*ast.InterfaceStatement,
		*ast.EnumStatement:
		return true
	case *ast.ExportStatement:
		// export wraps another statement - the wrapped form is what
		// needs to be a declarative kind.
		return isAllowedAtModuleTopLevel(s.Statement)
	case *ast.DestructuringStatement:
		// `let [a, b] = ...` is a declaration; bare `[a, b] = ...` is
		// not.
		return s.Define
	}
	return false
}

// statementToken returns the leading token of any statement, used to
// anchor diagnostics. Falls back to a zero token (position-less) when
// the type isn't recognised.
func statementToken(stmt ast.Statement) token.Token {
	switch s := stmt.(type) {
	case *ast.ModuleStatement:
		return s.Token
	case *ast.ImportStatement:
		return s.Token
	case *ast.FromImportStatement:
		return s.Token
	case *ast.ExportStatement:
		return s.Token
	case *ast.InitStatement:
		return s.Token
	case *ast.TypeAliasStatement:
		return s.Token
	case *ast.DeclarationStatement:
		return s.Token
	case *ast.DestructuringStatement:
		return s.Token
	case *ast.FunctionStatement:
		return s.Token
	case *ast.ClassStatement:
		return s.Token
	case *ast.InterfaceStatement:
		return s.Token
	case *ast.EnumStatement:
		return s.Token
	case *ast.ExpressionStatement:
		return tokenOfExpression(s.Expression)
	case *ast.ReturnStatement:
		return s.Token
	case *ast.YieldStatement:
		return s.Token
	case *ast.SimpleStatement:
		return s.Token
	case *ast.IfStatement:
		return s.Token
	case *ast.WhileStatement:
		return s.Token
	case *ast.ForStatement:
		return s.Token
	case *ast.TryStatement:
		return s.Token
	case *ast.MatchStatement:
		return s.Token
	case *ast.BlockStatement:
		return s.Token
	}
	return token.Token{}
}

// moduleTopLevelKind names the offending statement type for the
// diagnostic message.
func moduleTopLevelKind(stmt ast.Statement) string {
	switch stmt.(type) {
	case *ast.ExpressionStatement:
		return "expression"
	case *ast.ReturnStatement:
		return "return"
	case *ast.YieldStatement:
		return "yield"
	case *ast.SimpleStatement:
		return "statement"
	case *ast.IfStatement:
		return "if"
	case *ast.WhileStatement:
		return "while"
	case *ast.ForStatement:
		return "for"
	case *ast.TryStatement:
		return "try"
	case *ast.MatchStatement:
		return "match"
	case *ast.BlockStatement:
		return "block"
	case *ast.DestructuringStatement:
		return "destructuring assignment"
	}
	return "statement"
}

func (a *Analyzer) collectTypeDeclarations(stmts []ast.Statement) {
	for _, stmt := range stmts {
		switch stmt := stmt.(type) {
		case *ast.ExportStatement:
			a.collectTypeDeclarations([]ast.Statement{stmt.Statement})
		case *ast.TypeAliasStatement:
			a.aliases[strings.ToLower(stmt.Name.Value)] = a.typeInfoFromRef(stmt.Type)
		case *ast.FunctionStatement:
			info := a.methodInfoFromFunction(stmt)
			key := strings.ToLower(info.name)
			a.functions[key] = append(a.functions[key], info)
		case *ast.ClassStatement:
			info := classInfo{name: stmt.Name.Value, methods: map[string][]methodInfo{}}
			if stmt.Extends != nil {
				info.parent = stmt.Extends.Name
			}
			for _, iface := range stmt.Implements {
				info.implements = append(info.implements, iface.Name)
			}
			for _, member := range stmt.Members {
				if fn, ok := member.(*ast.FunctionStatement); ok && !fn.Static {
					method := a.methodInfoFromFunction(fn)
					key := strings.ToLower(method.name)
					info.methods[key] = append(info.methods[key], method)
				}
			}
			a.classes[info.name] = info
		case *ast.InterfaceStatement:
			info := interfaceInfo{name: stmt.Name.Value, methods: map[string][]methodInfo{}}
			for _, parent := range stmt.Parents {
				info.parents = append(info.parents, parent.Name)
			}
			for _, method := range stmt.Methods {
				methodInfo := a.methodInfoFromSignature(method)
				key := strings.ToLower(methodInfo.name)
				info.methods[key] = append(info.methods[key], methodInfo)
			}
			a.interfaces[info.name] = info
		}
	}
}

// castDunderExpectedReturn returns the declared-return-type a cast
// dunder must use. Empty when the method name is not a cast dunder.
func castDunderExpectedReturn(name string) string {
	switch name {
	case "__string":
		return "string"
	case "__int":
		return "int"
	case "__float":
		return "float"
	case "__bool":
		return "bool"
	case "__decimal":
		return "decimal"
	case "__bytes":
		return "bytes"
	}
	return ""
}

// validateCastDunderReturns rejects classes that declare a cast
// dunder (__string, __int, ...) with the wrong return type.
func (a *Analyzer) validateCastDunderReturns(stmts []ast.Statement) {
	for _, stmt := range stmts {
		class, ok := stmt.(*ast.ClassStatement)
		if !ok {
			continue
		}
		for _, member := range class.Members {
			fn, ok := member.(*ast.FunctionStatement)
			if !ok || fn.Static {
				continue
			}
			expected := castDunderExpectedReturn(fn.Name.Value)
			if expected == "" {
				continue
			}
			actual := ""
			if fn.ReturnType != nil {
				actual = fn.ReturnType.Name
			}
			if !strings.EqualFold(actual, expected) {
				a.errorAt(fn.Token, "%s.%s must declare return type %s, got %q", class.Name.Value, fn.Name.Value, expected, actual)
			}
		}
	}
}

func (a *Analyzer) validateTopLevelOverloads() {
	for name, methods := range a.functions {
		a.validateOverloadSet("function "+name, methods)
	}
}

func (a *Analyzer) validateClassOverloads() {
	for _, class := range a.classes {
		for name, methods := range class.methods {
			a.validateOverloadSet("method "+class.name+"."+name, methods)
		}
	}
}

func (a *Analyzer) validateInterfaceOverloads() {
	for _, iface := range a.interfaces {
		for name, methods := range iface.methods {
			a.validateOverloadSet("interface method "+iface.name+"."+name, methods)
		}
	}
}

func (a *Analyzer) validateOverloadSet(label string, methods []methodInfo) {
	seen := map[string]bool{}
	for _, method := range methods {
		key := method.signatureKey()
		if seen[key] {
			a.errorf("duplicate overload for %s with signature %s", label, key)
			continue
		}
		seen[key] = true
	}
}

func (a *Analyzer) validateInterfaceImplementations() {
	for _, class := range a.classes {
		for _, ifaceName := range class.implements {
			required := a.interfaceMethods(ifaceName, map[string]bool{})
			for name, expectedMethods := range required {
				for _, expected := range expectedMethods {
					if !a.classHasCompatibleMethod(class.name, name, expected) {
						a.errorf("class %s implements %s but is missing compatible method %s%s", class.name, ifaceName, name, expected.signatureKey())
					}
				}
			}
		}
	}
}

func (a *Analyzer) analyzeStatement(stmt ast.Statement, fn *ast.FunctionStatement) {
	switch stmt := stmt.(type) {
	case *ast.TypeAliasStatement:
		return
	case *ast.DeclarationStatement:
		a.analyzeDeclaration(stmt)
	case *ast.ExpressionStatement:
		a.analyzeExpression(stmt.Expression)
		a.validateCallStatementArgs(stmt.Expression)
	case *ast.ExportStatement:
		a.analyzeStatement(stmt.Statement, fn)
	case *ast.FunctionStatement:
		a.declare(stmt.Name.Value, typeInfo{name: "func", known: true})
		a.pushScope()
		for _, param := range stmt.Parameters {
			/* Parameter names are scoped to one function body and
			 * tend to use common labels like `args`, `path`, `io`
			 * intentionally. The shadowing diagnostic for those
			 * would create more noise than signal - reserved for
			 * top-level / class-level declarations where the
			 * confusion is more likely (`let errors = [...]`
			 * followed by `errors.push(...)`). */
			a.declare(param.Name.Value, a.typeInfoFromRef(param.Type))
			if param.Default != nil {
				a.checkAssignable(param.Type, param.Default, fmt.Sprintf("cannot use %s default for %s parameter %s", a.expressionTypeName(param.Default).display(), param.Type.String(), param.Name.Value))
			}
		}
		a.analyzeBlock(stmt.Body, stmt)
		a.popScope()
	case *ast.ReturnStatement:
		a.analyzeReturn(stmt, fn)
	case *ast.YieldStatement:
		if stmt.Value != nil {
			a.analyzeExpression(stmt.Value)
		}
	case *ast.InitStatement:
		a.analyzeBlock(stmt.Body, fn)
	case *ast.IfStatement:
		consequenceNarrowing, alternativeNarrowing := a.narrowingsForCondition(stmt.Condition)
		a.analyzeBlockWithNarrowing(stmt.Consequence, fn, consequenceNarrowing)
		for _, elseif := range stmt.ElseIfs {
			elseifCons, _ := a.narrowingsForCondition(elseif.Condition)
			a.analyzeBlockWithNarrowing(elseif.Body, fn, elseifCons)
		}
		a.analyzeBlockWithNarrowing(stmt.Alternative, fn, alternativeNarrowing)
	case *ast.WhileStatement:
		consequenceNarrowing, _ := a.narrowingsForCondition(stmt.Condition)
		a.analyzeBlockWithNarrowing(stmt.Body, fn, consequenceNarrowing)
	case *ast.ForStatement:
		if stmt.Init != nil {
			a.analyzeStatement(stmt.Init, fn)
		}
		if stmt.Update != nil {
			a.analyzeStatement(stmt.Update, fn)
		}
		a.analyzeBlock(stmt.Body, fn)
	case *ast.TryStatement:
		a.analyzeBlock(stmt.Body, fn)
		for _, catch := range stmt.Catches {
			a.analyzeBlock(catch.Body, fn)
		}
		a.analyzeBlock(stmt.Finally, fn)
	case *ast.MatchStatement:
		for _, matchCase := range stmt.Cases {
			a.analyzeBlock(matchCase.Body, fn)
		}
	case *ast.ClassStatement:
		a.declare(stmt.Name.Value, typeInfo{name: stmt.Name.Value, known: true})
	case *ast.InterfaceStatement:
		a.declare(stmt.Name.Value, typeInfo{name: stmt.Name.Value, known: true})
	case *ast.EnumStatement:
		a.declare(stmt.Name.Value, typeInfo{name: stmt.Name.Value, known: true})
	case *ast.DelStatement:
		if stmt.Target == nil {
			return
		}
		info, ok := a.lookup(stmt.Target.Value)
		if !ok {
			a.errorAt(stmt.Target.Token, "del: unknown identifier %q", stmt.Target.Value)
			return
		}
		if a.isDeclarationName(stmt.Target.Value, info) {
			a.errorAt(stmt.Target.Token, "cannot del %q: del operates on variables, not declarations", stmt.Target.Value)
			return
		}
		a.markBindingDestroyed(stmt.Target.Value)
	}
}

func (a *Analyzer) analyzeBlock(block *ast.BlockStatement, fn *ast.FunctionStatement) {
	a.analyzeBlockWithNarrowing(block, fn, nil)
}

func (a *Analyzer) analyzeBlockWithNarrowing(block *ast.BlockStatement, fn *ast.FunctionStatement, narrowing map[string]typeInfo) {
	if block == nil {
		return
	}
	a.pushScope()
	defer a.popScope()
	for name, typ := range narrowing {
		a.declare(name, typ)
	}
	for _, stmt := range block.Statements {
		a.analyzeStatement(stmt, fn)
		// Guard-pattern narrowing: if (x == null) { return/throw/... } narrows x to
		// non-null for all subsequent statements in this block.
		if ifStmt, ok := stmt.(*ast.IfStatement); ok && len(ifStmt.ElseIfs) == 0 && ifStmt.Alternative == nil {
			if a.blockAlwaysExits(ifStmt.Consequence) {
				_, altNarrowing := a.narrowingsForCondition(ifStmt.Condition)
				for name, typ := range altNarrowing {
					a.declare(name, typ)
				}
			}
		}
	}
}

func (a *Analyzer) analyzeDeclaration(stmt *ast.DeclarationStatement) {
	a.checkBuiltinModuleShadow(stmt.Name, stmt.Type)
	var declared typeInfo
	if stmt.Type != nil {
		declared = a.typeInfoFromRef(stmt.Type)
		a.checkTypeRefName(stmt.Type, stmt.Name.Value)
	} else if stmt.Value != nil {
		declared = a.expressionTypeName(stmt.Value)
	}
	if declared.known {
		a.declare(stmt.Name.Value, declared)
	}
	if stmt.Value != nil {
		a.analyzeExpression(stmt.Value)
	}
	if stmt.Type == nil || stmt.Value == nil {
		return
	}
	a.validateCallExpression(stmt.Value, declared)
	a.validateContainerLiteral(declared, stmt.Value, stmt.Name.Value)
	a.checkAssignable(stmt.Type, stmt.Value, fmt.Sprintf("cannot assign %s to %s %s", a.expressionTypeName(stmt.Value).display(), stmt.Type.String(), stmt.Name.Value))
}

// builtinTypeNames are the lower-case type names the language treats
// as primitives. Lower-case type refs that aren't in this set, an
// alias, a class, or an interface are flagged as unknown by
// checkTypeRefName. The set is intentionally permissive (includes
// pseudo-types like `iterable` and `callable`) so that legitimate
// stdlib signatures parse without complaint.
var builtinTypeNames = map[string]struct{}{
	"string": {}, "int": {}, "float": {}, "decimal": {}, "bool": {},
	"bytes": {}, "list": {}, "dict": {}, "set": {}, "range": {},
	"void": {}, "any": {}, "null": {},
	"callable": {}, "func": {}, "function": {},
	"iterable": {}, "generator": {},
}

func (a *Analyzer) isKnownTypeName(name string) bool {
	if name == "" {
		return true
	}
	if _, ok := builtinTypeNames[strings.ToLower(name)]; ok {
		return true
	}
	if _, ok := a.aliases[strings.ToLower(name)]; ok {
		return true
	}
	if _, ok := a.classes[name]; ok {
		return true
	}
	if _, ok := a.interfaces[name]; ok {
		return true
	}
	if _, ok := a.lookup(name); ok {
		return true
	}
	return false
}

// checkTypeRefName flags a typed declaration whose type name is all
// lower-case and not recognised. The lower-case scope deliberately
// ignores PascalCase and single-uppercase identifiers so generic type
// parameters (`T`, `U`) and yet-to-be-declared class names don't
// false-positive. The diagnostic targets the common typo case where
// two bare identifiers (`aaa bbb;`) parse as a typed declaration with
// an unknown type.
func (a *Analyzer) checkTypeRefName(ref *ast.TypeRef, declName string) {
	if ref == nil || ref.Operator != "" || ref.ListAlias {
		return
	}
	name := ref.Name
	if name == "" || !isAllLowerCase(name) {
		return
	}
	if a.isKnownTypeName(name) {
		return
	}
	a.errorAt(ref.Token, "unknown type %q in declaration of %s", name, declName)
}

func isAllLowerCase(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return false
		}
	}
	return true
}

// Declare exposes the analyzer's binding-registration so REPL sessions
// can re-seed bindings from prior prompts before analyzing a new one.
// typeName is wrapped in a minimal typeInfo; nullable and generic
// arguments are not preserved across prompt boundaries.
func (a *Analyzer) Declare(name, typeName string) {
	if name == "" {
		return
	}
	a.declare(name, typeInfo{name: typeName, known: true})
}

// validateContainerLiteral validates that the elements of a list/dict/set
// literal match the declared generic element type. For nested literals, the
// check recurses (e.g. `list<list<int>>` validates each inner list as
// `list<int>`). The function is a no-op when the declared type is not a
// generic collection or when no value is provided.
func (a *Analyzer) validateContainerLiteral(declared typeInfo, value ast.Expression, name string) {
	if !declared.known || len(declared.args) == 0 {
		return
	}
	switch declared.name {
	case "list", "set":
		var elements []ast.Expression
		switch lit := value.(type) {
		case *ast.ListLiteral:
			elements = lit.Elements
		case *ast.SetLiteral:
			elements = lit.Elements
		default:
			return
		}
		elemType := declared.args[0]
		for _, element := range elements {
			a.checkValueAgainstType(elemType, element, fmt.Sprintf("cannot use %s as %s element of %s %s", a.expressionTypeName(element).display(), elemType.display(), declared.name, name))
			a.validateContainerLiteral(elemType, element, name)
		}
	case "dict":
		lit, ok := value.(*ast.DictLiteral)
		if !ok {
			return
		}
		if len(declared.args) < 2 {
			return
		}
		keyType := declared.args[0]
		valueType := declared.args[1]
		for _, entry := range lit.Entries {
			a.checkValueAgainstType(keyType, entry.Key, fmt.Sprintf("cannot use %s as %s key of dict %s", a.expressionTypeName(entry.Key).display(), keyType.display(), name))
			a.checkValueAgainstType(valueType, entry.Value, fmt.Sprintf("cannot use %s as %s value of dict %s", a.expressionTypeName(entry.Value).display(), valueType.display(), name))
			a.validateContainerLiteral(valueType, entry.Value, name)
		}
	}
}

// checkTypedCollectionMethodCall validates that mutation methods on typed
// collections (list<T>.push, set<T>.add, dict<K,V>.set/insert) receive
// arguments compatible with the declared element/key/value types.
// checkPrimitiveMethodCall flags `value.method(...)` when value has a
// statically known built-in type (from a literal or typed binding) that
// has no such method. Conversion helpers and unknown/any types are
// never flagged.
func (a *Analyzer) checkPrimitiveMethodCall(call *ast.CallExpression) {
	if !a.methodChecks {
		return
	}
	selector, ok := call.Callee.(*ast.SelectorExpression)
	if !ok || selector.Name == nil || selector.Optional {
		return
	}
	recv := a.expressionTypeName(selector.Object)
	if !recv.known || recv.nullable {
		return
	}
	isPrimitive, exists := primitiveMethodLookup(recv.name, selector.Name.Value)
	if !isPrimitive || exists {
		return
	}
	// native.PrimitiveMethods is the authoritative built-in method set,
	// guarded against phantoms (every entry callable) and completeness
	// drift (the tripwire test), so an unlisted method is a real typo.
	a.errorAt(selector.Name.Token, "%s has no method %s", recv.name, selector.Name.Value)
}

// checkClassMethodCall flags `obj.method(...)` when obj is a typed
// instance of a resolvable class whose full hierarchy (parents +
// interfaces, across modules) has no such method or callable field.
// Only runs when a class-surface resolver is installed (the check
// command); stays silent on any uncertainty to avoid false positives.
func (a *Analyzer) checkClassMethodCall(call *ast.CallExpression) {
	if a.classSurface == nil {
		return
	}
	selector, ok := call.Callee.(*ast.SelectorExpression)
	if !ok || selector.Name == nil || selector.Optional {
		return
	}
	receiver, ok := selector.Object.(*ast.Identifier)
	if !ok {
		return
	}
	receiverType, ok := a.lookup(receiver.Value)
	if !ok || !receiverType.known || receiverType.nullable {
		return
	}
	members, resolvable := a.classSurface(receiverType.name)
	if !resolvable {
		return
	}
	if members[strings.ToLower(selector.Name.Value)] {
		return
	}
	a.errorAt(selector.Name.Token, "%s has no method %s", receiverType.name, selector.Name.Value)
}

func (a *Analyzer) checkTypedCollectionMethodCall(call *ast.CallExpression) {
	selector, ok := call.Callee.(*ast.SelectorExpression)
	if !ok {
		return
	}
	receiver, ok := selector.Object.(*ast.Identifier)
	if !ok {
		return
	}
	receiverType, ok := a.lookup(receiver.Value)
	if !ok || !receiverType.known || len(receiverType.args) == 0 {
		return
	}
	method := strings.ToLower(selector.Name.Value)
	switch receiverType.name {
	case "list":
		switch method {
		case "push", "prepend", "append", "unshift":
			a.validateArgs(call.Arguments, []typeInfo{receiverType.args[0]},
				fmt.Sprintf("list<%s>.%s element", receiverType.args[0].display(), selector.Name.Value), receiver.Value)
		case "insert":
			// insert(index, value) - second arg is the element.
			if len(call.Arguments) >= 2 {
				a.validateArgAt(call.Arguments[1], receiverType.args[0],
					fmt.Sprintf("list<%s>.insert element", receiverType.args[0].display()), receiver.Value)
			}
		case "set":
			// set(index, value)
			if len(call.Arguments) >= 2 {
				a.validateArgAt(call.Arguments[1], receiverType.args[0],
					fmt.Sprintf("list<%s>.set element", receiverType.args[0].display()), receiver.Value)
			}
		}
	case "set":
		if method == "add" || method == "insert" {
			a.validateArgs(call.Arguments, []typeInfo{receiverType.args[0]},
				fmt.Sprintf("set<%s>.%s element", receiverType.args[0].display(), selector.Name.Value), receiver.Value)
		}
	case "dict":
		if len(receiverType.args) < 2 {
			return
		}
		key := receiverType.args[0]
		value := receiverType.args[1]
		switch method {
		case "set", "insert", "put":
			// (key, value)
			if len(call.Arguments) >= 1 {
				a.validateArgAt(call.Arguments[0], key,
					fmt.Sprintf("dict<%s,%s>.%s key", key.display(), value.display(), selector.Name.Value), receiver.Value)
			}
			if len(call.Arguments) >= 2 {
				a.validateArgAt(call.Arguments[1], value,
					fmt.Sprintf("dict<%s,%s>.%s value", key.display(), value.display(), selector.Name.Value), receiver.Value)
			}
		}
	}
}

func (a *Analyzer) validateArgs(args []ast.CallArgument, expected []typeInfo, role, receiver string) {
	for i, arg := range args {
		if i >= len(expected) {
			break
		}
		a.validateArgAt(arg, expected[i], role, receiver)
	}
}

func (a *Analyzer) validateArgAt(arg ast.CallArgument, target typeInfo, role, receiver string) {
	if arg.Spread {
		return
	}
	a.checkValueAgainstType(target, arg.Value, fmt.Sprintf("cannot use %s as %s of %s", a.expressionTypeName(arg.Value).display(), role, receiver))
}

// checkValueAgainstType reports an error if expr cannot be assigned to the
// type described by target. Used by validateContainerLiteral and the
// .push/.set/.add element-type check.
func (a *Analyzer) checkValueAgainstType(target typeInfo, expr ast.Expression, message string) {
	if !target.known {
		return
	}
	if target.name == "any" {
		return
	}
	actual := a.expressionTypeName(expr)
	if !actual.known {
		return
	}
	tok := tokenOfExpression(expr)
	if actual.name == "null" {
		if !target.nullable {
			a.errorAt(tok, "%s", message)
		}
		return
	}
	if isNumericLiteralWidening(target.name, expr) {
		return
	}
	if !target.nullable && actual.nullable {
		a.errorAt(tok, "%s", message)
		return
	}
	if !a.isAssignable(target, actual) {
		a.errorAt(tok, "%s", message)
	}
}

func (a *Analyzer) analyzeReturn(stmt *ast.ReturnStatement, fn *ast.FunctionStatement) {
	if fn == nil || fn.ReturnType == nil {
		return
	}
	if stmt.Value == nil {
		/* Bare `return;` is always legal in a void function -
		 * there's nothing to assign, the early-exit just terminates
		 * the function body. For non-void returns, bare return is
		 * legal when the return type is nullable or `any`.
		 * Otherwise it's an error: the caller would observe a null
		 * where it expected a concrete value. */
		if !fn.ReturnType.Nullable &&
			!strings.EqualFold(fn.ReturnType.Name, "any") &&
			!strings.EqualFold(fn.ReturnType.Name, "void") {
			a.errorAt(stmt.Token, "cannot return null from %s returning %s", fn.Name.Value, fn.ReturnType.String())
		}
		return
	}
	a.validateCallExpression(stmt.Value, a.typeInfoFromRef(fn.ReturnType))
	a.checkAssignable(fn.ReturnType, stmt.Value, fmt.Sprintf("cannot return %s from %s returning %s", a.expressionTypeName(stmt.Value).display(), fn.Name.Value, fn.ReturnType.String()))
}

func (a *Analyzer) checkAssignable(target *ast.TypeRef, expr ast.Expression, message string) {
	if target == nil || target.Operator != "" {
		return
	}
	targetInfo := a.typeInfoFromRef(target)
	actual := a.expressionTypeName(expr)
	if !actual.known {
		return
	}
	if actual.name == "null" {
		if !target.Nullable && targetInfo.name != "any" {
			a.errorf("%s", message)
		}
		return
	}
	if targetInfo.name == "any" {
		return
	}
	if isNumericLiteralWidening(targetInfo.name, expr) {
		return
	}
	if target.ListAlias || targetInfo.name == "list" {
		if actual.name != "list" {
			a.errorf("%s", message)
			return
		}
		if len(targetInfo.args) > 0 && len(actual.args) > 0 && !a.isAssignable(targetInfo, actual) {
			a.errorf("%s", message)
		}
		return
	}
	if !targetInfo.nullable && actual.nullable {
		a.errorf("%s", message)
		return
	}
	if !a.isAssignable(targetInfo, actual) {
		a.errorf("%s", message)
	}
}

func (a *Analyzer) validateCallExpression(expr ast.Expression, expected typeInfo) {
	call, ok := expr.(*ast.CallExpression)
	if !ok {
		return
	}
	ident, ok := call.Callee.(*ast.Identifier)
	if !ok {
		return
	}
	if _, ok := a.classes[ident.Value]; ok {
		return
	}
	overloads := a.functions[strings.ToLower(ident.Value)]
	if len(overloads) == 0 {
		return
	}
	// Generic call-site inference and constraint check fires regardless of
	// whether the surrounding context expects a particular return type.
	a.checkGenericCallInference(call, ident.Value, overloads)
	if !expected.known {
		return
	}
	args := make([]typeInfo, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		if arg.Name != nil {
			return
		}
		argType := a.expressionTypeName(arg.Value)
		if !argType.known {
			return
		}
		args = append(args, argType)
	}
	matches := []methodInfo{}
	for _, overload := range overloads {
		if !a.callArgumentsCompatible(args, overload.parameters, overload.minArgs) {
			continue
		}
		if !a.returnTypeCompatible(expected, overload.returnType) {
			continue
		}
		matches = append(matches, overload)
	}
	if len(matches) == 0 {
		a.errorf("no matching overload for %s returning %s", ident.Value, expected.display())
		return
	}
	if len(matches) > 1 {
		a.errorf("ambiguous overload for %s returning %s", ident.Value, expected.display())
	}
}

// validateCallStatementArgs flags collection element-type mismatches on bare
// statement calls (e.g. `wantStrings(ints);`) that the bytecode compiler cannot
// see, since it strips collection element args. Scalar / arity / base-type
// mismatches are left to the bytecode compiler to avoid duplicate diagnostics:
// an error is raised only when some overload matches on base types but none
// matches once element types are checked.
func (a *Analyzer) validateCallStatementArgs(expr ast.Expression) {
	call, ok := expr.(*ast.CallExpression)
	if !ok {
		return
	}
	ident, ok := call.Callee.(*ast.Identifier)
	if !ok {
		return
	}
	if _, ok := a.classes[ident.Value]; ok {
		return
	}
	overloads := a.functions[strings.ToLower(ident.Value)]
	if len(overloads) == 0 {
		return
	}
	args := make([]typeInfo, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		if arg.Name != nil || arg.Spread {
			return
		}
		argType := a.expressionTypeName(arg.Value)
		if !argType.known {
			return
		}
		args = append(args, argType)
	}
	baseMatch := false
	for _, overload := range overloads {
		if a.callArgumentsCompatible(args, overload.parameters, overload.minArgs) {
			return
		}
		if a.callArgumentsBaseCompatible(args, overload.parameters, overload.minArgs) {
			baseMatch = true
		}
	}
	if baseMatch {
		a.errorf("no matching overload for %s with the given argument types", ident.Value)
	}
}

// callArgumentsBaseCompatible mirrors callArgumentsCompatible but compares only
// the base type names (ignoring collection element args), matching what the
// bytecode compiler resolves at compile time.
func (a *Analyzer) callArgumentsBaseCompatible(args, parameters []typeInfo, minArgs int) bool {
	if len(args) < minArgs || len(args) > len(parameters) {
		return false
	}
	for i, arg := range args {
		if !parameters[i].known || !arg.known {
			return false
		}
		if arg.name == "null" {
			if !parameters[i].nullable {
				return false
			}
			continue
		}
		if !parameters[i].nullable && arg.nullable {
			return false
		}
		if parameters[i].name != "any" && !a.isAssignableType(parameters[i].name, arg.name) {
			return false
		}
	}
	return true
}

// checkGenericCallInference walks each overload, infers the binding of every
// type parameter T from the argument types, and verifies (a) T is bound to a
// single type across multiple argument positions and (b) the inferred binding
// satisfies the parameter's declared constraint. Only fires when at least one
// overload has type parameters; non-generic overloads are unaffected.
func (a *Analyzer) checkGenericCallInference(call *ast.CallExpression, name string, overloads []methodInfo) {
	// Collect argument types up front.
	args := make([]typeInfo, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		if arg.Name != nil || arg.Spread {
			return
		}
		args = append(args, a.expressionTypeName(arg.Value))
	}
	// If a unique overload matches by arity AND has type params, run the
	// inference check on it.
	var generic *methodInfo
	for i := range overloads {
		if len(overloads[i].typeParams) == 0 {
			continue
		}
		if len(overloads[i].parameters) != len(args) {
			continue
		}
		if generic != nil {
			return // ambiguous; let the overload-resolution path report it
		}
		copy := overloads[i]
		generic = &copy
	}
	if generic == nil {
		return
	}
	paramSet := map[string]bool{}
	for _, tp := range generic.typeParams {
		paramSet[strings.ToLower(tp.name)] = true
	}
	bindings := map[string]typeInfo{}
	for i, paramType := range generic.parameters {
		if i >= len(args) {
			break
		}
		actual := args[i]
		if !actual.known {
			continue
		}
		a.inferTypeBinding(paramType, actual, paramSet, bindings, name)
	}
	for _, tp := range generic.typeParams {
		key := strings.ToLower(tp.name)
		bound, ok := bindings[key]
		if !ok || !bound.known {
			continue
		}
		if !tp.constraint.known || tp.constraint.name == "any" {
			continue
		}
		if !a.isAssignableType(tp.constraint.name, bound.name) {
			a.errorf("type parameter %s of %s is inferred as %s which does not satisfy constraint %s",
				tp.name, name, bound.display(), tp.constraint.display())
		}
	}
}

// inferTypeBinding walks paramType (a parameter's declared type) alongside
// actual (an argument's inferred type). When paramType is a bare type-parameter
// reference (its name matches a key in paramSet), bind it to actual. If a
// previous binding exists and differs, raise a consistency error. Generic
// argument lists are walked structurally so e.g. `list<T>` paired with
// `list<int>` binds T -> int.
func (a *Analyzer) inferTypeBinding(paramType, actual typeInfo, paramSet map[string]bool, bindings map[string]typeInfo, name string) {
	if !paramType.known || !actual.known {
		return
	}
	key := strings.ToLower(paramType.name)
	if paramSet[key] {
		if existing, ok := bindings[key]; ok && existing.known {
			if !sameTypeInfo(existing, actual) && !a.isAssignableType(existing.name, actual.name) && !a.isAssignableType(actual.name, existing.name) {
				a.errorf("type parameter %s of %s is bound to both %s and %s",
					paramType.name, name, existing.display(), actual.display())
			}
			return
		}
		bindings[key] = actual
		return
	}
	// Structural walk for generic containers: list<T>, dict<K,V>, Box<T>, etc.
	if len(paramType.args) > 0 && len(actual.args) == len(paramType.args) {
		for i := range paramType.args {
			a.inferTypeBinding(paramType.args[i], actual.args[i], paramSet, bindings, name)
		}
	}
}

func (a *Analyzer) callArgumentsCompatible(args, parameters []typeInfo, minArgs int) bool {
	if len(args) < minArgs || len(args) > len(parameters) {
		return false
	}
	for i, arg := range args {
		if !parameters[i].known || !arg.known {
			return false
		}
		if arg.name == "null" {
			if !parameters[i].nullable {
				return false
			}
			continue
		}
		if !parameters[i].nullable && arg.nullable {
			return false
		}
		if parameters[i].name != "any" && !a.isAssignable(parameters[i], arg) {
			return false
		}
	}
	return true
}

func (a *Analyzer) returnTypeCompatible(expected, actual typeInfo) bool {
	if expected.name == "any" {
		return true
	}
	if !actual.known {
		return expected.nullable
	}
	if !expected.nullable && actual.nullable {
		return false
	}
	return a.isAssignable(expected, actual)
}

func (a *Analyzer) analyzeExpression(expr ast.Expression) {
	switch expr := expr.(type) {
	case *ast.Identifier:
		a.checkBindingNotDestroyed(expr)
	case *ast.AssignmentExpression:
		a.analyzeAssignment(expr)
	case *ast.InfixExpression:
		a.analyzeExpression(expr.Left)
		a.analyzeExpression(expr.Right)
	case *ast.PrefixExpression:
		a.analyzeExpression(expr.Right)
	case *ast.PostfixExpression:
		a.analyzeExpression(expr.Left)
	case *ast.SelectorExpression:
		a.analyzeExpression(expr.Object)
	case *ast.IndexExpression:
		a.analyzeExpression(expr.Left)
		a.analyzeExpression(expr.Index)
	case *ast.CallExpression:
		a.analyzeExpression(expr.Callee)
		for _, arg := range expr.Arguments {
			a.analyzeExpression(arg.Value)
		}
		a.checkTypedCollectionMethodCall(expr)
		a.checkPrimitiveMethodCall(expr)
		a.checkClassMethodCall(expr)
		if ident, ok := expr.Callee.(*ast.Identifier); ok {
			if overloads := a.functions[strings.ToLower(ident.Value)]; len(overloads) > 0 {
				a.checkGenericCallInference(expr, ident.Value, overloads)
			}
		}
	case *ast.ListLiteral:
		for _, element := range expr.Elements {
			a.analyzeExpression(element)
		}
	case *ast.DictLiteral:
		for _, entry := range expr.Entries {
			a.analyzeExpression(entry.Key)
			a.analyzeExpression(entry.Value)
		}
	case *ast.SetLiteral:
		for _, element := range expr.Elements {
			a.analyzeExpression(element)
		}
	}
}

// checkBindingNotDestroyed emits a diagnostic when the identifier
// resolves to a binding that has been retired by `del`.
func (a *Analyzer) checkBindingNotDestroyed(ident *ast.Identifier) {
	if ident == nil {
		return
	}
	info, ok := a.lookup(ident.Value)
	if !ok || !info.destroyed {
		return
	}
	a.errorAt(ident.Token, "use of destroyed binding %q", ident.Value)
}

// markBindingDestroyed walks scopes inner-to-outer to find the
// binding for `name` and flips its destroyed flag. Called by the
// `del x` statement's analyzer hook.
// isDeclarationName reports whether name binds a class, function, enum, or
// interface declaration (vs a variable). Class/enum/interface declarations
// bind a typeInfo whose name equals the declared name.
func (a *Analyzer) isDeclarationName(name string, info typeInfo) bool {
	if _, ok := a.functions[name]; ok {
		return true
	}
	if _, ok := a.classes[name]; ok {
		return true
	}
	if _, ok := a.interfaces[name]; ok {
		return true
	}
	return info.name == name
}

func (a *Analyzer) markBindingDestroyed(name string) bool {
	for i := len(a.scopes) - 1; i >= 0; i-- {
		if entry, ok := a.scopes[i][name]; ok {
			entry.destroyed = true
			a.scopes[i][name] = entry
			return true
		}
	}
	return false
}

func (a *Analyzer) analyzeAssignment(expr *ast.AssignmentExpression) {
	a.analyzeExpression(expr.Value)
	ident, ok := expr.Left.(*ast.Identifier)
	if !ok {
		return
	}
	a.checkBindingNotDestroyed(ident)
	target, ok := a.lookup(ident.Value)
	if !ok || !target.known {
		return
	}
	actual := a.expressionTypeName(expr.Value)
	if !actual.known {
		return
	}
	if actual.name == "null" {
		if !target.nullable {
			a.errorf("cannot assign null to %s %s", target.name, ident.Value)
		}
		return
	}
	if target.name != "any" && !isNumericLiteralWidening(target.name, expr.Value) && !a.isAssignable(target, actual) {
		a.errorf("cannot assign %s to %s %s", actual.display(), target.display(), ident.Value)
	}
}

func isNumericLiteralWidening(target string, expr ast.Expression) bool {
	_, ok := expr.(*ast.IntegerLiteral)
	return ok && (target == "decimal" || target == "float")
}

// narrowingsForCondition returns (consequenceNarrowing, alternativeNarrowing).
// consequenceNarrowing applies when the condition is true; alternativeNarrowing when false.
func (a *Analyzer) narrowingsForCondition(expr ast.Expression) (map[string]typeInfo, map[string]typeInfo) {
	if expr == nil {
		return nil, nil
	}
	infix, ok := expr.(*ast.InfixExpression)
	if !ok {
		return nil, nil
	}
	switch infix.Operator {
	case "&&":
		// Both sides must be true → merge both consequence narrowings.
		consL, _ := a.narrowingsForCondition(infix.Left)
		consR, _ := a.narrowingsForCondition(infix.Right)
		return mergeNarrowings(consL, consR), nil
	case "||":
		// Either side may be false → merge both alternative narrowings.
		_, altL := a.narrowingsForCondition(infix.Left)
		_, altR := a.narrowingsForCondition(infix.Right)
		return nil, mergeNarrowings(altL, altR)
	case "instanceof":
		// if (x instanceof SomeClass) narrows x to SomeClass in the consequence.
		if ident, ok := infix.Left.(*ast.Identifier); ok {
			if typeIdent, ok := infix.Right.(*ast.Identifier); ok {
				narrowed := typeInfo{name: typeIdent.Value, known: true}
				return map[string]typeInfo{ident.Value: narrowed}, nil
			}
		}
		return nil, nil
	case "!=", "==":
		name, ok := nullComparedIdentifier(infix.Left, infix.Right)
		if !ok {
			return nil, nil
		}
		typ, ok := a.lookup(name)
		if !ok || !typ.known || !typ.nullable || typ.name == "null" {
			return nil, nil
		}
		nonNull := typ
		nonNull.nullable = false
		if infix.Operator == "!=" {
			return map[string]typeInfo{name: nonNull}, nil
		}
		return nil, map[string]typeInfo{name: nonNull}
	}
	return nil, nil
}

func mergeNarrowings(a, b map[string]typeInfo) map[string]typeInfo {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]typeInfo, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// blockAlwaysExits reports whether every execution path through block ends with
// an unconditional exit (return, throw, break, continue).
func (a *Analyzer) blockAlwaysExits(block *ast.BlockStatement) bool {
	if block == nil || len(block.Statements) == 0 {
		return false
	}
	for _, stmt := range block.Statements {
		if stmtAlwaysExits(stmt) {
			return true
		}
	}
	return false
}

func stmtAlwaysExits(stmt ast.Statement) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStatement:
		return true
	case *ast.SimpleStatement:
		return s.Kind == "throw" || s.Kind == "break" || s.Kind == "continue"
	case *ast.IfStatement:
		// An if-else where both branches always exit also always exits.
		if s.Alternative != nil && len(s.ElseIfs) == 0 {
			return blockAlwaysExitsStatic(s.Consequence) && blockAlwaysExitsStatic(s.Alternative)
		}
		return false
	}
	return false
}

func blockAlwaysExitsStatic(block *ast.BlockStatement) bool {
	if block == nil || len(block.Statements) == 0 {
		return false
	}
	for _, stmt := range block.Statements {
		if stmtAlwaysExits(stmt) {
			return true
		}
	}
	return false
}

func nullComparedIdentifier(left, right ast.Expression) (string, bool) {
	if ident, ok := left.(*ast.Identifier); ok && isNullLiteral(right) {
		return ident.Value, true
	}
	if ident, ok := right.(*ast.Identifier); ok && isNullLiteral(left) {
		return ident.Value, true
	}
	return "", false
}

func isNullLiteral(expr ast.Expression) bool {
	literal, ok := expr.(*ast.Literal)
	return ok && literal.Value == nil
}

func (a *Analyzer) expressionTypeName(expr ast.Expression) typeInfo {
	switch expr := expr.(type) {
	case *ast.IntegerLiteral:
		return typeInfo{name: "int", known: true}
	case *ast.DecimalLiteral:
		return typeInfo{name: "decimal", known: true}
	case *ast.FloatLiteral:
		return typeInfo{name: "float", known: true}
	case *ast.StringLiteral:
		return typeInfo{name: "string", known: true}
	case *ast.ListLiteral:
		return a.collectionLiteralType("list", expr.Elements)
	case *ast.SetLiteral:
		return a.collectionLiteralType("set", expr.Elements)
	case *ast.DictLiteral:
		return a.dictLiteralType(expr)
	case *ast.Identifier:
		if value, ok := a.lookup(expr.Value); ok {
			return value
		}
		return typeInfo{}
	case *ast.CallExpression:
		if ident, ok := expr.Callee.(*ast.Identifier); ok {
			if _, ok := a.classes[ident.Value]; ok {
				info := typeInfo{name: ident.Value, known: true}
				if len(expr.TypeArguments) > 0 {
					info.args = make([]typeInfo, 0, len(expr.TypeArguments))
					for _, arg := range expr.TypeArguments {
						info.args = append(info.args, a.typeInfoFromRef(arg))
					}
				}
				return info
			}
		}
		return typeInfo{}
	case *ast.Literal:
		switch expr.Value.(type) {
		case bool:
			return typeInfo{name: "bool", known: true}
		case nil:
			return typeInfo{name: "null", nullable: true, known: true}
		default:
			return typeInfo{}
		}
	default:
		return typeInfo{}
	}
}

func (a *Analyzer) isAssignableType(target, actual string) bool {
	if target == actual {
		return true
	}
	if isCallableTypeName(target) {
		return a.typeHasInvoke(actual)
	}
	if isGeneratorTypeName(target) && isGeneratorTypeName(actual) {
		return true
	}
	if _, ok := a.interfaces[target]; ok {
		return a.classImplements(actual, target) || a.interfaceExtends(actual, target)
	}
	if _, ok := a.classes[target]; ok {
		return a.classExtends(actual, target)
	}
	return false
}

func isCallableTypeName(name string) bool {
	return strings.EqualFold(name, "callable") || strings.EqualFold(name, "func") || strings.EqualFold(name, "function")
}

func isGeneratorTypeName(name string) bool {
	return strings.EqualFold(name, "generator") || strings.EqualFold(name, "iterable")
}

func (a *Analyzer) typeHasInvoke(name string) bool {
	if isCallableTypeName(name) {
		return true
	}
	return len(a.classMethods(name, "__invoke")) > 0
}

func (a *Analyzer) classExtends(actual, target string) bool {
	for current, ok := a.classes[actual]; ok && current.parent != ""; current, ok = a.classes[current.parent] {
		if current.parent == target {
			return true
		}
	}
	return false
}

func (a *Analyzer) classImplements(className, ifaceName string) bool {
	for current, ok := a.classes[className]; ok; current, ok = a.classes[current.parent] {
		for _, implemented := range current.implements {
			if implemented == ifaceName || a.interfaceExtends(implemented, ifaceName) {
				return true
			}
		}
		if current.parent == "" {
			break
		}
	}
	return false
}

func (a *Analyzer) interfaceExtends(actual, target string) bool {
	if actual == target {
		return true
	}
	info, ok := a.interfaces[actual]
	if !ok {
		return false
	}
	for _, parent := range info.parents {
		if parent == target || a.interfaceExtends(parent, target) {
			return true
		}
	}
	return false
}

func (a *Analyzer) interfaceMethods(name string, seen map[string]bool) map[string][]methodInfo {
	if seen[name] {
		return map[string][]methodInfo{}
	}
	seen[name] = true
	info, ok := a.interfaces[name]
	if !ok {
		return map[string][]methodInfo{}
	}
	methods := map[string][]methodInfo{}
	for _, parent := range info.parents {
		for name, overloads := range a.interfaceMethods(parent, seen) {
			methods[name] = append(methods[name], overloads...)
		}
	}
	for name, overloads := range info.methods {
		methods[name] = append(methods[name], overloads...)
	}
	return methods
}

func (a *Analyzer) classMethods(className, methodName string) []methodInfo {
	key := strings.ToLower(methodName)
	for current, ok := a.classes[className]; ok; current, ok = a.classes[current.parent] {
		if methods, ok := current.methods[key]; ok {
			return methods
		}
		if current.parent == "" {
			break
		}
	}
	return nil
}

func (a *Analyzer) classHasCompatibleMethod(className, methodName string, expected methodInfo) bool {
	for _, actual := range a.classMethods(className, methodName) {
		if a.methodCompatible(expected, actual) {
			return true
		}
	}
	return false
}

func (a *Analyzer) methodCompatible(expected, actual methodInfo) bool {
	if len(expected.parameters) != len(actual.parameters) {
		return false
	}
	for i := range expected.parameters {
		if !sameTypeInfo(expected.parameters[i], actual.parameters[i]) {
			return false
		}
	}
	if !expected.returnType.known {
		return true
	}
	if !actual.returnType.known {
		return false
	}
	if expected.returnType.nullable != actual.returnType.nullable {
		return false
	}
	return a.isAssignableType(expected.returnType.name, actual.returnType.name)
}

func (a *Analyzer) methodInfoFromFunction(fn *ast.FunctionStatement) methodInfo {
	info := methodInfo{name: fn.Name.Value, returnType: a.typeInfoFromRef(fn.ReturnType)}
	info.minArgs = len(fn.Parameters)
	for i, param := range fn.Parameters {
		info.parameters = append(info.parameters, a.typeInfoFromRef(param.Type))
		if param.Default != nil && i < info.minArgs {
			info.minArgs = i
		}
	}
	for _, generic := range fn.Generics {
		info.typeParams = append(info.typeParams, typeParam{
			name:       generic.Name.Value,
			constraint: a.typeInfoFromRef(generic.Constraint),
		})
	}
	return info
}

func (a *Analyzer) methodInfoFromSignature(sig *ast.FunctionSignature) methodInfo {
	info := methodInfo{name: sig.Name.Value, returnType: a.typeInfoFromRef(sig.ReturnType)}
	info.minArgs = len(sig.Parameters)
	for i, param := range sig.Parameters {
		info.parameters = append(info.parameters, a.typeInfoFromRef(param.Type))
		if param.Default != nil && i < info.minArgs {
			info.minArgs = i
		}
	}
	for _, generic := range sig.Generics {
		info.typeParams = append(info.typeParams, typeParam{
			name:       generic.Name.Value,
			constraint: a.typeInfoFromRef(generic.Constraint),
		})
	}
	return info
}

func (m methodInfo) signatureKey() string {
	params := make([]string, 0, len(m.parameters))
	for _, param := range m.parameters {
		params = append(params, param.display())
	}
	return fmt.Sprintf("(%s):%s", strings.Join(params, ","), m.returnType.display())
}

func sameTypeInfo(left, right typeInfo) bool {
	if !left.known || !right.known {
		return !left.known && !right.known
	}
	if left.name != right.name || left.nullable != right.nullable {
		return false
	}
	if len(left.args) != len(right.args) {
		return false
	}
	for i := range left.args {
		if !sameTypeInfo(left.args[i], right.args[i]) {
			return false
		}
	}
	return true
}

// isAssignable extends isAssignableType with generic-argument checking. The
// element rule matches the runtime: built-in collections (list/set/dict) are
// COVARIANT in their element types (`list<Dog>` is assignable to `list<Animal>`,
// `list<int>` to `list<any>`), while user generic classes stay INVARIANT (each
// argument must be the exact same type). Covariant collections are technically
// unsound with mutation, but the runtime allows it, so the analyzer matches to
// avoid false positives.
//
// When either side carries no type arguments (a raw instance whose reified
// bindings are still polymorphic), the check passes - argument checking only
// applies when both sides explicitly carry args.
func (a *Analyzer) isAssignable(target, actual typeInfo) bool {
	if !a.isAssignableType(target.name, actual.name) {
		return false
	}
	if len(target.args) == 0 || len(actual.args) == 0 {
		return true
	}
	if len(target.args) != len(actual.args) {
		return false
	}
	covariant := isCollectionTypeName(target.name)
	for i := range target.args {
		if covariant {
			if !a.elementAssignable(target.args[i], actual.args[i]) {
				return false
			}
		} else if !sameTypeInfo(target.args[i], actual.args[i]) {
			return false
		}
	}
	return true
}

func isCollectionTypeName(name string) bool {
	switch name {
	case "list", "set", "dict":
		return true
	}
	return false
}

// elementAssignable is the covariant rule for collection element/value types:
// `any` on either side is permissive, a subtype is assignable to its supertype,
// nested collections recurse covariantly, but two unrelated concrete types
// (including numeric widening like int->float) are rejected. An unresolved type
// parameter stays permissive so generic code does not false-positive.
func (a *Analyzer) elementAssignable(target, actual typeInfo) bool {
	if !target.known || !actual.known {
		return true
	}
	if target.name == "any" || actual.name == "any" {
		return true
	}
	if target.name == actual.name {
		if len(target.args) == 0 || len(actual.args) == 0 {
			return true
		}
		if len(target.args) != len(actual.args) {
			return false
		}
		covariant := isCollectionTypeName(target.name)
		for i := range target.args {
			if covariant {
				if !a.elementAssignable(target.args[i], actual.args[i]) {
					return false
				}
			} else if !sameTypeInfo(target.args[i], actual.args[i]) {
				return false
			}
		}
		return true
	}
	if a.isAssignableType(target.name, actual.name) {
		return true
	}
	if a.isConcreteTypeName(target.name) && a.isConcreteTypeName(actual.name) {
		return false
	}
	return true
}

func (a *Analyzer) isConcreteTypeName(name string) bool {
	switch name {
	case "int", "float", "decimal", "string", "bool", "bytes", "list", "set", "dict":
		return true
	}
	if _, ok := a.classes[name]; ok {
		return true
	}
	if _, ok := a.interfaces[name]; ok {
		return true
	}
	return false
}

// collectionLiteralType infers list<T>/set<T> when every element shares one
// known type; otherwise the bare collection type so element checks stay lenient.
func (a *Analyzer) collectionLiteralType(name string, elements []ast.Expression) typeInfo {
	elem, ok := a.homogeneousElementType(elements)
	if !ok {
		return typeInfo{name: name, known: true}
	}
	return typeInfo{name: name, known: true, args: []typeInfo{elem}}
}

func (a *Analyzer) dictLiteralType(lit *ast.DictLiteral) typeInfo {
	if len(lit.Entries) == 0 {
		return typeInfo{name: "dict", known: true}
	}
	keys := make([]ast.Expression, 0, len(lit.Entries))
	vals := make([]ast.Expression, 0, len(lit.Entries))
	for _, entry := range lit.Entries {
		if entry.Spread {
			return typeInfo{name: "dict", known: true}
		}
		keys = append(keys, entry.Key)
		vals = append(vals, entry.Value)
	}
	k, kok := a.homogeneousElementType(keys)
	v, vok := a.homogeneousElementType(vals)
	if !kok || !vok {
		return typeInfo{name: "dict", known: true}
	}
	return typeInfo{name: "dict", known: true, args: []typeInfo{k, v}}
}

// homogeneousElementType returns the common element type when every element is
// known and shares the exact same type; otherwise (empty, mixed, or any
// unknown) it reports not-ok so downstream element checks stay lenient.
func (a *Analyzer) homogeneousElementType(elements []ast.Expression) (typeInfo, bool) {
	if len(elements) == 0 {
		return typeInfo{}, false
	}
	var common typeInfo
	for i, el := range elements {
		t := a.expressionTypeName(el)
		if !t.known {
			return typeInfo{}, false
		}
		if i == 0 {
			common = t
			continue
		}
		if !sameTypeInfo(common, t) {
			return typeInfo{}, false
		}
	}
	return common, true
}

func (a *Analyzer) typeInfoFromRef(ref *ast.TypeRef) typeInfo {
	if ref == nil || ref.Operator != "" {
		return typeInfo{}
	}
	var args []typeInfo
	if len(ref.Arguments) > 0 {
		args = make([]typeInfo, 0, len(ref.Arguments))
		for _, arg := range ref.Arguments {
			args = append(args, a.typeInfoFromRef(arg))
		}
	}
	if ref.ListAlias {
		// `T[]` is shorthand for `list<T>` - record the element type so element
		// checks downstream behave identically to `list<T>`.
		var elemArgs []typeInfo
		if args == nil {
			elemArgs = []typeInfo{{name: ref.Name, nullable: ref.Nullable, known: true}}
		} else {
			elemArgs = args
		}
		return typeInfo{name: "list", nullable: ref.Nullable, known: true, args: elemArgs}
	}
	if alias, ok := a.aliases[strings.ToLower(ref.Name)]; ok {
		alias.nullable = alias.nullable || ref.Nullable
		return alias
	}
	return typeInfo{name: ref.Name, nullable: ref.Nullable, known: true, args: args}
}

func (t typeInfo) display() string {
	if !t.known {
		return "unknown"
	}
	name := t.name
	if len(t.args) > 0 {
		parts := make([]string, 0, len(t.args))
		for _, arg := range t.args {
			parts = append(parts, arg.display())
		}
		name = name + "<" + strings.Join(parts, ", ") + ">"
	}
	if t.nullable && t.name != "null" {
		return "?" + name
	}
	return name
}

func (a *Analyzer) pushScope() {
	a.scopes = append(a.scopes, map[string]typeInfo{})
}

func (a *Analyzer) popScope() {
	if len(a.scopes) > 1 {
		a.scopes = a.scopes[:len(a.scopes)-1]
	}
}

func (a *Analyzer) declare(name string, typ typeInfo) {
	if len(a.scopes) == 0 {
		a.scopes = []map[string]typeInfo{{}}
	}
	a.scopes[len(a.scopes)-1][name] = typ
}

func (a *Analyzer) lookup(name string) (typeInfo, bool) {
	for i := len(a.scopes) - 1; i >= 0; i-- {
		if value, ok := a.scopes[i][name]; ok {
			return value, true
		}
	}
	return typeInfo{}, false
}

// errorf records a semantic diagnostic without a known source position.
// Prefer errorAt when a token is in scope - the LSP layer reports
// position-less diagnostics at (1, 1) as a fallback, which is rarely
// where the actual problem is.
func (a *Analyzer) errorf(format string, args ...any) {
	a.diagnostics = append(a.diagnostics, Diagnostic{Message: fmt.Sprintf(format, args...)})
}

// errorAt records a semantic diagnostic anchored at the position of
// the given token. A zero token (Line == 0) falls back to the
// position-less behaviour of errorf. Defaults to SeverityError.
func (a *Analyzer) errorAt(tok token.Token, format string, args ...any) {
	a.diagnostics = append(a.diagnostics, Diagnostic{
		Message:  fmt.Sprintf(format, args...),
		Line:     tok.Line,
		Column:   tok.Column,
		Severity: SeverityError,
	})
}

// warningAt is the Warning counterpart of errorAt. Warnings print to
// stderr but do not block execution in `geblang run`; they show up
// as severity=2 in the LSP and `geblang check` output. Use this for
// checks where the runtime can sometimes prove the analyzer wrong,
// or for stylistic findings that shouldn't fail a build.
func (a *Analyzer) warningAt(tok token.Token, format string, args ...any) {
	a.diagnostics = append(a.diagnostics, Diagnostic{
		Message:  fmt.Sprintf(format, args...),
		Line:     tok.Line,
		Column:   tok.Column,
		Severity: SeverityWarning,
	})
}

// tokenOfExpression returns the leading token of an AST expression,
// or a zero Token if the type isn't recognised. The Expression
// interface intentionally doesn't expose a Token() method; this
// helper centralises the type switch used for diagnostic anchoring.
func tokenOfExpression(expr ast.Expression) token.Token {
	switch e := expr.(type) {
	case *ast.Identifier:
		return e.Token
	case *ast.IntegerLiteral:
		return e.Token
	case *ast.FloatLiteral:
		return e.Token
	case *ast.DecimalLiteral:
		return e.Token
	case *ast.StringLiteral:
		return e.Token
	case *ast.PrefixExpression:
		return e.Token
	case *ast.InfixExpression:
		return e.Token
	case *ast.PostfixExpression:
		return e.Token
	case *ast.AssignmentExpression:
		return e.Token
	case *ast.CallExpression:
		return tokenOfExpression(e.Callee)
	case *ast.SelectorExpression:
		return tokenOfExpression(e.Object)
	case *ast.IndexExpression:
		return tokenOfExpression(e.Left)
	case *ast.ListLiteral:
		return e.Token
	case *ast.DictLiteral:
		return e.Token
	case *ast.SetLiteral:
		return e.Token
	case *ast.FunctionLiteral:
		return e.Token
	case *ast.CastExpression:
		return e.Token
	case *ast.TernaryExpression:
		return e.Token
	}
	return token.Token{}
}
