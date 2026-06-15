package bytecode

import (
	"errors"
	"fmt"
	"geblang/internal/native"
	"geblang/internal/runtime"
	"io"
	"os"
	"strings"
)

func NewVM(chunk Chunk, stdout io.Writer) *VM {
	classIndex := make(map[string]int, len(chunk.Classes))
	for i, classInfo := range chunk.Classes {
		classIndex[strings.ToLower(classInfo.Name)] = i
	}
	initialDefers := make([][]deferredAction, 1, 64)
	// Pre-allocate the operand stack to a reasonable size so the first ~1000
	// stack pushes (typical for non-pathological programs) don't trigger
	// slice growth. 1024 entries × 32 bytes (VMValue) = 32 KB; cheap.
	initialStack := make([]runtime.VMValue, 0, 1024)
	// Pre-size globals and top-level locals to the compiled chunk's
	// known counts so the VM-internal hot helpers can skip bounds checks.
	// 256 is the historical minimum; round up to that floor.
	globalSize := int(chunk.GlobalCount)
	if globalSize < 256 {
		globalSize = 256
	}
	localsCap := int(chunk.TopLevelLocalCount)
	if localsCap < 256 {
		localsCap = 256
	}
	if meta := chunk.sharedMeta; meta != nil {
		// Shared-meta fast path: the chunk carries once-prepared
		// (memoised) functions, the class index, and type-assert
		// specs; this VM shares them read-only instead of detaching
		// and re-deriving per construction.
		meta.prepare(chunk)
		chunk.Functions = meta.preparedFunctions
		vm := &VM{chunk: chunk, stdout: stdout, globals: make([]runtime.VMValue, globalSize), dirtyGlobals: make([]bool, globalSize), localsStack: make([]runtime.VMValue, 0, localsCap), stack: initialStack, maxCallDepth: DefaultMaxCallDepth, frames: make([]callFrame, 0, 256), defers: initialDefers, classIndex: meta.classIndex, natives: native.NewBuiltinRegistry(), nativeCache: make([]native.Function, len(chunk.Constants)), decoratedFuncs: map[int64]runtime.Value{}, decoratedClasses: map[int64]runtime.Value{}, rawFunctionCalls: map[int64]bool{}, methodReceiverFuncs: map[int64]bool{}, interfaceFallbacks: map[int64]map[string]crossModuleDefault{}, interfaceExtraFields: map[int64][]extraField{}, typeSpecCache: map[string]vmTypeSpec{}, runInlineExitDepth: -1}
		vm.typeAssertSpecs = meta.typeAssertSpecs
		vm.requiresCallSitePolymorphism = meta.requiresCallSitePolymorphism
		native.SetInstanceInvoker(vm.invokeInstanceMethod)
		native.SetClassDeserializer(vm.deserializeIntoClass)
		native.SetCallableInvoker(vm.invokeCallable)
		return vm
	}
	// Detach Functions so prepareFunctionTypeMetadata can mutate the
	// per-VM metadata (typeParamSet, paramTypeSpecs, etc.) without
	// racing concurrent VMs that share the same source chunk.
	chunk.Functions = append([]FunctionInfo(nil), chunk.Functions...)
	vm := &VM{chunk: chunk, stdout: stdout, globals: make([]runtime.VMValue, globalSize), dirtyGlobals: make([]bool, globalSize), localsStack: make([]runtime.VMValue, 0, localsCap), stack: initialStack, maxCallDepth: DefaultMaxCallDepth, frames: make([]callFrame, 0, 256), defers: initialDefers, classIndex: classIndex, natives: native.NewBuiltinRegistry(), nativeCache: make([]native.Function, len(chunk.Constants)), decoratedFuncs: map[int64]runtime.Value{}, decoratedClasses: map[int64]runtime.Value{}, rawFunctionCalls: map[int64]bool{}, methodReceiverFuncs: map[int64]bool{}, interfaceFallbacks: map[int64]map[string]crossModuleDefault{}, interfaceExtraFields: map[int64][]extraField{}, typeSpecCache: map[string]vmTypeSpec{}, runInlineExitDepth: -1}
	vm.prepareFunctionTypeMetadata()
	// Register an InstanceInvoker so native code (e.g.
	// convert.go's __serialize__ dispatch) can call class
	// methods. Latest-writer-wins is intentional: when both
	// backends initialize in the same process, only one is
	// actively running serialization through native code at a
	// time.
	native.SetInstanceInvoker(vm.invokeInstanceMethod)
	native.SetClassDeserializer(vm.deserializeIntoClass)
	native.SetCallableInvoker(vm.invokeCallable)
	return vm
}

// deserializeIntoClass implements native.ClassDeserializer for
// the bytecode VM. Tries static __deserialize__ first; falls back
// to positional constructor calls with dict keys matched against
// constructor parameter names.
func (vm *VM) deserializeIntoClass(classValue runtime.Value, value runtime.Value) (runtime.Value, error) {
	class, ok := classValue.(runtime.BytecodeClass)
	if !ok {
		// `reflect.class("Name")` on the VM can compile-time-resolve
		// to a DecoratorTarget; promote it to a BytecodeClass via
		// the chunk's class index so json.parseAs et al. can
		// deserialize through the same path.
		if target, isTarget := classValue.(runtime.DecoratorTarget); isTarget && target.Target == "class" && target.Class != nil {
			if idx, ok := vm.classIndex[strings.ToLower(target.Class.Name)]; ok && int(idx) < len(vm.chunk.Classes) {
				class = vm.bytecodeClassFromInfo(vm.chunk.Classes[idx], int64(idx))
			} else if vm.moduleLoader != nil {
				if v, found := vm.moduleLoader.FindClassByName(target.Class.Name); found {
					if bc, isClass := v.(runtime.BytecodeClass); isClass {
						class = bc
					} else {
						return nil, fmt.Errorf("deserialize: expected class, got %s", classValue.TypeName())
					}
				} else {
					return nil, fmt.Errorf("deserialize: expected class, got %s", classValue.TypeName())
				}
			} else {
				return nil, fmt.Errorf("deserialize: expected class, got %s", classValue.TypeName())
			}
		} else {
			return nil, fmt.Errorf("deserialize: expected class, got %s", classValue.TypeName())
		}
	}
	// If the class was declared in a different chunk than the one
	// this VM is bound to, dispatch to a sub-VM via the moduleLoader
	// so the indices resolve against the right chunk. The check
	// covers both directions: a sub-VM holding a main-chunk class
	// (class.Module=="" while vm.moduleName!=""), and the main VM
	// holding an imported-module class.
	if vm.moduleLoader != nil && class.Module != vm.moduleName {
		return vm.moduleLoader.DeserializeModuleClass(class, value)
	}
	if class.Index < 0 || int(class.Index) >= len(vm.chunk.Classes) {
		return nil, fmt.Errorf("deserialize %s: class index out of range", class.Name)
	}
	classInfo := vm.chunk.Classes[class.Index]
	if indices, ok := vm.lookupStaticMethod(classInfo, "__deserialize__"); ok && len(indices) > 0 {
		args := []runtime.Value{value}
		functionIndex, err := vm.selectRuntimeFunction(Instruction{}, "__deserialize__", indices, args, 0)
		if err != nil {
			return nil, fmt.Errorf("deserialize %s: %w", class.Name, err)
		}
		return vm.CallFunctionRaw(functionIndex, args)
	}
	dict, ok := value.(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("deserialize %s: expected dict, got %s", class.Name, value.TypeName())
	}
	if len(classInfo.ConstructorIndices) == 0 {
		// Classes without an explicit constructor get their fields
		// populated directly from the dict. Matches the evaluator's
		// data-class behaviour so `json.parseAs(text, MyDTO)` is
		// usable without writing a constructor.
		v, err := vm.ConstructClass(class.Index, nil)
		if err != nil {
			return nil, err
		}
		instance, ok := v.(*runtime.Instance)
		if !ok {
			return v, nil
		}
		for _, fieldName := range classInfo.FieldNames {
			key := runtime.String{Value: fieldName}
			entry, hit := dict.GetEntry(native.DictKey(key))
			if hit {
				instance.Fields[fieldName] = entry.Value
			}
		}
		return instance, nil
	}
	ctorIndex := classInfo.ConstructorIndices[0]
	if ctorIndex < 0 || int(ctorIndex) >= len(vm.chunk.Functions) {
		return nil, fmt.Errorf("deserialize %s: constructor index out of range", class.Name)
	}
	ctor := vm.chunk.Functions[ctorIndex]
	args := make([]runtime.Value, 0, len(ctor.ParamNames))
	for _, paramName := range ctor.ParamNames {
		// Skip the implicit "this" receiver, which the compiler
		// prepends to method/constructor parameter lists.
		if paramName == "this" {
			continue
		}
		key := runtime.String{Value: paramName}
		entry, ok := dict.GetEntry(native.DictKey(key))
		if !ok {
			return nil, fmt.Errorf("deserialize %s: missing field %q", class.Name, paramName)
		}
		args = append(args, entry.Value)
	}
	return vm.ConstructClass(class.Index, args)
}

func NewVMWithModuleLoader(chunk Chunk, stdout io.Writer, loader ModuleLoader) *VM {
	vm := NewVM(chunk, stdout)
	vm.moduleLoader = loader
	return vm
}

// Cleanup invokes the destructor of every tracked instance whose
// class declared `func ~ClassName()`. Called at program exit by the
// CLI shutdown path. Instances are visited in reverse-creation
// order so younger objects clean up first. A destructor that
// instantiates more destructor-bearing objects is handled by
// re-draining the registry, bounded by maxDepth to guard against
// pathological recursion.
func (vm *VM) Cleanup() error {
	const maxDepth = 4
	for depth := 0; depth < maxDepth; depth++ {
		if len(vm.destructibleInstances) == 0 {
			return nil
		}
		batch := vm.destructibleInstances
		vm.destructibleInstances = nil
		for i := len(batch) - 1; i >= 0; i-- {
			inst := batch[i]
			if inst == nil || inst.Destroyed || inst.Class == nil {
				continue
			}
			classInfo, ok := vm.classInfo(inst.Class.Name)
			if !ok || classInfo.DestructorIndex < 0 {
				continue
			}
			inst.Destroyed = true
			if _, err := vm.CallFunctionRaw(classInfo.DestructorIndex, []runtime.Value{inst}); err != nil {
				fmt.Fprintln(vm.destructorStderr(), runtime.RenderDestructorFailure(inst.Class.Name, destructorFailure(err)))
			}
		}
	}
	return nil
}

func (vm *VM) destructorStderr() io.Writer {
	if vm.stderr != nil {
		return vm.stderr
	}
	return os.Stderr
}

// destructorFailure recovers the typed throw from a destructor error; non-throw faults class as RuntimeError.
func destructorFailure(err error) runtime.Error {
	var thrown vmThrownError
	if errors.As(err, &thrown) {
		return thrown.err
	}
	return runtime.NewRecoverableError(err)
}

// GlobalsSnapshot returns a copy of the current globals as a
// []runtime.Value for callers that interoperate with the evaluator's
// environment model. Internally globals are stored as []VMValue; the
// snapshot pays the conversion cost once at the boundary.
func (vm *VM) GlobalsSnapshot() []runtime.Value {
	out := make([]runtime.Value, len(vm.globals))
	for i, v := range vm.globals {
		out[i] = v.ToValue()
	}
	return out
}

// RestoreGlobals replaces the globals slice with a converted copy of
// the caller-provided []runtime.Value.
func (vm *VM) RestoreGlobals(globals []runtime.Value) {
	if cap(vm.globals) >= len(globals) {
		vm.globals = vm.globals[:len(globals)]
	} else {
		vm.globals = make([]runtime.VMValue, len(globals))
	}
	for i, v := range globals {
		vm.globals[i] = runtime.VMValueFromValue(v)
	}
}

// restoreGlobalsVM is the VM-internal fast path used when one VM hands
// its globals to a wrapper / generator VM. It copies the VMValue slice
// directly without round-tripping through runtime.Value.
func (vm *VM) restoreGlobalsVM(globals []runtime.VMValue) {
	vm.globals = append(vm.globals[:0], globals...)
}

func (vm *VM) FunctionDecoratorState() FunctionDecoratorState {
	return FunctionDecoratorState{
		decorated:           copyRuntimeValueMap(vm.decoratedFuncs),
		decoratedClasses:    copyRuntimeValueMap(vm.decoratedClasses),
		applied:             vm.decoratorsApplied,
		methodReceiverFuncs: copyBoolMap(vm.methodReceiverFuncs),
	}
}

func (vm *VM) RestoreFunctionDecoratorState(state FunctionDecoratorState) {
	vm.decoratedFuncs = copyRuntimeValueMap(state.decorated)
	if state.decoratedClasses != nil {
		vm.decoratedClasses = copyRuntimeValueMap(state.decoratedClasses)
	}
	vm.decoratorsApplied = state.applied
	if state.methodReceiverFuncs != nil {
		vm.methodReceiverFuncs = copyBoolMap(state.methodReceiverFuncs)
	}
}

// InterfaceFallbackState carries a module's runtime interface-default tables
// (built at OpDefineClass) so cross-module-spawned VMs can resolve interface
// default methods and interface-provided fields, mirroring globals/decorator
// state restoration.
type InterfaceFallbackState struct {
	fallbacks   map[int64]map[string]crossModuleDefault
	extraFields map[int64][]extraField
}

func (vm *VM) InterfaceFallbackState() InterfaceFallbackState {
	fallbacks := make(map[int64]map[string]crossModuleDefault, len(vm.interfaceFallbacks))
	for idx, methods := range vm.interfaceFallbacks {
		inner := make(map[string]crossModuleDefault, len(methods))
		for name, def := range methods {
			inner[name] = def
		}
		fallbacks[idx] = inner
	}
	extras := make(map[int64][]extraField, len(vm.interfaceExtraFields))
	for idx, fields := range vm.interfaceExtraFields {
		extras[idx] = append([]extraField(nil), fields...)
	}
	return InterfaceFallbackState{fallbacks: fallbacks, extraFields: extras}
}

func (vm *VM) RestoreInterfaceFallbackState(state InterfaceFallbackState) {
	if state.fallbacks != nil {
		vm.interfaceFallbacks = state.fallbacks
	}
	if state.extraFields != nil {
		vm.interfaceExtraFields = state.extraFields
	}
}

func (vm *VM) noteEscape() { vm.escapedRefs.Add(1) }

// resetForReuse clears the per-run mutable state of a wrapper VM so a
// pooled instance can serve another call over the same template chunk
// (Functions/Classes/meta identical; the caller re-points Constants and
// Instructions and re-applies its per-call setup). Chunk-shape caches
// (nativeCache, nameLowerCache, class/method lookup caches,
// typeSpecCache) are deliberately kept.
// ResetForReuse clears per-run state so a pooled VM can host another
// call against the same chunk.
func (vm *VM) ResetForReuse() { vm.resetForReuse() }

// Recyclable reports whether this VM handed out no vm-capturing
// closures during its run (safe to pool).
func (vm *VM) Recyclable() bool { return vm.escapedRefs.Load() == 0 }

func (vm *VM) resetForReuse() {
	vm.stack = vm.stack[:0]
	vm.localsStack = vm.localsStack[:0]
	vm.frames = vm.frames[:0]
	if cap(vm.defers) > 0 {
		vm.defers = vm.defers[:1]
		vm.defers[0] = vm.defers[0][:0]
	} else {
		vm.defers = make([][]deferredAction, 1, 64)
	}
	for i := range vm.dirtyGlobals {
		vm.dirtyGlobals[i] = false
	}
	vm.pendingThrow = nil
	vm.exceptionHandlers = vm.exceptionHandlers[:0]
	vm.constantsExtra = nil
	vm.staticLocal = nil
	vm.staticsLocalOnly = false
	vm.runEntryIP = 0
	vm.runInlineExitDepth = -1
	vm.inDispatchLoop = false
	vm.destructibleInstances = nil
	vm.pendingTypeBindings = nil
	vm.forwardThis = nil
	vm.generatorExecution = false
	vm.generatorYield = nil
	vm.generatorDone = nil
	vm.interfaceFallbacks = map[int64]map[string]crossModuleDefault{}
	vm.interfaceExtraFields = map[int64][]extraField{}
	vm.bridgeActive.Store(false)
	vm.escapedRefs.Store(0)
	// The global native hooks must point at a live VM for this chunk.
	native.SetInstanceInvoker(vm.invokeInstanceMethod)
	native.SetClassDeserializer(vm.deserializeIntoClass)
	native.SetCallableInvoker(vm.invokeCallable)
}

func (vm *VM) prepareFunctionTypeMetadata() {
	hasCallSitePolymorphism := false
	for i := range vm.chunk.Functions {
		function := &vm.chunk.Functions[i]
		function.typeParamSet = functionTypeParameterSetOrNil(*function)
		function.requiresParamValidation = functionRequiresParamValidation(*function)
		if function.Async || function.IsGenerator || len(function.Decorators) > 0 {
			hasCallSitePolymorphism = true
		}
		if len(function.ParamTypes) == 0 {
			continue
		}
		function.paramTypeSpecs = make([]vmTypeSpec, len(function.ParamTypes))
		for j, typ := range function.ParamTypes {
			if typ != "" {
				function.paramTypeSpecs[j] = vm.typeSpec(typ)
			}
		}
	}
	if !hasCallSitePolymorphism {
		for _, classInfo := range vm.chunk.Classes {
			if len(classInfo.Decorators) > 0 || len(classInfo.MethodDecorators) > 0 {
				hasCallSitePolymorphism = true
				break
			}
		}
	}
	vm.requiresCallSitePolymorphism = hasCallSitePolymorphism
	for _, instruction := range vm.chunk.Instructions {
		if instruction.Op != OpTypeAssert || len(instruction.Operands) != 1 {
			continue
		}
		constIdx := instruction.Operands[0]
		if constIdx < 0 || int(constIdx) >= len(vm.chunk.Constants) {
			continue
		}
		typeStr, ok := vm.chunk.Constants[constIdx].(runtime.String)
		if !ok {
			continue
		}
		if vm.typeAssertSpecs == nil {
			vm.typeAssertSpecs = map[int64]vmTypeSpec{}
		}
		vm.typeAssertSpecs[constIdx] = vm.typeSpec(typeStr.Value)
	}
}

func (vm *VM) snapshotCurrentFrameLocals() (snapshot []runtime.VMValue, base int) {
	if len(vm.frames) == 0 {
		return nil, 0
	}
	frame := vm.frames[len(vm.frames)-1]
	count := frame.localCount
	if count == 0 || frame.basePointer >= len(vm.localsStack) {
		return nil, frame.basePointer
	}
	end := frame.basePointer + count
	if end > len(vm.localsStack) {
		end = len(vm.localsStack)
	}
	snap := make([]runtime.VMValue, end-frame.basePointer)
	copy(snap, vm.localsStack[frame.basePointer:end])
	return snap, frame.basePointer
}

// takeCallArgsBuffer returns a []runtime.Value of length size, reusing
// a previously released slice from the per-VM free list when one with
// sufficient capacity is available. Pair every call with
// releaseCallArgsBuffer; the slice must not escape the call boundary.
func (vm *VM) takeCallArgsBuffer(size int) []runtime.Value {
	for i := len(vm.callArgsFree) - 1; i >= 0; i-- {
		buf := vm.callArgsFree[i]
		if cap(buf) < size {
			continue
		}
		vm.callArgsFree[i] = vm.callArgsFree[len(vm.callArgsFree)-1]
		vm.callArgsFree = vm.callArgsFree[:len(vm.callArgsFree)-1]
		return buf[:size]
	}
	return make([]runtime.Value, size)
}

func (vm *VM) releaseCallArgsBuffer(buf []runtime.Value) {
	if cap(buf) == 0 {
		return
	}
	clear(buf)
	vm.callArgsFree = append(vm.callArgsFree, buf[:0])
}

func (vm *VM) pushLocalsStackFrame(frame *callFrame, localCount int, shares bool) {
	vm.pushLocalsStackFrameSkip(frame, localCount, shares, 0)
}

// pushLocalsStackFrameSkip is pushLocalsStackFrame with the first
// skipLeading slots left unzeroed; the caller must overwrite them
// before any opcode runs (the fast call path's param copy).
func (vm *VM) pushLocalsStackFrameSkip(frame *callFrame, localCount int, shares bool, skipLeading int) {
	if shares {
		frame.basePointer = vm.currentFrameBP
	} else {
		frame.basePointer = len(vm.localsStack)
	}
	frame.localCount = localCount
	vm.currentFrameBP = frame.basePointer
	vm.ensureLocalsStackFrom(frame.basePointer+localCount, frame.basePointer+skipLeading)
}

func (vm *VM) ensureLocalsStack(end int) {
	vm.ensureLocalsStackFrom(end, 0)
}

func (vm *VM) ensureLocalsStackFrom(end, clearFrom int) {
	cur := len(vm.localsStack)
	if end <= cur {
		return
	}
	if end > cap(vm.localsStack) {
		newCap := cap(vm.localsStack) * 2
		if newCap < end {
			newCap = end
		}
		grown := make([]runtime.VMValue, end, newCap)
		copy(grown, vm.localsStack)
		vm.localsStack = grown
		return
	}
	vm.localsStack = vm.localsStack[:end]
	if clearFrom < cur {
		clearFrom = cur
	}
	if clearFrom < end {
		clear(vm.localsStack[clearFrom:end])
	}
}

func (vm *VM) popLocalsStackFrame(frame *callFrame) {
	if !frame.shared && frame.basePointer < len(vm.localsStack) {
		vm.localsStack = vm.localsStack[:frame.basePointer]
	}
	if len(vm.frames) > 1 {
		vm.currentFrameBP = vm.frames[len(vm.frames)-2].basePointer
	} else {
		vm.currentFrameBP = 0
	}
}

func (vm *VM) SetModuleName(name string) {
	vm.moduleName = name
}

func (vm *VM) SetModulePaths(paths []string) {
	vm.modulePaths = append([]string(nil), paths...)
}

func (vm *VM) SetStatefulNativeCaller(caller StatefulNativeCaller) {
	vm.statefulNative = caller
}

func (vm *VM) SetMaxCallDepth(limit int) {
	vm.maxCallDepth = limit
}
