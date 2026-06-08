package bytecode

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"geblang/internal/ast"
	"geblang/internal/desugar"
	"geblang/internal/native"
	"geblang/internal/runtime"
)

type Compiler struct {
	chunk           Chunk
	loops           []loopContext
	globals         map[string]int64
	globalTypes     map[string]string
	globalDeclKinds map[string]globalDecl
	deletedGlobals  map[string]bool
	declaredDecls   map[string]bool // case-sensitive class/func/enum/interface names
	scopes          []map[string]binding
	// locals is the next slot index for the function currently being
	// compiled. It resets to zero on every function boundary so that
	// slot numbers are relative to the function's own frame rather than
	// accumulating across the whole chunk. The previous value is pushed
	// onto localsStack at function entry and restored at exit. At any
	// point in compilation it doubles as the high-water mark (slots are
	// never reclaimed on scope exit).
	locals          int64
	localsStack     []int64
	funcs           map[string][]int64
	functionCursors map[string]int
	classes         map[string]int64
	interfaces      map[string]int64
	interfaceAST    map[string]*ast.InterfaceStatement
	enums           map[string]int64
	typeAliases     map[string]*ast.TypeRef
	inFunc          int
	classStack      []int64
	finalizers      []finalizerContext
	expectedTypes   []string
	returnTypes     []string
	reflectFuncs    map[string]runtime.DecoratorTarget
	reflectClasses  map[string]runtime.DecoratorTarget
	reflectMethods  map[string]map[string]runtime.DecoratorTarget
	reflectStatics  map[string]map[string]runtime.DecoratorTarget
	// moduleAliases maps an `import X as Y` alias name to its canonical
	// dotted module path. Populated for every native (bytecode-callable)
	// import so module-recognition sites can translate `Y.fn(...)` calls
	// back to the canonical `X.fn` dispatch.
	moduleAliases map[string]string
	// fromImports: from-import local/alias name -> fully-qualified class name.
	fromImports map[string]string
	// AssertionsDisabled elides assert(...) call sites at compile time
	// (no code emitted; arguments are not evaluated). Set via the
	// --no-assert CLI flag on `geblang` and `geblang build`.
	AssertionsDisabled bool
}

type binding struct {
	kind string
	slot int64
	typ  string
}

type loopContext struct {
	continueTarget int
	continueJumps  []int
	breakJumps     []int
	iterSlot       int64
	hasIterSlot    bool
}

type finalizerContext struct {
	body           *ast.BlockStatement
	popHandler     bool
	iterSlot       int64
	hasIterSlot    bool
	withSlot       int64
	hasWithCleanup bool
}

// AssertionsDisabled, when set true before Compile (typically by the
// --no-assert CLI flag), elides every assert(...) call at compile time:
// neither the condition nor the message is evaluated. Off by default.
var AssertionsDisabled bool

func Compile(program *ast.Program, source []byte, compilerVersion string) (Chunk, error) {
	if err := desugar.Dataclasses(program); err != nil {
		return Chunk{}, err
	}
	if err := desugar.Memoize(program); err != nil {
		return Chunk{}, err
	}
	c := &Compiler{
		chunk: Chunk{
			SourceHash: SourceHash(source),
			Compiler:   compilerVersion,
		},
		AssertionsDisabled: AssertionsDisabled,
		globals:            map[string]int64{},
		globalTypes:        map[string]string{},
		globalDeclKinds:    map[string]globalDecl{},
		deletedGlobals:     map[string]bool{},
		declaredDecls:      map[string]bool{},
		scopes:             []map[string]binding{{}},
		funcs:              map[string][]int64{},
		functionCursors:    map[string]int{},
		classes:            map[string]int64{},
		interfaces:         map[string]int64{},
		interfaceAST:       map[string]*ast.InterfaceStatement{},
		enums:              map[string]int64{},
		typeAliases:        map[string]*ast.TypeRef{},
		reflectFuncs:       map[string]runtime.DecoratorTarget{},
		reflectClasses:     map[string]runtime.DecoratorTarget{},
		reflectMethods:     map[string]map[string]runtime.DecoratorTarget{},
		reflectStatics:     map[string]map[string]runtime.DecoratorTarget{},
		moduleAliases:      map[string]string{},
		fromImports:        map[string]string{},
	}
	// A top-level `del name` removes the binding, so a later same-name
	// declaration is a legal re-bind (the evaluator allows it at runtime).
	// Exempt del'd names from the redeclaration check rather than model
	// control flow; erring toward accept never rejects valid code.
	for _, stmt := range program.Statements {
		if del, ok := stmt.(*ast.DelStatement); ok && del.Target != nil {
			c.deletedGlobals[del.Target.Value] = true
		}
	}
	for _, stmt := range program.Statements {
		if export, ok := stmt.(*ast.ExportStatement); ok {
			stmt = export.Statement
		}
		if alias, ok := stmt.(*ast.TypeAliasStatement); ok {
			c.declareTypeAlias(alias)
		}
		if from, ok := stmt.(*ast.FromImportStatement); ok {
			canonical := strings.Join(from.Path, ".")
			for _, n := range from.Names {
				// A from-imported name is immutable: it cannot be locally
				// redeclared or overloaded. Re-importing the same symbol is idempotent.
				if local := n.Local(); local != "" && n.Name != nil {
					if msg := c.claimGlobalKind(local, "import", canonical+"."+n.Name.Value); msg != "" {
						return Chunk{}, fmt.Errorf("line %d:%d: %s", from.Token.Line, from.Token.Column, msg)
					}
					c.fromImports[local] = canonical + "." + n.Name.Value
				}
			}
		}
		if fn, ok := stmt.(*ast.FunctionStatement); ok {
			c.declaredDecls[fn.Name.Value] = true
			if msg := c.claimGlobalKind(fn.Name.Value, "func", ""); msg != "" {
				return Chunk{}, fmt.Errorf("line %d:%d: %s", fn.Token.Line, fn.Token.Column, msg)
			}
			index := c.declareFunction(fn.Name.Value)
			/* Forward-call resolution: populate the function's
			 * signature (param names/types, return type, defaults,
			 * variadic flag) up front so a body compiled before
			 * this declaration can still match the call site
			 * against a real signature instead of skipping the
			 * "uninitialised" candidate. The body itself is filled
			 * in by compileFunctionStatement on the second pass. */
			c.populateFunctionSignature(index, fn)
			key := strings.ToLower(fn.Name.Value)
			meta, err := reflectFunctionMetadataFromStatement(c, fn, "function", int64(len(c.funcs[key])-1))
			if err != nil {
				return Chunk{}, err
			}
			c.reflectFuncs[key] = appendReflectFunctionTarget(c.reflectFuncs[key], meta)
		}
		if class, ok := stmt.(*ast.ClassStatement); ok {
			c.declaredDecls[class.Name.Value] = true
			if msg := c.claimGlobalKind(class.Name.Value, "class", ""); msg != "" {
				return Chunk{}, fmt.Errorf("line %d:%d: %s", class.Token.Line, class.Token.Column, msg)
			}
			c.declareClass(class.Name.Value)
			classKey := strings.ToLower(class.Name.Value)
			classDec, err := decoratorsMetadata(class.Decorators, "class", 0)
			if err != nil {
				return Chunk{}, err
			}
			c.reflectClasses[classKey] = runtime.DecoratorTarget{Target: "class", Decorators: classDec, Class: reflectClassMetadataFromStatement(class)}
			for _, member := range class.Members {
				fn, ok := member.(*ast.FunctionStatement)
				if !ok {
					continue
				}
				methodKey := strings.ToLower(fn.Name.Value)
				if fn.Static {
					if c.reflectStatics[classKey] == nil {
						c.reflectStatics[classKey] = map[string]runtime.DecoratorTarget{}
					}
					target := c.reflectStatics[classKey][methodKey]
					meta, err := reflectFunctionMetadataFromStatement(c, fn, "staticMethod", int64(reflectFunctionTargetOverloadCount(target)))
					if err != nil {
						return Chunk{}, err
					}
					c.reflectStatics[classKey][methodKey] = appendReflectFunctionTarget(target, meta)
				} else {
					if c.reflectMethods[classKey] == nil {
						c.reflectMethods[classKey] = map[string]runtime.DecoratorTarget{}
					}
					target := c.reflectMethods[classKey][methodKey]
					meta, err := reflectFunctionMetadataFromStatement(c, fn, "method", int64(reflectFunctionTargetOverloadCount(target)))
					if err != nil {
						return Chunk{}, err
					}
					c.reflectMethods[classKey][methodKey] = appendReflectFunctionTarget(target, meta)
				}
			}
		}
		if iface, ok := stmt.(*ast.InterfaceStatement); ok {
			c.declaredDecls[iface.Name.Value] = true
			if msg := c.claimGlobalKind(iface.Name.Value, "interface", ""); msg != "" {
				return Chunk{}, fmt.Errorf("line %d:%d: %s", iface.Token.Line, iface.Token.Column, msg)
			}
			c.declareInterface(iface.Name.Value)
		}
		if enum, ok := stmt.(*ast.EnumStatement); ok {
			c.declaredDecls[enum.Name.Value] = true
			if msg := c.claimGlobalKind(enum.Name.Value, "enum", ""); msg != "" {
				return Chunk{}, fmt.Errorf("line %d:%d: %s", enum.Token.Line, enum.Token.Column, msg)
			}
			c.declareEnum(enum)
		}
		if decl, ok := stmt.(*ast.DeclarationStatement); ok && decl.Name != nil {
			if msg := c.claimGlobalKind(decl.Name.Value, "var", ""); msg != "" {
				return Chunk{}, fmt.Errorf("line %d:%d: %s", decl.Token.Line, decl.Token.Column, msg)
			}
		}
		if imp, ok := stmt.(*ast.ImportStatement); ok {
			if alias := imp.ModuleName(); alias != "" {
				if msg := c.claimGlobalKind(alias, "import", strings.Join(imp.Path, ".")); msg != "" {
					return Chunk{}, fmt.Errorf("line %d:%d: %s", imp.Token.Line, imp.Token.Column, msg)
				}
			}
		}
	}
	for _, stmt := range program.Statements {
		if err := c.compileStatement(stmt); err != nil {
			return Chunk{}, c.withStatementLocation(stmt, err)
		}
	}
	c.emit(OpReturn)
	c.chunk.TopLevelLocalCount = c.locals
	c.chunk.GlobalCount = int64(len(c.globals))
	c.chunk.consolidateOperands()
	return c.chunk, nil
}

func reflectFunctionMetadataFromStatement(c *Compiler, stmt *ast.FunctionStatement, target string, overload int64) (runtime.FunctionMetadata, error) {
	parameters := make([]runtime.ParameterMetadata, 0, len(stmt.Parameters))
	for _, param := range stmt.Parameters {
		name := ""
		if param.Name != nil {
			name = param.Name.Value
		}
		paramDecs, err := decoratorsMetadata(param.Decorators, "parameter", 0)
		if err != nil {
			return runtime.FunctionMetadata{}, err
		}
		parameters = append(parameters, runtime.ParameterMetadata{
			Name:       name,
			Type:       c.bytecodeTypeName(param.Type),
			Variadic:   param.Variadic,
			HasDefault: param.Default != nil,
			Decorators: paramDecs,
		})
	}
	dec, err := decoratorsMetadata(stmt.Decorators, target, overload)
	if err != nil {
		return runtime.FunctionMetadata{}, err
	}
	return runtime.FunctionMetadata{
		Name:       stmt.Name.Value,
		Target:     target,
		Doc:        stmt.Doc,
		Parameters: parameters,
		ReturnType: c.bytecodeReturnType(stmt.ReturnType),
		Async:      stmt.Async,
		Variadic:   len(stmt.Parameters) > 0 && stmt.Parameters[len(stmt.Parameters)-1].Variadic,
		Decorators: dec,
		DefLine:    int64(stmt.Token.Line),
		DefColumn:  int64(stmt.Token.Column),
	}, nil
}

func reflectFunctionMetadataFromSignature(c *Compiler, sig *ast.FunctionSignature) runtime.FunctionMetadata {
	parameters := make([]runtime.ParameterMetadata, 0, len(sig.Parameters))
	for _, param := range sig.Parameters {
		name := ""
		if param.Name != nil {
			name = param.Name.Value
		}
		paramDecs, _ := decoratorsMetadata(param.Decorators, "parameter", 0)
		parameters = append(parameters, runtime.ParameterMetadata{
			Name:       name,
			Type:       c.bytecodeTypeName(param.Type),
			Variadic:   param.Variadic,
			HasDefault: param.Default != nil,
			Decorators: paramDecs,
		})
	}
	name := ""
	if sig.Name != nil {
		name = sig.Name.Value
	}
	return runtime.FunctionMetadata{
		Name:       name,
		Target:     "interfaceMethod",
		Doc:        sig.Doc,
		Parameters: parameters,
		ReturnType: c.bytecodeReturnType(sig.ReturnType),
		Variadic:   len(sig.Parameters) > 0 && sig.Parameters[len(sig.Parameters)-1].Variadic,
	}
}

func reflectClassMetadataFromStatement(stmt *ast.ClassStatement) *runtime.ClassMetadata {
	metadata := &runtime.ClassMetadata{
		Name:      stmt.Name.Value,
		Doc:       stmt.Doc,
		DefLine:   int64(stmt.Token.Line),
		DefColumn: int64(stmt.Token.Column),
	}
	if stmt.Extends != nil {
		metadata.Parent = stmt.Extends.Name
	}
	for _, iface := range stmt.Implements {
		metadata.Interfaces = append(metadata.Interfaces, iface.Name)
	}
	methods := map[string]string{}
	staticMethods := map[string]string{}
	for _, member := range stmt.Members {
		switch member := member.(type) {
		case *ast.DeclarationStatement:
			if member.Name != nil && !strings.HasPrefix(member.Kind, "static ") {
				metadata.Fields = append(metadata.Fields, member.Name.Value)
			}
		case *ast.FunctionStatement:
			if member.Name == nil {
				continue
			}
			// A function whose name matches the class name is the
			// constructor - exposed via reflect.constructors rather
			// than reflect.methods. Matches the evaluator and avoids
			// the previous divergence where the VM included the
			// constructor in the methods list.
			if strings.EqualFold(member.Name.Value, stmt.Name.Value) {
				continue
			}
			key := strings.ToLower(member.Name.Value)
			if member.Static {
				staticMethods[key] = member.Name.Value
			} else {
				methods[key] = member.Name.Value
			}
		}
	}
	metadata.Methods = sortedMapValues(methods)
	metadata.StaticMethods = sortedMapValues(staticMethods)
	sort.Strings(metadata.Fields)
	sort.Strings(metadata.Interfaces)
	return metadata
}

func sortedMapValues(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out
}

func appendReflectFunctionTarget(target runtime.DecoratorTarget, fn runtime.FunctionMetadata) runtime.DecoratorTarget {
	if target.Target == "" {
		target.Target = fn.Target
	}
	if target.Function == nil {
		copied := fn
		target.Function = &copied
	} else {
		target.Function.Decorators = append(target.Function.Decorators, fn.Decorators...)
	}
	target.Decorators = append(target.Decorators, fn.Decorators...)
	return target
}

func reflectFunctionTargetOverloadCount(target runtime.DecoratorTarget) int {
	maxOverload := int64(-1)
	for _, decorator := range target.Decorators {
		if decorator.Overload > maxOverload {
			maxOverload = decorator.Overload
		}
	}
	return int(maxOverload + 1)
}

func (c *Compiler) declareNativeInterfacesForImport(path []string) {
	if len(path) != 1 {
		return
	}
	switch path[0] {
	case "log":
		c.declareNativeInterface("log.LogInterface", []runtime.FunctionMetadata{
			{
				Name:   "handle",
				Target: "interfaceMethod",
				Parameters: []runtime.ParameterMetadata{
					{Name: "level"},
					{Name: "message"},
					{Name: "fields"},
				},
			},
		})
	}
}

func (c *Compiler) declareNativeInterface(name string, methods []runtime.FunctionMetadata) {
	index := c.declareInterface(name)
	if index < 0 || int(index) >= len(c.chunk.Interfaces) {
		return
	}
	iface := c.chunk.Interfaces[index]
	iface.Methods = append([]runtime.FunctionMetadata(nil), methods...)
	c.chunk.Interfaces[index] = iface
}

func (c *Compiler) compileStatement(stmt ast.Statement) error {
	switch stmt := stmt.(type) {
	case *ast.ModuleStatement:
		return nil
	case *ast.ImportStatement:
		if isEvaluatorOnlyBuiltinImport(stmt.Path) {
			return c.withStatementLocation(stmt, parityErrorf("bytecode compiler does not support builtin module %s yet", strings.Join(stmt.Path, ".")))
		}
		c.declareNativeInterfacesForImport(stmt.Path)
		if !isBytecodeImportModule(stmt.Path) {
			alias := stmt.ModuleName()
			if alias == "" {
				return fmt.Errorf("empty import path")
			}
			canonical := strings.Join(stmt.Path, ".")
			// Record the alias so resolveQualifiedClassName can rewrite
			// `alias.Class` references (e.g. `extends mymod.Foo`) into
			// their canonical `module.Class` form for cross-module
			// parent-class dispatch.
			c.moduleAliases[alias] = canonical
			canonicalIndex := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: canonical})
			aliasIndex := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: alias})
			c.emitAt(OpImportModule, stmt.Token.Line, stmt.Token.Column, canonicalIndex, aliasIndex, c.globalSlot(alias))
			return nil
		}
		// Native (bytecode-callable) imports: register the alias-to-canonical
		// mapping so later `alias.fn(...)` calls can be dispatched as
		// `canonical.fn(...)` by the same module-recognition path that
		// handles unaliased calls. The unaliased form maps a name to
		// itself, which is a harmless no-op in canonicalModule().
		c.moduleAliases[stmt.ModuleName()] = strings.Join(stmt.Path, ".")
		return nil
	case *ast.FromImportStatement:
		if isEvaluatorOnlyBuiltinImport(stmt.Path) {
			return c.withStatementLocation(stmt, parityErrorf("bytecode compiler does not support builtin module %s yet", strings.Join(stmt.Path, ".")))
		}
		canonical := strings.Join(stmt.Path, ".")
		native := isBytecodeImportModule(stmt.Path)
		canonicalIdx := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: canonical})
		nativeFlag := int64(0)
		if native {
			nativeFlag = 1
		}
		operands := []int64{canonicalIdx, nativeFlag, int64(len(stmt.Names))}
		for _, item := range stmt.Names {
			if item.Name == nil {
				return c.withStatementLocation(stmt, fmt.Errorf("from %s import: empty name", canonical))
			}
			nameIdx := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: item.Name.Value})
			operands = append(operands, nameIdx, int64(c.globalSlot(item.Local())))
		}
		c.emitAt(OpImportFrom, stmt.Token.Line, stmt.Token.Column, operands...)
		return nil
	case *ast.ExportStatement:
		if err := c.compileStatement(stmt.Statement); err != nil {
			return err
		}
		if decl, ok := stmt.Statement.(*ast.DeclarationStatement); ok && decl.Name != nil {
			c.chunk.Exports = append(c.chunk.Exports, ExportInfo{Name: decl.Name.Value, Slot: c.globalSlot(decl.Name.Value), FunctionIndex: -1, ClassIndex: -1, InterfaceIndex: -1})
		}
		if fn, ok := stmt.Statement.(*ast.FunctionStatement); ok && fn.Name != nil {
			functionIndex, err := c.singleFunctionIndex(fn.Name.Value)
			if err != nil {
				return err
			}
			c.chunk.Exports = append(c.chunk.Exports, ExportInfo{Name: fn.Name.Value, Slot: -1, FunctionIndex: functionIndex, ClassIndex: -1, InterfaceIndex: -1})
		}
		if class, ok := stmt.Statement.(*ast.ClassStatement); ok && class.Name != nil {
			c.chunk.Exports = append(c.chunk.Exports, ExportInfo{Name: class.Name.Value, Slot: -1, FunctionIndex: -1, ClassIndex: c.classes[strings.ToLower(class.Name.Value)], InterfaceIndex: -1})
		}
		if iface, ok := stmt.Statement.(*ast.InterfaceStatement); ok && iface.Name != nil {
			c.chunk.Exports = append(c.chunk.Exports, ExportInfo{Name: iface.Name.Value, Slot: -1, FunctionIndex: -1, ClassIndex: -1, InterfaceIndex: c.interfaces[strings.ToLower(iface.Name.Value)]})
		}
		return nil
	case *ast.InitStatement:
		return c.compileBlock(stmt.Body)
	case *ast.TypeAliasStatement:
		c.declareTypeAlias(stmt)
		return nil
	case *ast.DeclarationStatement:
		declType := c.bytecodeTypeName(stmt.Type)
		if declType == "any" && stmt.Value != nil {
			declType = c.expressionStaticType(stmt.Value)
		}
		if stmt.Value == nil {
			c.emitConstant(runtime.Null{}, stmt.Token.Line, stmt.Token.Column)
		} else {
			expected := declType
			if expected == "any" {
				expected = ""
			}
			if err := c.compileExpressionWithExpected(stmt.Value, expected); err != nil {
				return err
			}
		}
		// For generic built-in collection declarations (e.g. list<int>, int[]), emit OpTypeAssert
		// to enforce element types at runtime.
		if stmt.Type != nil && stmt.Type.Operator == "" && stmt.Value != nil {
			baseName := strings.ToLower(stmt.Type.Name)
			isListAlias := stmt.Type.ListAlias && len(stmt.Type.Arguments) == 0
			isGenericCollection := len(stmt.Type.Arguments) > 0 && (baseName == "list" || baseName == "set" || baseName == "dict")
			if isListAlias || isGenericCollection {
				fullTypeName := c.bytecodeTypeNameForParam(stmt.Type, nil)
				typeStrIdx := int64(len(c.chunk.Constants))
				c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: fullTypeName})
				c.emitAt(OpTypeAssert, stmt.Token.Line, stmt.Token.Column, typeStrIdx)
			}
		}
		// When the declared type has explicit type arguments (e.g. Box<int>), emit
		// OpSetTypeBindings so the constructed instance carries the declared bindings
		// rather than ones inferred from the constructor argument types.
		if stmt.Type != nil && stmt.Type.Operator == "" && len(stmt.Type.Arguments) > 0 {
			if classIdx, ok := c.classes[strings.ToLower(stmt.Type.Name)]; ok && int(classIdx) < len(c.chunk.Classes) {
				classInfo := c.chunk.Classes[classIdx]
				if len(classInfo.TypeParameters) > 0 {
					operands := []int64{0}
					count := int64(0)
					for i, arg := range stmt.Type.Arguments {
						if i >= len(classInfo.TypeParameters) {
							break
						}
						if arg == nil || arg.Operator != "" || arg.Name == "" {
							continue
						}
						paramNameIdx := int64(len(c.chunk.Constants))
						c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: classInfo.TypeParameters[i]})
						typeNameIdx := int64(len(c.chunk.Constants))
						c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: arg.Name})
						operands = append(operands, paramNameIdx, typeNameIdx)
						count++
					}
					if count > 0 {
						operands[0] = count
						c.emitAt(OpSetTypeBindings, stmt.Token.Line, stmt.Token.Column, operands...)
					}
				}
			}
		}
		if stmt.Kind == "const" {
			c.emitAt(OpShallowFreeze, stmt.Token.Line, stmt.Token.Column)
		}
		if c.inGlobalScope() {
			slot := c.globalSlot(stmt.Name.Value)
			c.globalTypes[stmt.Name.Value] = declType
			c.emitAt(OpDefineGlobal, stmt.Token.Line, stmt.Token.Column, slot)
			return nil
		}
		slot := c.defineLocalWithType(stmt.Name.Value, declType)
		c.emitAt(OpDefineLocal, stmt.Token.Line, stmt.Token.Column, slot)
		return nil
	case *ast.DestructuringStatement:
		return c.compileDestructuringStatement(stmt)
	case *ast.IfStatement:
		return c.compileIfStatement(stmt)
	case *ast.WhileStatement:
		return c.compileWhileStatement(stmt)
	case *ast.ForStatement:
		return c.compileForStatement(stmt)
	case *ast.MatchStatement:
		return c.compileMatchStatement(stmt)
	case *ast.SelectStatement:
		return c.compileSelectStatement(stmt)
	case *ast.FunctionStatement:
		return c.compileFunctionStatement(stmt)
	case *ast.ClassStatement:
		return c.compileClassStatement(stmt)
	case *ast.InterfaceStatement:
		return c.compileInterfaceStatement(stmt)
	case *ast.EnumStatement:
		return c.compileEnumStatement(stmt)
	case *ast.TryStatement:
		return c.compileTryStatement(stmt)
	case *ast.WithStatement:
		return c.compileWithStatement(stmt)
	case *ast.DelStatement:
		return c.compileDelStatement(stmt)
	case *ast.SimpleStatement:
		return c.compileSimpleStatement(stmt)
	case *ast.ReturnStatement:
		if c.inFunc > 0 && stmt.Value != nil && len(c.finalizers) == 0 {
			if emitted, err := c.tryEmitTailCall(stmt); err != nil {
				return err
			} else if emitted {
				return nil
			}
		}
		if c.inFunc > 0 {
			if stmt.Value != nil {
				expected := c.currentReturnType()
				if expected == "void" || expected == "any" {
					expected = ""
				}
				if err := c.compileExpressionWithExpected(stmt.Value, expected); err != nil {
					return err
				}
			} else {
				c.emitConstant(runtime.Null{}, stmt.Token.Line, stmt.Token.Column)
			}
		} else if stmt.Value != nil {
			if err := c.compileExpression(stmt.Value); err != nil {
				return err
			}
			c.emitAt(OpPop, stmt.Token.Line, stmt.Token.Column)
		}
		if err := c.compileActiveFinalizers(stmt.Token.Line, stmt.Token.Column, true); err != nil {
			return err
		}
		c.emitAt(OpReturn, stmt.Token.Line, stmt.Token.Column)
		return nil
	case *ast.YieldStatement:
		if stmt.Value != nil {
			if err := c.compileExpression(stmt.Value); err != nil {
				return err
			}
		} else {
			c.emitConstant(runtime.Null{}, stmt.Token.Line, stmt.Token.Column)
		}
		c.emitAt(OpYield, stmt.Token.Line, stmt.Token.Column)
		return nil
	case *ast.ExpressionStatement:
		if c.tryEmitFusedStringAppendStmt(stmt.Expression) {
			return nil
		}
		if err := c.compileExpression(stmt.Expression); err != nil {
			return err
		}
		if !c.expressionLeavesNoValue(stmt.Expression) {
			c.emitAt(OpPop, stmt.Token.Line, stmt.Token.Column)
		}
		return nil
	default:
		return parityErrorf("bytecode compiler does not support %T yet", stmt)
	}
}

// tryEmitTailCall checks whether `return f(args)` qualifies for tail
// call elimination and, if so, emits an OpTailCall plus the args. Skips
// the OpReturn emission; the tail-called function's eventual OpReturn
// pops the reused frame to the original caller.
//
// Conditions:
//   - The return value is a direct CallExpression with a plain
//     identifier callee resolving to a statically-known function.
//   - The function has no spread / no type args / no decorators.
//   - Args use positional-only (no named args).
//
// Returns true if the tail call was emitted; the caller skips its
// normal compile-then-OpReturn path. Returns false to fall through.
func (c *Compiler) tryEmitTailCall(stmt *ast.ReturnStatement) (bool, error) {
	call, ok := stmt.Value.(*ast.CallExpression)
	if !ok {
		return false, nil
	}
	ident, ok := call.Callee.(*ast.Identifier)
	if !ok {
		return false, nil
	}
	// Skip when the identifier resolves to a local/global (the call
	// dispatches through a closure value, not a direct function).
	if _, isVar := c.resolveName(ident.Value); isVar {
		return false, nil
	}
	// Skip class instantiation: `Stream(args)` is OpNew + constructor,
	// not a direct function call. The constructor function lives in
	// c.funcs under the class name, but tail-calling it would bypass
	// OpNew and produce an empty value. Case-sensitive: `view(args)`
	// must NOT be treated as construction just because `View` exists.
	if classIndex, isClass := c.classes[strings.ToLower(ident.Value)]; isClass && c.chunk.Classes[classIndex].Name == ident.Value {
		return false, nil
	}
	if _, hasSpread := callSpreadIndex(call.Arguments); hasSpread {
		return false, nil
	}
	if len(call.TypeArguments) > 0 {
		return false, nil
	}
	for _, arg := range call.Arguments {
		if arg.Name != nil {
			return false, nil
		}
	}
	index, orderedArgs, err := c.selectFunctionCall(ident.Value, call.Arguments, 0)
	if err != nil {
		return false, nil
	}
	fn := c.chunk.Functions[index]
	if fn.Async || fn.IsGenerator || fn.Variadic {
		return false, nil
	}
	if fn.SharesParentFrame {
		// Nested functions borrow the enclosing frame's locals; tail
		// calling would release them out from under any in-flight
		// reference.
		return false, nil
	}
	if len(fn.Decorators) > 0 {
		return false, nil
	}
	if len(fn.TypeParameters) > 0 {
		// Generic functions need type-binding inference; skip TCE.
		return false, nil
	}
	if len(orderedArgs) != len(fn.ParamSlots) {
		// Default args or short calls; let the normal path expand them.
		return false, nil
	}
	for _, paramType := range fn.ParamTypes {
		// Restrict TCE to functions whose params validate in O(1).
		// Container types (dict/list/set/Class<T>) require deep walks
		// that the OpCall path amortises but TCE would repeat per
		// iteration of a tail-recursive loop.
		if paramType == "" {
			continue
		}
		if !isPrimitiveTypeForTCE(paramType) {
			return false, nil
		}
	}
	if err := c.compileOrderedArguments(fn, orderedArgs, 0, call.Token.Line, call.Token.Column); err != nil {
		return false, err
	}
	c.emitAt(OpTailCall, stmt.Token.Line, stmt.Token.Column, index, int64(len(orderedArgs)))
	return true, nil
}

func (c *Compiler) tryEmitFusedStringAppendStmt(expr ast.Expression) bool {
	assign, ok := expr.(*ast.AssignmentExpression)
	if !ok {
		return false
	}
	leftIdent, ok := assign.Left.(*ast.Identifier)
	if !ok {
		return false
	}
	resolved, ok := c.resolveName(leftIdent.Value)
	if !ok || resolved.typ != "string" {
		return false
	}
	if resolved.kind != "local" && resolved.kind != "global" {
		return false
	}
	lit, ok := selfStringConstAppendAssignment(leftIdent.Value, assign.Value)
	if !ok {
		return false
	}
	constIdx := int64(len(c.chunk.Constants))
	c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: lit})
	op := OpAppendStringConstStmt
	if resolved.kind == "global" {
		op = OpAppendGlobalStringConstStmt
	}
	c.emitAt(op, assign.Token.Line, assign.Token.Column, resolved.slot, constIdx)
	return true
}

func (c *Compiler) compileWhileStatement(stmt *ast.WhileStatement) error {
	loopStart := len(c.chunk.Instructions)
	exitJump, err := c.compileConditionAndJumpIfFalse(stmt.Condition, stmt.Token.Line, stmt.Token.Column)
	if err != nil {
		return err
	}
	c.loops = append(c.loops, loopContext{continueTarget: loopStart})
	if err := c.compileBlock(stmt.Body); err != nil {
		c.loops = c.loops[:len(c.loops)-1]
		return err
	}
	loop := c.loops[len(c.loops)-1]
	c.loops = c.loops[:len(c.loops)-1]
	c.emitAt(OpJump, stmt.Token.Line, stmt.Token.Column, int64(loopStart))
	c.patchJump(exitJump)
	for _, jump := range loop.breakJumps {
		c.patchJump(jump)
	}
	return nil
}

func (c *Compiler) compileDestructuringStatement(stmt *ast.DestructuringStatement) error {
	if err := c.compileExpression(stmt.Value); err != nil {
		return err
	}
	tempSlot := c.allocateLocal()
	c.emitAt(OpDefineLocal, stmt.Token.Line, stmt.Token.Column, tempSlot)
	if stmt.IsList {
		c.emitAt(OpCheckUnpackLen, stmt.Token.Line, stmt.Token.Column, tempSlot, int64(len(stmt.Names)))
	}
	for i, name := range stmt.Names {
		c.emitAt(OpGetLocal, stmt.Token.Line, stmt.Token.Column, tempSlot)
		if stmt.IsList {
			idxConst := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.SmallInt{Value: int64(i)})
			c.emitAt(OpConstant, stmt.Token.Line, stmt.Token.Column, idxConst)
		} else {
			key := name.Value
			if i < len(stmt.Keys) && stmt.Keys[i] != "" {
				key = stmt.Keys[i]
			}
			keyConst := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: key})
			c.emitAt(OpConstant, stmt.Token.Line, stmt.Token.Column, keyConst)
		}
		c.emitAt(OpIndex, stmt.Token.Line, stmt.Token.Column)
		if stmt.Define {
			if c.inGlobalScope() {
				slot := c.globalSlot(name.Value)
				c.globalTypes[name.Value] = "any"
				c.emitAt(OpDefineGlobal, stmt.Token.Line, stmt.Token.Column, slot)
			} else {
				slot := c.defineLocalWithType(name.Value, "any")
				c.emitAt(OpDefineLocal, stmt.Token.Line, stmt.Token.Column, slot)
			}
		} else {
			resolved, ok := c.resolveName(name.Value)
			if !ok {
				return fmt.Errorf("unknown bytecode name %s", name.Value)
			}
			if resolved.kind == "local" {
				c.emitAt(OpSetLocal, stmt.Token.Line, stmt.Token.Column, resolved.slot)
			} else {
				c.emitAt(OpSetGlobal, stmt.Token.Line, stmt.Token.Column, resolved.slot)
			}
		}
	}
	return nil
}

func (c *Compiler) compileForStatement(stmt *ast.ForStatement) error {
	if stmt.Iterable != nil {
		return c.compileForInStatement(stmt)
	}
	c.pushScope()
	defer c.popScope()
	if stmt.Init != nil {
		if err := c.compileStatement(stmt.Init); err != nil {
			return err
		}
	}
	loopStart := len(c.chunk.Instructions)
	exitJump := -1
	if stmt.Condition != nil {
		jp, err := c.compileConditionAndJumpIfFalse(stmt.Condition, stmt.Token.Line, stmt.Token.Column)
		if err != nil {
			return err
		}
		exitJump = jp
	}
	c.loops = append(c.loops, loopContext{continueTarget: -1})
	if err := c.compileBlock(stmt.Body); err != nil {
		c.loops = c.loops[:len(c.loops)-1]
		return err
	}
	loop := c.loops[len(c.loops)-1]
	c.loops = c.loops[:len(c.loops)-1]
	updateStart := len(c.chunk.Instructions)
	c.patchLoopContinues(loop, updateStart)
	if stmt.Update != nil {
		if err := c.compileStatement(stmt.Update); err != nil {
			return err
		}
	}
	c.emitAt(OpJump, stmt.Token.Line, stmt.Token.Column, int64(loopStart))
	if exitJump >= 0 {
		c.patchJump(exitJump)
	}
	for _, jump := range loop.breakJumps {
		c.patchJump(jump)
	}
	return nil
}

func (c *Compiler) compileForInStatement(stmt *ast.ForStatement) error {
	names := stmt.VarNames
	if len(names) == 0 && stmt.VarName != nil {
		names = []*ast.Identifier{stmt.VarName}
	}
	if len(names) == 0 {
		return fmt.Errorf("for-in loop has no loop variable")
	}
	c.pushScope()
	defer c.popScope()

	c.emitConstant(runtime.Null{}, stmt.Token.Line, stmt.Token.Column)
	valueSlot := c.allocateLocal()
	c.emitAt(OpDefineLocal, stmt.Token.Line, stmt.Token.Column, valueSlot)
	targetSlots := make([]int64, 0, len(names))
	for _, name := range names {
		c.emitConstant(runtime.Null{}, stmt.Token.Line, stmt.Token.Column)
		slot := c.defineLocal(name.Value)
		c.emitAt(OpDefineLocal, stmt.Token.Line, stmt.Token.Column, slot)
		targetSlots = append(targetSlots, slot)
	}
	if err := c.compileExpression(stmt.Iterable); err != nil {
		return err
	}
	c.emitAt(OpIterInit, stmt.Token.Line, stmt.Token.Column)
	iterSlot := c.allocateLocal()
	c.emitAt(OpDefineLocal, stmt.Token.Line, stmt.Token.Column, iterSlot)

	loopStart := len(c.chunk.Instructions)
	nextJump := c.emitJump(OpIterNext, stmt.Token.Line, stmt.Token.Column)
	c.chunk.Instructions[nextJump].Operands = append(c.chunk.Instructions[nextJump].Operands, iterSlot, valueSlot)
	if len(targetSlots) == 1 {
		c.emitAt(OpGetLocal, stmt.Token.Line, stmt.Token.Column, valueSlot)
		c.emitAt(OpSetLocal, stmt.Token.Line, stmt.Token.Column, targetSlots[0])
		c.emitAt(OpPop, stmt.Token.Line, stmt.Token.Column)
	} else {
		operands := append([]int64{valueSlot}, targetSlots...)
		c.emitAt(OpUnpackList, stmt.Token.Line, stmt.Token.Column, operands...)
	}

	c.loops = append(c.loops, loopContext{continueTarget: loopStart, iterSlot: iterSlot, hasIterSlot: true})
	c.pushFinalizer(finalizerContext{iterSlot: iterSlot, hasIterSlot: true})
	if err := c.compileBlock(stmt.Body); err != nil {
		c.popFinalizer()
		c.loops = c.loops[:len(c.loops)-1]
		return err
	}
	c.popFinalizer()
	loop := c.loops[len(c.loops)-1]
	c.loops = c.loops[:len(c.loops)-1]
	c.emitAt(OpJump, stmt.Token.Line, stmt.Token.Column, int64(loopStart))
	c.patchJump(nextJump)
	c.emitAt(OpIterClose, stmt.Token.Line, stmt.Token.Column, iterSlot)
	for _, jump := range loop.breakJumps {
		c.patchJump(jump)
	}
	return nil
}

func (c *Compiler) compileFunctionStatement(stmt *ast.FunctionStatement) error {
	return c.compileFunction(stmt, stmt.Name.Value, "")
}

func (c *Compiler) compileFunction(stmt *ast.FunctionStatement, name string, receiverName string) error {
	return c.compileFunctionWithPrologue(stmt, name, receiverName, nil)
}

func (c *Compiler) currentClassTypeParams() []string {
	if len(c.classStack) == 0 {
		return nil
	}
	classIndex := c.classStack[len(c.classStack)-1]
	if classIndex < 0 || int(classIndex) >= len(c.chunk.Classes) {
		return nil
	}
	return c.chunk.Classes[classIndex].TypeParameters
}

func (c *Compiler) currentClassTypeParamConstraintExprs() []string {
	if len(c.classStack) == 0 {
		return nil
	}
	classIndex := c.classStack[len(c.classStack)-1]
	if classIndex < 0 || int(classIndex) >= len(c.chunk.Classes) {
		return nil
	}
	return c.chunk.Classes[classIndex].TypeParamConstraintExprs
}

func (c *Compiler) compileFunctionWithPrologue(stmt *ast.FunctionStatement, name string, receiverName string, prologue func() error) error {
	/* Static methods compile through the same body pipeline as regular
	 * methods - they just skip the implicit `this` receiver. The
	 * caller is responsible for passing `receiverName = ""` for
	 * statics, and for registering the resulting function index
	 * under `class.StaticMethods` rather than `class.Methods` (see
	 * compileClassStatement). */
	index := c.nextFunctionIndex(name)
	skipJump := c.emitJump(OpJump, stmt.Token.Line, stmt.Token.Column)
	entry := int64(len(c.chunk.Instructions))
	c.pushScope()
	// Nested function statements (declared inside another function's body)
	// share the enclosing function's slot space because their bodies may
	// reference outer locals directly via resolveName. Only outermost
	// function and method declarations reset the slot counter.
	nestedStatement := c.inFunc > 0
	if !nestedStatement {
		c.pushFunctionLocals()
	}
	paramSlots := make([]int64, 0, len(stmt.Parameters))
	var constSlots []int64
	paramNames := make([]string, 0, len(stmt.Parameters))
	paramTypes := make([]string, 0, len(stmt.Parameters))
	paramDecorators := make([][]runtime.DecoratorMetadata, 0, len(stmt.Parameters))
	defaultConstants := make([]int64, 0, len(stmt.Parameters))
	if receiverName != "" {
		paramNames = append(paramNames, strings.ToLower(receiverName))
		paramSlots = append(paramSlots, c.defineLocalWithType(receiverName, ""))
		paramTypes = append(paramTypes, "")
		paramDecorators = append(paramDecorators, nil)
		defaultConstants = append(defaultConstants, -1)
	}
	allTypeParams := append(typeParameterNames(stmt.Generics), c.currentClassTypeParams()...)
	allTypeParamConstraintExprs := append(typeParamConstraintExprs(stmt.Generics), c.currentClassTypeParamConstraintExprs()...)
	for _, param := range stmt.Parameters {
		if param.Name == nil {
			if !nestedStatement {
				c.popFunctionLocals()
			}
			c.popScope()
			return fmt.Errorf("function parameter has no name")
		}
		paramNames = append(paramNames, strings.ToLower(param.Name.Value))
		paramType := c.bytecodeTypeNameForParam(param.Type, allTypeParams)
		paramSlot := c.defineLocalWithType(param.Name.Value, c.bytecodeTypeName(param.Type))
		paramSlots = append(paramSlots, paramSlot)
		if param.Const {
			constSlots = append(constSlots, paramSlot)
		}
		paramTypes = append(paramTypes, paramType)
		paramDecs, err := decoratorsMetadata(param.Decorators, "parameter", 0)
		if err != nil {
			if !nestedStatement {
				c.popFunctionLocals()
			}
			c.popScope()
			return err
		}
		paramDecorators = append(paramDecorators, paramDecs)
		if param.Default == nil {
			defaultConstants = append(defaultConstants, -1)
			continue
		}
		value, err := constantValueFromExpression(param.Default)
		if err != nil {
			if !nestedStatement {
				c.popFunctionLocals()
			}
			c.popScope()
			return err
		}
		defaultConstants = append(defaultConstants, int64(len(c.chunk.Constants)))
		c.chunk.Constants = append(c.chunk.Constants, value)
	}
	c.chunk.Functions[index].TypeParameters = allTypeParams
	c.chunk.Functions[index].TypeParamConstraintExprs = allTypeParamConstraintExprs
	c.chunk.Functions[index].Doc = stmt.Doc
	c.chunk.Functions[index].Entry = entry
	c.chunk.Functions[index].DefLine = int64(stmt.Token.Line)
	c.chunk.Functions[index].DefColumn = int64(stmt.Token.Column)
	c.chunk.Functions[index].ParamNames = paramNames
	c.chunk.Functions[index].ParamSlots = paramSlots
	c.chunk.Functions[index].ParamTypes = paramTypes
	c.chunk.Functions[index].ParamDecorators = paramDecorators
	c.chunk.Functions[index].ReturnType = c.bytecodeReturnType(stmt.ReturnType)
	c.chunk.Functions[index].DefaultConstants = defaultConstants
	c.chunk.Functions[index].IsGenerator = blockContainsYield(stmt.Body)
	target := "function"
	if receiverName != "" {
		target = "method"
	} else if strings.Contains(name, ".") {
		target = "staticMethod"
	}
	dec, err := decoratorsMetadata(stmt.Decorators, target, int64(len(c.chunk.Functions[index].Decorators)))
	if err != nil {
		return err
	}
	c.chunk.Functions[index].Decorators = dec
	if n := len(stmt.Parameters); n > 0 && stmt.Parameters[n-1].Variadic {
		c.chunk.Functions[index].Variadic = true
	}
	c.chunk.Functions[index].Async = stmt.Async
	c.inFunc++
	c.returnTypes = append(c.returnTypes, c.chunk.Functions[index].ReturnType)
	// Isolate finalizers so a `return` here emits only this function's, not an
	// enclosing for-loop's iterator close (whose slot lives in another frame).
	savedLoops, savedFinalizers := c.loops, c.finalizers
	c.loops, c.finalizers = nil, nil
	defer func() { c.loops, c.finalizers = savedLoops, savedFinalizers }()
	if prologue != nil {
		if err := prologue(); err != nil {
			c.inFunc--
			c.returnTypes = c.returnTypes[:len(c.returnTypes)-1]
			if !nestedStatement {
				c.popFunctionLocals()
			}
			c.popScope()
			return err
		}
	}
	for _, slot := range constSlots {
		c.emitAt(OpFreezeLocal, stmt.Token.Line, stmt.Token.Column, slot)
	}
	if err := c.compileBlock(stmt.Body); err != nil {
		c.inFunc--
		c.returnTypes = c.returnTypes[:len(c.returnTypes)-1]
		if !nestedStatement {
			c.popFunctionLocals()
		}
		c.popScope()
		return err
	}
	c.emitConstant(runtime.Null{}, stmt.Token.Line, stmt.Token.Column)
	c.emitAt(OpReturn, stmt.Token.Line, stmt.Token.Column)
	if nestedStatement {
		// Inner function shares the outer function's slot space; its
		// LocalCount tracks the current high-water so the outer can
		// surface a correct cumulative count when it finishes. The VM
		// uses the SharesParentFrame flag to skip allocating a fresh
		// locals slice on call entry.
		c.chunk.Functions[index].LocalCount = c.locals
		c.chunk.Functions[index].SharesParentFrame = true
	} else {
		c.chunk.Functions[index].LocalCount = c.popFunctionLocals()
	}
	c.inFunc--
	c.returnTypes = c.returnTypes[:len(c.returnTypes)-1]
	c.popScope()
	c.patchJump(skipJump)
	return nil
}

func (c *Compiler) compileClassStatement(stmt *ast.ClassStatement) error {
	index := c.declareClass(stmt.Name.Value)
	class := c.chunk.Classes[index]
	class.Doc = stmt.Doc
	class.ParentIndex = -1
	class.DefLine = int64(stmt.Token.Line)
	class.DefColumn = int64(stmt.Token.Column)
	class.TypeParameters = typeParameterNames(stmt.Generics)
	class.TypeParamConstraintExprs = typeParamConstraintExprs(stmt.Generics)
	class.Methods = map[string][]int64{}
	class.StaticValues = map[string]int64{}
	class.StaticMethods = map[string][]int64{}
	callableDecorators := make([]ast.Decorator, 0, len(stmt.Decorators))
	for _, dec := range stmt.Decorators {
		if dec.Name.Value == "immutable" && len(dec.Arguments) == 0 {
			class.Immutable = true
		} else {
			callableDecorators = append(callableDecorators, dec)
		}
	}
	classDec, err := decoratorsMetadata(callableDecorators, "class", 0)
	if err != nil {
		return err
	}
	class.Decorators = classDec
	class.MethodDecorators = map[string][]runtime.DecoratorMetadata{}
	class.StaticDecorators = map[string][]runtime.DecoratorMetadata{}
	if stmt.Extends != nil {
		parentIndex, ok := c.classes[strings.ToLower(stmt.Extends.Name)]
		if ok {
			class.ParentIndex = parentIndex
			class.ParentName = c.chunk.Classes[parentIndex].Name
		} else if isBuiltinErrorClass(stmt.Extends.Name) || strings.Contains(stmt.Extends.Name, ".") {
			// Cross-module parent (e.g. `extends pluginmod.Plugin`). Resolve
			// the alias prefix to its canonical module so the VM can look
			// up the parent class at runtime via the module loader.
			class.ParentName = c.resolveQualifiedClassName(stmt.Extends.Name)
		} else if qual, ok := c.fromImports[stmt.Extends.Name]; ok {
			class.ParentName = qual
		} else {
			return fmt.Errorf("bytecode compiler parent class %s is not declared", stmt.Extends.Name)
		}
		if len(stmt.Extends.Arguments) > 0 {
			args := make([]string, 0, len(stmt.Extends.Arguments))
			for _, arg := range stmt.Extends.Arguments {
				args = append(args, arg.String())
			}
			class.ParentArguments = args
		}
	}
	for _, ifaceRef := range stmt.Implements {
		if _, ok := c.interfaces[strings.ToLower(ifaceRef.Name)]; !ok {
			if strings.Contains(ifaceRef.Name, ".") {
				// dotted cross-module interface: pass through as-is
			} else if qual, ok := c.fromImports[ifaceRef.Name]; ok {
				class.Implements = append(class.Implements, qual)
				continue
			} else {
				return fmt.Errorf("bytecode compiler interface %s is not declared", ifaceRef.Name)
			}
		}
		class.Implements = append(class.Implements, ifaceRef.Name)
	}
	if err := c.injectInterfaceMembers(stmt); err != nil {
		return err
	}
	c.chunk.Classes[index] = class
	c.classStack = append(c.classStack, index)
	defer func() {
		c.classStack = c.classStack[:len(c.classStack)-1]
	}()
	for _, member := range stmt.Members {
		switch member := member.(type) {
		case *ast.DeclarationStatement:
			if member.Kind == "static const" || member.Kind == "static let" {
				value := runtime.Value(runtime.Null{})
				if member.Value != nil {
					parsed, err := constantValueFromExpression(member.Value)
					if err != nil {
						return c.withStatementLocation(member, fmt.Errorf("bytecode compiler only supports literal static %s values", strings.TrimPrefix(member.Kind, "static ")))
					}
					value = parsed
				}
				class.StaticValues[member.Name.Value] = int64(len(c.chunk.Constants))
				c.chunk.Constants = append(c.chunk.Constants, value)
				continue
			}
			if strings.HasPrefix(member.Kind, "static ") {
				return c.withStatementLocation(member, fmt.Errorf("bytecode compiler only supports static const and static let class declarations"))
			}
			class.FieldNames = append(class.FieldNames, member.Name.Value)
			fieldType := ""
			if member.Type != nil {
				fieldType = member.Type.String()
			}
			class.FieldTypes = append(class.FieldTypes, fieldType)
			/* Field decorators ride alongside the field at compile time
			 * so reflect.fields can surface them at runtime without
			 * re-reading the AST. Frameworks consume them as
			 * metadata - they are never auto-executed on field
			 * access or assignment. */
			fieldDecs := member.Decorators
			if hasImmutableFieldDecorator(member.Decorators) {
				if member.Value != nil {
					return c.withStatementLocation(member, fmt.Errorf("@immutable field %q may not declare a default value", member.Name.Value))
				}
				class.ImmutableFields = append(class.ImmutableFields, member.Name.Value)
				fieldDecs = withoutImmutableDecorator(member.Decorators)
			}
			class.FieldDecorators = appendFieldDecorators(class.FieldDecorators, len(class.FieldNames)-1, fieldDecs)
			if member.Value == nil {
				class.FieldDefaults = append(class.FieldDefaults, -1)
				continue
			}
			value, err := constantValueFromExpression(member.Value)
			if err != nil {
				return c.withStatementLocation(member, fmt.Errorf("bytecode compiler only supports literal class field defaults"))
			}
			class.FieldDefaults = append(class.FieldDefaults, int64(len(c.chunk.Constants)))
			c.chunk.Constants = append(c.chunk.Constants, value)
		case *ast.FunctionStatement:
			// Flush field metadata into chunk.Classes before compiling
			// methods so staticIntExpr / staticStringExpr can resolve
			// `this.field` against the up-to-date class info.
			c.chunk.Classes[index] = class
			functionName := stmt.Name.Value + "." + member.Name.Value
			compiledMember := *member
			compiledMember.Static = false
			var receiverName string
			var prologue func() error
			if member.Static {
				receiverName = ""
			} else {
				receiverName = "this"
			}
			if !member.Static && strings.EqualFold(member.Name.Value, stmt.Name.Value) && class.ParentIndex >= 0 && !containsParentConstructorCall(member.Body) {
				prologue = func() error {
					parent := c.chunk.Classes[class.ParentIndex]
					if len(parent.ConstructorIndices) > 0 {
						if _, _, err := c.selectFunctionIndicesCallIgnoringReturn(parent.Name, parent.ConstructorIndices, nil, 1); err != nil {
							return err
						}
					}
					resolved, ok := c.resolveName("this")
					if !ok {
						return fmt.Errorf("auto parent constructor call requires this")
					}
					c.emitAt(OpGetLocal, member.Token.Line, member.Token.Column, resolved.slot)
					c.emitAt(OpCallParentConstructor, member.Token.Line, member.Token.Column, index, 0)
					c.emitAt(OpPop, member.Token.Line, member.Token.Column)
					return nil
				}
			}
			if err := c.compileFunctionWithPrologue(&compiledMember, functionName, receiverName, prologue); err != nil {
				return c.withStatementLocation(member, err)
			}
			functionIndex, err := c.lastFunctionIndex(functionName)
			if err != nil {
				return err
			}
			if member.Static {
				key := strings.ToLower(member.Name.Value)
				class.StaticMethods[key] = append(class.StaticMethods[key], functionIndex)
				staticDec, err := decoratorsMetadata(member.Decorators, "staticMethod", nextOverloadIndex(class.StaticDecorators[key]))
				if err != nil {
					return c.withStatementLocation(member, err)
				}
				class.StaticDecorators[key] = append(class.StaticDecorators[key], staticDec...)
			} else if strings.EqualFold(member.Name.Value, stmt.Name.Value) {
				class.ConstructorIndices = append(class.ConstructorIndices, functionIndex)
			} else {
				key := strings.ToLower(member.Name.Value)
				class.Methods[key] = append(class.Methods[key], functionIndex)
				methodDec, err := decoratorsMetadata(member.Decorators, "method", nextOverloadIndex(class.MethodDecorators[key]))
				if err != nil {
					return c.withStatementLocation(member, err)
				}
				class.MethodDecorators[key] = append(class.MethodDecorators[key], methodDec...)
			}
		default:
			return c.withStatementLocation(member, parityErrorf("bytecode compiler does not support class member %T yet", member))
		}
	}
	if stmt.Destructor != nil {
		dtor := stmt.Destructor
		functionName := stmt.Name.Value + ".~" + dtor.Name.Value
		compiled := *dtor
		compiled.Static = false
		if err := c.compileFunctionWithPrologue(&compiled, functionName, "this", nil); err != nil {
			return c.withStatementLocation(dtor, err)
		}
		functionIndex, err := c.lastFunctionIndex(functionName)
		if err != nil {
			return err
		}
		class.DestructorIndex = functionIndex
	}
	c.chunk.Classes[index] = class
	c.emitAt(OpDefineClass, stmt.Token.Line, stmt.Token.Column, index)
	return nil
}

func (c *Compiler) injectInterfaceMembers(stmt *ast.ClassStatement) error {
	declaredMethods := map[string]bool{}
	declaredFields := map[string]bool{}
	for _, member := range stmt.Members {
		switch m := member.(type) {
		case *ast.FunctionStatement:
			declaredMethods[strings.ToLower(m.Name.Value)] = true
		case *ast.DeclarationStatement:
			if !strings.HasPrefix(m.Kind, "static") {
				declaredFields[strings.ToLower(m.Name.Value)] = true
			}
		}
	}
	defaultSource := map[string]string{}
	defaultMethod := map[string]*ast.FunctionStatement{}
	fieldSource := map[string]string{}
	fieldDecl := map[string]*ast.DeclarationStatement{}
	for _, ifaceRef := range stmt.Implements {
		iface, ok := c.interfaceAST[strings.ToLower(ifaceRef.Name)]
		if !ok {
			continue
		}
		for _, def := range iface.Defaults {
			key := strings.ToLower(def.Name.Value)
			if declaredMethods[key] {
				continue
			}
			if prev, seen := defaultSource[key]; seen && prev != iface.Name.Value {
				return c.withStatementLocation(stmt, fmt.Errorf("class %s inherits multiple defaults for %s from %s and %s; class must override", stmt.Name.Value, def.Name.Value, prev, iface.Name.Value))
			}
			defaultSource[key] = iface.Name.Value
			defaultMethod[key] = def
		}
		for _, field := range iface.Fields {
			key := strings.ToLower(field.Name.Value)
			if declaredFields[key] {
				continue
			}
			if prev, seen := fieldSource[key]; seen {
				prevField := fieldDecl[key]
				if prevField.Type.String() != field.Type.String() {
					return c.withStatementLocation(stmt, fmt.Errorf("class %s inherits field %s from %s (%s) and %s (%s) with conflicting types", stmt.Name.Value, field.Name.Value, prev, prevField.Type.String(), iface.Name.Value, field.Type.String()))
				}
				continue
			}
			fieldSource[key] = iface.Name.Value
			fieldDecl[key] = field
		}
	}
	for _, field := range fieldDecl {
		stmt.Members = append(stmt.Members, field)
	}
	for _, method := range defaultMethod {
		stmt.Members = append(stmt.Members, method)
	}
	return nil
}

func (c *Compiler) compileInterfaceStatement(stmt *ast.InterfaceStatement) error {
	c.interfaceAST[strings.ToLower(stmt.Name.Value)] = stmt
	index := c.declareInterface(stmt.Name.Value)
	iface := c.chunk.Interfaces[index]
	iface.Doc = stmt.Doc
	iface.TypeParameters = typeParameterNames(stmt.Generics)
	iface.Parents = iface.Parents[:0]
	iface.Methods = iface.Methods[:0]
	iface.Fields = iface.Fields[:0]
	iface.FieldTypes = iface.FieldTypes[:0]
	iface.Defaults = nil
	for _, parent := range stmt.Parents {
		if _, ok := c.interfaces[strings.ToLower(parent.Name)]; !ok {
			if !strings.Contains(parent.Name, ".") {
				return fmt.Errorf("bytecode compiler parent interface %s is not declared", parent.Name)
			}
		}
		iface.Parents = append(iface.Parents, parent.Name)
	}
	for _, method := range stmt.Methods {
		iface.Methods = append(iface.Methods, reflectFunctionMetadataFromSignature(c, method))
	}
	for _, field := range stmt.Fields {
		iface.Fields = append(iface.Fields, field.Name.Value)
		typeStr := ""
		if field.Type != nil {
			typeStr = field.Type.String()
		}
		iface.FieldTypes = append(iface.FieldTypes, typeStr)
	}
	if len(stmt.Defaults) > 0 {
		iface.Defaults = make(map[string]int64, len(stmt.Defaults))
		for _, def := range stmt.Defaults {
			fnIndex, err := c.compileInterfaceDefault(stmt.Name.Value, def)
			if err != nil {
				return err
			}
			iface.Defaults[strings.ToLower(def.Name.Value)] = fnIndex
		}
	}
	c.chunk.Interfaces[index] = iface
	return nil
}

func (c *Compiler) compileInterfaceDefault(ifaceName string, def *ast.FunctionStatement) (int64, error) {
	wrapped := *def
	wrapped.Static = false
	functionName := ifaceName + "." + def.Name.Value
	if err := c.compileFunction(&wrapped, functionName, "this"); err != nil {
		return -1, err
	}
	return c.lastFunctionIndex(functionName)
}

func (c *Compiler) compileSimpleStatement(stmt *ast.SimpleStatement) error {
	switch stmt.Kind {
	case "break":
		if len(c.loops) == 0 {
			return fmt.Errorf("break is only supported inside bytecode loops")
		}
		if err := c.compileActiveFinalizers(stmt.Token.Line, stmt.Token.Column, true); err != nil {
			return err
		}
		jump := c.emitJump(OpJump, stmt.Token.Line, stmt.Token.Column)
		current := len(c.loops) - 1
		c.loops[current].breakJumps = append(c.loops[current].breakJumps, jump)
		return nil
	case "continue":
		if len(c.loops) == 0 {
			return fmt.Errorf("continue is only supported inside bytecode loops")
		}
		if err := c.compileActiveFinalizers(stmt.Token.Line, stmt.Token.Column, false); err != nil {
			return err
		}
		current := len(c.loops) - 1
		if c.loops[current].continueTarget >= 0 {
			c.emitAt(OpJump, stmt.Token.Line, stmt.Token.Column, int64(c.loops[current].continueTarget))
			return nil
		}
		jump := c.emitJump(OpJump, stmt.Token.Line, stmt.Token.Column)
		c.loops[current].continueJumps = append(c.loops[current].continueJumps, jump)
		return nil
	case "defer":
		return c.compileDeferStatement(stmt)
	case "throw":
		if stmt.Value == nil {
			return fmt.Errorf("throw expects a value")
		}
		if err := c.compileExpression(stmt.Value); err != nil {
			return err
		}
		c.emitAt(OpThrow, stmt.Token.Line, stmt.Token.Column)
		return nil
	default:
		return parityErrorf("bytecode compiler does not support %s statements yet", stmt.Kind)
	}
}

func (c *Compiler) compileTryStatement(stmt *ast.TryStatement) error {
	handlerJump := c.emitJump(OpPushExceptionHandler, stmt.Token.Line, stmt.Token.Column)
	c.pushFinalizer(finalizerContext{body: stmt.Finally, popHandler: true})
	if err := c.compileBlock(stmt.Body); err != nil {
		c.popFinalizer()
		return err
	}
	c.popFinalizer()
	c.emitAt(OpPopExceptionHandler, stmt.Token.Line, stmt.Token.Column)
	normalJump := c.emitJump(OpJump, stmt.Token.Line, stmt.Token.Column)
	c.patchJump(handlerJump)
	catchEndJumps := []int{}
	for _, catch := range stmt.Catches {
		nextCatchJump := c.emitJump(OpCatch, stmt.Token.Line, stmt.Token.Column)
		typeIndex := int64(-1)
		if catch.Type != nil {
			typeIndex = int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: catch.Type.Name})
		}
		slot := int64(-1)
		c.pushScope()
		if catch.Name != nil {
			slot = c.defineLocal(catch.Name.Value)
		}
		c.chunk.Instructions[nextCatchJump].Operands = append(c.chunk.Instructions[nextCatchJump].Operands, typeIndex, slot)
		c.pushFinalizer(finalizerContext{body: stmt.Finally})
		for _, bodyStmt := range catch.Body.Statements {
			if err := c.compileStatement(bodyStmt); err != nil {
				c.popFinalizer()
				c.popScope()
				return err
			}
		}
		c.popFinalizer()
		c.popScope()
		catchEndJumps = append(catchEndJumps, c.emitJump(OpJump, stmt.Token.Line, stmt.Token.Column))
		c.patchJump(nextCatchJump)
	}
	if stmt.Finally != nil {
		if err := c.compileBlock(stmt.Finally); err != nil {
			return err
		}
	}
	c.emitAt(OpRethrow, stmt.Token.Line, stmt.Token.Column)
	handledOrNormalTarget := len(c.chunk.Instructions)
	c.chunk.Instructions[normalJump].Operands[0] = int64(handledOrNormalTarget)
	for _, jump := range catchEndJumps {
		c.chunk.Instructions[jump].Operands[0] = int64(handledOrNormalTarget)
	}
	if stmt.Finally != nil {
		if err := c.compileBlock(stmt.Finally); err != nil {
			return err
		}
	}
	return nil
}

// compileWithStatement emits bytecode for `with (expr) { ... }`
// and `with (name = expr) { ... }`. The resource value is stored
// in a hidden local slot so the cleanup epilogue can locate it
// regardless of how the block exits (normal completion, return,
// break, continue, or exception). Cleanup invokes __exit__ if the
// class defines it, else the class destructor.
func (c *Compiler) compileWithStatement(stmt *ast.WithStatement) error {
	c.pushScope()
	defer c.popScope()
	if err := c.compileExpression(stmt.Value); err != nil {
		return err
	}
	hiddenSlot := c.allocateLocal()
	c.emitAt(OpDefineLocal, stmt.Token.Line, stmt.Token.Column, hiddenSlot)
	c.emitAt(OpGetLocal, stmt.Token.Line, stmt.Token.Column, hiddenSlot)
	c.emitAt(OpWithEnter, stmt.Token.Line, stmt.Token.Column)
	if stmt.Name != nil {
		nameSlot := c.defineLocal(stmt.Name.Value)
		c.emitAt(OpDefineLocal, stmt.Token.Line, stmt.Token.Column, nameSlot)
	} else {
		c.emitAt(OpPop, stmt.Token.Line, stmt.Token.Column)
	}
	handlerJump := c.emitJump(OpPushExceptionHandler, stmt.Token.Line, stmt.Token.Column)
	c.pushFinalizer(finalizerContext{popHandler: true, hasWithCleanup: true, withSlot: hiddenSlot})
	if err := c.compileBlock(stmt.Body); err != nil {
		c.popFinalizer()
		return err
	}
	c.popFinalizer()
	c.emitAt(OpPopExceptionHandler, stmt.Token.Line, stmt.Token.Column)
	c.emitAt(OpGetLocal, stmt.Token.Line, stmt.Token.Column, hiddenSlot)
	c.emitAt(OpWithExit, stmt.Token.Line, stmt.Token.Column)
	normalJump := c.emitJump(OpJump, stmt.Token.Line, stmt.Token.Column)
	c.patchJump(handlerJump)
	c.emitAt(OpGetLocal, stmt.Token.Line, stmt.Token.Column, hiddenSlot)
	c.emitAt(OpWithExit, stmt.Token.Line, stmt.Token.Column)
	c.emitAt(OpRethrow, stmt.Token.Line, stmt.Token.Column)
	c.patchJump(normalJump)
	return nil
}

// compileDelStatement emits OpDel for `del x`. The opcode takes
// two operands: the slot index and a kind flag (0 = local,
// 1 = global). Other kinds are rejected at compile time.
func (c *Compiler) compileDelStatement(stmt *ast.DelStatement) error {
	if stmt.Target == nil {
		return fmt.Errorf("del requires an identifier")
	}
	// del operates on variables; declarations are rejected so both backends agree.
	if c.isDeclaredNonVariable(stmt.Target.Value) {
		return fmt.Errorf("cannot del %q: del operates on variables, not declarations", stmt.Target.Value)
	}
	resolved, ok := c.resolveName(stmt.Target.Value)
	if !ok {
		return fmt.Errorf("del: unknown identifier %q", stmt.Target.Value)
	}
	switch resolved.kind {
	case "local":
		c.emitAt(OpDel, stmt.Token.Line, stmt.Token.Column, resolved.slot, 0)
	case "global":
		c.emitAt(OpDel, stmt.Token.Line, stmt.Token.Column, resolved.slot, 1)
	default:
		return fmt.Errorf("del: unsupported binding kind for %q", stmt.Target.Value)
	}
	return nil
}

func (c *Compiler) isDeclaredNonVariable(name string) bool {
	return c.declaredDecls[name]
}

func (c *Compiler) compileDeferStatement(stmt *ast.SimpleStatement) error {
	call, ok := stmt.Value.(*ast.CallExpression)
	if !ok {
		return fmt.Errorf("bytecode compiler only supports deferring calls, not %T; use --disable-vm to run with the tree-walking evaluator", stmt.Value)
	}
	// Plain identifier call: defer someFunc(args)
	if ident, ok := call.Callee.(*ast.Identifier); ok {
		index, orderedArgs, err := c.selectFunctionCall(ident.Value, call.Arguments, 0)
		if err == nil {
			if err := c.compileOrderedArguments(c.chunk.Functions[index], orderedArgs, 0, stmt.Token.Line, stmt.Token.Column); err != nil {
				return err
			}
			c.emitAt(OpDeferFuncCall, stmt.Token.Line, stmt.Token.Column, index, int64(len(orderedArgs)))
			return nil
		}
		// Fall back to a variable holding a callable (closure/lambda).
		if resolved, isVar := c.resolveName(ident.Value); isVar {
			if resolved.kind == "local" {
				c.emitAt(OpGetLocal, stmt.Token.Line, stmt.Token.Column, resolved.slot)
			} else {
				c.emitAt(OpGetGlobal, stmt.Token.Line, stmt.Token.Column, resolved.slot)
			}
			hasNamedArgs := false
			for _, arg := range call.Arguments {
				if arg.Name != nil {
					hasNamedArgs = true
				}
				if err := c.compileExpression(arg.Value); err != nil {
					return err
				}
			}
			if !hasNamedArgs {
				c.emitAt(OpDeferCallableCall, stmt.Token.Line, stmt.Token.Column, int64(len(call.Arguments)))
				return nil
			}
			operands := []int64{int64(len(call.Arguments))}
			for _, arg := range call.Arguments {
				operands = append(operands, c.argNameIndex(arg))
			}
			c.emitAt(OpDeferCallableCallNamed, stmt.Token.Line, stmt.Token.Column, operands...)
			return nil
		}
		return fmt.Errorf("defer: %w", err)
	}
	module, name, ok := selectorName(call.Callee)
	if !ok {
		return fmt.Errorf("bytecode compiler only supports deferring function and module calls; use --disable-vm to run with the tree-walking evaluator")
	}
	// If the selector object resolves to a variable it's a method call, not a module call.
	if resolved, isVar := c.resolveName(module); isVar {
		if resolved.kind == "local" {
			c.emitAt(OpGetLocal, stmt.Token.Line, stmt.Token.Column, resolved.slot)
		} else {
			c.emitAt(OpGetGlobal, stmt.Token.Line, stmt.Token.Column, resolved.slot)
		}
		hasNamedArgs := false
		for _, arg := range call.Arguments {
			if arg.Name != nil {
				hasNamedArgs = true
			}
			if err := c.compileExpression(arg.Value); err != nil {
				return err
			}
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: name})
		if !hasNamedArgs {
			c.emitAt(OpDeferMethodCall, stmt.Token.Line, stmt.Token.Column, nameIndex, int64(len(call.Arguments)))
			return nil
		}
		operands := []int64{nameIndex, int64(len(call.Arguments))}
		for _, arg := range call.Arguments {
			operands = append(operands, c.argNameIndex(arg))
		}
		c.emitAt(OpDeferMethodCallNamed, stmt.Token.Line, stmt.Token.Column, operands...)
		return nil
	}
	// Resolve any `import path as natpath` alias to its canonical name
	// before dispatching. Unaliased calls map to themselves.
	canonical := c.canonicalModule(module)
	// Keep dedicated opcodes for io.print/println (they write to stdout directly).
	if canonical == "io" && len(call.Arguments) == 1 && call.Arguments[0].Name == nil {
		if err := c.compileExpression(call.Arguments[0].Value); err != nil {
			return err
		}
		switch name {
		case "println":
			c.emitAt(OpDeferPrintln, stmt.Token.Line, stmt.Token.Column)
			return nil
		case "print", "stdoutWrite":
			c.emitAt(OpDeferPrint, stmt.Token.Line, stmt.Token.Column)
			return nil
		}
	}
	if !isBytecodeCallableModule(canonical) {
		return fmt.Errorf("bytecode compiler cannot defer calls to module %q", module)
	}
	hasNamedArgs := false
	for _, arg := range call.Arguments {
		if arg.Name != nil {
			hasNamedArgs = true
		}
		if err := c.compileExpression(arg.Value); err != nil {
			return err
		}
	}
	nameIndex := int64(len(c.chunk.Constants))
	c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: canonical + "." + name})
	if !hasNamedArgs {
		c.emitAt(OpDeferNativeCall, stmt.Token.Line, stmt.Token.Column, nameIndex, int64(len(call.Arguments)))
		return nil
	}
	operands := []int64{nameIndex, int64(len(call.Arguments))}
	for _, arg := range call.Arguments {
		operands = append(operands, c.argNameIndex(arg))
	}
	c.emitAt(OpDeferNativeCallNamed, stmt.Token.Line, stmt.Token.Column, operands...)
	return nil
}

// argNameIndex returns the constant-pool index of a String holding the
// argument's keyword name, or -1 when the argument is positional. Used
// by the OpDefer*CallNamed encoders.
func (c *Compiler) argNameIndex(arg ast.CallArgument) int64 {
	if arg.Name == nil {
		return -1
	}
	idx := int64(len(c.chunk.Constants))
	c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: arg.Name.Value})
	return idx
}

func (c *Compiler) pushFinalizer(ctx finalizerContext) {
	c.finalizers = append(c.finalizers, ctx)
}

func (c *Compiler) popFinalizer() {
	c.finalizers = c.finalizers[:len(c.finalizers)-1]
}

func (c *Compiler) compileActiveFinalizers(line int, column int, includeIteratorFinalizers bool) error {
	for i := len(c.finalizers) - 1; i >= 0; i-- {
		ctx := c.finalizers[i]
		if ctx.hasIterSlot && includeIteratorFinalizers {
			c.emitAt(OpIterClose, line, column, ctx.iterSlot)
		}
		if ctx.popHandler {
			c.emitAt(OpPopExceptionHandler, line, column)
		}
		if ctx.hasWithCleanup {
			c.emitAt(OpGetLocal, line, column, ctx.withSlot)
			c.emitAt(OpWithExit, line, column)
		}
		if ctx.body != nil {
			if err := c.compileBlock(ctx.body); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Compiler) compileIfStatement(stmt *ast.IfStatement) error {
	jumpIfFalse, err := c.compileConditionAndJumpIfFalse(stmt.Condition, stmt.Token.Line, stmt.Token.Column)
	if err != nil {
		return err
	}
	if err := c.compileBlock(stmt.Consequence); err != nil {
		return err
	}
	endJumps := []int{c.emitJump(OpJump, stmt.Token.Line, stmt.Token.Column)}
	c.patchJump(jumpIfFalse)
	for _, elseif := range stmt.ElseIfs {
		branchJump, err := c.compileConditionAndJumpIfFalse(elseif.Condition, stmt.Token.Line, stmt.Token.Column)
		if err != nil {
			return err
		}
		if err := c.compileBlock(elseif.Body); err != nil {
			return err
		}
		endJumps = append(endJumps, c.emitJump(OpJump, stmt.Token.Line, stmt.Token.Column))
		c.patchJump(branchJump)
	}
	if stmt.Alternative != nil {
		if err := c.compileBlock(stmt.Alternative); err != nil {
			return err
		}
	}
	for _, jump := range endJumps {
		c.patchJump(jump)
	}
	return nil
}

func (c *Compiler) compileSelectStatement(stmt *ast.SelectStatement) error {
	c.pushScope()
	defer c.popScope()
	line, col := stmt.Token.Line, stmt.Token.Column
	for _, sc := range stmt.Cases {
		if err := c.compileExpression(sc.Channel); err != nil {
			return err
		}
		hIdx := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "_h"})
		c.emitAt(OpGetField, sc.Token.Line, sc.Token.Column, hIdx)
		if sc.Kind == "send" {
			if err := c.compileExpression(sc.Value); err != nil {
				return err
			}
		}
	}
	hasDefault := int64(0)
	if stmt.Default != nil {
		hasDefault = 1
	}
	operands := []int64{int64(len(stmt.Cases)), hasDefault}
	bodyOffsetPositions := make([]int, 0, len(stmt.Cases)+1)
	bindingSlots := make([]int64, 0, len(stmt.Cases))
	for _, sc := range stmt.Cases {
		kindCode := int64(0)
		if sc.Kind == "send" {
			kindCode = 1
		}
		operands = append(operands, kindCode)
		// Placeholder for body offset; patched after body emitted.
		bodyOffsetPositions = append(bodyOffsetPositions, len(operands))
		operands = append(operands, -1)
		bindingSlot := int64(-1)
		if sc.Kind == "recv" && sc.Binding != "" {
			bindingSlot = c.allocateLocal()
		}
		bindingSlots = append(bindingSlots, bindingSlot)
		operands = append(operands, bindingSlot)
	}
	if hasDefault == 1 {
		bodyOffsetPositions = append(bodyOffsetPositions, len(operands))
		operands = append(operands, -1)
	}
	c.emitAt(OpSelect, line, col, operands...)
	selectIPos := len(c.chunk.Instructions) - 1
	endJumps := []int{}
	for i, sc := range stmt.Cases {
		c.chunk.Instructions[selectIPos].Operands[bodyOffsetPositions[i]] = int64(len(c.chunk.Instructions))
		c.pushScope()
		if sc.Kind == "recv" && sc.Binding != "" {
			c.scopes[len(c.scopes)-1][sc.Binding] = binding{kind: "local", slot: bindingSlots[i]}
		}
		if sc.Body != nil {
			if err := c.compileBlock(sc.Body); err != nil {
				c.popScope()
				return err
			}
		}
		c.popScope()
		endJumps = append(endJumps, c.emitJump(OpJump, sc.Token.Line, sc.Token.Column))
	}
	if hasDefault == 1 {
		c.chunk.Instructions[selectIPos].Operands[bodyOffsetPositions[len(stmt.Cases)]] = int64(len(c.chunk.Instructions))
		if err := c.compileBlock(stmt.Default); err != nil {
			return err
		}
		endJumps = append(endJumps, c.emitJump(OpJump, line, col))
	}
	for _, j := range endJumps {
		c.patchJump(j)
	}
	return nil
}

func (c *Compiler) compileMatchStatement(stmt *ast.MatchStatement) error {
	c.pushScope()
	defer c.popScope()
	if err := c.compileExpression(stmt.Expr); err != nil {
		return err
	}
	valueSlot := c.allocateLocal()
	c.emitAt(OpDefineLocal, stmt.Token.Line, stmt.Token.Column, valueSlot)
	endJumps := []int{}
	for _, matchCase := range stmt.Cases {
		c.pushScope()
		nextJumps, err := c.compileMatchCaseCondition(valueSlot, matchCase)
		if err != nil {
			c.popScope()
			return err
		}
		if matchCase.Body != nil {
			if err := c.compileBlock(matchCase.Body); err != nil {
				c.popScope()
				return err
			}
		}
		c.popScope()
		endJumps = append(endJumps, c.emitJump(OpJump, matchCase.Token.Line, matchCase.Token.Column))
		for _, nextJump := range nextJumps {
			c.patchJump(nextJump)
		}
	}
	hint := matchExhaustivenessHint(stmt.Cases)
	hintIndex := int64(len(c.chunk.Constants))
	c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: hint})
	c.emitAt(OpGetLocal, stmt.Token.Line, stmt.Token.Column, valueSlot)
	c.emitAt(OpMatchError, stmt.Token.Line, stmt.Token.Column, hintIndex)
	for _, jump := range endJumps {
		c.patchJump(jump)
	}
	return nil
}

func (c *Compiler) compileBlock(block *ast.BlockStatement) error {
	c.pushScope()
	defer c.popScope()
	for _, stmt := range block.Statements {
		if err := c.compileStatement(stmt); err != nil {
			return c.withStatementLocation(stmt, err)
		}
	}
	return nil
}

// parityError marks a compiler failure that is a VM capability gap rather
// than a genuine static error in the user's code. IsParityError detects it.
type parityError struct{ err error }

func (e parityError) Error() string { return e.err.Error() }
func (e parityError) Unwrap() error { return e.err }

func parityErrorf(format string, args ...any) error {
	return parityError{err: fmt.Errorf(format, args...)}
}

// NewParityError wraps err as a VM capability gap so that IsParityError
// returns true. Intended for tests that need to synthesize a parity error.
func NewParityError(err error) error { return parityError{err: err} }

type locatedError struct {
	line   int
	column int
	err    error
}

func (e locatedError) Error() string {
	return fmt.Sprintf("line %d:%d: %v", e.line, e.column, e.err)
}

func (e locatedError) Unwrap() error {
	return e.err
}

func (c *Compiler) withStatementLocation(stmt ast.Statement, err error) error {
	if err == nil {
		return nil
	}
	var located locatedError
	if errors.As(err, &located) {
		return err
	}
	line, column := statementLocation(stmt)
	if line <= 0 {
		return err
	}
	return locatedError{line: line, column: column, err: err}
}

func statementLocation(stmt ast.Statement) (int, int) {
	switch stmt := stmt.(type) {
	case *ast.BlockStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.ModuleStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.ImportStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.ExportStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.InitStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.DeclarationStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.ExpressionStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.ReturnStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.YieldStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.SimpleStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.IfStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.WhileStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.ForStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.FunctionStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.ClassStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.InterfaceStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.EnumStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.TryStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.WithStatement:
		return stmt.Token.Line, stmt.Token.Column
	case *ast.MatchStatement:
		return stmt.Token.Line, stmt.Token.Column
	default:
		return 0, 0
	}
}

func (c *Compiler) compileMatchExpression(expr *ast.MatchExpression) error {
	c.pushScope()
	defer c.popScope()
	if err := c.compileExpression(expr.Expr); err != nil {
		return err
	}
	valueSlot := c.allocateLocal()
	c.emitAt(OpDefineLocal, expr.Token.Line, expr.Token.Column, valueSlot)
	endJumps := []int{}
	for _, matchCase := range expr.Cases {
		c.pushScope()
		nextJumps, err := c.compileMatchCaseCondition(valueSlot, matchCase)
		if err != nil {
			c.popScope()
			return err
		}
		if matchCase.Value == nil {
			c.emitConstant(runtime.Null{}, matchCase.Token.Line, matchCase.Token.Column)
		} else if err := c.compileExpression(matchCase.Value); err != nil {
			c.popScope()
			return err
		}
		c.popScope()
		endJumps = append(endJumps, c.emitJump(OpJump, matchCase.Token.Line, matchCase.Token.Column))
		for _, nextJump := range nextJumps {
			c.patchJump(nextJump)
		}
	}
	hint := matchExhaustivenessHint(expr.Cases)
	hintIndex := int64(len(c.chunk.Constants))
	c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: hint})
	c.emitAt(OpGetLocal, expr.Token.Line, expr.Token.Column, valueSlot)
	c.emitAt(OpMatchError, expr.Token.Line, expr.Token.Column, hintIndex)
	for _, jump := range endJumps {
		c.patchJump(jump)
	}
	return nil
}

func matchExhaustivenessHint(cases []ast.MatchCase) string {
	for _, mc := range cases {
		if mc.Default && mc.Guard == nil {
			return "no match case matched"
		}
	}
	return "no match case matched (add a 'default:' case to handle all values)"
}

func (c *Compiler) compileMatchCaseCondition(valueSlot int64, matchCase ast.MatchCase) ([]int, error) {
	if matchCase.Default {
		if matchCase.Guard == nil {
			return nil, nil
		}
		jp, err := c.compileConditionAndJumpIfFalse(matchCase.Guard, matchCase.Token.Line, matchCase.Token.Column)
		if err != nil {
			return nil, err
		}
		return []int{jp}, nil
	}
	line, col := matchCase.Token.Line, matchCase.Token.Column
	c.emitAt(OpGetLocal, line, col, valueSlot)
	switch {
	case matchCase.EnumVariant != nil:
		ev := matchCase.EnumVariant
		typeName := ev.Enum.Value + "." + ev.Variant.Value
		c.emitConstant(runtime.String{Value: typeName}, line, col)
		c.emitAt(OpInstanceOf, line, col)
	case matchCase.Type != nil:
		c.emitConstant(runtime.String{Value: matchCase.Type.String()}, line, col)
		c.emitAt(OpInstanceOf, line, col)
	case matchCase.ListPattern != nil:
		c.emitAt(OpMatchListShape, line, col, int64(len(matchCase.ListPattern.Bindings)))
	case matchCase.Pattern != nil:
		if err := c.compileExpression(matchCase.Pattern); err != nil {
			return nil, err
		}
		c.emitAt(OpEqual, line, col)
	default:
		return nil, fmt.Errorf("match case has no pattern")
	}
	var nextJumps []int
	bodyJumps := []int{}
	if len(matchCase.Alternates) > 0 {
		primaryFail := c.emitJump(OpJumpIfFalse, line, col)
		bodyJumps = append(bodyJumps, c.emitJump(OpJump, line, col))
		c.patchJump(primaryFail)
		for i, alt := range matchCase.Alternates {
			c.emitAt(OpGetLocal, line, col, valueSlot)
			if err := c.emitOrAlternateCheck(alt, line, col); err != nil {
				return nil, err
			}
			if i == len(matchCase.Alternates)-1 {
				nextJumps = append(nextJumps, c.emitJump(OpJumpIfFalse, line, col))
			} else {
				altFail := c.emitJump(OpJumpIfFalse, line, col)
				bodyJumps = append(bodyJumps, c.emitJump(OpJump, line, col))
				c.patchJump(altFail)
			}
		}
		for _, jp := range bodyJumps {
			c.patchJump(jp)
		}
	} else {
		nextJumps = []int{c.emitJump(OpJumpIfFalse, line, col)}
	}
	if matchCase.EnumVariant != nil {
		for i, param := range matchCase.EnumVariant.Params {
			if param.Name != nil {
				c.emitAt(OpGetLocal, line, col, valueSlot)
				idxName := strconv.Itoa(i)
				nameIndex := int64(len(c.chunk.Constants))
				c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: idxName})
				c.emitAt(OpGetField, line, col, nameIndex)
				c.emitAt(OpDefineLocal, line, col, c.defineLocal(param.Name.Value))
			}
		}
	}
	if matchCase.ListPattern != nil {
		for i, binding := range matchCase.ListPattern.Bindings {
			isWildcard := binding.Name == nil || binding.Name.Value == "_"
			if binding.Type == nil && isWildcard {
				continue
			}
			c.emitAt(OpGetLocal, line, col, valueSlot)
			c.emitConstant(runtime.SmallInt{Value: int64(i)}, line, col)
			c.emitAt(OpIndex, line, col)
			if binding.Type != nil {
				c.emitConstant(runtime.String{Value: binding.Type.Name}, line, col)
				c.emitAt(OpInstanceOf, line, col)
				nextJumps = append(nextJumps, c.emitJump(OpJumpIfFalse, line, col))
				if !isWildcard {
					c.emitAt(OpGetLocal, line, col, valueSlot)
					c.emitConstant(runtime.SmallInt{Value: int64(i)}, line, col)
					c.emitAt(OpIndex, line, col)
				}
			}
			if isWildcard {
				continue
			}
			c.emitAt(OpDefineLocal, line, col, c.defineLocal(binding.Name.Value))
		}
	}
	if matchCase.Name != nil {
		c.emitAt(OpGetLocal, matchCase.Token.Line, matchCase.Token.Column, valueSlot)
		c.emitAt(OpDefineLocal, matchCase.Token.Line, matchCase.Token.Column, c.defineLocal(matchCase.Name.Value))
	}
	if matchCase.Guard != nil {
		jp, err := c.compileConditionAndJumpIfFalse(matchCase.Guard, matchCase.Token.Line, matchCase.Token.Column)
		if err != nil {
			return nil, err
		}
		nextJumps = append(nextJumps, jp)
	}
	return nextJumps, nil
}

// emitOrAlternateCheck emits the type-or-equality check for a single
// or-pattern alternate. The value to test is already on the stack.
// Bare-type alternates (e.g. `int`, stored as an Identifier) emit
// OpInstanceOf; everything else evaluates the expression and emits OpEqual.
func (c *Compiler) emitOrAlternateCheck(alt ast.Expression, line, col int) error {
	if ident, ok := alt.(*ast.Identifier); ok && isOrAlternateBuiltinType(ident.Value) {
		c.emitConstant(runtime.String{Value: ident.Value}, line, col)
		c.emitAt(OpInstanceOf, line, col)
		return nil
	}
	if err := c.compileExpression(alt); err != nil {
		return err
	}
	c.emitAt(OpEqual, line, col)
	return nil
}

func isOrAlternateBuiltinType(name string) bool {
	switch name {
	case "string", "int", "float", "decimal", "bool", "bytes", "list", "dict", "set", "range", "null":
		return true
	}
	return false
}

func (c *Compiler) patchLoopContinues(loop loopContext, target int) {
	for _, jump := range loop.continueJumps {
		c.chunk.Instructions[jump].Operands[0] = int64(target)
	}
}

// Canonicalize + resolveName-gate exactly like compileBuiltinCall so an aliased
// io.print/println (value-less opcode) skips the trailing OpPop too.
func (c *Compiler) expressionLeavesNoValue(expr ast.Expression) bool {
	call, ok := expr.(*ast.CallExpression)
	if !ok {
		return false
	}
	module, name, ok := selectorName(call.Callee)
	if !ok {
		return false
	}
	if _, resolved := c.resolveName(module); resolved {
		return false
	}
	return c.canonicalModule(module) == "io" && (name == "println" || name == "print" || name == "stdoutWrite")
}

func containsParentConstructorCall(block *ast.BlockStatement) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Statements {
		if statementContainsParentConstructorCall(stmt) {
			return true
		}
	}
	return false
}

func statementContainsParentConstructorCall(stmt ast.Statement) bool {
	switch stmt := stmt.(type) {
	case *ast.BlockStatement:
		return containsParentConstructorCall(stmt)
	case *ast.ExportStatement:
		return statementContainsParentConstructorCall(stmt.Statement)
	case *ast.DeclarationStatement:
		return expressionContainsParentConstructorCall(stmt.Value)
	case *ast.ExpressionStatement:
		return expressionContainsParentConstructorCall(stmt.Expression)
	case *ast.ReturnStatement:
		return expressionContainsParentConstructorCall(stmt.Value)
	case *ast.YieldStatement:
		return expressionContainsParentConstructorCall(stmt.Value)
	case *ast.SimpleStatement:
		return expressionContainsParentConstructorCall(stmt.Value)
	case *ast.IfStatement:
		if expressionContainsParentConstructorCall(stmt.Condition) || containsParentConstructorCall(stmt.Consequence) || containsParentConstructorCall(stmt.Alternative) {
			return true
		}
		for _, elseif := range stmt.ElseIfs {
			if expressionContainsParentConstructorCall(elseif.Condition) || containsParentConstructorCall(elseif.Body) {
				return true
			}
		}
	case *ast.WhileStatement:
		return expressionContainsParentConstructorCall(stmt.Condition) || containsParentConstructorCall(stmt.Body)
	case *ast.ForStatement:
		return statementContainsParentConstructorCall(stmt.Init) ||
			expressionContainsParentConstructorCall(stmt.Condition) ||
			statementContainsParentConstructorCall(stmt.Update) ||
			expressionContainsParentConstructorCall(stmt.Iterable) ||
			expressionContainsParentConstructorCall(stmt.Step) ||
			containsParentConstructorCall(stmt.Body)
	case *ast.MatchStatement:
		if expressionContainsParentConstructorCall(stmt.Expr) {
			return true
		}
		for _, matchCase := range stmt.Cases {
			if expressionContainsParentConstructorCall(matchCase.Pattern) ||
				expressionContainsParentConstructorCall(matchCase.Guard) ||
				expressionContainsParentConstructorCall(matchCase.Value) ||
				containsParentConstructorCall(matchCase.Body) {
				return true
			}
		}
	case *ast.TryStatement:
		if containsParentConstructorCall(stmt.Body) || containsParentConstructorCall(stmt.Finally) {
			return true
		}
		for _, catch := range stmt.Catches {
			if containsParentConstructorCall(catch.Body) {
				return true
			}
		}
	case *ast.WithStatement:
		if expressionContainsParentConstructorCall(stmt.Value) || containsParentConstructorCall(stmt.Body) {
			return true
		}
	}
	return false
}

func expressionContainsParentConstructorCall(expr ast.Expression) bool {
	switch expr := expr.(type) {
	case nil:
		return false
	case *ast.CallExpression:
		if ident, ok := expr.Callee.(*ast.Identifier); ok && strings.EqualFold(ident.Value, "parent") {
			return true
		}
		if expressionContainsParentConstructorCall(expr.Callee) {
			return true
		}
		for _, arg := range expr.Arguments {
			if expressionContainsParentConstructorCall(arg.Value) {
				return true
			}
		}
	case *ast.PrefixExpression:
		return expressionContainsParentConstructorCall(expr.Right)
	case *ast.PostfixExpression:
		return expressionContainsParentConstructorCall(expr.Left)
	case *ast.InfixExpression:
		return expressionContainsParentConstructorCall(expr.Left) || expressionContainsParentConstructorCall(expr.Right)
	case *ast.AssignmentExpression:
		return expressionContainsParentConstructorCall(expr.Left) || expressionContainsParentConstructorCall(expr.Value)
	case *ast.SelectorExpression:
		return expressionContainsParentConstructorCall(expr.Object)
	case *ast.IndexExpression:
		return expressionContainsParentConstructorCall(expr.Left) || expressionContainsParentConstructorCall(expr.Index)
	case *ast.ListLiteral:
		for _, element := range expr.Elements {
			if expressionContainsParentConstructorCall(element) {
				return true
			}
		}
	case *ast.DictLiteral:
		for _, entry := range expr.Entries {
			if expressionContainsParentConstructorCall(entry.Key) || expressionContainsParentConstructorCall(entry.Value) {
				return true
			}
		}
	case *ast.SetLiteral:
		for _, element := range expr.Elements {
			if expressionContainsParentConstructorCall(element) {
				return true
			}
		}
	case *ast.RangeExpression:
		return expressionContainsParentConstructorCall(expr.Start) ||
			expressionContainsParentConstructorCall(expr.End) ||
			expressionContainsParentConstructorCall(expr.Step)
	case *ast.FunctionLiteral:
		return false
	case *ast.MatchExpression:
		if expressionContainsParentConstructorCall(expr.Expr) {
			return true
		}
		for _, matchCase := range expr.Cases {
			if expressionContainsParentConstructorCall(matchCase.Pattern) ||
				expressionContainsParentConstructorCall(matchCase.Guard) ||
				expressionContainsParentConstructorCall(matchCase.Value) ||
				containsParentConstructorCall(matchCase.Body) {
				return true
			}
		}
	case *ast.SpreadExpression:
		return expressionContainsParentConstructorCall(expr.Value)
	case *ast.AwaitExpression:
		return expressionContainsParentConstructorCall(expr.Value)
	case *ast.CastExpression:
		return expressionContainsParentConstructorCall(expr.Value)
	case *ast.TernaryExpression:
		return expressionContainsParentConstructorCall(expr.Condition) ||
			expressionContainsParentConstructorCall(expr.ThenExpr) ||
			expressionContainsParentConstructorCall(expr.ElseExpr)
	}
	return false
}

func (c *Compiler) compileExpression(expr ast.Expression) error {
	return c.compileExpressionWithExpected(expr, "")
}

func (c *Compiler) compileExpressionWithExpected(expr ast.Expression, expected string) error {
	c.expectedTypes = append(c.expectedTypes, expected)
	defer func() {
		c.expectedTypes = c.expectedTypes[:len(c.expectedTypes)-1]
	}()
	return c.compileExpressionInner(expr)
}

func (c *Compiler) compileExpressionInner(expr ast.Expression) error {
	switch expr := expr.(type) {
	case *ast.IntegerLiteral:
		value, err := runtime.NewIntLiteral(expr.Value)
		if err != nil {
			return err
		}
		if value.Value.IsInt64() {
			c.emitConstant(runtime.SmallInt{Value: value.Value.Int64()}, expr.Token.Line, expr.Token.Column)
		} else {
			c.emitConstant(value, expr.Token.Line, expr.Token.Column)
		}
		return nil
	case *ast.DecimalLiteral:
		value, err := runtime.NewDecimalLiteral(expr.Value)
		if err != nil {
			return err
		}
		c.emitConstant(value, expr.Token.Line, expr.Token.Column)
		return nil
	case *ast.FloatLiteral:
		stripped := strings.ReplaceAll(expr.Value[:len(expr.Value)-1], "_", "")
		value, err := strconv.ParseFloat(stripped, 64)
		if err != nil {
			return fmt.Errorf("invalid float literal %q", expr.Value)
		}
		c.emitConstant(runtime.Float{Value: value}, expr.Token.Line, expr.Token.Column)
		return nil
	case *ast.StringLiteral:
		c.emitConstant(runtime.String{Value: expr.Value}, expr.Token.Line, expr.Token.Column)
		return nil
	case *ast.InterpolatedString:
		if len(expr.Parts) == 0 {
			c.emitConstant(runtime.String{Value: ""}, expr.Token.Line, expr.Token.Column)
			return nil
		}
		for i, part := range expr.Parts {
			if err := c.compileExpression(part); err != nil {
				return err
			}
			_, isStr := part.(*ast.StringLiteral)
			_, isFmt := part.(*ast.FormattedInterpolation)
			if !isStr && !isFmt {
				// cast expression result to string using the same path as `as string`
				c.emitConstant(runtime.String{Value: "string"}, expr.Token.Line, expr.Token.Column)
				c.emitAt(OpCast, expr.Token.Line, expr.Token.Column)
			}
			if i > 0 {
				c.emitAt(OpAdd, expr.Token.Line, expr.Token.Column)
			}
		}
		return nil
	case *ast.FormattedInterpolation:
		if err := c.compileExpression(expr.Value); err != nil {
			return err
		}
		c.emitConstant(runtime.String{Value: expr.Spec}, expr.Token.Line, expr.Token.Column)
		c.emitAt(OpFormatSpec, expr.Token.Line, expr.Token.Column)
		return nil
	case *ast.Literal:
		switch value := expr.Value.(type) {
		case bool:
			c.emitConstant(runtime.Bool{Value: value}, expr.Token.Line, expr.Token.Column)
			return nil
		case nil:
			c.emitConstant(runtime.Null{}, expr.Token.Line, expr.Token.Column)
			return nil
		default:
			return parityErrorf("bytecode compiler does not support literal %T", value)
		}
	case *ast.ListLiteral:
		if !listHasSpread(expr.Elements) {
			for _, element := range expr.Elements {
				if err := c.compileExpression(element); err != nil {
					return err
				}
			}
			c.emitAt(OpBuildList, expr.Token.Line, expr.Token.Column, int64(len(expr.Elements)))
			return nil
		}
		segCount := int64(0)
		segStart := 0
		for i, element := range expr.Elements {
			spread, ok := element.(*ast.SpreadExpression)
			if !ok {
				continue
			}
			if i > segStart {
				for _, e := range expr.Elements[segStart:i] {
					if err := c.compileExpression(e); err != nil {
						return err
					}
				}
				c.emitAt(OpBuildList, expr.Token.Line, expr.Token.Column, int64(i-segStart))
				segCount++
			}
			if err := c.compileExpression(spread.Value); err != nil {
				return err
			}
			segCount++
			segStart = i + 1
		}
		if segStart < len(expr.Elements) {
			for _, e := range expr.Elements[segStart:] {
				if err := c.compileExpression(e); err != nil {
					return err
				}
			}
			c.emitAt(OpBuildList, expr.Token.Line, expr.Token.Column, int64(len(expr.Elements)-segStart))
			segCount++
		}
		c.emitAt(OpListConcat, expr.Token.Line, expr.Token.Column, segCount)
		return nil
	case *ast.SpreadExpression:
		return fmt.Errorf("spread expression is only valid inside a list literal or function call")

	case *ast.DictLiteral:
		if dictHasSpread(expr.Entries) {
			return c.compileExpression(desugarDictLiteralWithSpread(expr))
		}
		for _, entry := range expr.Entries {
			if err := c.compileExpression(entry.Key); err != nil {
				return err
			}
			if err := c.compileExpression(entry.Value); err != nil {
				return err
			}
		}
		c.emitAt(OpBuildDict, expr.Token.Line, expr.Token.Column, int64(len(expr.Entries)))
		return nil
	case *ast.SetLiteral:
		if setHasSpread(expr.Elements) {
			return c.compileExpression(desugarSetLiteralWithSpread(expr))
		}
		for _, element := range expr.Elements {
			if err := c.compileExpression(element); err != nil {
				return err
			}
		}
		c.emitAt(OpBuildSet, expr.Token.Line, expr.Token.Column, int64(len(expr.Elements)))
		return nil
	case *ast.PipeExpression:
		call, ok := ast.LowerPipe(expr)
		if !ok {
			return fmt.Errorf("`|>` right side must be a call, identifier, or selector")
		}
		return c.compileExpression(call)
	case *ast.ListComprehension:
		return c.compileExpression(desugarListComprehension(expr))
	case *ast.SetComprehension:
		return c.compileExpression(desugarSetComprehension(expr))
	case *ast.DictComprehension:
		return c.compileExpression(desugarDictComprehension(expr))
	case *ast.RangeExpression:
		if expr.Start == nil {
			c.emitConstant(runtime.Null{}, expr.Token.Line, expr.Token.Column)
		} else if err := c.compileExpression(expr.Start); err != nil {
			return err
		}
		if expr.End == nil {
			c.emitConstant(runtime.Null{}, expr.Token.Line, expr.Token.Column)
		} else if err := c.compileExpression(expr.End); err != nil {
			return err
		}
		if expr.Step == nil {
			c.emitConstant(runtime.Null{}, expr.Token.Line, expr.Token.Column)
		} else if err := c.compileExpression(expr.Step); err != nil {
			return err
		}
		if expr.Exclusive {
			c.emitAt(OpBuildRange, expr.Token.Line, expr.Token.Column, 1)
		} else {
			c.emitAt(OpBuildRange, expr.Token.Line, expr.Token.Column, 0)
		}
		return nil
	case *ast.IndexExpression:
		if err := c.compileExpression(expr.Left); err != nil {
			return err
		}
		if rng, ok := expr.Index.(*ast.RangeExpression); ok {
			if rng.Start == nil {
				c.emitConstant(runtime.Null{}, expr.Token.Line, expr.Token.Column)
			} else if err := c.compileExpression(rng.Start); err != nil {
				return err
			}
			if rng.End == nil {
				c.emitConstant(runtime.Null{}, expr.Token.Line, expr.Token.Column)
			} else if err := c.compileExpression(rng.End); err != nil {
				return err
			}
			if rng.Step == nil {
				c.emitConstant(runtime.Null{}, expr.Token.Line, expr.Token.Column)
			} else if err := c.compileExpression(rng.Step); err != nil {
				return err
			}
			if rng.Exclusive {
				c.emitAt(OpSlice, expr.Token.Line, expr.Token.Column, 1)
			} else {
				c.emitAt(OpSlice, expr.Token.Line, expr.Token.Column, 0)
			}
			return nil
		}
		if err := c.compileExpression(expr.Index); err != nil {
			return err
		}
		c.emitAt(OpIndex, expr.Token.Line, expr.Token.Column)
		return nil
	case *ast.Identifier:
		resolved, ok := c.resolveName(expr.Value)
		if !ok {
			if classIndex, found := c.classes[strings.ToLower(expr.Value)]; found && int(classIndex) < len(c.chunk.Classes) && c.chunk.Classes[classIndex].Name == expr.Value {
				classInfo := c.chunk.Classes[classIndex]
				c.emitConstant(runtime.BytecodeClass{
					Name:             classInfo.Name,
					Doc:              classInfo.Doc,
					Index:            classIndex,
					Decorators:       classInfo.Decorators,
					MethodDecorators: classInfo.MethodDecorators,
					StaticDecorators: classInfo.StaticDecorators,
				}, expr.Token.Line, expr.Token.Column)
				return nil
			}
			// Allow a named top-level function to be used as a value via a zero-upvalue closure.
			if indices, found := c.funcs[strings.ToLower(expr.Value)]; found && len(indices) > 0 {
				c.emitAt(OpMakeClosure, expr.Token.Line, expr.Token.Column, indices[len(indices)-1], 0)
				return nil
			}
			if runtime.IsBuiltinTypeName(expr.Value) {
				c.emitConstant(runtime.Type{Name: strings.ToLower(expr.Value)}, expr.Token.Line, expr.Token.Column)
				return nil
			}
			return fmt.Errorf("unknown bytecode name %s", expr.Value)
		}
		if resolved.kind == "local" {
			c.emitAt(OpGetLocal, expr.Token.Line, expr.Token.Column, resolved.slot)
		} else {
			c.emitAt(OpGetGlobal, expr.Token.Line, expr.Token.Column, resolved.slot)
		}
		return nil
	case *ast.InfixExpression:
		if expr.Operator == "instanceof" {
			if err := c.compileExpression(expr.Left); err != nil {
				return err
			}
			typeName, err := typeNameFromExpression(expr.Right)
			if err != nil {
				return err
			}
			c.emitConstant(runtime.String{Value: typeName}, expr.Token.Line, expr.Token.Column)
			c.emitAt(OpInstanceOf, expr.Token.Line, expr.Token.Column)
			return nil
		}
		if expr.Operator == "is" || expr.Operator == "is not" {
			if err := c.compileExpression(expr.Left); err != nil {
				return err
			}
			if err := c.compileExpression(expr.Right); err != nil {
				return err
			}
			c.emitAt(OpIdentical, expr.Token.Line, expr.Token.Column)
			if expr.Operator == "is not" {
				c.emitAt(OpNot, expr.Token.Line, expr.Token.Column)
			}
			return nil
		}
		if expr.Operator == "&&" || expr.Operator == "||" {
			return c.compileShortCircuitExpression(expr)
		}
		if expr.Operator == "??" {
			return c.compileNullCoalesceExpression(expr)
		}
		if folded, ok, err := foldConstantBinary(expr.Operator, expr.Left, expr.Right); ok {
			if err != nil {
				return fmt.Errorf("%d:%d: %s", expr.Token.Line, expr.Token.Column, err.Error())
			}
			c.emitConstant(folded, expr.Token.Line, expr.Token.Column)
			return nil
		}
		bothInt := c.staticIntExpr(expr.Left) && c.staticIntExpr(expr.Right)
		bothString := !bothInt && expr.Operator == "+" && c.staticStringExpr(expr.Left) && c.staticStringExpr(expr.Right)
		// `acc + "literal"` (the common string-builder pattern): bake
		// the literal's constant index into a specialised opcode so
		// the runtime only pops one operand instead of two.
		if bothString {
			if rightLit, ok := expr.Right.(*ast.StringLiteral); ok {
				if !isStringLiteral(expr.Left) {
					if err := c.compileExpression(expr.Left); err != nil {
						return err
					}
					constIdx := int64(len(c.chunk.Constants))
					c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: rightLit.Value})
					c.emitAt(OpAddStringConst, expr.Token.Line, expr.Token.Column, constIdx)
					return nil
				}
			}
		}
		if err := c.compileExpression(expr.Left); err != nil {
			return err
		}
		if err := c.compileExpression(expr.Right); err != nil {
			return err
		}
		switch expr.Operator {
		case "in":
			c.emitAt(OpContains, expr.Token.Line, expr.Token.Column)
		case "+":
			if bothInt {
				c.emitAt(OpAddInt, expr.Token.Line, expr.Token.Column)
			} else if bothString {
				c.emitAt(OpAddString, expr.Token.Line, expr.Token.Column)
			} else {
				c.emitAt(OpAdd, expr.Token.Line, expr.Token.Column)
			}
		case "-":
			if bothInt {
				c.emitAt(OpSubInt, expr.Token.Line, expr.Token.Column)
			} else {
				c.emitAt(OpSub, expr.Token.Line, expr.Token.Column)
			}
		case "*":
			if bothInt {
				c.emitAt(OpMulInt, expr.Token.Line, expr.Token.Column)
			} else {
				c.emitAt(OpMul, expr.Token.Line, expr.Token.Column)
			}
		case "/":
			c.emitAt(OpDiv, expr.Token.Line, expr.Token.Column)
		case "//":
			c.emitAt(OpIntDiv, expr.Token.Line, expr.Token.Column)
		case "%":
			if bothInt {
				c.emitAt(OpModInt, expr.Token.Line, expr.Token.Column)
			} else {
				c.emitAt(OpMod, expr.Token.Line, expr.Token.Column)
			}
		case "**":
			c.emitAt(OpPow, expr.Token.Line, expr.Token.Column)
		case "==":
			if bothInt {
				c.emitAt(OpEqualInt, expr.Token.Line, expr.Token.Column)
			} else {
				c.emitAt(OpEqual, expr.Token.Line, expr.Token.Column)
			}
		case "!=":
			if bothInt {
				c.emitAt(OpEqualInt, expr.Token.Line, expr.Token.Column)
			} else {
				c.emitAt(OpEqual, expr.Token.Line, expr.Token.Column)
			}
			c.emitAt(OpNot, expr.Token.Line, expr.Token.Column)
		case "<":
			if bothInt {
				c.emitAt(OpLessInt, expr.Token.Line, expr.Token.Column)
			} else {
				c.emitAt(OpLess, expr.Token.Line, expr.Token.Column)
			}
		case "<=":
			if bothInt {
				c.emitAt(OpLessEqualInt, expr.Token.Line, expr.Token.Column)
			} else {
				c.emitAt(OpLessEqual, expr.Token.Line, expr.Token.Column)
			}
		case ">":
			if bothInt {
				c.emitAt(OpGreaterInt, expr.Token.Line, expr.Token.Column)
			} else {
				c.emitAt(OpGreater, expr.Token.Line, expr.Token.Column)
			}
		case ">=":
			if bothInt {
				c.emitAt(OpGreaterEqualInt, expr.Token.Line, expr.Token.Column)
			} else {
				c.emitAt(OpGreaterEqual, expr.Token.Line, expr.Token.Column)
			}
		case "xor":
			c.emitAt(OpBoolXor, expr.Token.Line, expr.Token.Column)
		case "&":
			c.emitAt(OpBitAnd, expr.Token.Line, expr.Token.Column)
		case "|":
			c.emitAt(OpBitOr, expr.Token.Line, expr.Token.Column)
		case "^":
			c.emitAt(OpBitXor, expr.Token.Line, expr.Token.Column)
		case "<<":
			c.emitAt(OpLShift, expr.Token.Line, expr.Token.Column)
		case ">>":
			c.emitAt(OpRShift, expr.Token.Line, expr.Token.Column)
		default:
			return parityErrorf("bytecode compiler does not support operator %q", expr.Operator)
		}
		return nil
	case *ast.PrefixExpression:
		if err := c.compileExpression(expr.Right); err != nil {
			return err
		}
		switch expr.Operator {
		case "!":
			c.emitAt(OpNot, expr.Token.Line, expr.Token.Column)
		case "-":
			c.emitAt(OpNegate, expr.Token.Line, expr.Token.Column)
		case "~":
			c.emitAt(OpBitNot, expr.Token.Line, expr.Token.Column)
		default:
			return parityErrorf("bytecode compiler does not support prefix operator %q", expr.Operator)
		}
		return nil
	case *ast.PostfixExpression:
		ident, ok := expr.Left.(*ast.Identifier)
		if !ok {
			return fmt.Errorf("bytecode compiler only supports identifier postfix increments")
		}
		resolved, ok := c.resolveName(ident.Value)
		if !ok {
			return fmt.Errorf("unknown bytecode name %s", ident.Value)
		}
		if resolved.typ == "int" {
			switch expr.Operator {
			case "++":
				if resolved.kind == "local" {
					c.emitAt(OpIncLocalInt, expr.Token.Line, expr.Token.Column, resolved.slot)
				} else {
					c.emitAt(OpIncGlobalInt, expr.Token.Line, expr.Token.Column, resolved.slot)
				}
				return nil
			case "--":
				if resolved.kind == "local" {
					c.emitAt(OpDecLocalInt, expr.Token.Line, expr.Token.Column, resolved.slot)
				} else {
					c.emitAt(OpDecGlobalInt, expr.Token.Line, expr.Token.Column, resolved.slot)
				}
				return nil
			}
		}
		if resolved.kind == "local" {
			c.emitAt(OpGetLocal, expr.Token.Line, expr.Token.Column, resolved.slot)
		} else {
			c.emitAt(OpGetGlobal, expr.Token.Line, expr.Token.Column, resolved.slot)
		}
		c.emitAt(OpDup, expr.Token.Line, expr.Token.Column)
		c.emitConstant(runtime.SmallInt{Value: 1}, expr.Token.Line, expr.Token.Column)
		switch expr.Operator {
		case "++":
			c.emitAt(OpAdd, expr.Token.Line, expr.Token.Column)
		case "--":
			c.emitAt(OpSub, expr.Token.Line, expr.Token.Column)
		default:
			return parityErrorf("bytecode compiler does not support postfix operator %q", expr.Operator)
		}
		if resolved.kind == "local" {
			c.emitAt(OpSetLocal, expr.Token.Line, expr.Token.Column, resolved.slot)
		} else {
			c.emitAt(OpSetGlobal, expr.Token.Line, expr.Token.Column, resolved.slot)
		}
		c.emitAt(OpPop, expr.Token.Line, expr.Token.Column)
		return nil
	case *ast.AssignmentExpression:
		return c.compileAssignmentExpression(expr)
	case *ast.CallExpression:
		if ident, ok := expr.Callee.(*ast.Identifier); ok {
			if strings.EqualFold(ident.Value, "parent") {
				if len(c.classStack) == 0 {
					return fmt.Errorf("parent is only available inside class methods")
				}
				currentClass := c.chunk.Classes[c.classStack[len(c.classStack)-1]]
				isBuiltinErrorParent := isBuiltinErrorClass(currentClass.ParentName) && (currentClass.ParentIndex < 0 || int(currentClass.ParentIndex) >= len(c.chunk.Classes))
				isCrossModuleParent := !isBuiltinErrorParent && (currentClass.ParentIndex < 0 || int(currentClass.ParentIndex) >= len(c.chunk.Classes)) && strings.Contains(currentClass.ParentName, ".")
				if !isBuiltinErrorParent && !isCrossModuleParent && (currentClass.ParentIndex < 0 || int(currentClass.ParentIndex) >= len(c.chunk.Classes)) {
					return fmt.Errorf("%s has no parent class", currentClass.Name)
				}
				var orderedArgs []ast.Expression
				if !isBuiltinErrorParent && !isCrossModuleParent {
					parent := c.chunk.Classes[currentClass.ParentIndex]
					if len(parent.ConstructorIndices) > 0 {
						var err error
						_, orderedArgs, err = c.selectFunctionIndicesCallIgnoringReturn(parent.Name, parent.ConstructorIndices, expr.Arguments, 1)
						if err != nil {
							return err
						}
					} else {
						orderedArgs = positionalArguments(expr.Arguments)
						if orderedArgs == nil && len(expr.Arguments) > 0 {
							return fmt.Errorf("%s constructor does not accept named arguments", parent.Name)
						}
					}
				} else {
					// Cross-module or builtin-error parent: positional only at
					// compile time. Overload selection (cross-module) happens
					// at runtime via the module loader.
					orderedArgs = positionalArguments(expr.Arguments)
					if orderedArgs == nil && len(expr.Arguments) > 0 {
						return fmt.Errorf("%s constructor does not accept named arguments", currentClass.ParentName)
					}
				}
				resolved, ok := c.resolveName("this")
				if !ok {
					return fmt.Errorf("parent constructor is only available inside instance methods")
				}
				if resolved.kind == "local" {
					c.emitAt(OpGetLocal, expr.Token.Line, expr.Token.Column, resolved.slot)
				} else {
					c.emitAt(OpGetGlobal, expr.Token.Line, expr.Token.Column, resolved.slot)
				}
				for _, arg := range orderedArgs {
					if err := c.compileExpression(arg); err != nil {
						return err
					}
				}
				c.emitAt(OpCallParentConstructor, expr.Token.Line, expr.Token.Column, c.classStack[len(c.classStack)-1], int64(len(orderedArgs)))
				return nil
			}
			if strings.EqualFold(ident.Value, "dir") {
				if len(expr.Arguments) != 1 || expr.Arguments[0].Name != nil {
					return fmt.Errorf("dir(value) expects one positional argument; dir() scope introspection is evaluator/REPL-only")
				}
				if err := c.compileExpression(expr.Arguments[0].Value); err != nil {
					return err
				}
				c.emitAt(OpDir, expr.Token.Line, expr.Token.Column)
				return nil
			}
			if strings.EqualFold(ident.Value, "dump") {
				if len(expr.Arguments) != 1 || expr.Arguments[0].Name != nil {
					return fmt.Errorf("dump expects exactly one positional argument")
				}
				if err := c.compileExpression(expr.Arguments[0].Value); err != nil {
					return err
				}
				c.emitAt(OpDump, expr.Token.Line, expr.Token.Column)
				return nil
			}
			if strings.EqualFold(ident.Value, "typeof") {
				if len(expr.Arguments) != 1 || expr.Arguments[0].Name != nil {
					return fmt.Errorf("typeof expects exactly one positional argument")
				}
				if err := c.compileExpression(expr.Arguments[0].Value); err != nil {
					return err
				}
				c.emitAt(OpTypeOf, expr.Token.Line, expr.Token.Column)
				return nil
			}
			if ident.Value == "assert" {
				return c.compileAssertCall(expr)
			}
			if ident.Value == "range" {
				if len(expr.Arguments) < 2 || len(expr.Arguments) > 3 {
					return fmt.Errorf("range expects (start, end) or (start, end, step)")
				}
				for _, arg := range expr.Arguments {
					if arg.Name != nil {
						return fmt.Errorf("range does not accept named arguments")
					}
					if err := c.compileExpression(arg.Value); err != nil {
						return err
					}
				}
				c.emitAt(OpRange, expr.Token.Line, expr.Token.Column, int64(len(expr.Arguments)))
				return nil
			}
			if isBuiltinErrorClass(ident.Value) {
				if len(expr.Arguments) > 1 {
					return fmt.Errorf("%s expects zero or one argument", ident.Value)
				}
				for _, arg := range expr.Arguments {
					if arg.Name != nil && !strings.EqualFold(arg.Name.Value, "message") {
						return fmt.Errorf("%s has no parameter %s", ident.Value, arg.Name.Value)
					}
					if err := c.compileExpression(arg.Value); err != nil {
						return err
					}
				}
				classIndex := int64(len(c.chunk.Constants))
				c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: ident.Value})
				c.emitAt(OpMakeError, expr.Token.Line, expr.Token.Column, classIndex, int64(len(expr.Arguments)))
				return nil
			}
			/* Class dispatch is case-sensitive at the call site: when
			 * `view(...)` is called we must not bind to a `View`
			 * class. The compiler's internal storage keys are
			 * lowercased but the language distinguishes case. */
			if classIndex, ok := c.classes[strings.ToLower(ident.Value)]; ok && c.chunk.Classes[classIndex].Name == ident.Value {
				classInfo := c.chunk.Classes[classIndex]
				if c.classExtendsBuiltinError(classInfo) && len(classInfo.FieldNames) == 0 && len(classInfo.ConstructorIndices) == 0 {
					if len(expr.Arguments) > 1 {
						return fmt.Errorf("%s expects zero or one argument", ident.Value)
					}
					for _, arg := range expr.Arguments {
						if arg.Name != nil && !strings.EqualFold(arg.Name.Value, "message") {
							return fmt.Errorf("%s has no parameter %s", ident.Value, arg.Name.Value)
						}
						if err := c.compileExpression(arg.Value); err != nil {
							return err
						}
					}
					classNameIndex := int64(len(c.chunk.Constants))
					c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: classInfo.Name})
					c.emitAt(OpMakeError, expr.Token.Line, expr.Token.Column, classNameIndex, int64(len(expr.Arguments)))
					return nil
				}
				var orderedArgs []ast.Expression
				var functionIndex int64
				if len(classInfo.ConstructorIndices) > 0 {
					var err error
					functionIndex, orderedArgs, err = c.selectFunctionIndicesCallIgnoringReturn(classInfo.Name, classInfo.ConstructorIndices, expr.Arguments, 1)
					if err != nil {
						return err
					}
				} else {
					orderedArgs = positionalArguments(expr.Arguments)
					if orderedArgs == nil && len(expr.Arguments) > 0 {
						return fmt.Errorf("%s constructor does not accept named arguments", classInfo.Name)
					}
				}
				if len(classInfo.ConstructorIndices) > 0 {
					if err := c.compileOrderedArguments(c.chunk.Functions[functionIndex], orderedArgs, 1, expr.Token.Line, expr.Token.Column); err != nil {
						return err
					}
				} else {
					for _, arg := range orderedArgs {
						if err := c.compileExpression(arg); err != nil {
							return err
						}
					}
				}
				c.emitAt(OpConstructClass, expr.Token.Line, expr.Token.Column, classIndex, int64(len(orderedArgs)))
				// Explicit type arguments on the call site (e.g. `Container<T>(...)`)
				// pin the reified bindings on the freshly-constructed instance, so
				// runtime invariance checks against a typed parameter see the
				// caller's intent rather than only those bindings inferred from
				// constructor argument types.
				if len(expr.TypeArguments) > 0 && len(classInfo.TypeParameters) > 0 {
					operands := []int64{0}
					count := int64(0)
					for i, arg := range expr.TypeArguments {
						if i >= len(classInfo.TypeParameters) {
							break
						}
						if arg == nil || arg.Operator != "" || arg.Name == "" {
							continue
						}
						paramNameIdx := int64(len(c.chunk.Constants))
						c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: classInfo.TypeParameters[i]})
						typeNameIdx := int64(len(c.chunk.Constants))
						c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: arg.Name})
						operands = append(operands, paramNameIdx, typeNameIdx)
						count++
					}
					if count > 0 {
						operands[0] = count
						c.emitAt(OpSetTypeBindings, expr.Token.Line, expr.Token.Column, operands...)
					}
				}
				return nil
			}
			if resolved, ok := c.resolveName(ident.Value); ok {
				if resolved.kind == "local" {
					c.emitAt(OpGetLocal, expr.Token.Line, expr.Token.Column, resolved.slot)
				} else {
					c.emitAt(OpGetGlobal, expr.Token.Line, expr.Token.Column, resolved.slot)
				}
				if spreadIndex, hasSpread := callSpreadIndex(expr.Arguments); hasSpread {
					for _, arg := range expr.Arguments[:spreadIndex] {
						if arg.Name != nil {
							return fmt.Errorf("named arguments are not supported with spread on a callable value")
						}
						if err := c.compileExpression(arg.Value); err != nil {
							return err
						}
					}
					if err := c.compileExpression(expr.Arguments[spreadIndex].Value); err != nil {
						return err
					}
					nameIndex := int64(len(c.chunk.Constants))
					c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "__invoke"})
					c.emitAt(OpMethodCallSpread, expr.Token.Line, expr.Token.Column, nameIndex, int64(spreadIndex))
					return nil
				}
				hasNamedArgs := false
				for _, arg := range expr.Arguments {
					hasNamedArgs = hasNamedArgs || arg.Name != nil
					if err := c.compileExpression(arg.Value); err != nil {
						return err
					}
				}
				nameIndex := int64(len(c.chunk.Constants))
				c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "__invoke"})
				if hasNamedArgs {
					operands := []int64{nameIndex, int64(len(expr.Arguments))}
					for _, arg := range expr.Arguments {
						argNameIndex := int64(-1)
						if arg.Name != nil {
							argNameIndex = int64(len(c.chunk.Constants))
							c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: arg.Name.Value})
						}
						operands = append(operands, argNameIndex)
					}
					c.emitAt(OpMethodCallNamed, expr.Token.Line, expr.Token.Column, operands...)
					return nil
				}
				c.emitAt(OpMethodCall, expr.Token.Line, expr.Token.Column, nameIndex, int64(len(expr.Arguments)))
				return nil
			}
			if spreadIndex, hasSpread := callSpreadIndex(expr.Arguments); hasSpread {
				index, err := c.selectFunctionCallSpread(ident.Value)
				if err != nil {
					return err
				}
				for _, arg := range expr.Arguments[:spreadIndex] {
					if err := c.compileExpression(arg.Value); err != nil {
						return err
					}
				}
				if err := c.compileExpression(expr.Arguments[spreadIndex].Value); err != nil {
					return err
				}
				c.emitAt(OpCallSpread, expr.Token.Line, expr.Token.Column, index, int64(spreadIndex))
				return nil
			}
			index, orderedArgs, err := c.selectFunctionCall(ident.Value, expr.Arguments, 0)
			if err != nil {
				return err
			}
			if err := c.compileOrderedArguments(c.chunk.Functions[index], orderedArgs, 0, expr.Token.Line, expr.Token.Column); err != nil {
				return err
			}
			c.emitPlantCallTypeBindings(expr, c.chunk.Functions[index].TypeParameters)
			c.emitAt(OpCall, expr.Token.Line, expr.Token.Column, index, int64(len(orderedArgs)))
			return nil
		}
		if selector, ok := expr.Callee.(*ast.SelectorExpression); ok && selector.Parenthesized {
			/* `(obj.fn)(args)` invokes the value of obj.fn rather
			 * than dispatching as a method call on obj. Compile
			 * the selector as a value, push args, OpMethodCall
			 * __invoke. */
			if spreadIndex, hasSpread := callSpreadIndex(expr.Arguments); hasSpread {
				if err := c.compileExpression(expr.Callee); err != nil {
					return err
				}
				for _, arg := range expr.Arguments[:spreadIndex] {
					if arg.Name != nil {
						return fmt.Errorf("named arguments are not supported with spread on a callable value")
					}
					if err := c.compileExpression(arg.Value); err != nil {
						return err
					}
				}
				if err := c.compileExpression(expr.Arguments[spreadIndex].Value); err != nil {
					return err
				}
				nameIndex := int64(len(c.chunk.Constants))
				c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "__invoke"})
				c.emitAt(OpMethodCallSpread, expr.Token.Line, expr.Token.Column, nameIndex, int64(spreadIndex))
				return nil
			}
			if err := c.compileExpression(expr.Callee); err != nil {
				return err
			}
			hasNamedArgs := false
			for _, arg := range expr.Arguments {
				hasNamedArgs = hasNamedArgs || arg.Name != nil
				if err := c.compileExpression(arg.Value); err != nil {
					return err
				}
			}
			nameIndex := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "__invoke"})
			if hasNamedArgs {
				operands := []int64{nameIndex, int64(len(expr.Arguments))}
				for _, arg := range expr.Arguments {
					argNameIndex := int64(-1)
					if arg.Name != nil {
						argNameIndex = int64(len(c.chunk.Constants))
						c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: arg.Name.Value})
					}
					operands = append(operands, argNameIndex)
				}
				c.emitAt(OpMethodCallNamed, expr.Token.Line, expr.Token.Column, operands...)
				return nil
			}
			c.emitAt(OpMethodCall, expr.Token.Line, expr.Token.Column, nameIndex, int64(len(expr.Arguments)))
			return nil
		}
		if selector, ok := expr.Callee.(*ast.SelectorExpression); ok {
			if object, ok := selector.Object.(*ast.Identifier); ok && strings.EqualFold(object.Value, "parent") {
				if len(c.classStack) == 0 {
					return fmt.Errorf("parent is only available inside class methods")
				}
				currentClass := c.chunk.Classes[c.classStack[len(c.classStack)-1]]
				isCrossModuleParent := (currentClass.ParentIndex < 0 || int(currentClass.ParentIndex) >= len(c.chunk.Classes)) && strings.Contains(currentClass.ParentName, ".")
				if !isCrossModuleParent && (currentClass.ParentIndex < 0 || int(currentClass.ParentIndex) >= len(c.chunk.Classes)) {
					return fmt.Errorf("%s has no parent class", currentClass.Name)
				}
				var orderedArgs []ast.Expression
				crossModuleAncestor := ""
				if !isCrossModuleParent {
					parent := c.chunk.Classes[currentClass.ParentIndex]
					indices, ok := c.lookupMethod(parent, selector.Name.Value)
					if !ok {
						// Method may live in a cross-module ancestor above this
						// same-chunk parent; defer overload selection to runtime.
						if qualified, boundary := c.crossModuleBoundary(parent); boundary {
							crossModuleAncestor = qualified
						} else {
							return fmt.Errorf("unknown parent method %s.%s", parent.Name, selector.Name.Value)
						}
					} else {
						var err error
						_, orderedArgs, err = c.selectFunctionIndicesCall(selector.Name.Value, indices, expr.Arguments, 1)
						if err != nil {
							return err
						}
					}
				}
				if isCrossModuleParent || crossModuleAncestor != "" {
					// Cross-module parent method: positional args; overload
					// selection runs in the parent module's VM at runtime.
					orderedArgs = positionalArguments(expr.Arguments)
					if orderedArgs == nil && len(expr.Arguments) > 0 {
						ancestor := currentClass.ParentName
						if crossModuleAncestor != "" {
							ancestor = crossModuleAncestor
						}
						return fmt.Errorf("parent method %s.%s does not accept named arguments", ancestor, selector.Name.Value)
					}
				}
				resolved, ok := c.resolveName("this")
				if !ok {
					return fmt.Errorf("parent method is only available inside instance methods")
				}
				if resolved.kind == "local" {
					c.emitAt(OpGetLocal, expr.Token.Line, expr.Token.Column, resolved.slot)
				} else {
					c.emitAt(OpGetGlobal, expr.Token.Line, expr.Token.Column, resolved.slot)
				}
				for _, arg := range orderedArgs {
					if err := c.compileExpression(arg); err != nil {
						return err
					}
				}
				nameIndex := int64(len(c.chunk.Constants))
				c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: selector.Name.Value})
				c.emitAt(OpCallParentMethod, expr.Token.Line, expr.Token.Column, c.classStack[len(c.classStack)-1], nameIndex, int64(len(orderedArgs)))
				return nil
			}
			if object, ok := selector.Object.(*ast.Identifier); ok {
				if _, resolvedName := c.resolveName(object.Value); !resolvedName {
					if classIndex, ok := c.classes[strings.ToLower(object.Value)]; ok && c.chunk.Classes[classIndex].Name == object.Value {
						indices, ok := c.lookupStaticMethod(c.chunk.Classes[classIndex], selector.Name.Value)
						var orderedArgs []ast.Expression
						var functionIndex int64
						var err error
						if ok {
							functionIndex, orderedArgs, err = c.selectFunctionIndicesCall(selector.Name.Value, indices, expr.Arguments, 0)
							if err != nil {
								return err
							}
							if err := c.compileOrderedArguments(c.chunk.Functions[functionIndex], orderedArgs, 0, expr.Token.Line, expr.Token.Column); err != nil {
								return err
							}
						} else {
							orderedArgs = make([]ast.Expression, 0, len(expr.Arguments))
							for _, arg := range expr.Arguments {
								orderedArgs = append(orderedArgs, arg.Value)
							}
							for _, arg := range orderedArgs {
								if err := c.compileExpression(arg); err != nil {
									return err
								}
							}
						}
						nameIndex := int64(len(c.chunk.Constants))
						c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: selector.Name.Value})
						c.emitAt(OpCallStaticMethod, expr.Token.Line, expr.Token.Column, classIndex, nameIndex, int64(len(orderedArgs)))
						return nil
					}
					if enumConstIndex, ok := c.enums[strings.ToLower(object.Value)]; ok {
						c.emitAt(OpConstant, expr.Token.Line, expr.Token.Column, enumConstIndex)
						for _, arg := range expr.Arguments {
							if err := c.compileExpression(arg.Value); err != nil {
								return err
							}
						}
						nameIndex := int64(len(c.chunk.Constants))
						c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: selector.Name.Value})
						c.emitAt(OpMethodCall, expr.Token.Line, expr.Token.Column, nameIndex, int64(len(expr.Arguments)))
						return nil
					}
				}
			}
			if module, name, ok := selectorName(expr.Callee); ok {
				canonical := c.canonicalModule(module)
				if isBytecodeCallableModule(canonical) {
					// resolveName uses the *original* identifier so a local
					// variable shadowing the alias still wins over module
					// dispatch (the established precedence).
					if _, resolved := c.resolveName(module); !resolved {
						return c.compileBuiltinCall(expr, canonical, name)
					}
				}
			}
			hasNamedMethodArgs := false
			for _, arg := range expr.Arguments {
				if arg.Name != nil {
					hasNamedMethodArgs = true
					break
				}
			}
			if !selector.Optional && !hasNamedMethodArgs {
				if fnIndex, ok := c.staticallyResolveMethodCall(selector, expr.Arguments); ok {
					if err := c.compileExpression(selector.Object); err != nil {
						return err
					}
					for _, arg := range expr.Arguments {
						if err := c.compileExpression(arg.Value); err != nil {
							return err
						}
					}
					c.emitAt(OpCallResolvedMethod, expr.Token.Line, expr.Token.Column, fnIndex, int64(len(expr.Arguments)))
					return nil
				}
			}
			if err := c.compileExpression(selector.Object); err != nil {
				return err
			}
			var optionalJump int
			if selector.Optional {
				optionalJump = c.emitJump(OpOptionalChain, expr.Token.Line, expr.Token.Column)
			}
			for _, arg := range expr.Arguments {
				if err := c.compileExpression(arg.Value); err != nil {
					return err
				}
			}
			nameIndex := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: selector.Name.Value})
			if hasNamedMethodArgs {
				operands := []int64{nameIndex, int64(len(expr.Arguments))}
				for _, arg := range expr.Arguments {
					argNameIndex := int64(-1)
					if arg.Name != nil {
						argNameIndex = int64(len(c.chunk.Constants))
						c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: arg.Name.Value})
					}
					operands = append(operands, argNameIndex)
				}
				c.emitAt(OpMethodCallNamed, expr.Token.Line, expr.Token.Column, operands...)
				if selector.Optional {
					c.patchJump(optionalJump)
				}
				return nil
			}
			c.emitAt(OpMethodCall, expr.Token.Line, expr.Token.Column, nameIndex, int64(len(expr.Arguments)))
			if selector.Optional {
				c.patchJump(optionalJump)
			}
			return nil
		}
		if spreadIndex, hasSpread := callSpreadIndex(expr.Arguments); hasSpread {
			if err := c.compileExpression(expr.Callee); err != nil {
				return err
			}
			for _, arg := range expr.Arguments[:spreadIndex] {
				if arg.Name != nil {
					return fmt.Errorf("named arguments are not supported with spread on a callable value")
				}
				if err := c.compileExpression(arg.Value); err != nil {
					return err
				}
			}
			if err := c.compileExpression(expr.Arguments[spreadIndex].Value); err != nil {
				return err
			}
			nameIndex := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "__invoke"})
			c.emitAt(OpMethodCallSpread, expr.Token.Line, expr.Token.Column, nameIndex, int64(spreadIndex))
			return nil
		}
		if err := c.compileExpression(expr.Callee); err != nil {
			return err
		}
		hasNamedArgs := false
		for _, arg := range expr.Arguments {
			hasNamedArgs = hasNamedArgs || arg.Name != nil
			if err := c.compileExpression(arg.Value); err != nil {
				return err
			}
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "__invoke"})
		if hasNamedArgs {
			operands := []int64{nameIndex, int64(len(expr.Arguments))}
			for _, arg := range expr.Arguments {
				argNameIndex := int64(-1)
				if arg.Name != nil {
					argNameIndex = int64(len(c.chunk.Constants))
					c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: arg.Name.Value})
				}
				operands = append(operands, argNameIndex)
			}
			c.emitAt(OpMethodCallNamed, expr.Token.Line, expr.Token.Column, operands...)
			return nil
		}
		c.emitAt(OpMethodCall, expr.Token.Line, expr.Token.Column, nameIndex, int64(len(expr.Arguments)))
		return nil
	case *ast.SelectorExpression:
		if expr.Name.Value == "type" && !expr.Optional {
			if ident, ok := expr.Object.(*ast.Identifier); ok {
				if _, resolvedName := c.resolveName(ident.Value); !resolvedName {
					c.emitConstant(runtime.Type{Name: ident.Value}, expr.Token.Line, expr.Token.Column)
					return nil
				}
			}
			// Resolved variable or non-identifier: evaluate and get type at runtime.
			if err := c.compileExpression(expr.Object); err != nil {
				return err
			}
			c.emitAt(OpTypeOf, expr.Token.Line, expr.Token.Column)
			return nil
		}
		if !expr.Optional {
			if object, ok := expr.Object.(*ast.Identifier); ok {
				if _, resolvedName := c.resolveName(object.Value); !resolvedName {
					if classIndex, ok := c.classes[strings.ToLower(object.Value)]; ok && c.chunk.Classes[classIndex].Name == object.Value {
						nameIndex := int64(len(c.chunk.Constants))
						c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: expr.Name.Value})
						c.emitAt(OpGetStaticValue, expr.Token.Line, expr.Token.Column, classIndex, nameIndex)
						return nil
					}
					if enumConstIndex, ok := c.enums[strings.ToLower(object.Value)]; ok {
						c.emitAt(OpConstant, expr.Token.Line, expr.Token.Column, enumConstIndex)
						nameIndex := int64(len(c.chunk.Constants))
						c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: expr.Name.Value})
						c.emitAt(OpGetField, expr.Token.Line, expr.Token.Column, nameIndex)
						return nil
					}
					// An imported native module's function as a first-class value
					// (math.abs without calling it). Native modules are not runtime
					// values, so emit a dedicated push.
					if _, imported := c.moduleAliases[object.Value]; imported {
						canonical := c.canonicalModule(object.Value)
						if native.IsPureBuiltin(canonical, expr.Name.Value) {
							canonicalIdx := int64(len(c.chunk.Constants))
							c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: canonical})
							nameIdx := int64(len(c.chunk.Constants))
							c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: expr.Name.Value})
							c.emitAt(OpNativeValue, expr.Token.Line, expr.Token.Column, canonicalIdx, nameIdx)
							return nil
						}
					}
				}
			}
		}
		if err := c.compileExpression(expr.Object); err != nil {
			return err
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: expr.Name.Value})
		if expr.Optional {
			endJump := c.emitJump(OpOptionalChain, expr.Token.Line, expr.Token.Column)
			c.emitAt(OpGetField, expr.Token.Line, expr.Token.Column, nameIndex)
			c.patchJump(endJump)
		} else {
			c.emitAt(OpGetField, expr.Token.Line, expr.Token.Column, nameIndex)
		}
		return nil
	case *ast.CastExpression:
		if err := c.compileExpression(expr.Value); err != nil {
			return err
		}
		c.emitConstant(runtime.String{Value: c.bytecodeTypeName(expr.Type)}, expr.Token.Line, expr.Token.Column)
		c.emitAt(OpCast, expr.Token.Line, expr.Token.Column)
		return nil
	case *ast.MatchExpression:
		return c.compileMatchExpression(expr)
	case *ast.FunctionLiteral:
		return c.compileFunctionLiteral(expr)
	case *ast.AwaitExpression:
		if err := c.compileExpression(expr.Value); err != nil {
			return err
		}
		c.emitAt(OpAwait, expr.Token.Line, expr.Token.Column)
		return nil
	case *ast.TernaryExpression:
		return c.compileTernaryExpression(expr)
	default:
		return parityErrorf("bytecode compiler does not support %T yet", expr)
	}
}

func (c *Compiler) compileTernaryExpression(node *ast.TernaryExpression) error {
	jumpFalsePos, err := c.compileConditionAndJumpIfFalse(node.Condition, node.Token.Line, node.Token.Column)
	if err != nil {
		return err
	}
	if err := c.compileExpression(node.ThenExpr); err != nil {
		return err
	}
	jumpPos := c.emitJump(OpJump, node.Token.Line, node.Token.Column)
	c.patchJump(jumpFalsePos)
	if err := c.compileExpression(node.ElseExpr); err != nil {
		return err
	}
	c.patchJump(jumpPos)
	return nil
}

// freeVarSet walks an AST node collecting identifiers that are used but not
// declared within the subtree. Nested function literals are handled: their own
// free variables are propagated up unless they are locally shadowed.
type freeVarSet struct {
	defined map[string]bool
	free    map[string]bool
}

func newFreeVarSet(params []ast.Parameter) *freeVarSet {
	s := &freeVarSet{defined: map[string]bool{}, free: map[string]bool{}}
	for _, p := range params {
		if p.Name != nil {
			// Identifiers in Geblang are case-sensitive (locals are
			// looked up by exact name in the enclosing scope). The
			// scanner used to lowercase here, which silently
			// dropped captured upvalues whose names contained
			// uppercase letters - the closure body then emitted
			// `OpGetLocal <outer-slot>` that, at runtime, indexed
			// into the closure's own locals frame at the wrong slot.
			s.defined[p.Name.Value] = true
		}
	}
	return s
}

func (s *freeVarSet) scanBlock(block *ast.BlockStatement) {
	if block == nil {
		return
	}
	for _, stmt := range block.Statements {
		s.scanStatement(stmt)
	}
}

func (s *freeVarSet) scanStatement(stmt ast.Statement) {
	if stmt == nil {
		return
	}
	switch stmt := stmt.(type) {
	case *ast.DeclarationStatement:
		if stmt.Value != nil {
			s.scanExpr(stmt.Value)
		}
		if stmt.Name != nil {
			s.defined[stmt.Name.Value] = true
		}
	case *ast.ReturnStatement:
		if stmt.Value != nil {
			s.scanExpr(stmt.Value)
		}
	case *ast.YieldStatement:
		if stmt.Value != nil {
			s.scanExpr(stmt.Value)
		}
	case *ast.ExpressionStatement:
		s.scanExpr(stmt.Expression)
	case *ast.IfStatement:
		s.scanExpr(stmt.Condition)
		s.scanBlock(stmt.Consequence)
		for _, branch := range stmt.ElseIfs {
			s.scanExpr(branch.Condition)
			s.scanBlock(branch.Body)
		}
		s.scanBlock(stmt.Alternative)
	case *ast.WhileStatement:
		s.scanExpr(stmt.Condition)
		s.scanBlock(stmt.Body)
	case *ast.ForStatement:
		s.scanStatement(stmt.Init)
		s.scanExpr(stmt.Condition)
		s.scanStatement(stmt.Update)
		if stmt.VarName != nil {
			s.defined[stmt.VarName.Value] = true
		}
		for _, v := range stmt.VarNames {
			if v != nil {
				s.defined[v.Value] = true
			}
		}
		s.scanExpr(stmt.Iterable)
		s.scanExpr(stmt.Step)
		s.scanBlock(stmt.Body)
	case *ast.TryStatement:
		s.scanBlock(stmt.Body)
		for _, catch := range stmt.Catches {
			if catch.Name != nil {
				inner := &freeVarSet{defined: map[string]bool{catch.Name.Value: true}, free: map[string]bool{}}
				for k := range s.defined {
					inner.defined[k] = true
				}
				inner.scanBlock(catch.Body)
				for name := range inner.free {
					if !s.defined[name] {
						s.free[name] = true
					}
				}
			} else {
				s.scanBlock(catch.Body)
			}
		}
		s.scanBlock(stmt.Finally)
	case *ast.FunctionStatement:
		if stmt.Name != nil {
			s.defined[stmt.Name.Value] = true
		}
		inner := newFreeVarSet(stmt.Parameters)
		inner.scanBlock(stmt.Body)
		for name := range inner.free {
			if !s.defined[name] {
				s.free[name] = true
			}
		}
	case *ast.SimpleStatement:
		s.scanExpr(stmt.Value)
	case *ast.MatchStatement:
		s.scanExpr(stmt.Expr)
		for _, mc := range stmt.Cases {
			s.scanExpr(mc.Pattern)
			s.scanExpr(mc.Guard)
			s.scanExpr(mc.Value)
			s.scanBlock(mc.Body)
		}
	case *ast.ClassStatement:
		if stmt.Name != nil {
			s.defined[stmt.Name.Value] = true
		}
	case *ast.WithStatement:
		s.scanExpr(stmt.Value)
		if stmt.Name != nil {
			inner := &freeVarSet{defined: map[string]bool{stmt.Name.Value: true}, free: map[string]bool{}}
			for k := range s.defined {
				inner.defined[k] = true
			}
			inner.scanBlock(stmt.Body)
			for name := range inner.free {
				if !s.defined[name] {
					s.free[name] = true
				}
			}
		} else {
			s.scanBlock(stmt.Body)
		}
	}
}

func (s *freeVarSet) scanExpr(expr ast.Expression) {
	if expr == nil {
		return
	}
	switch expr := expr.(type) {
	case *ast.Identifier:
		name := expr.Value
		if !s.defined[name] {
			s.free[name] = true
		}
	case *ast.FunctionLiteral:
		inner := newFreeVarSet(expr.Parameters)
		inner.scanBlock(expr.Body)
		for name := range inner.free {
			if !s.defined[name] {
				s.free[name] = true
			}
		}
	case *ast.InfixExpression:
		s.scanExpr(expr.Left)
		s.scanExpr(expr.Right)
	case *ast.PrefixExpression:
		s.scanExpr(expr.Right)
	case *ast.PostfixExpression:
		s.scanExpr(expr.Left)
	case *ast.AssignmentExpression:
		s.scanExpr(expr.Left)
		s.scanExpr(expr.Value)
	case *ast.CallExpression:
		s.scanExpr(expr.Callee)
		for _, arg := range expr.Arguments {
			s.scanExpr(arg.Value)
		}
	case *ast.SelectorExpression:
		s.scanExpr(expr.Object)
	case *ast.IndexExpression:
		s.scanExpr(expr.Left)
		s.scanExpr(expr.Index)
	case *ast.ListLiteral:
		for _, el := range expr.Elements {
			s.scanExpr(el)
		}
	case *ast.DictLiteral:
		for _, entry := range expr.Entries {
			s.scanExpr(entry.Key)
			s.scanExpr(entry.Value)
		}
	case *ast.SetLiteral:
		for _, el := range expr.Elements {
			s.scanExpr(el)
		}
	case *ast.RangeExpression:
		s.scanExpr(expr.Start)
		s.scanExpr(expr.End)
		s.scanExpr(expr.Step)
	case *ast.MatchExpression:
		s.scanExpr(expr.Expr)
		for _, mc := range expr.Cases {
			s.scanExpr(mc.Pattern)
			s.scanExpr(mc.Guard)
			s.scanExpr(mc.Value)
			s.scanBlock(mc.Body)
		}
	case *ast.SpreadExpression:
		s.scanExpr(expr.Value)
	case *ast.CastExpression:
		s.scanExpr(expr.Value)
	case *ast.AwaitExpression:
		s.scanExpr(expr.Value)
	case *ast.TernaryExpression:
		s.scanExpr(expr.Condition)
		s.scanExpr(expr.ThenExpr)
		s.scanExpr(expr.ElseExpr)
	}
}

func (c *Compiler) compileFunctionLiteral(expr *ast.FunctionLiteral) error {

	// Collect free variables: identifiers used in body not declared as params.
	scanner := newFreeVarSet(expr.Parameters)
	scanner.scanBlock(expr.Body)

	// Resolve which free variables are local upvalues vs. globals.
	type upvalueCapture struct {
		name      string
		outerSlot int64
	}
	var captures []upvalueCapture
	for name := range scanner.free {
		b, ok := c.resolveName(name)
		if !ok || b.kind != "local" {
			continue
		}
		captures = append(captures, upvalueCapture{name: name, outerSlot: b.slot})
	}
	// Sort for deterministic slot assignment.
	for i := 1; i < len(captures); i++ {
		for j := i; j > 0 && captures[j].name < captures[j-1].name; j-- {
			captures[j], captures[j-1] = captures[j-1], captures[j]
		}
	}

	index := c.declareFunction("<closure>")
	skipJump := c.emitJump(OpJump, expr.Token.Line, expr.Token.Column)
	entry := int64(len(c.chunk.Instructions))
	c.pushScope()
	c.pushFunctionLocals()

	// Allocate upvalue slots first (indices 0..N-1 in ParamSlots).
	upvalueCount := int64(len(captures))
	paramSlots := make([]int64, 0, upvalueCount+int64(len(expr.Parameters)))
	paramNames := make([]string, 0, cap(paramSlots))
	paramTypes := make([]string, 0, cap(paramSlots))
	defaultConstants := make([]int64, 0, cap(paramSlots))

	for _, cap := range captures {
		slot := c.defineLocalWithType(cap.name, "")
		paramSlots = append(paramSlots, slot)
		paramNames = append(paramNames, cap.name)
		paramTypes = append(paramTypes, "")
		defaultConstants = append(defaultConstants, -1)
	}

	// Allocate parameter slots.
	for _, param := range expr.Parameters {
		if param.Name == nil {
			c.popFunctionLocals()
			c.popScope()
			return fmt.Errorf("function literal parameter has no name")
		}
		paramType := c.bytecodeTypeName(param.Type)
		slot := c.defineLocalWithType(param.Name.Value, paramType)
		paramSlots = append(paramSlots, slot)
		paramNames = append(paramNames, strings.ToLower(param.Name.Value))
		paramTypes = append(paramTypes, paramType)
		if param.Default == nil {
			defaultConstants = append(defaultConstants, -1)
			continue
		}
		value, err := constantValueFromExpression(param.Default)
		if err != nil {
			c.popFunctionLocals()
			c.popScope()
			return err
		}
		defaultConstants = append(defaultConstants, int64(len(c.chunk.Constants)))
		c.chunk.Constants = append(c.chunk.Constants, value)
	}

	fn := &c.chunk.Functions[index]
	fn.Entry = entry
	fn.ParamNames = paramNames
	fn.ParamSlots = paramSlots
	fn.ParamTypes = paramTypes
	fn.ReturnType = c.bytecodeReturnType(expr.ReturnType)
	fn.DefaultConstants = defaultConstants
	fn.UpvalueCount = upvalueCount
	fn.Variadic = len(expr.Parameters) > 0 && expr.Parameters[len(expr.Parameters)-1].Variadic
	fn.Async = expr.Async
	fn.IsGenerator = blockContainsYield(expr.Body)

	c.inFunc++
	c.returnTypes = append(c.returnTypes, fn.ReturnType)
	// Isolate finalizers (see compileFunctionWithPrologue).
	savedLoops, savedFinalizers := c.loops, c.finalizers
	c.loops, c.finalizers = nil, nil
	defer func() { c.loops, c.finalizers = savedLoops, savedFinalizers }()
	if err := c.compileBlock(expr.Body); err != nil {
		c.inFunc--
		c.returnTypes = c.returnTypes[:len(c.returnTypes)-1]
		c.popFunctionLocals()
		c.popScope()
		return err
	}
	c.emitConstant(runtime.Null{}, expr.Token.Line, expr.Token.Column)
	c.emitAt(OpReturn, expr.Token.Line, expr.Token.Column)
	fn.LocalCount = c.popFunctionLocals()
	c.inFunc--
	c.returnTypes = c.returnTypes[:len(c.returnTypes)-1]
	c.popScope()
	c.patchJump(skipJump)

	// Emit OpMakeClosure: [funcIndex, N, outerSlot0, ..., outerSlotN-1].
	operands := make([]int64, 0, 2+len(captures))
	operands = append(operands, index, upvalueCount)
	for _, cap := range captures {
		operands = append(operands, cap.outerSlot)
	}
	c.emitAt(OpMakeClosure, expr.Token.Line, expr.Token.Column, operands...)
	return nil
}

func (c *Compiler) compileShortCircuitExpression(expr *ast.InfixExpression) error {
	if err := c.compileExpression(expr.Left); err != nil {
		return err
	}
	if expr.Operator == "&&" {
		falseJump := c.emitJump(OpJumpIfFalse, expr.Token.Line, expr.Token.Column)
		if err := c.compileExpression(expr.Right); err != nil {
			return err
		}
		endJump := c.emitJump(OpJump, expr.Token.Line, expr.Token.Column)
		c.patchJump(falseJump)
		c.emitConstant(runtime.Bool{Value: false}, expr.Token.Line, expr.Token.Column)
		c.patchJump(endJump)
		return nil
	}
	rightJump := c.emitJump(OpJumpIfFalse, expr.Token.Line, expr.Token.Column)
	c.emitConstant(runtime.Bool{Value: true}, expr.Token.Line, expr.Token.Column)
	endJump := c.emitJump(OpJump, expr.Token.Line, expr.Token.Column)
	c.patchJump(rightJump)
	if err := c.compileExpression(expr.Right); err != nil {
		return err
	}
	c.patchJump(endJump)
	return nil
}

func (c *Compiler) compileNullCoalesceExpression(expr *ast.InfixExpression) error {
	if err := c.compileExpression(expr.Left); err != nil {
		return err
	}
	// If left is non-null, jump past right side (leaving left on stack).
	endJump := c.emitJump(OpNullCoalesce, expr.Token.Line, expr.Token.Column)
	// Left was null: pop it, then evaluate right side.
	if err := c.compileExpression(expr.Right); err != nil {
		return err
	}
	c.patchJump(endJump)
	return nil
}

func (c *Compiler) compileBuiltinCall(expr *ast.CallExpression, module, name string) error {
	if module == "reflect" {
		return c.compileReflectCall(expr, name)
	}
	switch {
	case module == "io" && name == "println":
		args, err := c.singleBuiltinArgument(expr, module, name)
		if err != nil {
			return err
		}
		if err := c.compileExpression(args[0]); err != nil {
			return err
		}
		c.emitAt(OpPrintln, expr.Token.Line, expr.Token.Column)
		return nil
	case module == "io" && (name == "print" || name == "stdoutWrite"):
		args, err := c.singleBuiltinArgument(expr, module, name)
		if err != nil {
			return err
		}
		if err := c.compileExpression(args[0]); err != nil {
			return err
		}
		c.emitAt(OpPrint, expr.Token.Line, expr.Token.Column)
		return nil
	case module == "sys" && name == "exit":
		args, err := c.singleBuiltinArgument(expr, module, name)
		if err != nil {
			return err
		}
		if err := c.compileExpression(args[0]); err != nil {
			return err
		}
		c.emitAt(OpExit, expr.Token.Line, expr.Token.Column)
		return nil
	case native.IsPureBuiltin(module, name), isStatefulBytecodeBuiltin(module, name):
		if spreadIndex, hasSpread := callSpreadIndex(expr.Arguments); hasSpread {
			for _, arg := range expr.Arguments[:spreadIndex] {
				if arg.Name != nil {
					return fmt.Errorf("named arguments are not supported with spread on %s.%s", module, name)
				}
				if err := c.compileExpression(arg.Value); err != nil {
					return err
				}
			}
			if err := c.compileExpression(expr.Arguments[spreadIndex].Value); err != nil {
				return err
			}
			nameIndex := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: module + "." + name})
			c.emitAt(OpNativeCallSpread, expr.Token.Line, expr.Token.Column, nameIndex, int64(spreadIndex))
			return nil
		}
		hasNamedArgs := false
		for _, arg := range expr.Arguments {
			if arg.Name != nil {
				hasNamedArgs = true
			}
			if err := c.compileExpression(arg.Value); err != nil {
				return err
			}
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: module + "." + name})
		if !hasNamedArgs {
			c.emitAt(OpNativeCall, expr.Token.Line, expr.Token.Column, nameIndex, int64(len(expr.Arguments)))
			return nil
		}
		operands := []int64{nameIndex, int64(len(expr.Arguments))}
		for _, arg := range expr.Arguments {
			argName := ""
			if arg.Name != nil {
				argName = arg.Name.Value
			}
			argNameIndex := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: argName})
			operands = append(operands, argNameIndex)
		}
		c.emitAt(OpNativeCallNamed, expr.Token.Line, expr.Token.Column, operands...)
		return nil
	default:
		return parityErrorf("bytecode compiler does not support %s.%s; use --disable-vm to run with the tree-walking evaluator", module, name)
	}
}

func (c *Compiler) compileReflectCall(expr *ast.CallExpression, name string) error {
	switch name {
	case "function", "class":
		if len(expr.Arguments) != 1 {
			return fmt.Errorf("reflect.%s expects exactly one argument", name)
		}
		if literal, ok := expr.Arguments[0].Value.(*ast.StringLiteral); ok {
			value, handled, err := c.reflectNamedTarget(expr, name)
			if err != nil {
				return err
			}
			if handled {
				c.emitConstant(value, expr.Token.Line, expr.Token.Column)
				return nil
			}
			// Qualified `module.export` form goes through the
			// compile-time lookup helper; bare names fall through
			// to the runtime native call so the VM's FindClassByName
			// can resolve them across loaded modules.
			if strings.Contains(literal.Value, ".") {
				argc, err := c.compileReflectQualifiedLookup(expr, name)
				if err != nil {
					return err
				}
				nameIndex := int64(len(c.chunk.Constants))
				c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "reflect." + name})
				c.emitAt(OpNativeCall, expr.Token.Line, expr.Token.Column, nameIndex, int64(argc))
				return nil
			}
			c.emitConstant(runtime.String{Value: literal.Value}, expr.Token.Line, expr.Token.Column)
			nameIndex := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "reflect." + name})
			c.emitAt(OpNativeCall, expr.Token.Line, expr.Token.Column, nameIndex, 1)
			return nil
		}
		if err := c.compileExpression(expr.Arguments[0].Value); err != nil {
			return err
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "reflect." + name})
		c.emitAt(OpNativeCall, expr.Token.Line, expr.Token.Column, nameIndex, 1)
		return nil
	case "module":
		if err := c.compileReflectModuleLookup(expr); err != nil {
			return err
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "reflect.module"})
		c.emitAt(OpNativeCall, expr.Token.Line, expr.Token.Column, nameIndex, 1)
		return nil
	case "method", "staticMethod", "getField", "setField":
		for _, arg := range expr.Arguments {
			if err := c.compileExpression(arg.Value); err != nil {
				return err
			}
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "reflect." + name})
		c.emitAt(OpNativeCall, expr.Token.Line, expr.Token.Column, nameIndex, int64(len(expr.Arguments)))
		return nil
	case "decorators", "hasDecorator", "decorator", "parameters", "returnType", "doc", "docs", "typeOf", "location", "fields", "methods", "staticMethods", "parent", "interfaces", "className", "constructors", "typeBindings", "interfaceMethods", "interfaceParents":
		if reflectSingleValueCall(name) && len(expr.Arguments) != 1 {
			return fmt.Errorf("reflect.%s expects value", name)
		}
		if name == "decorators" && len(expr.Arguments) != 1 && len(expr.Arguments) != 2 {
			return fmt.Errorf("reflect.decorators expects value and optional decorator name")
		}
		if len(expr.Arguments) != 2 && name != "decorators" && !reflectSingleValueCall(name) {
			return fmt.Errorf("reflect.%s expects value and decorator name", name)
		}
		if name == "typeOf" {
			if err := c.compileExpression(expr.Arguments[0].Value); err != nil {
				return err
			}
		} else if name == "interfaceMethods" || name == "interfaceParents" {
			if err := c.compileReflectInterfaceArgument(expr.Arguments[0].Value); err != nil {
				return err
			}
		} else if name == "doc" || name == "docs" {
			if ident, ok := expr.Arguments[0].Value.(*ast.Identifier); ok {
				if _, found := c.interfaces[strings.ToLower(ident.Value)]; found {
					c.emitConstant(runtime.String{Value: ident.Value}, ident.Token.Line, ident.Token.Column)
				} else if err := c.compileReflectTargetArgument(expr.Arguments[0].Value); err != nil {
					return err
				}
			} else if err := c.compileReflectTargetArgument(expr.Arguments[0].Value); err != nil {
				return err
			}
		} else {
			if err := c.compileReflectTargetArgument(expr.Arguments[0].Value); err != nil {
				return err
			}
		}
		for _, arg := range expr.Arguments[1:] {
			if err := c.compileExpression(arg.Value); err != nil {
				return err
			}
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "reflect." + name})
		c.emitAt(OpNativeCall, expr.Token.Line, expr.Token.Column, nameIndex, int64(len(expr.Arguments)))
		return nil
	case "exports":
		if len(expr.Arguments) != 1 {
			return fmt.Errorf("reflect.exports expects module")
		}
		if err := c.compileExpression(expr.Arguments[0].Value); err != nil {
			return err
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "reflect.exports"})
		c.emitAt(OpNativeCall, expr.Token.Line, expr.Token.Column, nameIndex, 1)
		return nil
	case "classes":
		if len(expr.Arguments) != 0 {
			return fmt.Errorf("reflect.classes takes no arguments")
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "reflect.classes"})
		c.emitAt(OpNativeCall, expr.Token.Line, expr.Token.Column, nameIndex, 0)
		return nil
	default:
		return parityErrorf("bytecode compiler does not support reflect.%s", name)
	}
}

func reflectSingleValueCall(name string) bool {
	switch name {
	case "parameters", "returnType", "doc", "docs", "typeOf", "location", "fields", "methods", "staticMethods", "parent", "interfaces", "className", "constructors", "typeBindings", "interfaceMethods", "interfaceParents":
		return true
	default:
		return false
	}
}

func (c *Compiler) compileReflectInterfaceArgument(expr ast.Expression) error {
	if ident, ok := expr.(*ast.Identifier); ok {
		if _, found := c.interfaces[strings.ToLower(ident.Value)]; found {
			c.emitConstant(runtime.String{Value: ident.Value}, ident.Token.Line, ident.Token.Column)
			return nil
		}
	}
	return c.compileExpression(expr)
}

func (c *Compiler) compileReflectModuleLookup(expr *ast.CallExpression) error {
	if len(expr.Arguments) != 1 {
		return fmt.Errorf("reflect.module expects exactly one name")
	}
	nameLiteral, ok := expr.Arguments[0].Value.(*ast.StringLiteral)
	if !ok {
		return fmt.Errorf("reflect.module name must be a string literal in bytecode")
	}
	if strings.Contains(nameLiteral.Value, ".") {
		return fmt.Errorf("reflect.module bytecode name must be an imported module alias")
	}
	resolved, ok := c.resolveName(nameLiteral.Value)
	if !ok {
		c.emitConstant(runtime.Null{}, expr.Token.Line, expr.Token.Column)
		return nil
	}
	if resolved.kind == "local" {
		c.emitAt(OpGetLocal, expr.Token.Line, expr.Token.Column, resolved.slot)
	} else {
		c.emitAt(OpGetGlobal, expr.Token.Line, expr.Token.Column, resolved.slot)
	}
	return nil
}

func (c *Compiler) compileReflectTargetArgument(expr ast.Expression) error {
	if target, ok, err := c.reflectTargetFromExpression(expr); ok || err != nil {
		if err != nil {
			return err
		}
		line, column := expressionLocation(expr)
		c.emitConstant(target, line, column)
		return nil
	}
	return c.compileExpression(expr)
}

func (c *Compiler) reflectTargetFromExpression(expr ast.Expression) (runtime.DecoratorTarget, bool, error) {
	if ident, ok := expr.(*ast.Identifier); ok {
		key := strings.ToLower(ident.Value)
		if target, ok := c.reflectFuncs[key]; ok {
			if err := validateReflectDecorators(target.Decorators); err != nil {
				return runtime.DecoratorTarget{}, false, err
			}
			return target, true, nil
		}
		if target, ok := c.reflectClasses[key]; ok {
			if err := validateReflectDecorators(target.Decorators); err != nil {
				return runtime.DecoratorTarget{}, false, err
			}
			return target, true, nil
		}
	}
	if call, ok := expr.(*ast.CallExpression); ok {
		if module, name, ok := selectorName(call.Callee); ok && module == "reflect" {
			switch name {
			case "function":
				target, handled, err := c.reflectNamedTarget(call, "function")
				if err != nil {
					return runtime.DecoratorTarget{}, true, err
				}
				if !handled {
					return runtime.DecoratorTarget{}, false, nil
				}
				if targetValue, ok := target.(runtime.DecoratorTarget); ok {
					return targetValue, true, nil
				}
				return runtime.DecoratorTarget{}, false, nil
			case "class":
				target, handled, err := c.reflectNamedTarget(call, "class")
				if err != nil {
					return runtime.DecoratorTarget{}, true, err
				}
				if !handled {
					return runtime.DecoratorTarget{}, false, nil
				}
				if targetValue, ok := target.(runtime.DecoratorTarget); ok {
					return targetValue, true, nil
				}
				return runtime.DecoratorTarget{}, false, nil
			case "method":
				return runtime.DecoratorTarget{}, false, nil
			case "staticMethod":
				return runtime.DecoratorTarget{}, false, nil
			}
		}
	}
	return runtime.DecoratorTarget{}, false, nil
}

func (c *Compiler) reflectNamedTarget(expr *ast.CallExpression, name string) (runtime.Value, bool, error) {
	if len(expr.Arguments) != 1 {
		return nil, false, fmt.Errorf("reflect.%s expects exactly one name", name)
	}
	nameLiteral, ok := expr.Arguments[0].Value.(*ast.StringLiteral)
	if !ok {
		return nil, false, fmt.Errorf("reflect.%s name must be a string literal in bytecode", name)
	}
	if strings.Contains(nameLiteral.Value, ".") {
		return nil, false, nil
	}
	key := strings.ToLower(nameLiteral.Value)
	switch name {
	case "function":
		target, ok := c.reflectFuncs[key]
		if !ok {
			// Fall through to the runtime path so the VM can look
			// up the function in another loaded module via the
			// module loader.
			return nil, false, nil
		}
		if err := validateReflectDecorators(target.Decorators); err != nil {
			return nil, false, err
		}
		return target, true, nil
	case "class":
		target, ok := c.reflectClasses[key]
		if !ok {
			// Fall through to the runtime path so the VM's
			// FindClassByName can resolve classes declared in
			// other loaded modules.
			return nil, false, nil
		}
		if err := validateReflectDecorators(target.Decorators); err != nil {
			return nil, false, err
		}
		return target, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported reflect lookup %s", name)
	}
}

func (c *Compiler) compileReflectQualifiedLookup(expr *ast.CallExpression, name string) (int, error) {
	if len(expr.Arguments) != 1 {
		return 0, fmt.Errorf("reflect.%s expects exactly one name", name)
	}
	nameLiteral, ok := expr.Arguments[0].Value.(*ast.StringLiteral)
	if !ok {
		return 0, fmt.Errorf("reflect.%s name must be a string literal in bytecode", name)
	}
	moduleName, exportName, ok := strings.Cut(nameLiteral.Value, ".")
	if !ok || moduleName == "" || exportName == "" || strings.Contains(exportName, ".") {
		return 0, fmt.Errorf("reflect.%s bytecode qualified name must be module.export", name)
	}
	resolved, ok := c.resolveName(moduleName)
	if !ok {
		return 0, fmt.Errorf("unknown bytecode module %s", moduleName)
	}
	if resolved.kind == "local" {
		c.emitAt(OpGetLocal, expr.Token.Line, expr.Token.Column, resolved.slot)
	} else {
		c.emitAt(OpGetGlobal, expr.Token.Line, expr.Token.Column, resolved.slot)
	}
	c.emitConstant(runtime.String{Value: exportName}, expr.Token.Line, expr.Token.Column)
	return 2, nil
}

func (c *Compiler) reflectMethodTarget(expr *ast.CallExpression, static bool) (runtime.DecoratorTarget, bool, error) {
	label := "reflect.method"
	if static {
		label = "reflect.staticMethod"
	}
	if len(expr.Arguments) != 2 {
		return runtime.DecoratorTarget{}, false, fmt.Errorf("%s expects class and method name", label)
	}
	classIdent, ok := expr.Arguments[0].Value.(*ast.Identifier)
	if !ok {
		return runtime.DecoratorTarget{}, false, nil
	}
	if _, ok := c.resolveName(classIdent.Value); ok {
		return runtime.DecoratorTarget{}, false, nil
	}
	methodName, ok := expr.Arguments[1].Value.(*ast.StringLiteral)
	if !ok {
		return runtime.DecoratorTarget{}, false, fmt.Errorf("%s method name must be a string literal in bytecode", label)
	}
	classKey := strings.ToLower(classIdent.Value)
	if _, ok := c.reflectClasses[classKey]; !ok {
		return runtime.DecoratorTarget{}, false, nil
	}
	methodKey := strings.ToLower(methodName.Value)
	target := "method"
	reflectTarget := runtime.DecoratorTarget{}
	if static {
		target = "staticMethod"
		if methods, ok := c.reflectStatics[classKey]; ok {
			reflectTarget = methods[methodKey]
		}
		if reflectTarget.Target == "" {
			if methods, ok := c.reflectMethods[classKey]; ok {
				reflectTarget = methods[methodKey]
			}
		}
	} else if methods, ok := c.reflectMethods[classKey]; ok {
		reflectTarget = methods[methodKey]
	}
	if reflectTarget.Target == "" {
		reflectTarget = runtime.DecoratorTarget{Target: target, Decorators: []runtime.DecoratorMetadata{}}
	}
	if err := validateReflectDecorators(reflectTarget.Decorators); err != nil {
		return runtime.DecoratorTarget{}, false, err
	}
	return reflectTarget, true, nil
}

func validateReflectDecorators(decorators []runtime.DecoratorMetadata) error {
	for _, decorator := range decorators {
		for _, value := range decorator.Args {
			if errValue, ok := value.(runtime.Error); ok {
				return fmt.Errorf("decorator %s metadata: %s", decorator.Name, errValue.Message)
			}
		}
		for _, value := range decorator.NamedArgs {
			if errValue, ok := value.(runtime.Error); ok {
				return fmt.Errorf("decorator %s metadata: %s", decorator.Name, errValue.Message)
			}
		}
	}
	return nil
}

// emitPlantCallTypeBindings emits an OpPlantCallTypeBindings instruction
// for a CallExpression whose explicit `<TypeArgs>` clause should pin the
// callee's type parameters before the matching OpCall consumes them.
// No instruction is emitted when there are no explicit type args, the
// callee declares no type parameters, or none of the args produce a
// usable binding. The order of operands mirrors OpSetTypeBindings so
// the runtime handler can be shared in spirit with the class-instance
// path.
func (c *Compiler) emitPlantCallTypeBindings(expr *ast.CallExpression, typeParameters []string) {
	if len(expr.TypeArguments) == 0 || len(typeParameters) == 0 {
		return
	}
	operands := []int64{0}
	count := int64(0)
	for i, arg := range expr.TypeArguments {
		if i >= len(typeParameters) {
			break
		}
		if arg == nil || arg.Operator != "" || arg.Name == "" {
			continue
		}
		paramNameIdx := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: typeParameters[i]})
		typeNameIdx := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: arg.Name})
		operands = append(operands, paramNameIdx, typeNameIdx)
		count++
	}
	if count == 0 {
		return
	}
	operands[0] = count
	c.emitAt(OpPlantCallTypeBindings, expr.Token.Line, expr.Token.Column, operands...)
}

func expressionLocation(expr ast.Expression) (int, int) {
	switch expr := expr.(type) {
	case *ast.Identifier:
		return expr.Token.Line, expr.Token.Column
	case *ast.CallExpression:
		return expr.Token.Line, expr.Token.Column
	case *ast.SelectorExpression:
		return expr.Token.Line, expr.Token.Column
	default:
		return 0, 0
	}
}

func (c *Compiler) singleBuiltinArgument(expr *ast.CallExpression, module, name string) ([]ast.Expression, error) {
	if len(expr.Arguments) != 1 {
		switch {
		case module == "io" && name == "println":
			return nil, fmt.Errorf("io.println expects exactly one argument")
		case module == "io" && (name == "print" || name == "stdoutWrite"):
			return nil, fmt.Errorf("%s.%s expects exactly one argument", module, name)
		case module == "sys" && name == "exit":
			return nil, fmt.Errorf("sys.exit expects exactly one argument")
		default:
			return nil, fmt.Errorf("%s.%s expects exactly one argument", module, name)
		}
	}
	return []ast.Expression{expr.Arguments[0].Value}, nil
}

func (c *Compiler) compileAssignmentExpression(expr *ast.AssignmentExpression) error {
	switch left := expr.Left.(type) {
	case *ast.Identifier:
		resolved, ok := c.resolveName(left.Value)
		if !ok {
			return fmt.Errorf("unknown bytecode name %s", left.Value)
		}
		if arg, ok := selfListPushAssignment(left.Value, expr.Value); ok {
			if err := c.compileExpression(arg); err != nil {
				return err
			}
			if resolved.kind == "local" {
				c.emitAt(OpAppendLocalList, expr.Token.Line, expr.Token.Column, resolved.slot)
			} else {
				c.emitAt(OpAppendGlobalList, expr.Token.Line, expr.Token.Column, resolved.slot)
			}
			return nil
		}
		if resolved.typ == "int" {
			if op, rhsKind, rhsSlot, rhsConst, ok := c.selfIntArithAssignment(left.Value, expr.Value); ok {
				c.emitTypedIntSelfArith(op, resolved.kind, resolved.slot, rhsKind, rhsSlot, rhsConst, expr.Token.Line, expr.Token.Column)
				return nil
			}
		}
		if resolved.typ == "string" {
			if lit, ok := selfStringConstAppendAssignment(left.Value, expr.Value); ok {
				constIdx := int64(len(c.chunk.Constants))
				c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: lit})
				op := OpAppendStringConst
				if resolved.kind == "global" {
					op = OpAppendGlobalStringConst
				}
				c.emitAt(op, expr.Token.Line, expr.Token.Column, resolved.slot, constIdx)
				return nil
			}
		}
		if err := c.compileExpression(expr.Value); err != nil {
			return err
		}
		if resolved.kind == "local" {
			c.emitAt(OpSetLocal, expr.Token.Line, expr.Token.Column, resolved.slot)
		} else {
			c.emitAt(OpSetGlobal, expr.Token.Line, expr.Token.Column, resolved.slot)
		}
		return nil
	case *ast.IndexExpression:
		if err := c.compileExpression(left.Left); err != nil {
			return err
		}
		if err := c.compileExpression(left.Index); err != nil {
			return err
		}
		if err := c.compileExpression(expr.Value); err != nil {
			return err
		}
		c.emitAt(OpSetIndex, expr.Token.Line, expr.Token.Column)
		return nil
	case *ast.SelectorExpression:
		if object, ok := left.Object.(*ast.Identifier); ok {
			if _, resolvedName := c.resolveName(object.Value); !resolvedName {
				if classIndex, ok := c.classes[strings.ToLower(object.Value)]; ok && c.chunk.Classes[classIndex].Name == object.Value {
					if err := c.compileExpression(expr.Value); err != nil {
						return err
					}
					nameIndex := int64(len(c.chunk.Constants))
					c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: left.Name.Value})
					c.emitAt(OpSetStaticValue, expr.Token.Line, expr.Token.Column, classIndex, nameIndex)
					return nil
				}
			}
		}
		if err := c.compileExpression(left.Object); err != nil {
			return err
		}
		if err := c.compileExpression(expr.Value); err != nil {
			return err
		}
		nameIndex := int64(len(c.chunk.Constants))
		c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: left.Name.Value})
		c.emitAt(OpSetField, expr.Token.Line, expr.Token.Column, nameIndex)
		return nil
	case *ast.ListLiteral:
		if err := c.compileExpression(expr.Value); err != nil {
			return err
		}
		tempSlot := c.allocateLocal()
		c.emitAt(OpDefineLocal, expr.Token.Line, expr.Token.Column, tempSlot)
		for i, element := range left.Elements {
			ident, ok := element.(*ast.Identifier)
			if !ok {
				return fmt.Errorf("list destructuring target must be identifier")
			}
			resolved, ok := c.resolveName(ident.Value)
			if !ok {
				return fmt.Errorf("unknown bytecode name %s", ident.Value)
			}
			c.emitAt(OpGetLocal, expr.Token.Line, expr.Token.Column, tempSlot)
			idxConst := int64(len(c.chunk.Constants))
			c.chunk.Constants = append(c.chunk.Constants, runtime.SmallInt{Value: int64(i)})
			c.emitAt(OpConstant, expr.Token.Line, expr.Token.Column, idxConst)
			c.emitAt(OpIndex, expr.Token.Line, expr.Token.Column)
			if resolved.kind == "local" {
				c.emitAt(OpSetLocal, expr.Token.Line, expr.Token.Column, resolved.slot)
			} else {
				c.emitAt(OpSetGlobal, expr.Token.Line, expr.Token.Column, resolved.slot)
			}
		}
		return nil
	default:
		return fmt.Errorf("bytecode compiler only supports identifier, selector, index, and list destructuring assignment")
	}
}

// selfIntArithAssignment recognizes `target = target <op> rhs` where op is
// `+` or `-` and rhs is a statically int-typed term. Returns the operator, the
// rhs kind ("local" / "global" / "const"), the rhs slot or const value, and
// ok=true on match. Used to emit fused OpAdd/Sub<Scope>Int<Scope|Const> opcodes
// in compileAssignmentExpression.
func (c *Compiler) selfIntArithAssignment(targetName string, expr ast.Expression) (op string, rhsKind string, rhsSlot int64, rhsConst int64, ok bool) {
	infix, isInfix := expr.(*ast.InfixExpression)
	if !isInfix {
		return "", "", 0, 0, false
	}
	if infix.Operator != "+" && infix.Operator != "-" {
		return "", "", 0, 0, false
	}
	leftIdent, isLeftIdent := infix.Left.(*ast.Identifier)
	if !isLeftIdent || leftIdent.Value != targetName {
		return "", "", 0, 0, false
	}
	if rhsIdent, ok := infix.Right.(*ast.Identifier); ok {
		b, found := c.resolveName(rhsIdent.Value)
		if !found || b.typ != "int" {
			return "", "", 0, 0, false
		}
		return infix.Operator, b.kind, b.slot, 0, true
	}
	if intLit, ok := infix.Right.(*ast.IntegerLiteral); ok {
		value, err := runtime.NewIntLiteral(intLit.Value)
		if err != nil || !value.Value.IsInt64() {
			return "", "", 0, 0, false
		}
		return infix.Operator, "const", 0, value.Value.Int64(), true
	}
	return "", "", 0, 0, false
}

// emitTypedIntSelfArith emits the correct fused self-update arithmetic opcode
// for the destination kind, the rhs kind, and the operator. The result is
// stored back to dst and pushed onto the stack.
func (c *Compiler) emitTypedIntSelfArith(op, dstKind string, dstSlot int64, rhsKind string, rhsSlot, rhsConst int64, line, column int) {
	var opcode Op
	var rhsOperand int64
	switch dstKind {
	case "local":
		switch rhsKind {
		case "local":
			if op == "+" {
				opcode = OpAddLocalIntLocal
			} else {
				opcode = OpSubLocalIntLocal
			}
			rhsOperand = rhsSlot
		case "global":
			if op == "+" {
				opcode = OpAddLocalIntGlobal
			} else {
				opcode = OpSubLocalIntGlobal
			}
			rhsOperand = rhsSlot
		case "const":
			if op == "+" {
				opcode = OpAddLocalIntConst
			} else {
				opcode = OpSubLocalIntConst
			}
			rhsOperand = rhsConst
		}
	case "global":
		switch rhsKind {
		case "global":
			if op == "+" {
				opcode = OpAddGlobalIntGlobal
			} else {
				opcode = OpSubGlobalIntGlobal
			}
			rhsOperand = rhsSlot
		case "local":
			if op == "+" {
				opcode = OpAddGlobalIntLocal
			} else {
				opcode = OpSubGlobalIntLocal
			}
			rhsOperand = rhsSlot
		case "const":
			if op == "+" {
				opcode = OpAddGlobalIntConst
			} else {
				opcode = OpSubGlobalIntConst
			}
			rhsOperand = rhsConst
		}
	}
	c.emitAt(opcode, line, column, dstSlot, rhsOperand)
}

func selfListPushAssignment(name string, expr ast.Expression) (ast.Expression, bool) {
	call, ok := expr.(*ast.CallExpression)
	if !ok || len(call.Arguments) != 1 || call.Arguments[0].Name != nil || call.Arguments[0].Spread {
		return nil, false
	}
	selector, ok := call.Callee.(*ast.SelectorExpression)
	if !ok || selector.Optional || !strings.EqualFold(selector.Name.Value, "push") {
		return nil, false
	}
	object, ok := selector.Object.(*ast.Identifier)
	if !ok || !strings.EqualFold(object.Value, name) {
		return nil, false
	}
	return call.Arguments[0].Value, true
}

func (c *Compiler) orderFunctionArguments(function FunctionInfo, args []ast.CallArgument, paramOffset int) ([]ast.Expression, error) {
	if len(args) == 0 {
		variadicIndex := -1
		if function.Variadic && len(function.ParamNames) > 0 {
			variadicIndex = len(function.ParamNames) - 1
		}
		for i := paramOffset; i < len(function.ParamNames); i++ {
			if i == variadicIndex {
				continue
			}
			if i >= len(function.DefaultConstants) || function.DefaultConstants[i] < 0 {
				return nil, fmt.Errorf("%s missing argument before parameter %s", function.Name, function.ParamNames[i])
			}
		}
		return nil, nil
	}
	hasNamed := false
	for _, arg := range args {
		if arg.Name != nil {
			hasNamed = true
			break
		}
	}
	if !hasNamed {
		available := len(function.ParamNames) - paramOffset
		if !function.Variadic && len(args) > available {
			return nil, fmt.Errorf("%s received too many positional arguments", function.Name)
		}
		if function.Variadic && len(args) < available-1 {
			return nil, fmt.Errorf("%s received too few positional arguments", function.Name)
		}
		if !function.Variadic {
			for i := len(args) + paramOffset; i < len(function.ParamNames); i++ {
				if i >= len(function.DefaultConstants) || function.DefaultConstants[i] < 0 {
					return nil, fmt.Errorf("%s missing argument before parameter %s", function.Name, function.ParamNames[i])
				}
			}
		}
		ordered := make([]ast.Expression, 0, len(args))
		for _, arg := range args {
			ordered = append(ordered, arg.Value)
		}
		return ordered, nil
	}
	if len(function.ParamNames) <= paramOffset {
		return nil, fmt.Errorf("function metadata for %s has no parameter names", function.Name)
	}
	positions := map[string]int{}
	for i := paramOffset; i < len(function.ParamNames); i++ {
		positions[function.ParamNames[i]] = i - paramOffset
	}
	ordered := make([]ast.Expression, len(function.ParamNames)-paramOffset)
	nextPositional := 0
	for _, arg := range args {
		if arg.Name == nil {
			for nextPositional < len(ordered) && ordered[nextPositional] != nil {
				nextPositional++
			}
			if nextPositional >= len(ordered) {
				return nil, fmt.Errorf("%s received too many positional arguments", function.Name)
			}
			ordered[nextPositional] = arg.Value
			nextPositional++
			continue
		}
		position, ok := positions[strings.ToLower(arg.Name.Value)]
		if !ok {
			return nil, fmt.Errorf("%s has no parameter %s", function.Name, arg.Name.Value)
		}
		if ordered[position] != nil {
			return nil, fmt.Errorf("%s parameter %s passed more than once", function.Name, arg.Name.Value)
		}
		ordered[position] = arg.Value
	}
	for i, arg := range ordered {
		if arg == nil {
			paramIndex := i + paramOffset
			if paramIndex >= len(function.DefaultConstants) || function.DefaultConstants[paramIndex] < 0 {
				return nil, fmt.Errorf("%s missing argument before parameter %s", function.Name, function.ParamNames[paramIndex])
			}
		}
	}
	for len(ordered) > 0 && ordered[len(ordered)-1] == nil {
		ordered = ordered[:len(ordered)-1]
	}
	return ordered, nil
}

func (c *Compiler) selectFunctionCall(name string, args []ast.CallArgument, paramOffset int) (int64, []ast.Expression, error) {
	indices := c.funcs[strings.ToLower(name)]
	if len(indices) == 0 {
		return 0, nil, fmt.Errorf("unknown bytecode function %s", name)
	}
	return c.selectFunctionIndicesCall(name, indices, args, paramOffset)
}

func (c *Compiler) selectFunctionIndicesCall(name string, indices []int64, args []ast.CallArgument, paramOffset int) (int64, []ast.Expression, error) {
	return c.selectFunctionIndicesCallWithReturnFilter(name, indices, args, paramOffset, true)
}

func (c *Compiler) selectFunctionIndicesCallIgnoringReturn(name string, indices []int64, args []ast.CallArgument, paramOffset int) (int64, []ast.Expression, error) {
	return c.selectFunctionIndicesCallWithReturnFilter(name, indices, args, paramOffset, false)
}

func (c *Compiler) selectFunctionIndicesCallWithReturnFilter(name string, indices []int64, args []ast.CallArgument, paramOffset int, filterReturn bool) (int64, []ast.Expression, error) {
	matches := []int64{}
	orderedMatches := [][]ast.Expression{}
	for _, index := range indices {
		function := c.chunk.Functions[index]
		if len(function.ParamNames) == 0 && len(args) > 0 {
			continue
		}
		orderedArgs, err := c.orderFunctionArguments(function, args, paramOffset)
		if err != nil {
			continue
		}
		if !c.staticArgumentsMayMatch(function, orderedArgs, paramOffset) {
			continue
		}
		if expected := c.currentExpectedType(); filterReturn && expected != "" && !c.staticFunctionReturnAssignable(function, expected) {
			continue
		}
		matches = append(matches, index)
		orderedMatches = append(orderedMatches, orderedArgs)
	}
	if len(matches) == 0 {
		return 0, nil, fmt.Errorf("no matching overload for %s", name)
	}
	if len(matches) > 1 {
		return 0, nil, fmt.Errorf("ambiguous overload for %s", name)
	}
	return matches[0], orderedMatches[0], nil
}

func (c *Compiler) staticArgumentsMayMatch(function FunctionInfo, args []ast.Expression, paramOffset int) bool {
	if len(function.ParamTypes) == 0 {
		return true
	}
	variadicIndex := -1
	if function.Variadic && len(function.ParamTypes) > 0 {
		variadicIndex = len(function.ParamTypes) - 1
	}
	for i, arg := range args {
		if arg == nil {
			continue
		}
		argType := c.expressionStaticType(arg)
		if argType == "" {
			continue
		}
		paramIndex := i + paramOffset
		if paramIndex >= len(function.ParamTypes) {
			if variadicIndex < 0 {
				return false
			}
			paramIndex = variadicIndex
		}
		if !c.staticFunctionParamAssignable(function, function.ParamTypes[paramIndex], argType) {
			return false
		}
	}
	return true
}

func (c *Compiler) expressionStaticType(expr ast.Expression) string {
	switch expr := expr.(type) {
	case *ast.IntegerLiteral:
		return "int"
	case *ast.DecimalLiteral:
		return "decimal"
	case *ast.FloatLiteral:
		return "float"
	case *ast.StringLiteral:
		return "string"
	case *ast.InterpolatedString:
		return "string"
	case *ast.Literal:
		switch expr.Value.(type) {
		case bool:
			return "bool"
		case nil:
			return "null"
		}
	case *ast.ListLiteral:
		return "list"
	case *ast.DictLiteral:
		return "dict"
	case *ast.SetLiteral:
		return "set"
	case *ast.Identifier:
		if strings.EqualFold(expr.Value, "null") {
			return "null"
		}
		if resolved, ok := c.resolveName(expr.Value); ok {
			return resolved.typ
		}
	case *ast.CallExpression:
		if ident, ok := expr.Callee.(*ast.Identifier); ok {
			if classIndex, ok := c.classes[strings.ToLower(ident.Value)]; ok && c.chunk.Classes[classIndex].Name == ident.Value {
				return ident.Value
			}
		}
	case *ast.FunctionLiteral:
		return "func"
	case *ast.CastExpression:
		return c.bytecodeTypeName(expr.Type)
	}
	return ""
}

func (c *Compiler) staticTypeAssignable(target string, actual string) bool {
	if target == "" || strings.EqualFold(target, "any") || actual == "" {
		return true
	}
	// Union target: value is assignable when it matches any branch.
	if branches, ok := splitTopLevelTypeOp(target, '|'); ok {
		for _, b := range branches {
			if c.staticTypeAssignable(b, actual) {
				return true
			}
		}
		return false
	}
	// Intersection target: value is assignable only when it matches
	// every branch. (Rare at the parameter boundary; mainly here for
	// symmetry with the runtime check.)
	if branches, ok := splitTopLevelTypeOp(target, '&'); ok {
		for _, b := range branches {
			if !c.staticTypeAssignable(b, actual) {
				return false
			}
		}
		return true
	}
	// Union actual: every branch must be assignable to the target.
	if branches, ok := splitTopLevelTypeOp(actual, '|'); ok {
		for _, b := range branches {
			if !c.staticTypeAssignable(target, b) {
				return false
			}
		}
		return true
	}
	target = normalizeCallableTypeName(target)
	actual = normalizeCallableTypeName(actual)
	nullable := strings.HasPrefix(target, "?")
	if nullable {
		target = strings.TrimPrefix(target, "?")
	}
	if strings.EqualFold(actual, "null") {
		return nullable
	}
	actual = strings.TrimPrefix(actual, "?")
	if strings.EqualFold(target, actual) {
		return true
	}
	// Compare base type names by stripping generic type arguments. The
	// reified-generics element check is enforced at runtime; static checking
	// here only validates the base type compatibility.
	baseTarget := target
	if ltIdx := strings.Index(target, "<"); ltIdx >= 0 {
		baseTarget = target[:ltIdx]
	}
	baseActual := actual
	if ltIdx := strings.Index(actual, "<"); ltIdx >= 0 {
		baseActual = actual[:ltIdx]
	}
	if strings.EqualFold(baseTarget, baseActual) {
		return true
	}
	// generator and iterable are interchangeable type names: a function
	// declared to return generator<T> may be passed where iterable<T> is
	// expected, and vice versa. Mirrors isGeneratorTypeName at runtime.
	if isGeneratorTypeName(baseTarget) && isGeneratorTypeName(baseActual) {
		return true
	}
	return c.staticClassAssignable(baseTarget, baseActual)
}

func (c *Compiler) staticFunctionParamAssignable(function FunctionInfo, target string, actual string) bool {
	// Strip generic type arg ("list<T>" → "list") from both sides before
	// the type-name comparison. The element type comparison is handled by
	// the semantic analyzer for declarations and by reified-generics at
	// runtime.
	baseTarget := target
	if ltIdx := strings.Index(target, "<"); ltIdx >= 0 {
		baseTarget = target[:ltIdx]
	}
	baseActual := actual
	if ltIdx := strings.Index(actual, "<"); ltIdx >= 0 {
		baseActual = actual[:ltIdx]
	}
	if functionTypeParameterSetOrNil(function)[strings.ToLower(strings.TrimPrefix(baseTarget, "?"))] {
		return true
	}
	return c.staticTypeAssignable(baseTarget, baseActual)
}

func (c *Compiler) staticFunctionReturnAssignable(function FunctionInfo, expected string) bool {
	if functionTypeParameterSetOrNil(function)[strings.ToLower(strings.TrimPrefix(function.ReturnType, "?"))] {
		return true
	}
	return c.staticTypeAssignable(expected, function.ReturnType)
}

func functionTypeParameterSet(function FunctionInfo) map[string]bool {
	params := map[string]bool{}
	for _, name := range function.TypeParameters {
		params[strings.ToLower(name)] = true
	}
	return params
}

func functionTypeParameterSetOrNil(function FunctionInfo) map[string]bool {
	if len(function.TypeParameters) == 0 {
		return nil
	}
	return functionTypeParameterSet(function)
}

// staticallyResolveMethodCall returns the chunk-function index when
// the receiver's class is known statically, the named method
// resolves to a single non-decorated/non-async/non-generator
// overload, and no subclass overrides it.
func (c *Compiler) staticallyResolveMethodCall(selector *ast.SelectorExpression, args []ast.CallArgument) (int64, bool) {
	typeName := c.expressionStaticType(selector.Object)
	if typeName == "" {
		return 0, false
	}
	classIndex, ok := c.classes[strings.ToLower(typeName)]
	if !ok {
		return 0, false
	}
	classInfo := c.chunk.Classes[classIndex]
	if len(classInfo.TypeParameters) > 0 {
		return 0, false
	}
	if len(classInfo.Decorators) > 0 {
		return 0, false
	}
	methodName := selector.Name.Value
	indices, ok := classInfo.Methods[strings.ToLower(methodName)]
	if !ok || len(indices) != 1 {
		return 0, false
	}
	if c.classHasSubclassOverriding(classIndex, methodName) {
		return 0, false
	}
	fnIndex := indices[0]
	if fnIndex < 0 || int(fnIndex) >= len(c.chunk.Functions) {
		return 0, false
	}
	fn := c.chunk.Functions[fnIndex]
	if fn.Async || fn.IsGenerator || len(fn.Decorators) > 0 || fn.Variadic {
		return 0, false
	}
	if len(args) != len(fn.ParamSlots)-1 {
		return 0, false
	}
	if decorators, hasDecorators := classInfo.MethodDecorators[strings.ToLower(methodName)]; hasDecorators && len(decorators) > 0 {
		return 0, false
	}
	return fnIndex, true
}

func (c *Compiler) classHasSubclassOverriding(classIndex int64, methodName string) bool {
	lowered := strings.ToLower(methodName)
	for i := range c.chunk.Classes {
		if int64(i) == classIndex {
			continue
		}
		sub := c.chunk.Classes[i]
		for ancestor := sub.ParentIndex; ancestor >= 0 && int(ancestor) < len(c.chunk.Classes); {
			if ancestor == classIndex {
				if _, hasMethod := sub.Methods[lowered]; hasMethod {
					return true
				}
				break
			}
			ancestor = c.chunk.Classes[ancestor].ParentIndex
		}
	}
	return false
}

func isStringLiteral(expr ast.Expression) bool {
	_, ok := expr.(*ast.StringLiteral)
	return ok
}

// selfStringConstAppendAssignment detects `name = name + "literal"`
// where both reads of `name` refer to the same identifier and the
// right operand of `+` is a string literal. Returns the literal's
// raw value on a match.
func selfStringConstAppendAssignment(name string, value ast.Expression) (string, bool) {
	infix, ok := value.(*ast.InfixExpression)
	if !ok || infix.Operator != "+" {
		return "", false
	}
	leftIdent, ok := infix.Left.(*ast.Identifier)
	if !ok || !strings.EqualFold(leftIdent.Value, name) {
		return "", false
	}
	lit, ok := infix.Right.(*ast.StringLiteral)
	if !ok {
		return "", false
	}
	return lit.Value, true
}

func isPrimitiveTypeForTCE(paramType string) bool {
	switch strings.ToLower(paramType) {
	case "", "any", "int", "bool", "float", "string", "decimal":
		return true
	}
	// Trim a leading "?" nullable marker; the underlying type still
	// needs primitive scalar validation.
	if strings.HasPrefix(paramType, "?") {
		return isPrimitiveTypeForTCE(paramType[1:])
	}
	return false
}

func functionRequiresParamValidation(function FunctionInfo) bool {
	for _, typ := range function.ParamTypes {
		if typ == "" {
			continue
		}
		if strings.EqualFold(typ, "any") {
			continue
		}
		return true
	}
	return false
}

func (c *Compiler) staticClassAssignable(target string, actual string) bool {
	classIndex, ok := c.classes[strings.ToLower(actual)]
	if !ok {
		return false
	}
	for {
		if classIndex < 0 || int(classIndex) >= len(c.chunk.Classes) {
			return false
		}
		classInfo := c.chunk.Classes[classIndex]
		if strings.EqualFold(classInfo.Name, target) {
			return true
		}
		for _, iface := range classInfo.Implements {
			if c.staticInterfaceAssignable(target, iface) {
				return true
			}
		}
		if classInfo.ParentIndex < 0 {
			return false
		}
		classIndex = classInfo.ParentIndex
	}
}

func (c *Compiler) staticInterfaceAssignable(target string, actual string) bool {
	if strings.EqualFold(target, actual) {
		return true
	}
	ifaceIndex, ok := c.interfaces[strings.ToLower(actual)]
	if !ok || ifaceIndex < 0 || int(ifaceIndex) >= len(c.chunk.Interfaces) {
		return false
	}
	for _, parent := range c.chunk.Interfaces[ifaceIndex].Parents {
		if c.staticInterfaceAssignable(target, parent) {
			return true
		}
	}
	return false
}

func (c *Compiler) compileOrderedArguments(function FunctionInfo, args []ast.Expression, paramOffset int, line int, column int) error {
	for i, arg := range args {
		if arg == nil {
			defaultIndex := function.DefaultConstants[i+paramOffset]
			c.emitAt(OpConstant, line, column, defaultIndex)
			continue
		}
		expectedParamType := ""
		paramIndex := i + paramOffset
		if paramIndex < len(function.ParamTypes) {
			t := function.ParamTypes[paramIndex]
			if t != "" && t != "any" {
				expectedParamType = t
			}
		} else if function.Variadic && len(function.ParamTypes) > 0 {
			t := function.ParamTypes[len(function.ParamTypes)-1]
			if t != "" && t != "any" {
				expectedParamType = t
			}
		}
		if err := c.compileExpressionWithExpected(arg, expectedParamType); err != nil {
			return err
		}
	}
	return nil
}

func positionalArguments(args []ast.CallArgument) []ast.Expression {
	ordered := make([]ast.Expression, 0, len(args))
	for _, arg := range args {
		if arg.Name != nil {
			return nil
		}
		ordered = append(ordered, arg.Value)
	}
	return ordered
}

func (c *Compiler) lookupStaticMethod(classInfo ClassInfo, name string) ([]int64, bool) {
	if indices, ok := classInfo.StaticMethods[strings.ToLower(name)]; ok {
		return indices, true
	}
	if classInfo.ParentIndex >= 0 && int(classInfo.ParentIndex) < len(c.chunk.Classes) {
		return c.lookupStaticMethod(c.chunk.Classes[classInfo.ParentIndex], name)
	}
	return nil, false
}

func (c *Compiler) lookupMethod(classInfo ClassInfo, name string) ([]int64, bool) {
	if indices, ok := classInfo.Methods[strings.ToLower(name)]; ok {
		return indices, true
	}
	if classInfo.ParentIndex >= 0 && int(classInfo.ParentIndex) < len(c.chunk.Classes) {
		return c.lookupMethod(c.chunk.Classes[classInfo.ParentIndex], name)
	}
	return nil, false
}

// crossModuleBoundary reports whether classInfo's same-chunk ancestor chain
// terminates in a cross-module parent, returning that qualified name.
func (c *Compiler) crossModuleBoundary(classInfo ClassInfo) (string, bool) {
	top := classInfo
	for top.ParentIndex >= 0 && int(top.ParentIndex) < len(c.chunk.Classes) {
		top = c.chunk.Classes[top.ParentIndex]
	}
	if strings.Contains(top.ParentName, ".") {
		return top.ParentName, true
	}
	return "", false
}

func (c *Compiler) emitConstant(value runtime.Value, line int, column int) {
	index := int64(len(c.chunk.Constants))
	c.chunk.Constants = append(c.chunk.Constants, value)
	c.emitAt(OpConstant, line, column, index)
}

func foldConstantBinary(op string, left, right ast.Expression) (runtime.Value, bool, error) {
	if lInt, ok := intLiteralValue(left); ok {
		if rInt, ok := intLiteralValue(right); ok {
			return foldIntInt(op, lInt, rInt)
		}
		if rFloat, ok := floatLiteralValue(right); ok {
			return foldFloatFloat(op, float64(lInt), rFloat)
		}
	}
	if lFloat, ok := floatLiteralValue(left); ok {
		if rFloat, ok := floatLiteralValue(right); ok {
			return foldFloatFloat(op, lFloat, rFloat)
		}
		if rInt, ok := intLiteralValue(right); ok {
			return foldFloatFloat(op, lFloat, float64(rInt))
		}
	}
	if lStr, ok := stringLiteralValue(left); ok {
		if rStr, ok := stringLiteralValue(right); ok {
			return foldStringString(op, lStr, rStr)
		}
	}
	if lBool, ok := boolLiteralValue(left); ok {
		if rBool, ok := boolLiteralValue(right); ok {
			return foldBoolBool(op, lBool, rBool)
		}
	}
	return nil, false, nil
}

func intLiteralValue(expr ast.Expression) (int64, bool) {
	lit, ok := expr.(*ast.IntegerLiteral)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(lit.Value, 0, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func floatLiteralValue(expr ast.Expression) (float64, bool) {
	lit, ok := expr.(*ast.FloatLiteral)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(lit.Value, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func stringLiteralValue(expr ast.Expression) (string, bool) {
	lit, ok := expr.(*ast.StringLiteral)
	if !ok || lit.Triple {
		return "", false
	}
	return lit.Value, true
}

func boolLiteralValue(expr ast.Expression) (bool, bool) {
	lit, ok := expr.(*ast.Literal)
	if !ok {
		return false, false
	}
	b, ok := lit.Value.(bool)
	if !ok {
		return false, false
	}
	return b, true
}

func foldIntInt(op string, l, r int64) (runtime.Value, bool, error) {
	switch op {
	case "+":
		return runtime.NewInt64(l + r), true, nil
	case "-":
		return runtime.NewInt64(l - r), true, nil
	case "*":
		return runtime.NewInt64(l * r), true, nil
	case "//":
		if r == 0 {
			return nil, true, fmt.Errorf("integer division by zero")
		}
		// Floor semantics: Go's `/` truncates toward zero.
		q := l / r
		if (l^r) < 0 && q*r != l {
			q--
		}
		return runtime.NewInt64(q), true, nil
	case "%":
		if r == 0 {
			return nil, true, fmt.Errorf("modulo by zero")
		}
		// Floor semantics: sign of result follows divisor.
		m := l % r
		if m != 0 && (m^r) < 0 {
			m += r
		}
		return runtime.NewInt64(m), true, nil
	case "==":
		return runtime.Bool{Value: l == r}, true, nil
	case "!=":
		return runtime.Bool{Value: l != r}, true, nil
	case "<":
		return runtime.Bool{Value: l < r}, true, nil
	case "<=":
		return runtime.Bool{Value: l <= r}, true, nil
	case ">":
		return runtime.Bool{Value: l > r}, true, nil
	case ">=":
		return runtime.Bool{Value: l >= r}, true, nil
	}
	return nil, false, nil
}

func foldFloatFloat(op string, l, r float64) (runtime.Value, bool, error) {
	switch op {
	case "+":
		return runtime.Float{Value: l + r}, true, nil
	case "-":
		return runtime.Float{Value: l - r}, true, nil
	case "*":
		return runtime.Float{Value: l * r}, true, nil
	case "==":
		return runtime.Bool{Value: l == r}, true, nil
	case "!=":
		return runtime.Bool{Value: l != r}, true, nil
	case "<":
		return runtime.Bool{Value: l < r}, true, nil
	case "<=":
		return runtime.Bool{Value: l <= r}, true, nil
	case ">":
		return runtime.Bool{Value: l > r}, true, nil
	case ">=":
		return runtime.Bool{Value: l >= r}, true, nil
	}
	return nil, false, nil
}

func foldStringString(op string, l, r string) (runtime.Value, bool, error) {
	switch op {
	case "+":
		return runtime.String{Value: l + r}, true, nil
	case "==":
		return runtime.Bool{Value: l == r}, true, nil
	case "!=":
		return runtime.Bool{Value: l != r}, true, nil
	case "<":
		return runtime.Bool{Value: l < r}, true, nil
	case "<=":
		return runtime.Bool{Value: l <= r}, true, nil
	case ">":
		return runtime.Bool{Value: l > r}, true, nil
	case ">=":
		return runtime.Bool{Value: l >= r}, true, nil
	}
	return nil, false, nil
}

func foldBoolBool(op string, l, r bool) (runtime.Value, bool, error) {
	switch op {
	case "==":
		return runtime.Bool{Value: l == r}, true, nil
	case "!=":
		return runtime.Bool{Value: l != r}, true, nil
	}
	return nil, false, nil
}

func (c *Compiler) compileAssertCall(expr *ast.CallExpression) error {
	if len(expr.Arguments) < 1 || len(expr.Arguments) > 2 {
		return fmt.Errorf("assert expects (condition) or (condition, message)")
	}
	for _, arg := range expr.Arguments {
		if arg.Name != nil {
			return fmt.Errorf("assert does not accept named arguments")
		}
	}
	line, col := expr.Token.Line, expr.Token.Column
	if c.AssertionsDisabled {
		c.emitConstant(runtime.Null{}, line, col)
		return nil
	}
	cond := expr.Arguments[0].Value
	if err := c.compileExpression(cond); err != nil {
		return err
	}
	c.emitAt(OpNot, line, col)
	jumpEnd := c.emitJump(OpJumpIfFalse, line, col)
	if len(expr.Arguments) == 2 {
		if err := c.compileExpression(expr.Arguments[1].Value); err != nil {
			return err
		}
	} else {
		c.emitConstant(runtime.String{Value: "assertion failed: " + cond.String()}, line, col)
	}
	classIndex := int64(len(c.chunk.Constants))
	c.chunk.Constants = append(c.chunk.Constants, runtime.String{Value: "AssertionError"})
	c.emitAt(OpMakeError, line, col, classIndex, 1)
	c.emitAt(OpThrow, line, col)
	c.patchJump(jumpEnd)
	c.emitConstant(runtime.Null{}, line, col)
	return nil
}

func (c *Compiler) emit(op Op, operands ...int64) {
	c.emitAt(op, 0, 0, operands...)
}

func (c *Compiler) emitAt(op Op, line int, column int, operands ...int64) {
	c.chunk.Instructions = append(c.chunk.Instructions, Instruction{Op: op, Operands: operands, Line: line, Column: column})
}

func (c *Compiler) emitJump(op Op, line int, column int) int {
	c.emitAt(op, line, column, -1)
	return len(c.chunk.Instructions) - 1
}

// compileConditionAndJumpIfFalse compiles cond and emits a jump that fires
// when cond is logically false. When cond is a typed-int infix comparison,
// emits a single fused OpJumpIfNot<Cmp>Int opcode (Phase 11) and returns its
// patch position. Otherwise emits the unfused [cond; OpJumpIfFalse] pair.
func (c *Compiler) compileConditionAndJumpIfFalse(cond ast.Expression, line, column int) (int, error) {
	if infix, ok := cond.(*ast.InfixExpression); ok {
		if slot, divisor, op, ok := c.tryFuseModZeroBranch(infix); ok {
			c.emitAt(op, line, column, -1, slot, divisor)
			return len(c.chunk.Instructions) - 1, nil
		}
		fusedOp, fused := fusedCompareJumpOpFor(infix.Operator)
		if fused && c.staticIntExpr(infix.Left) && c.staticIntExpr(infix.Right) {
			if err := c.compileExpression(infix.Left); err != nil {
				return 0, err
			}
			if err := c.compileExpression(infix.Right); err != nil {
				return 0, err
			}
			return c.emitJump(fusedOp, line, column), nil
		}
	}
	if err := c.compileExpression(cond); err != nil {
		return 0, err
	}
	return c.emitJump(OpJumpIfFalse, line, column), nil
}

// tryFuseModZeroBranch matches `<local> % <const_int> == 0` (and !=, and the
// reversed `0 == <local>%<const>` form). On a match returns the local slot,
// divisor, and the matching fused opcode (NotZero opcode for `==`, Zero
// opcode for `!=`, because both jump when the body should be skipped).
func (c *Compiler) tryFuseModZeroBranch(infix *ast.InfixExpression) (int64, int64, Op, bool) {
	if infix.Operator != "==" && infix.Operator != "!=" {
		return 0, 0, 0, false
	}
	modExpr, zero := infix.Left, infix.Right
	if !isIntZeroLiteral(zero) {
		modExpr, zero = infix.Right, infix.Left
		if !isIntZeroLiteral(zero) {
			return 0, 0, 0, false
		}
	}
	mod, ok := modExpr.(*ast.InfixExpression)
	if !ok || mod.Operator != "%" {
		return 0, 0, 0, false
	}
	ident, ok := mod.Left.(*ast.Identifier)
	if !ok {
		return 0, 0, 0, false
	}
	b, found := c.resolveName(ident.Value)
	if !found || b.kind != "local" || b.typ != "int" {
		return 0, 0, 0, false
	}
	divLit, ok := mod.Right.(*ast.IntegerLiteral)
	if !ok {
		return 0, 0, 0, false
	}
	divisor, err := strconv.ParseInt(divLit.Value, 10, 64)
	if err != nil || divisor == 0 {
		return 0, 0, 0, false
	}
	op := OpJumpIfModNotZero
	if infix.Operator == "!=" {
		op = OpJumpIfModZero
	}
	return b.slot, divisor, op, true
}

func isIntZeroLiteral(e ast.Expression) bool {
	lit, ok := e.(*ast.IntegerLiteral)
	if !ok {
		return false
	}
	v, err := strconv.ParseInt(lit.Value, 10, 64)
	return err == nil && v == 0
}

// fusedCompareJumpOpFor maps an infix comparison operator to the corresponding
// fused compare-and-skip-body opcode. Returns (op, true) on match.
func fusedCompareJumpOpFor(operator string) (Op, bool) {
	switch operator {
	case "<":
		return OpJumpIfNotLessInt, true
	case "<=":
		return OpJumpIfNotLessEqualInt, true
	case ">":
		return OpJumpIfNotGreaterInt, true
	case ">=":
		return OpJumpIfNotGreaterEqualInt, true
	case "==":
		return OpJumpIfNotEqualInt, true
	case "!=":
		return OpJumpIfEqualInt, true
	}
	return 0, false
}

func (c *Compiler) patchJump(index int) {
	c.chunk.Instructions[index].Operands[0] = int64(len(c.chunk.Instructions))
}

func (c *Compiler) pushScope() {
	c.scopes = append(c.scopes, map[string]binding{})
}

func (c *Compiler) popScope() {
	c.scopes = c.scopes[:len(c.scopes)-1]
}

func (c *Compiler) inGlobalScope() bool {
	return len(c.scopes) == 1
}

func (c *Compiler) defineLocal(name string) int64 {
	return c.defineLocalWithType(name, "")
}

func (c *Compiler) defineLocalWithType(name string, typ string) int64 {
	slot := c.allocateLocal()
	c.scopes[len(c.scopes)-1][name] = binding{kind: "local", slot: slot, typ: typ}
	return slot
}

func (c *Compiler) allocateLocal() int64 {
	slot := c.locals
	c.locals++
	return slot
}

// pushFunctionLocals saves the current locals counter and resets it to
// zero so the function being entered gets its own relative slot range.
func (c *Compiler) pushFunctionLocals() {
	c.localsStack = append(c.localsStack, c.locals)
	c.locals = 0
}

// popFunctionLocals returns the LocalCount of the just-compiled function
// (the high-water mark) and restores the enclosing scope's counter.
func (c *Compiler) popFunctionLocals() int64 {
	count := c.locals
	last := len(c.localsStack) - 1
	c.locals = c.localsStack[last]
	c.localsStack = c.localsStack[:last]
	return count
}

func (c *Compiler) declareFunction(name string) int64 {
	key := strings.ToLower(name)
	index := int64(len(c.chunk.Functions))
	c.funcs[key] = append(c.funcs[key], index)
	c.chunk.Functions = append(c.chunk.Functions, FunctionInfo{Name: key})
	return index
}

// populateFunctionSignature fills in the parameter / return type
// metadata for a function declared but not yet compiled. Callers
// that depend on signature-based overload resolution (notably the
// forward-call lookup in selectFunctionIndicesCall) read this
// metadata; the function body is compiled later by
// compileFunctionStatement which overwrites everything except the
// reserved index.
func (c *Compiler) populateFunctionSignature(index int64, fn *ast.FunctionStatement) {
	info := &c.chunk.Functions[index]
	info.ParamNames = make([]string, 0, len(fn.Parameters))
	info.ParamTypes = make([]string, 0, len(fn.Parameters))
	info.DefaultConstants = make([]int64, 0, len(fn.Parameters))
	for _, param := range fn.Parameters {
		info.ParamNames = append(info.ParamNames, param.Name.Value)
		if param.Type != nil {
			info.ParamTypes = append(info.ParamTypes, param.Type.String())
		} else {
			info.ParamTypes = append(info.ParamTypes, "")
		}
		info.DefaultConstants = append(info.DefaultConstants, -1)
		if param.Variadic {
			info.Variadic = true
		}
	}
	info.ReturnType = c.bytecodeReturnType(fn.ReturnType)
}

func (c *Compiler) nextFunctionIndex(name string) int64 {
	key := strings.ToLower(name)
	indices := c.funcs[key]
	cursor := c.functionCursors[key]
	if cursor >= len(indices) {
		index := c.declareFunction(name)
		c.functionCursors[key] = cursor + 1
		return index
	}
	c.functionCursors[key] = cursor + 1
	return indices[cursor]
}

func (c *Compiler) lastFunctionIndex(name string) (int64, error) {
	indices := c.funcs[strings.ToLower(name)]
	if len(indices) == 0 {
		return 0, fmt.Errorf("unknown bytecode function %s", name)
	}
	return indices[len(indices)-1], nil
}

func (c *Compiler) singleFunctionIndex(name string) (int64, error) {
	indices := c.funcs[strings.ToLower(name)]
	if len(indices) == 0 {
		return 0, fmt.Errorf("unknown bytecode function %s", name)
	}
	if len(indices) > 1 {
		return 0, parityErrorf("bytecode compiler does not support exporting overloaded function %s yet", name)
	}
	return indices[0], nil
}

func (c *Compiler) declareClass(name string) int64 {
	key := strings.ToLower(name)
	if index, ok := c.classes[key]; ok {
		return index
	}
	index := int64(len(c.chunk.Classes))
	c.classes[key] = index
	c.chunk.Classes = append(c.chunk.Classes, ClassInfo{
		Name:            name,
		ParentIndex:     -1,
		DestructorIndex: -1,
		Methods:         map[string][]int64{},
		StaticValues:    map[string]int64{},
		StaticMethods:   map[string][]int64{},
	})
	return index
}

func (c *Compiler) declareInterface(name string) int64 {
	key := strings.ToLower(name)
	if index, ok := c.interfaces[key]; ok {
		return index
	}
	index := int64(len(c.chunk.Interfaces))
	c.interfaces[key] = index
	c.chunk.Interfaces = append(c.chunk.Interfaces, InterfaceInfo{Name: name})
	return index
}

func (c *Compiler) compileEnumStatement(stmt *ast.EnumStatement) error {
	index := c.declareEnum(stmt)
	c.emitAt(OpConstant, stmt.Token.Line, stmt.Token.Column, index)
	slot := c.globalSlot(stmt.Name.Value)
	c.emitAt(OpDefineGlobal, stmt.Token.Line, stmt.Token.Column, slot)
	return nil
}

func (c *Compiler) declareEnum(stmt *ast.EnumStatement) int64 {
	key := strings.ToLower(stmt.Name.Value)
	if index, ok := c.enums[key]; ok {
		return index
	}
	enumDef := &runtime.EnumDef{Name: stmt.Name.Value}
	for _, v := range stmt.Variants {
		enumDef.Variants = append(enumDef.Variants, runtime.EnumVariantDefRuntime{
			Name:       v.Name.Value,
			FieldCount: len(v.FieldTypes),
		})
	}
	index := int64(len(c.chunk.Constants))
	c.chunk.Constants = append(c.chunk.Constants, enumDef)
	c.enums[key] = index
	return index
}

func (c *Compiler) declareTypeAlias(stmt *ast.TypeAliasStatement) {
	if stmt == nil || stmt.Name == nil || stmt.Type == nil {
		return
	}
	c.typeAliases[strings.ToLower(stmt.Name.Value)] = c.resolveTypeRef(stmt.Type)
}

func (c *Compiler) resolveTypeRef(ref *ast.TypeRef) *ast.TypeRef {
	if ref == nil {
		return nil
	}
	out := cloneTypeRef(ref)
	if out.Operator != "" {
		out.Left = c.resolveTypeRef(out.Left)
		out.Right = c.resolveTypeRef(out.Right)
		return out
	}
	if alias, ok := c.typeAliases[strings.ToLower(out.Name)]; ok {
		resolved := cloneTypeRef(alias)
		resolved.Nullable = resolved.Nullable || out.Nullable
		resolved.ListAlias = resolved.ListAlias || out.ListAlias
		return resolved
	}
	for i, arg := range out.Arguments {
		out.Arguments[i] = c.resolveTypeRef(arg)
	}
	return out
}

func cloneTypeRef(ref *ast.TypeRef) *ast.TypeRef {
	if ref == nil {
		return nil
	}
	out := *ref
	if len(ref.Arguments) > 0 {
		out.Arguments = make([]*ast.TypeRef, len(ref.Arguments))
		for i, arg := range ref.Arguments {
			out.Arguments[i] = cloneTypeRef(arg)
		}
	}
	out.Left = cloneTypeRef(ref.Left)
	out.Right = cloneTypeRef(ref.Right)
	return &out
}

func typeParameterNames(params []*ast.TypeParam) []string {
	names := make([]string, 0, len(params))
	for _, param := range params {
		if param != nil && param.Name != nil {
			names = append(names, param.Name.Value)
		}
	}
	return names
}

func typeParamConstraintExprs(params []*ast.TypeParam) []string {
	exprs := make([]string, 0, len(params))
	for _, param := range params {
		if param == nil || param.Constraint == nil {
			exprs = append(exprs, "")
		} else {
			exprs = append(exprs, typeRefConstraintString(param.Constraint))
		}
	}
	return exprs
}

func typeRefConstraintString(ref *ast.TypeRef) string {
	if ref == nil {
		return ""
	}
	if ref.Operator != "" {
		return "(" + typeRefConstraintString(ref.Left) + ref.Operator + typeRefConstraintString(ref.Right) + ")"
	}
	return ref.Name
}

func blockContainsYield(block *ast.BlockStatement) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Statements {
		if statementContainsYield(stmt) {
			return true
		}
	}
	return false
}

func statementContainsYield(stmt ast.Statement) bool {
	switch stmt := stmt.(type) {
	case *ast.YieldStatement:
		return true
	case *ast.IfStatement:
		if blockContainsYield(stmt.Consequence) || blockContainsYield(stmt.Alternative) {
			return true
		}
		for _, elseif := range stmt.ElseIfs {
			if blockContainsYield(elseif.Body) {
				return true
			}
		}
	case *ast.WhileStatement:
		return blockContainsYield(stmt.Body)
	case *ast.ForStatement:
		return blockContainsYield(stmt.Body)
	case *ast.TryStatement:
		if blockContainsYield(stmt.Body) || blockContainsYield(stmt.Finally) {
			return true
		}
		for _, catch := range stmt.Catches {
			if blockContainsYield(catch.Body) {
				return true
			}
		}
	case *ast.MatchStatement:
		for _, matchCase := range stmt.Cases {
			if blockContainsYield(matchCase.Body) {
				return true
			}
		}
	case *ast.WithStatement:
		return blockContainsYield(stmt.Body)
	}
	return false
}

func (c *Compiler) bytecodeTypeName(typ *ast.TypeRef) string {
	if typ == nil || strings.EqualFold(typ.Name, "any") {
		return "any"
	}
	if typ.Operator == "|" || typ.Operator == "&" {
		// Preserve union / intersection shape so the VM type-spec
		// parser can split on the top-level operator. Other binary
		// operators (legacy / unknown) keep the historical
		// "any" fallback.
		return c.bytecodeTypeName(typ.Left) + " " + typ.Operator + " " + c.bytecodeTypeName(typ.Right)
	}
	if typ.Operator != "" {
		return "any"
	}
	typ = c.resolveTypeRef(typ)
	name := typ.Name
	if isCallableTypeName(name) {
		name = "func"
	}
	if isGeneratorTypeName(name) {
		name = "generator"
	}
	if typ.ListAlias || strings.EqualFold(name, "list") {
		name = "list"
	}
	if typ.Nullable {
		return "?" + name
	}
	return name
}

// bytecodeTypeNameForParam is like bytecodeTypeName but preserves generic type arguments.
// Concrete collection types (e.g. list<int>, int[]) retain their element type for runtime enforcement.
// Generic type parameters (e.g. list<T>) are also preserved for type-binding inference.
func (c *Compiler) bytecodeTypeNameForParam(typ *ast.TypeRef, typeParams []string) string {
	base := c.bytecodeTypeName(typ)
	if typ == nil {
		return base
	}
	// T[] syntax: ListAlias=true, Arguments is empty, Name holds the element type.
	if typ.ListAlias && len(typ.Arguments) == 0 && typ.Name != "" && !strings.EqualFold(typ.Name, "list") {
		return "list<" + typ.Name + ">"
	}
	if len(typ.Arguments) == 0 {
		return base
	}
	parts := make([]string, 0, len(typ.Arguments))
	for _, arg := range typ.Arguments {
		if arg == nil || arg.Operator != "" || arg.Name == "" {
			parts = append(parts, c.bytecodeTypeName(arg))
			continue
		}
		parts = append(parts, c.bytecodeTypeNameForParam(arg, typeParams))
	}
	return base + "<" + strings.Join(parts, ",") + ">"
}

func normalizeCallableTypeName(name string) string {
	nullable := strings.HasPrefix(name, "?")
	trimmed := strings.TrimPrefix(name, "?")
	if isCallableTypeName(trimmed) {
		if nullable {
			return "?func"
		}
		return "func"
	}
	return name
}

func isCallableTypeName(name string) bool {
	return strings.EqualFold(name, "func") || strings.EqualFold(name, "callable") || strings.EqualFold(name, "function")
}

func isGeneratorTypeName(name string) bool {
	return strings.EqualFold(name, "generator") || strings.EqualFold(name, "iterable")
}

func (c *Compiler) bytecodeReturnType(typ *ast.TypeRef) string {
	if typ == nil {
		return "void"
	}
	return c.bytecodeTypeName(typ)
}

func (c *Compiler) currentExpectedType() string {
	if len(c.expectedTypes) == 0 {
		return ""
	}
	return c.expectedTypes[len(c.expectedTypes)-1]
}

func (c *Compiler) currentReturnType() string {
	if len(c.returnTypes) == 0 {
		return ""
	}
	return c.returnTypes[len(c.returnTypes)-1]
}

// staticIntExpr reports whether expr is provably of static type "int" at
// compile time. Used to decide whether to emit type-specialized integer
// opcodes (OpAddInt, OpSubInt, etc.).
func (c *Compiler) staticIntExpr(expr ast.Expression) bool {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return true
	case *ast.Identifier:
		b, ok := c.resolveName(e.Value)
		return ok && b.typ == "int"
	case *ast.InfixExpression:
		switch e.Operator {
		case "+", "-", "*", "//", "%":
			return c.staticIntExpr(e.Left) && c.staticIntExpr(e.Right)
		}
	case *ast.PrefixExpression:
		if e.Operator == "-" {
			return c.staticIntExpr(e.Right)
		}
	case *ast.CallExpression:
		return c.callExprReturnsType(e, "int")
	case *ast.CastExpression:
		return strings.EqualFold(c.bytecodeTypeName(e.Type), "int")
	case *ast.SelectorExpression:
		return c.selectorReturnsType(e, "int")
	}
	return false
}

// selectorReturnsType reports whether `obj.field` resolves to a class
// field whose declared type matches `want`. Lets staticIntExpr (and
// friends) propagate int/string/bool through `this.fieldname` access
// inside methods of typed classes.
func (c *Compiler) selectorReturnsType(sel *ast.SelectorExpression, want string) bool {
	typeName := c.staticReceiverType(sel.Object)
	if typeName == "" {
		return false
	}
	classIndex, ok := c.classes[strings.ToLower(typeName)]
	if !ok {
		return false
	}
	classInfo := c.chunk.Classes[classIndex]
	for i, name := range classInfo.FieldNames {
		if strings.EqualFold(name, sel.Name.Value) {
			if i < len(classInfo.FieldTypes) {
				return strings.EqualFold(classInfo.FieldTypes[i], want)
			}
			return false
		}
	}
	return false
}

// staticReceiverType returns the class name of `expr` when it can be
// determined at compile time. Handles plain identifiers (via
// expressionStaticType) and the `this` keyword (via the enclosing
// class stack).
func (c *Compiler) staticReceiverType(expr ast.Expression) string {
	if ident, ok := expr.(*ast.Identifier); ok && strings.EqualFold(ident.Value, "this") {
		if len(c.classStack) == 0 {
			return ""
		}
		idx := c.classStack[len(c.classStack)-1]
		if idx < 0 || int(idx) >= len(c.chunk.Classes) {
			return ""
		}
		return c.chunk.Classes[idx].Name
	}
	return c.expressionStaticType(expr)
}

// callExprReturnsType reports whether a call to `e` is provably of the
// given return type. Handles direct-name function calls and
// `this.method()` / `typedReceiver.method()` style method calls.
func (c *Compiler) callExprReturnsType(e *ast.CallExpression, want string) bool {
	if ident, ok := e.Callee.(*ast.Identifier); ok {
		indices, found := c.funcs[strings.ToLower(ident.Value)]
		if !found || len(indices) == 0 {
			return false
		}
		for _, idx := range indices {
			if idx < 0 || int(idx) >= len(c.chunk.Functions) {
				return false
			}
			if !strings.EqualFold(c.chunk.Functions[idx].ReturnType, want) {
				return false
			}
		}
		return true
	}
	if sel, ok := e.Callee.(*ast.SelectorExpression); ok {
		typeName := c.expressionStaticType(sel.Object)
		if typeName == "" {
			return false
		}
		classIndex, ok := c.classes[strings.ToLower(typeName)]
		if !ok {
			return false
		}
		classInfo := c.chunk.Classes[classIndex]
		indices, ok := classInfo.Methods[strings.ToLower(sel.Name.Value)]
		if !ok || len(indices) == 0 {
			return false
		}
		for _, idx := range indices {
			if idx < 0 || int(idx) >= len(c.chunk.Functions) {
				return false
			}
			if !strings.EqualFold(c.chunk.Functions[idx].ReturnType, want) {
				return false
			}
		}
		return true
	}
	return false
}

// staticStringExpr reports whether expr is provably of static type
// `string` at compile time. Used to decide whether to emit OpAddString
// for `string + string`, mirroring staticIntExpr for the int arith
// opcodes. Nested concatenations like `"a" + b + "c"` produce a static
// string when every leaf is statically typed.
func (c *Compiler) staticStringExpr(expr ast.Expression) bool {
	switch e := expr.(type) {
	case *ast.StringLiteral:
		return true
	case *ast.Identifier:
		b, ok := c.resolveName(e.Value)
		return ok && b.typ == "string"
	case *ast.InfixExpression:
		if e.Operator == "+" {
			return c.staticStringExpr(e.Left) && c.staticStringExpr(e.Right)
		}
	case *ast.CallExpression:
		return c.callExprReturnsType(e, "string")
	case *ast.SelectorExpression:
		return c.selectorReturnsType(e, "string")
	case *ast.CastExpression:
		return strings.EqualFold(c.bytecodeTypeName(e.Type), "string")
	}
	return false
}

// resolveQualifiedClassName takes a dotted reference like `pluginmod.Plugin`
// and replaces the alias prefix with its canonical module path so the
// runtime can locate the class through the module loader.
func (c *Compiler) resolveQualifiedClassName(name string) string {
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return name
	}
	prefix := name[:dot]
	className := name[dot+1:]
	if canonical, ok := c.moduleAliases[prefix]; ok {
		return canonical + "." + className
	}
	return name
}

// canonicalModule returns the canonical native-module path that `name`
// resolves to. For an alias registered at `import` time (e.g.
// `import path as natpath`), returns the canonical path ("path"). For
// an unaliased identifier or a name that isn't a known module alias,
// returns `name` unchanged - safe for the caller to feed straight into
// `isBytecodeCallableModule` / `isStatefulBytecodeBuiltin`.
func (c *Compiler) canonicalModule(name string) string {
	if canonical, ok := c.moduleAliases[name]; ok {
		return canonical
	}
	return name
}

func (c *Compiler) resolveName(name string) (binding, bool) {
	for i := len(c.scopes) - 1; i >= 1; i-- {
		if resolved, ok := c.scopes[i][name]; ok {
			return resolved, true
		}
	}
	if slot, ok := c.globals[name]; ok {
		return binding{kind: "global", slot: slot, typ: c.globalTypes[name]}, true
	}
	return binding{}, false
}

func (c *Compiler) globalSlot(name string) int64 {
	if slot, ok := c.globals[name]; ok {
		return slot
	}
	slot := int64(len(c.globals))
	c.globals[name] = slot
	return slot
}

type globalDecl struct {
	kind      string
	canonical string // module path; lets idempotent re-import of one module pass
}

// Mirrors the evaluator's Environment.Define/DefineFunction: only func+func
// (overloads) and re-import of the same module pass; every other same-name
// top-level declaration is a redeclaration. Type aliases use a separate
// namespace and never call this.
func (c *Compiler) claimGlobalKind(name, kind, canonical string) string {
	if c.deletedGlobals[name] {
		return ""
	}
	if existing, ok := c.globalDeclKinds[name]; ok {
		if existing.kind == "func" && kind == "func" {
			return ""
		}
		if existing.kind == "import" && kind == "import" && existing.canonical == canonical {
			return ""
		}
		return fmt.Sprintf("%q is already declared in this scope", name)
	}
	c.globalDeclKinds[name] = globalDecl{kind: kind, canonical: canonical}
	return ""
}

func selectorName(expr ast.Expression) (string, string, bool) {
	selector, ok := expr.(*ast.SelectorExpression)
	if !ok {
		return "", "", false
	}
	ident, ok := selector.Object.(*ast.Identifier)
	if !ok {
		return "", "", false
	}
	return ident.Value, selector.Name.Value, true
}

func typeNameFromExpression(expr ast.Expression) (string, error) {
	switch expr := expr.(type) {
	case *ast.Identifier:
		return expr.Value, nil
	case *ast.SelectorExpression:
		if expr.Name.Value == "type" {
			return expr.Object.String(), nil
		}
		if ident, ok := expr.Object.(*ast.Identifier); ok {
			return ident.Value + "." + expr.Name.Value, nil
		}
	}
	return "", fmt.Errorf("expected type name, got %s", expr.String())
}

// appendFieldDecorators grows the parallel FieldDecorators slice up
// to the given field index (filling intermediate slots with nil) and
// stores the metadata derived from the AST decorators. Keeps the
// slice length aligned with FieldNames so the encoder writes a
// matching number of entries. Argument-decoding errors are silently
// ignored to keep field declarations parseable even when the user
// hasn't yet introduced the constants their decorator args
// reference; the runtime simply sees an empty decorator list for
// that field.
// hasImmutableFieldDecorator reports whether a field is annotated `@immutable`
// (no arguments) - the set-once field marker.
func hasImmutableFieldDecorator(decorators []ast.Decorator) bool {
	for _, dec := range decorators {
		if dec.Name != nil && dec.Name.Value == "immutable" && len(dec.Arguments) == 0 {
			return true
		}
	}
	return false
}

// withoutImmutableDecorator returns decorators with the bare `@immutable`
// removed, so it is not also stored as a reflectable/callable field decorator.
func withoutImmutableDecorator(decorators []ast.Decorator) []ast.Decorator {
	out := make([]ast.Decorator, 0, len(decorators))
	for _, dec := range decorators {
		if dec.Name != nil && dec.Name.Value == "immutable" && len(dec.Arguments) == 0 {
			continue
		}
		out = append(out, dec)
	}
	return out
}

func appendFieldDecorators(existing [][]runtime.DecoratorMetadata, fieldIndex int, decorators []ast.Decorator) [][]runtime.DecoratorMetadata {
	for len(existing) <= fieldIndex {
		existing = append(existing, nil)
	}
	if len(decorators) == 0 {
		return existing
	}
	metas, err := decoratorsMetadata(decorators, "field", 0)
	if err != nil {
		return existing
	}
	existing[fieldIndex] = metas
	return existing
}

func constantValueFromExpression(expr ast.Expression) (runtime.Value, error) {
	switch expr := expr.(type) {
	case *ast.IntegerLiteral:
		value, err := runtime.NewIntLiteral(expr.Value)
		if err != nil {
			return nil, err
		}
		if value.Value.IsInt64() {
			return runtime.SmallInt{Value: value.Value.Int64()}, nil
		}
		return value, nil
	case *ast.DecimalLiteral:
		return runtime.NewDecimalLiteral(expr.Value)
	case *ast.FloatLiteral:
		stripped := strings.ReplaceAll(expr.Value[:len(expr.Value)-1], "_", "")
		value, err := strconv.ParseFloat(stripped, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid float literal %q", expr.Value)
		}
		return runtime.Float{Value: value}, nil
	case *ast.StringLiteral:
		return runtime.String{Value: expr.Value}, nil
	case *ast.Literal:
		switch value := expr.Value.(type) {
		case bool:
			return runtime.Bool{Value: value}, nil
		case nil:
			return runtime.Null{}, nil
		}
	case *ast.ListLiteral:
		/* Accept the empty list literal as a default. Non-empty
		 * lists need runtime expression evaluation (each element
		 * could be an arbitrary expression). Empty lists are safe
		 * because the VM clones on default-fill to avoid the
		 * Python-style mutable-default trap. */
		if len(expr.Elements) == 0 {
			return &runtime.List{Elements: nil}, nil
		}
	case *ast.SetLiteral:
		if len(expr.Elements) == 0 {
			return runtime.Set{Elements: map[string]runtime.SetEntry{}}, nil
		}
	case *ast.DictLiteral:
		if len(expr.Entries) == 0 {
			return runtime.Dict{Entries: map[string]runtime.DictEntry{}}, nil
		}
	}
	return nil, fmt.Errorf("bytecode compiler only supports literal default function parameters")
}

func decoratorsMetadata(decorators []ast.Decorator, target string, overload int64) ([]runtime.DecoratorMetadata, error) {
	metadata := make([]runtime.DecoratorMetadata, 0, len(decorators))
	for position, decorator := range decorators {
		item := runtime.DecoratorMetadata{
			Target:    target,
			Position:  int64(position),
			Overload:  overload,
			Line:      int64(decorator.Token.Line),
			Column:    int64(decorator.Token.Column),
			NamedArgs: map[string]runtime.Value{},
		}
		if decorator.Name != nil {
			item.Name = decorator.Name.Value
		}
		for _, arg := range decorator.Arguments {
			value, err := decoratorConstantValue(arg.Value)
			if err != nil {
				return nil, fmt.Errorf("decorator @%s: %w", item.Name, err)
			}
			if arg.Name != nil {
				item.NamedArgs[arg.Name.Value] = value
			} else {
				item.Args = append(item.Args, value)
			}
		}
		metadata = append(metadata, item)
	}
	return metadata, nil
}

func nextOverloadIndex(decorators []runtime.DecoratorMetadata) int64 {
	next := int64(0)
	for _, decorator := range decorators {
		if decorator.Overload >= next {
			next = decorator.Overload + 1
		}
	}
	return next
}

func decoratorConstantValue(expr ast.Expression) (runtime.Value, error) {
	if value, err := constantValueFromExpression(expr); err == nil {
		return value, nil
	}
	switch expr := expr.(type) {
	case *ast.ListLiteral:
		values := make([]runtime.Value, 0, len(expr.Elements))
		for _, element := range expr.Elements {
			value, err := decoratorConstantValue(element)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return &runtime.List{Elements: values}, nil
	case *ast.DictLiteral:
		entries := map[string]runtime.DictEntry{}
		for _, entry := range expr.Entries {
			key, err := decoratorConstantValue(entry.Key)
			if err != nil {
				return nil, err
			}
			value, err := decoratorConstantValue(entry.Value)
			if err != nil {
				return nil, err
			}
			entries[native.DictKey(key)] = runtime.DictEntry{Key: key, Value: value}
		}
		return runtime.Dict{Entries: entries}, nil
	case *ast.SetLiteral:
		entries := map[string]runtime.SetEntry{}
		for _, element := range expr.Elements {
			value, err := decoratorConstantValue(element)
			if err != nil {
				return nil, err
			}
			entries[native.DictKey(value)] = runtime.SetEntry{Value: value}
		}
		return runtime.Set{Elements: entries}, nil
	default:
		return nil, fmt.Errorf("unsupported decorator argument expression %s", expr.String())
	}
}

func isBytecodeBuiltinModule(name string) bool {
	return name == "io" || name == "sys" || name == "reflect" || native.IsPureBuiltinModule(name)
}

func isBytecodeCallableModule(name string) bool {
	return isBytecodeBuiltinModule(name) || isStatefulBytecodeBuiltinModule(name)
}

func isBytecodeImportModule(path []string) bool {
	return len(path) == 1 && isBytecodeCallableModule(path[0])
}

func isEvaluatorOnlyBuiltinImport(path []string) bool {
	return len(path) == 1 && isEvaluatorOnlyBuiltinModule(path[0])
}

func isStatefulBytecodeBuiltinModule(name string) bool {
	switch name {
	case "io", "sys", "secrets", "process", "procnative", "sshnative",
		"http", "websocket", "smtp", "web", "db", "ext", "ffinative", "net", "test", "log", "watch",
		"csv", "schema", "serde", "metrics", "trace", "profile", "path", "async", "dotenv", "cli",
		"amqp", "kafka":
		return true
	default:
		return false
	}
}

func isStatefulBytecodeBuiltin(module, name string) bool {
	if isStatefulBytecodeBuiltinModule(module) {
		return true
	}
	switch module {
	case "json", "xml", "yaml":
		return name == "reader" || name == "stream"
	default:
		return false
	}
}

func isEvaluatorOnlyBuiltinModule(name string) bool {
	if isBytecodeCallableModule(name) {
		return false
	}
	switch name {
	case "reflect":
		return true
	default:
		return false
	}
}

func isBuiltinErrorClass(name string) bool {
	switch name {
	case "Error", "RuntimeError", "TypeError", "ValueError", "IOError", "ParseError", "MatchError", "ImmutableError", "PermissionError", "AssertionError", "FatalError":
		return true
	default:
		return false
	}
}

func (c *Compiler) classExtendsBuiltinError(classInfo ClassInfo) bool {
	for {
		if isBuiltinErrorClass(classInfo.ParentName) {
			return true
		}
		if classInfo.ParentIndex < 0 || int(classInfo.ParentIndex) >= len(c.chunk.Classes) {
			return false
		}
		classInfo = c.chunk.Classes[classInfo.ParentIndex]
	}
}

func callSpreadIndex(args []ast.CallArgument) (int, bool) {
	for i, arg := range args {
		if arg.Spread {
			return i, true
		}
	}
	return -1, false
}

func listHasSpread(elements []ast.Expression) bool {
	for _, e := range elements {
		if _, ok := e.(*ast.SpreadExpression); ok {
			return true
		}
	}
	return false
}

func (c *Compiler) selectFunctionCallSpread(name string) (int64, error) {
	indices := c.funcs[strings.ToLower(name)]
	if len(indices) == 0 {
		return 0, fmt.Errorf("unknown function %s", name)
	}
	if len(indices) > 1 {
		return 0, fmt.Errorf("cannot use spread with overloaded function %s", name)
	}
	return indices[0], nil
}

// IsParityError reports whether err is a VM capability gap (a construct the
// bytecode compiler doesn't support yet) rather than a genuine static error.
func IsParityError(err error) bool {
	if err == nil {
		return false
	}
	var pe parityError
	return errors.As(err, &pe)
}
