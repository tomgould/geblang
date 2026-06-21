// Package bcloader is the production bytecode module loader, importing neither evaluator nor check (those two couplings are injected via Options).
package bcloader

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"geblang/internal/ast"
	"geblang/internal/bytecode"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/native"
	"geblang/internal/parser"
	"geblang/internal/runtime"
)

// Options injects the two dependencies bcloader must not import directly.
type Options struct {
	// Compile turns module source into a chunk (production owns .gbc cache + cross-module analysis; harness is no-cache); modulePaths is the loader's live path list at the call point.
	Compile func(canonical, sourcePath string, source []byte, program *ast.Program, modulePaths []string) (bytecode.Chunk, error)

	// LookupBuiltin resolves a native Go-side module by canonical/alias, or nil.
	LookupBuiltin func(canonical, alias string) *runtime.Module
}

// Loader implements bytecode.ModuleLoader for a host VM.
type Loader struct {
	stdout      io.Writer
	modulePaths []string
	stateful    bytecode.StatefulNativeCaller
	opts        Options

	modules    map[string]*runtime.Module
	chunks     map[string]bytecode.Chunk
	globals    map[string][]runtime.Value
	decorators map[string]bytecode.FunctionDecoratorState
	// ifaceFallbacks snapshots each sub-module's interface-default tables; module "" reads live from mainVM.
	ifaceFallbacks map[string]bytecode.InterfaceFallbackState
	mainVM         *bytecode.VM
	loading        map[string]bool
	vmPools        sync.Map
	// mainChunk is the entry chunk so cross-module reflect lookups resolve user-script classes.
	mainChunk    bytecode.Chunk
	hasMainChunk bool
}

// New builds a loader; stateful may be nil and supplied later via SetStateful.
func New(stdout io.Writer, modulePaths []string, stateful bytecode.StatefulNativeCaller, opts Options) *Loader {
	return &Loader{
		stdout:         stdout,
		modulePaths:    append([]string(nil), modulePaths...),
		stateful:       stateful,
		opts:           opts,
		modules:        map[string]*runtime.Module{},
		chunks:         map[string]bytecode.Chunk{},
		globals:        map[string][]runtime.Value{},
		decorators:     map[string]bytecode.FunctionDecoratorState{},
		ifaceFallbacks: map[string]bytecode.InterfaceFallbackState{},
		loading:        map[string]bool{},
	}
}

var _ bytecode.ModuleLoader = (*Loader)(nil)

// SetMainChunk registers the entry-point chunk for cross-module reflect lookups.
func (l *Loader) SetMainChunk(chunk bytecode.Chunk) { l.mainChunk = chunk; l.hasMainChunk = true }

// SetMainVM connects the running entry VM so its globals/interface-defaults read live.
func (l *Loader) SetMainVM(vm *bytecode.VM) { l.mainVM = vm }

// SetStateful injects the stateful native caller after construction.
func (l *Loader) SetStateful(s bytecode.StatefulNativeCaller) { l.stateful = s }

// SetModulePaths replaces the module search paths used to resolve imports.
func (l *Loader) SetModulePaths(paths []string) { l.modulePaths = append([]string(nil), paths...) }

func (l *Loader) lookupBuiltin(canonical, alias string) *runtime.Module {
	if l.opts.LookupBuiltin == nil {
		return nil
	}
	return l.opts.LookupBuiltin(canonical, alias)
}

func (l *Loader) LoadModule(canonical string, alias string) (*runtime.Module, error) {
	if module, ok := l.modules[canonical]; ok {
		return module, nil
	}
	path, err := modules.NewResolver(l.modulePaths).Resolve(canonical)
	if err != nil {
		if native := l.lookupBuiltin(canonical, alias); native != nil {
			// Cache so repeated loads are a map hit, not a reconstruct.
			l.modules[canonical] = native
			return native, nil
		}
		return nil, err
	}
	if l.loading[path] {
		if native := l.lookupBuiltin(canonical, alias); native != nil {
			l.modules[canonical] = native
			return native, nil
		}
		return nil, fmt.Errorf("circular import detected for %s", canonical)
	}
	l.loading[path] = true
	defer delete(l.loading, path)

	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read module %s: %w", canonical, err)
	}
	p := parser.New(lexer.New(string(source)))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("parse module %s: %s", canonical, strings.Join(p.Errors(), "\n"))
	}
	if l.opts.Compile == nil {
		return nil, fmt.Errorf("compile module %s: loader has no Compile callback", canonical)
	}
	chunk, err := l.opts.Compile(canonical, path, source, program, l.modulePaths)
	if err != nil {
		return nil, fmt.Errorf("compile module %s: %w", canonical, err)
	}
	previousPaths := l.modulePaths
	l.modulePaths = append([]string{filepath.Dir(path)}, l.modulePaths...)
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(canonical)
	vm.SetModulePaths(l.modulePaths)
	vm.SetStatefulNativeCaller(l.stateful)
	err = vm.Run()
	l.modulePaths = previousPaths
	if err != nil {
		return nil, fmt.Errorf("evaluate module %s: %w", canonical, err)
	}
	exports, err := vm.Exports()
	if err != nil {
		return nil, fmt.Errorf("export module %s: %w", canonical, err)
	}
	module := &runtime.Module{Name: alias, Canonical: canonical, Exports: exports}
	for name, value := range module.Exports {
		if function, ok := value.(runtime.BytecodeFunction); ok {
			function.Module = canonical
			module.Exports[name] = function
		}
		if class, ok := value.(runtime.BytecodeClass); ok {
			class.Module = canonical
			module.Exports[name] = class
		}
	}
	l.modules[canonical] = module
	l.chunks[canonical] = chunk
	l.globals[canonical] = vm.GlobalsSnapshot()
	l.decorators[canonical] = vm.FunctionDecoratorState()
	l.ifaceFallbacks[canonical] = vm.InterfaceFallbackState()
	return module, nil
}

// moved verbatim from cmd/geblang/main.go (receiver renamed)
func (l *Loader) ifaceFallbackStateFor(module string) bytecode.InterfaceFallbackState {
	if module == "" && l.mainVM != nil {
		return l.mainVM.InterfaceFallbackState()
	}
	return l.ifaceFallbacks[module]
}

// Entry-chunk globals come live from the main VM so a cross-module dispatch sees its imports.
func (l *Loader) globalsFor(module string) []runtime.Value {
	if module == "" && l.mainVM != nil {
		return l.mainVM.GlobalsSnapshot()
	}
	return l.globals[module]
}

// moduleVM returns a pooled (or fresh) host VM bound to the module's
// chunk and configured with the module's canonical state. Hosts carry
// per-call configuration only; pooling them avoids a full VM
// construction per cross-module call.
func (l *Loader) moduleVM(module string, chunk bytecode.Chunk) (*bytecode.VM, *sync.Pool) {
	var pool *sync.Pool
	if p, ok := l.vmPools.Load(module); ok {
		pool = p.(*sync.Pool)
	} else {
		p, _ := l.vmPools.LoadOrStore(module, &sync.Pool{})
		pool = p.(*sync.Pool)
	}
	vm, _ := pool.Get().(*bytecode.VM)
	if vm == nil {
		vm = bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	} else {
		vm.ResetForReuse()
	}
	vm.SetModuleName(module)
	vm.SetModulePaths(l.modulePaths)
	vm.SetStatefulNativeCaller(l.stateful)
	vm.RestoreGlobals(l.globalsFor(module))
	vm.RestoreFunctionDecoratorState(l.decorators[module])
	vm.RestoreInterfaceFallbackState(l.ifaceFallbackStateFor(module))
	return vm, pool
}

func releaseModuleVM(pool *sync.Pool, vm *bytecode.VM, err error) {
	if err == nil && vm.Recyclable() {
		pool.Put(vm)
	}
}

func (l *Loader) CallModuleFunction(function runtime.BytecodeFunction, args []runtime.Value) (runtime.Value, error) {
	var chunk bytecode.Chunk
	if function.Module == "" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("entry-script function invoked without a main chunk")
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[function.Module]
		if !ok {
			return nil, fmt.Errorf("module %s is not loaded", function.Module)
		}
		chunk = c
	}
	vm, pool := l.moduleVM(function.Module, chunk)
	result, err := vm.CallFunction(function.Index, args)
	releaseModuleVM(pool, vm, err)
	return result, err
}

func (l *Loader) CallModuleClosure(closure runtime.BytecodeClosure, args []runtime.Value) (runtime.Value, error) {
	var chunk bytecode.Chunk
	if closure.Module == "" {
		// Closures created in the entry script carry Module="".
		// Dispatch against the main chunk so the FunctionIndex
		// resolves to the closure's body rather than whatever
		// happens to live at that index in some sub-VM's chunk.
		if !l.hasMainChunk {
			return nil, fmt.Errorf("entry-script closure invoked without a main chunk")
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[closure.Module]
		if !ok {
			return nil, fmt.Errorf("module %s is not loaded", closure.Module)
		}
		chunk = c
	}
	vm, pool := l.moduleVM(closure.Module, chunk)
	result, err := vm.CallClosure(closure, args)
	releaseModuleVM(pool, vm, err)
	return result, err
}

func (l *Loader) ConstructModuleClass(class runtime.BytecodeClass, args []runtime.Value, typeArgs []string) (runtime.Value, error) {
	var chunk bytecode.Chunk
	if class.Module == "" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("construct %s: main chunk is not registered", class.Name)
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[class.Module]
		if !ok {
			return nil, fmt.Errorf("module %s is not loaded", class.Module)
		}
		chunk = c
	}
	vm, pool := l.moduleVM(class.Module, chunk)
	result, err := vm.ConstructClassWithTypeArgs(class.Index, args, typeArgs)
	releaseModuleVM(pool, vm, err)
	return result, err
}

// ConstructorsForModuleClass evaluates `reflect.constructors(class)`
// against the chunk that declared the class.
func (l *Loader) ConstructorsForModuleClass(class runtime.BytecodeClass) (runtime.Value, error) {
	var chunk bytecode.Chunk
	if class.Module == "" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("reflect.constructors %s: main chunk is not registered", class.Name)
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[class.Module]
		if !ok {
			return nil, fmt.Errorf("module %s is not loaded", class.Module)
		}
		chunk = c
	}
	vm, pool := l.moduleVM(class.Module, chunk)
	result, err := vm.ReflectConstructorsForChunkClass(class)
	releaseModuleVM(pool, vm, err)
	return result, err
}

func (l *Loader) FieldsForModuleClass(class runtime.BytecodeClass) (runtime.Value, error) {
	var chunk bytecode.Chunk
	if class.Module == "" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("reflect.fields %s: main chunk is not registered", class.Name)
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[class.Module]
		if !ok {
			return nil, fmt.Errorf("module %s is not loaded", class.Module)
		}
		chunk = c
	}
	vm, pool := l.moduleVM(class.Module, chunk)
	result, err := vm.ReflectFieldsForChunkClass(class)
	releaseModuleVM(pool, vm, err)
	return result, err
}

// DeserializeModuleClass picks the right chunk for a class returned
// from another module (or the main program) and runs the local
// deserialize path on a sub-VM bound to that chunk.
func (l *Loader) DeserializeModuleClass(class runtime.BytecodeClass, value runtime.Value) (runtime.Value, error) {
	var chunk bytecode.Chunk
	if class.Module == "" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("deserialize %s: main chunk is not registered", class.Name)
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[class.Module]
		if !ok {
			return nil, fmt.Errorf("module %s is not loaded", class.Module)
		}
		chunk = c
	}
	vm, pool := l.moduleVM(class.Module, chunk)
	result, err := vm.DeserializeIntoChunkClass(class, value)
	releaseModuleVM(pool, vm, err)
	return result, err
}

func (l *Loader) CallModuleStaticMethod(class runtime.BytecodeClass, methodName string, args []runtime.Value) (runtime.Value, error) {
	chunk, ok := l.chunks[class.Module]
	if !ok {
		return nil, fmt.Errorf("module %s is not loaded", class.Module)
	}
	vm, pool := l.moduleVM(class.Module, chunk)
	result, err := vm.CallStaticMethod(class.Index, methodName, args)
	releaseModuleVM(pool, vm, err)
	return result, err
}

func (l *Loader) CallParentInModule(module string, className string, methodName string, instance *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	var chunk bytecode.Chunk
	if module == "" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("entry-script parent dispatch called without a main chunk")
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[module]
		if !ok {
			return nil, fmt.Errorf("module %s is not loaded", module)
		}
		chunk = c
	}
	vm, pool := l.moduleVM(module, chunk)
	result, err := vm.CallMethodAs(className, instance, methodName, args)
	releaseModuleVM(pool, vm, err)
	return result, err
}

func (l *Loader) ImmutableFieldsForModuleClass(module string, className string) []string {
	var chunk bytecode.Chunk
	if module == "" {
		if !l.hasMainChunk {
			return nil
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[module]
		if !ok {
			return nil
		}
		chunk = c
	}
	for i := range chunk.Classes {
		if chunk.Classes[i].Name == className {
			return chunk.Classes[i].ImmutableFields
		}
	}
	return nil
}

func (l *Loader) chunkFor(module string) (bytecode.Chunk, bool) {
	if module == "" {
		if !l.hasMainChunk {
			return bytecode.Chunk{}, false
		}
		return l.mainChunk, true
	}
	c, ok := l.chunks[module]
	return c, ok
}

func (l *Loader) ModuleClassDescendsFrom(module, className, targetSimpleName string) bool {
	chunk, ok := l.chunkFor(module)
	if !ok {
		return false
	}
	for i := range chunk.Classes {
		if !strings.EqualFold(chunk.Classes[i].Name, className) {
			continue
		}
		for ci := chunk.Classes[i]; ; {
			if strings.EqualFold(ci.Name, targetSimpleName) {
				return true
			}
			if ci.ParentIndex >= 0 && int(ci.ParentIndex) < len(chunk.Classes) {
				ci = chunk.Classes[ci.ParentIndex]
				continue
			}
			if dot := strings.LastIndex(ci.ParentName, "."); dot >= 0 {
				return l.ModuleClassDescendsFrom(ci.ParentName[:dot], ci.ParentName[dot+1:], targetSimpleName)
			}
			return false
		}
	}
	return false
}

func (l *Loader) StaticValueForModuleClass(module, className, name string) (runtime.Value, bool) {
	chunk, ok := l.chunkFor(module)
	if !ok {
		return nil, false
	}
	for i := range chunk.Classes {
		if !strings.EqualFold(chunk.Classes[i].Name, className) {
			continue
		}
		for ci := chunk.Classes[i]; ; {
			if idx, present := ci.StaticValues[name]; present && idx >= 0 && int(idx) < len(chunk.Constants) {
				return chunk.Constants[idx], true
			}
			if ci.ParentIndex >= 0 && int(ci.ParentIndex) < len(chunk.Classes) {
				ci = chunk.Classes[ci.ParentIndex]
				continue
			}
			if dot := strings.LastIndex(ci.ParentName, "."); dot >= 0 {
				return l.StaticValueForModuleClass(ci.ParentName[:dot], ci.ParentName[dot+1:], name)
			}
			return nil, false
		}
	}
	return nil, false
}

func (l *Loader) CallModuleStaticMethodByName(module, className, methodName string, args []runtime.Value) (runtime.Value, bool, error) {
	chunk, ok := l.chunkFor(module)
	if !ok {
		return nil, false, nil
	}
	for i := range chunk.Classes {
		if !strings.EqualFold(chunk.Classes[i].Name, className) {
			continue
		}
		for ci := chunk.Classes[i]; ; {
			if _, present := ci.StaticMethods[strings.ToLower(methodName)]; present {
				vm, pool := l.moduleVM(module, chunk)
				result, err := vm.CallStaticMethod(int64(i), methodName, args)
				releaseModuleVM(pool, vm, err)
				return result, true, err
			}
			if ci.ParentIndex >= 0 && int(ci.ParentIndex) < len(chunk.Classes) {
				ci = chunk.Classes[ci.ParentIndex]
				continue
			}
			if dot := strings.LastIndex(ci.ParentName, "."); dot >= 0 {
				return l.CallModuleStaticMethodByName(ci.ParentName[:dot], ci.ParentName[dot+1:], methodName, args)
			}
			return nil, false, nil
		}
	}
	return nil, false, nil
}

func (l *Loader) UnimplementedAbstractMethods(module, className string) map[string]string {
	chunk, ok := l.chunkFor(module)
	if !ok {
		return nil
	}
	for i := range chunk.Classes {
		if !strings.EqualFold(chunk.Classes[i].Name, className) {
			continue
		}
		overridden := map[string]bool{}
		abstractDecl := map[string]string{}
		for ci := chunk.Classes[i]; ; {
			for method := range ci.Methods {
				isAbstract := false
				for _, dec := range ci.MethodDecorators[method] {
					if strings.EqualFold(dec.Name, "abstract") {
						isAbstract = true
						break
					}
				}
				if isAbstract {
					if !overridden[method] {
						if _, seen := abstractDecl[method]; !seen {
							abstractDecl[method] = ci.Name
						}
					}
				} else {
					overridden[method] = true
					delete(abstractDecl, method)
				}
			}
			if ci.ParentIndex >= 0 && int(ci.ParentIndex) < len(chunk.Classes) {
				ci = chunk.Classes[ci.ParentIndex]
				continue
			}
			if dot := strings.LastIndex(ci.ParentName, "."); dot >= 0 {
				for method, owner := range l.UnimplementedAbstractMethods(ci.ParentName[:dot], ci.ParentName[dot+1:]) {
					if !overridden[method] {
						if _, seen := abstractDecl[method]; !seen {
							abstractDecl[method] = owner
						}
					}
				}
			}
			break
		}
		return abstractDecl
	}
	return nil
}

func (l *Loader) ModuleMethodParamNames(module string, className string, methodName string) ([]string, error) {
	var chunk bytecode.Chunk
	if module == "" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("entry-script class %s referenced without a main chunk", className)
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[module]
		if !ok {
			return nil, fmt.Errorf("module %s is not loaded", module)
		}
		chunk = c
	}
	return chunk.MethodParamNames(className, methodName)
}

func (l *Loader) CallModuleMethod(module string, className string, methodName string, instance *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	var chunk bytecode.Chunk
	if module == "" {
		// Main-chunk classes carry Module="". Sub-VMs running stdlib
		// modules dispatch into the entry chunk through this branch
		// when invoking a method on a user instance (e.g. the F3
		// dunder protocol: streams.copy(src, ...) calls
		// src.__read(n) where src is a main-chunk class).
		if !l.hasMainChunk {
			return nil, fmt.Errorf("entry-script instance method %s.%s called without a main chunk", className, methodName)
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[module]
		if !ok {
			// Native modules (http, process, ...) carry no Geblang chunk, so an
			// unresolved method on one of their class instances is simply
			// undefined, not an unloaded module; report it as such so the VM
			// matches the evaluator's "unknown method" error.
			if native.IsNativeModule(module) {
				return nil, &runtime.MethodNotFoundError{Class: className, Method: methodName}
			}
			return nil, fmt.Errorf("module %s is not loaded", module)
		}
		chunk = c
	}
	vm, pool := l.moduleVM(module, chunk)
	result, err := vm.CallMethod(instance, methodName, args)
	releaseModuleVM(pool, vm, err)
	return result, err
}

func (l *Loader) ListAllClasses() []runtime.Value {
	out := []runtime.Value{}
	appendChunkClasses := func(module string, chunk bytecode.Chunk) {
		for i, classInfo := range chunk.Classes {
			out = append(out, runtime.BytecodeClass{
				Name:             classInfo.Name,
				Doc:              classInfo.Doc,
				Index:            int64(i),
				Module:           module,
				Parent:           classInfo.ParentName,
				Fields:           append([]string(nil), classInfo.FieldNames...),
				Interfaces:       append([]string(nil), classInfo.Implements...),
				Decorators:       classInfo.Decorators,
				MethodDecorators: classInfo.MethodDecorators,
				StaticDecorators: classInfo.StaticDecorators,
			})
		}
	}
	for module, chunk := range l.chunks {
		appendChunkClasses(module, chunk)
	}
	// Entry chunk too, so reflect.classes() from an imported module sees it.
	if l.hasMainChunk {
		appendChunkClasses("", l.mainChunk)
	}
	return out
}

func (l *Loader) LookupModuleInterface(module, name string) (bytecode.InterfaceInfo, bool) {
	var chunk bytecode.Chunk
	if module == "" {
		if !l.hasMainChunk {
			return bytecode.InterfaceInfo{}, false
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[module]
		if !ok {
			return bytecode.InterfaceInfo{}, false
		}
		chunk = c
	}
	for _, iface := range chunk.Interfaces {
		if strings.EqualFold(iface.Name, name) {
			return iface, true
		}
	}
	return bytecode.InterfaceInfo{}, false
}

// FindFunctionByName scans every loaded module's chunk for an
// exported function by name. Returns nil when no match.
func (l *Loader) FindFunctionByName(name string) (runtime.Value, bool) {
	for moduleName, module := range l.modules {
		if module == nil {
			continue
		}
		if value, ok := module.Exports[name]; ok {
			switch v := value.(type) {
			case runtime.Function, runtime.OverloadedFunction, runtime.BytecodeFunction, runtime.DecoratorTarget:
				_ = moduleName
				return v, true
			}
		}
	}
	return nil, false
}

// FindClassByName scans every loaded module's chunk for a class with
// the given bare name and returns a BytecodeClass value bound to that
// chunk. Used by reflect.class(name) so framework helpers can resolve
// a user class without needing the originating module on import.
func (l *Loader) FindClassByName(name string) (runtime.Value, bool) {
	key := strings.ToLower(name)
	for moduleName, chunk := range l.chunks {
		for idx, classInfo := range chunk.Classes {
			if strings.EqualFold(classInfo.Name, name) || strings.ToLower(classInfo.Name) == key {
				return runtime.BytecodeClass{
					Name:             classInfo.Name,
					Doc:              classInfo.Doc,
					Index:            int64(idx),
					Module:           moduleName,
					Parent:           classInfo.ParentName,
					Fields:           append([]string(nil), classInfo.FieldNames...),
					Interfaces:       append([]string(nil), classInfo.Implements...),
					Decorators:       classInfo.Decorators,
					MethodDecorators: classInfo.MethodDecorators,
					StaticDecorators: classInfo.StaticDecorators,
					DefLine:          classInfo.DefLine,
					DefColumn:        classInfo.DefColumn,
				}, true
			}
		}
	}
	if l.hasMainChunk {
		for idx, classInfo := range l.mainChunk.Classes {
			if strings.EqualFold(classInfo.Name, name) || strings.ToLower(classInfo.Name) == key {
				return runtime.BytecodeClass{
					Name:             classInfo.Name,
					Doc:              classInfo.Doc,
					Index:            int64(idx),
					Module:           "",
					Parent:           classInfo.ParentName,
					Fields:           append([]string(nil), classInfo.FieldNames...),
					Interfaces:       append([]string(nil), classInfo.Implements...),
					Decorators:       classInfo.Decorators,
					MethodDecorators: classInfo.MethodDecorators,
					StaticDecorators: classInfo.StaticDecorators,
					DefLine:          classInfo.DefLine,
					DefColumn:        classInfo.DefColumn,
				}, true
			}
		}
	}
	return nil, false
}
