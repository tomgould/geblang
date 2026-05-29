package bytecode

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"geblang/internal/ast"
	"geblang/internal/native"
	"geblang/internal/runtime"
)

type VM struct {
	chunk   Chunk
	stdout  io.Writer
	stack   []runtime.VMValue
	globals []runtime.VMValue
	// globalsMu guards bulk operations on the globals slice used by the
	// wrap-bridge: setGlobal writes and the snapshot-in / per-slot
	// write-back done when a wrapped callable fires on a different
	// goroutine than the parent. Per-instruction OpGetGlobal stays
	// lock-free; the bridge boundary is the only race surface the
	// detector flags.
	globalsMu sync.Mutex
	// dirtyGlobals tracks which global slots a sub-VM has written so the
	// wrap-bridge write-back can copy only the touched slots back to the
	// parent rather than slicecopying the entire backing array (which
	// races the parent's lock-free OpGetGlobal on unrelated slots).
	dirtyGlobals []bool
	// bridgeActive is set once a wrap-bridge worker is, or could become,
	// alive against this VM's globals. While false, setGlobalVM /
	// setGlobal can write without taking globalsMu because no concurrent
	// reader/writer of vm.globals exists. Set monotonically: once true,
	// stays true for the VM's lifetime. The single-goroutine hot path
	// (numeric_loop) never bridges, so it stays lock-free.
	bridgeActive      atomic.Bool
	localsStack       []runtime.VMValue
	currentFrameBP    int
	frames            []callFrame
	maxCallDepth      int
	defers            [][]deferredAction
	exceptionHandlers []exceptionHandler
	pendingThrow      *runtime.Error
	moduleLoader      ModuleLoader
	moduleName        string
	modulePaths       []string
	statefulNative    StatefulNativeCaller
	classIndex        map[string]int
	natives           *native.Registry
	// nil entries fall through to evalNativeCall (statefuls / errors.is).
	nativeCache         []native.Function
	syncMode            bool // when true, async functions are called synchronously
	decoratedFuncs      map[int64]runtime.Value
	decoratedClasses    map[int64]runtime.Value
	decoratorsApplied   bool
	applyingDecorators  bool
	rawFunctionCalls    map[int64]bool
	methodReceiverFuncs map[int64]bool
	interfaceFallbacks   map[int64]map[string]crossModuleDefault
	interfaceExtraFields map[int64][]extraField
	forwardThis         *runtime.Instance
	generatorExecution  bool
	generatorYield      chan vmGeneratorItem
	generatorDone       <-chan struct{}
	typeSpecCache       map[string]vmTypeSpec
	typeAssertSpecs     map[int64]vmTypeSpec
	callArgsFree        [][]runtime.Value
	// pendingTypeBindings carries an inherited type-binding map from a
	// closure callsite into the next startFunctionWithValidation call.
	// startFunctionWithBindings sets it; startFunctionWithValidation
	// reads it during arg validation and again when planting the
	// frame's type bindings, then leaves it for the caller to clear.
	pendingTypeBindings map[string]string
	// destructibleInstances tracks instances of classes that declare
	// `func ~ClassName()`. The sweep in Cleanup() invokes their
	// destructors in reverse-creation order. `del x` removes the
	// corresponding entry and fires the destructor immediately.
	destructibleInstances []*runtime.Instance
	// nameLowerCache memoises strings.ToLower(name) by chunk-constant
	// index. Method names recur every call in a tight loop and the
	// dispatch path needs the lowercased form to look up entries in
	// the ClassInfo.Methods map (which the compiler stores
	// case-insensitively). Hit on a 500k-call benchmark: ~6% wall.
	nameLowerCache []string
	// classInfoNameCache memoises classInfo by the runtime
	// instance.Class.Name. The map lookup itself is small, but the
	// `strings.ToLower(name)` inside vm.classInfo runs once per call
	// in the dispatch loop - cache the resolved ClassInfo so the
	// hot path is a single direct read.
	classInfoNameCache map[string]ClassInfo
	// methodLookupCache is a single-slot cache for the most-recent
	// `lookupMethodLower` result. A tight loop calling one method on
	// one class - the class_dispatch benchmark's
	// `Counter.step` x 50k - hits this on every call after the first
	// and skips the `classInfo.Methods` map access entirely. The
	// cache uses pointer-equality on the indices slice to detect
	// hits without copying.
	methodLookupClass    string
	methodLookupName     string
	methodLookupIndices  []int64
	methodLookupValid    bool
	fieldLookupClass     *runtime.Class
	fieldLookupName      string
	fieldLookupOnClass   bool
	fieldLookupHasGetMag bool
	fieldLookupHasSetMag bool
	fieldLookupValid     bool
	// Re-entry knobs used by callBytecodeInline to host a nested
	// function invocation on the same VM without rebuilding the chunk.
	runEntryIP         int
	runInlineExitDepth int // -1 = top-level (exit on OpReturn with frames==0); >=0 = inline (exit when frames drops to this)
	runSuppressCleanup bool
	// Tracks whether Run() is currently executing the dispatch loop on
	// this goroutine. callBytecodeInline relies on this being true so
	// that native callbacks fired from a different goroutine (e.g. a
	// stateful native module's async hook) take the runWrapper path
	// instead of mutating shared state.
	inDispatchLoop bool
	// False when chunk has no decorators/async/generators; lets
	// vm.call skip the three per-call probes for those features.
	requiresCallSitePolymorphism bool
}

type ModuleLoader interface {
	LoadModule(canonical string, alias string) (*runtime.Module, error)
	CallModuleFunction(function runtime.BytecodeFunction, args []runtime.Value) (runtime.Value, error)
	CallModuleClosure(closure runtime.BytecodeClosure, args []runtime.Value) (runtime.Value, error)
	ConstructModuleClass(class runtime.BytecodeClass, args []runtime.Value) (runtime.Value, error)
	CallModuleStaticMethod(class runtime.BytecodeClass, methodName string, args []runtime.Value) (runtime.Value, error)
	CallModuleMethod(module string, className string, methodName string, instance *runtime.Instance, args []runtime.Value) (runtime.Value, error)
	// CallParentInModule invokes the parent class's constructor or
	// method on the supplied instance inside the parent module's chunk.
	// className is looked up in the target chunk regardless of
	// instance.Class.Name, which is necessary because instance is a
	// subclass declared in another chunk.
	CallParentInModule(module string, className string, methodName string, instance *runtime.Instance, args []runtime.Value) (runtime.Value, error)
	// FindClassByName looks up a class by its bare (unqualified) name
	// across every loaded module. Returns nil when nothing matches.
	// Used by reflect.class so framework code can resolve a user
	// class regardless of which module declared it.
	FindClassByName(name string) (runtime.Value, bool)
	// FindFunctionByName looks up a function by its bare name
	// across every loaded module. Returns nil when nothing matches.
	FindFunctionByName(name string) (runtime.Value, bool)
	// DeserializeModuleClass runs json.parseAs / xml.parseAs style
	// deserialization for a class declared in another chunk. The
	// loader resolves the right chunk by class.Module and dispatches
	// to a VM bound to it. Used when a stdlib module receives a
	// class value originating from the user's main program.
	DeserializeModuleClass(class runtime.BytecodeClass, value runtime.Value) (runtime.Value, error)
	// ConstructorsForModuleClass returns `reflect.constructors(class)`
	// for a class declared in another chunk. The loader dispatches to
	// a VM bound to the right chunk so the metadata reflects the
	// originating class's actual constructor list rather than the
	// caller chunk's stale view.
	ConstructorsForModuleClass(class runtime.BytecodeClass) (runtime.Value, error)
	FieldsForModuleClass(class runtime.BytecodeClass) (runtime.Value, error)
	LookupModuleInterface(module, name string) (InterfaceInfo, bool)
	ListAllClasses() []runtime.Value
}

type StatefulNativeCaller interface {
	CallBuiltin(module, name string, args []runtime.Value, argNames []string) (runtime.Value, error)
}

type directPrintStatefulNative interface {
	HandleDirectPrint() bool
}

type FunctionDecoratorState struct {
	decorated           map[int64]runtime.Value
	decoratedClasses    map[int64]runtime.Value
	applied             bool
	methodReceiverFuncs map[int64]bool
}

const DefaultMaxCallDepth = 10000

type callFrame struct {
	returnIP         int
	basePointer      int
	localCount       int
	returnOverride   runtime.Value
	typeBindings     map[string]string
	generator        chan vmGeneratorItem
	generatorDone    <-chan struct{}
	functionName     string
	callLine         int
	negateReturn     bool
	isErrorClass     bool
	isImmutableClass bool
	// isDestructibleConstructor is true when this frame is a class
	// constructor for a class that declares a `~ClassName()`
	// destructor. OpReturn registers the constructed instance in
	// vm.destructibleInstances so the exit-time sweep finds it.
	isDestructibleConstructor bool
	// shared is true when this frame inherits its parent's locals slice
	// (a nested function statement that references the outer function's
	// locals by absolute slot number). When false, the frame owns a
	// fresh locals slice that is recycled on return.
	shared bool
}

type vmGeneratorItem struct {
	value runtime.Value
	err   error
}

type deferredCallKind int8

const (
	deferKindPrint deferredCallKind = iota
	deferKindPrintln
	deferKindNative
	deferKindFunc
	deferKindMethod
	deferKindCallable
)

type deferredAction struct {
	kind     deferredCallKind
	value    runtime.Value   // for print/println
	name     string          // for native: "module.func"; for method: method name
	funcIdx  int64           // for func
	receiver runtime.Value   // for method
	args     []runtime.Value // for native/func/method
	// names parallels args when the deferred call was emitted by an
	// OpDefer*Named opcode. Empty entries mark positional args;
	// non-empty entries name the corresponding argument so the
	// deferred-dispatch path can reorder against the callee's
	// signature when the queue runs. nil when the defer used the
	// positional-only opcode.
	names []string
}

type exceptionHandler struct {
	handlerIP        int
	frameDepth       int
	stackDepth       int
	localsStackDepth int
	snapshot         []runtime.VMValue
	snapshotBase     int
}

type iteratorValue struct {
	values    []runtime.Value
	index     int
	rangeIter *rangeIterator
	generator *runtime.Generator
	// userIter holds a user-defined iterator instance returned by
	// the source's __iter() method (or the source itself when it
	// already implements __next/__done). next() calls
	// userIterDone()/userIterNext() per step.
	userIter *runtime.Instance
}

func (v *iteratorValue) TypeName() string { return "iterator" }
func (v *iteratorValue) Inspect() string  { return "<iterator>" }

type rangeIterator struct {
	current   *big.Int
	end       *big.Int
	step      *big.Int
	exclusive bool
}

type ExitError struct {
	Code int
}

func (e ExitError) Error() string {
	return fmt.Sprintf("bytecode exited with %d", e.Code)
}

type recoverableNativeError struct {
	err error
}

func (e recoverableNativeError) Error() string {
	return e.err.Error()
}

func (e recoverableNativeError) Unwrap() error {
	return e.err
}

// vmTypedError is a catchable typed exception from a primitive or native method.
type vmTypedError struct {
	class   string
	message string
}

func (e vmTypedError) Error() string { return e.class + ": " + e.message }

// vmThrownError carries a runtime.Error across a VM boundary so the
// caller VM can re-throw it as a catchable pendingThrow instead of
// losing the class / parent-chain information by collapsing it to a
// plain string. Used when a closure called via the moduleLoader
// throws — the sub-VM packages its pendingThrow into a vmThrownError
// and the caller VM's invocation site unwraps it.
type vmThrownError struct {
	err runtime.Error
}

func (e vmThrownError) Error() string { return "uncaught " + e.err.Inspect() }

type vmTypeSpec struct {
	raw       string
	base      string
	baseLower string
	nullable  bool
	kind      vmTypeKind
	args      []vmTypeSpec
}

type vmTypeKind int8

const (
	vmTypeOther vmTypeKind = iota
	vmTypeAny
	vmTypeInt
	vmTypeString
	vmTypeBool
	vmTypeFloat
	vmTypeDecimal
	vmTypeList
	vmTypeSet
	vmTypeDict
	vmTypeCallable
	vmTypeGenerator
	// vmTypeUnion holds a `T | U | ...` shape: spec.args carries the
	// branches; a value matches when any branch matches.
	vmTypeUnion
	// vmTypeIntersection holds a `T & U & ...` shape: spec.args carries
	// the branches; a value matches only when every branch matches.
	vmTypeIntersection
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
			entry, hit := dict.Entries[native.DictKey(key)]
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
		entry, ok := dict.Entries[native.DictKey(key)]
		if !ok {
			return nil, fmt.Errorf("deserialize %s: missing field %q", class.Name, paramName)
		}
		args = append(args, entry.Value)
	}
	return vm.ConstructClass(class.Index, args)
}

// invokeInstanceMethod implements native.InstanceInvoker for the
// bytecode VM. Returns (result, true, nil) when the method exists
// and was called, (nil, false, nil) when the class has no such
// method, (nil, false, err) on call error.
func (vm *VM) invokeInstanceMethod(instance *runtime.Instance, method string, args []runtime.Value) (runtime.Value, bool, error) {
	if instance == nil || instance.Class == nil {
		return nil, false, nil
	}
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		return nil, false, nil
	}
	indices, ok := vm.lookupMethod(classInfo, method)
	if !ok || len(indices) == 0 {
		return nil, false, nil
	}
	result, err := vm.CallMethod(instance, method, args)
	if err != nil {
		return nil, false, err
	}
	return result, true, nil
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
				fmt.Fprintf(vm.stdout, "destructor for %s: %v\n", inst.Class.Name, err)
			}
		}
	}
	return nil
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
	vm.globals = make([]runtime.VMValue, len(globals))
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
	if shares {
		frame.basePointer = vm.currentFrameBP
	} else {
		frame.basePointer = len(vm.localsStack)
	}
	frame.localCount = localCount
	vm.currentFrameBP = frame.basePointer
	vm.ensureLocalsStack(frame.basePointer + localCount)
}

func (vm *VM) ensureLocalsStack(end int) {
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
	clear(vm.localsStack[cur:end])
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

func (vm *VM) Run() (err error) {
	if !vm.runSuppressCleanup {
		// Mirror the evaluator's behaviour: after a script finishes
		// (success, exit, or runtime error) drain the destructor
		// registry so end-of-lifetime cleanup is deterministic.
		defer func() {
			cleanupErr := vm.Cleanup()
			if err == nil && cleanupErr != nil {
				err = cleanupErr
			}
		}()
		if err := vm.ensureCallableDecorators(); err != nil {
			return err
		}
	}
	inlineExitDepth := vm.runInlineExitDepth
	oldInDispatch := vm.inDispatchLoop
	vm.inDispatchLoop = true
	defer func() { vm.inDispatchLoop = oldInDispatch }()
	// Hoist instructions slice into a local so each iteration reads from
	// a stable register-resident pointer instead of dereferencing
	// vm.chunk.Instructions through the VM struct on every fetch.
	instructions := vm.chunk.Instructions
	for ip := vm.runEntryIP; ip < len(instructions); ip++ {
		instruction := instructions[ip]
		switch instruction.Op {
		case OpNoop:
		case OpConstant:
			index := instruction.Operands[0]
			if index < 0 || int(index) >= len(vm.chunk.Constants) {
				return vm.runtimeError(instruction, "constant index out of range")
			}
			value := vm.chunk.Constants[index]
			switch x := value.(type) {
			case runtime.SmallInt:
				vm.pushVM(runtime.VMValueSmallInt(x.Value))
			case runtime.Bool:
				vm.pushVM(runtime.VMValueBool(x.Value))
			case runtime.Null:
				vm.pushVM(runtime.VMValueNull)
			case runtime.BytecodeClass:
				if dec, exists := vm.decoratedClasses[x.Index]; exists {
					vm.push(dec)
				} else {
					vm.push(value)
				}
			default:
				vm.push(value)
			}
		case OpAdd:
			nextIP, err := vm.add(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpAppendStringConst, OpAppendGlobalStringConst:
			slot := instruction.Operands[0]
			constIdx := instruction.Operands[1]
			if constIdx < 0 || int(constIdx) >= len(vm.chunk.Constants) {
				return vm.runtimeError(instruction, "constant index out of range")
			}
			litVal, ok := vm.chunk.Constants[constIdx].(runtime.String)
			if !ok {
				return vm.runtimeError(instruction, "literal must be string")
			}
			var target []runtime.VMValue
			var offset int
			if instruction.Op == OpAppendStringConst {
				target = vm.localsStack
				offset = vm.currentFrameBP
			} else {
				target = vm.globals
			}
			idx := int(slot) + offset
			if idx >= len(target) {
				return vm.runtimeError(instruction, "slot out of range")
			}
			cur := target[idx]
			if cur.Kind == runtime.VMKindBoxed {
				if acc, ok := cur.Boxed.(*runtime.StringAccumulator); ok {
					acc.B.WriteString(litVal.Value)
					materialised := runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: acc.Materialize()}
					target[idx] = materialised
					vm.pushVM(materialised)
					continue
				}
				if l, lok := cur.Boxed.(runtime.String); lok {
					next := runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: runtime.String{Value: l.Value + litVal.Value}}
					target[idx] = next
					vm.pushVM(next)
					continue
				}
			}
			vm.pushVM(cur)
			vm.pushVM(runtime.VMValueFromValue(litVal))
			nextIP, err := vm.add(instruction, ip)
			if err != nil {
				return err
			}
			result, perr := vm.popVM()
			if perr != nil {
				return vm.runtimeError(instruction, "%s", perr.Error())
			}
			target[idx] = result
			vm.pushVM(result)
			ip = nextIP
		case OpAppendStringConstStmt, OpAppendGlobalStringConstStmt:
			slot := instruction.Operands[0]
			constIdx := instruction.Operands[1]
			if constIdx < 0 || int(constIdx) >= len(vm.chunk.Constants) {
				return vm.runtimeError(instruction, "constant index out of range")
			}
			litVal, ok := vm.chunk.Constants[constIdx].(runtime.String)
			if !ok {
				return vm.runtimeError(instruction, "literal must be string")
			}
			var target []runtime.VMValue
			var offset int
			if instruction.Op == OpAppendStringConstStmt {
				target = vm.localsStack
				offset = vm.currentFrameBP
			} else {
				target = vm.globals
			}
			idx := int(slot) + offset
			if idx >= len(target) {
				return vm.runtimeError(instruction, "slot out of range")
			}
			cur := target[idx]
			if cur.Kind == runtime.VMKindBoxed {
				if acc, ok := cur.Boxed.(*runtime.StringAccumulator); ok {
					acc.B.WriteString(litVal.Value)
					continue
				}
				if l, lok := cur.Boxed.(runtime.String); lok {
					if l.Value == "" {
						target[idx] = runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: runtime.String{Value: litVal.Value}}
						continue
					}
					sb := &strings.Builder{}
					sb.Grow(len(l.Value) + len(litVal.Value) + 256)
					sb.WriteString(l.Value)
					sb.WriteString(litVal.Value)
					target[idx] = runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: &runtime.StringAccumulator{B: sb}}
					continue
				}
			}
			vm.pushVM(cur)
			vm.pushVM(runtime.VMValueFromValue(litVal))
			nextIP, err := vm.add(instruction, ip)
			if err != nil {
				return err
			}
			result, perr := vm.popVM()
			if perr != nil {
				return vm.runtimeError(instruction, "%s", perr.Error())
			}
			target[idx] = result
			ip = nextIP
		case OpAddStringConst:
			n := len(vm.stack)
			if n < 1 {
				return vm.runtimeError(instruction, "stack underflow")
			}
			constIdx := instruction.Operands[0]
			if constIdx < 0 || int(constIdx) >= len(vm.chunk.Constants) {
				return vm.runtimeError(instruction, "constant index out of range")
			}
			litVal, ok := vm.chunk.Constants[constIdx].(runtime.String)
			if !ok {
				return vm.runtimeError(instruction, "OpAddStringConst literal must be string")
			}
			leftVM := vm.stack[n-1]
			if leftVM.Kind == runtime.VMKindBoxed {
				if l, lok := leftVM.Boxed.(runtime.String); lok {
					vm.stack[n-1] = runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: runtime.String{Value: l.Value + litVal.Value}}
					continue
				}
			}
			// Defer to generic OpAdd if the static-type proof missed.
			vm.pushVM(runtime.VMValueFromValue(litVal))
			nextIP, err := vm.add(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpAddString:
			n := len(vm.stack)
			if n < 2 {
				return vm.runtimeError(instruction, "stack underflow")
			}
			leftVM := vm.stack[n-2]
			rightVM := vm.stack[n-1]
			if leftVM.Kind == runtime.VMKindBoxed && rightVM.Kind == runtime.VMKindBoxed {
				if l, lok := leftVM.Boxed.(runtime.String); lok {
					if r, rok := rightVM.Boxed.(runtime.String); rok {
						vm.stack[n-2] = runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: runtime.String{Value: l.Value + r.Value}}
						vm.stack = vm.stack[:n-1]
						continue
					}
				}
			}
			// Untyped local can carry a non-string at runtime; defer to vm.add.
			nextIP, err := vm.add(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpSub, OpMul, OpDiv, OpIntDiv, OpMod, OpPow:
			nextIP, err := vm.binaryNumeric(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpAddInt, OpSubInt, OpMulInt, OpModInt:
			n := len(vm.stack)
			if n < 2 {
				return vm.runtimeError(instruction, "stack underflow")
			}
			leftVM := vm.stack[n-2]
			rightVM := vm.stack[n-1]
			if leftVM.Kind == runtime.VMKindSmallInt && rightVM.Kind == runtime.VMKindSmallInt {
				li := leftVM.I64
				ri := rightVM.I64
				var result runtime.VMValue
				switch instruction.Op {
				case OpAddInt:
					v := li + ri
					if (li^v)&(ri^v) < 0 {
						result = runtime.VMValueFromValue(runtime.Int{Value: new(big.Int).Add(big.NewInt(li), big.NewInt(ri))})
					} else {
						result = runtime.VMValueSmallInt(v)
					}
				case OpSubInt:
					v := li - ri
					if (li^ri)&(li^v) < 0 {
						result = runtime.VMValueFromValue(runtime.Int{Value: new(big.Int).Sub(big.NewInt(li), big.NewInt(ri))})
					} else {
						result = runtime.VMValueSmallInt(v)
					}
				case OpMulInt:
					v := li * ri
					if li != 0 && v/li != ri {
						result = runtime.VMValueFromValue(runtime.Int{Value: new(big.Int).Mul(big.NewInt(li), big.NewInt(ri))})
					} else {
						result = runtime.VMValueSmallInt(v)
					}
				case OpModInt:
					if ri == 0 {
						return vm.runtimeError(instruction, "integer division by zero")
					}
					v := li % ri
					if v != 0 && (li < 0) != (ri < 0) {
						v += ri
					}
					result = runtime.VMValueSmallInt(v)
				}
				vm.stack[n-2] = result
				vm.stack = vm.stack[:n-1]
				continue
			}
			// Fallback: materialise both operands as runtime.Value, drop
			// them from the stack, and route through generic arithmetic.
			left := leftVM.ToValue()
			right := rightVM.ToValue()
			vm.stack = vm.stack[:n-2]
			genericOp := instruction
			switch instruction.Op {
			case OpAddInt:
				genericOp.Op = OpAdd
			case OpSubInt:
				genericOp.Op = OpSub
			case OpMulInt:
				genericOp.Op = OpMul
			case OpModInt:
				genericOp.Op = OpMod
			}
			if nextIP, handled, err := vm.callBinaryOperatorMethod(genericOp, ip, left, right); handled || err != nil {
				if err != nil {
					return err
				}
				ip = nextIP
			} else if err := vm.binaryNumericValues(genericOp, left, right); err != nil {
				return err
			}
			continue
		case OpLessInt, OpGreaterInt, OpLessEqualInt, OpGreaterEqualInt, OpEqualInt:
			n := len(vm.stack)
			if n < 2 {
				return vm.runtimeError(instruction, "stack underflow")
			}
			leftVM := vm.stack[n-2]
			rightVM := vm.stack[n-1]
			if leftVM.Kind == runtime.VMKindSmallInt && rightVM.Kind == runtime.VMKindSmallInt {
				li := leftVM.I64
				ri := rightVM.I64
				var result bool
				switch instruction.Op {
				case OpLessInt:
					result = li < ri
				case OpGreaterInt:
					result = li > ri
				case OpLessEqualInt:
					result = li <= ri
				case OpGreaterEqualInt:
					result = li >= ri
				case OpEqualInt:
					result = li == ri
				}
				vm.stack[n-2] = runtime.VMValueBool(result)
				vm.stack = vm.stack[:n-1]
				continue
			}
			// Fallback for non-SmallInt int values (runtime.Int after overflow):
			// vm.equal/vm.compare pop from stack themselves and re-push the
			// bool result, so leave both operands on the stack here.
			genericOp := instruction
			switch instruction.Op {
			case OpLessInt:
				genericOp.Op = OpLess
			case OpGreaterInt:
				genericOp.Op = OpGreater
			case OpLessEqualInt:
				genericOp.Op = OpLessEqual
			case OpGreaterEqualInt:
				genericOp.Op = OpGreaterEqual
			case OpEqualInt:
				genericOp.Op = OpEqual
			}
			if instruction.Op == OpEqualInt {
				nextIP, err := vm.equal(genericOp, ip)
				if err != nil {
					return err
				}
				ip = nextIP
			} else {
				nextIP, err := vm.compare(genericOp, ip)
				if err != nil {
					return err
				}
				ip = nextIP
			}
			continue
		case OpIncLocalInt, OpDecLocalInt:
			slot := instruction.Operands[0]
			idx := vm.currentFrameBP + int(slot)
			if idx < len(vm.localsStack) {
				cur := vm.localsStack[idx]
				if cur.Kind == runtime.VMKindSmallInt {
					delta := int64(1)
					if instruction.Op == OpDecLocalInt {
						delta = -1
					}
					nv := cur.I64 + delta
					if (cur.I64^nv)&(delta^nv) >= 0 {
						vm.localsStack[idx] = runtime.VMValueSmallInt(nv)
						vm.pushVM(cur)
						continue
					}
				}
			}
			if err := vm.updateIntSlot(instruction); err != nil {
				return err
			}
		case OpIncGlobalInt, OpDecGlobalInt:
			slot := instruction.Operands[0]
			if slot >= 0 && int(slot) < len(vm.globals) && !vm.bridgeActive.Load() {
				cur := vm.globals[slot]
				if cur.Kind == runtime.VMKindSmallInt {
					delta := int64(1)
					if instruction.Op == OpDecGlobalInt {
						delta = -1
					}
					nv := cur.I64 + delta
					if (cur.I64^nv)&(delta^nv) >= 0 {
						vm.globals[slot] = runtime.VMValueSmallInt(nv)
						vm.pushVM(cur)
						continue
					}
				}
			}
			if err := vm.updateIntSlot(instruction); err != nil {
				return err
			}
		case OpAppendLocalList, OpAppendGlobalList:
			if err := vm.appendListSlot(instruction); err != nil {
				return err
			}
		case OpJumpIfNotLessInt, OpJumpIfNotLessEqualInt, OpJumpIfNotGreaterInt,
			OpJumpIfNotGreaterEqualInt, OpJumpIfNotEqualInt, OpJumpIfEqualInt:
			n := len(vm.stack)
			if n < 2 {
				return vm.runtimeError(instruction, "stack underflow")
			}
			lvm := vm.stack[n-2]
			rvm := vm.stack[n-1]
			if lvm.Kind == runtime.VMKindSmallInt && rvm.Kind == runtime.VMKindSmallInt {
				li := lvm.I64
				ri := rvm.I64
				vm.stack = vm.stack[:n-2]
				var jump bool
				switch instruction.Op {
				case OpJumpIfNotLessInt:
					jump = !(li < ri)
				case OpJumpIfNotLessEqualInt:
					jump = !(li <= ri)
				case OpJumpIfNotGreaterInt:
					jump = !(li > ri)
				case OpJumpIfNotGreaterEqualInt:
					jump = !(li >= ri)
				case OpJumpIfNotEqualInt:
					jump = !(li == ri)
				case OpJumpIfEqualInt:
					jump = li == ri
				}
				if jump {
					ip = int(instruction.Operands[0]) - 1
				}
				continue
			}
			nextIP, err := vm.compareJumpIntFallback(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpAddLocalIntLocal, OpSubLocalIntLocal, OpAddLocalIntConst, OpSubLocalIntConst,
			OpAddLocalIntGlobal, OpSubLocalIntGlobal,
			OpAddGlobalIntGlobal, OpSubGlobalIntGlobal, OpAddGlobalIntConst, OpSubGlobalIntConst,
			OpAddGlobalIntLocal, OpSubGlobalIntLocal:
			dst := instruction.Operands[0]
			rhsOp := instruction.Operands[1]
			dstIsGlobal := false
			var lv runtime.VMValue
			var lerr error
			switch instruction.Op {
			case OpAddGlobalIntGlobal, OpSubGlobalIntGlobal, OpAddGlobalIntConst, OpSubGlobalIntConst,
				OpAddGlobalIntLocal, OpSubGlobalIntLocal:
				lv, lerr = vm.getGlobalVM(dst)
				dstIsGlobal = true
			default:
				lv, lerr = vm.getLocalVM(dst)
			}
			if lerr != nil {
				return vm.runtimeError(instruction, "%s", lerr.Error())
			}
			var rv runtime.VMValue
			var rerr error
			isConst := false
			switch instruction.Op {
			case OpAddLocalIntConst, OpSubLocalIntConst, OpAddGlobalIntConst, OpSubGlobalIntConst:
				isConst = true
			case OpAddLocalIntLocal, OpSubLocalIntLocal, OpAddGlobalIntLocal, OpSubGlobalIntLocal:
				rv, rerr = vm.getLocalVM(rhsOp)
			case OpAddLocalIntGlobal, OpSubLocalIntGlobal, OpAddGlobalIntGlobal, OpSubGlobalIntGlobal:
				rv, rerr = vm.getGlobalVM(rhsOp)
			}
			if rerr != nil {
				return vm.runtimeError(instruction, "%s", rerr.Error())
			}
			subtract := false
			switch instruction.Op {
			case OpSubLocalIntLocal, OpSubLocalIntConst, OpSubGlobalIntGlobal, OpSubGlobalIntConst,
				OpSubLocalIntGlobal, OpSubGlobalIntLocal:
				subtract = true
			}
			if lv.Kind == runtime.VMKindSmallInt {
				var bv int64
				bOK := true
				if isConst {
					bv = rhsOp
				} else if rv.Kind == runtime.VMKindSmallInt {
					bv = rv.I64
				} else {
					bOK = false
				}
				if bOK {
					var v int64
					var overflow bool
					if subtract {
						v = lv.I64 - bv
						overflow = (lv.I64^bv)&(lv.I64^v) < 0
					} else {
						v = lv.I64 + bv
						overflow = (lv.I64^v)&(bv^v) < 0
					}
					var result runtime.VMValue
					if overflow {
						res := new(big.Int)
						if subtract {
							res.Sub(big.NewInt(lv.I64), big.NewInt(bv))
						} else {
							res.Add(big.NewInt(lv.I64), big.NewInt(bv))
						}
						result = runtime.VMValueFromValue(runtime.Int{Value: res})
					} else {
						result = runtime.VMValueSmallInt(v)
					}
					var storeErr error
					if dstIsGlobal {
						storeErr = vm.setGlobalVM(dst, result)
					} else {
						storeErr = vm.setLocalVM(dst, result)
					}
					if storeErr != nil {
						return vm.runtimeError(instruction, "%s", storeErr.Error())
					}
					vm.pushVM(result)
					continue
				}
			}
			if err := vm.intSelfArith(instruction); err != nil {
				return err
			}
		case OpEqual:
			nextIP, err := vm.equal(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpLess, OpGreater, OpLessEqual, OpGreaterEqual:
			nextIP, err := vm.compare(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpNot:
			nextIP, err := vm.not(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpBoolXor:
			if err := vm.boolXor(instruction); err != nil {
				return err
			}
		case OpNegate:
			nextIP, err := vm.negate(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpDefineGlobal:
			slot := instruction.Operands[0]
			value, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if err := vm.setGlobal(slot, value); err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
		case OpGetGlobal:
			slot := instruction.Operands[0]
			if int(slot) < len(vm.globals) {
				value := vm.globals[slot]
				if value.Kind != runtime.VMKindBoxed && value.Kind != runtime.VMKindUnset {
					vm.pushVM(value)
					continue
				}
			}
			value, err := vm.getGlobalVM(slot)
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.pushVM(value)
		case OpSetGlobal:
			slot := instruction.Operands[0]
			value, err := vm.popVM()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if int(slot) < len(vm.globals) && !vm.bridgeActive.Load() {
				cur := vm.globals[slot]
				if cur.Kind != runtime.VMKindBoxed {
					vm.globals[slot] = value
					vm.pushVM(value)
					continue
				}
			}
			if err := vm.setGlobalVM(slot, value); err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.pushVM(value)
		case OpDefineLocal:
			slot := instruction.Operands[0]
			value, err := vm.popVM()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if err := vm.setLocalVM(slot, value); err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
		case OpGetLocal:
			slot := instruction.Operands[0]
			idx := vm.currentFrameBP + int(slot)
			if idx < len(vm.localsStack) {
				value := vm.localsStack[idx]
				if value.Kind != runtime.VMKindBoxed && value.Kind != runtime.VMKindUnset {
					vm.pushVM(value)
					continue
				}
			}
			value, err := vm.getLocalVM(slot)
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.pushVM(value)
		case OpSetLocal:
			slot := instruction.Operands[0]
			value, err := vm.popVM()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			idx := vm.currentFrameBP + int(slot)
			if idx < len(vm.localsStack) {
				cur := vm.localsStack[idx]
				if cur.Kind != runtime.VMKindBoxed {
					vm.localsStack[idx] = value
					vm.pushVM(value)
					continue
				}
			}
			if err := vm.setLocalVM(slot, value); err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.pushVM(value)
		case OpBuildList:
			count := instruction.Operands[0]
			if count < 0 {
				return vm.runtimeError(instruction, "list element count out of range")
			}
			elements := make([]runtime.Value, int(count))
			for i := int(count) - 1; i >= 0; i-- {
				value, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				elements[i] = value
			}
			vm.push(runtime.List{Elements: elements})
		case OpBuildDict:
			count := instruction.Operands[0]
			if count < 0 {
				return vm.runtimeError(instruction, "dict entry count out of range")
			}
			entries := make(map[string]runtime.DictEntry, int(count))
			for i := int64(0); i < count; i++ {
				value, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				key, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				entries[native.DictKey(key)] = runtime.DictEntry{Key: key, Value: value}
			}
			vm.push(runtime.Dict{Entries: entries})
		case OpBuildSet:
			count := instruction.Operands[0]
			if count < 0 {
				return vm.runtimeError(instruction, "set element count out of range")
			}
			elements := make(map[string]runtime.SetEntry, int(count))
			for i := int64(0); i < count; i++ {
				value, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				elements[native.DictKey(value)] = runtime.SetEntry{Value: value}
			}
			vm.push(runtime.Set{Elements: elements})
		case OpIndex:
			if err := vm.index(instruction); err != nil {
				return err
			}
		case OpSetIndex:
			nextIP, err := vm.setIndex(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpSlice:
			if err := vm.slice(instruction); err != nil {
				return err
			}
		case OpIterInit:
			if err := vm.iterInit(instruction); err != nil {
				return err
			}
		case OpIterNext:
			hasNext, err := vm.iterNext(instruction)
			if err != nil {
				return err
			}
			if !hasNext {
				ip = int(instruction.Operands[0]) - 1
			}
		case OpIterClose:
			if err := vm.iterClose(instruction); err != nil {
				return err
			}
		case OpTypeAssert:
			if err := vm.typeAssert(instruction); err != nil {
				return err
			}
		case OpShallowFreeze:
			value, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.push(runtime.ShallowFreeze(value))
		case OpMatchListShape:
			if len(instruction.Operands) != 1 {
				return vm.runtimeError(instruction, "match-list-shape instruction has invalid operands")
			}
			expected := instruction.Operands[0]
			vmv, err := vm.popVM()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			ok := false
			if vmv.Kind == runtime.VMKindBoxed {
				if list, isList := vmv.Boxed.(runtime.List); isList && int64(len(list.Elements)) == expected {
					ok = true
				}
			}
			vm.pushVM(runtime.VMValueBool(ok))
		case OpUnpackList:
			if err := vm.unpackList(instruction); err != nil {
				return err
			}
		case OpBuildRange:
			if err := vm.buildRange(instruction); err != nil {
				return err
			}
		case OpExit:
			code, err := vm.popExitCode(instruction)
			if err != nil {
				return err
			}
			return ExitError{Code: code}
		case OpCall:
			nextIP, err := vm.call(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpTailCall:
			nextIP, err := vm.tailCall(instruction)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpRange:
			if err := vm.execRange(instruction); err != nil {
				return err
			}
		case OpTypeOf:
			vmv, err := vm.popVM()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			switch vmv.Kind {
			case runtime.VMKindSmallInt:
				vm.pushVM(runtime.VMValueFromValue(runtime.Type{Name: "int"}))
			case runtime.VMKindBool:
				vm.pushVM(runtime.VMValueFromValue(runtime.Type{Name: "bool"}))
			case runtime.VMKindFloat:
				vm.pushVM(runtime.VMValueFromValue(runtime.Type{Name: "float"}))
			case runtime.VMKindNull, runtime.VMKindUnset:
				vm.pushVM(runtime.VMValueFromValue(runtime.Type{Name: "null"}))
			default:
				value := vmv.ToValue()
				switch v := value.(type) {
				case *runtime.Class:
					vm.push(runtime.Type{Name: v.Name})
				case runtime.BytecodeClass:
					vm.push(runtime.Type{Name: v.Name})
				default:
					vm.push(runtime.Type{Name: value.TypeName()})
				}
			}
		case OpInstanceOf:
			if err := vm.instanceOf(instruction); err != nil {
				return err
			}
		case OpCast:
			nextIP, err := vm.cast(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpMethodCall:
			nextIP, err := vm.methodCall(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpCallResolvedMethod:
			nextIP, err := vm.callResolvedMethod(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpMethodCallSpread:
			nextIP, err := vm.methodCallSpread(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpWithEnter:
			if err := vm.withEnter(instruction); err != nil {
				return err
			}
		case OpWithExit:
			if err := vm.withExit(instruction); err != nil {
				return err
			}
		case OpDel:
			if err := vm.execDel(instruction); err != nil {
				return err
			}
		case OpMethodCallNamed:
			nextIP, err := vm.methodCallNamed(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpNativeCall:
			if err := vm.nativeCall(instruction); err != nil {
				var recoverable recoverableNativeError
				if errors.As(err, &recoverable) {
					nextIP, throwErr := vm.throwRecoverableError(instruction, ip, recoverable.err)
					if throwErr != nil {
						return throwErr
					}
					ip = nextIP
					continue
				}
				return err
			}
		case OpNativeCallNamed:
			if err := vm.nativeCallNamed(instruction); err != nil {
				var recoverable recoverableNativeError
				if errors.As(err, &recoverable) {
					nextIP, throwErr := vm.throwRecoverableError(instruction, ip, recoverable.err)
					if throwErr != nil {
						return throwErr
					}
					ip = nextIP
					continue
				}
				return err
			}
		case OpDefineClass:
			if len(instruction.Operands) != 1 {
				return vm.runtimeError(instruction, "define class instruction has invalid operands")
			}
			classIndex := instruction.Operands[0]
			if classIndex >= 0 && int(classIndex) < len(vm.chunk.Classes) {
				classInfo := vm.chunk.Classes[classIndex]
				classValue, decorated, err := vm.applyCallableDecoratorsForClass(classIndex, classInfo)
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				if decorated {
					vm.decoratedClasses[classIndex] = classValue
				}
				if err := vm.resolveCrossModuleInterfaceMembers(instruction, classIndex, classInfo); err != nil {
					return err
				}
			}
		case OpConstructClass:
			nextIP, err := vm.constructClass(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpGetField:
			nextIP, err := vm.getField(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpSetField:
			nextIP, err := vm.setField(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpCallParentConstructor:
			nextIP, err := vm.callParentConstructor(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpCallParentMethod:
			nextIP, err := vm.callParentMethod(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpGetStaticValue:
			nextIP, err := vm.getStaticValue(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpSetStaticValue:
			nextIP, err := vm.setStaticValue(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpCallStaticMethod:
			nextIP, err := vm.callStaticMethod(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpIdentical:
			if err := vm.identical(instruction); err != nil {
				return err
			}
		case OpImportModule:
			if err := vm.importModule(instruction); err != nil {
				return err
			}
		case OpImportFrom:
			if err := vm.importFrom(instruction); err != nil {
				return err
			}
		case OpMakeClosure:
			if len(instruction.Operands) < 2 {
				return vm.runtimeError(instruction, "make closure instruction has invalid operands")
			}
			funcIndex := instruction.Operands[0]
			upvalueCount := instruction.Operands[1]
			if funcIndex < 0 || int(funcIndex) >= len(vm.chunk.Functions) {
				return vm.runtimeError(instruction, "closure function index out of range")
			}
			if int64(len(instruction.Operands))-2 != upvalueCount {
				return vm.runtimeError(instruction, "closure upvalue count mismatch")
			}
			upvalues := make([]runtime.Value, upvalueCount)
			for i := int64(0); i < upvalueCount; i++ {
				outerSlot := instruction.Operands[2+i]
				if outerSlot < 0 {
					return vm.runtimeError(instruction, "closure upvalue slot out of range")
				}
				outerIdx := vm.currentFrameBP + int(outerSlot)
				if outerIdx >= len(vm.localsStack) {
					return vm.runtimeError(instruction, "closure upvalue slot out of range")
				}
				slot := vm.localsStack[outerIdx]
				var cell *runtime.BytecodeCell
				if slot.Kind == runtime.VMKindBoxed {
					if c, ok := slot.Boxed.(*runtime.BytecodeCell); ok {
						cell = c
					}
				}
				if cell == nil {
					cell = &runtime.BytecodeCell{Value: slot.ToValue()}
					vm.localsStack[outerIdx] = runtime.VMValueFromValue(cell)
				}
				upvalues[i] = cell
			}
			fn := vm.chunk.Functions[funcIndex]
			// Capture the enclosing generic frame's type bindings so the
			// closure can resolve T-typed parameters and instanceof T
			// checks against the outer call site's concrete bindings.
			var capturedBindings map[string]string
			if len(vm.frames) > 0 {
				if outer := vm.frames[len(vm.frames)-1].typeBindings; len(outer) > 0 {
					capturedBindings = make(map[string]string, len(outer))
					for k, v := range outer {
						capturedBindings[k] = v
					}
				}
			}
			vm.push(runtime.BytecodeClosure{
				FunctionIndex: funcIndex,
				Name:          fn.Name,
				Module:        vm.moduleName,
				Upvalues:      upvalues,
				TypeBindings:  capturedBindings,
			})
		case OpMakeError:
			if err := vm.makeError(instruction); err != nil {
				return err
			}
		case OpPushExceptionHandler:
			if len(instruction.Operands) != 1 {
				return vm.runtimeError(instruction, "exception handler instruction has invalid operands")
			}
			snap, snapBase := vm.snapshotCurrentFrameLocals()
			vm.exceptionHandlers = append(vm.exceptionHandlers, exceptionHandler{
				handlerIP:        int(instruction.Operands[0]),
				frameDepth:       len(vm.frames),
				stackDepth:       len(vm.stack),
				localsStackDepth: len(vm.localsStack),
				snapshot:         snap,
				snapshotBase:     snapBase,
			})
		case OpPopExceptionHandler:
			if len(vm.exceptionHandlers) == 0 {
				return vm.runtimeError(instruction, "exception handler stack is empty")
			}
			vm.exceptionHandlers = vm.exceptionHandlers[:len(vm.exceptionHandlers)-1]
		case OpThrow:
			nextIP, err := vm.throw(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpCatch:
			nextIP, err := vm.catchException(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpRethrow:
			nextIP, err := vm.rethrow(instruction, ip)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpRuntimeError:
			message, err := vm.popString(instruction, "runtime error message must be string")
			if err != nil {
				return err
			}
			return vm.runtimeError(instruction, "%s", message)
		case OpMatchError:
			if len(instruction.Operands) != 1 || int(instruction.Operands[0]) >= len(vm.chunk.Constants) {
				return vm.runtimeError(instruction, "match error instruction has invalid operands")
			}
			hint, ok := vm.chunk.Constants[instruction.Operands[0]].(runtime.String)
			if !ok {
				return vm.runtimeError(instruction, "match error hint must be string")
			}
			matchValue, popErr := vm.pop()
			if popErr != nil {
				return vm.runtimeError(instruction, "%s", popErr.Error())
			}
			msg := fmt.Sprintf("%s; got %s (type: %s)", hint.Value, matchValue.Inspect(), matchValue.TypeName())
			matchErrValue := vm.withErrorStackTrace(runtime.Error{Class: "MatchError", Message: msg}, instruction.Line)
			vm.pendingThrow = &matchErrValue
			nextMatchIP, throwErr := vm.jumpToExceptionHandler(instruction, ip)
			if throwErr != nil {
				return throwErr
			}
			ip = nextMatchIP
		case OpDeferPrint:
			value, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.addDefer(deferredAction{kind: deferKindPrint, value: value})
		case OpDeferPrintln:
			value, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.addDefer(deferredAction{kind: deferKindPrintln, value: value})
		case OpDeferNativeCall:
			if len(instruction.Operands) != 2 {
				return vm.runtimeError(instruction, "defer native call instruction has invalid operands")
			}
			nameIndex := instruction.Operands[0]
			argc := instruction.Operands[1]
			if nameIndex < 0 || int(nameIndex) >= len(vm.chunk.Constants) {
				return vm.runtimeError(instruction, "defer native call name out of range")
			}
			nameConst, ok := vm.chunk.Constants[nameIndex].(runtime.String)
			if !ok {
				return vm.runtimeError(instruction, "defer native call name must be string")
			}
			args := make([]runtime.Value, argc)
			for i := int(argc) - 1; i >= 0; i-- {
				v, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				args[i] = v
			}
			vm.addDefer(deferredAction{kind: deferKindNative, name: nameConst.Value, args: args})
		case OpDeferFuncCall:
			if len(instruction.Operands) != 2 {
				return vm.runtimeError(instruction, "defer func call instruction has invalid operands")
			}
			funcIdx := instruction.Operands[0]
			argc := instruction.Operands[1]
			if funcIdx < 0 || int(funcIdx) >= len(vm.chunk.Functions) {
				return vm.runtimeError(instruction, "defer func call function index out of range")
			}
			args := make([]runtime.Value, argc)
			for i := int(argc) - 1; i >= 0; i-- {
				v, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				args[i] = v
			}
			vm.addDefer(deferredAction{kind: deferKindFunc, funcIdx: funcIdx, args: args})
		case OpDeferMethodCall:
			if len(instruction.Operands) != 2 {
				return vm.runtimeError(instruction, "defer method call instruction has invalid operands")
			}
			nameIndex := instruction.Operands[0]
			argc := instruction.Operands[1]
			if nameIndex < 0 || int(nameIndex) >= len(vm.chunk.Constants) {
				return vm.runtimeError(instruction, "defer method call name out of range")
			}
			nameConst, ok := vm.chunk.Constants[nameIndex].(runtime.String)
			if !ok {
				return vm.runtimeError(instruction, "defer method call name must be string")
			}
			args := make([]runtime.Value, argc)
			for i := int(argc) - 1; i >= 0; i-- {
				v, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				args[i] = v
			}
			receiver, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.addDefer(deferredAction{kind: deferKindMethod, name: nameConst.Value, receiver: receiver, args: args})
		case OpDeferCallableCall:
			if len(instruction.Operands) != 1 {
				return vm.runtimeError(instruction, "defer callable call instruction has invalid operands")
			}
			argc := instruction.Operands[0]
			args := make([]runtime.Value, argc)
			for i := int(argc) - 1; i >= 0; i-- {
				v, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				args[i] = v
			}
			callable, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.addDefer(deferredAction{kind: deferKindCallable, value: callable, args: args})
		case OpDeferNativeCallNamed:
			if len(instruction.Operands) < 2 {
				return vm.runtimeError(instruction, "defer named native call has invalid operands")
			}
			nameIndex := instruction.Operands[0]
			argc := int(instruction.Operands[1])
			if len(instruction.Operands) != 2+argc {
				return vm.runtimeError(instruction, "defer named native call argument metadata mismatch")
			}
			nameConst, ok := vm.chunk.Constants[nameIndex].(runtime.String)
			if !ok {
				return vm.runtimeError(instruction, "defer named native call name must be string")
			}
			args := make([]runtime.Value, argc)
			for i := argc - 1; i >= 0; i-- {
				v, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				args[i] = v
			}
			names, err := vm.readArgNames(instruction, instruction.Operands[2:])
			if err != nil {
				return err
			}
			vm.addDefer(deferredAction{kind: deferKindNative, name: nameConst.Value, args: args, names: names})
		case OpDeferMethodCallNamed:
			if len(instruction.Operands) < 2 {
				return vm.runtimeError(instruction, "defer named method call has invalid operands")
			}
			nameIndex := instruction.Operands[0]
			argc := int(instruction.Operands[1])
			if len(instruction.Operands) != 2+argc {
				return vm.runtimeError(instruction, "defer named method call argument metadata mismatch")
			}
			nameConst, ok := vm.chunk.Constants[nameIndex].(runtime.String)
			if !ok {
				return vm.runtimeError(instruction, "defer named method call name must be string")
			}
			args := make([]runtime.Value, argc)
			for i := argc - 1; i >= 0; i-- {
				v, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				args[i] = v
			}
			names, err := vm.readArgNames(instruction, instruction.Operands[2:])
			if err != nil {
				return err
			}
			receiver, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.addDefer(deferredAction{kind: deferKindMethod, name: nameConst.Value, receiver: receiver, args: args, names: names})
		case OpDeferCallableCallNamed:
			if len(instruction.Operands) < 1 {
				return vm.runtimeError(instruction, "defer named callable call has invalid operands")
			}
			argc := int(instruction.Operands[0])
			if len(instruction.Operands) != 1+argc {
				return vm.runtimeError(instruction, "defer named callable call argument metadata mismatch")
			}
			args := make([]runtime.Value, argc)
			for i := argc - 1; i >= 0; i-- {
				v, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				args[i] = v
			}
			names, err := vm.readArgNames(instruction, instruction.Operands[1:])
			if err != nil {
				return err
			}
			callable, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.addDefer(deferredAction{kind: deferKindCallable, value: callable, args: args, names: names})
		case OpPrintln:
			value, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if vm.shouldRouteDirectPrint() {
				if _, err := vm.statefulNativeCall("io", "println", []runtime.Value{value}, nil); err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				continue
			}
			if _, err := fmt.Fprintln(vm.stdout, value.Inspect()); err != nil {
				return err
			}
		case OpPrint:
			value, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if vm.shouldRouteDirectPrint() {
				if _, err := vm.statefulNativeCall("io", "print", []runtime.Value{value}, nil); err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				continue
			}
			if _, err := fmt.Fprint(vm.stdout, value.Inspect()); err != nil {
				return err
			}
		case OpJump:
			ip = int(instruction.Operands[0]) - 1
		case OpJumpIfFalse:
			value, err := vm.popVM()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			b, ok := value.AsBool()
			if !ok {
				return vm.runtimeError(instruction, "jump condition must be bool")
			}
			if !b {
				ip = int(instruction.Operands[0]) - 1
			}
		case OpPop:
			if _, err := vm.popVM(); err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
		case OpDup:
			if len(vm.stack) == 0 {
				return vm.runtimeError(instruction, "stack underflow")
			}
			vm.pushVM(vm.stack[len(vm.stack)-1])
		case OpReturn:
			if len(vm.frames) == 0 {
				if err := vm.runDefers(instruction); err != nil {
					return err
				}
				return nil
			}
			valueVM, err := vm.popVM()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if err := vm.runDefers(instruction); err != nil {
				return err
			}
			// Read out the frame fields we need without copying the whole
			// struct, then clear references in the slot before shrinking so
			// the popped slot doesn't pin a defunct locals snapshot or
			// generator channel.
			frameIdx := len(vm.frames) - 1
			slot := &vm.frames[frameIdx]
			returnIP := slot.returnIP
			returnOverride := slot.returnOverride
			isErrorClass := slot.isErrorClass
			isImmutableClass := slot.isImmutableClass
			isDestructibleConstructor := slot.isDestructibleConstructor
			negateReturn := slot.negateReturn
			vm.popLocalsStackFrame(slot)
			slot.returnOverride = nil
			slot.generator = nil
			slot.generatorDone = nil
			slot.typeBindings = nil
			vm.frames = vm.frames[:frameIdx]
			// Hot path: a regular return — no override, no error-class
			// reification, no negate, no immutable freeze, not a
			// destructor-bearing constructor. Push the VMValue
			// straight back without converting through runtime.Value.
			if returnOverride == nil && !isErrorClass && !isImmutableClass && !negateReturn && !isDestructibleConstructor {
				vm.pushVM(valueVM)
				if inlineExitDepth >= 0 && len(vm.frames) <= inlineExitDepth {
					return nil
				}
				ip = returnIP
				continue
			}
			value := valueVM.ToValue()
			if returnOverride != nil {
				value = returnOverride
			}
			if isDestructibleConstructor {
				if inst, ok := value.(*runtime.Instance); ok {
					vm.destructibleInstances = append(vm.destructibleInstances, inst)
				}
			}
			if isErrorClass {
				if inst, ok := value.(*runtime.Instance); ok {
					msg := ""
					if m, ok2 := inst.Fields["__parentMsg"]; ok2 {
						if s, ok3 := m.(runtime.String); ok3 {
							msg = s.Value
						}
						delete(inst.Fields, "__parentMsg")
					}
					var fields map[string]runtime.Value
					if len(inst.Fields) > 0 {
						fields = inst.Fields
					}
					var parents []string
					if classInfo, ok := vm.classInfo(inst.Class.Name); ok {
						parents = vm.errorParentChain(classInfo)
					}
					value = runtime.Error{Class: inst.Class.Name, Message: msg, Fields: fields, Parents: parents}
				}
			}
			if isImmutableClass {
				if inst, ok := value.(*runtime.Instance); ok {
					inst.Frozen = true
				}
			}
			if negateReturn {
				boolValue, ok := value.(runtime.Bool)
				if !ok {
					return vm.runtimeError(instruction, "comparison operator method must return bool")
				}
				value = runtime.Bool{Value: !boolValue.Value}
			}
			vm.push(value)
			if inlineExitDepth >= 0 && len(vm.frames) <= inlineExitDepth {
				return nil
			}
			ip = returnIP
		case OpYield:
			value, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if len(vm.frames) == 0 || vm.frames[len(vm.frames)-1].generator == nil {
				return vm.runtimeError(instruction, "yield can only be used inside a generator function")
			}
			frame := vm.frames[len(vm.frames)-1]
			select {
			case frame.generator <- vmGeneratorItem{value: value}:
			case <-frame.generatorDone:
				return nil
			}
		case OpBitAnd, OpBitOr, OpBitXor, OpLShift, OpRShift:
			nextIP, err := vm.bitwiseInfix(instruction, ip)
			if err != nil {
				return err
			}
			if nextIP >= 0 {
				ip = nextIP
			}
		case OpBitNot:
			nextIP, err := vm.bitwiseNot(instruction, ip)
			if err != nil {
				return err
			}
			if nextIP >= 0 {
				ip = nextIP
			}
		case OpNullCoalesce:
			top, err := vm.peekVM()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if top.Kind != runtime.VMKindNull && top.Kind != runtime.VMKindUnset {
				ip = int(instruction.Operands[0]) - 1
			} else {
				if _, err := vm.popVM(); err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
			}
		case OpOptionalChain:
			top, err := vm.peekVM()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if top.Kind == runtime.VMKindNull || top.Kind == runtime.VMKindUnset {
				ip = int(instruction.Operands[0]) - 1
			}
		case OpCallSpread:
			if len(instruction.Operands) != 2 {
				return vm.runtimeError(instruction, "call-spread instruction has invalid operands")
			}
			funcIndex := instruction.Operands[0]
			staticArgCount := int(instruction.Operands[1])
			if funcIndex < 0 || int(funcIndex) >= len(vm.chunk.Functions) {
				return vm.runtimeError(instruction, "function index out of range")
			}
			spreadVal, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			spreadList, ok := spreadVal.(runtime.List)
			staticArgs := make([]runtime.Value, staticArgCount)
			for i := staticArgCount - 1; i >= 0; i-- {
				val, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				staticArgs[i] = val
			}
			if ok {
				combined := append(staticArgs, spreadList.Elements...)
				nextIP, err := vm.startFunction(instruction, ip, &vm.chunk.Functions[funcIndex], combined, nil)
				if err != nil {
					return err
				}
				ip = nextIP
				continue
			}
			spreadDict, ok := spreadVal.(runtime.Dict)
			if !ok {
				return vm.runtimeError(instruction, "spread argument must be a list or dict")
			}
			args, names, err := spreadDictNamedArguments(spreadDict, staticArgs)
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			ordered, err := vm.orderRuntimeArguments(instruction, vm.chunk.Functions[funcIndex], args, names, 0)
			if err != nil {
				return err
			}
			nextIP, err := vm.startFunction(instruction, ip, &vm.chunk.Functions[funcIndex], ordered, nil)
			if err != nil {
				return err
			}
			ip = nextIP
		case OpListConcat:
			if len(instruction.Operands) != 1 {
				return vm.runtimeError(instruction, "list-concat instruction has invalid operands")
			}
			n := int(instruction.Operands[0])
			segments := make([]runtime.List, n)
			for i := n - 1; i >= 0; i-- {
				val, err := vm.pop()
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				list, ok := val.(runtime.List)
				if !ok {
					return vm.runtimeError(instruction, "list-concat operand must be a list")
				}
				segments[i] = list
			}
			total := 0
			for _, seg := range segments {
				total += len(seg.Elements)
			}
			result := make([]runtime.Value, 0, total)
			for _, seg := range segments {
				result = append(result, seg.Elements...)
			}
			vm.push(runtime.List{Elements: result})
		case OpAwait:
			value, err := vm.pop()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if task, ok := value.(*runtime.Task); ok {
				result := task.Await()
				if result.Err != nil {
					return vm.runtimeError(instruction, "await: %v", result.Err)
				}
				if result.Value == nil {
					vm.push(runtime.Null{})
				} else {
					vm.push(result.Value)
				}
			} else {
				vm.push(value)
			}
		case OpSetTypeBindings:
			// Operands: [count, paramName1Idx, typeName1Idx, paramName2Idx, typeName2Idx, ...]
			// Peeks at the top of stack; if it is an instance, sets TypeBindings from the
			// declaration annotation, overriding any bindings inferred from constructor args.
			if len(instruction.Operands) < 1 {
				return vm.runtimeError(instruction, "OpSetTypeBindings: missing operands")
			}
			count := int(instruction.Operands[0])
			if len(instruction.Operands) < 1+count*2 {
				return vm.runtimeError(instruction, "OpSetTypeBindings: operand count mismatch")
			}
			top, err := vm.peek()
			if err != nil {
				return vm.runtimeError(instruction, "%s", err.Error())
			}
			if instance, ok := top.(*runtime.Instance); ok {
				for j := 0; j < count; j++ {
					pIdx := instruction.Operands[1+j*2]
					tIdx := instruction.Operands[2+j*2]
					if int(pIdx) >= len(vm.chunk.Constants) || int(tIdx) >= len(vm.chunk.Constants) {
						return vm.runtimeError(instruction, "OpSetTypeBindings: constant index out of range")
					}
					paramName, pOK := vm.chunk.Constants[pIdx].(runtime.String)
					typeName, tOK := vm.chunk.Constants[tIdx].(runtime.String)
					if !pOK || !tOK {
						return vm.runtimeError(instruction, "OpSetTypeBindings: constants must be strings")
					}
					if instance.TypeBindings == nil {
						instance.TypeBindings = map[string]string{}
					}
					instance.TypeBindings[paramName.Value] = typeName.Value
				}
			}
		case OpPlantCallTypeBindings:
			// Stages explicit `<TypeArgs>` from a generic function call's
			// `name<T>(args)` syntax into vm.pendingTypeBindings; the next
			// OpCall sees these alongside any closure-inherited bindings
			// and seeds inferTypeBindingsFromLocals with them, giving
			// explicit args strict priority over arg-inferred bindings.
			// Operands match OpSetTypeBindings: [count, pName1Idx,
			// tName1Idx, pName2Idx, tName2Idx, ...].
			if len(instruction.Operands) < 1 {
				return vm.runtimeError(instruction, "OpPlantCallTypeBindings: missing operands")
			}
			count := int(instruction.Operands[0])
			if len(instruction.Operands) < 1+count*2 {
				return vm.runtimeError(instruction, "OpPlantCallTypeBindings: operand count mismatch")
			}
			if count > 0 && vm.pendingTypeBindings == nil {
				vm.pendingTypeBindings = map[string]string{}
			}
			for j := 0; j < count; j++ {
				pIdx := instruction.Operands[1+j*2]
				tIdx := instruction.Operands[2+j*2]
				if int(pIdx) >= len(vm.chunk.Constants) || int(tIdx) >= len(vm.chunk.Constants) {
					return vm.runtimeError(instruction, "OpPlantCallTypeBindings: constant index out of range")
				}
				paramName, pOK := vm.chunk.Constants[pIdx].(runtime.String)
				typeName, tOK := vm.chunk.Constants[tIdx].(runtime.String)
				if !pOK || !tOK {
					return vm.runtimeError(instruction, "OpPlantCallTypeBindings: constants must be strings")
				}
				vm.pendingTypeBindings[paramName.Value] = typeName.Value
			}
		default:
			return vm.runtimeError(instruction, "unknown opcode %d", instruction.Op)
		}
	}
	return nil
}

func (vm *VM) ensureCallableDecorators() error {
	if vm.decoratorsApplied || vm.applyingDecorators {
		return nil
	}
	return vm.applyCallableDecorators()
}

func (vm *VM) applyCallableDecorators() error {
	vm.applyingDecorators = true
	defer func() {
		vm.applyingDecorators = false
	}()
	for index, function := range vm.chunk.Functions {
		current, decorated, err := vm.applyCallableDecoratorsForFunction(int64(index), function)
		if err != nil {
			return err
		}
		if !decorated {
			continue
		}
		if raw, ok := current.(runtime.BytecodeFunction); ok && raw.Raw && raw.Index == int64(index) {
			delete(vm.decoratedFuncs, int64(index))
			continue
		}
		vm.decoratedFuncs[int64(index)] = current
	}
	vm.decoratorsApplied = true
	return nil
}

func (vm *VM) applyCallableDecoratorsForClass(index int64, classInfo ClassInfo) (runtime.Value, bool, error) {
	if len(classInfo.Decorators) == 0 {
		return nil, false, nil
	}
	methodMetadata := vm.classFunctionMetadata(classInfo.Methods, "method", 1)
	staticMetadata := vm.classFunctionMetadata(classInfo.StaticMethods, "staticMethod", 0)
	initial := runtime.BytecodeClass{
		Module:              vm.moduleName,
		Name:                classInfo.Name,
		Doc:                 classInfo.Doc,
		Index:               index,
		Parent:              classInfo.ParentName,
		Fields:              append([]string(nil), classInfo.FieldNames...),
		Interfaces:          append([]string(nil), classInfo.Implements...),
		Decorators:          classInfo.Decorators,
		MethodDecorators:    classInfo.MethodDecorators,
		StaticDecorators:    classInfo.StaticDecorators,
		MethodMetadata:      methodMetadata,
		StaticMetadata:      staticMetadata,
		ConstructorMetadata: vm.constructorFunctionMetadata(classInfo.ConstructorIndices),
		Raw:                 true,
	}
	var current runtime.Value = initial
	decorated := false
	for i := len(classInfo.Decorators) - 1; i >= 0; i-- {
		decorator := classInfo.Decorators[i]
		if decorator.Name == "" {
			continue
		}
		indices := vm.decoratorFunctionIndices(decorator.Name)
		if len(indices) == 0 {
			continue
		}
		args, names, err := decoratorCallArguments(current, decorator)
		if err != nil {
			return nil, false, fmt.Errorf("decorator @%s: %w", decorator.Name, err)
		}
		functionIndex, ordered, err := vm.selectRuntimeNamedFunction(Instruction{}, decorator.Name, indices, args, names, 0)
		if err != nil {
			return nil, false, fmt.Errorf("decorator @%s cannot be called for class %s: %w", decorator.Name, classInfo.Name, err)
		}
		result, err := vm.CallFunctionRaw(functionIndex, ordered)
		if err != nil {
			return nil, false, fmt.Errorf("decorator @%s: %w", decorator.Name, err)
		}
		switch result.(type) {
		case runtime.BytecodeClass, runtime.BytecodeClosure, runtime.Function, runtime.OverloadedFunction, runtime.BytecodeFunction, runtime.DecoratorTarget:
			current = result
			decorated = true
		default:
			return nil, false, fmt.Errorf("decorator @%s must return class or callable, got %s", decorator.Name, result.TypeName())
		}
	}
	return current, decorated, nil
}

func (vm *VM) applyCallableDecoratorsForFunction(index int64, function FunctionInfo) (runtime.Value, bool, error) {
	if len(function.Decorators) == 0 {
		return nil, false, nil
	}
	var current runtime.Value = vm.bytecodeFunctionValue(index, true)
	decorated := false
	for i := len(function.Decorators) - 1; i >= 0; i-- {
		decorator := function.Decorators[i]
		if decorator.Target != "function" && decorator.Target != "method" && decorator.Target != "staticMethod" {
			continue
		}
		if decorator.Target == "method" {
			vm.methodReceiverFuncs[index] = true
		}
		indices := vm.decoratorFunctionIndices(decorator.Name)
		if len(indices) == 0 {
			continue
		}
		args, names, err := decoratorCallArguments(current, decorator)
		if err != nil {
			return nil, false, fmt.Errorf("decorator @%s: %w", decorator.Name, err)
		}
		functionIndex, ordered, err := vm.selectRuntimeNamedFunction(Instruction{}, decorator.Name, indices, args, names, 0)
		if err != nil {
			return nil, false, fmt.Errorf("decorator @%s cannot be called for %s: %w", decorator.Name, function.Name, err)
		}
		result, err := vm.CallFunctionRaw(functionIndex, ordered)
		if err != nil {
			return nil, false, fmt.Errorf("decorator @%s: %w", decorator.Name, err)
		}
		switch result.(type) {
		case runtime.BytecodeFunction, runtime.BytecodeClosure:
			if !vm.decoratorWrapperCompatible(function, decorator.Target, result) {
				return nil, false, fmt.Errorf("decorator @%s returned incompatible wrapper for %s", decorator.Name, function.Name)
			}
			current = result
			decorated = true
		default:
			return nil, false, fmt.Errorf("decorator @%s must return function, got %s", decorator.Name, result.TypeName())
		}
	}
	return current, decorated, nil
}

func (vm *VM) decoratorWrapperCompatible(original FunctionInfo, target string, wrapper runtime.Value) bool {
	originalOffset := 0
	if target == "method" && len(original.ParamNames) > 0 {
		originalOffset = 1
	}
	origMin, origMax, origVariadic := bytecodeFunctionArityRange(original, originalOffset)
	wrapMin, wrapMax, wrapVariadic, ok := vm.callableArityRange(wrapper)
	if !ok {
		return false
	}
	if wrapMin > origMin {
		return false
	}
	if origVariadic {
		return wrapVariadic
	}
	return wrapVariadic || wrapMax >= origMax
}

func (vm *VM) callableArityRange(value runtime.Value) (int, int, bool, bool) {
	switch callable := value.(type) {
	case runtime.BytecodeFunction:
		if callable.Index < 0 || int(callable.Index) >= len(vm.chunk.Functions) {
			return 0, 0, false, false
		}
		min, max, variadic := bytecodeFunctionArityRange(vm.chunk.Functions[callable.Index], 0)
		return min, max, variadic, true
	case runtime.BytecodeClosure:
		if callable.FunctionIndex < 0 || int(callable.FunctionIndex) >= len(vm.chunk.Functions) {
			return 0, 0, false, false
		}
		info := vm.chunk.Functions[callable.FunctionIndex]
		min, max, variadic := bytecodeFunctionArityRange(info, int(info.UpvalueCount))
		return min, max, variadic, true
	default:
		return 0, 0, false, false
	}
}

func bytecodeFunctionArityRange(function FunctionInfo, offset int) (int, int, bool) {
	if offset > len(function.ParamNames) {
		offset = len(function.ParamNames)
	}
	max := len(function.ParamNames) - offset
	min := max
	for min > 0 {
		paramIndex := offset + min - 1
		if paramIndex >= len(function.DefaultConstants) || function.DefaultConstants[paramIndex] < 0 {
			break
		}
		min--
	}
	variadic := function.Variadic && max > 0
	if variadic {
		if min == max {
			min--
		}
		return min, max, true
	}
	return min, max, false
}

func (vm *VM) decoratorFunctionIndices(name string) []int64 {
	if name == "" {
		return nil
	}
	var indices []int64
	lowerName := strings.ToLower(name)
	for index, function := range vm.chunk.Functions {
		if strings.EqualFold(function.Name, lowerName) {
			indices = append(indices, int64(index))
		}
	}
	return indices
}

func (vm *VM) bytecodeFunctionValue(index int64, raw bool) runtime.BytecodeFunction {
	function := runtime.BytecodeFunction{Index: index, Raw: raw}
	if index < 0 || int(index) >= len(vm.chunk.Functions) {
		return function
	}
	info := vm.chunk.Functions[index]
	function.Module = vm.moduleName
	function.Name = info.Name
	function.Doc = info.Doc
	function.TypeParameters = append([]string(nil), info.TypeParameters...)
	function.Parameters = parameterMetadataFromFunctionInfo(info, 0)
	function.ReturnType = info.ReturnType
	function.Async = info.Async
	function.Variadic = info.Variadic
	function.Decorators = append([]runtime.DecoratorMetadata(nil), info.Decorators...)
	function.DefLine = info.DefLine
	function.DefColumn = info.DefColumn
	return function
}

func decoratorCallArguments(current runtime.Value, decorator runtime.DecoratorMetadata) ([]runtime.Value, []string, error) {
	args := make([]runtime.Value, 0, 1+len(decorator.Args)+len(decorator.NamedArgs))
	names := make([]string, 0, 1+len(decorator.Args)+len(decorator.NamedArgs))
	args = append(args, current)
	names = append(names, "")
	for _, arg := range decorator.Args {
		if err := validateDecoratorArgument(arg); err != nil {
			return nil, nil, err
		}
		args = append(args, arg)
		names = append(names, "")
	}
	namedNames := make([]string, 0, len(decorator.NamedArgs))
	for name := range decorator.NamedArgs {
		namedNames = append(namedNames, name)
	}
	sort.Strings(namedNames)
	for _, name := range namedNames {
		arg := decorator.NamedArgs[name]
		if err := validateDecoratorArgument(arg); err != nil {
			return nil, nil, err
		}
		args = append(args, arg)
		names = append(names, name)
	}
	return args, names, nil
}

func validateDecoratorArgument(value runtime.Value) error {
	if value == nil {
		return nil
	}
	if errValue, ok := value.(runtime.Error); ok {
		return fmt.Errorf("%s", errValue.Message)
	}
	return nil
}

func (vm *VM) boolXor(instruction Instruction) error {
	right, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	left, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	l, ok := left.(runtime.Bool)
	if !ok {
		return vm.runtimeError(instruction, "left operand must be bool")
	}
	r, ok := right.(runtime.Bool)
	if !ok {
		return vm.runtimeError(instruction, "right operand must be bool")
	}
	vm.push(runtime.Bool{Value: l.Value != r.Value})
	return nil
}

func (vm *VM) not(instruction Instruction, ip int) (int, error) {
	value, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	if nextIP, handled, err := vm.callPrefixOperatorMethod(instruction, ip, value); handled || err != nil {
		return nextIP, err
	}
	boolValue, ok := value.(runtime.Bool)
	if !ok {
		return 0, vm.runtimeError(instruction, "! expects bool")
	}
	vm.push(runtime.Bool{Value: !boolValue.Value})
	return ip, nil
}

func (vm *VM) negate(instruction Instruction, ip int) (int, error) {
	value, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	if nextIP, handled, err := vm.callPrefixOperatorMethod(instruction, ip, value); handled || err != nil {
		return nextIP, err
	}
	switch value := value.(type) {
	case runtime.SmallInt:
		if value.Value == math.MinInt64 {
			vm.push(runtime.Int{Value: new(big.Int).Neg(big.NewInt(value.Value))})
		} else {
			vm.push(runtime.SmallInt{Value: -value.Value})
		}
	case runtime.Int:
		vm.push(runtime.Int{Value: new(big.Int).Neg(value.Value)})
	case runtime.Decimal:
		vm.push(runtime.Decimal{Value: new(big.Rat).Neg(value.Value)})
	case runtime.Float:
		vm.push(runtime.Float{Value: -value.Value})
	default:
		return 0, vm.runtimeError(instruction, "- expects numeric value")
	}
	return ip, nil
}

func (vm *VM) callPrefixOperatorMethod(instruction Instruction, ip int, value runtime.Value) (int, bool, error) {
	instance, ok := value.(*runtime.Instance)
	if !ok {
		return ip, false, nil
	}
	methodName, ok := prefixOperatorMethodName(instruction.Op)
	if !ok {
		return ip, false, nil
	}
	if instance.Class.Module != vm.moduleName {
		if vm.moduleLoader == nil {
			return 0, true, vm.runtimeError(instruction, "bytecode module loader is not configured")
		}
		result, err := vm.moduleLoader.CallModuleMethod(instance.Class.Module, instance.Class.Name, methodName, instance, nil)
		if err != nil {
			nextIP, perr := vm.propagateModuleError(instruction, ip, err)
			return nextIP, true, perr
		}
		vm.push(result)
		return ip, true, nil
	}
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		return 0, true, vm.runtimeError(instruction, "unknown class %s", instance.Class.Name)
	}
	indices, ok := vm.lookupMethod(classInfo, methodName)
	if !ok {
		return ip, false, nil
	}
	functionIndex, err := vm.selectRuntimeFunction(instruction, methodName, indices, nil, 1)
	if err != nil {
		return 0, true, err
	}
	nextIP, err := vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{instance}, nil)
	return nextIP, true, err
}

func prefixOperatorMethodName(op Op) (string, bool) {
	switch op {
	case OpNot:
		return "__not", true
	case OpNegate:
		return "__neg", true
	default:
		return "", false
	}
}

func (vm *VM) compare(instruction Instruction, ip int) (int, error) {
	right, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	left, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	switch instruction.Op {
	case OpLess, OpGreater, OpLessEqual, OpGreaterEqual:
		methodName := "__lt"
		switch instruction.Op {
		case OpGreater:
			methodName = "__gt"
		case OpLessEqual:
			methodName = "__lte"
		case OpGreaterEqual:
			methodName = "__gte"
		}
		if instance, ok := left.(*runtime.Instance); ok {
			classInfo, ok := vm.classInfo(instance.Class.Name)
			if !ok {
				return 0, vm.runtimeError(instruction, "unknown class %s", instance.Class.Name)
			}
			if indices, ok := vm.lookupMethod(classInfo, methodName); ok {
				functionIndex, err := vm.selectRuntimeFunction(instruction, methodName, indices, []runtime.Value{right}, 1)
				if err != nil {
					return 0, err
				}
				return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{instance, right}, nil)
			}
			if fallbackName, ok := inverseComparisonMethodName(instruction.Op); ok {
				if indices, ok := vm.lookupMethod(classInfo, fallbackName); ok {
					functionIndex, err := vm.selectRuntimeFunction(instruction, fallbackName, indices, []runtime.Value{right}, 1)
					if err != nil {
						return 0, err
					}
					nextIP, err := vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{instance, right}, nil)
					if err != nil {
						return 0, err
					}
					vm.frames[len(vm.frames)-1].negateReturn = true
					return nextIP, nil
				}
			}
		}
		cmp, err := native.NumericCompare(left, right)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		switch instruction.Op {
		case OpLess:
			vm.push(runtime.Bool{Value: cmp < 0})
		case OpGreater:
			vm.push(runtime.Bool{Value: cmp > 0})
		case OpLessEqual:
			vm.push(runtime.Bool{Value: cmp <= 0})
		case OpGreaterEqual:
			vm.push(runtime.Bool{Value: cmp >= 0})
		}
		return ip, nil
	default:
		return 0, vm.runtimeError(instruction, "unknown comparison opcode")
	}
}

func inverseComparisonMethodName(op Op) (string, bool) {
	switch op {
	case OpLessEqual:
		return "__gt", true
	case OpGreaterEqual:
		return "__lt", true
	default:
		return "", false
	}
}

func (vm *VM) equal(instruction Instruction, ip int) (int, error) {
	right, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	left, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	if instance, ok := left.(*runtime.Instance); ok {
		if instance.Class.Module != vm.moduleName {
			if len(instance.Class.Methods["__eq"]) == 0 {
				vm.push(runtime.Bool{Value: valuesEqual(left, right)})
				return ip, nil
			}
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			result, err := vm.moduleLoader.CallModuleMethod(instance.Class.Module, instance.Class.Name, "__eq", instance, vm.wrapStatefulNativeArgs([]runtime.Value{right}))
			if err != nil {
				return vm.propagateModuleError(instruction, ip, err)
			}
			vm.push(result)
			return ip, nil
		}
		classInfo, ok := vm.classInfo(instance.Class.Name)
		if !ok {
			return 0, vm.runtimeError(instruction, "unknown class %s", instance.Class.Name)
		}
		if indices, ok := vm.lookupMethod(classInfo, "__eq"); ok {
			functionIndex, err := vm.selectRuntimeFunction(instruction, "__eq", indices, []runtime.Value{right}, 1)
			if err != nil {
				return 0, err
			}
			return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{instance, right}, nil)
		}
	}
	vm.push(runtime.Bool{Value: valuesEqual(left, right)})
	return ip, nil
}

func (vm *VM) identical(instruction Instruction) error {
	right, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	left, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	vm.push(runtime.Bool{Value: valuesIdentical(left, right)})
	return nil
}

func (vm *VM) makeError(instruction Instruction) error {
	if len(instruction.Operands) != 2 {
		return vm.runtimeError(instruction, "make error instruction has invalid operands")
	}
	class, err := vm.constantStringAt(instruction, instruction.Operands[0], "error class must be string")
	if err != nil {
		return err
	}
	argc := int(instruction.Operands[1])
	if argc > 1 {
		return vm.runtimeError(instruction, "%s expects zero or one argument", class)
	}
	message := ""
	if argc == 1 {
		value, err := vm.pop()
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		stringValue, ok := value.(runtime.String)
		if !ok {
			return vm.runtimeError(instruction, "%s message must be string", class)
		}
		message = stringValue.Value
	}
	vm.push(runtime.Error{Class: class, Message: message})
	return nil
}

func (vm *VM) importFrom(instruction Instruction) error {
	if len(instruction.Operands) < 3 {
		return vm.runtimeError(instruction, "import-from instruction has invalid operands")
	}
	canonical, err := vm.constantStringAt(instruction, instruction.Operands[0], "module name must be string")
	if err != nil {
		return err
	}
	isNative := instruction.Operands[1] != 0
	count := int(instruction.Operands[2])
	if len(instruction.Operands) != 3+2*count {
		return vm.runtimeError(instruction, "import-from instruction operand count mismatch")
	}
	var module *runtime.Module
	if !isNative {
		if vm.moduleLoader == nil {
			return vm.runtimeError(instruction, "bytecode module loader is not configured")
		}
		loaded, err := vm.moduleLoader.LoadModule(canonical, "")
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		module = loaded
	}
	for i := 0; i < count; i++ {
		nameIdx := instruction.Operands[3+2*i]
		slot := instruction.Operands[3+2*i+1]
		name, err := vm.constantStringAt(instruction, nameIdx, "import-from name must be string")
		if err != nil {
			return err
		}
		var value runtime.Value
		if isNative {
			key := native.Key(canonical, name)
			fn := vm.natives.LookupKey(key)
			if fn == nil && vm.statefulNative != nil {
				value, err = vm.wrapStatefulNativeImport(canonical, name)
				if err != nil {
					return vm.runtimeError(instruction, "%s", err.Error())
				}
			} else if fn != nil {
				captured := fn
				value = runtime.Function{
					Name: canonical + "." + name,
					Native: func(_ *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
						return captured(args)
					},
				}
			} else {
				return vm.runtimeError(instruction, "from %s import %s: %s is not exported", canonical, name, name)
			}
		} else {
			v, ok := module.Exports[name]
			if !ok {
				return vm.runtimeError(instruction, "from %s import %s: %s is not exported", canonical, name, name)
			}
			value = v
		}
		if err := vm.setGlobal(slot, value); err != nil {
			return err
		}
	}
	return nil
}

// wrapStatefulNativeImport bridges from-imports of stateful native
// modules (http, db, etc.) by routing each call back through the
// statefulNative caller exposed on the VM.
func (vm *VM) wrapStatefulNativeImport(canonical, name string) (runtime.Value, error) {
	caller := vm.statefulNative
	if caller == nil {
		return nil, fmt.Errorf("from %s import %s: stateful natives unavailable", canonical, name)
	}
	return runtime.Function{
		Name: canonical + "." + name,
		Native: func(_ *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
			return caller.CallBuiltin(canonical, name, args, nil)
		},
	}, nil
}

func (vm *VM) importModule(instruction Instruction) error {
	if len(instruction.Operands) != 3 {
		return vm.runtimeError(instruction, "import module instruction has invalid operands")
	}
	if vm.moduleLoader == nil {
		return vm.runtimeError(instruction, "bytecode module loader is not configured")
	}
	canonical, err := vm.constantStringAt(instruction, instruction.Operands[0], "module name must be string")
	if err != nil {
		return err
	}
	alias, err := vm.constantStringAt(instruction, instruction.Operands[1], "module alias must be string")
	if err != nil {
		return err
	}
	module, err := vm.moduleLoader.LoadModule(canonical, alias)
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	return vm.setGlobal(instruction.Operands[2], module)
}

func (vm *VM) throw(instruction Instruction, ip int) (int, error) {
	value, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	errValue, ok := value.(runtime.Error)
	if !ok {
		return 0, vm.runtimeError(instruction, "throw expects Error, got %s", value.TypeName())
	}
	errValue = vm.withErrorStackTrace(errValue, instruction.Line)
	vm.pendingThrow = &errValue
	return vm.jumpToExceptionHandler(instruction, ip)
}

func (vm *VM) throwRecoverableError(instruction Instruction, ip int, err error) (int, error) {
	errValue := vm.withErrorStackTrace(runtime.NewRecoverableError(err), instruction.Line)
	vm.pendingThrow = &errValue
	return vm.jumpToExceptionHandler(instruction, ip)
}

func (vm *VM) withErrorStackTrace(err runtime.Error, line int) runtime.Error {
	if err.StackTrace == "" {
		err.StackTrace = vm.vmStackTrace(line)
	}
	return err
}

func (vm *VM) throwTyped(instruction Instruction, ip int, class, message string) (int, error) {
	errValue := vm.withErrorStackTrace(runtime.Error{Class: class, Message: message}, instruction.Line)
	vm.pendingThrow = &errValue
	return vm.jumpToExceptionHandler(instruction, ip)
}

func (vm *VM) rethrow(instruction Instruction, ip int) (int, error) {
	if vm.pendingThrow == nil {
		return ip, nil
	}
	return vm.jumpToExceptionHandler(instruction, ip)
}

func (vm *VM) jumpToExceptionHandler(instruction Instruction, ip int) (int, error) {
	if len(vm.exceptionHandlers) == 0 {
		thrown := *vm.pendingThrow
		// Wrap a vmThrownError so caller VMs can recover the original
		// runtime.Error (with its class + parent chain) and re-throw
		// it as a typed pendingThrow rather than collapsing it to a
		// plain string at the boundary.
		return 0, vm.runtimeErrorWith(instruction, vmThrownError{err: thrown}, "uncaught %s", thrown.Inspect())
	}
	handler := vm.exceptionHandlers[len(vm.exceptionHandlers)-1]
	vm.exceptionHandlers = vm.exceptionHandlers[:len(vm.exceptionHandlers)-1]
	for len(vm.frames) > handler.frameDepth {
		if err := vm.runDefers(instruction); err != nil {
			return 0, err
		}
		frame := vm.frames[len(vm.frames)-1]
		vm.frames = vm.frames[:len(vm.frames)-1]
		if !frame.shared && frame.basePointer < len(vm.localsStack) {
			vm.localsStack = vm.localsStack[:frame.basePointer]
		}
	}
	if len(vm.stack) > handler.stackDepth {
		vm.stack = vm.stack[:handler.stackDepth]
	}
	if len(vm.localsStack) > handler.localsStackDepth {
		vm.localsStack = vm.localsStack[:handler.localsStackDepth]
	}
	if len(handler.snapshot) > 0 && handler.snapshotBase+len(handler.snapshot) <= len(vm.localsStack) {
		copy(vm.localsStack[handler.snapshotBase:handler.snapshotBase+len(handler.snapshot)], handler.snapshot)
	}
	if len(vm.frames) > 0 {
		vm.currentFrameBP = vm.frames[len(vm.frames)-1].basePointer
	} else {
		vm.currentFrameBP = 0
	}
	return handler.handlerIP - 1, nil
}

func (vm *VM) catchException(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 3 {
		return 0, vm.runtimeError(instruction, "catch instruction has invalid operands")
	}
	if vm.pendingThrow == nil {
		return ip, nil
	}
	nextIP := int(instruction.Operands[0])
	typeIndex := instruction.Operands[1]
	slot := instruction.Operands[2]
	if typeIndex >= 0 {
		target, err := vm.constantStringAt(instruction, typeIndex, "catch type must be string")
		if err != nil {
			return 0, err
		}
		if !vm.errorValueMatches(*vm.pendingThrow, target) {
			return nextIP - 1, nil
		}
	}
	caught := *vm.pendingThrow
	vm.pendingThrow = nil
	if slot >= 0 {
		if err := vm.setLocal(slot, caught); err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
	}
	return ip, nil
}

func (vm *VM) binaryNumeric(instruction Instruction, ip int) (int, error) {
	right, err := vm.popVM()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	left, err := vm.popVM()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	if left.Kind == runtime.VMKindSmallInt && right.Kind == runtime.VMKindSmallInt {
		if err := vm.smallIntBinary(instruction, left.I64, right.I64); err != nil {
			return 0, err
		}
		return ip, nil
	}
	if left.Kind == runtime.VMKindFloat && right.Kind == runtime.VMKindFloat {
		l, _ := left.AsFloat()
		r, _ := right.AsFloat()
		if err := vm.floatBinary(instruction, runtime.Float{Value: l}, runtime.Float{Value: r}); err != nil {
			return 0, err
		}
		return ip, nil
	}
	leftV := left.ToValue()
	rightV := right.ToValue()
	if nextIP, handled, err := vm.callBinaryOperatorMethod(instruction, ip, leftV, rightV); handled || err != nil {
		return nextIP, err
	}
	if err := vm.binaryNumericValues(instruction, leftV, rightV); err != nil {
		return 0, err
	}
	return ip, nil
}

func (vm *VM) binaryNumericValues(instruction Instruction, left runtime.Value, right runtime.Value) error {
	// Fast path: both SmallInt — zero allocation for common integer arithmetic.
	if l, ok := left.(runtime.SmallInt); ok {
		if r, ok := right.(runtime.SmallInt); ok {
			return vm.smallIntBinary(instruction, l.Value, r.Value)
		}
		if r, ok := right.(runtime.Int); ok {
			return vm.intBinary(instruction, runtime.Int{Value: big.NewInt(l.Value)}, r)
		}
		if r, ok := right.(runtime.Decimal); ok {
			return vm.decimalBinary(instruction, native.SmallIntToDecimal(l), r)
		}
	}
	if l, ok := left.(runtime.Decimal); ok {
		if r, ok := right.(runtime.SmallInt); ok {
			return vm.decimalBinary(instruction, l, native.SmallIntToDecimal(r))
		}
		switch r := right.(type) {
		case runtime.Int:
			return vm.decimalBinary(instruction, l, native.IntToDecimal(r))
		case runtime.Decimal:
			return vm.decimalBinary(instruction, l, r)
		}
	}
	if l, ok := left.(runtime.Int); ok {
		if r, ok := right.(runtime.SmallInt); ok {
			return vm.intBinary(instruction, l, runtime.Int{Value: big.NewInt(r.Value)})
		}
		switch r := right.(type) {
		case runtime.Decimal:
			return vm.decimalBinary(instruction, native.IntToDecimal(l), r)
		case runtime.Int:
			return vm.intBinary(instruction, l, r)
		}
	}
	if l, ok := left.(runtime.Float); ok {
		r, ok := right.(runtime.Float)
		if !ok {
			return vm.runtimeError(instruction, "unsupported mixed numeric operands for %s: %s and %s (implicit coercion between int/decimal and float is not supported)", binaryOpSymbol(instruction.Op), left.TypeName(), right.TypeName())
		}
		return vm.floatBinary(instruction, l, r)
	}
	if isNumericValue(left) && isNumericValue(right) {
		return vm.runtimeError(instruction, "unsupported mixed numeric operands for %s: %s and %s (implicit coercion between int/decimal and float is not supported)", binaryOpSymbol(instruction.Op), left.TypeName(), right.TypeName())
	}
	return vm.runtimeError(instruction, "left operand must be numeric")
}

// compareJumpIntFallback handles the fused integer compare-and-branch opcodes
// for non-SmallInt operands (e.g., runtime.Int after overflow). The SmallInt
// fast path is inlined directly into the Run() dispatch.
func (vm *VM) compareJumpIntFallback(instruction Instruction, ip int) (int, error) {
	var cmpOp Op
	jumpIfTrue := false
	switch instruction.Op {
	case OpJumpIfNotLessInt:
		cmpOp = OpLess
	case OpJumpIfNotLessEqualInt:
		cmpOp = OpLessEqual
	case OpJumpIfNotGreaterInt:
		cmpOp = OpGreater
	case OpJumpIfNotGreaterEqualInt:
		cmpOp = OpGreaterEqual
	case OpJumpIfNotEqualInt:
		cmpOp = OpEqual
	case OpJumpIfEqualInt:
		cmpOp = OpEqual
		jumpIfTrue = true
	}
	genericOp := instruction
	genericOp.Op = cmpOp
	var nextIP int
	var err error
	if cmpOp == OpEqual {
		nextIP, err = vm.equal(genericOp, ip)
	} else {
		nextIP, err = vm.compare(genericOp, ip)
	}
	if err != nil {
		return ip, err
	}
	value, err := vm.pop()
	if err != nil {
		return ip, vm.runtimeError(instruction, "%s", err.Error())
	}
	b, ok := value.(runtime.Bool)
	if !ok {
		return ip, vm.runtimeError(instruction, "compare result must be bool")
	}
	shouldJump := b.Value
	if !jumpIfTrue {
		shouldJump = !b.Value
	}
	if shouldJump {
		return int(instruction.Operands[0]) - 1, nil
	}
	return nextIP, nil
}

// intSelfArith handles the fused self-update integer arithmetic opcodes
// (OpAdd/SubLocalIntLocal, OpAdd/SubLocalIntConst, and Global mirrors). The
// opcode reads the destination slot, applies the right-hand operand, stores
// the result back, and pushes it onto the stack.
func (vm *VM) intSelfArith(instruction Instruction) error {
	if len(instruction.Operands) != 2 {
		return vm.runtimeError(instruction, "int self-arith has invalid operands")
	}
	dst := instruction.Operands[0]
	rhsOperand := instruction.Operands[1]
	var lhs runtime.Value
	var err error
	isGlobal := false
	switch instruction.Op {
	case OpAddLocalIntLocal, OpSubLocalIntLocal, OpAddLocalIntConst, OpSubLocalIntConst,
		OpAddLocalIntGlobal, OpSubLocalIntGlobal:
		lhs, err = vm.getLocal(dst)
	case OpAddGlobalIntGlobal, OpSubGlobalIntGlobal, OpAddGlobalIntConst, OpSubGlobalIntConst,
		OpAddGlobalIntLocal, OpSubGlobalIntLocal:
		lhs, err = vm.getGlobal(dst)
		isGlobal = true
	default:
		return vm.runtimeError(instruction, "unknown int self-arith opcode")
	}
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	var rhs runtime.Value
	switch instruction.Op {
	case OpAddLocalIntLocal, OpSubLocalIntLocal,
		OpAddGlobalIntLocal, OpSubGlobalIntLocal:
		rhs, err = vm.getLocal(rhsOperand)
	case OpAddGlobalIntGlobal, OpSubGlobalIntGlobal,
		OpAddLocalIntGlobal, OpSubLocalIntGlobal:
		rhs, err = vm.getGlobal(rhsOperand)
	default:
		rhs = runtime.SmallInt{Value: rhsOperand}
	}
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	subtract := false
	switch instruction.Op {
	case OpSubLocalIntLocal, OpSubLocalIntConst, OpSubGlobalIntGlobal, OpSubGlobalIntConst,
		OpSubGlobalIntLocal, OpSubLocalIntGlobal:
		subtract = true
	}
	result, err := addOrSubInt(lhs, rhs, subtract)
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	if isGlobal {
		if err := vm.setGlobal(dst, result); err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
	} else {
		if err := vm.setLocal(dst, result); err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
	}
	vm.push(result)
	return nil
}

// addOrSubInt computes a+b or a-b with SmallInt fast path and big.Int overflow
// fallback. Returns an error if either operand is not an integer.
func addOrSubInt(a, b runtime.Value, subtract bool) (runtime.Value, error) {
	al, aOK := a.(runtime.SmallInt)
	bl, bOK := b.(runtime.SmallInt)
	if aOK && bOK {
		if subtract {
			v := al.Value - bl.Value
			if (al.Value^bl.Value)&(al.Value^v) < 0 {
				return runtime.Int{Value: new(big.Int).Sub(big.NewInt(al.Value), big.NewInt(bl.Value))}, nil
			}
			return runtime.SmallInt{Value: v}, nil
		}
		v := al.Value + bl.Value
		if (al.Value^v)&(bl.Value^v) < 0 {
			return runtime.Int{Value: new(big.Int).Add(big.NewInt(al.Value), big.NewInt(bl.Value))}, nil
		}
		return runtime.SmallInt{Value: v}, nil
	}
	// Slow path: promote both operands to big.Int.
	aBig, ok := intToBigInt(a)
	if !ok {
		return nil, fmt.Errorf("int arithmetic expects int, got %s", a.TypeName())
	}
	bBig, ok := intToBigInt(b)
	if !ok {
		return nil, fmt.Errorf("int arithmetic expects int, got %s", b.TypeName())
	}
	result := new(big.Int)
	if subtract {
		result.Sub(aBig, bBig)
	} else {
		result.Add(aBig, bBig)
	}
	if result.IsInt64() {
		return runtime.SmallInt{Value: result.Int64()}, nil
	}
	return runtime.Int{Value: result}, nil
}

func intToBigInt(v runtime.Value) (*big.Int, bool) {
	switch x := v.(type) {
	case runtime.SmallInt:
		return big.NewInt(x.Value), true
	case runtime.Int:
		return x.Value, true
	}
	return nil, false
}

func (vm *VM) updateIntSlot(instruction Instruction) error {
	if len(instruction.Operands) != 1 {
		return vm.runtimeError(instruction, "integer slot update has invalid operands")
	}
	slot := instruction.Operands[0]
	var old runtime.VMValue
	var err error
	switch instruction.Op {
	case OpIncLocalInt, OpDecLocalInt:
		old, err = vm.getLocalVM(slot)
	case OpIncGlobalInt, OpDecGlobalInt:
		old, err = vm.getGlobalVM(slot)
	default:
		return vm.runtimeError(instruction, "unknown integer slot update opcode")
	}
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	delta := int64(1)
	if instruction.Op == OpDecLocalInt || instruction.Op == OpDecGlobalInt {
		delta = -1
	}
	var next runtime.VMValue
	if old.Kind == runtime.VMKindSmallInt {
		nv := old.I64 + delta
		if (old.I64^nv)&(delta^nv) < 0 {
			next = runtime.VMValueFromValue(runtime.Int{Value: new(big.Int).Add(big.NewInt(old.I64), big.NewInt(delta))})
		} else {
			next = runtime.VMValueSmallInt(nv)
		}
	} else {
		nextVal, err := updatedIntValue(instruction, old.ToValue())
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		next = runtime.VMValueFromValue(nextVal)
	}
	switch instruction.Op {
	case OpIncLocalInt, OpDecLocalInt:
		if err := vm.setLocalVM(slot, next); err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
	case OpIncGlobalInt, OpDecGlobalInt:
		if err := vm.setGlobalVM(slot, next); err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
	}
	vm.pushVM(old)
	return nil
}

func updatedIntValue(instruction Instruction, old runtime.Value) (runtime.Value, error) {
	delta := int64(1)
	if instruction.Op == OpDecLocalInt || instruction.Op == OpDecGlobalInt {
		delta = -1
	}
	switch value := old.(type) {
	case runtime.SmallInt:
		next := value.Value + delta
		if (value.Value^next)&(delta^next) < 0 {
			return runtime.Int{Value: new(big.Int).Add(big.NewInt(value.Value), big.NewInt(delta))}, nil
		}
		return runtime.SmallInt{Value: next}, nil
	case runtime.Int:
		return runtime.Int{Value: new(big.Int).Add(value.Value, big.NewInt(delta))}, nil
	default:
		return nil, fmt.Errorf("integer update expects int, got %s", old.TypeName())
	}
}

func (vm *VM) appendListSlot(instruction Instruction) error {
	if len(instruction.Operands) != 1 {
		return vm.runtimeError(instruction, "list append slot update has invalid operands")
	}
	value, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	slot := instruction.Operands[0]
	var current runtime.Value
	switch instruction.Op {
	case OpAppendLocalList:
		current, err = vm.getLocal(slot)
	case OpAppendGlobalList:
		current, err = vm.getGlobal(slot)
	default:
		return vm.runtimeError(instruction, "unknown list append slot update opcode")
	}
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	list, ok := current.(runtime.List)
	if !ok {
		return vm.runtimeError(instruction, "list append requires list, got %s", current.TypeName())
	}
	list.Elements = append(list.Elements, value)
	switch instruction.Op {
	case OpAppendLocalList:
		if err := vm.setLocal(slot, list); err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
	case OpAppendGlobalList:
		if err := vm.setGlobal(slot, list); err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
	}
	vm.push(list)
	return nil
}

// smallIntBinary performs arithmetic on two int64 values, promoting to big.Int on overflow.
func (vm *VM) smallIntBinary(instruction Instruction, l int64, r int64) error {
	switch instruction.Op {
	case OpAdd:
		result := l + r
		// Overflow: same-sign operands produce opposite-sign result.
		if (l^result)&(r^result) < 0 {
			vm.push(runtime.Int{Value: new(big.Int).Add(big.NewInt(l), big.NewInt(r))})
		} else {
			vm.push(runtime.SmallInt{Value: result})
		}
	case OpSub:
		result := l - r
		// Overflow: different-sign operands produce opposite sign from l.
		if (l^r)&(l^result) < 0 {
			vm.push(runtime.Int{Value: new(big.Int).Sub(big.NewInt(l), big.NewInt(r))})
		} else {
			vm.push(runtime.SmallInt{Value: result})
		}
	case OpMul:
		result := l * r
		// Overflow check: result/l != r (when l != 0).
		if l != 0 && result/l != r {
			vm.push(runtime.Int{Value: new(big.Int).Mul(big.NewInt(l), big.NewInt(r))})
		} else {
			vm.push(runtime.SmallInt{Value: result})
		}
	case OpDiv:
		return vm.decimalBinary(instruction, native.SmallIntToDecimal(runtime.SmallInt{Value: l}), native.SmallIntToDecimal(runtime.SmallInt{Value: r}))
	case OpIntDiv:
		if r == 0 {
			return vm.runtimeError(instruction, "integer division by zero")
		}
		// math.MinInt64 / -1 is the only overflow case for int64 division.
		if l == math.MinInt64 && r == -1 {
			vm.push(runtime.Int{Value: new(big.Int).Neg(big.NewInt(l))})
		} else {
			q := l / r
			rem := l - q*r
			if rem != 0 && ((l < 0) != (r < 0)) {
				q--
			}
			vm.push(runtime.SmallInt{Value: q})
		}
	case OpMod:
		if r == 0 {
			return vm.runtimeError(instruction, "modulo by zero")
		}
		m := l % r
		if m != 0 && ((l < 0) != (r < 0)) {
			m += r
		}
		vm.push(runtime.SmallInt{Value: m})
	case OpPow:
		if r < 0 {
			return vm.runtimeError(instruction, "exponent must be a non-negative int64")
		}
		result := new(big.Int).Exp(big.NewInt(l), big.NewInt(r), nil)
		if result.IsInt64() {
			vm.push(runtime.SmallInt{Value: result.Int64()})
		} else {
			vm.push(runtime.Int{Value: result})
		}
	default:
		return vm.runtimeError(instruction, "unsupported operator %s for int values", binaryOpSymbol(instruction.Op))
	}
	return nil
}

func isNumericValue(value runtime.Value) bool {
	switch value.(type) {
	case runtime.SmallInt, runtime.Int, runtime.Decimal, runtime.Float:
		return true
	default:
		return false
	}
}

func binaryOpSymbol(op Op) string {
	switch op {
	case OpAdd:
		return "+"
	case OpSub:
		return "-"
	case OpMul:
		return "*"
	case OpDiv:
		return "/"
	case OpIntDiv:
		return "//"
	case OpMod:
		return "%"
	case OpPow:
		return "**"
	default:
		return fmt.Sprintf("opcode %d", op)
	}
}

func (vm *VM) callBinaryOperatorMethod(instruction Instruction, ip int, left runtime.Value, right runtime.Value) (int, bool, error) {
	instance, ok := left.(*runtime.Instance)
	if !ok {
		return ip, false, nil
	}
	methodName, ok := binaryOperatorMethodName(instruction.Op)
	if !ok {
		return ip, false, nil
	}
	if instance.Class.Module != vm.moduleName {
		if len(instance.Class.Methods[strings.ToLower(methodName)]) == 0 {
			return ip, false, nil
		}
		if vm.moduleLoader == nil {
			return 0, true, vm.runtimeError(instruction, "bytecode module loader is not configured")
		}
		result, err := vm.moduleLoader.CallModuleMethod(instance.Class.Module, instance.Class.Name, methodName, instance, vm.wrapStatefulNativeArgs([]runtime.Value{right}))
		if err != nil {
			nextIP, perr := vm.propagateModuleError(instruction, ip, err)
			return nextIP, true, perr
		}
		vm.push(result)
		return ip, true, nil
	}
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		return 0, true, vm.runtimeError(instruction, "unknown class %s", instance.Class.Name)
	}
	indices, ok := vm.lookupMethod(classInfo, methodName)
	if !ok {
		return ip, false, nil
	}
	functionIndex, err := vm.selectRuntimeFunction(instruction, methodName, indices, []runtime.Value{right}, 1)
	if err != nil {
		return 0, true, err
	}
	nextIP, err := vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{instance, right}, nil)
	return nextIP, true, err
}

func binaryOperatorMethodName(op Op) (string, bool) {
	switch op {
	case OpAdd:
		return "__add", true
	case OpSub:
		return "__sub", true
	case OpMul:
		return "__mul", true
	case OpDiv:
		return "__div", true
	case OpIntDiv:
		return "__intdiv", true
	case OpMod:
		return "__mod", true
	case OpPow:
		return "__pow", true
	default:
		return "", false
	}
}

func (vm *VM) intBinary(instruction Instruction, l runtime.Int, r runtime.Int) error {
	switch instruction.Op {
	case OpAdd:
		vm.push(runtime.Int{Value: new(big.Int).Add(l.Value, r.Value)})
	case OpSub:
		vm.push(runtime.Int{Value: new(big.Int).Sub(l.Value, r.Value)})
	case OpMul:
		vm.push(runtime.Int{Value: new(big.Int).Mul(l.Value, r.Value)})
	case OpDiv:
		return vm.decimalBinary(instruction, native.IntToDecimal(l), native.IntToDecimal(r))
	case OpIntDiv:
		if r.Value.Sign() == 0 {
			return vm.runtimeError(instruction, "integer division by zero")
		}
		quotient, _ := intFloorDivMod(l.Value, r.Value)
		vm.push(runtime.Int{Value: quotient})
	case OpMod:
		if r.Value.Sign() == 0 {
			return vm.runtimeError(instruction, "modulo by zero")
		}
		_, remainder := intFloorDivMod(l.Value, r.Value)
		vm.push(runtime.Int{Value: remainder})
	case OpPow:
		if !r.Value.IsInt64() || r.Value.Sign() < 0 {
			return vm.runtimeError(instruction, "exponent must be a non-negative int64")
		}
		vm.push(runtime.Int{Value: new(big.Int).Exp(l.Value, big.NewInt(r.Value.Int64()), nil)})
	default:
		return vm.runtimeError(instruction, "unsupported operator %s for int values", binaryOpSymbol(instruction.Op))
	}
	return nil
}

func (vm *VM) decimalBinary(instruction Instruction, l runtime.Decimal, r runtime.Decimal) error {
	switch instruction.Op {
	case OpAdd:
		vm.push(runtime.Decimal{Value: new(big.Rat).Add(l.Value, r.Value)})
	case OpSub:
		vm.push(runtime.Decimal{Value: new(big.Rat).Sub(l.Value, r.Value)})
	case OpMul:
		vm.push(runtime.Decimal{Value: new(big.Rat).Mul(l.Value, r.Value)})
	case OpDiv:
		if r.Value.Sign() == 0 {
			return vm.runtimeError(instruction, "decimal division by zero")
		}
		vm.push(runtime.Decimal{Value: new(big.Rat).Quo(l.Value, r.Value)})
	case OpIntDiv:
		if r.Value.Sign() == 0 {
			return vm.runtimeError(instruction, "decimal integer division by zero")
		}
		quotient := new(big.Rat).Quo(l.Value, r.Value)
		/* Same-kind result: decimal // decimal stays a decimal
		 * (with an integer numerator). Returning Int here would
		 * silently collapse arbitrary-precision values. */
		vm.push(runtime.Decimal{Value: new(big.Rat).SetInt(ratFloorInt(quotient))})
	case OpMod:
		if r.Value.Sign() == 0 {
			return vm.runtimeError(instruction, "decimal modulo by zero")
		}
		quotient := new(big.Rat).Quo(l.Value, r.Value)
		floor := new(big.Rat).SetInt(ratFloorInt(quotient))
		product := new(big.Rat).Mul(floor, r.Value)
		vm.push(runtime.Decimal{Value: new(big.Rat).Sub(l.Value, product)})
	case OpPow:
		value, err := decimalPow(l.Value, r.Value)
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(runtime.Decimal{Value: value})
	default:
		return vm.runtimeError(instruction, "unsupported operator %s for decimal values", binaryOpSymbol(instruction.Op))
	}
	return nil
}

func (vm *VM) floatBinary(instruction Instruction, l runtime.Float, r runtime.Float) error {
	switch instruction.Op {
	case OpAdd:
		vm.push(runtime.Float{Value: l.Value + r.Value})
	case OpSub:
		vm.push(runtime.Float{Value: l.Value - r.Value})
	case OpMul:
		vm.push(runtime.Float{Value: l.Value * r.Value})
	case OpDiv:
		vm.push(runtime.Float{Value: l.Value / r.Value})
	case OpIntDiv:
		if r.Value == 0 {
			return vm.runtimeError(instruction, "float division by zero")
		}
		vm.push(runtime.Float{Value: math.Floor(l.Value / r.Value)})
	case OpMod:
		if r.Value == 0 {
			return vm.runtimeError(instruction, "float modulo by zero")
		}
		/* Floor modulo: sign of the remainder follows the divisor.
		 * Consistent with Geblang's int and decimal modulo, and
		 * with Python's `%` for floats. (Go's `math.Mod` is
		 * truncated modulo, which we deliberately don't expose.) */
		vm.push(runtime.Float{Value: l.Value - math.Floor(l.Value/r.Value)*r.Value})
	case OpPow:
		vm.push(runtime.Float{Value: math.Pow(l.Value, r.Value)})
	default:
		return vm.runtimeError(instruction, "unsupported operator %s for float values", binaryOpSymbol(instruction.Op))
	}
	return nil
}

func ratFloorInt(value *big.Rat) *big.Int {
	quotient := new(big.Int).Quo(value.Num(), value.Denom())
	if value.Sign() < 0 && new(big.Int).Rem(value.Num(), value.Denom()).Sign() != 0 {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient
}

func intFloorDivMod(left *big.Int, right *big.Int) (*big.Int, *big.Int) {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(left, right, remainder)
	if remainder.Sign() != 0 && ((left.Sign() < 0) != (right.Sign() < 0)) {
		quotient.Sub(quotient, big.NewInt(1))
		remainder.Add(remainder, right)
	}
	return quotient, remainder
}

func decimalPow(base *big.Rat, exponent *big.Rat) (*big.Rat, error) {
	if exponent.IsInt() {
		if !exponent.Num().IsInt64() {
			return nil, fmt.Errorf("decimal exponent is out of int64 range")
		}
		exp := exponent.Num().Int64()
		if exp == 0 {
			return big.NewRat(1, 1), nil
		}
		result := big.NewRat(1, 1)
		factor := new(big.Rat).Set(base)
		if exp < 0 {
			if factor.Sign() == 0 {
				return nil, fmt.Errorf("zero cannot be raised to a negative exponent")
			}
			factor.Inv(factor)
			exp = -exp
		}
		for exp > 0 {
			if exp&1 == 1 {
				result.Mul(result, factor)
			}
			factor.Mul(factor, factor)
			exp >>= 1
		}
		return result, nil
	}
	baseFloat, _ := base.Float64()
	exponentFloat, _ := exponent.Float64()
	result := math.Pow(baseFloat, exponentFloat)
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return nil, fmt.Errorf("decimal exponent result is not finite")
	}
	decimal, err := runtime.NewDecimalLiteral(strconv.FormatFloat(result, 'g', -1, 64))
	if err != nil {
		return nil, err
	}
	return decimal.Value, nil
}

func (vm *VM) add(instruction Instruction, ip int) (int, error) {
	right, err := vm.popVM()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	left, err := vm.popVM()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	if left.Kind == runtime.VMKindSmallInt && right.Kind == runtime.VMKindSmallInt {
		result := left.I64 + right.I64
		if (left.I64^result)&(right.I64^result) >= 0 {
			vm.pushVM(runtime.VMValueSmallInt(result))
			return ip, nil
		}
		vm.pushVM(runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: runtime.Int{Value: new(big.Int).Add(big.NewInt(left.I64), big.NewInt(right.I64))}})
		return ip, nil
	}
	if left.Kind == runtime.VMKindFloat && right.Kind == runtime.VMKindFloat {
		l, _ := left.AsFloat()
		r, _ := right.AsFloat()
		vm.pushVM(runtime.VMValueFloat(l + r))
		return ip, nil
	}
	leftV := left.ToValue()
	rightV := right.ToValue()
	// String fast path: skip the method-dispatch detour because the
	// built-in `string` has no __add__ overload.
	if l, ok := leftV.(runtime.String); ok {
		if r, ok := rightV.(runtime.String); ok {
			vm.push(runtime.String{Value: l.Value + r.Value})
			return ip, nil
		}
	}
	if nextIP, handled, err := vm.callBinaryOperatorMethod(instruction, ip, leftV, rightV); handled || err != nil {
		return nextIP, err
	}
	if l, ok := leftV.(runtime.String); ok {
		r, ok := rightV.(runtime.String)
		if !ok {
			return 0, vm.runtimeError(instruction, "right operand must be string")
		}
		vm.push(runtime.String{Value: l.Value + r.Value})
		return ip, nil
	}
	if err := vm.binaryNumericValues(instruction, leftV, rightV); err != nil {
		return 0, err
	}
	return ip, nil
}

func (vm *VM) index(instruction Instruction) error {
	index, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	left, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	switch value := left.(type) {
	case runtime.List:
		i, err := indexInt(index)
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		if i < 0 {
			i = len(value.Elements) + i
		}
		if i < 0 || i >= len(value.Elements) {
			return vm.runtimeError(instruction, "list index out of range")
		}
		vm.push(value.Elements[i])
	case runtime.String:
		i, err := indexInt(index)
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		runes := []rune(value.Value)
		if i < 0 {
			i = len(runes) + i
		}
		if i < 0 || i >= len(runes) {
			return vm.runtimeError(instruction, "string index out of range")
		}
		vm.push(runtime.String{Value: string(runes[i])})
	case runtime.Bytes:
		i, err := indexInt(index)
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		if i < 0 {
			i = len(value.Value) + i
		}
		if i < 0 || i >= len(value.Value) {
			return vm.runtimeError(instruction, "bytes index out of range")
		}
		vm.push(runtime.NewInt64(int64(value.Value[i])))
	case runtime.Dict:
		entry, ok := value.Entries[dictKeyFor(index)]
		if !ok {
			vm.push(runtime.Null{})
			return nil
		}
		vm.push(entry.Value)
	default:
		return vm.runtimeError(instruction, "%s is not indexable", left.TypeName())
	}
	return nil
}

func (vm *VM) setIndex(instruction Instruction, ip int) (int, error) {
	newValue, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	index, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	left, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	switch value := left.(type) {
	case runtime.List:
		if value.Frozen {
			return vm.throwTyped(instruction, ip, "ImmutableError", "cannot modify frozen list")
		}
		i, err := indexInt(index)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		if i < 0 {
			i = len(value.Elements) + i
		}
		if i < 0 || i >= len(value.Elements) {
			return 0, vm.runtimeError(instruction, "list index out of range")
		}
		value.Elements[i] = newValue
	case runtime.Dict:
		if value.Frozen {
			return vm.throwTyped(instruction, ip, "ImmutableError", "cannot modify frozen dict")
		}
		value.Entries[dictKeyFor(index)] = runtime.DictEntry{Key: index, Value: newValue}
	default:
		return 0, vm.runtimeError(instruction, "%s does not support index assignment", left.TypeName())
	}
	vm.push(newValue)
	return ip, nil
}

func (vm *VM) slice(instruction Instruction) error {
	stepValue, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	endValue, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	startValue, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	left, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	exclusive := instruction.Operands[0] != 0
	switch value := left.(type) {
	case runtime.List:
		indices, err := sliceIndices(startValue, endValue, stepValue, exclusive, len(value.Elements))
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		elements := make([]runtime.Value, len(indices))
		for i, idx := range indices {
			elements[i] = value.Elements[idx]
		}
		vm.push(runtime.List{Elements: elements})
	case runtime.String:
		runes := []rune(value.Value)
		indices, err := sliceIndices(startValue, endValue, stepValue, exclusive, len(runes))
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		out := make([]rune, len(indices))
		for i, idx := range indices {
			out[i] = runes[idx]
		}
		vm.push(runtime.String{Value: string(out)})
	case runtime.Bytes:
		indices, err := sliceIndices(startValue, endValue, stepValue, exclusive, len(value.Value))
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		out := make([]byte, len(indices))
		for i, idx := range indices {
			out[i] = value.Value[idx]
		}
		vm.push(runtime.Bytes{Value: out})
	default:
		return vm.runtimeError(instruction, "%s is not sliceable", left.TypeName())
	}
	return nil
}

// sliceIndices computes the list of original indices a slice expression
// produces. Honours Python-style step (including negative) when stepValue
// is non-null; otherwise falls back to sliceBounds for the common
// contiguous case. Returns the indices in iteration order.
func sliceIndices(startValue runtime.Value, endValue runtime.Value, stepValue runtime.Value, exclusive bool, length int) ([]int, error) {
	step := 1
	if _, ok := stepValue.(runtime.Null); !ok {
		s, err := indexInt(stepValue)
		if err != nil {
			return nil, err
		}
		step = s
	}
	if step == 0 {
		return nil, fmt.Errorf("slice step cannot be zero")
	}
	if step == 1 {
		start, end, err := sliceBounds(startValue, endValue, exclusive, length)
		if err != nil {
			return nil, err
		}
		out := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			out = append(out, i)
		}
		return out, nil
	}
	// Python-style start/stop defaults depend on step's sign.
	var start, stop int
	if step > 0 {
		start = 0
		stop = length
	} else {
		start = length - 1
		stop = -1
	}
	if _, ok := startValue.(runtime.Null); !ok {
		i, err := indexInt(startValue)
		if err != nil {
			return nil, err
		}
		if i < 0 {
			i += length
		}
		if step > 0 {
			if i < 0 {
				i = 0
			}
			if i > length {
				i = length
			}
		} else {
			if i < -1 {
				i = -1
			}
			if i > length-1 {
				i = length - 1
			}
		}
		start = i
	}
	if _, ok := endValue.(runtime.Null); !ok {
		i, err := indexInt(endValue)
		if err != nil {
			return nil, err
		}
		if !exclusive {
			if step > 0 {
				i++
			} else {
				i--
			}
		}
		if i < 0 {
			i += length
		}
		if step > 0 {
			if i < 0 {
				i = 0
			}
			if i > length {
				i = length
			}
		} else {
			if i < -1 {
				i = -1
			}
			if i > length-1 {
				i = length - 1
			}
		}
		stop = i
	}
	out := []int{}
	if step > 0 {
		for i := start; i < stop; i += step {
			out = append(out, i)
		}
	} else {
		for i := start; i > stop; i += step {
			out = append(out, i)
		}
	}
	return out, nil
}

func sliceBounds(startValue runtime.Value, endValue runtime.Value, exclusive bool, length int) (int, int, error) {
	start := 0
	if _, ok := startValue.(runtime.Null); !ok {
		i, err := indexInt(startValue)
		if err != nil {
			return 0, 0, err
		}
		start = i
	}
	end := length
	if _, ok := endValue.(runtime.Null); !ok {
		i, err := indexInt(endValue)
		if err != nil {
			return 0, 0, err
		}
		end = i
		if !exclusive {
			end++
		}
	}
	if start < 0 {
		start = length + start
	}
	if end < 0 {
		end = length + end
	}
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if start > length {
		start = length
	}
	if end > length {
		end = length
	}
	if end < start {
		end = start
	}
	return start, end, nil
}

func (vm *VM) iterInit(instruction Instruction) error {
	value, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	iter, err := vm.iteratorFor(instruction, value)
	if err != nil {
		return err
	}
	vm.push(iter)
	return nil
}

// iteratorFor turns any iterable value into an iteratorValue. List /
// Generator / Range take the existing fast paths; user-class
// instances dispatch __iter() (or use __next/__done directly when
// __iter is absent) and the result is itself fed back into this
// function so a Stream-style class can return a Generator, another
// Instance, a List, etc.
func (vm *VM) iteratorFor(instruction Instruction, value runtime.Value) (*iteratorValue, error) {
	switch v := value.(type) {
	case runtime.List:
		values := append([]runtime.Value(nil), v.Elements...)
		return &iteratorValue{values: values}, nil
	case *runtime.Generator:
		return &iteratorValue{generator: v}, nil
	case runtime.Range:
		return newRangeIterator(v), nil
	case *runtime.Instance:
		return vm.userInstanceIterator(instruction, v)
	}
	return nil, vm.runtimeError(instruction, "%s is not iterable", value.TypeName())
}

// userInstanceIterator resolves the iterator for a *runtime.Instance.
// Calls obj.__iter() when defined and recursively iteratorFor's the
// result. Falls back to the instance-as-iterator path when only
// __next is defined.
func (vm *VM) userInstanceIterator(instruction Instruction, instance *runtime.Instance) (*iteratorValue, error) {
	if instance == nil || instance.Class == nil {
		return nil, vm.runtimeError(instruction, "cannot iterate uninitialised instance")
	}
	hasIter, hasNext := vm.instanceIteratorHooks(instance)
	if hasIter {
		result, err := vm.CallMethod(instance, "__iter", nil)
		if err != nil {
			return nil, vm.runtimeError(instruction, "%s.__iter: %s", instance.Class.Name, err.Error())
		}
		// If __iter returns the same instance and the instance is its
		// own iterator (has __next/__done), break the recursion.
		if inst, ok := result.(*runtime.Instance); ok && inst == instance {
			if hasNext {
				return &iteratorValue{userIter: instance}, nil
			}
		}
		return vm.iteratorFor(instruction, result)
	}
	if hasNext {
		return &iteratorValue{userIter: instance}, nil
	}
	return nil, vm.runtimeError(instruction, "%s is not iterable: define __iter() or __next()/__done()", instance.Class.Name)
}

// instanceIteratorHooks reports whether an instance's class exposes
// __iter and __next. Falls back to the runtime.Class method table for
// instances whose ClassInfo lives in a different chunk (e.g. a class
// defined in an imported stdlib module).
func (vm *VM) instanceIteratorHooks(instance *runtime.Instance) (hasIter, hasNext bool) {
	if instance.Class == nil {
		return false, false
	}
	if classInfo, ok := vm.classInfo(instance.Class.Name); ok {
		_, hasIter = vm.lookupMethod(classInfo, "__iter")
		_, hasNext = vm.lookupMethod(classInfo, "__next")
		return hasIter, hasNext
	}
	for c := instance.Class; c != nil; c = c.Parent {
		if len(c.Methods["__iter"]) > 0 {
			hasIter = true
		}
		if len(c.Methods["__next"]) > 0 {
			hasNext = true
		}
		if hasIter && hasNext {
			break
		}
	}
	return hasIter, hasNext
}

func (vm *VM) iterNext(instruction Instruction) (bool, error) {
	if len(instruction.Operands) != 3 {
		return false, vm.runtimeError(instruction, "iterator instruction has invalid operands")
	}
	iterSlot := instruction.Operands[1]
	valueSlot := instruction.Operands[2]
	value, err := vm.getLocal(iterSlot)
	if err != nil {
		return false, vm.runtimeError(instruction, "%s", err.Error())
	}
	iter, ok := value.(*iteratorValue)
	if !ok {
		return false, vm.runtimeError(instruction, "local is not an iterator")
	}
	if iter.userIter != nil {
		next, ok, err := vm.advanceUserIterator(instruction, iter.userIter)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		if err := vm.setLocal(valueSlot, next); err != nil {
			return false, vm.runtimeError(instruction, "%s", err.Error())
		}
		return true, nil
	}
	next, ok, err := iter.next()
	if err != nil {
		return false, vm.runtimeError(instruction, "%s", err.Error())
	}
	if !ok {
		return false, nil
	}
	if err := vm.setLocal(valueSlot, next); err != nil {
		return false, vm.runtimeError(instruction, "%s", err.Error())
	}
	return true, nil
}

// advanceUserIterator drives one step of a user-defined iterator
// (Instance with __next()/__done() methods). Returns (value, true)
// for an item, (nil, false) when the iterator reports done.
func (vm *VM) advanceUserIterator(instruction Instruction, iter *runtime.Instance) (runtime.Value, bool, error) {
	if iter == nil || iter.Class == nil {
		return nil, false, vm.runtimeError(instruction, "user iterator has no class")
	}
	classInfo, ok := vm.classInfo(iter.Class.Name)
	if !ok {
		return nil, false, vm.runtimeError(instruction, "%s is not an iterator", iter.Class.Name)
	}
	if _, hasDone := vm.lookupMethod(classInfo, "__done"); hasDone {
		doneResult, err := vm.CallMethod(iter, "__done", nil)
		if err != nil {
			return nil, false, vm.runtimeError(instruction, "%s.__done: %s", iter.Class.Name, err.Error())
		}
		doneBool, ok := doneResult.(runtime.Bool)
		if !ok {
			return nil, false, vm.runtimeError(instruction, "%s.__done must return bool, got %s", iter.Class.Name, doneResult.TypeName())
		}
		if doneBool.Value {
			return nil, false, nil
		}
	}
	if _, hasNext := vm.lookupMethod(classInfo, "__next"); !hasNext {
		return nil, false, vm.runtimeError(instruction, "%s is not an iterator: define __next()", iter.Class.Name)
	}
	value, err := vm.CallMethod(iter, "__next", nil)
	if err != nil {
		return nil, false, vm.runtimeError(instruction, "%s.__next: %s", iter.Class.Name, err.Error())
	}
	return value, true, nil
}

// withEnter pops the resource from the stack. If the resource is a
// class instance whose class defines __enter__(), it invokes the
// method and pushes its return value; otherwise it pushes the
// resource back so the binding (if any) receives it.
func (vm *VM) withEnter(instruction Instruction) error {
	value, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	instance, ok := value.(*runtime.Instance)
	if !ok {
		vm.push(value)
		return nil
	}
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		vm.push(value)
		return nil
	}
	indices, name, ok := vm.lookupDunder(classInfo, "__enter", "__enter__")
	if !ok || len(indices) == 0 {
		vm.push(value)
		return nil
	}
	result, err := vm.CallMethod(instance, name, nil)
	if err != nil {
		return vm.runtimeError(instruction, "with: %s: %s", name, err.Error())
	}
	vm.push(result)
	return nil
}

// withExit pops the resource from the stack and invokes
// __exit__() when the resource is a class instance whose class
// defines it. Otherwise it is a no-op: destructors are end-of-
// lifetime hooks, not block-scoped cleanup, and fire later via the
// program-exit sweep or an explicit `del`.
func (vm *VM) withExit(instruction Instruction) error {
	value, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	instance, ok := value.(*runtime.Instance)
	if !ok {
		return nil
	}
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		return nil
	}
	if indices, name, ok := vm.lookupDunder(classInfo, "__exit", "__exit__"); ok && len(indices) > 0 {
		if _, err := vm.CallMethod(instance, name, nil); err != nil {
			return vm.runtimeError(instruction, "with: %s: %s", name, err.Error())
		}
	}
	return nil
}

// execDel implements OpDel. Looks up the binding, fires the
// destructor if the value is a class instance whose class
// declares one and hasn't already been destroyed, unregisters
// the instance from the destructible-instance list, and clears
// the slot to Null{}.
func (vm *VM) execDel(instruction Instruction) error {
	if len(instruction.Operands) != 2 {
		return vm.runtimeError(instruction, "del instruction has invalid operands")
	}
	slot := instruction.Operands[0]
	kind := instruction.Operands[1]
	var value runtime.Value
	switch kind {
	case 0:
		v, err := vm.getLocal(slot)
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		value = v
	case 1:
		if slot < 0 || int(slot) >= len(vm.globals) {
			return vm.runtimeError(instruction, "del: global slot out of range")
		}
		value = vm.globals[slot].ToValue()
	default:
		return vm.runtimeError(instruction, "del: unknown binding kind %d", kind)
	}
	if instance, ok := value.(*runtime.Instance); ok && instance != nil && !instance.Destroyed && instance.Class != nil {
		classInfo, ok := vm.classInfo(instance.Class.Name)
		if ok && classInfo.DestructorIndex >= 0 {
			instance.Destroyed = true
			for i, tracked := range vm.destructibleInstances {
				if tracked == instance {
					vm.destructibleInstances = append(vm.destructibleInstances[:i], vm.destructibleInstances[i+1:]...)
					break
				}
			}
			if _, err := vm.CallFunctionRaw(classInfo.DestructorIndex, []runtime.Value{instance}); err != nil {
				return vm.runtimeError(instruction, "del: destructor: %s", err.Error())
			}
		}
	}
	switch kind {
	case 0:
		if err := vm.setLocal(slot, runtime.Null{}); err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
	case 1:
		vm.globals[slot] = runtime.VMValueNull
	}
	return nil
}

func (vm *VM) iterClose(instruction Instruction) error {
	if len(instruction.Operands) != 1 {
		return vm.runtimeError(instruction, "iterator close instruction has invalid operands")
	}
	value, err := vm.getLocal(instruction.Operands[0])
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	iter, ok := value.(*iteratorValue)
	if !ok || iter.generator == nil {
		return nil
	}
	iter.generator.Close()
	return nil
}

// typeAssert checks the top-of-stack value against the type string stored in the constant pool
// at operand[0]. Leaves the value on the stack. Throws a runtime error on mismatch.
func (vm *VM) typeAssert(instruction Instruction) error {
	if len(instruction.Operands) != 1 {
		return vm.runtimeError(instruction, "type assert instruction has invalid operands")
	}
	constIdx := instruction.Operands[0]
	if constIdx < 0 || int(constIdx) >= len(vm.chunk.Constants) {
		return vm.runtimeError(instruction, "type assert: constant index out of range")
	}
	typeStr, ok := vm.chunk.Constants[constIdx].(runtime.String)
	if !ok {
		return vm.runtimeError(instruction, "type assert: expected string constant")
	}
	value, err := vm.peek()
	if err != nil {
		return vm.runtimeError(instruction, "type assert: %s", err.Error())
	}
	spec, ok := vm.typeAssertSpecs[constIdx]
	if !ok {
		spec = vm.typeSpec(typeStr.Value)
	}
	if !vm.matchValueToTypeSpec(nil, value, spec) {
		suffix := vm.collectionMismatchSuffixStr(value, typeStr.Value)
		gotName := vm.descriptiveRuntimeTypeName(value)
		if suffix != "" {
			gotName = value.TypeName()
		}
		return vm.runtimeError(instruction, "type error: cannot assign %s to %s%s", gotName, typeStr.Value, suffix)
	}
	// Attach the reified element-type tag so reflect.typeBindings() and
	// `instanceof list<T>` see the declared bindings on the tagged value.
	// Mutate the top of the stack in place: List/Dict/Set are value
	// types, so we pop, tag the copy, and push the tagged version.
	if tagged, ok := vm.tagCollectionWithSpec(value, spec); ok {
		if _, err := vm.pop(); err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(tagged)
	}
	return nil
}

// tagCollectionWithSpec mirrors the evaluator's tagCollectionWithTypeRef
// for the VM's vmTypeSpec form. Returns (tagged, true) when the value is
// a List/Dict/Set whose tag should be set; otherwise (value, false).
func (vm *VM) tagCollectionWithSpec(value runtime.Value, spec vmTypeSpec) (runtime.Value, bool) {
	switch v := value.(type) {
	case runtime.List:
		if len(spec.args) >= 1 {
			tag := make([]string, len(spec.args))
			for i, a := range spec.args {
				tag[i] = a.base
			}
			v.ElementTypes = tag
			return v, true
		}
	case runtime.Set:
		if len(spec.args) >= 1 {
			v.ElementTypes = []string{spec.args[0].base}
			return v, true
		}
	case runtime.Dict:
		if len(spec.args) >= 2 {
			v.ElementTypes = []string{spec.args[0].base, spec.args[1].base}
			return v, true
		}
	}
	return value, false
}

// execRange implements OpRange: pops 2 or 3 integer values and pushes a
// list<int> covering the inclusive range. With 3 args the step is
// explicit; with 2 args the step defaults to +1 (or -1 when start > end).
func (vm *VM) execRange(instruction Instruction) error {
	if len(instruction.Operands) != 1 {
		return vm.runtimeError(instruction, "range expects argument count operand")
	}
	argc := int(instruction.Operands[0])
	if argc != 2 && argc != 3 {
		return vm.runtimeError(instruction, "range expects (start, end) or (start, end, step)")
	}
	var step *big.Int
	if argc == 3 {
		stepVal, err := vm.pop()
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		s, ok := native.IntValueToBigInt(stepVal)
		if !ok {
			return vm.runtimeError(instruction, "range step must be int")
		}
		step = s
	}
	endVal, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	endBig, ok := native.IntValueToBigInt(endVal)
	if !ok {
		return vm.runtimeError(instruction, "range end must be int")
	}
	startVal, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	startBig, ok := native.IntValueToBigInt(startVal)
	if !ok {
		return vm.runtimeError(instruction, "range start must be int")
	}
	if step == nil {
		if startBig.Cmp(endBig) > 0 {
			step = big.NewInt(-1)
		} else {
			step = big.NewInt(1)
		}
	}
	if step.Sign() == 0 {
		return vm.runtimeError(instruction, "range step cannot be zero")
	}
	var elements []runtime.Value
	current := new(big.Int).Set(startBig)
	for {
		cmp := current.Cmp(endBig)
		if step.Sign() > 0 && cmp > 0 {
			break
		}
		if step.Sign() < 0 && cmp < 0 {
			break
		}
		elements = append(elements, runtime.Int{Value: new(big.Int).Set(current)})
		current.Add(current, step)
	}
	vm.push(runtime.List{Elements: elements})
	return nil
}

func newRangeIterator(value runtime.Range) *iteratorValue {
	return &iteratorValue{rangeIter: &rangeIterator{
		current:   new(big.Int).Set(value.Start),
		end:       new(big.Int).Set(value.End),
		step:      new(big.Int).Set(value.Step),
		exclusive: value.Exclusive,
	}}
}

func (iter *iteratorValue) next() (runtime.Value, bool, error) {
	if iter.generator != nil {
		return iter.generator.Next()
	}
	if iter.rangeIter != nil {
		value, ok := iter.rangeIter.next()
		return value, ok, nil
	}
	if iter.userIter != nil {
		return nil, false, errUserIterPlaceholder
	}
	if iter.index >= len(iter.values) {
		return nil, false, nil
	}
	value := iter.values[iter.index]
	iter.index++
	return value, true, nil
}

// errUserIterPlaceholder signals that the iterator backs a user
// *Instance and must be driven via the VM (which holds the call
// dispatcher). Callers that observe this error read userIter and
// dispatch __done()/__next() themselves.
var errUserIterPlaceholder = errors.New("user iterator must be advanced via VM")

func (iter *rangeIterator) next() (runtime.Value, bool) {
	if !rangeContains(iter.current, iter.end, iter.step, iter.exclusive) {
		return nil, false
	}
	value := runtime.Int{Value: new(big.Int).Set(iter.current)}
	iter.current.Add(iter.current, iter.step)
	return value, true
}

func rangeContains(current *big.Int, end *big.Int, step *big.Int, exclusive bool) bool {
	cmp := current.Cmp(end)
	if step.Sign() > 0 {
		if exclusive {
			return cmp < 0
		}
		return cmp <= 0
	}
	if exclusive {
		return cmp > 0
	}
	return cmp >= 0
}

func (vm *VM) unpackList(instruction Instruction) error {
	if len(instruction.Operands) < 2 {
		return vm.runtimeError(instruction, "unpack instruction has invalid operands")
	}
	value, err := vm.getLocal(instruction.Operands[0])
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	list, ok := value.(runtime.List)
	if !ok {
		return vm.runtimeError(instruction, "cannot destructure %s into %d loop variables", value.TypeName(), len(instruction.Operands)-1)
	}
	if len(list.Elements) != len(instruction.Operands)-1 {
		return vm.runtimeError(instruction, "cannot destructure list of length %d into %d loop variables", len(list.Elements), len(instruction.Operands)-1)
	}
	for i, slot := range instruction.Operands[1:] {
		if err := vm.setLocal(slot, list.Elements[i]); err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
	}
	return nil
}

func (vm *VM) buildRange(instruction Instruction) error {
	stepValue, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	endValue, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	startValue, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	// Char-range fast path: 'a'..'z' (single-character string operands)
	// builds an eager list<string> of single-character entries rather
	// than a Range value. Step is ignored - char ranges always step by 1.
	if startStr, ok := startValue.(runtime.String); ok && len([]rune(startStr.Value)) == 1 {
		endStr, ok := endValue.(runtime.String)
		if !ok || len([]rune(endStr.Value)) != 1 {
			return vm.runtimeError(instruction, "char range end must be a single-character string")
		}
		exclusive := instruction.Operands[0] != 0
		startRune := []rune(startStr.Value)[0]
		endRune := []rune(endStr.Value)[0]
		var elements []runtime.Value
		if startRune <= endRune {
			for r := startRune; r <= endRune; r++ {
				if exclusive && r == endRune {
					break
				}
				elements = append(elements, runtime.String{Value: string(r)})
			}
		} else {
			for r := startRune; r >= endRune; r-- {
				if exclusive && r == endRune {
					break
				}
				elements = append(elements, runtime.String{Value: string(r)})
			}
		}
		vm.push(runtime.List{Elements: elements})
		return nil
	}
	start := big.NewInt(0)
	if _, ok := startValue.(runtime.Null); !ok {
		switch sv := startValue.(type) {
		case runtime.SmallInt:
			start = big.NewInt(sv.Value)
		case runtime.Int:
			start = sv.Value
		default:
			return vm.runtimeError(instruction, "range start must be int")
		}
	}
	var endBig *big.Int
	switch ev := endValue.(type) {
	case runtime.SmallInt:
		endBig = big.NewInt(ev.Value)
	case runtime.Int:
		endBig = ev.Value
	default:
		return vm.runtimeError(instruction, "range end must be int")
	}
	step := big.NewInt(1)
	if _, ok := stepValue.(runtime.Null); !ok {
		switch sv := stepValue.(type) {
		case runtime.SmallInt:
			step = big.NewInt(sv.Value)
		case runtime.Int:
			step = sv.Value
		default:
			return vm.runtimeError(instruction, "range step must be int")
		}
	}
	if step.Sign() == 0 {
		return vm.runtimeError(instruction, "range step cannot be zero")
	}
	vm.push(runtime.Range{
		Start:     new(big.Int).Set(start),
		End:       new(big.Int).Set(endBig),
		Exclusive: instruction.Operands[0] != 0,
		Step:      new(big.Int).Set(step),
	})
	return nil
}

func (vm *VM) popExitCode(instruction Instruction) (int, error) {
	value, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	switch v := value.(type) {
	case runtime.SmallInt:
		return int(v.Value), nil
	case runtime.Int:
		if !v.Value.IsInt64() {
			return 0, vm.runtimeError(instruction, "exit code is out of range")
		}
		return int(v.Value.Int64()), nil
	}
	return 0, vm.runtimeError(instruction, "sys.exit expects int")
}

// tailCall replaces the top frame's function with a new one. Used by
// OpTailCall for `return f(args)` in tail position. The current
// frame's returnIP, returnOverride, and locals buffer are reused
// (or replaced if LocalCount differs). Defers and exception handlers
// in the caller MUST be empty - the compiler enforces this.
func (vm *VM) tailCall(instruction Instruction) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.runtimeError(instruction, "tail call instruction has invalid operands")
	}
	index := instruction.Operands[0]
	argc := instruction.Operands[1]
	if index < 0 || int(index) >= len(vm.chunk.Functions) {
		return 0, vm.runtimeError(instruction, "tail call function index out of range")
	}
	function := vm.chunk.Functions[index]
	if len(vm.frames) == 0 {
		return 0, vm.runtimeError(instruction, "tail call without an active frame")
	}
	paramCount := len(function.ParamSlots)
	if int(argc) != paramCount {
		return 0, vm.runtimeError(instruction, "tail call arity mismatch: %s expects %d, got %d", function.Name, paramCount, argc)
	}
	n := len(vm.stack)
	if int(argc) > n {
		return 0, vm.runtimeError(instruction, "stack underflow")
	}
	stackArgs := vm.stack[n-int(argc) : n]
	if function.requiresParamValidation {
		typeParams := function.typeParamSet
		specs := function.paramTypeSpecs
		for i := 0; i < paramCount; i++ {
			pt := function.ParamTypes[i]
			if pt == "" {
				continue
			}
			argKind := stackArgs[i].Kind
			if i < len(specs) && specs[i].kind == vmTypeInt && argKind == runtime.VMKindSmallInt {
				continue
			}
			var spec vmTypeSpec
			if i < len(specs) && specs[i].raw != "" {
				spec = specs[i]
			} else {
				spec = vm.typeSpec(pt)
			}
			if !vm.matchVMValueToTypeSpec(typeParams, stackArgs[i], spec) {
				return 0, vm.runtimeError(instruction, "%s: argument %d expected %s, got %s", function.Name, i+1, pt, stackArgs[i].ToValue().TypeName())
			}
		}
	}
	args := make([]runtime.VMValue, paramCount)
	copy(args, stackArgs)
	vm.stack = vm.stack[:n-int(argc)]
	// Reuse the existing top frame: keep returnIP, returnOverride.
	// Replace functionName / callLine for stack traces. Reset other
	// fields to defaults for the new function.
	frame := &vm.frames[len(vm.frames)-1]
	frame.functionName = function.Name
	frame.callLine = instruction.Line
	frame.negateReturn = false
	frame.isErrorClass = false
	frame.isImmutableClass = false
	frame.isDestructibleConstructor = false
	frame.typeBindings = nil
	frame.generator = nil
	frame.generatorDone = nil
	vm.pendingTypeBindings = nil
	// Release the caller-function's own locals (the frame was reusing
	// them; the eventual OpReturn will use frame.locals, which still
	// points to the original caller's locals, to restore on pop). Then
	// allocate a fresh slice for the callee. function.SharesParentFrame
	frame.shared = false
	vm.popLocalsStackFrame(frame)
	vm.pushLocalsStackFrame(frame, int(function.LocalCount), function.SharesParentFrame)
	bp := frame.basePointer
	for i := 0; i < paramCount; i++ {
		vm.localsStack[bp+int(function.ParamSlots[i])] = args[i]
	}
	frameDepth := len(vm.frames)
	if frameDepth < cap(vm.defers) {
		vm.defers = vm.defers[:frameDepth+1]
		vm.defers[frameDepth] = vm.defers[frameDepth][:0]
	}
	return int(function.Entry) - 1, nil
}

func (vm *VM) call(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.runtimeError(instruction, "call instruction has invalid operands")
	}
	index := instruction.Operands[0]
	argc := instruction.Operands[1]
	if index < 0 || int(index) >= len(vm.chunk.Functions) {
		return 0, vm.runtimeError(instruction, "function index out of range")
	}
	function := &vm.chunk.Functions[index]
	if !vm.requiresCallSitePolymorphism {
		n := len(vm.stack)
		if int(argc) > n {
			return 0, vm.runtimeError(instruction, "stack underflow")
		}
		stackArgs := vm.stack[n-int(argc) : n]
		vm.stack = vm.stack[:n-int(argc)]
		return vm.startFunctionVMValue(instruction, ip, function, stackArgs, nil)
	}
	decorated, hasDecorated := vm.decoratedFuncs[index]
	if !hasDecorated || vm.rawFunctionCalls[index] {
		if !function.Async || vm.syncMode {
			if !function.IsGenerator || vm.generatorExecution {
				n := len(vm.stack)
				if int(argc) > n {
					return 0, vm.runtimeError(instruction, "stack underflow")
				}
				stackArgs := vm.stack[n-int(argc) : n]
				vm.stack = vm.stack[:n-int(argc)]
				return vm.startFunctionVMValue(instruction, ip, function, stackArgs, nil)
			}
		}
	}
	// Async / generator / callable branches let args escape into a
	// worker goroutine or stored task, so they keep the raw make().
	// The terminal startFunction path copies args into locals before
	// returning, so it can ride the call-args pool.
	asyncEscape := function.Async && !vm.syncMode
	generatorEscape := function.IsGenerator && !vm.generatorExecution
	callableEscape := hasDecorated && !vm.rawFunctionCalls[index]
	canPool := !asyncEscape && !generatorEscape && !callableEscape
	var args []runtime.Value
	if canPool {
		args = vm.takeCallArgsBuffer(int(argc))
	} else {
		args = make([]runtime.Value, argc)
	}
	for i := int(argc) - 1; i >= 0; i-- {
		value, err := vm.pop()
		if err != nil {
			if canPool {
				vm.releaseCallArgsBuffer(args)
			}
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		args[i] = value
	}
	if hasDecorated && !vm.rawFunctionCalls[index] {
		if function.Async && !vm.syncMode {
			task := vm.startAsyncCallable(decorated, args)
			vm.push(task)
			return ip, nil
		}
		result, err := vm.callCallable(decorated, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if function.Async && !vm.syncMode {
		task := vm.startAsyncFunction(index, args)
		vm.push(task)
		return ip, nil
	}
	if function.IsGenerator && !vm.generatorExecution {
		vm.push(vm.lazyGenerator(index, args))
		return ip, nil
	}
	nextIP, err := vm.startFunction(instruction, ip, function, args, nil)
	vm.releaseCallArgsBuffer(args)
	return nextIP, err
}

func (vm *VM) startAsyncFunction(index int64, args []runtime.Value) *runtime.Task {
	task := runtime.NewTask()
	// Snapshot parent VM state into a private worker before spawning the
	// goroutine; the worker touches no parent fields after this point.
	worker := vm.spawnAsyncWorker()
	go func() {
		result, err := worker.CallFunction(index, args)
		task.Complete(result, err)
	}()
	return task
}

// spawnAsyncWorker constructs a worker VM holding snapshots of the parent's
// mutable state (globals, decorator maps, etc.). The caller must spawn its
// goroutine AFTER this returns so the snapshot copies are happen-before
// the worker's reads.
func (parent *VM) spawnAsyncWorker() *VM {
	worker := NewVMWithModuleLoader(parent.chunk, parent.stdout, parent.moduleLoader)
	worker.restoreGlobalsVM(parent.globals)
	worker.SetModuleName(parent.moduleName)
	worker.SetModulePaths(parent.modulePaths)
	if parent.statefulNative != nil {
		worker.SetStatefulNativeCaller(parent.statefulNative)
	}
	worker.syncMode = true
	worker.forwardThis = parent.forwardThis
	worker.decoratedFuncs = copyRuntimeValueMap(parent.decoratedFuncs)
	worker.decoratedClasses = copyRuntimeValueMap(parent.decoratedClasses)
	worker.decoratorsApplied = true
	worker.rawFunctionCalls = copyBoolMap(parent.rawFunctionCalls)
	worker.methodReceiverFuncs = copyBoolMap(parent.methodReceiverFuncs)
	return worker
}

func (vm *VM) lazyGenerator(index int64, args []runtime.Value) *runtime.Generator {
	items := make(chan vmGeneratorItem)
	doneCh := make(chan struct{})
	stop := sync.Once{}
	start := sync.Once{}
	closed := false
	var pendingErr error
	return runtime.NewClosableGenerator(func() (runtime.Value, bool, error) {
		start.Do(func() {
			callVM := vm.spawnAsyncWorker()
			callVM.generatorExecution = true
			callVM.generatorYield = items
			callVM.generatorDone = doneCh
			go func() {
				defer close(items)
				if _, err := callVM.CallFunction(index, args); err != nil {
					select {
					case items <- vmGeneratorItem{err: err}:
					case <-doneCh:
					}
				}
			}()
		})
		if closed {
			return nil, false, pendingErr
		}
		item, ok := <-items
		if !ok {
			closed = true
			return nil, false, nil
		}
		if item.err != nil {
			closed = true
			pendingErr = item.err
			return nil, false, item.err
		}
		return item.value, true, nil
	}, func() {
		stop.Do(func() {
			close(doneCh)
		})
	})
}

func (vm *VM) lazyClosureGenerator(closure runtime.BytecodeClosure, args []runtime.Value) *runtime.Generator {
	items := make(chan vmGeneratorItem)
	doneCh := make(chan struct{})
	stop := sync.Once{}
	start := sync.Once{}
	closed := false
	var pendingErr error
	return runtime.NewClosableGenerator(func() (runtime.Value, bool, error) {
		start.Do(func() {
			callVM := vm.spawnAsyncWorker()
			callVM.generatorExecution = true
			callVM.generatorYield = items
			callVM.generatorDone = doneCh
			go func() {
				defer close(items)
				if _, err := callVM.callCallable(closure, args); err != nil {
					select {
					case items <- vmGeneratorItem{err: err}:
					case <-doneCh:
					}
				}
			}()
		})
		if closed {
			return nil, false, pendingErr
		}
		item, ok := <-items
		if !ok {
			closed = true
			return nil, false, nil
		}
		if item.err != nil {
			closed = true
			pendingErr = item.err
			return nil, false, item.err
		}
		return item.value, true, nil
	}, func() {
		stop.Do(func() {
			close(doneCh)
		})
	})
}

func (vm *VM) startAsyncCallable(fn runtime.Value, args []runtime.Value) *runtime.Task {
	return vm.startAsyncCallableWithForwardThis(fn, args, nil)
}

func (vm *VM) startAsyncCallableWithForwardThis(fn runtime.Value, args []runtime.Value, receiver *runtime.Instance) *runtime.Task {
	task := runtime.NewTask()
	// Snapshot parent state into a private worker; the goroutine touches
	// only the worker, not the parent VM.
	worker := vm.spawnAsyncWorker()
	if receiver != nil {
		worker.forwardThis = receiver
	}
	go func() {
		result, err := worker.callCallable(fn, args)
		task.Complete(result, err)
	}()
	return task
}

func (vm *VM) callCallableWithForwardThis(fn runtime.Value, args []runtime.Value, receiver *runtime.Instance) (runtime.Value, error) {
	previous := vm.forwardThis
	vm.forwardThis = receiver
	defer func() {
		vm.forwardThis = previous
	}()
	return vm.callCallable(fn, args)
}

func (vm *VM) startFunction(instruction Instruction, ip int, function *FunctionInfo, provided []runtime.Value, returnOverride runtime.Value) (int, error) {
	return vm.startFunctionWithValidation(instruction, ip, function, provided, returnOverride, true)
}

// startFunctionWithBindings enters a function body with an inherited
// type-binding map planted into the new call frame. Used by the
// closure call path so that a lambda or named-generic-function value
// can resolve type-parameter names from the call site of its
// enclosing generic function.
func (vm *VM) startFunctionWithBindings(instruction Instruction, ip int, function *FunctionInfo, provided []runtime.Value, returnOverride runtime.Value, inheritedBindings map[string]string) (int, error) {
	prev := vm.pendingTypeBindings
	vm.pendingTypeBindings = inheritedBindings
	nextIP, err := vm.startFunctionWithValidation(instruction, ip, function, provided, returnOverride, true)
	vm.pendingTypeBindings = prev
	return nextIP, err
}

// startFunctionVMValue is the fast-path call entry that operates on
// VMValue arguments directly. The caller passes a slice over the VM
// stack and the number of args (argc); the slice is consumed but not
// retained beyond this call. No []runtime.Value allocation or per-arg
// ToValue conversion happens on this path.
//
// The fast path covers the common case: no variadic, no defaults
// needed, no generics inference required, all arg types either `any`
// or match the parameter spec inline. Anything else falls back to the
// interface-form path by materialising args at the boundary, paying
// the allocation cost only on the slow path.
func (vm *VM) startFunctionVMValue(instruction Instruction, ip int, function *FunctionInfo, stackArgs []runtime.VMValue, returnOverride runtime.Value) (int, error) {
	maxDepth := vm.maxCallDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxCallDepth
	}
	if len(vm.frames) >= maxDepth {
		return 0, vm.runtimeError(instruction, "maximum call depth exceeded (%d)", maxDepth)
	}
	argc := len(stackArgs)
	paramCount := len(function.ParamSlots)

	// Fast path requirements: fixed arity, no variadic packing, no
	// default-arg fill, returnOverride nil. The arity check itself runs
	// inline so the fast path can stay branch-light.
	fastPath := !function.Variadic && argc == paramCount && returnOverride == nil
	if !fastPath {
		// Materialise the args as []runtime.Value and let the legacy
		// path handle defaults/variadic/etc. startFunctionWithValidation
		// copies into locals before returning so the args slice is
		// safe to recycle once it returns.
		args := vm.takeCallArgsBuffer(argc)
		for i, a := range stackArgs {
			args[i] = a.ToValue()
		}
		nextIP, err := vm.startFunctionWithValidation(instruction, ip, function, args, returnOverride, true)
		vm.releaseCallArgsBuffer(args)
		return nextIP, err
	}

	// Type validation, inline against VMValues. Skips the spec lookup
	// when a typed-int param meets a SmallInt arg (the dominant case).
	if function.requiresParamValidation {
		typeParams := function.typeParamSet
		specs := function.paramTypeSpecs
		for i := 0; i < paramCount; i++ {
			pt := function.ParamTypes[i]
			if pt == "" {
				continue
			}
			argKind := stackArgs[i].Kind
			// Fast path: typed-int param, SmallInt arg.
			if i < len(specs) && specs[i].kind == vmTypeInt && argKind == runtime.VMKindSmallInt {
				continue
			}
			var spec vmTypeSpec
			if i < len(specs) && specs[i].raw != "" {
				spec = specs[i]
			} else {
				spec = vm.typeSpec(pt)
			}
			if !vm.matchVMValueToTypeSpec(typeParams, stackArgs[i], spec) {
				// Fall back to the slow path so the existing error
				// reporting (which formats runtime.Value descriptions)
				// runs uniformly.
				args := vm.takeCallArgsBuffer(argc)
				for j, a := range stackArgs {
					args[j] = a.ToValue()
				}
				nextIP, err := vm.startFunctionWithValidation(instruction, ip, function, args, returnOverride, true)
				vm.releaseCallArgsBuffer(args)
				return nextIP, err
			}
		}
	}

	// Push the frame in place.
	if len(vm.frames) == cap(vm.frames) {
		grown := make([]callFrame, len(vm.frames), cap(vm.frames)*2)
		copy(grown, vm.frames)
		vm.frames = grown
	}
	vm.frames = vm.frames[:len(vm.frames)+1]
	frame := &vm.frames[len(vm.frames)-1]
	frame.returnIP = ip
	frame.returnOverride = nil
	frame.functionName = function.Name
	frame.callLine = instruction.Line
	frame.negateReturn = false
	frame.isErrorClass = false
	frame.isImmutableClass = false
	frame.isDestructibleConstructor = false
	frame.shared = function.SharesParentFrame
	if function.IsGenerator && vm.generatorExecution {
		frame.generator = vm.generatorYield
		frame.generatorDone = vm.generatorDone
	} else {
		frame.generator = nil
		frame.generatorDone = nil
	}
	frame.typeBindings = nil
	frameDepth := len(vm.frames)
	if frameDepth < cap(vm.defers) {
		vm.defers = vm.defers[:frameDepth+1]
		vm.defers[frameDepth] = vm.defers[frameDepth][:0]
	} else {
		vm.defers = append(vm.defers, nil)
	}
	// Save parent's locals slice handle (no copy) and either reuse it
	// (nested function statement) or allocate a fresh frame slice for
	// the callee. Fresh slice avoids the per-call snapshot copy that
	// previously dominated recursion overhead for small functions.
	vm.pushLocalsStackFrame(frame, int(function.LocalCount), function.SharesParentFrame)
	bp := frame.basePointer
	for i := 0; i < paramCount; i++ {
		vm.localsStack[bp+int(function.ParamSlots[i])] = stackArgs[i]
	}
	if len(function.TypeParameters) > 0 && len(function.ParamTypes) > 0 {
		typeBindings := vm.inferTypeBindingsFromLocals(function)
		if err := vm.checkTypeParamConstraints(instruction, function, typeBindings); err != nil {
			return 0, err
		}
	}
	if vm.forwardThis != nil {
		vm.inheritInstanceTypeBindings(vm.forwardThis)
	}
	return int(function.Entry) - 1, nil
}

// matchVMValueToTypeSpec is the VMValue-aware variant of
// matchValueToTypeSpec. It avoids the ToValue() round-trip for the
// common primitive cases.
func (vm *VM) matchVMValueToTypeSpec(typeParams map[string]bool, value runtime.VMValue, spec vmTypeSpec) bool {
	if typeParams[spec.baseLower] {
		return true
	}
	if spec.kind == vmTypeUnion || spec.kind == vmTypeIntersection {
		return vm.matchValueToTypeSpec(typeParams, value.ToValue(), spec)
	}
	if value.Kind == runtime.VMKindUnset || value.Kind == runtime.VMKindNull {
		return spec.nullable
	}
	switch spec.kind {
	case vmTypeAny:
		return true
	case vmTypeInt:
		if value.Kind == runtime.VMKindSmallInt {
			return true
		}
		if value.Kind == runtime.VMKindBoxed {
			if _, ok := value.Boxed.(runtime.Int); ok {
				return true
			}
		}
		return false
	case vmTypeBool:
		return value.Kind == runtime.VMKindBool
	case vmTypeFloat:
		return value.Kind == runtime.VMKindFloat
	}
	// Fall back to the interface-form check for less common types.
	return vm.matchValueToTypeSpec(typeParams, value.ToValue(), spec)
}

// inferTypeBindingsFromLocals replicates the binding-inference loop in
// startFunctionWithValidation but reads from vm.localsStack (already
// set up by startFunctionVMValue via the new frame's basePointer)
// rather than a []runtime.Value arg list. Returns the inferred
// bindings so the caller can run constraint checks.
func (vm *VM) inferTypeBindingsFromLocals(function *FunctionInfo) map[string]string {
	typeParamSet := function.typeParamSet
	typeBindings := map[string]string{}
	// Seed with any explicit `<TypeArgs>` planted by OpPlantCallTypeBindings
	// before the matching OpCall. Inference's "skip if already exists" checks
	// below then leave these alone, giving explicit args strict priority
	// over types inferred from argument values. The pending map is consumed
	// here so a subsequent call without explicit args doesn't inherit them.
	if len(vm.pendingTypeBindings) > 0 {
		for k, v := range vm.pendingTypeBindings {
			if typeParamSet[strings.ToLower(k)] {
				typeBindings[k] = v
			}
		}
		vm.pendingTypeBindings = nil
	}
	for i, paramType := range function.ParamTypes {
		if i >= len(function.ParamSlots) {
			break
		}
		var spec vmTypeSpec
		if i < len(function.paramTypeSpecs) && function.paramTypeSpecs[i].raw != "" {
			spec = function.paramTypeSpecs[i]
		} else {
			spec = vm.typeSpec(paramType)
		}
		if len(spec.args) > 0 {
			slot := function.ParamSlots[i]
			v, err := vm.getLocal(slot)
			if err != nil || v == nil {
				continue
			}
			vm.inferGenericBindingsFromSpec(spec, v, typeParamSet, typeBindings)
			continue
		}
		if typeParamSet[strings.ToLower(paramType)] {
			slot := function.ParamSlots[i]
			if v, err := vm.getLocal(slot); err == nil && v != nil {
				if _, exists := typeBindings[paramType]; !exists {
					typeBindings[paramType] = v.TypeName()
				}
			}
		}
	}
	if len(typeBindings) > 0 {
		vm.frames[len(vm.frames)-1].typeBindings = typeBindings
	}
	return typeBindings
}

// inferGenericBindingsFromSpec walks a parameter's type-spec tree
// against the concrete arg value to discover bindings for any leaf
// type-parameter references. Recurses into nested container shapes so
// `list<dict<K, V>>` populates both K and V from a single arg, not
// just the immediate level. Direct type-param leaves (e.g. just `T`)
// fall through to the caller's same-level path; this helper only
// fires when the spec has args (a generic container shape). Caller
// already null-checked v.
func (vm *VM) inferGenericBindingsFromSpec(spec vmTypeSpec, v runtime.Value, typeParamSet map[string]bool, typeBindings map[string]string) {
	if len(spec.args) == 0 {
		return
	}
	switch spec.baseLower {
	case "list":
		list, ok := v.(runtime.List)
		if !ok || len(list.Elements) == 0 {
			return
		}
		vm.bindOrRecurse(spec.args[0], list.Elements[0], typeParamSet, typeBindings)
	case "set":
		set, ok := v.(runtime.Set)
		if !ok || len(set.Elements) == 0 {
			return
		}
		for _, entry := range set.Elements {
			vm.bindOrRecurse(spec.args[0], entry.Value, typeParamSet, typeBindings)
			break
		}
	case "dict":
		if len(spec.args) != 2 {
			return
		}
		d, ok := v.(runtime.Dict)
		if !ok || len(d.Entries) == 0 {
			return
		}
		for _, entry := range d.Entries {
			vm.bindOrRecurse(spec.args[0], entry.Key, typeParamSet, typeBindings)
			vm.bindOrRecurse(spec.args[1], entry.Value, typeParamSet, typeBindings)
			break
		}
	}
}

// bindOrRecurse is the leaf step of inferGenericBindingsFromSpec.
// When `spec` is a bare type-param reference, record the binding from
// the concrete value. Otherwise it is a nested container; recurse.
func (vm *VM) bindOrRecurse(spec vmTypeSpec, v runtime.Value, typeParamSet map[string]bool, typeBindings map[string]string) {
	if len(spec.args) == 0 {
		if !typeParamSet[strings.ToLower(spec.base)] {
			return
		}
		if v == nil {
			return
		}
		if _, exists := typeBindings[spec.base]; exists {
			return
		}
		typeBindings[spec.base] = v.TypeName()
		return
	}
	vm.inferGenericBindingsFromSpec(spec, v, typeParamSet, typeBindings)
}

func (vm *VM) startPrevalidatedFunction(instruction Instruction, ip int, function *FunctionInfo, provided []runtime.Value, returnOverride runtime.Value) (int, error) {
	return vm.startFunctionWithValidation(instruction, ip, function, provided, returnOverride, false)
}

func (vm *VM) startFunctionWithValidation(instruction Instruction, ip int, function *FunctionInfo, provided []runtime.Value, returnOverride runtime.Value, validateTypes bool) (int, error) {
	maxDepth := vm.maxCallDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxCallDepth
	}
	if len(vm.frames) >= maxDepth {
		return 0, vm.runtimeError(instruction, "maximum call depth exceeded (%d)", maxDepth)
	}
	argc := len(provided)
	if function.Variadic && len(function.ParamSlots) > 0 {
		variadicIndex := len(function.ParamSlots) - 1
		if argc >= variadicIndex {
			variadicElements := make([]runtime.Value, argc-variadicIndex)
			copy(variadicElements, provided[variadicIndex:])
			newProvided := make([]runtime.Value, variadicIndex+1)
			copy(newProvided, provided[:variadicIndex])
			newProvided[variadicIndex] = runtime.List{Elements: variadicElements}
			provided = newProvided
			argc = len(provided)
		}
	} else if argc > len(function.ParamSlots) {
		return 0, vm.runtimeError(instruction, "%s expects at most %d args, got %d", function.Name, len(function.ParamSlots), argc)
	}
	required := len(function.ParamSlots)
	for required > 0 && required <= len(function.DefaultConstants) && function.DefaultConstants[required-1] >= 0 {
		required--
	}
	if function.Variadic && required == len(function.ParamSlots) && len(function.ParamSlots) > 0 {
		required--
	}
	if argc < required {
		return 0, vm.runtimeError(instruction, "%s expects at least %d args, got %d", function.Name, required, argc)
	}
	args := provided
	if function.Variadic || argc != len(function.ParamSlots) {
		args = make([]runtime.Value, len(function.ParamSlots))
		copy(args, provided)
		if function.Variadic && len(function.ParamSlots) > 0 {
			variadicIndex := len(function.ParamSlots) - 1
			if args[variadicIndex] == nil {
				args[variadicIndex] = runtime.List{Elements: nil}
			}
		}
		for i := argc; i < len(args); i++ {
			if i >= len(function.DefaultConstants) || function.DefaultConstants[i] < 0 {
				return 0, vm.runtimeError(instruction, "%s missing argument %d", function.Name, i+1)
			}
			defaultIndex := function.DefaultConstants[i]
			if defaultIndex < 0 || int(defaultIndex) >= len(vm.chunk.Constants) {
				return 0, vm.runtimeError(instruction, "default argument constant out of range")
			}
			args[i] = cloneContainerDefault(vm.chunk.Constants[defaultIndex])
		}
	}
	if validateTypes && function.requiresParamValidation {
		// Enforce parameter type annotations (skip the bundled variadic slot).
		typeParams := function.typeParamSet
		inherited := vm.pendingTypeBindings
		for i, arg := range args {
			if function.Variadic && i == len(function.ParamSlots)-1 {
				break
			}
			if arg == nil || i >= len(function.ParamTypes) || function.ParamTypes[i] == "" {
				continue
			}
			matches := false
			if i < len(function.paramTypeSpecs) && function.paramTypeSpecs[i].raw != "" {
				matches = vm.matchValueToTypeSpecWith(typeParams, inherited, arg, function.paramTypeSpecs[i])
			} else {
				matches = vm.matchValueToTypeStrWith(typeParams, inherited, arg, function.ParamTypes[i])
			}
			if !matches {
				paramName := ""
				if i < len(function.ParamNames) {
					paramName = function.ParamNames[i]
				}
				suffix := vm.collectionMismatchSuffixStr(arg, function.ParamTypes[i])
				gotName := vm.descriptiveRuntimeTypeName(arg)
				if suffix != "" {
					gotName = arg.TypeName()
				}
				var msg string
				if paramName != "" {
					msg = fmt.Sprintf("%s expects %s for parameter '%s', got %s%s", function.Name, function.ParamTypes[i], paramName, gotName, suffix)
				} else {
					msg = fmt.Sprintf("%s expects %s, got %s%s", function.Name, function.ParamTypes[i], gotName, suffix)
				}
				return vm.throwTyped(instruction, ip, "RuntimeError", msg)
			}
		}
	}
	// Construct the frame in place rather than building a callFrame value and
	// letting append copy the whole struct. For deep call stacks (fib's
	// ~243k calls) this avoids a duffcopy per push. The pop path zeroes the
	// fields that hold references when the frame is released, so we only
	// need to assign the active fields here.
	if len(vm.frames) == cap(vm.frames) {
		grown := make([]callFrame, len(vm.frames), cap(vm.frames)*2)
		copy(grown, vm.frames)
		vm.frames = grown
	}
	vm.frames = vm.frames[:len(vm.frames)+1]
	frame := &vm.frames[len(vm.frames)-1]
	frame.returnIP = ip
	frame.returnOverride = returnOverride
	frame.functionName = function.Name
	frame.callLine = instruction.Line
	frame.negateReturn = false
	frame.isErrorClass = false
	frame.isImmutableClass = false
	frame.isDestructibleConstructor = false
	frame.shared = function.SharesParentFrame
	if function.IsGenerator && vm.generatorExecution {
		frame.generator = vm.generatorYield
		frame.generatorDone = vm.generatorDone
	} else {
		frame.generator = nil
		frame.generatorDone = nil
	}
	frame.typeBindings = nil
	// Reuse the backing array slot for this frame's defers rather than
	// allocating a new inner slice each call.
	frameDepth := len(vm.frames)
	if frameDepth < cap(vm.defers) {
		vm.defers = vm.defers[:frameDepth+1]
		vm.defers[frameDepth] = vm.defers[frameDepth][:0]
	} else {
		vm.defers = append(vm.defers, nil)
	}
	// Save the parent's locals slice handle (no copy) and either reuse
	// it for a nested function statement or allocate a fresh frame
	// slice for any other callee. This replaces the previous
	// snapshotLocals() deep copy.
	vm.pushLocalsStackFrame(frame, int(function.LocalCount), function.SharesParentFrame)
	for i, slot := range function.ParamSlots {
		if err := vm.setLocal(slot, args[i]); err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
	}
	// Infer type parameter bindings from param types and the bound local values.
	if len(function.TypeParameters) > 0 && len(function.ParamTypes) > 0 {
		typeParamSet := function.typeParamSet
		typeBindings := map[string]string{}
		for i, paramType := range function.ParamTypes {
			if i >= len(function.ParamSlots) {
				break
			}
			var spec vmTypeSpec
			if i < len(function.paramTypeSpecs) && function.paramTypeSpecs[i].raw != "" {
				spec = function.paramTypeSpecs[i]
			} else {
				spec = vm.typeSpec(paramType)
			}
			if len(spec.args) > 0 {
				slot := function.ParamSlots[i]
				if v, err := vm.getLocal(slot); err == nil && v != nil {
					vm.inferGenericBindingsFromSpec(spec, v, typeParamSet, typeBindings)
				}
				continue
			}
			// Direct T parameter.
			if typeParamSet[strings.ToLower(paramType)] {
				slot := function.ParamSlots[i]
				if v, err := vm.getLocal(slot); err == nil && v != nil {
					if _, exists := typeBindings[paramType]; !exists {
						typeBindings[paramType] = v.TypeName()
					}
				}
			}
		}
		if len(typeBindings) > 0 {
			vm.frames[len(vm.frames)-1].typeBindings = typeBindings
		}
		if err := vm.checkTypeParamConstraints(instruction, function, typeBindings); err != nil {
			return 0, err
		}
	}
	// If called with a forwarded receiver (e.g. via a callable decorator), inherit
	// its type bindings so that instanceof T checks work inside decorated methods.
	if vm.forwardThis != nil {
		vm.inheritInstanceTypeBindings(vm.forwardThis)
	}
	// If this call inherited type bindings from a closure value (lambda
	// or generic-function-as-reference captured from an outer generic
	// frame) OR from OpPlantCallTypeBindings staging explicit `<TypeArgs>`,
	// merge them into the new frame so the body's instanceof T checks and
	// inner OpMakeClosure captures see the correct bindings. Pending
	// bindings are consumed here; the closure-inheritance path uses
	// save/restore semantics around its caller so the restore still sees
	// the original prev value regardless of this clear.
	if len(vm.pendingTypeBindings) > 0 && len(vm.frames) > 0 {
		frame := &vm.frames[len(vm.frames)-1]
		if frame.typeBindings == nil {
			frame.typeBindings = map[string]string{}
		}
		for k, v := range vm.pendingTypeBindings {
			if _, exists := frame.typeBindings[k]; !exists {
				frame.typeBindings[k] = v
			}
		}
		vm.pendingTypeBindings = nil
	}
	return int(function.Entry) - 1, nil
}

func (vm *VM) startClosureFunction(instruction Instruction, ip int, closure runtime.BytecodeClosure, args []runtime.Value) (int, error) {
	if int(closure.FunctionIndex) >= len(vm.chunk.Functions) {
		return 0, vm.runtimeError(instruction, "closure function index out of range")
	}
	function := &vm.chunk.Functions[closure.FunctionIndex]
	if function.IsGenerator && !vm.generatorExecution {
		vm.push(vm.lazyClosureGenerator(closure, args))
		return ip, nil
	}
	upvalueCount := int(function.UpvalueCount)
	provided := make([]runtime.Value, upvalueCount+len(args))
	copy(provided[:upvalueCount], closure.Upvalues)
	copy(provided[upvalueCount:], args)
	if len(closure.TypeBindings) > 0 {
		return vm.startFunctionWithBindings(instruction, ip, function, provided, nil, closure.TypeBindings)
	}
	return vm.startFunction(instruction, ip, function, provided, nil)
}

func (vm *VM) nativeCall(instruction Instruction) error {
	if len(instruction.Operands) != 2 {
		return vm.runtimeError(instruction, "native call instruction has invalid operands")
	}
	nameIndex := instruction.Operands[0]
	argc := instruction.Operands[1]
	if nameIndex < 0 || int(nameIndex) >= len(vm.chunk.Constants) {
		return vm.runtimeError(instruction, "native call name out of range")
	}
	if int(nameIndex) < len(vm.nativeCache) {
		if fn := vm.nativeCache[nameIndex]; fn != nil {
			args := vm.takeCallArgsBuffer(int(argc))
			for i := int(argc) - 1; i >= 0; i-- {
				value, err := vm.pop()
				if err != nil {
					vm.releaseCallArgsBuffer(args)
					return vm.runtimeError(instruction, "%s", err.Error())
				}
				args[i] = value
			}
			result, err := fn(args)
			vm.releaseCallArgsBuffer(args)
			if err != nil {
				return recoverableNativeError{err: err}
			}
			vm.push(result)
			return nil
		}
	}
	name, ok := vm.chunk.Constants[nameIndex].(runtime.String)
	if !ok {
		return vm.runtimeError(instruction, "native call name must be string")
	}
	args := make([]runtime.Value, argc)
	for i := int(argc) - 1; i >= 0; i-- {
		value, err := vm.pop()
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		args[i] = value
	}
	if vm.statefulNative == nil && name.Value != "errors.is" {
		if module, function, ok := strings.Cut(name.Value, "."); ok && !isStatefulNativeCall(module, function) {
			_ = module
			_ = function
			if fn := vm.natives.LookupKey(name.Value); fn != nil && int(nameIndex) < len(vm.nativeCache) {
				vm.nativeCache[nameIndex] = fn
			}
		}
	}
	result, err := vm.evalNativeCall(name.Value, args)
	if err != nil {
		return recoverableNativeError{err: err}
	}
	vm.push(result)
	return nil
}

func (vm *VM) nativeCallNamed(instruction Instruction) error {
	if len(instruction.Operands) < 2 {
		return vm.runtimeError(instruction, "named native call instruction has invalid operands")
	}
	nameIndex := instruction.Operands[0]
	argc := instruction.Operands[1]
	if argc < 0 || len(instruction.Operands) != int(argc)+2 {
		return vm.runtimeError(instruction, "named native call argument metadata mismatch")
	}
	if nameIndex < 0 || int(nameIndex) >= len(vm.chunk.Constants) {
		return vm.runtimeError(instruction, "native call name out of range")
	}
	name, ok := vm.chunk.Constants[nameIndex].(runtime.String)
	if !ok {
		return vm.runtimeError(instruction, "native call name must be string")
	}
	args := make([]runtime.Value, argc)
	for i := int(argc) - 1; i >= 0; i-- {
		value, err := vm.pop()
		if err != nil {
			return vm.runtimeError(instruction, "%s", err.Error())
		}
		args[i] = value
	}
	names := make([]string, argc)
	for i := int64(0); i < argc; i++ {
		argName, err := vm.constantStringAt(instruction, instruction.Operands[i+2], "argument name constant must be string")
		if err != nil {
			return err
		}
		names[i] = argName
	}
	result, err := vm.evalNativeCallWithNames(name.Value, args, names)
	if err != nil {
		return recoverableNativeError{err: err}
	}
	vm.push(result)
	return nil
}

func (vm *VM) constructClass(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.runtimeError(instruction, "construct class instruction has invalid operands")
	}
	classIndex := instruction.Operands[0]
	argc := int(instruction.Operands[1])
	if classIndex < 0 || int(classIndex) >= len(vm.chunk.Classes) {
		return 0, vm.runtimeError(instruction, "class index out of range")
	}
	args := make([]runtime.Value, argc)
	for i := argc - 1; i >= 0; i-- {
		value, err := vm.pop()
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		args[i] = value
	}
	return vm.constructClassWithArgs(instruction, ip, classIndex, args, false)
}

func (vm *VM) constructClassWithArgs(instruction Instruction, ip int, classIndex int64, args []runtime.Value, raw bool) (int, error) {
	classInfo := vm.chunk.Classes[classIndex]
	if !raw {
		if decorated, ok := vm.decoratedClasses[classIndex]; ok {
			switch dec := decorated.(type) {
			case runtime.BytecodeClass:
				if dec.Index != classIndex {
					return vm.constructDecoratedClass(instruction, ip, dec, args)
				}
			default:
				result, err := vm.callCallable(decorated, args)
				if err != nil {
					return 0, vm.runtimeError(instruction, "%s", err.Error())
				}
				classInfo := vm.chunk.Classes[classIndex]
				if instance, ok := result.(*runtime.Instance); ok && instance != nil && instance.Class != nil && instance.Class.Name != classInfo.Name {
					instance.ExtraTypeNames = append(instance.ExtraTypeNames, classInfo.Name)
				}
				vm.push(result)
				return ip, nil
			}
		}
	}
	if reason, abstract := vm.classAbstractnessReason(classInfo); abstract {
		thrown := runtime.Error{Class: "RuntimeError", Message: reason}
		vm.pendingThrow = &thrown
		return vm.jumpToExceptionHandler(instruction, ip)
	}
	instance := &runtime.Instance{
		Class: &runtime.Class{
			Name:           classInfo.Name,
			Module:         vm.moduleName,
			TypeParameters: append([]string(nil), classInfo.TypeParameters...),
			Methods:        vm.runtimeMethodWrappers(classIndex),
			MethodMetadata: vm.classFunctionMetadata(classInfo.Methods, "method", 1),
			StaticMetadata: vm.classFunctionMetadata(classInfo.StaticMethods, "staticMethod", 0),
			// Link the parent runtime.Class so cross-module
			// `instanceof Parent` and `reflect.parent(instance)`
			// can walk the chain regardless of which chunk holds
			// the parent's ClassInfo.
			Parent:     vm.runtimeClassForParent(classInfo),
			Implements: vm.runtimeInterfacesForClass(classInfo),
			// Populate runtime.Class.Fields with field metadata
			// (name + optional decorators) so cross-chunk reflect
			// from a sub-VM can read the originating chunk's
			// declarations even when the bytecode ClassInfo isn't
			// reachable. Type-info on the field is left nil here;
			// reflectFieldsResult re-derives type strings from the
			// chunk when present.
			Fields: vm.runtimeFieldsForClass(classInfo),
		},
		Fields: map[string]runtime.Value{},
	}
	// Inherit type bindings from each parent's extends-clause arguments
	// (`class Sub extends Base<string>` propagates {T: "string"} to Sub).
	// Walk the chain via ParentIndex so multi-level inheritance composes.
	for ci := classInfo; ci.ParentIndex >= 0 && int(ci.ParentIndex) < len(vm.chunk.Classes); {
		parent := vm.chunk.Classes[ci.ParentIndex]
		if len(ci.ParentArguments) > 0 && len(parent.TypeParameters) > 0 {
			if instance.TypeBindings == nil {
				instance.TypeBindings = map[string]string{}
			}
			for i, name := range parent.TypeParameters {
				if i >= len(ci.ParentArguments) {
					break
				}
				if _, exists := instance.TypeBindings[name]; !exists {
					instance.TypeBindings[name] = ci.ParentArguments[i]
				}
			}
		}
		ci = parent
	}
	if err := vm.initializeFields(instruction, instance, classIndex); err != nil {
		return 0, err
	}
	if len(classInfo.ConstructorIndices) == 0 {
		if len(args) != 0 {
			return 0, vm.runtimeError(instruction, "%s expects no constructor arguments", classInfo.Name)
		}
		if vm.classExtendsBuiltinError(classInfo) {
			var fields map[string]runtime.Value
			if len(instance.Fields) > 0 {
				fields = instance.Fields
			}
			vm.push(runtime.Error{Class: classInfo.Name, Fields: fields, Parents: vm.errorParentChain(classInfo)})
		} else {
			if classInfo.Immutable {
				instance.Frozen = true
			}
			if classInfo.DestructorIndex >= 0 {
				vm.destructibleInstances = append(vm.destructibleInstances, instance)
			}
			vm.push(instance)
		}
		return ip, nil
	}
	functionIndex, err := vm.selectRuntimeFunction(instruction, classInfo.Name, classInfo.ConstructorIndices, args, 1)
	if err != nil {
		return 0, err
	}
	callArgs := append([]runtime.Value{instance}, args...)
	nextIP, err := vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], callArgs, instance)
	if err != nil {
		return 0, err
	}
	// Mark the frame so OpReturn wraps the result as runtime.Error for error-derived classes.
	if vm.classExtendsBuiltinError(classInfo) && len(vm.frames) > 0 {
		vm.frames[len(vm.frames)-1].isErrorClass = true
	}
	if classInfo.Immutable && len(vm.frames) > 0 {
		vm.frames[len(vm.frames)-1].isImmutableClass = true
	}
	// Mark the frame so OpReturn registers the constructed instance
	// in the destructible-instances list once construction succeeds.
	if classInfo.DestructorIndex >= 0 && !vm.classExtendsBuiltinError(classInfo) && len(vm.frames) > 0 {
		vm.frames[len(vm.frames)-1].isDestructibleConstructor = true
	}
	// Persist inferred class type parameter bindings onto the instance.
	if len(classInfo.TypeParameters) > 0 && len(vm.frames) > 0 {
		classParamSet := map[string]bool{}
		for _, p := range classInfo.TypeParameters {
			classParamSet[p] = true
		}
		frame := vm.frames[len(vm.frames)-1]
		for name, typeName := range frame.typeBindings {
			if classParamSet[name] {
				if instance.TypeBindings == nil {
					instance.TypeBindings = map[string]string{}
				}
				instance.TypeBindings[name] = typeName
			}
		}
	}
	return nextIP, nil
}

func (vm *VM) constructDecoratedClass(instruction Instruction, ip int, replacement runtime.BytecodeClass, args []runtime.Value) (int, error) {
	return vm.constructClassWithArgs(instruction, ip, replacement.Index, args, false)
}

// runtimeFieldsForClass builds the per-field metadata on a
// runtime.Class. The slice is consumed by cross-chunk reflect.fields
// when the receiving VM can't look up the class by index in its own
// chunk. Decorators ride along so framework reflection sees the
// originating chunk's annotations from any other module.
func (vm *VM) runtimeFieldsForClass(classInfo ClassInfo) []runtime.Field {
	fields := make([]runtime.Field, 0, len(classInfo.FieldNames))
	for i, name := range classInfo.FieldNames {
		field := runtime.Field{Name: name}
		if i < len(classInfo.FieldDecorators) && len(classInfo.FieldDecorators[i]) > 0 {
			field.Decorators = decoratorsToAST(classInfo.FieldDecorators[i])
		}
		fields = append(fields, field)
	}
	return fields
}

// decoratorsToAST converts persisted DecoratorMetadata (chunk-format)
// back into ast.Decorator values for the reflection surface. Used when
// populating runtime.Class.Fields from the bytecode ClassInfo.
func decoratorsToAST(metas []runtime.DecoratorMetadata) []ast.Decorator {
	out := make([]ast.Decorator, 0, len(metas))
	for _, m := range metas {
		dec := ast.Decorator{Name: &ast.Identifier{Value: m.Name}}
		for _, arg := range m.Args {
			dec.Arguments = append(dec.Arguments, ast.CallArgument{Value: literalExpressionForValue(arg)})
		}
		for k, v := range m.NamedArgs {
			dec.Arguments = append(dec.Arguments, ast.CallArgument{
				Name:  &ast.Identifier{Value: k},
				Value: literalExpressionForValue(v),
			})
		}
		out = append(out, dec)
	}
	return out
}

// literalExpressionForValue is a thin shim that returns an AST node
// wrapping a runtime value. Only used for decorator arg reconstruction
// where the user previously passed a literal (string / int / etc.).
// Returns nil for anything more elaborate - the decorator-reading
// helpers know to fall back to the raw metadata in that case.
func literalExpressionForValue(v runtime.Value) ast.Expression {
	switch x := v.(type) {
	case runtime.String:
		return &ast.StringLiteral{Value: x.Value}
	case runtime.SmallInt:
		return &ast.IntegerLiteral{Value: fmt.Sprintf("%d", x.Value)}
	case runtime.Int:
		return &ast.IntegerLiteral{Value: x.Value.String()}
	case runtime.Float:
		return &ast.FloatLiteral{Value: fmt.Sprintf("%g", x.Value)}
	case runtime.Bool:
		return &ast.Literal{Value: x.Value}
	case runtime.Null:
		return &ast.Literal{Value: nil}
	}
	return nil
}

func (vm *VM) runtimeMethodWrappers(classIndex int64) map[string][]runtime.Function {
	if classIndex < 0 || int(classIndex) >= len(vm.chunk.Classes) {
		return nil
	}
	classInfo := vm.chunk.Classes[classIndex]
	methods := map[string][]runtime.Function{}
	for key, indices := range classInfo.Methods {
		methodKey := key
		methodIndices := append([]int64(nil), indices...)
		if len(methodIndices) == 0 {
			continue
		}
		name := methodKey
		firstIndex := methodIndices[0]
		if firstIndex >= 0 && int(firstIndex) < len(vm.chunk.Functions) && vm.chunk.Functions[firstIndex].Name != "" {
			name = vm.chunk.Functions[firstIndex].Name
		}
		methods[methodKey] = []runtime.Function{{
			Name:   name,
			Target: "method",
			Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
				if this == nil {
					return nil, fmt.Errorf("method receiver is not available")
				}
				functionIndex, err := vm.selectRuntimeFunction(Instruction{}, methodKey, methodIndices, args, 1)
				if err != nil {
					return nil, err
				}
				if err := vm.ensureCallableDecorators(); err != nil {
					return nil, err
				}
				if decorated, ok := vm.decoratedFuncs[functionIndex]; ok {
					return vm.callCallableWithForwardThis(decorated, args, this)
				}
				callArgs := append([]runtime.Value{this}, args...)
				return vm.CallFunctionRaw(functionIndex, callArgs)
			},
		}}
	}
	return methods
}

func (vm *VM) initializeFields(instruction Instruction, instance *runtime.Instance, classIndex int64) error {
	if classIndex < 0 || int(classIndex) >= len(vm.chunk.Classes) {
		return vm.runtimeError(instruction, "class index out of range")
	}
	classInfo := vm.chunk.Classes[classIndex]
	if classInfo.ParentIndex >= 0 {
		if err := vm.initializeFields(instruction, instance, classInfo.ParentIndex); err != nil {
			return err
		}
	}
	for i, field := range classInfo.FieldNames {
		value := runtime.Value(runtime.Null{})
		if i < len(classInfo.FieldDefaults) && classInfo.FieldDefaults[i] >= 0 {
			defaultIndex := classInfo.FieldDefaults[i]
			if defaultIndex < 0 || int(defaultIndex) >= len(vm.chunk.Constants) {
				return vm.runtimeError(instruction, "field default constant out of range")
			}
			/* Clone container defaults so each new instance gets a
			 * fresh empty dict/list/set. Sharing across instances
			 * is the Python-style mutable-default trap. */
			value = cloneContainerDefault(vm.chunk.Constants[defaultIndex])
		}
		instance.Fields[field] = value
	}
	for _, extra := range vm.interfaceExtraFields[classIndex] {
		if _, exists := instance.Fields[extra.name]; !exists {
			instance.Fields[extra.name] = runtime.Null{}
		}
	}
	return nil
}

func (vm *VM) getField(instruction Instruction, ip int) (int, error) {
	name, err := vm.constantString(instruction, "field name must be string")
	if err != nil {
		return 0, err
	}
	receiver, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	if enumDef, ok := receiver.(*runtime.EnumDef); ok {
		for _, v := range enumDef.Variants {
			if strings.EqualFold(v.Name, name) {
				if v.FieldCount == 0 {
					vm.push(runtime.EnumVariant{Enum: enumDef, Variant: v.Name})
					return ip, nil
				}
				return 0, vm.runtimeError(instruction, "enum variant %s.%s requires arguments", enumDef.Name, v.Name)
			}
		}
		return 0, vm.runtimeError(instruction, "enum %s has no variant %s", enumDef.Name, name)
	}
	if ev, ok := receiver.(runtime.EnumVariant); ok {
		if idx, err2 := strconv.Atoi(name); err2 == nil && idx >= 0 && idx < len(ev.Fields) {
			vm.push(ev.Fields[idx])
			return ip, nil
		}
		return 0, vm.runtimeError(instruction, "enum variant %s.%s has no field %s", ev.Enum.Name, ev.Variant, name)
	}
	instance, ok := receiver.(*runtime.Instance)
	if !ok {
		if module, ok := receiver.(*runtime.Module); ok {
			value, ok := module.Exports[name]
			if !ok {
				return 0, vm.runtimeError(instruction, "module %s has no export %s", module.Name, name)
			}
			vm.push(value)
			return ip, nil
		}
		if errValue, ok := receiver.(runtime.Error); ok {
			switch name {
			case "class", "name":
				vm.push(runtime.String{Value: errValue.Class})
				return ip, nil
			case "message":
				vm.push(runtime.String{Value: errValue.Message})
				return ip, nil
			default:
				if errValue.Fields != nil {
					if v, ok := errValue.Fields[name]; ok {
						vm.push(v)
						return ip, nil
					}
				}
				return 0, vm.runtimeError(instruction, "%s has no field %s", errValue.Class, name)
			}
		}
		if task, ok := receiver.(*runtime.Task); ok {
			switch name {
			case "done":
				vm.push(runtime.Bool{Value: task.Done()})
				return ip, nil
			case "cancelled":
				vm.push(runtime.Bool{Value: task.Cancelled()})
				return ip, nil
			default:
				return 0, vm.runtimeError(instruction, "Task has no field %s", name)
			}
		}
		if r, ok := receiver.(runtime.Range); ok {
			switch name {
			case "start":
				vm.push(runtime.Int{Value: new(big.Int).Set(r.Start)})
				return ip, nil
			case "end":
				vm.push(runtime.Int{Value: new(big.Int).Set(r.End)})
				return ip, nil
			case "step":
				vm.push(runtime.Int{Value: new(big.Int).Set(r.Step)})
				return ip, nil
			case "length":
				vm.push(runtime.Int{Value: r.Length()})
				return ip, nil
			default:
				return 0, vm.runtimeError(instruction, "range has no field %s", name)
			}
		}
		if name == "length" {
			switch v := receiver.(type) {
			case runtime.String:
				vm.push(runtime.SmallInt{Value: int64(len([]rune(v.Value)))})
				return ip, nil
			case runtime.Bytes:
				vm.push(runtime.SmallInt{Value: int64(len(v.Value))})
				return ip, nil
			case runtime.List:
				vm.push(runtime.SmallInt{Value: int64(len(v.Elements))})
				return ip, nil
			case runtime.Dict:
				vm.push(runtime.SmallInt{Value: int64(len(v.Entries))})
				return ip, nil
			case runtime.Set:
				vm.push(runtime.SmallInt{Value: int64(len(v.Elements))})
				return ip, nil
			}
		}
		return 0, vm.runtimeError(instruction, "%s has no field %s", receiver.TypeName(), name)
	}
	if value, ok := instance.Fields[name]; ok {
		vm.cacheFieldShape(instance.Class, name, true)
		vm.push(value)
		return ip, nil
	}
	if vm.fieldLookupValid && vm.fieldLookupClass == instance.Class && vm.fieldLookupName == name {
		if !vm.fieldLookupHasGetMag {
			return 0, vm.runtimeError(instruction, "%s has no field %s", instance.Class.Name, name)
		}
	}
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		return 0, vm.runtimeError(instruction, "unknown class %s", instance.Class.Name)
	}
	indices, hasGet := vm.lookupMethod(classInfo, "__get")
	vm.cacheFieldShapeFull(instance.Class, name, false, hasGet, classInfo)
	if hasGet {
		functionIndex, err := vm.selectRuntimeFunction(instruction, "__get", indices, []runtime.Value{runtime.String{Value: name}}, 1)
		if err != nil {
			return 0, err
		}
		return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{instance, runtime.String{Value: name}}, nil)
	}
	return 0, vm.runtimeError(instruction, "%s has no field %s", instance.Class.Name, name)
}

func (vm *VM) cacheFieldShape(class *runtime.Class, name string, onClass bool) {
	if vm.fieldLookupValid && vm.fieldLookupClass == class && vm.fieldLookupName == name && vm.fieldLookupOnClass == onClass {
		return
	}
	vm.fieldLookupClass = class
	vm.fieldLookupName = name
	vm.fieldLookupOnClass = onClass
	vm.fieldLookupHasGetMag = false
	vm.fieldLookupHasSetMag = false
	vm.fieldLookupValid = true
}

func (vm *VM) cacheFieldShapeFull(class *runtime.Class, name string, onClass, hasGet bool, classInfo ClassInfo) {
	_, hasSet := vm.lookupMethod(classInfo, "__set")
	vm.fieldLookupClass = class
	vm.fieldLookupName = name
	vm.fieldLookupOnClass = onClass
	vm.fieldLookupHasGetMag = hasGet
	vm.fieldLookupHasSetMag = hasSet
	vm.fieldLookupValid = true
}

func (vm *VM) setField(instruction Instruction, ip int) (int, error) {
	name, err := vm.constantString(instruction, "field name must be string")
	if err != nil {
		return 0, err
	}
	value, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	receiver, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	instance, ok := receiver.(*runtime.Instance)
	if !ok {
		return 0, vm.runtimeError(instruction, "%s has no field %s", receiver.TypeName(), name)
	}
	if instance.Frozen {
		return vm.throwTyped(instruction, ip, "ImmutableError", "cannot modify frozen instance of "+instance.Class.Name)
	}
	if classInfo, ok := vm.classInfo(instance.Class.Name); ok {
		transformed, err := vm.applyFieldDecorators(instruction, classInfo, name, value)
		if err != nil {
			return vm.propagateModuleError(instruction, ip, err)
		}
		value = transformed
	}
	if vm.fieldLookupValid && vm.fieldLookupClass == instance.Class && vm.fieldLookupName == name && vm.fieldLookupOnClass {
		instance.Fields[name] = value
		vm.push(value)
		return ip, nil
	}
	if _, ok := instance.Fields[name]; ok {
		vm.cacheFieldShape(instance.Class, name, true)
		instance.Fields[name] = value
		vm.push(value)
		return ip, nil
	}
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		// Cross-module subclass: the instance was created from a class
		// in another chunk (e.g. a user subclass running its parent's
		// constructor here). The __set magic method can't be looked up
		// from this chunk; fall back to a direct field write to match
		// evaluator semantics.
		instance.Fields[name] = value
		vm.push(value)
		return ip, nil
	}
	indices, hasSet := vm.lookupMethod(classInfo, "__set")
	vm.cacheFieldShapeFull(instance.Class, name, false, false, classInfo)
	vm.fieldLookupHasSetMag = hasSet
	if hasSet {
		functionIndex, err := vm.selectRuntimeFunction(instruction, "__set", indices, []runtime.Value{runtime.String{Value: name}, value}, 1)
		if err != nil {
			return 0, err
		}
		return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{instance, runtime.String{Value: name}, value}, value)
	}
	instance.Fields[name] = value
	vm.push(value)
	return ip, nil
}

func (vm *VM) applyFieldDecorators(instruction Instruction, classInfo ClassInfo, name string, value runtime.Value) (runtime.Value, error) {
	fieldIndex := -1
	for i, fname := range classInfo.FieldNames {
		if strings.EqualFold(fname, name) {
			fieldIndex = i
			break
		}
	}
	if fieldIndex < 0 || fieldIndex >= len(classInfo.FieldDecorators) {
		return value, nil
	}
	decorators := classInfo.FieldDecorators[fieldIndex]
	if len(decorators) == 0 {
		return value, nil
	}
	for i := len(decorators) - 1; i >= 0; i-- {
		dec := decorators[i]
		if dec.Name == "" {
			continue
		}
		indices := vm.decoratorFunctionIndices(dec.Name)
		if len(indices) == 0 {
			continue
		}
		callArgs := make([]runtime.Value, 0, len(dec.Args)+1)
		callArgs = append(callArgs, dec.Args...)
		callArgs = append(callArgs, value)
		functionIndex, err := vm.selectRuntimeFunction(instruction, dec.Name, indices, callArgs, 0)
		if err != nil {
			return nil, vm.runtimeError(instruction, "field decorator @%s: %s", dec.Name, err.Error())
		}
		result, err := vm.CallFunction(functionIndex, callArgs)
		if err != nil {
			return nil, err
		}
		value = result
	}
	return value, nil
}

func (vm *VM) callParentConstructor(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.runtimeError(instruction, "parent constructor instruction has invalid operands")
	}
	classIndex := instruction.Operands[0]
	argc := int(instruction.Operands[1])
	instance, args, err := vm.popInstanceAndArgs(instruction, argc)
	if err != nil {
		return 0, err
	}
	// Check if the parent is a builtin error class (ParentIndex == -1, ParentName set).
	if classIndex >= 0 && int(classIndex) < len(vm.chunk.Classes) {
		classInfo := vm.chunk.Classes[classIndex]
		if isBuiltinErrorClass(classInfo.ParentName) {
			// Capture the message from the first string argument for use in OpReturn.
			if len(args) >= 1 {
				if s, ok := args[0].(runtime.String); ok {
					instance.Fields["__parentMsg"] = s
				}
			}
			vm.push(runtime.Null{})
			return ip, nil
		}
		// Cross-module parent: ParentIndex == -1 but ParentName carries a
		// qualified `module.Class` reference. Dispatch through the module
		// loader so the parent's constructor runs against this instance
		// inside its own chunk.
		if classInfo.ParentIndex < 0 && strings.Contains(classInfo.ParentName, ".") {
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "cross-module parent constructor requires a module loader")
			}
			module, parentClass, ok := splitQualifiedClassName(classInfo.ParentName)
			if !ok {
				return 0, vm.runtimeError(instruction, "cross-module parent name %q is malformed", classInfo.ParentName)
			}
			if _, err := vm.moduleLoader.LoadModule(module, module); err != nil {
				return 0, vm.runtimeError(instruction, "load parent module %s: %s", module, err.Error())
			}
			if _, err := vm.moduleLoader.CallParentInModule(module, parentClass, parentClass, instance, args); err != nil {
				return 0, vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.push(runtime.Null{})
			return ip, nil
		}
	}
	parent, err := vm.parentClassInfo(instruction, classIndex)
	if err != nil {
		return 0, err
	}
	if len(parent.ConstructorIndices) == 0 {
		if argc != 0 {
			return 0, vm.runtimeError(instruction, "%s expects no constructor arguments", parent.Name)
		}
		vm.push(runtime.Null{})
		return ip, nil
	}
	functionIndex, err := vm.selectRuntimeFunction(instruction, parent.Name, parent.ConstructorIndices, args, 1)
	if err != nil {
		return 0, err
	}
	callArgs := append([]runtime.Value{instance}, args...)
	return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], callArgs, runtime.Null{})
}

func (vm *VM) callParentMethod(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 3 {
		return 0, vm.runtimeError(instruction, "parent method instruction has invalid operands")
	}
	classIndex := instruction.Operands[0]
	nameIndex := instruction.Operands[1]
	argc := int(instruction.Operands[2])
	if nameIndex < 0 || int(nameIndex) >= len(vm.chunk.Constants) {
		return 0, vm.runtimeError(instruction, "method name constant out of range")
	}
	name, ok := vm.chunk.Constants[nameIndex].(runtime.String)
	if !ok {
		return 0, vm.runtimeError(instruction, "method name constant must be string")
	}
	instance, args, err := vm.popInstanceAndArgs(instruction, argc)
	if err != nil {
		return 0, err
	}
	// Cross-module parent: dispatch the named method into the parent's
	// own chunk through the module loader. Mirrors callParentConstructor.
	if classIndex >= 0 && int(classIndex) < len(vm.chunk.Classes) {
		classInfo := vm.chunk.Classes[classIndex]
		if classInfo.ParentIndex < 0 && strings.Contains(classInfo.ParentName, ".") {
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "cross-module parent method requires a module loader")
			}
			module, parentClass, ok := splitQualifiedClassName(classInfo.ParentName)
			if !ok {
				return 0, vm.runtimeError(instruction, "cross-module parent name %q is malformed", classInfo.ParentName)
			}
			if _, err := vm.moduleLoader.LoadModule(module, module); err != nil {
				return 0, vm.runtimeError(instruction, "load parent module %s: %s", module, err.Error())
			}
			result, err := vm.moduleLoader.CallParentInModule(module, parentClass, name.Value, instance, args)
			if err != nil {
				return 0, vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.push(result)
			return ip, nil
		}
	}
	parent, err := vm.parentClassInfo(instruction, classIndex)
	if err != nil {
		return 0, err
	}
	indices, ok := vm.lookupMethod(parent, name.Value)
	if !ok {
		return 0, vm.runtimeError(instruction, "unknown parent method %s.%s", parent.Name, name.Value)
	}
	functionIndex, err := vm.selectRuntimeFunction(instruction, name.Value, indices, args, 1)
	if err != nil {
		return 0, err
	}
	callArgs := append([]runtime.Value{instance}, args...)
	return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], callArgs, nil)
}

func (vm *VM) popInstanceAndArgs(instruction Instruction, argc int) (*runtime.Instance, []runtime.Value, error) {
	args := make([]runtime.Value, argc)
	for i := argc - 1; i >= 0; i-- {
		value, err := vm.pop()
		if err != nil {
			return nil, nil, vm.runtimeError(instruction, "%s", err.Error())
		}
		args[i] = value
	}
	receiver, err := vm.pop()
	if err != nil {
		return nil, nil, vm.runtimeError(instruction, "%s", err.Error())
	}
	instance, ok := receiver.(*runtime.Instance)
	if !ok {
		return nil, nil, vm.runtimeError(instruction, "%s is not an instance", receiver.TypeName())
	}
	return instance, args, nil
}

// splitQualifiedClassName separates a `module.path.Class` reference into
// the canonical module path and the class name. Returns ok=false when
// the input has no dot (and therefore isn't qualified).
func splitQualifiedClassName(qualified string) (string, string, bool) {
	dot := strings.LastIndex(qualified, ".")
	if dot < 0 {
		return "", "", false
	}
	return qualified[:dot], qualified[dot+1:], true
}

func (vm *VM) parentClassInfo(instruction Instruction, classIndex int64) (ClassInfo, error) {
	if classIndex < 0 || int(classIndex) >= len(vm.chunk.Classes) {
		return ClassInfo{}, vm.runtimeError(instruction, "class index out of range")
	}
	parentIndex := vm.chunk.Classes[classIndex].ParentIndex
	if parentIndex < 0 || int(parentIndex) >= len(vm.chunk.Classes) {
		return ClassInfo{}, vm.runtimeError(instruction, "%s has no parent class", vm.chunk.Classes[classIndex].Name)
	}
	return vm.chunk.Classes[parentIndex], nil
}

func (vm *VM) getStaticValue(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.runtimeError(instruction, "static value instruction has invalid operands")
	}
	classIndex := instruction.Operands[0]
	nameIndex := instruction.Operands[1]
	if classIndex < 0 || int(classIndex) >= len(vm.chunk.Classes) {
		return 0, vm.runtimeError(instruction, "class index out of range")
	}
	name, err := vm.constantStringAt(instruction, nameIndex, "static value name must be string")
	if err != nil {
		return 0, err
	}
	constantIndex, ok := vm.lookupStaticValue(vm.chunk.Classes[classIndex], name)
	if !ok {
		if indices, ok := vm.lookupStaticMethod(vm.chunk.Classes[classIndex], "__getStatic"); ok {
			functionIndex, err := vm.selectRuntimeFunction(instruction, "__getStatic", indices, []runtime.Value{runtime.String{Value: name}}, 0)
			if err != nil {
				return 0, err
			}
			return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{runtime.String{Value: name}}, nil)
		}
		return 0, vm.runtimeError(instruction, "unknown static member %s.%s", vm.chunk.Classes[classIndex].Name, name)
	}
	if constantIndex < 0 || int(constantIndex) >= len(vm.chunk.Constants) {
		return 0, vm.runtimeError(instruction, "static value constant out of range")
	}
	vm.push(vm.chunk.Constants[constantIndex])
	return ip, nil
}

func (vm *VM) setStaticValue(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.runtimeError(instruction, "static assignment instruction has invalid operands")
	}
	classIndex := instruction.Operands[0]
	nameIndex := instruction.Operands[1]
	if classIndex < 0 || int(classIndex) >= len(vm.chunk.Classes) {
		return 0, vm.runtimeError(instruction, "class index out of range")
	}
	name, err := vm.constantStringAt(instruction, nameIndex, "static value name must be string")
	if err != nil {
		return 0, err
	}
	value, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	// Direct assignment to a declared static let / const member. The
	// chunk stores StaticValues as constant pool indices that the
	// runtime overwrites in place; we hijack the constant pool slot so
	// subsequent reads see the new value. This mirrors the evaluator's
	// behaviour for `Class.name = value`. The assignment expression
	// itself evaluates to the assigned value, so re-push it for the
	// enclosing OpPop (or further use).
	for ci := classIndex; ci >= 0; {
		classInfo := vm.chunk.Classes[ci]
		if constIdx, present := classInfo.StaticValues[name]; present && constIdx >= 0 && int(constIdx) < len(vm.chunk.Constants) {
			vm.chunk.Constants[constIdx] = value
			vm.push(value)
			return ip, nil
		}
		ci = classInfo.ParentIndex
	}
	if indices, ok := vm.lookupStaticMethod(vm.chunk.Classes[classIndex], "__setStatic"); ok {
		functionIndex, err := vm.selectRuntimeFunction(instruction, "__setStatic", indices, []runtime.Value{runtime.String{Value: name}, value}, 0)
		if err != nil {
			return 0, err
		}
		return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{runtime.String{Value: name}, value}, nil)
	}
	return 0, vm.runtimeError(instruction, "unknown static member %s.%s", vm.chunk.Classes[classIndex].Name, name)
}

func (vm *VM) callStaticMethod(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 3 {
		return 0, vm.runtimeError(instruction, "static method instruction has invalid operands")
	}
	classIndex := instruction.Operands[0]
	nameIndex := instruction.Operands[1]
	argc := int(instruction.Operands[2])
	if classIndex < 0 || int(classIndex) >= len(vm.chunk.Classes) {
		return 0, vm.runtimeError(instruction, "class index out of range")
	}
	name, err := vm.constantStringAt(instruction, nameIndex, "static method name must be string")
	if err != nil {
		return 0, err
	}
	args := make([]runtime.Value, argc)
	for i := argc - 1; i >= 0; i-- {
		value, err := vm.pop()
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		args[i] = value
	}
	indices, ok := vm.lookupStaticMethod(vm.chunk.Classes[classIndex], name)
	if !ok {
		if fallbackIndices, ok := vm.lookupStaticMethod(vm.chunk.Classes[classIndex], "__callStatic"); ok {
			functionIndex, err := vm.selectRuntimeFunction(instruction, "__callStatic", fallbackIndices, []runtime.Value{runtime.String{Value: name}, runtime.List{Elements: args}}, 0)
			if err != nil {
				return 0, err
			}
			return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{runtime.String{Value: name}, runtime.List{Elements: args}}, nil)
		}
		return 0, vm.runtimeError(instruction, "unknown static method %s.%s", vm.chunk.Classes[classIndex].Name, name)
	}
	functionIndex, err := vm.selectRuntimeFunction(instruction, name, indices, args, 0)
	if err != nil {
		return 0, err
	}
	if decorated, ok := vm.decoratedFuncs[functionIndex]; ok {
		if vm.chunk.Functions[functionIndex].Async && !vm.syncMode {
			vm.push(vm.startAsyncCallable(decorated, args))
			return ip, nil
		}
		result, err := vm.callCallable(decorated, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], args, nil)
}

func (vm *VM) orderRuntimeArguments(instruction Instruction, function FunctionInfo, args []runtime.Value, names []string, paramOffset int) ([]runtime.Value, error) {
	if len(args) != len(names) {
		return nil, vm.runtimeError(instruction, "argument metadata mismatch")
	}
	if len(function.ParamNames) < paramOffset {
		return nil, vm.runtimeError(instruction, "function metadata for %s has invalid receiver offset", function.Name)
	}
	ordered := make([]runtime.Value, len(function.ParamNames)-paramOffset)
	assigned := make([]bool, len(ordered))
	positions := map[string]int{}
	for i := paramOffset; i < len(function.ParamNames); i++ {
		positions[function.ParamNames[i]] = i - paramOffset
	}
	nextPositional := 0
	for i, arg := range args {
		if names[i] == "" {
			for nextPositional < len(ordered) && assigned[nextPositional] {
				nextPositional++
			}
			if nextPositional >= len(ordered) {
				return nil, vm.runtimeError(instruction, "%s received too many positional arguments", function.Name)
			}
			ordered[nextPositional] = arg
			assigned[nextPositional] = true
			nextPositional++
			continue
		}
		position, ok := positions[strings.ToLower(names[i])]
		if !ok {
			return nil, vm.runtimeError(instruction, "%s has no parameter %s", function.Name, names[i])
		}
		if assigned[position] {
			return nil, vm.runtimeError(instruction, "%s parameter %s passed more than once", function.Name, names[i])
		}
		ordered[position] = arg
		assigned[position] = true
	}
	for i := range ordered {
		if assigned[i] {
			continue
		}
		paramIndex := i + paramOffset
		if paramIndex >= len(function.DefaultConstants) || function.DefaultConstants[paramIndex] < 0 {
			return nil, vm.runtimeError(instruction, "%s missing argument before parameter %s", function.Name, function.ParamNames[paramIndex])
		}
		defaultIndex := function.DefaultConstants[paramIndex]
		if defaultIndex < 0 || int(defaultIndex) >= len(vm.chunk.Constants) {
			return nil, vm.runtimeError(instruction, "default argument constant out of range")
		}
		ordered[i] = vm.chunk.Constants[defaultIndex]
		assigned[i] = true
	}
	for len(ordered) > 0 {
		paramIndex := len(ordered) - 1 + paramOffset
		if paramIndex >= len(function.DefaultConstants) || function.DefaultConstants[paramIndex] < 0 {
			break
		}
		defaultIndex := function.DefaultConstants[paramIndex]
		if assigned[len(ordered)-1] && defaultIndex >= 0 && int(defaultIndex) < len(vm.chunk.Constants) && valuesEqual(ordered[len(ordered)-1], vm.chunk.Constants[defaultIndex]) {
			ordered = ordered[:len(ordered)-1]
			continue
		}
		break
	}
	return ordered, nil
}

func spreadDictNamedArguments(dict runtime.Dict, positional []runtime.Value) ([]runtime.Value, []string, error) {
	type namedArg struct {
		name  string
		value runtime.Value
	}
	named := make([]namedArg, 0, len(dict.Entries))
	for _, entry := range dict.Entries {
		key, ok := entry.Key.(runtime.String)
		if !ok {
			return nil, nil, fmt.Errorf("spread dict argument keys must be strings")
		}
		named = append(named, namedArg{name: key.Value, value: entry.Value})
	}
	sort.Slice(named, func(i, j int) bool { return named[i].name < named[j].name })
	args := make([]runtime.Value, 0, len(positional)+len(named))
	names := make([]string, 0, len(positional)+len(named))
	for _, value := range positional {
		args = append(args, value)
		names = append(names, "")
	}
	for _, arg := range named {
		args = append(args, arg.value)
		names = append(names, arg.name)
	}
	return args, names, nil
}

func (vm *VM) evalNativeCall(name string, args []runtime.Value) (runtime.Value, error) {
	return vm.evalNativeCallWithNames(name, args, nil)
}

func (vm *VM) evalNativeCallWithNames(name string, args []runtime.Value, names []string) (runtime.Value, error) {
	if strings.HasPrefix(name, "collections.") {
		return vm.collectionsNativeCall(name[len("collections."):], args)
	}
	if strings.HasPrefix(name, "reflect.") {
		return vm.reflectNativeCall(name[len("reflect."):], args)
	}
	if name == "async.run" {
		return vm.asyncRun(args)
	}
	// async.{sleep,await,done,cancel,all,race,timeout} are self-contained on
	// runtime.Task primitives. When a stateful native caller has been
	// configured we still route through it so embedders can observe the calls
	// (existing TestVMRunsAsyncModuleThroughStatefulNativeBridge expects this).
	if vm.statefulNative == nil {
		if name == "async.sleep" {
			return vm.asyncSleepNative(args)
		}
		if name == "async.await" {
			return vm.asyncAwaitNative(args)
		}
		if name == "async.done" {
			return vm.asyncDoneNative(args)
		}
		if name == "async.cancel" {
			return vm.asyncCancelNative(args)
		}
		if name == "async.all" {
			return vm.asyncAllNative(args)
		}
		if name == "async.race" {
			return vm.asyncRaceNative(args)
		}
		if name == "async.timeout" {
			return vm.asyncTimeoutNative(args)
		}
	}
	// errors.is must use the VM's own hierarchy walk so user-defined class
	// hierarchies (stored in vm.chunk.Classes) are consulted correctly.
	if name == "errors.is" {
		return vm.errorsIs(args)
	}
	if module, function, ok := strings.Cut(name, "."); ok && isStatefulNativeCall(module, function) {
		return vm.statefulNativeCall(module, function, args, names)
	}
	return vm.natives.CallKey(name, args)
}

func (vm *VM) asyncRun(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("async.run expects one function argument")
	}
	switch fn := args[0].(type) {
	case runtime.BytecodeFunction:
		return vm.startAsyncCallable(fn, nil), nil
	case runtime.BytecodeClosure:
		return vm.startAsyncCallable(fn, nil), nil
	default:
		return nil, fmt.Errorf("async.run expects a function, got %s", args[0].TypeName())
	}
}

func (vm *VM) asyncSleepNative(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("async.sleep expects one argument (milliseconds)")
	}
	ms, ok := asInt64Ms(args[0])
	if !ok {
		return nil, fmt.Errorf("async.sleep expects a numeric millisecond value")
	}
	task := runtime.NewTask()
	go func() {
		if ms > 0 {
			select {
			case <-time.After(time.Duration(ms) * time.Millisecond):
			case <-task.CancelChan():
				return
			}
		}
		task.Complete(runtime.Null{}, nil)
	}()
	return task, nil
}

func (vm *VM) asyncAwaitNative(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("async.await expects one task")
	}
	if task, ok := args[0].(*runtime.Task); ok {
		result := task.Await()
		if result.Err != nil {
			return nil, result.Err
		}
		if result.Value == nil {
			return runtime.Null{}, nil
		}
		return result.Value, nil
	}
	return args[0], nil
}

func (vm *VM) asyncDoneNative(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("async.done expects one task")
	}
	task, ok := args[0].(*runtime.Task)
	if !ok {
		return nil, fmt.Errorf("async.done expects Task")
	}
	return runtime.Bool{Value: task.Done()}, nil
}

func (vm *VM) asyncCancelNative(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("async.cancel expects one task")
	}
	task, ok := args[0].(*runtime.Task)
	if !ok {
		return nil, fmt.Errorf("async.cancel expects Task")
	}
	task.Cancel()
	return runtime.Null{}, nil
}

func (vm *VM) asyncTimeoutNative(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("async.timeout expects (task, milliseconds)")
	}
	inner, ok := args[0].(*runtime.Task)
	if !ok {
		return nil, fmt.Errorf("async.timeout expects a Task as the first argument")
	}
	ms, ok := asInt64Ms(args[1])
	if !ok {
		return nil, fmt.Errorf("async.timeout expects a numeric millisecond value")
	}
	out := runtime.NewTask()
	go func() {
		select {
		case <-inner.DoneChan():
			result := inner.Await()
			out.Complete(result.Value, result.Err)
		case <-time.After(time.Duration(ms) * time.Millisecond):
			inner.Cancel()
			out.Complete(runtime.Null{}, fmt.Errorf("async.timeout: task did not complete within %dms", ms))
		}
	}()
	return out, nil
}

func (vm *VM) asyncAllNative(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("async.all expects one list of tasks")
	}
	list, ok := args[0].(runtime.List)
	if !ok {
		return nil, fmt.Errorf("async.all expects a list of tasks")
	}
	tasks := make([]*runtime.Task, 0, len(list.Elements))
	for i, el := range list.Elements {
		task, ok := el.(*runtime.Task)
		if !ok {
			return nil, fmt.Errorf("async.all: element %d is not a Task", i)
		}
		tasks = append(tasks, task)
	}
	out := runtime.NewTask()
	go func() {
		results := make([]runtime.Value, len(tasks))
		for i, t := range tasks {
			r := t.Await()
			if r.Err != nil {
				for j, sibling := range tasks {
					if j != i {
						sibling.Cancel()
					}
				}
				out.Complete(runtime.Null{}, r.Err)
				return
			}
			results[i] = r.Value
		}
		out.Complete(runtime.List{Elements: results}, nil)
	}()
	return out, nil
}

func (vm *VM) asyncRaceNative(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("async.race expects one list of tasks")
	}
	list, ok := args[0].(runtime.List)
	if !ok {
		return nil, fmt.Errorf("async.race expects a list of tasks")
	}
	if len(list.Elements) == 0 {
		return nil, fmt.Errorf("async.race requires at least one task")
	}
	tasks := make([]*runtime.Task, 0, len(list.Elements))
	for i, el := range list.Elements {
		task, ok := el.(*runtime.Task)
		if !ok {
			return nil, fmt.Errorf("async.race: element %d is not a Task", i)
		}
		tasks = append(tasks, task)
	}
	out := runtime.NewTask()
	go func() {
		winner := make(chan int, len(tasks))
		for i, t := range tasks {
			i, t := i, t
			go func() {
				<-t.DoneChan()
				winner <- i
			}()
		}
		first := <-winner
		for j, sibling := range tasks {
			if j != first {
				sibling.Cancel()
			}
		}
		r := tasks[first].Await()
		out.Complete(r.Value, r.Err)
	}()
	return out, nil
}

// asInt64Ms extracts a millisecond count from a runtime.Value supporting
// SmallInt, Int (big.Int that fits in int64), and Float.
func asInt64Ms(v runtime.Value) (int64, bool) {
	switch n := v.(type) {
	case runtime.SmallInt:
		return n.Value, true
	case runtime.Int:
		if n.Value.IsInt64() {
			return n.Value.Int64(), true
		}
	case runtime.Float:
		return int64(n.Value), true
	}
	return 0, false
}

func (vm *VM) reflectNativeCall(fn string, args []runtime.Value) (runtime.Value, error) {
	switch fn {
	case "function", "class":
		return vm.reflectLookupNativeCall(fn, args)
	case "module":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.module expects value")
		}
		if _, ok := args[0].(runtime.Null); ok {
			return runtime.Null{}, nil
		}
		module, ok := args[0].(*runtime.Module)
		if !ok {
			return nil, fmt.Errorf("reflect.module expects module, got %s", args[0].TypeName())
		}
		return module, nil
	case "classes":
		if len(args) != 0 {
			return nil, fmt.Errorf("reflect.classes takes no arguments")
		}
		out := vm.collectChunkClasses(vm.chunk)
		if vm.moduleLoader != nil {
			out = append(out, vm.moduleLoader.ListAllClasses()...)
		}
		return runtime.List{Elements: dedupeClassValues(out)}, nil
	case "exports":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.exports expects module")
		}
		module, ok := args[0].(*runtime.Module)
		if !ok {
			return nil, fmt.Errorf("reflect.exports expects module, got %s", args[0].TypeName())
		}
		names := make([]string, 0, len(module.Exports))
		for name := range module.Exports {
			names = append(names, name)
		}
		sort.Strings(names)
		values := make([]runtime.Value, 0, len(names))
		for _, name := range names {
			values = append(values, runtime.String{Value: name})
		}
		return runtime.List{Elements: values}, nil
	case "method", "staticMethod":
		return vm.reflectMethodNativeCall(fn, args)
	case "decorators":
		if len(args) != 1 && len(args) != 2 {
			return nil, fmt.Errorf("reflect.decorators expects value and optional decorator name")
		}
		target, ok := reflectDecoratorTarget(args[0])
		if !ok {
			return nil, fmt.Errorf("reflect.decorators expects reflect target, got %s", args[0].TypeName())
		}
		filter := ""
		if len(args) == 2 {
			name, ok := args[1].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("reflect.decorators decorator name must be string")
			}
			filter = name.Value
		}
		values := make([]runtime.Value, 0, len(target.Decorators))
		for _, decorator := range target.Decorators {
			if filter != "" && !strings.EqualFold(decorator.Name, filter) {
				continue
			}
			values = append(values, decoratorMetadataDict(decorator))
		}
		return runtime.List{Elements: values}, nil
	case "hasDecorator":
		if len(args) != 2 {
			return nil, fmt.Errorf("reflect.hasDecorator expects value and decorator name")
		}
		target, ok := reflectDecoratorTarget(args[0])
		if !ok {
			return nil, fmt.Errorf("reflect.hasDecorator expects reflect target, got %s", args[0].TypeName())
		}
		name, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("reflect.hasDecorator decorator name must be string")
		}
		for _, decorator := range target.Decorators {
			if strings.EqualFold(decorator.Name, name.Value) {
				return runtime.Bool{Value: true}, nil
			}
		}
		return runtime.Bool{Value: false}, nil
	case "decorator":
		if len(args) != 2 {
			return nil, fmt.Errorf("reflect.decorator expects value and decorator name")
		}
		target, ok := reflectDecoratorTarget(args[0])
		if !ok {
			return nil, fmt.Errorf("reflect.decorator expects reflect target, got %s", args[0].TypeName())
		}
		name, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("reflect.decorator decorator name must be string")
		}
		for _, decorator := range target.Decorators {
			if strings.EqualFold(decorator.Name, name.Value) {
				return decoratorMetadataDict(decorator), nil
			}
		}
		return runtime.Null{}, nil
	case "parameters":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.parameters expects value")
		}
		metadata, ok := reflectFunctionMetadata(args[0])
		if !ok {
			return nil, fmt.Errorf("reflect.parameters expects function or method, got %s", args[0].TypeName())
		}
		values := make([]runtime.Value, 0, len(metadata.Parameters))
		for _, parameter := range metadata.Parameters {
			values = append(values, parameterMetadataDict(parameter))
		}
		return runtime.List{Elements: values}, nil
	case "returnType":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.returnType expects value")
		}
		metadata, ok := reflectFunctionMetadata(args[0])
		if !ok {
			return nil, fmt.Errorf("reflect.returnType expects function or method, got %s", args[0].TypeName())
		}
		return runtime.String{Value: metadata.ReturnType}, nil
	case "doc", "docs":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.%s expects value", fn)
		}
		doc, ok := vm.reflectDoc(args[0])
		if !ok {
			return nil, fmt.Errorf("reflect.%s expects function, method, class, or interface, got %s", fn, args[0].TypeName())
		}
		if doc == "" {
			return runtime.Null{}, nil
		}
		if fn == "docs" {
			return bytecodeDocMetadataDict(doc), nil
		}
		return runtime.String{Value: doc}, nil
	case "typeOf":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.typeOf expects value")
		}
		return runtime.Type{Name: args[0].TypeName()}, nil
	case "location":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.location expects value")
		}
		return vm.reflectLocation(args[0])
	case "getField":
		if len(args) != 2 {
			return nil, fmt.Errorf("reflect.getField expects (instance, fieldName)")
		}
		instance, ok := args[0].(*runtime.Instance)
		if !ok {
			return nil, fmt.Errorf("reflect.getField expects instance, got %s", args[0].TypeName())
		}
		name, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("reflect.getField field name must be string")
		}
		if v, hit := instance.Fields[name.Value]; hit {
			return v, nil
		}
		return runtime.Null{}, nil
	case "setField":
		if len(args) != 3 {
			return nil, fmt.Errorf("reflect.setField expects (instance, fieldName, value)")
		}
		instance, ok := args[0].(*runtime.Instance)
		if !ok {
			return nil, fmt.Errorf("reflect.setField expects instance, got %s", args[0].TypeName())
		}
		name, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("reflect.setField field name must be string")
		}
		if instance.Fields == nil {
			instance.Fields = map[string]runtime.Value{}
		}
		instance.Fields[name.Value] = args[2]
		return instance, nil
	case "fields", "methods", "staticMethods", "parent", "interfaces", "className":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.%s expects class", fn)
		}
		metadata, ok := reflectClassMetadata(args[0])
		if !ok {
			// Fall back to built-in primitive metadata so
			// `reflect.methods([1,2,3])` and the rest of the
			// reflect API work on lists / dicts / sets / strings
			// / bytes / ranges.
			if md, primOk := vmPrimitiveTypeMetadata(args[0]); primOk {
				metadata = md
				ok = true
			}
		}
		if !ok {
			// className is total: for a primitive without class
			// metadata, return its runtime type name (symmetric
			// with how reflect.typeOf handles instances).
			if fn == "className" {
				return runtime.String{Value: args[0].TypeName()}, nil
			}
			return nil, fmt.Errorf("reflect.%s expects class, got %s", fn, args[0].TypeName())
		}
		switch fn {
		case "fields":
			return vm.reflectFieldsResult(args[0], metadata), nil
		case "methods":
			return bytecodeStringList(metadata.Methods), nil
		case "staticMethods":
			return bytecodeStringList(metadata.StaticMethods), nil
		case "parent":
			if metadata.Parent == "" {
				return runtime.Null{}, nil
			}
			return runtime.String{Value: metadata.Parent}, nil
		case "className":
			if metadata.Name == "" {
				return runtime.Null{}, nil
			}
			return runtime.String{Value: metadata.Name}, nil
		case "interfaces":
			return bytecodeStringList(metadata.Interfaces), nil
		}
		return nil, fmt.Errorf("unsupported native call reflect.%s", fn)
	case "constructors":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.constructors expects class")
		}
		return vm.reflectConstructors(args[0])
	case "typeBindings":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.typeBindings expects instance")
		}
		entries := map[string]runtime.DictEntry{}
		putBinding := func(name, typeName string) {
			k := runtime.String{Value: name}
			entries[native.DictKey(k)] = runtime.DictEntry{Key: k, Value: runtime.String{Value: typeName}}
		}
		switch v := args[0].(type) {
		case *runtime.Instance:
			for name, typeName := range v.TypeBindings {
				putBinding(name, typeName)
			}
		case runtime.List:
			if len(v.ElementTypes) >= 1 {
				putBinding("T", v.ElementTypes[0])
			}
		case runtime.Set:
			if len(v.ElementTypes) >= 1 {
				putBinding("T", v.ElementTypes[0])
			}
		case runtime.Dict:
			if len(v.ElementTypes) >= 2 {
				putBinding("K", v.ElementTypes[0])
				putBinding("V", v.ElementTypes[1])
			}
		default:
			return nil, fmt.Errorf("reflect.typeBindings expects instance or generic collection, got %s", args[0].TypeName())
		}
		return runtime.Dict{Entries: entries}, nil
	case "interfaceMethods", "interfaceParents":
		if len(args) != 1 {
			return nil, fmt.Errorf("reflect.%s expects interface", fn)
		}
		iface, ok := vm.reflectInterfaceInfo(args[0])
		if !ok {
			return nil, fmt.Errorf("reflect.%s expects interface, got %s", fn, args[0].TypeName())
		}
		if fn == "interfaceParents" {
			parents := append([]string(nil), iface.Parents...)
			sort.Strings(parents)
			return bytecodeStringList(parents), nil
		}
		methods := append([]runtime.FunctionMetadata(nil), iface.Methods...)
		sort.Slice(methods, func(i, j int) bool {
			return strings.ToLower(methods[i].Name) < strings.ToLower(methods[j].Name)
		})
		values := make([]runtime.Value, 0, len(methods))
		for _, method := range methods {
			values = append(values, interfaceMethodMetadataDict(method))
		}
		return runtime.List{Elements: values}, nil
	default:
		return nil, fmt.Errorf("unsupported native call reflect.%s", fn)
	}
}

func (vm *VM) reflectInterfaceInfo(value runtime.Value) (InterfaceInfo, bool) {
	switch value := value.(type) {
	case runtime.String:
		return vm.lookupInterfaceInfo(value.Value)
	default:
		return InterfaceInfo{}, false
	}
}

func (vm *VM) reflectDoc(value runtime.Value) (string, bool) {
	if metadata, ok := reflectFunctionMetadata(value); ok {
		return metadata.Doc, true
	}
	if metadata, ok := reflectClassMetadata(value); ok {
		return metadata.Doc, true
	}
	if iface, ok := vm.reflectInterfaceInfo(value); ok {
		return iface.Doc, true
	}
	return "", false
}

func (vm *VM) reflectLookupNativeCall(fn string, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("reflect.%s expects value or module and export name", fn)
	}
	value := args[0]
	if len(args) == 2 {
		module, ok := args[0].(*runtime.Module)
		if !ok {
			return nil, fmt.Errorf("reflect.%s qualified lookup expects module, got %s", fn, args[0].TypeName())
		}
		exportName, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("reflect.%s qualified export name must be string", fn)
		}
		exported, ok := module.Exports[exportName.Value]
		if !ok {
			return runtime.Null{}, nil
		}
		value = exported
	}
	switch fn {
	case "function":
		switch value := value.(type) {
		case runtime.DecoratorTarget:
			if value.Target == "function" {
				return value, nil
			}
		case runtime.BytecodeFunction:
			return value, nil
		case runtime.String:
			// Look up a function in the chunk by name (or fall
			// through to module loader). Returns Null when the
			// name isn't found, matching the eval semantics.
			if vm.moduleLoader != nil {
				if found, ok := vm.moduleLoader.FindFunctionByName(value.Value); ok {
					return found, nil
				}
			}
			return runtime.Null{}, nil
		}
		return nil, fmt.Errorf("reflect.function expects function, got %s", value.TypeName())
	case "class":
		switch value := value.(type) {
		case runtime.DecoratorTarget:
			if value.Target == "class" {
				return value, nil
			}
		case runtime.BytecodeClass:
			return value, nil
		case runtime.String:
			// Look up the class by name in the chunk's class table
			// first; fall back to cross-module search through the
			// module loader so framework helpers can resolve a
			// user-declared class from another module.
			if classIndex, ok := vm.classIndex[strings.ToLower(value.Value)]; ok {
				classInfo := vm.chunk.Classes[classIndex]
				return vm.bytecodeClassFromInfo(classInfo, int64(classIndex)), nil
			}
			if vm.moduleLoader != nil {
				if found, ok := vm.moduleLoader.FindClassByName(value.Value); ok {
					return found, nil
				}
			}
			return runtime.Null{}, nil
		case *runtime.Instance:
			classIndex, ok := vm.classIndex[strings.ToLower(value.Class.Name)]
			if !ok {
				if metadata, ok := runtimeClassMetadata(value.Class); ok {
					return runtime.DecoratorTarget{Target: "class", Class: &metadata}, nil
				}
				return nil, fmt.Errorf("reflect.class unknown class %s", value.Class.Name)
			}
			classInfo := vm.chunk.Classes[classIndex]
			return vm.bytecodeClassFromInfo(classInfo, int64(classIndex)), nil
		}
		return nil, fmt.Errorf("reflect.class expects class, instance, or name string, got %s", value.TypeName())
	default:
		return nil, fmt.Errorf("unsupported reflect lookup %s", fn)
	}
}

func (vm *VM) reflectMethodNativeCall(fn string, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("reflect.%s expects class and method name", fn)
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("reflect.%s method name must be string", fn)
	}
	key := strings.ToLower(name.Value)
	var class runtime.BytecodeClass
	var instance *runtime.Instance
	switch value := args[0].(type) {
	case runtime.BytecodeClass:
		class = value
	case *runtime.Instance:
		instance = value
		classIndex, ok := vm.classIndex[strings.ToLower(value.Class.Name)]
		if !ok {
			if target, ok := reflectedRuntimeInstanceMethod(fn, key, value); ok {
				return target, nil
			}
			return nil, fmt.Errorf("reflect.%s unknown class %s", fn, value.Class.Name)
		}
		classInfo := vm.chunk.Classes[classIndex]
		class = runtime.BytecodeClass{
			Name:             classInfo.Name,
			Doc:              classInfo.Doc,
			Index:            int64(classIndex),
			Decorators:       classInfo.Decorators,
			MethodDecorators: classInfo.MethodDecorators,
			StaticDecorators: classInfo.StaticDecorators,
			MethodMetadata:   vm.classFunctionMetadata(classInfo.Methods, "method", 1),
			StaticMetadata:   vm.classFunctionMetadata(classInfo.StaticMethods, "staticMethod", 0),
		}
	default:
		return nil, fmt.Errorf("reflect.%s expects bytecode class or instance, got %s", fn, args[0].TypeName())
	}
	target := "method"
	decorators := []runtime.DecoratorMetadata(nil)
	var callable runtime.Value
	if fn == "staticMethod" {
		target = "staticMethod"
		decorators = class.StaticDecorators[key]
		callable = vm.reflectStaticMethodCallable(class, key)
	} else {
		decorators = class.MethodDecorators[key]
		if instance != nil {
			callable = vm.reflectBoundMethodCallable(class, key, instance)
		}
	}
	metadata := classMethodMetadata(class, key, fn == "staticMethod")
	if metadata == nil && class.Index >= 0 && int(class.Index) < len(vm.chunk.Classes) {
		classInfo := vm.chunk.Classes[class.Index]
		if fn == "staticMethod" {
			class.StaticMetadata = vm.classFunctionMetadata(classInfo.StaticMethods, "staticMethod", 0)
		} else {
			class.MethodMetadata = vm.classFunctionMetadata(classInfo.Methods, "method", 1)
		}
		metadata = classMethodMetadata(class, key, fn == "staticMethod")
	}
	if decorators == nil && metadata == nil && callable == nil {
		return runtime.Null{}, nil
	}
	if decorators == nil {
		decorators = []runtime.DecoratorMetadata{}
	}
	return runtime.DecoratorTarget{Target: target, Decorators: decorators, Function: metadata, Callable: callable}, nil
}

func (vm *VM) reflectBoundMethodCallable(class runtime.BytecodeClass, key string, instance *runtime.Instance) runtime.Value {
	if class.Index < 0 || int(class.Index) >= len(vm.chunk.Classes) {
		return nil
	}
	indices := vm.chunk.Classes[class.Index].Methods[key]
	if len(indices) == 0 {
		return nil
	}
	return runtime.Function{
		Name: key,
		Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
			functionIndex, err := vm.selectRuntimeFunction(Instruction{}, key, indices, args, 1)
			if err != nil {
				return nil, err
			}
			if err := vm.ensureCallableDecorators(); err != nil {
				return nil, err
			}
			if decorated, ok := vm.decoratedFuncs[functionIndex]; ok {
				return vm.callCallableWithForwardThis(decorated, args, instance)
			}
			callArgs := append([]runtime.Value{instance}, args...)
			return vm.CallFunctionRaw(functionIndex, callArgs)
		},
	}
}

func reflectedRuntimeInstanceMethod(fn, key string, instance *runtime.Instance) (runtime.Value, bool) {
	if fn == "staticMethod" || instance == nil || instance.Class == nil {
		return runtime.Null{}, false
	}
	metadata := runtime.FunctionMetadata{}
	if overloads := instance.Class.MethodMetadata[key]; len(overloads) > 0 {
		metadata = overloads[0]
	}
	methods := instance.Class.Methods[key]
	if len(methods) == 0 && metadata.Name == "" {
		return runtime.Null{}, false
	}
	decorators := append([]runtime.DecoratorMetadata(nil), metadata.Decorators...)
	var callable runtime.Value
	if len(methods) > 0 {
		method := methods[0]
		callable = runtime.Function{
			Name: metadata.Name,
			Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
				if method.Native == nil {
					return nil, fmt.Errorf("reflected method is not callable")
				}
				return method.Native(instance, args)
			},
		}
	}
	if metadata.Name == "" {
		metadata.Name = key
	}
	return runtime.DecoratorTarget{Target: "method", Decorators: decorators, Function: &metadata, Callable: callable}, true
}

func (vm *VM) reflectStaticMethodCallable(class runtime.BytecodeClass, key string) runtime.Value {
	if class.Index < 0 || int(class.Index) >= len(vm.chunk.Classes) {
		return nil
	}
	indices := vm.chunk.Classes[class.Index].StaticMethods[key]
	if len(indices) == 0 {
		return nil
	}
	return runtime.Function{
		Name: key,
		Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
			functionIndex, err := vm.selectRuntimeFunction(Instruction{}, key, indices, args, 0)
			if err != nil {
				return nil, err
			}
			if err := vm.ensureCallableDecorators(); err != nil {
				return nil, err
			}
			if decorated, ok := vm.decoratedFuncs[functionIndex]; ok {
				return vm.callCallable(decorated, args)
			}
			return vm.CallFunctionRaw(functionIndex, args)
		},
	}
}

func reflectDecoratorTarget(value runtime.Value) (runtime.DecoratorTarget, bool) {
	switch value := value.(type) {
	case runtime.DecoratorTarget:
		return value, true
	case runtime.BytecodeFunction:
		return runtime.DecoratorTarget{Target: "function", Decorators: value.Decorators, Function: bytecodeFunctionMetadata(value)}, true
	case runtime.BytecodeClass:
		return runtime.DecoratorTarget{Target: "class", Decorators: value.Decorators}, true
	default:
		return runtime.DecoratorTarget{}, false
	}
}

// reflectLocation returns the source position of a function or class
// declaration as `{module: string, line: int, column: int}`. Returns
// null when the value has no recorded location (native stdlib, etc.).
func (vm *VM) reflectLocation(value runtime.Value) (runtime.Value, error) {
	switch v := value.(type) {
	case runtime.BytecodeFunction:
		if v.DefLine == 0 && v.DefColumn == 0 {
			return runtime.Null{}, nil
		}
		return makeLocationDict(v.Module, v.DefLine, v.DefColumn), nil
	case runtime.BytecodeClosure:
		if int(v.FunctionIndex) >= len(vm.chunk.Functions) {
			return runtime.Null{}, nil
		}
		info := vm.chunk.Functions[v.FunctionIndex]
		if info.DefLine == 0 && info.DefColumn == 0 {
			return runtime.Null{}, nil
		}
		return makeLocationDict(v.Module, info.DefLine, info.DefColumn), nil
	case runtime.BytecodeClass:
		if v.DefLine == 0 && v.DefColumn == 0 {
			return runtime.Null{}, nil
		}
		return makeLocationDict(v.Module, v.DefLine, v.DefColumn), nil
	case runtime.DecoratorTarget:
		if v.Function != nil && (v.Function.DefLine != 0 || v.Function.DefColumn != 0) {
			return makeLocationDict(v.Function.Module, v.Function.DefLine, v.Function.DefColumn), nil
		}
		if v.Class != nil && (v.Class.DefLine != 0 || v.Class.DefColumn != 0) {
			return makeLocationDict(v.Class.Module, v.Class.DefLine, v.Class.DefColumn), nil
		}
		return runtime.Null{}, nil
	case *runtime.Instance:
		if v == nil || v.Class == nil {
			return runtime.Null{}, nil
		}
		if classInfo, ok := vm.classInfo(v.Class.Name); ok {
			if classInfo.DefLine == 0 && classInfo.DefColumn == 0 {
				return runtime.Null{}, nil
			}
			return makeLocationDict(v.Class.Module, classInfo.DefLine, classInfo.DefColumn), nil
		}
		return runtime.Null{}, nil
	}
	return runtime.Null{}, nil
}

func makeLocationDict(module string, line, column int64) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	putBytecodeDict(entries, "module", runtime.String{Value: module})
	putBytecodeDict(entries, "line", runtime.NewInt64(line))
	putBytecodeDict(entries, "column", runtime.NewInt64(column))
	return runtime.Dict{Entries: entries}
}

func reflectFunctionMetadata(value runtime.Value) (runtime.FunctionMetadata, bool) {
	switch value := value.(type) {
	case runtime.DecoratorTarget:
		if value.Function != nil {
			return *value.Function, true
		}
	case runtime.BytecodeFunction:
		metadata := bytecodeFunctionMetadata(value)
		if metadata != nil {
			return *metadata, true
		}
	}
	return runtime.FunctionMetadata{}, false
}

// bytecodeClassFromInfo builds a BytecodeClass value from a class index.
// Used by reflect.class when looking up a class by name or instance so
// the produced value matches the one users get from passing the class
// reference directly.
func (vm *VM) bytecodeClassFromInfo(classInfo ClassInfo, index int64) runtime.BytecodeClass {
	return runtime.BytecodeClass{
		Module:              vm.moduleName,
		Name:                classInfo.Name,
		Doc:                 classInfo.Doc,
		Index:               index,
		Parent:              classInfo.ParentName,
		Fields:              append([]string(nil), classInfo.FieldNames...),
		Interfaces:          append([]string(nil), classInfo.Implements...),
		Decorators:          classInfo.Decorators,
		MethodDecorators:    classInfo.MethodDecorators,
		StaticDecorators:    classInfo.StaticDecorators,
		MethodMetadata:      vm.classFunctionMetadata(classInfo.Methods, "method", 1),
		StaticMetadata:      vm.classFunctionMetadata(classInfo.StaticMethods, "staticMethod", 0),
		ConstructorMetadata: vm.constructorFunctionMetadata(classInfo.ConstructorIndices),
		DefLine:             classInfo.DefLine,
		DefColumn:           classInfo.DefColumn,
	}
}

func reflectClassMetadata(value runtime.Value) (runtime.ClassMetadata, bool) {
	switch value := value.(type) {
	case runtime.DecoratorTarget:
		if value.Class != nil {
			return *value.Class, true
		}
	case runtime.BytecodeClass:
		return bytecodeClassMetadata(value), true
	case *runtime.Class:
		return runtimeClassMetadata(value)
	case *runtime.Instance:
		// Accept an instance and walk to its class so framework
		// code that has the instance in hand doesn't need to
		// recover the class separately.
		if value != nil && value.Class != nil {
			return runtimeClassMetadata(value.Class)
		}
	}
	return runtime.ClassMetadata{}, false
}

// vmPrimitiveTypeMetadata mirrors the evaluator's primitiveTypeMetadata
// for the VM. See evaluator.go for the rationale.
func vmPrimitiveTypeMetadata(value runtime.Value) (runtime.ClassMetadata, bool) {
	switch value.(type) {
	case runtime.List:
		return runtime.ClassMetadata{Name: "list", Methods: vmPrimitiveMethodNamesFor("list")}, true
	case runtime.Dict:
		return runtime.ClassMetadata{Name: "dict", Methods: vmPrimitiveMethodNamesFor("dict")}, true
	case runtime.Set:
		return runtime.ClassMetadata{Name: "set", Methods: vmPrimitiveMethodNamesFor("set")}, true
	case runtime.String:
		return runtime.ClassMetadata{Name: "string", Methods: vmPrimitiveMethodNamesFor("string")}, true
	case runtime.Bytes:
		return runtime.ClassMetadata{Name: "bytes", Methods: vmPrimitiveMethodNamesFor("bytes")}, true
	case runtime.Range:
		return runtime.ClassMetadata{Name: "range", Methods: vmPrimitiveMethodNamesFor("range")}, true
	}
	return runtime.ClassMetadata{}, false
}

func vmPrimitiveMethodNamesFor(typeName string) []string {
	switch typeName {
	case "list":
		return []string{"append", "contains", "filter", "first", "indexOf", "insert", "isEmpty", "join", "last", "length", "map", "pop", "prepend", "push", "remove", "reverse", "set", "slice", "sort", "toList", "unshift"}
	case "dict":
		return []string{"contains", "entries", "get", "insert", "isEmpty", "keys", "length", "remove", "set", "values"}
	case "set":
		return []string{"add", "contains", "difference", "intersection", "isEmpty", "length", "remove", "toList", "union"}
	case "string":
		return []string{"chars", "codeAt", "contains", "endsWith", "format", "indexOf", "isEmpty", "length", "lower", "padLeft", "padRight", "replace", "split", "startsWith", "substring", "toBool", "toDecimal", "toFloat", "toInt", "trim", "trimLeft", "trimRight", "upper"}
	case "bytes":
		return []string{"contains", "get", "isEmpty", "length", "toBase64", "toBase64Url", "toHex", "toString"}
	case "range":
		return []string{"contains", "first", "isEmpty", "last", "length", "toList"}
	}
	return nil
}

func runtimeClassMetadata(value *runtime.Class) (runtime.ClassMetadata, bool) {
	if value == nil {
		return runtime.ClassMetadata{}, false
	}
	methods := map[string]string{}
	for name, overloads := range value.MethodMetadata {
		methods[name] = bytecodeFunctionMetadataName(name, overloads)
	}
	staticMethods := map[string]string{}
	for name, overloads := range value.StaticMetadata {
		staticMethods[name] = bytecodeFunctionMetadataName(name, overloads)
	}
	metadata := runtime.ClassMetadata{
		Name:          value.Name,
		Doc:           value.Doc,
		Methods:       sortedStringMapValues(methods),
		StaticMethods: sortedStringMapValues(staticMethods),
	}
	if value.Parent != nil {
		metadata.Parent = value.Parent.Name
	}
	for _, field := range value.Fields {
		metadata.Fields = append(metadata.Fields, field.Name)
	}
	for _, iface := range value.Implements {
		metadata.Interfaces = append(metadata.Interfaces, iface.Name)
	}
	sort.Strings(metadata.Fields)
	sort.Strings(metadata.Interfaces)
	return metadata, len(metadata.Methods) > 0 || len(metadata.StaticMethods) > 0 || len(metadata.Fields) > 0 || metadata.Name != ""
}

func bytecodeClassMetadata(value runtime.BytecodeClass) runtime.ClassMetadata {
	methods := map[string]string{}
	for name, overloads := range value.MethodMetadata {
		methods[name] = bytecodeFunctionMetadataName(name, overloads)
	}
	staticMethods := map[string]string{}
	for name, overloads := range value.StaticMetadata {
		staticMethods[name] = bytecodeFunctionMetadataName(name, overloads)
	}
	metadata := runtime.ClassMetadata{
		Name:          value.Name,
		Doc:           value.Doc,
		Parent:        value.Parent,
		Fields:        append([]string(nil), value.Fields...),
		Methods:       sortedStringMapValues(methods),
		StaticMethods: sortedStringMapValues(staticMethods),
		Interfaces:    append([]string(nil), value.Interfaces...),
		Module:        value.Module,
		DefLine:       value.DefLine,
		DefColumn:     value.DefColumn,
	}
	sort.Strings(metadata.Fields)
	sort.Strings(metadata.Interfaces)
	return metadata
}

func bytecodeFunctionMetadataName(fallback string, overloads []runtime.FunctionMetadata) string {
	if len(overloads) > 0 && overloads[0].Name != "" {
		if _, name, ok := strings.Cut(overloads[0].Name, "."); ok {
			return name
		}
		return overloads[0].Name
	}
	return fallback
}

func sortedStringMapValues(values map[string]string) []string {
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

func bytecodeStringList(values []string) runtime.List {
	elements := make([]runtime.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, runtime.String{Value: value})
	}
	return runtime.List{Elements: elements}
}

func bytecodeFunctionMetadata(value runtime.BytecodeFunction) *runtime.FunctionMetadata {
	return &runtime.FunctionMetadata{
		Name:           value.Name,
		Target:         "function",
		Doc:            value.Doc,
		TypeParameters: append([]string(nil), value.TypeParameters...),
		Parameters:     append([]runtime.ParameterMetadata(nil), value.Parameters...),
		ReturnType:     value.ReturnType,
		Async:          value.Async,
		Variadic:       value.Variadic,
		Decorators:     append([]runtime.DecoratorMetadata(nil), value.Decorators...),
		Module:         value.Module,
		DefLine:        value.DefLine,
		DefColumn:      value.DefColumn,
	}
}

func classMethodMetadata(class runtime.BytecodeClass, key string, static bool) *runtime.FunctionMetadata {
	var methods []runtime.FunctionMetadata
	if static {
		methods = class.StaticMetadata[key]
	} else {
		methods = class.MethodMetadata[key]
	}
	if len(methods) == 0 {
		return nil
	}
	metadata := methods[0]
	return &metadata
}

// decoratorMetadataDictFromAST mirrors decoratorMetadataDict for AST-
// shaped decorators carried on runtime.Class.Fields. Used by the
// cross-chunk reflect.fields path where the field's decorators were
// rebuilt from ast nodes (see decoratorsToAST). Only literal-shape
// args are reproduced; the named-args map is preserved for parity.
func decoratorMetadataDictFromAST(decorator ast.Decorator) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	name := ""
	if decorator.Name != nil {
		name = decorator.Name.Value
	}
	putBytecodeDict(entries, "name", runtime.String{Value: name})
	putBytecodeDict(entries, "target", runtime.String{Value: "field"})
	args := []runtime.Value{}
	named := map[string]runtime.DictEntry{}
	for _, arg := range decorator.Arguments {
		v := literalValueForExpression(arg.Value)
		if arg.Name != nil {
			putBytecodeDict(named, arg.Name.Value, v)
		} else {
			args = append(args, v)
		}
	}
	putBytecodeDict(entries, "args", runtime.List{Elements: args})
	putBytecodeDict(entries, "namedArgs", runtime.Dict{Entries: named})
	return runtime.Dict{Entries: entries}
}

// literalValueForExpression is the inverse of literalExpressionForValue
// (used during decoratorsToAST). For decorator-arg AST nodes we know
// were rebuilt from literal values, recover the runtime value.
func literalValueForExpression(expr ast.Expression) runtime.Value {
	switch e := expr.(type) {
	case *ast.StringLiteral:
		return runtime.String{Value: e.Value}
	case *ast.IntegerLiteral:
		n, err := runtime.NewIntLiteral(e.Value)
		if err == nil && n.Value.IsInt64() {
			return runtime.SmallInt{Value: n.Value.Int64()}
		}
		if err == nil {
			return n
		}
	case *ast.FloatLiteral:
		f, err := strconv.ParseFloat(e.Value, 64)
		if err == nil {
			return runtime.Float{Value: f}
		}
	case *ast.Literal:
		switch v := e.Value.(type) {
		case bool:
			return runtime.Bool{Value: v}
		case nil:
			return runtime.Null{}
		}
	}
	return runtime.Null{}
}

func decoratorMetadataDict(decorator runtime.DecoratorMetadata) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	putBytecodeDict(entries, "name", runtime.String{Value: decorator.Name})
	putBytecodeDict(entries, "target", runtime.String{Value: decorator.Target})
	putBytecodeDict(entries, "position", runtime.NewInt64(decorator.Position))
	putBytecodeDict(entries, "overload", runtime.NewInt64(decorator.Overload))
	putBytecodeDict(entries, "args", runtime.List{Elements: append([]runtime.Value(nil), decorator.Args...)})
	named := map[string]runtime.DictEntry{}
	for name, value := range decorator.NamedArgs {
		putBytecodeDict(named, name, value)
	}
	putBytecodeDict(entries, "namedArgs", runtime.Dict{Entries: named})
	putBytecodeDict(entries, "line", runtime.NewInt64(decorator.Line))
	putBytecodeDict(entries, "column", runtime.NewInt64(decorator.Column))
	return runtime.Dict{Entries: entries}
}

func parameterMetadataDict(parameter runtime.ParameterMetadata) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	putBytecodeDict(entries, "name", runtime.String{Value: parameter.Name})
	putBytecodeDict(entries, "type", runtime.String{Value: parameter.Type})
	putBytecodeDict(entries, "variadic", runtime.Bool{Value: parameter.Variadic})
	putBytecodeDict(entries, "hasDefault", runtime.Bool{Value: parameter.HasDefault})
	return runtime.Dict{Entries: entries}
}

func interfaceMethodMetadataDict(method runtime.FunctionMetadata) runtime.Dict {
	params := make([]runtime.Value, 0, len(method.Parameters))
	for _, parameter := range method.Parameters {
		params = append(params, parameterMetadataDict(parameter))
	}
	entries := map[string]runtime.DictEntry{}
	putBytecodeDict(entries, "name", runtime.String{Value: method.Name})
	if method.Doc == "" {
		putBytecodeDict(entries, "doc", runtime.Null{})
	} else {
		putBytecodeDict(entries, "doc", runtime.String{Value: method.Doc})
	}
	putBytecodeDict(entries, "parameters", runtime.List{Elements: params})
	putBytecodeDict(entries, "returnType", runtime.String{Value: method.ReturnType})
	return runtime.Dict{Entries: entries}
}

func bytecodeDocMetadataDict(doc string) runtime.Dict {
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	lineValues := make([]runtime.Value, 0, len(lines))
	for _, line := range lines {
		lineValues = append(lineValues, runtime.String{Value: line})
	}
	summary := ""
	summaryIndex := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			summary = strings.TrimSpace(line)
			summaryIndex = i
			break
		}
	}
	body := ""
	if summaryIndex >= 0 && summaryIndex+1 < len(lines) {
		body = strings.TrimSpace(strings.Join(lines[summaryIndex+1:], "\n"))
	}
	entries := map[string]runtime.DictEntry{}
	putBytecodeDict(entries, "text", runtime.String{Value: doc})
	putBytecodeDict(entries, "summary", runtime.String{Value: summary})
	putBytecodeDict(entries, "body", runtime.String{Value: body})
	putBytecodeDict(entries, "lines", runtime.List{Elements: lineValues})
	return runtime.Dict{Entries: entries}
}

func putBytecodeDict(entries map[string]runtime.DictEntry, key string, value runtime.Value) {
	keyValue := runtime.String{Value: key}
	entries[native.DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: value}
}

func (vm *VM) statefulNativeCall(module, function string, args []runtime.Value, names []string) (runtime.Value, error) {
	if vm.statefulNative == nil {
		return nil, fmt.Errorf("stateful native module %s is not configured for VM execution", module)
	}
	return vm.statefulNative.CallBuiltin(module, function, vm.wrapStatefulNativeArgs(args), names)
}

func (vm *VM) shouldRouteDirectPrint() bool {
	if vm.statefulNative == nil {
		return false
	}
	router, ok := vm.statefulNative.(directPrintStatefulNative)
	return ok && router.HandleDirectPrint()
}

func (vm *VM) wrapStatefulNativeArgs(args []runtime.Value) []runtime.Value {
	if len(args) == 0 {
		return args
	}
	wrapped := make([]runtime.Value, len(args))
	for i, arg := range args {
		wrapped[i] = vm.wrapStatefulNativeValue(arg)
	}
	return wrapped
}

func (vm *VM) wrapStatefulNativeValue(value runtime.Value) runtime.Value {
	switch callable := value.(type) {
	case runtime.BytecodeFunction:
		// The wrapped Native closure can be invoked later from another
		// goroutine (timer fires, HTTP handler, etc.). From this point on,
		// any concurrent write-back into vm.globals must serialise with
		// the parent's setGlobalVM, so flip bridgeActive monotonically.
		vm.bridgeActive.Store(true)
		return runtime.Function{
			Name: callable.Name,
			Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
				return vm.callCallableSlow(callable, args)
			},
		}
	case runtime.BytecodeClosure:
		vm.bridgeActive.Store(true)
		return runtime.Function{
			Name: callable.Name,
			Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
				return vm.callCallableSlow(callable, args)
			},
		}
	case runtime.List:
		elements := make([]runtime.Value, len(callable.Elements))
		for i, element := range callable.Elements {
			elements[i] = vm.wrapStatefulNativeValue(element)
		}
		return runtime.List{Elements: elements}
	case runtime.Dict:
		entries := make(map[string]runtime.DictEntry, len(callable.Entries))
		for key, entry := range callable.Entries {
			entries[key] = runtime.DictEntry{Key: entry.Key, Value: vm.wrapStatefulNativeValue(entry.Value)}
		}
		return runtime.Dict{Entries: entries}
	default:
		return value
	}
}

func isStatefulNativeModule(module string) bool {
	switch module {
	case "io", "sys", "secrets", "process", "procnative", "sshnative",
		"http", "websocket", "smtp", "web", "db", "ext", "ffinative", "net", "test", "log", "watch",
		"csv", "schema", "serde", "metrics", "trace", "profile", "path", "async", "dotenv", "cli":
		return true
	default:
		return false
	}
}

func isStatefulNativeCall(module, function string) bool {
	if native.IsPureBuiltin(module, function) {
		return false
	}
	if isStatefulNativeModule(module) {
		return true
	}
	switch module {
	case "json", "xml", "yaml":
		return function == "reader" || function == "stream"
	default:
		return false
	}
}

func (vm *VM) collectionsNativeCall(fn string, args []runtime.Value) (runtime.Value, error) {
	switch fn {
	case "length":
		if len(args) != 1 {
			return nil, fmt.Errorf("collections.length expects one argument")
		}
		switch v := args[0].(type) {
		case runtime.List:
			return runtime.SmallInt{Value: int64(len(v.Elements))}, nil
		case runtime.Dict:
			return runtime.SmallInt{Value: int64(len(v.Entries))}, nil
		case runtime.Set:
			return runtime.SmallInt{Value: int64(len(v.Elements))}, nil
		case runtime.String:
			return runtime.SmallInt{Value: int64(len([]rune(v.Value)))}, nil
		default:
			return nil, fmt.Errorf("collections.length does not support %s", args[0].TypeName())
		}
	case "isEmpty":
		if len(args) != 1 {
			return nil, fmt.Errorf("collections.isEmpty expects one argument")
		}
		switch v := args[0].(type) {
		case runtime.List:
			return runtime.Bool{Value: len(v.Elements) == 0}, nil
		case runtime.Dict:
			return runtime.Bool{Value: len(v.Entries) == 0}, nil
		case runtime.Set:
			return runtime.Bool{Value: len(v.Elements) == 0}, nil
		case runtime.String:
			return runtime.Bool{Value: len(v.Value) == 0}, nil
		default:
			return nil, fmt.Errorf("collections.isEmpty does not support %s", args[0].TypeName())
		}
	case "contains":
		if len(args) != 2 {
			return nil, fmt.Errorf("collections.contains expects two arguments")
		}
		switch v := args[0].(type) {
		case runtime.List:
			for _, el := range v.Elements {
				if valuesEqual(el, args[1]) {
					return runtime.Bool{Value: true}, nil
				}
			}
			return runtime.Bool{Value: false}, nil
		case runtime.Dict:
			_, ok := v.Entries[dictKeyFor(args[1])]
			return runtime.Bool{Value: ok}, nil
		case runtime.Set:
			_, ok := v.Elements[dictKeyFor(args[1])]
			return runtime.Bool{Value: ok}, nil
		case runtime.String:
			sub, ok := args[1].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("collections.contains string needle must be string")
			}
			return runtime.Bool{Value: strings.Contains(v.Value, sub.Value)}, nil
		default:
			return nil, fmt.Errorf("collections.contains does not support %s", args[0].TypeName())
		}
	case "reverse":
		if len(args) != 1 {
			return nil, fmt.Errorf("collections.reverse expects one argument")
		}
		switch v := args[0].(type) {
		case runtime.List:
			out := make([]runtime.Value, len(v.Elements))
			for i, el := range v.Elements {
				out[len(v.Elements)-1-i] = el
			}
			return runtime.List{Elements: out}, nil
		case runtime.String:
			runes := []rune(v.Value)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return runtime.String{Value: string(runes)}, nil
		case runtime.Bytes:
			out := append([]byte(nil), v.Value...)
			for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
				out[i], out[j] = out[j], out[i]
			}
			return runtime.Bytes{Value: out}, nil
		default:
			return nil, fmt.Errorf("collections.reverse does not support %s", args[0].TypeName())
		}
	case "sort":
		if len(args) != 1 {
			return nil, fmt.Errorf("collections.sort expects one argument")
		}
		list, ok := args[0].(runtime.List)
		if !ok {
			return nil, fmt.Errorf("collections.sort expects list")
		}
		out := make([]runtime.Value, len(list.Elements))
		copy(out, list.Elements)
		var sortErr error
		sort.SliceStable(out, func(i, j int) bool {
			if sortErr != nil {
				return false
			}
			cmp, err := valuesCompare(out[i], out[j])
			if err != nil {
				sortErr = err
				return false
			}
			return cmp < 0
		})
		if sortErr != nil {
			return nil, sortErr
		}
		return runtime.List{Elements: out}, nil
	case "join":
		if len(args) != 2 {
			return nil, fmt.Errorf("collections.join expects list and separator")
		}
		list, ok := args[0].(runtime.List)
		if !ok {
			return nil, fmt.Errorf("collections.join expects list")
		}
		sep, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("collections.join separator must be string")
		}
		parts := make([]string, 0, len(list.Elements))
		for _, el := range list.Elements {
			if s, ok := el.(runtime.String); ok {
				parts = append(parts, s.Value)
			} else {
				parts = append(parts, el.Inspect())
			}
		}
		return runtime.String{Value: strings.Join(parts, sep.Value)}, nil
	case "range":
		return collectionsNativeRange(args)
	case "take":
		return vm.collectionsNativeTake(args)
	case "lazyMap":
		return vm.collectionsNativeLazyMap(args)
	case "lazyFilter":
		return vm.collectionsNativeLazyFilter(args)
	case "bfs", "dfs", "topologicalSort", "shortestPath":
		if len(args) == 0 {
			return nil, fmt.Errorf("collections.%s expects a graph dict as first argument", fn)
		}
		graph, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("collections.%s expects dict as first argument (adjacency graph)", fn)
		}
		result, _, err := vm.dictCollectionsMethod(graph, fn, args[1:])
		return result, err
	case "map", "filter", "reduce", "find", "any", "all", "flatten", "unique", "zip", "sorted",
		"groupBy", "partition",
		"findLast", "containsBy", "indexBy", "binarySearch", "lowerBound", "upperBound",
		"minBy", "maxBy", "sortBy", "topBy", "sumBy", "averageBy",
		"topK", "bottomK", "frequencies", "mode",
		"difference", "intersection", "differenceBy", "intersectionBy", "zipWith":
		if len(args) == 0 {
			return nil, fmt.Errorf("collections.%s expects at least a collection argument", fn)
		}
		list, ok := args[0].(runtime.List)
		if !ok {
			return nil, fmt.Errorf("collections.%s expects list as first argument", fn)
		}
		result, _, err := vm.listHigherOrderMethod(Instruction{}, list, fn, args[1:])
		return result, err
	case "chunk":
		if len(args) != 2 {
			return nil, fmt.Errorf("collections.chunk expects list and size")
		}
		list, ok := args[0].(runtime.List)
		if !ok {
			return nil, fmt.Errorf("collections.chunk expects list as first argument")
		}
		result, _, err := vm.listHigherOrderMethod(Instruction{}, list, "chunk", args[1:])
		return result, err
	default:
		return nil, fmt.Errorf("unknown collections function: %s", fn)
	}
}

type collectionsNativeIterator struct {
	next  func() (runtime.Value, bool, error)
	close func()
}

func collectionsNativeIntArg(value runtime.Value, label string) (*big.Int, error) {
	nb, ok := native.IntValueToBigInt(value)
	if !ok {
		return nil, fmt.Errorf("%s must be int", label)
	}
	return new(big.Int).Set(nb), nil
}

func collectionsNativeRangeContains(current, end, step *big.Int, exclusive bool) bool {
	cmp := current.Cmp(end)
	if step.Sign() > 0 {
		if exclusive {
			return cmp < 0
		}
		return cmp <= 0
	}
	if exclusive {
		return cmp > 0
	}
	return cmp >= 0
}

func collectionsNativeIteratorFor(value runtime.Value, label string) (collectionsNativeIterator, error) {
	switch v := value.(type) {
	case runtime.List:
		index := 0
		return collectionsNativeIterator{next: func() (runtime.Value, bool, error) {
			if index >= len(v.Elements) {
				return nil, false, nil
			}
			next := v.Elements[index]
			index++
			return next, true, nil
		}}, nil
	case *runtime.Generator:
		return collectionsNativeIterator{next: v.Next, close: v.Close}, nil
	case runtime.Range:
		current := new(big.Int).Set(v.Start)
		end := new(big.Int).Set(v.End)
		step := new(big.Int).Set(v.Step)
		return collectionsNativeIterator{next: func() (runtime.Value, bool, error) {
			if !collectionsNativeRangeContains(current, end, step, v.Exclusive) {
				return nil, false, nil
			}
			out := runtime.Int{Value: new(big.Int).Set(current)}
			current.Add(current, step)
			return out, true, nil
		}}, nil
	default:
		return collectionsNativeIterator{}, fmt.Errorf("%s expects iterable", label)
	}
}

func collectionsNativeRange(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return nil, fmt.Errorf("collections.range expects end, optional start/end, or start/end/step")
	}
	start := big.NewInt(0)
	end, err := collectionsNativeIntArg(args[0], "collections.range end")
	if err != nil {
		return nil, err
	}
	if len(args) >= 2 {
		start, err = collectionsNativeIntArg(args[0], "collections.range start")
		if err != nil {
			return nil, err
		}
		end, err = collectionsNativeIntArg(args[1], "collections.range end")
		if err != nil {
			return nil, err
		}
	}
	step := big.NewInt(1)
	if len(args) == 3 {
		step, err = collectionsNativeIntArg(args[2], "collections.range step")
		if err != nil {
			return nil, err
		}
		if step.Sign() == 0 {
			return nil, fmt.Errorf("collections.range step cannot be zero")
		}
	}
	current := new(big.Int).Set(start)
	return runtime.NewGenerator(func() (runtime.Value, bool, error) {
		if !collectionsNativeRangeContains(current, end, step, true) {
			return nil, false, nil
		}
		out := runtime.Int{Value: new(big.Int).Set(current)}
		current.Add(current, step)
		return out, true, nil
	}), nil
}

func (vm *VM) collectionsNativeTake(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("collections.take expects iterable and count")
	}
	source, err := collectionsNativeIteratorFor(args[0], "collections.take")
	if err != nil {
		return nil, err
	}
	count, err := collectionsNativeIntArg(args[1], "collections.take count")
	if err != nil {
		return nil, err
	}
	if count.Sign() < 0 {
		return nil, fmt.Errorf("collections.take count cannot be negative")
	}
	remaining := new(big.Int).Set(count)
	return runtime.NewClosableGenerator(func() (runtime.Value, bool, error) {
		if remaining.Sign() <= 0 {
			if source.close != nil {
				source.close()
			}
			return nil, false, nil
		}
		next, ok, err := source.next()
		if err != nil || !ok {
			if source.close != nil {
				source.close()
			}
			return next, ok, err
		}
		remaining.Sub(remaining, big.NewInt(1))
		return next, true, nil
	}, func() {
		if source.close != nil {
			source.close()
		}
	}), nil
}

func (vm *VM) collectionsNativeLazyMap(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("collections.lazyMap expects iterable and function")
	}
	source, err := collectionsNativeIteratorFor(args[0], "collections.lazyMap")
	if err != nil {
		return nil, err
	}
	fn := args[1]
	return runtime.NewClosableGenerator(func() (runtime.Value, bool, error) {
		next, ok, err := source.next()
		if err != nil || !ok {
			if source.close != nil {
				source.close()
			}
			return next, ok, err
		}
		mapped, err := vm.callCallable(fn, []runtime.Value{next})
		if err != nil {
			if source.close != nil {
				source.close()
			}
			return nil, false, err
		}
		return mapped, true, nil
	}, func() {
		if source.close != nil {
			source.close()
		}
	}), nil
}

func (vm *VM) collectionsNativeLazyFilter(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("collections.lazyFilter expects iterable and function")
	}
	source, err := collectionsNativeIteratorFor(args[0], "collections.lazyFilter")
	if err != nil {
		return nil, err
	}
	fn := args[1]
	return runtime.NewClosableGenerator(func() (runtime.Value, bool, error) {
		for {
			next, ok, err := source.next()
			if err != nil || !ok {
				if source.close != nil {
					source.close()
				}
				return next, ok, err
			}
			keep, err := vm.callCallable(fn, []runtime.Value{next})
			if err != nil {
				if source.close != nil {
					source.close()
				}
				return nil, false, err
			}
			if b, ok := keep.(runtime.Bool); ok && b.Value {
				return next, true, nil
			}
		}
	}, func() {
		if source.close != nil {
			source.close()
		}
	}), nil
}

// reorderMethodNamedArgs reorders deferred-method-call arguments
// against the method's actual ParamNames so the runtime dispatch sees
// values in positional order. The receiver counts as paramOffset=1.
func (vm *VM) reorderMethodNamedArgs(instruction Instruction, instance *runtime.Instance, methodName string, args []runtime.Value, names []string) ([]runtime.Value, error) {
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		return args, nil
	}
	indices, ok := vm.lookupMethod(classInfo, methodName)
	if !ok || len(indices) == 0 {
		return args, nil
	}
	idx, err := vm.selectRuntimeFunction(instruction, methodName, indices, args, 1)
	if err != nil {
		return nil, err
	}
	function := vm.chunk.Functions[idx]
	return vm.orderRuntimeArguments(instruction, function, args, names, 1)
}

// reorderCallableNamedArgs reorders deferred-callable-call arguments
// using the callable's signature. BytecodeFunction / BytecodeClosure
// expose a FunctionIndex pointing at ParamNames; other callable kinds
// (native functions, Instance.__invoke) can't be reordered statically,
// so the names list is rejected up front by the compiler in those
// cases - here we fall back to the positional order if the callable
// shape is unrecognised.
func (vm *VM) reorderCallableNamedArgs(instruction Instruction, callee runtime.Value, args []runtime.Value, names []string) ([]runtime.Value, error) {
	var funcIdx int64 = -1
	switch v := callee.(type) {
	case runtime.BytecodeFunction:
		funcIdx = v.Index
	case runtime.BytecodeClosure:
		funcIdx = v.FunctionIndex
	default:
		return args, nil
	}
	if funcIdx < 0 || int(funcIdx) >= len(vm.chunk.Functions) {
		return args, nil
	}
	function := vm.chunk.Functions[funcIdx]
	offset := 0
	if closure, ok := callee.(runtime.BytecodeClosure); ok {
		offset = len(closure.Upvalues)
	}
	return vm.orderRuntimeArguments(instruction, function, args, names, offset)
}

// readArgNames decodes the trailing name-index operands carried by
// OpDefer*CallNamed. Each entry is a constant-pool index pointing to
// a String holding the argument name, or -1 for a positional arg.
func (vm *VM) readArgNames(instruction Instruction, nameOperands []int64) ([]string, error) {
	names := make([]string, len(nameOperands))
	for i, idx := range nameOperands {
		if idx < 0 {
			continue
		}
		name, err := vm.constantStringAt(instruction, idx, "argument name constant must be string")
		if err != nil {
			return nil, err
		}
		names[i] = name
	}
	return names, nil
}

func (vm *VM) addDefer(action deferredAction) {
	current := len(vm.defers) - 1
	vm.defers[current] = append(vm.defers[current], action)
}

func (vm *VM) runDefers(instruction Instruction) error {
	if len(vm.defers) == 0 {
		return nil
	}
	current := len(vm.defers) - 1
	actions := vm.defers[current]
	vm.defers = vm.defers[:current]
	for i := len(actions) - 1; i >= 0; i-- {
		action := actions[i]
		switch action.kind {
		case deferKindPrint:
			if vm.shouldRouteDirectPrint() {
				if _, err := vm.statefulNativeCall("io", "print", []runtime.Value{action.value}, nil); err != nil {
					return vm.runtimeError(instruction, "deferred print: %v", err)
				}
				continue
			}
			if _, err := fmt.Fprint(vm.stdout, action.value.Inspect()); err != nil {
				return err
			}
		case deferKindPrintln:
			if vm.shouldRouteDirectPrint() {
				if _, err := vm.statefulNativeCall("io", "println", []runtime.Value{action.value}, nil); err != nil {
					return vm.runtimeError(instruction, "deferred println: %v", err)
				}
				continue
			}
			if _, err := fmt.Fprintln(vm.stdout, action.value.Inspect()); err != nil {
				return err
			}
		case deferKindNative:
			if len(action.names) > 0 {
				if _, err := vm.evalNativeCallWithNames(action.name, action.args, action.names); err != nil {
					return vm.runtimeError(instruction, "deferred call %s: %v", action.name, err)
				}
			} else if _, err := vm.evalNativeCall(action.name, action.args); err != nil {
				return vm.runtimeError(instruction, "deferred call %s: %v", action.name, err)
			}
		case deferKindFunc:
			if _, err := vm.CallFunction(action.funcIdx, action.args); err != nil {
				return vm.runtimeError(instruction, "deferred call: %v", err)
			}
		case deferKindMethod:
			instance, ok := action.receiver.(*runtime.Instance)
			if !ok {
				return vm.runtimeError(instruction, "deferred method call receiver is not an instance")
			}
			args := action.args
			if len(action.names) > 0 {
				reordered, err := vm.reorderMethodNamedArgs(instruction, instance, action.name, action.args, action.names)
				if err != nil {
					return vm.runtimeError(instruction, "deferred method call %s: %v", action.name, err)
				}
				args = reordered
			}
			if _, err := vm.CallMethod(instance, action.name, args); err != nil {
				return vm.runtimeError(instruction, "deferred method call %s: %v", action.name, err)
			}
		case deferKindCallable:
			args := action.args
			if len(action.names) > 0 {
				reordered, err := vm.reorderCallableNamedArgs(instruction, action.value, action.args, action.names)
				if err != nil {
					return vm.runtimeError(instruction, "deferred callable call: %v", err)
				}
				args = reordered
			}
			if _, err := vm.callCallable(action.value, args); err != nil {
				return vm.runtimeError(instruction, "deferred callable call: %v", err)
			}
		default:
			return vm.runtimeError(instruction, "unknown deferred action kind")
		}
	}
	return nil
}

func (vm *VM) Exports() (map[string]runtime.Value, error) {
	exports := map[string]runtime.Value{}
	for _, export := range vm.chunk.Exports {
		if export.FunctionIndex >= 0 {
			function := runtime.BytecodeFunction{Name: export.Name, Index: export.FunctionIndex, Module: vm.moduleName}
			if int(export.FunctionIndex) < len(vm.chunk.Functions) {
				info := vm.chunk.Functions[export.FunctionIndex]
				function.Doc = info.Doc
				function.TypeParameters = append([]string(nil), info.TypeParameters...)
				function.Parameters = parameterMetadataFromFunctionInfo(info, 0)
				function.ReturnType = info.ReturnType
				function.Async = info.Async
				function.Variadic = info.Variadic
				function.Decorators = append([]runtime.DecoratorMetadata(nil), info.Decorators...)
				function.DefLine = info.DefLine
				function.DefColumn = info.DefColumn
			}
			exports[export.Name] = function
			continue
		}
		if export.ClassIndex >= 0 {
			decorators := []runtime.DecoratorMetadata(nil)
			methodDecorators := map[string][]runtime.DecoratorMetadata(nil)
			staticDecorators := map[string][]runtime.DecoratorMetadata(nil)
			methodMetadata := map[string][]runtime.FunctionMetadata(nil)
			staticMetadata := map[string][]runtime.FunctionMetadata(nil)
			if int(export.ClassIndex) < len(vm.chunk.Classes) {
				classInfo := vm.chunk.Classes[export.ClassIndex]
				decorators = classInfo.Decorators
				methodDecorators = classInfo.MethodDecorators
				staticDecorators = classInfo.StaticDecorators
				methodMetadata = vm.classFunctionMetadata(classInfo.Methods, "method", 1)
				staticMetadata = vm.classFunctionMetadata(classInfo.StaticMethods, "staticMethod", 0)
			}
			if dec, exists := vm.decoratedClasses[export.ClassIndex]; exists {
				exports[export.Name] = dec
				continue
			}
			classValue := runtime.BytecodeClass{Name: export.Name, Index: export.ClassIndex, Decorators: decorators, MethodDecorators: methodDecorators, StaticDecorators: staticDecorators, MethodMetadata: methodMetadata, StaticMetadata: staticMetadata}
			if int(export.ClassIndex) < len(vm.chunk.Classes) {
				classInfo := vm.chunk.Classes[export.ClassIndex]
				classValue.Doc = classInfo.Doc
				classValue.Parent = classInfo.ParentName
				classValue.Fields = append([]string(nil), classInfo.FieldNames...)
				classValue.Interfaces = append([]string(nil), classInfo.Implements...)
				classValue.ConstructorMetadata = vm.constructorFunctionMetadata(classInfo.ConstructorIndices)
				sort.Strings(classValue.Fields)
				sort.Strings(classValue.Interfaces)
			}
			exports[export.Name] = classValue
			continue
		}
		value, err := vm.getGlobal(export.Slot)
		if err != nil {
			return nil, fmt.Errorf("export %s: %w", export.Name, err)
		}
		exports[export.Name] = value
	}
	return exports, nil
}

func (vm *VM) constructorFunctionMetadata(indices []int64) []runtime.FunctionMetadata {
	result := make([]runtime.FunctionMetadata, 0, len(indices))
	for _, index := range indices {
		if index < 0 || int(index) >= len(vm.chunk.Functions) {
			continue
		}
		info := vm.chunk.Functions[index]
		result = append(result, runtime.FunctionMetadata{
			Name:       info.Name,
			Target:     "constructor",
			Doc:        info.Doc,
			Parameters: parameterMetadataFromFunctionInfo(info, 1),
			ReturnType: info.ReturnType,
			Async:      info.Async,
			Variadic:   info.Variadic,
			Decorators: append([]runtime.DecoratorMetadata(nil), info.Decorators...),
			Module:     vm.moduleName,
			DefLine:    info.DefLine,
			DefColumn:  info.DefColumn,
		})
	}
	return result
}

func (vm *VM) classFunctionMetadata(methods map[string][]int64, target string, paramOffset int) map[string][]runtime.FunctionMetadata {
	metadata := map[string][]runtime.FunctionMetadata{}
	for name, indices := range methods {
		for _, index := range indices {
			if index < 0 || int(index) >= len(vm.chunk.Functions) {
				continue
			}
			info := vm.chunk.Functions[index]
			metadata[name] = append(metadata[name], runtime.FunctionMetadata{
				Name:           info.Name,
				Target:         target,
				Doc:            info.Doc,
				TypeParameters: append([]string(nil), info.TypeParameters...),
				Parameters:     parameterMetadataFromFunctionInfo(info, paramOffset),
				ReturnType:     info.ReturnType,
				Async:          info.Async,
				Variadic:       info.Variadic,
				Decorators:     append([]runtime.DecoratorMetadata(nil), info.Decorators...),
				Module:         vm.moduleName,
				DefLine:        info.DefLine,
				DefColumn:      info.DefColumn,
			})
		}
	}
	return metadata
}

func (vm *VM) reflectConstructors(arg runtime.Value) (runtime.Value, error) {
	/* Cross-chunk class: dispatch through the moduleLoader so the
	 * index resolves against the chunk that declared the class. */
	if bc, ok := arg.(runtime.BytecodeClass); ok && bc.Module != vm.moduleName && vm.moduleLoader != nil {
		return vm.moduleLoader.ConstructorsForModuleClass(bc)
	}
	var classIndex int64 = -1
	switch value := arg.(type) {
	case runtime.BytecodeClass:
		classIndex = value.Index
	case runtime.DecoratorTarget:
		if value.Class != nil {
			if idx, ok := vm.classIndex[strings.ToLower(value.Class.Name)]; ok {
				classIndex = int64(idx)
			}
		}
	case *runtime.Instance:
		if idx, ok := vm.classIndex[strings.ToLower(value.Class.Name)]; ok {
			classIndex = int64(idx)
		}
	}
	if classIndex < 0 || int(classIndex) >= len(vm.chunk.Classes) {
		return runtime.List{Elements: []runtime.Value{}}, nil
	}
	classInfo := vm.chunk.Classes[classIndex]
	overloads := make([]runtime.Value, 0, len(classInfo.ConstructorIndices))
	for _, idx := range classInfo.ConstructorIndices {
		if idx < 0 || int(idx) >= len(vm.chunk.Functions) {
			continue
		}
		info := vm.chunk.Functions[idx]
		params := parameterMetadataFromFunctionInfo(info, 1) // skip 'this'
		paramValues := make([]runtime.Value, 0, len(params))
		for _, p := range params {
			paramValues = append(paramValues, parameterMetadataDict(p))
		}
		overloads = append(overloads, runtime.List{Elements: paramValues})
	}
	return runtime.List{Elements: overloads}, nil
}

func parameterMetadataFromFunctionInfo(info FunctionInfo, paramOffset int) []runtime.ParameterMetadata {
	if paramOffset > len(info.ParamNames) {
		paramOffset = len(info.ParamNames)
	}
	parameters := make([]runtime.ParameterMetadata, 0, len(info.ParamNames)-paramOffset)
	for i := paramOffset; i < len(info.ParamNames); i++ {
		typ := "any"
		if i < len(info.ParamTypes) && info.ParamTypes[i] != "" {
			typ = info.ParamTypes[i]
		}
		hasDefault := false
		if i < len(info.DefaultConstants) {
			hasDefault = info.DefaultConstants[i] >= 0
		}
		parameters = append(parameters, runtime.ParameterMetadata{
			Name:       info.ParamNames[i],
			Type:       typ,
			Variadic:   info.Variadic && i == len(info.ParamNames)-1,
			HasDefault: hasDefault,
		})
	}
	return parameters
}

func valuesCompare(left, right runtime.Value) (int, error) {
	if ls, ok := left.(runtime.String); ok {
		if rs, ok := right.(runtime.String); ok {
			if ls.Value < rs.Value {
				return -1, nil
			}
			if ls.Value > rs.Value {
				return 1, nil
			}
			return 0, nil
		}
	}
	return native.NumericCompare(left, right)
}

// callBytecodeInline invokes a bytecode function on the current VM without
// rebuilding the chunk. Used as the fast path for callCallable when the
// target is a BytecodeFunction or no-upvalue BytecodeClosure in the same
// module. Returns the function's return value and pops it off the stack.
func (vm *VM) callBytecodeInline(funcIndex int64, args []runtime.Value) (runtime.Value, error) {
	if funcIndex < 0 || int(funcIndex) >= len(vm.chunk.Functions) {
		return nil, fmt.Errorf("function index out of range")
	}
	function := &vm.chunk.Functions[funcIndex]
	if function.IsGenerator || function.Async {
		return nil, fmt.Errorf("inline call does not support generator/async functions")
	}
	baseline := len(vm.frames)
	stackArgs := make([]runtime.VMValue, len(args))
	for i, a := range args {
		stackArgs[i] = runtime.VMValueFromValue(a)
	}
	// runDefers shrinks vm.defers before iterating its actions, so
	// when an action lands here the slice is one slot short of the
	// frame-stack invariant startFunctionVMValue relies on. Restore
	// it for the inline call and snap back to the caller's expected
	// length when Run() returns so the parent runDefers iteration
	// can't read stale slots.
	savedDefersLen := len(vm.defers)
	oldEntryIP := vm.runEntryIP
	oldExitDepth := vm.runInlineExitDepth
	oldSuppress := vm.runSuppressCleanup
	defer func() {
		if len(vm.defers) > savedDefersLen {
			for i := savedDefersLen; i < len(vm.defers); i++ {
				vm.defers[i] = vm.defers[i][:0]
			}
			vm.defers = vm.defers[:savedDefersLen]
		}
		vm.runEntryIP = oldEntryIP
		vm.runInlineExitDepth = oldExitDepth
		vm.runSuppressCleanup = oldSuppress
	}()
	nextIP, err := vm.startFunctionVMValue(Instruction{}, 0, function, stackArgs, nil)
	if err != nil {
		return nil, err
	}
	// startFunctionVMValue returns Entry-1 to compensate for the dispatch
	// loop's ip++ after the calling instruction. We're entering the loop
	// fresh, so increment to the actual function entry.
	vm.runEntryIP = nextIP + 1
	vm.runInlineExitDepth = baseline
	vm.runSuppressCleanup = true
	if err := vm.Run(); err != nil {
		return nil, err
	}
	result, err := vm.popVM()
	if err != nil {
		return nil, err
	}
	return result.ToValue(), nil
}

// callCallableSlow mirrors callCallable but disables the
// callBytecodeInline fast path. Used by stateful native bridges where
// the callback can fire on a different goroutine than the VM dispatch
// loop and inline state mutations would race.
func (vm *VM) callCallableSlow(fn runtime.Value, args []runtime.Value) (runtime.Value, error) {
	old := vm.inDispatchLoop
	vm.inDispatchLoop = false
	defer func() { vm.inDispatchLoop = old }()
	return vm.callCallable(fn, args)
}

func (vm *VM) callCallable(fn runtime.Value, args []runtime.Value) (runtime.Value, error) {
	switch f := fn.(type) {
	case runtime.Function:
		if f.Native == nil {
			return nil, fmt.Errorf("runtime function is not callable by VM")
		}
		return f.Native(nil, args)
	case runtime.OverloadedFunction:
		for _, overload := range f.Overloads {
			if len(overload.Parameters) == len(args) {
				return vm.callCallable(overload, args)
			}
		}
		return nil, fmt.Errorf("no matching overload for %s with %d arguments", f.Name, len(args))
	case runtime.BytecodeFunction:
		if f.Module != vm.moduleName {
			// Cross-chunk function reference - the Index resolves
			// against the defining chunk's function table, not ours.
			// Includes entry-script values (Module=="") that crossed
			// into a stdlib sub-VM.
			if vm.moduleLoader == nil {
				return nil, fmt.Errorf("bytecode module loader is not configured")
			}
			return vm.moduleLoader.CallModuleFunction(f, vm.wrapStatefulNativeArgs(args))
		}
		if f.Raw {
			if vm.methodReceiverFuncs[f.Index] {
				if vm.forwardThis == nil {
					return nil, fmt.Errorf("method receiver is not available")
				}
				args = append([]runtime.Value{vm.forwardThis}, args...)
			}
			return vm.CallFunctionRaw(f.Index, args)
		}
		if vm.inDispatchLoop && !vm.requiresCallSitePolymorphism {
			return vm.callBytecodeInline(f.Index, args)
		}
		return vm.CallFunction(f.Index, args)
	case runtime.BytecodeClosure:
		if f.Module != vm.moduleName {
			// The closure was created in a different chunk - its
			// FunctionIndex resolves against that chunk's function
			// table, not ours. Route through the module loader so
			// the call dispatches in the right VM context. Includes
			// closures from the entry script (Module=="") that
			// crossed into a stdlib module-defined class method.
			if vm.moduleLoader == nil {
				return nil, fmt.Errorf("bytecode module loader is not configured")
			}
			return vm.moduleLoader.CallModuleClosure(f, vm.wrapStatefulNativeArgs(args))
		}
		if vm.inDispatchLoop && len(f.Upvalues) == 0 && f.TypeBindings == nil && !vm.requiresCallSitePolymorphism {
			return vm.callBytecodeInline(f.FunctionIndex, args)
		}
		constants := make([]runtime.Value, 0, len(args)+2)
		constants = append(constants, f)
		constants = append(constants, args...)
		methodNameIndex := int64(len(constants))
		constants = append(constants, runtime.String{Value: "__invoke"})
		wrapper := make([]Instruction, 0, len(args)+3)
		wrapper = append(wrapper, Instruction{Op: OpConstant, Operands: []int64{0}})
		for i := range args {
			wrapper = append(wrapper, Instruction{Op: OpConstant, Operands: []int64{int64(i + 1)}})
		}
		wrapper = append(wrapper, Instruction{Op: OpMethodCall, Operands: []int64{methodNameIndex, int64(len(args))}})
		wrapper = append(wrapper, Instruction{Op: OpReturn})
		return vm.runWrapper(constants, wrapper)
	case *runtime.Instance:
		constants := make([]runtime.Value, 0, len(args)+2)
		constants = append(constants, f)
		constants = append(constants, args...)
		methodNameIndex := int64(len(constants))
		constants = append(constants, runtime.String{Value: "__invoke"})
		wrapper := make([]Instruction, 0, len(args)+3)
		wrapper = append(wrapper, Instruction{Op: OpConstant, Operands: []int64{0}})
		for i := range args {
			wrapper = append(wrapper, Instruction{Op: OpConstant, Operands: []int64{int64(i + 1)}})
		}
		wrapper = append(wrapper, Instruction{Op: OpMethodCall, Operands: []int64{methodNameIndex, int64(len(args))}})
		wrapper = append(wrapper, Instruction{Op: OpReturn})
		return vm.runWrapper(constants, wrapper)
	default:
		return nil, fmt.Errorf("value is not callable")
	}
}

func (vm *VM) listHigherOrderMethod(instruction Instruction, list runtime.List, name string, args []runtime.Value) (runtime.Value, bool, error) {
	switch name {
	case "map":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.map expects one argument (function)")
		}
		result := make([]runtime.Value, len(list.Elements))
		for i, el := range list.Elements {
			mapped, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			result[i] = mapped
		}
		return runtime.List{Elements: result}, true, nil
	case "filter":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.filter expects one argument (function)")
		}
		var result []runtime.Value
		for _, el := range list.Elements {
			keep, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			if b, ok := keep.(runtime.Bool); ok && b.Value {
				result = append(result, el)
			}
		}
		return runtime.List{Elements: result}, true, nil
	case "reduce":
		if len(args) != 2 {
			return nil, true, fmt.Errorf("list.reduce expects two arguments (function, initial)")
		}
		acc := args[1]
		for _, el := range list.Elements {
			next, err := vm.callCallable(args[0], []runtime.Value{acc, el})
			if err != nil {
				return nil, true, err
			}
			acc = next
		}
		return acc, true, nil
	case "find":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.find expects one argument (function)")
		}
		for _, el := range list.Elements {
			match, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			if b, ok := match.(runtime.Bool); ok && b.Value {
				return el, true, nil
			}
		}
		return runtime.Null{}, true, nil
	case "any":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.any expects one argument (function)")
		}
		for _, el := range list.Elements {
			result, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			if b, ok := result.(runtime.Bool); ok && b.Value {
				return runtime.Bool{Value: true}, true, nil
			}
		}
		return runtime.Bool{Value: false}, true, nil
	case "all":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.all expects one argument (function)")
		}
		for _, el := range list.Elements {
			result, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			if b, ok := result.(runtime.Bool); ok && !b.Value {
				return runtime.Bool{Value: false}, true, nil
			}
		}
		return runtime.Bool{Value: true}, true, nil
	case "count":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.count expects one argument (function)")
		}
		n := 0
		for _, el := range list.Elements {
			result, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			if b, ok := result.(runtime.Bool); ok && b.Value {
				n++
			}
		}
		return runtime.NewInt64(int64(n)), true, nil
	case "sorted", "sort":
		if len(args) > 1 {
			return nil, true, fmt.Errorf("list.%s expects zero or one argument", name)
		}
		newElements := make([]runtime.Value, len(list.Elements))
		copy(newElements, list.Elements)
		var sortErr error
		sort.SliceStable(newElements, func(i, j int) bool {
			if sortErr != nil {
				return false
			}
			if len(args) == 1 {
				result, err := vm.callCallable(args[0], []runtime.Value{newElements[i], newElements[j]})
				if err != nil {
					sortErr = err
					return false
				}
				b, ok := result.(runtime.Bool)
				return ok && b.Value
			}
			cmp, err := valuesCompare(newElements[i], newElements[j])
			if err != nil {
				sortErr = err
				return false
			}
			return cmp < 0
		})
		if sortErr != nil {
			return nil, true, sortErr
		}
		return runtime.List{Elements: newElements}, true, nil
	case "flatten":
		if len(args) != 0 {
			return nil, true, fmt.Errorf("list.flatten expects no arguments")
		}
		var result []runtime.Value
		for _, el := range list.Elements {
			if nested, ok := el.(runtime.List); ok {
				result = append(result, nested.Elements...)
			} else {
				result = append(result, el)
			}
		}
		return runtime.List{Elements: result}, true, nil
	case "unique":
		if len(args) != 0 {
			return nil, true, fmt.Errorf("list.unique expects no arguments")
		}
		seen := make([]runtime.Value, 0, len(list.Elements))
		var result []runtime.Value
		for _, el := range list.Elements {
			found := false
			for _, s := range seen {
				if valuesEqual(el, s) {
					found = true
					break
				}
			}
			if !found {
				seen = append(seen, el)
				result = append(result, el)
			}
		}
		return runtime.List{Elements: result}, true, nil
	case "zip":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.zip expects one argument (list)")
		}
		other, ok := args[0].(runtime.List)
		if !ok {
			return nil, true, fmt.Errorf("list.zip expects list argument")
		}
		n := len(list.Elements)
		if len(other.Elements) < n {
			n = len(other.Elements)
		}
		result := make([]runtime.Value, n)
		for i := 0; i < n; i++ {
			result[i] = runtime.List{Elements: []runtime.Value{list.Elements[i], other.Elements[i]}}
		}
		return runtime.List{Elements: result}, true, nil
	case "groupBy":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.groupBy expects one argument (function)")
		}
		entries := map[string]runtime.DictEntry{}
		for _, el := range list.Elements {
			key, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			dk := native.DictKey(key)
			existing, ok := entries[dk]
			if !ok {
				existing = runtime.DictEntry{Key: key, Value: runtime.List{}}
			}
			existing.Value = runtime.List{Elements: append(existing.Value.(runtime.List).Elements, el)}
			entries[dk] = existing
		}
		return runtime.Dict{Entries: entries}, true, nil
	case "chunk":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.chunk expects one argument (size)")
		}
		nInt, ok := native.AsInt64(args[0])
		if !ok {
			return nil, true, fmt.Errorf("list.chunk size must be int")
		}
		n := int(nInt)
		if n <= 0 {
			return nil, true, fmt.Errorf("list.chunk size must be positive")
		}
		var chunks []runtime.Value
		for i := 0; i < len(list.Elements); i += n {
			end := i + n
			if end > len(list.Elements) {
				end = len(list.Elements)
			}
			chunks = append(chunks, runtime.List{Elements: append([]runtime.Value(nil), list.Elements[i:end]...)})
		}
		return runtime.List{Elements: chunks}, true, nil
	case "partition":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.partition expects one argument (function)")
		}
		var yes, no []runtime.Value
		for _, el := range list.Elements {
			result, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			if b, ok := result.(runtime.Bool); ok && b.Value {
				yes = append(yes, el)
			} else {
				no = append(no, el)
			}
		}
		return runtime.List{Elements: []runtime.Value{
			runtime.List{Elements: yes},
			runtime.List{Elements: no},
		}}, true, nil
	case "findLast":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.findLast expects one argument (function)")
		}
		for i := len(list.Elements) - 1; i >= 0; i-- {
			result, err := vm.callCallable(args[0], []runtime.Value{list.Elements[i]})
			if err != nil {
				return nil, true, err
			}
			if b, ok := result.(runtime.Bool); ok && b.Value {
				return list.Elements[i], true, nil
			}
		}
		return runtime.Null{}, true, nil
	case "containsBy":
		if len(args) != 2 {
			return nil, true, fmt.Errorf("list.containsBy expects two arguments (value, function)")
		}
		target, fn := args[0], args[1]
		for _, el := range list.Elements {
			key, err := vm.callCallable(fn, []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			if valuesEqual(key, target) {
				return runtime.Bool{Value: true}, true, nil
			}
		}
		return runtime.Bool{Value: false}, true, nil
	case "indexBy":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.indexBy expects one argument (function)")
		}
		for i, el := range list.Elements {
			result, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			if b, ok := result.(runtime.Bool); ok && b.Value {
				return runtime.NewInt64(int64(i)), true, nil
			}
		}
		return runtime.NewInt64(-1), true, nil
	case "binarySearch":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.binarySearch expects one argument (value)")
		}
		target := args[0]
		lo, hi := 0, len(list.Elements)
		for lo < hi {
			mid := (lo + hi) / 2
			cmp, err := valuesCompare(list.Elements[mid], target)
			if err != nil {
				return nil, true, err
			}
			if cmp == 0 {
				return runtime.NewInt64(int64(mid)), true, nil
			} else if cmp < 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return runtime.NewInt64(-1), true, nil
	case "lowerBound":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.lowerBound expects one argument (value)")
		}
		target := args[0]
		lo, hi := 0, len(list.Elements)
		for lo < hi {
			mid := (lo + hi) / 2
			cmp, err := valuesCompare(list.Elements[mid], target)
			if err != nil {
				return nil, true, err
			}
			if cmp < 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return runtime.NewInt64(int64(lo)), true, nil
	case "upperBound":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.upperBound expects one argument (value)")
		}
		target := args[0]
		lo, hi := 0, len(list.Elements)
		for lo < hi {
			mid := (lo + hi) / 2
			cmp, err := valuesCompare(list.Elements[mid], target)
			if err != nil {
				return nil, true, err
			}
			if cmp <= 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return runtime.NewInt64(int64(lo)), true, nil
	case "minBy":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.minBy expects one argument (function)")
		}
		if len(list.Elements) == 0 {
			return runtime.Null{}, true, nil
		}
		best := list.Elements[0]
		bestKey, err := vm.callCallable(args[0], []runtime.Value{best})
		if err != nil {
			return nil, true, err
		}
		for _, el := range list.Elements[1:] {
			key, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			cmp, err := valuesCompare(key, bestKey)
			if err != nil {
				return nil, true, err
			}
			if cmp < 0 {
				best, bestKey = el, key
			}
		}
		return best, true, nil
	case "maxBy":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.maxBy expects one argument (function)")
		}
		if len(list.Elements) == 0 {
			return runtime.Null{}, true, nil
		}
		best := list.Elements[0]
		bestKey, err := vm.callCallable(args[0], []runtime.Value{best})
		if err != nil {
			return nil, true, err
		}
		for _, el := range list.Elements[1:] {
			key, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			cmp, err := valuesCompare(key, bestKey)
			if err != nil {
				return nil, true, err
			}
			if cmp > 0 {
				best, bestKey = el, key
			}
		}
		return best, true, nil
	case "sortBy":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.sortBy expects one argument (function)")
		}
		type keyedEl struct {
			key runtime.Value
			el  runtime.Value
		}
		pairs := make([]keyedEl, len(list.Elements))
		for i, el := range list.Elements {
			key, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			pairs[i] = keyedEl{key, el}
		}
		var sortErr error
		sort.SliceStable(pairs, func(i, j int) bool {
			if sortErr != nil {
				return false
			}
			cmp, err := valuesCompare(pairs[i].key, pairs[j].key)
			if err != nil {
				sortErr = err
				return false
			}
			return cmp < 0
		})
		if sortErr != nil {
			return nil, true, sortErr
		}
		result := make([]runtime.Value, len(pairs))
		for i, p := range pairs {
			result[i] = p.el
		}
		return runtime.List{Elements: result}, true, nil
	case "topBy":
		if len(args) != 2 {
			return nil, true, fmt.Errorf("list.topBy expects two arguments (function, count)")
		}
		nVal, nOk := native.AsInt64(args[1])
		if !nOk {
			return nil, true, fmt.Errorf("list.topBy: count must be an integer")
		}
		n := int(nVal)
		type keyedEl struct {
			key runtime.Value
			el  runtime.Value
		}
		pairs := make([]keyedEl, len(list.Elements))
		for i, el := range list.Elements {
			key, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			pairs[i] = keyedEl{key, el}
		}
		var sortErr error
		sort.SliceStable(pairs, func(i, j int) bool {
			if sortErr != nil {
				return false
			}
			cmp, err := valuesCompare(pairs[i].key, pairs[j].key)
			if err != nil {
				sortErr = err
				return false
			}
			return cmp > 0
		})
		if sortErr != nil {
			return nil, true, sortErr
		}
		if n < 0 {
			n = 0
		}
		if n > len(pairs) {
			n = len(pairs)
		}
		result := make([]runtime.Value, n)
		for i := 0; i < n; i++ {
			result[i] = pairs[i].el
		}
		return runtime.List{Elements: result}, true, nil
	case "sumBy":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.sumBy expects one argument (function)")
		}
		sum := new(big.Rat)
		hasFloat := false
		var floatSum float64
		for _, el := range list.Elements {
			key, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			switch k := key.(type) {
			case runtime.SmallInt:
				if hasFloat {
					floatSum += float64(k.Value)
				} else {
					sum.Add(sum, new(big.Rat).SetInt64(k.Value))
				}
			case runtime.Int:
				if hasFloat {
					f, _ := new(big.Float).SetInt(k.Value).Float64()
					floatSum += f
				} else {
					sum.Add(sum, new(big.Rat).SetInt(k.Value))
				}
			case runtime.Decimal:
				if hasFloat {
					f, _ := k.Value.Float64()
					floatSum += f
				} else {
					sum.Add(sum, k.Value)
				}
			case runtime.Float:
				if !hasFloat {
					floatSum, _ = sum.Float64()
					hasFloat = true
				}
				floatSum += k.Value
			default:
				return nil, true, fmt.Errorf("list.sumBy: selector must return a number, got %s", key.TypeName())
			}
		}
		if hasFloat {
			return runtime.Float{Value: floatSum}, true, nil
		}
		if sum.IsInt() {
			n := new(big.Int).Set(sum.Num())
			if n.IsInt64() {
				return runtime.SmallInt{Value: n.Int64()}, true, nil
			}
			return runtime.Int{Value: n}, true, nil
		}
		return runtime.Decimal{Value: sum}, true, nil
	case "averageBy":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.averageBy expects one argument (function)")
		}
		if len(list.Elements) == 0 {
			return runtime.Null{}, true, nil
		}
		sum := new(big.Rat)
		hasFloat := false
		var floatSum float64
		for _, el := range list.Elements {
			key, err := vm.callCallable(args[0], []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			switch k := key.(type) {
			case runtime.SmallInt:
				if hasFloat {
					floatSum += float64(k.Value)
				} else {
					sum.Add(sum, new(big.Rat).SetInt64(k.Value))
				}
			case runtime.Int:
				if hasFloat {
					f, _ := new(big.Float).SetInt(k.Value).Float64()
					floatSum += f
				} else {
					sum.Add(sum, new(big.Rat).SetInt(k.Value))
				}
			case runtime.Decimal:
				if hasFloat {
					f, _ := k.Value.Float64()
					floatSum += f
				} else {
					sum.Add(sum, k.Value)
				}
			case runtime.Float:
				if !hasFloat {
					floatSum, _ = sum.Float64()
					hasFloat = true
				}
				floatSum += k.Value
			default:
				return nil, true, fmt.Errorf("list.averageBy: selector must return a number, got %s", key.TypeName())
			}
		}
		count := int64(len(list.Elements))
		if hasFloat {
			return runtime.Float{Value: floatSum / float64(count)}, true, nil
		}
		avg := new(big.Rat).Quo(sum, new(big.Rat).SetInt64(count))
		if avg.IsInt() {
			return runtime.Int{Value: new(big.Int).Set(avg.Num())}, true, nil
		}
		return runtime.Decimal{Value: avg}, true, nil
	case "topK":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.topK expects one argument (count)")
		}
		nVal, nOk := native.AsInt64(args[0])
		if !nOk {
			return nil, true, fmt.Errorf("list.topK: count must be an integer")
		}
		n := int(nVal)
		newElements := make([]runtime.Value, len(list.Elements))
		copy(newElements, list.Elements)
		var sortErr error
		sort.SliceStable(newElements, func(i, j int) bool {
			if sortErr != nil {
				return false
			}
			cmp, err := valuesCompare(newElements[i], newElements[j])
			if err != nil {
				sortErr = err
				return false
			}
			return cmp > 0
		})
		if sortErr != nil {
			return nil, true, sortErr
		}
		if n < 0 {
			n = 0
		}
		if n > len(newElements) {
			n = len(newElements)
		}
		return runtime.List{Elements: newElements[:n]}, true, nil
	case "bottomK":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.bottomK expects one argument (count)")
		}
		nVal, nOk := native.AsInt64(args[0])
		if !nOk {
			return nil, true, fmt.Errorf("list.bottomK: count must be an integer")
		}
		n := int(nVal)
		newElements := make([]runtime.Value, len(list.Elements))
		copy(newElements, list.Elements)
		var sortErr error
		sort.SliceStable(newElements, func(i, j int) bool {
			if sortErr != nil {
				return false
			}
			cmp, err := valuesCompare(newElements[i], newElements[j])
			if err != nil {
				sortErr = err
				return false
			}
			return cmp < 0
		})
		if sortErr != nil {
			return nil, true, sortErr
		}
		if n < 0 {
			n = 0
		}
		if n > len(newElements) {
			n = len(newElements)
		}
		return runtime.List{Elements: newElements[:n]}, true, nil
	case "frequencies":
		if len(args) != 0 {
			return nil, true, fmt.Errorf("list.frequencies expects no arguments")
		}
		type countEntry struct {
			value runtime.Value
			count int
		}
		seen := map[string]int{}
		var counts []countEntry
		for _, el := range list.Elements {
			k := el.Inspect()
			if idx, ok2 := seen[k]; ok2 {
				counts[idx].count++
			} else {
				seen[k] = len(counts)
				counts = append(counts, countEntry{el, 1})
			}
		}
		entries := map[string]runtime.DictEntry{}
		for _, c := range counts {
			entries[native.DictKey(c.value)] = runtime.DictEntry{Key: c.value, Value: runtime.NewInt64(int64(c.count))}
		}
		return runtime.Dict{Entries: entries}, true, nil
	case "mode":
		if len(args) != 0 {
			return nil, true, fmt.Errorf("list.mode expects no arguments")
		}
		if len(list.Elements) == 0 {
			return runtime.Null{}, true, nil
		}
		type countEntry struct {
			value runtime.Value
			count int
		}
		seen := map[string]int{}
		var counts []countEntry
		for _, el := range list.Elements {
			k := el.Inspect()
			if idx, ok2 := seen[k]; ok2 {
				counts[idx].count++
			} else {
				seen[k] = len(counts)
				counts = append(counts, countEntry{el, 1})
			}
		}
		best := counts[0]
		for _, c := range counts[1:] {
			if c.count > best.count {
				best = c
			}
		}
		return best.value, true, nil
	case "difference":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.difference expects one argument (list)")
		}
		other, ok := args[0].(runtime.List)
		if !ok {
			return nil, true, fmt.Errorf("list.difference: second argument must be a list")
		}
		exclude := map[string]bool{}
		for _, el := range other.Elements {
			exclude[el.Inspect()] = true
		}
		var result []runtime.Value
		for _, el := range list.Elements {
			if !exclude[el.Inspect()] {
				result = append(result, el)
			}
		}
		return runtime.List{Elements: result}, true, nil
	case "intersection":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("list.intersection expects one argument (list)")
		}
		other, ok := args[0].(runtime.List)
		if !ok {
			return nil, true, fmt.Errorf("list.intersection: second argument must be a list")
		}
		include := map[string]bool{}
		for _, el := range other.Elements {
			include[el.Inspect()] = true
		}
		var result []runtime.Value
		for _, el := range list.Elements {
			if include[el.Inspect()] {
				result = append(result, el)
			}
		}
		return runtime.List{Elements: result}, true, nil
	case "differenceBy":
		if len(args) != 2 {
			return nil, true, fmt.Errorf("list.differenceBy expects two arguments (list, function)")
		}
		other, ok := args[0].(runtime.List)
		if !ok {
			return nil, true, fmt.Errorf("list.differenceBy: second argument must be a list")
		}
		fn := args[1]
		exclude := map[string]bool{}
		for _, el := range other.Elements {
			key, err := vm.callCallable(fn, []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			exclude[key.Inspect()] = true
		}
		var result []runtime.Value
		for _, el := range list.Elements {
			key, err := vm.callCallable(fn, []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			if !exclude[key.Inspect()] {
				result = append(result, el)
			}
		}
		return runtime.List{Elements: result}, true, nil
	case "intersectionBy":
		if len(args) != 2 {
			return nil, true, fmt.Errorf("list.intersectionBy expects two arguments (list, function)")
		}
		other, ok := args[0].(runtime.List)
		if !ok {
			return nil, true, fmt.Errorf("list.intersectionBy: second argument must be a list")
		}
		fn := args[1]
		include := map[string]bool{}
		for _, el := range other.Elements {
			key, err := vm.callCallable(fn, []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			include[key.Inspect()] = true
		}
		var result []runtime.Value
		for _, el := range list.Elements {
			key, err := vm.callCallable(fn, []runtime.Value{el})
			if err != nil {
				return nil, true, err
			}
			if include[key.Inspect()] {
				result = append(result, el)
			}
		}
		return runtime.List{Elements: result}, true, nil
	case "zipWith":
		if len(args) != 2 {
			return nil, true, fmt.Errorf("list.zipWith expects two arguments (list, function)")
		}
		other, ok := args[0].(runtime.List)
		if !ok {
			return nil, true, fmt.Errorf("list.zipWith: second argument must be a list")
		}
		fn := args[1]
		n := len(list.Elements)
		if len(other.Elements) < n {
			n = len(other.Elements)
		}
		result := make([]runtime.Value, n)
		for i := 0; i < n; i++ {
			combined, err := vm.callCallable(fn, []runtime.Value{list.Elements[i], other.Elements[i]})
			if err != nil {
				return nil, true, err
			}
			result[i] = combined
		}
		return runtime.List{Elements: result}, true, nil
	}
	return nil, false, nil
}

func (vm *VM) CallFunction(index int64, args []runtime.Value) (runtime.Value, error) {
	if index < 0 || int(index) >= len(vm.chunk.Functions) {
		return nil, fmt.Errorf("function index out of range")
	}
	if err := vm.ensureCallableDecorators(); err != nil {
		return nil, err
	}
	wrapper := make([]Instruction, 0, len(args)+2)
	for i := range args {
		wrapper = append(wrapper, Instruction{Op: OpConstant, Operands: []int64{int64(i)}})
	}
	wrapper = append(wrapper, Instruction{Op: OpCall, Operands: []int64{index, int64(len(args))}})
	wrapper = append(wrapper, Instruction{Op: OpReturn})
	return vm.runWrapper(args, wrapper)
}

func (vm *VM) CallFunctionRaw(index int64, args []runtime.Value) (runtime.Value, error) {
	if index < 0 || int(index) >= len(vm.chunk.Functions) {
		return nil, fmt.Errorf("function index out of range")
	}
	wrapper := make([]Instruction, 0, len(args)+2)
	for i := range args {
		wrapper = append(wrapper, Instruction{Op: OpConstant, Operands: []int64{int64(i)}})
	}
	wrapper = append(wrapper, Instruction{Op: OpCall, Operands: []int64{index, int64(len(args))}})
	wrapper = append(wrapper, Instruction{Op: OpReturn})
	return vm.runWrapperWithRawCall(args, wrapper, index)
}

func (vm *VM) CallClosure(closure runtime.BytecodeClosure, args []runtime.Value) (runtime.Value, error) {
	return vm.callCallable(closure, args)
}

func (vm *VM) ReflectConstructorsForChunkClass(class runtime.BytecodeClass) (runtime.Value, error) {
	return vm.reflectConstructors(class)
}

func (vm *VM) ReflectFieldsForChunkClass(class runtime.BytecodeClass) (runtime.Value, error) {
	return vm.reflectFieldsResult(class, runtime.ClassMetadata{}), nil
}

func (vm *VM) collectChunkClasses(chunk Chunk) []runtime.Value {
	out := make([]runtime.Value, 0, len(chunk.Classes))
	for i, classInfo := range chunk.Classes {
		out = append(out, vm.bytecodeClassFromInfo(classInfo, int64(i)))
	}
	return out
}

func dedupeClassValues(values []runtime.Value) []runtime.Value {
	seen := map[string]bool{}
	out := make([]runtime.Value, 0, len(values))
	for _, v := range values {
		if bc, ok := v.(runtime.BytecodeClass); ok {
			key := bc.Module + "." + bc.Name
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, v)
	}
	return out
}

// DeserializeIntoChunkClass is the public entry the moduleLoader uses
// to deserialize a class declared in this VM's chunk. It bypasses the
// cross-module dispatch in deserializeIntoClass (we are already on the
// right VM) and goes straight to the local logic.
func (vm *VM) DeserializeIntoChunkClass(class runtime.BytecodeClass, value runtime.Value) (runtime.Value, error) {
	if class.Index < 0 || int(class.Index) >= len(vm.chunk.Classes) {
		return nil, fmt.Errorf("deserialize %s: class index out of range", class.Name)
	}
	classInfo := vm.chunk.Classes[class.Index]
	if indices, name, ok := vm.lookupStaticDunder(classInfo, "__deserialize", "__deserialize__"); ok && len(indices) > 0 {
		args := []runtime.Value{value}
		functionIndex, err := vm.selectRuntimeFunction(Instruction{}, name, indices, args, 0)
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
			entry, hit := dict.Entries[native.DictKey(key)]
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
		key := runtime.String{Value: paramName}
		entry, hit := dict.Entries[native.DictKey(key)]
		if !hit {
			return nil, fmt.Errorf("deserialize %s: missing field %q", class.Name, paramName)
		}
		args = append(args, entry.Value)
	}
	return vm.ConstructClass(class.Index, args)
}

func (vm *VM) ConstructClass(index int64, args []runtime.Value) (runtime.Value, error) {
	if index < 0 || int(index) >= len(vm.chunk.Classes) {
		return nil, fmt.Errorf("class index out of range")
	}
	wrapper := make([]Instruction, 0, len(args)+2)
	for i := range args {
		wrapper = append(wrapper, Instruction{Op: OpConstant, Operands: []int64{int64(i)}})
	}
	wrapper = append(wrapper, Instruction{Op: OpConstructClass, Operands: []int64{index, int64(len(args))}})
	wrapper = append(wrapper, Instruction{Op: OpReturn})
	return vm.runWrapper(args, wrapper)
}

// CallMethodAs invokes `name` on `instance` using the supplied
// className to locate the method definition. Used by cross-module
// parent dispatch where the subclass instance is declared in
// another chunk: `instance.Class.Name` would point at the subclass,
// which doesn't exist in the parent module's chunk.
func (vm *VM) CallMethodAs(className string, instance *runtime.Instance, name string, args []runtime.Value) (runtime.Value, error) {
	classInfo, ok := vm.classInfo(className)
	if !ok {
		return nil, fmt.Errorf("unknown class %s", className)
	}
	indices, ok := vm.lookupMethod(classInfo, name)
	if !ok {
		if strings.EqualFold(name, className) {
			indices = append([]int64(nil), classInfo.ConstructorIndices...)
			if len(indices) == 0 {
				return runtime.Null{}, nil
			}
		} else {
			if classInfo.ParentIndex < 0 && strings.Contains(classInfo.ParentName, ".") && vm.moduleLoader != nil {
				if module, parentClass, mok := splitQualifiedClassName(classInfo.ParentName); mok {
					if _, lerr := vm.moduleLoader.LoadModule(module, module); lerr == nil {
						return vm.moduleLoader.CallParentInModule(module, parentClass, name, instance, args)
					}
				}
			}
			return nil, &runtime.MethodNotFoundError{Class: className, Method: name}
		}
	}
	functionIndex, err := vm.selectRuntimeFunction(Instruction{}, name, indices, args, 1)
	if err != nil {
		return nil, err
	}
	if err := vm.ensureCallableDecorators(); err != nil {
		return nil, err
	}
	if decorated, ok := vm.decoratedFuncs[functionIndex]; ok {
		if vm.chunk.Functions[functionIndex].Async && !vm.syncMode {
			return vm.startAsyncCallableWithForwardThis(decorated, args, instance), nil
		}
		return vm.callCallableWithForwardThis(decorated, args, instance)
	}
	callArgs := append([]runtime.Value{instance}, args...)
	return vm.CallFunction(functionIndex, callArgs)
}

func (vm *VM) CallMethod(instance *runtime.Instance, name string, args []runtime.Value) (runtime.Value, error) {
	if nativeMethods := instance.Class.Methods[strings.ToLower(name)]; len(nativeMethods) > 0 && nativeMethods[0].Native != nil {
		return nativeMethods[0].Native(instance, args)
	}
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		return nil, fmt.Errorf("unknown class %s", instance.Class.Name)
	}
	indices, ok := vm.lookupMethod(classInfo, name)
	if !ok {
		if classInfo.ParentIndex < 0 && strings.Contains(classInfo.ParentName, ".") && vm.moduleLoader != nil {
			if module, parentClass, mok := splitQualifiedClassName(classInfo.ParentName); mok {
				if _, lerr := vm.moduleLoader.LoadModule(module, module); lerr == nil {
					return vm.moduleLoader.CallParentInModule(module, parentClass, name, instance, args)
				}
			}
		}
		return nil, &runtime.MethodNotFoundError{Class: instance.Class.Name, Method: name}
	}
	functionIndex, err := vm.selectRuntimeFunction(Instruction{}, name, indices, args, 1)
	if err != nil {
		return nil, err
	}
	if err := vm.ensureCallableDecorators(); err != nil {
		return nil, err
	}
	if decorated, ok := vm.decoratedFuncs[functionIndex]; ok {
		if vm.chunk.Functions[functionIndex].Async && !vm.syncMode {
			return vm.startAsyncCallableWithForwardThis(decorated, args, instance), nil
		}
		return vm.callCallableWithForwardThis(decorated, args, instance)
	}
	callArgs := append([]runtime.Value{instance}, args...)
	return vm.CallFunction(functionIndex, callArgs)
}

func (vm *VM) CallStaticMethod(classIndex int64, name string, args []runtime.Value) (runtime.Value, error) {
	if classIndex < 0 || int(classIndex) >= len(vm.chunk.Classes) {
		return nil, fmt.Errorf("class index out of range")
	}
	indices, ok := vm.lookupStaticMethod(vm.chunk.Classes[classIndex], name)
	if !ok {
		return nil, fmt.Errorf("unknown static method %s.%s", vm.chunk.Classes[classIndex].Name, name)
	}
	functionIndex, err := vm.selectRuntimeFunction(Instruction{}, name, indices, args, 0)
	if err != nil {
		return nil, err
	}
	if err := vm.ensureCallableDecorators(); err != nil {
		return nil, err
	}
	if decorated, ok := vm.decoratedFuncs[functionIndex]; ok {
		if vm.chunk.Functions[functionIndex].Async && !vm.syncMode {
			return vm.startAsyncCallable(decorated, args), nil
		}
		return vm.callCallable(decorated, args)
	}
	return vm.CallFunction(functionIndex, args)
}

func (vm *VM) runWrapper(args []runtime.Value, wrapper []Instruction) (runtime.Value, error) {
	return vm.runWrapperWithRawCall(args, wrapper, -1)
}

func (vm *VM) runWrapperWithRawCall(args []runtime.Value, wrapper []Instruction, rawIndex int64) (runtime.Value, error) {
	chunk := vm.chunk
	shift := len(wrapper)
	chunk.Constants = append(append([]runtime.Value(nil), args...), vm.chunk.Constants...)
	chunk.Instructions = append([]Instruction(nil), wrapper...)
	chunk.Functions = append([]FunctionInfo(nil), vm.chunk.Functions...)
	for i := range chunk.Functions {
		chunk.Functions[i].Entry += int64(shift)
		chunk.Functions[i].DefaultConstants = append([]int64(nil), chunk.Functions[i].DefaultConstants...)
		for j, defaultIndex := range chunk.Functions[i].DefaultConstants {
			if defaultIndex >= 0 {
				chunk.Functions[i].DefaultConstants[j] = defaultIndex + int64(len(args))
			}
		}
	}
	chunk.Classes = copyClasses(vm.chunk.Classes)
	for i := range chunk.Classes {
		for j, defaultIndex := range chunk.Classes[i].FieldDefaults {
			if defaultIndex >= 0 {
				chunk.Classes[i].FieldDefaults[j] = defaultIndex + int64(len(args))
			}
		}
		for name, constantIndex := range chunk.Classes[i].StaticValues {
			if constantIndex >= 0 {
				chunk.Classes[i].StaticValues[name] = constantIndex + int64(len(args))
			}
		}
	}
	for _, instruction := range vm.chunk.Instructions {
		copied := instruction
		copied.Operands = append([]int64(nil), instruction.Operands...)
		shiftInstructionOperands(&copied, shift, len(args))
		chunk.Instructions = append(chunk.Instructions, copied)
	}
	callVM := NewVMWithModuleLoader(chunk, vm.stdout, vm.moduleLoader)
	// The callVM is a wrap-bridge worker: its setGlobalVM writes feed the
	// write-back loop below, so it must take the locked + dirty-tracking
	// path even if no further wrap fires inside it.
	callVM.bridgeActive.Store(true)
	// Mark this parent VM as bridging so its subsequent setGlobalVM
	// writes serialise with the snapshot-in / write-back done by the
	// callVM on whichever goroutine eventually invokes the wrapped
	// callable. The store happens on the parent's goroutine before any
	// possibility of cross-goroutine read.
	vm.bridgeActive.Store(true)
	// Snapshot parent globals under the mutex so a wrap-bridge callback
	// invoked from a worker goroutine doesn't race with the parent's
	// setGlobal writes.
	vm.globalsMu.Lock()
	callVM.restoreGlobalsVM(vm.globals)
	vm.globalsMu.Unlock()
	callVM.SetModuleName(vm.moduleName)
	if vm.statefulNative != nil {
		callVM.SetStatefulNativeCaller(vm.statefulNative)
	}
	callVM.syncMode = true
	callVM.forwardThis = vm.forwardThis
	callVM.decoratedFuncs = copyRuntimeValueMap(vm.decoratedFuncs)
	callVM.decoratedClasses = copyRuntimeValueMap(vm.decoratedClasses)
	callVM.decoratorsApplied = true
	callVM.rawFunctionCalls = copyBoolMap(vm.rawFunctionCalls)
	callVM.methodReceiverFuncs = vm.methodReceiverFuncs
	callVM.generatorExecution = vm.generatorExecution
	callVM.generatorYield = vm.generatorYield
	callVM.generatorDone = vm.generatorDone
	if rawIndex >= 0 {
		callVM.rawFunctionCalls[rawIndex] = true
	}
	if err := callVM.Run(); err != nil {
		return nil, err
	}
	// Propagate any global-state mutations the callVM produced back to the
	// caller. Without this, deferred function calls and any other paths that
	// invoke CallFunction from inside a running VM silently discard global
	// writes the callee performed (defer-fired closures over module-level
	// state were the symptom). Write only the slots the callee touched -
	// a slicecopy of the entire backing array would race the parent's
	// lock-free OpGetGlobal on unrelated slots when the bridge fires on a
	// goroutine.
	vm.globalsMu.Lock()
	for i, dirty := range callVM.dirtyGlobals {
		if !dirty {
			continue
		}
		if i >= len(callVM.globals) || i >= len(vm.globals) {
			continue
		}
		vm.globals[i] = callVM.globals[i]
		if i < len(vm.dirtyGlobals) {
			vm.dirtyGlobals[i] = true
		}
	}
	vm.globalsMu.Unlock()
	if len(callVM.stack) == 0 {
		return runtime.Null{}, nil
	}
	return callVM.pop()
}

func copyRuntimeValueMap(values map[int64]runtime.Value) map[int64]runtime.Value {
	copied := make(map[int64]runtime.Value, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func copyBoolMap(values map[int64]bool) map[int64]bool {
	copied := make(map[int64]bool, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func copyClasses(classes []ClassInfo) []ClassInfo {
	copied := append([]ClassInfo(nil), classes...)
	for i := range copied {
		copied[i].FieldNames = append([]string(nil), classes[i].FieldNames...)
		copied[i].FieldDefaults = append([]int64(nil), classes[i].FieldDefaults...)
		copied[i].Methods = copyStringInt64SliceMap(classes[i].Methods)
		copied[i].StaticValues = copyStringInt64Map(classes[i].StaticValues)
		copied[i].StaticMethods = copyStringInt64SliceMap(classes[i].StaticMethods)
		copied[i].Implements = append([]string(nil), classes[i].Implements...)
		copied[i].Decorators = copyDecoratorMetadata(classes[i].Decorators)
		copied[i].MethodDecorators = copyDecoratorMetadataMap(classes[i].MethodDecorators)
		copied[i].StaticDecorators = copyDecoratorMetadataMap(classes[i].StaticDecorators)
	}
	return copied
}

func copyDecoratorMetadata(values []runtime.DecoratorMetadata) []runtime.DecoratorMetadata {
	if values == nil {
		return nil
	}
	copied := make([]runtime.DecoratorMetadata, len(values))
	for i, value := range values {
		copied[i] = value
		copied[i].Args = append([]runtime.Value(nil), value.Args...)
		if value.NamedArgs != nil {
			copied[i].NamedArgs = make(map[string]runtime.Value, len(value.NamedArgs))
			for key, arg := range value.NamedArgs {
				copied[i].NamedArgs[key] = arg
			}
		}
	}
	return copied
}

func copyDecoratorMetadataMap(values map[string][]runtime.DecoratorMetadata) map[string][]runtime.DecoratorMetadata {
	if values == nil {
		return nil
	}
	copied := make(map[string][]runtime.DecoratorMetadata, len(values))
	for key, value := range values {
		copied[key] = copyDecoratorMetadata(value)
	}
	return copied
}

func copyStringInt64Map(values map[string]int64) map[string]int64 {
	if values == nil {
		return nil
	}
	copied := make(map[string]int64, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func copyStringInt64SliceMap(values map[string][]int64) map[string][]int64 {
	if values == nil {
		return nil
	}
	copied := make(map[string][]int64, len(values))
	for key, value := range values {
		copied[key] = append([]int64(nil), value...)
	}
	return copied
}

func shiftInstructionOperands(instruction *Instruction, instructionShift int, constantShift int) {
	switch instruction.Op {
	case OpConstant, OpRuntimeError, OpMatchError, OpNativeCall, OpNativeCallNamed, OpGetField, OpSetField, OpCallParentMethod, OpGetStaticValue, OpSetStaticValue, OpCallStaticMethod, OpMethodCall, OpMethodCallSpread, OpMethodCallNamed, OpMakeError, OpImportModule, OpCatch, OpDeferNativeCall, OpDeferNativeCallNamed, OpDeferMethodCall, OpDeferMethodCallNamed, OpDeferCallableCallNamed, OpTypeAssert, OpAddStringConst, OpAppendStringConst, OpAppendGlobalStringConst, OpAppendStringConstStmt, OpAppendGlobalStringConstStmt:
		for i := range instruction.Operands {
			if isConstantOperand(instruction.Op, i) && instruction.Operands[i] >= 0 {
				instruction.Operands[i] += int64(constantShift)
			}
		}
	case OpSetTypeBindings, OpPlantCallTypeBindings:
		// Operands: [count, pIdx1, tIdx1, pIdx2, tIdx2, ...] — all indices from 1 onward are constant indices.
		for i := 1; i < len(instruction.Operands); i++ {
			if instruction.Operands[i] >= 0 {
				instruction.Operands[i] += int64(constantShift)
			}
		}
	}
	switch instruction.Op {
	case OpJump, OpJumpIfFalse, OpIterNext, OpPushExceptionHandler,
		OpJumpIfNotLessInt, OpJumpIfNotLessEqualInt, OpJumpIfNotGreaterInt,
		OpJumpIfNotGreaterEqualInt, OpJumpIfNotEqualInt, OpJumpIfEqualInt,
		OpNullCoalesce, OpOptionalChain:
		if len(instruction.Operands) > 0 && instruction.Operands[0] >= 0 {
			instruction.Operands[0] += int64(instructionShift)
		}
	case OpCatch:
		if len(instruction.Operands) > 0 && instruction.Operands[0] >= 0 {
			instruction.Operands[0] += int64(instructionShift)
		}
	}
}

func isConstantOperand(op Op, index int) bool {
	switch op {
	case OpConstant, OpRuntimeError, OpMatchError, OpNativeCall, OpGetField, OpSetField, OpMethodCall, OpMethodCallSpread, OpMakeError, OpDeferNativeCall, OpDeferMethodCall, OpTypeAssert, OpAddStringConst:
		return index == 0
	case OpAppendStringConst, OpAppendGlobalStringConst, OpAppendStringConstStmt, OpAppendGlobalStringConstStmt:
		return index == 1
	case OpNativeCallNamed:
		return index == 0 || index >= 2
	case OpDeferNativeCallNamed:
		return index == 0 || index >= 2
	case OpDeferMethodCallNamed:
		return index == 0 || index >= 2
	case OpDeferCallableCallNamed:
		return index >= 1
	case OpCallParentMethod:
		return index == 1
	case OpGetStaticValue, OpSetStaticValue:
		return index == 1
	case OpCallStaticMethod:
		return index == 1
	case OpMethodCallNamed:
		return index == 0 || index >= 2
	case OpImportModule:
		return index == 0 || index == 1
	case OpCatch:
		return index == 1
	default:
		return false
	}
}

func (vm *VM) instanceOf(instruction Instruction) error {
	target, err := vm.popString(instruction, "instanceof target must be string")
	if err != nil {
		return err
	}
	value, err := vm.pop()
	if err != nil {
		return vm.runtimeError(instruction, "%s", err.Error())
	}
	// Resolve type parameter binding if target is a generic type param name.
	if len(vm.frames) > 0 {
		if bindings := vm.frames[len(vm.frames)-1].typeBindings; bindings != nil {
			if bound, ok := bindings[target]; ok {
				target = bound
			}
		}
	}
	if ev, ok := value.(runtime.EnumVariant); ok {
		if dotIdx := strings.Index(target, "."); dotIdx >= 0 {
			enumName := target[:dotIdx]
			variantName := target[dotIdx+1:]
			vm.push(runtime.Bool{Value: strings.EqualFold(ev.Enum.Name, enumName) && strings.EqualFold(ev.Variant, variantName)})
		} else {
			vm.push(runtime.Bool{Value: strings.EqualFold(ev.Enum.Name, target)})
		}
		return nil
	}
	if instance, ok := value.(*runtime.Instance); ok {
		// Try the chunk-local ClassInfo first (cheaper and lets
		// classImplements consult the per-chunk interface table).
		// Fall back to a direct walk of the runtime.Class parent
		// chain so cross-chunk class hierarchies - e.g. a class
		// imported from another module - also resolve correctly.
		stripped := stripModulePrefix(target)
		if classInfo, ok := vm.classInfo(instance.Class.Name); ok {
			if vm.classMatches(classInfo, stripped) || vm.classImplements(classInfo, stripped) {
				vm.push(runtime.Bool{Value: true})
				return nil
			}
		}
		if runtimeClassMatches(instance.Class, stripped) {
			vm.push(runtime.Bool{Value: true})
			return nil
		}
		for _, extra := range instance.ExtraTypeNames {
			if strings.EqualFold(stripModulePrefix(extra), stripped) {
				vm.push(runtime.Bool{Value: true})
				return nil
			}
		}
		vm.push(runtime.Bool{Value: false})
		return nil
	}
	if errValue, ok := value.(runtime.Error); ok {
		// Error-derived class instances are wrapped as runtime.Error
		// rather than *runtime.Instance. Walk the parent chain
		// captured at construction so `instanceof Parent` matches an
		// error subclass even when the parent class was declared in
		// another module.
		stripped := stripModulePrefix(target)
		vm.push(runtime.Bool{Value: vm.errorClassMatches(errValue, stripped)})
		return nil
	}
	// `instanceof list<int>` and friends: split off the generic args
	// and dispatch element-aware matching. Tagged collections compare
	// the recorded element types; untagged collections walk elements.
	if base, args, ok := vmSplitGenericTypeName(target); ok {
		vm.push(runtime.Bool{Value: vmCollectionMatchesGeneric(value, base, args)})
		return nil
	}
	vm.push(runtime.Bool{Value: value.TypeName() == target})
	return nil
}

func vmSplitGenericTypeName(typeName string) (string, []string, bool) {
	if strings.HasPrefix(typeName, "?") {
		typeName = typeName[1:]
	}
	lt := strings.IndexByte(typeName, '<')
	if lt < 0 || !strings.HasSuffix(typeName, ">") {
		return "", nil, false
	}
	base := typeName[:lt]
	inner := typeName[lt+1 : len(typeName)-1]
	var args []string
	depth := 0
	start := 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(inner[start:i]))
				start = i + 1
			}
		}
	}
	if start <= len(inner) {
		args = append(args, strings.TrimSpace(inner[start:]))
	}
	return base, args, true
}

func vmCollectionMatchesGeneric(value runtime.Value, base string, args []string) bool {
	switch v := value.(type) {
	case runtime.List:
		if base != "list" || len(args) != 1 {
			return false
		}
		if len(v.ElementTypes) >= 1 {
			return strings.EqualFold(v.ElementTypes[0], args[0])
		}
		for _, el := range v.Elements {
			if !vmValueMatchesSimpleType(el, args[0]) {
				return false
			}
		}
		return true
	case runtime.Set:
		if base != "set" || len(args) != 1 {
			return false
		}
		if len(v.ElementTypes) >= 1 {
			return strings.EqualFold(v.ElementTypes[0], args[0])
		}
		for _, e := range v.Elements {
			if !vmValueMatchesSimpleType(e.Value, args[0]) {
				return false
			}
		}
		return true
	case runtime.Dict:
		if base != "dict" || len(args) != 2 {
			return false
		}
		if len(v.ElementTypes) >= 2 {
			return strings.EqualFold(v.ElementTypes[0], args[0]) && strings.EqualFold(v.ElementTypes[1], args[1])
		}
		for _, e := range v.Entries {
			if !vmValueMatchesSimpleType(e.Key, args[0]) {
				return false
			}
			if !vmValueMatchesSimpleType(e.Value, args[1]) {
				return false
			}
		}
		return true
	}
	return false
}

func vmValueMatchesSimpleType(value runtime.Value, target string) bool {
	if base, args, ok := vmSplitGenericTypeName(target); ok {
		return vmCollectionMatchesGeneric(value, base, args)
	}
	switch value.(type) {
	case runtime.SmallInt, runtime.Int:
		return target == "int"
	case runtime.Float:
		return target == "float"
	case runtime.Decimal:
		return target == "decimal"
	case runtime.String:
		return target == "string"
	case runtime.Bool:
		return target == "bool"
	case runtime.Bytes:
		return target == "bytes"
	}
	return strings.EqualFold(value.TypeName(), target)
}

func (vm *VM) cast(instruction Instruction, ip int) (int, error) {
	target, err := vm.popString(instruction, "cast target must be string")
	if err != nil {
		return 0, err
	}
	value, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	// Class / interface / parent-chain widening cast: an Error or
	// Instance is assignable to any ancestor in its chain, with the
	// module prefix on the target name stripped (so `e as errors.X`
	// matches `e` whose class extends X declared in any module).
	stripped := stripModulePrefix(target)
	if errValue, ok := value.(runtime.Error); ok {
		if vm.errorClassMatches(errValue, stripped) {
			vm.push(value)
			return ip, nil
		}
	}
	if instance, ok := value.(*runtime.Instance); ok {
		if classInfo, ok := vm.classInfo(instance.Class.Name); ok {
			if vm.classMatches(classInfo, stripped) || vm.classImplements(classInfo, stripped) {
				vm.push(value)
				return ip, nil
			}
		}
		if runtimeClassMatches(instance.Class, stripped) {
			vm.push(value)
			return ip, nil
		}
		if dunder := castDunderName(target); dunder != "" {
			if result, handled, err := vm.invokeInstanceMethod(instance, dunder, nil); err != nil {
				return 0, vm.runtimeError(instruction, "%s", err.Error())
			} else if handled {
				if err := checkCastDunderReturn(target, result); err != nil {
					return vm.throwTyped(instruction, ip, "RuntimeError", err.Error())
				}
				vm.push(result)
				return ip, nil
			}
		}
	}
	cast, err := castValue(value, target)
	if err != nil {
		/* Cast failures are user-catchable via `try / catch (RuntimeError e)`.
		 * Matches the evaluator, where castValue's error bubbles into the
		 * try frame as a thrown RuntimeError. */
		return vm.throwTyped(instruction, ip, "RuntimeError", err.Error())
	}
	vm.push(cast)
	return ip, nil
}

func (vm *VM) selectRuntimeFunction(instruction Instruction, name string, indices []int64, args []runtime.Value, paramOffset int) (int64, error) {
	/* Fast path: most classes declare a single overload per method.
	 * Skip the slice allocation + post-loop "ambiguous" check and
	 * just verify arity + types directly on the lone candidate. */
	if len(indices) == 1 {
		index := indices[0]
		if index < 0 || int(index) >= len(vm.chunk.Functions) {
			return 0, vm.runtimeError(instruction, "method index out of range")
		}
		function := vm.chunk.Functions[index]
		required := len(function.ParamSlots)
		for required > paramOffset && required <= len(function.DefaultConstants) && function.DefaultConstants[required-1] >= 0 {
			required--
		}
		provided := len(args) + paramOffset
		if provided < required || provided > len(function.ParamSlots) {
			return 0, vm.runtimeError(instruction, "no matching overload for %s", name)
		}
		if !vm.runtimeArgumentsMatch(function, args, paramOffset) {
			return 0, vm.runtimeError(instruction, "no matching overload for %s", name)
		}
		return index, nil
	}
	matches := []int64{}
	for _, index := range indices {
		if index < 0 || int(index) >= len(vm.chunk.Functions) {
			return 0, vm.runtimeError(instruction, "method index out of range")
		}
		function := vm.chunk.Functions[index]
		required := len(function.ParamSlots)
		for required > paramOffset && required <= len(function.DefaultConstants) && function.DefaultConstants[required-1] >= 0 {
			required--
		}
		provided := len(args) + paramOffset
		if provided < required || provided > len(function.ParamSlots) {
			continue
		}
		if !vm.runtimeArgumentsMatch(function, args, paramOffset) {
			continue
		}
		matches = append(matches, index)
	}
	if len(matches) == 0 {
		return 0, vm.runtimeError(instruction, "no matching overload for %s", name)
	}
	if len(matches) > 1 {
		return 0, vm.runtimeError(instruction, "ambiguous overload for %s", name)
	}
	return matches[0], nil
}

func (vm *VM) selectRuntimeNamedFunction(instruction Instruction, name string, indices []int64, args []runtime.Value, names []string, paramOffset int) (int64, []runtime.Value, error) {
	matches := []int64{}
	orderedMatches := [][]runtime.Value{}
	for _, index := range indices {
		if index < 0 || int(index) >= len(vm.chunk.Functions) {
			return 0, nil, vm.runtimeError(instruction, "method index out of range")
		}
		ordered, err := vm.orderRuntimeArguments(instruction, vm.chunk.Functions[index], args, names, paramOffset)
		if err != nil {
			continue
		}
		if !vm.runtimeArgumentsMatch(vm.chunk.Functions[index], ordered, paramOffset) {
			continue
		}
		matches = append(matches, index)
		orderedMatches = append(orderedMatches, ordered)
	}
	if len(matches) == 0 {
		return 0, nil, vm.runtimeError(instruction, "no matching overload for %s", name)
	}
	if len(matches) > 1 {
		return 0, nil, vm.runtimeError(instruction, "ambiguous overload for %s", name)
	}
	return matches[0], orderedMatches[0], nil
}

func (vm *VM) runtimeArgumentsMatch(function FunctionInfo, args []runtime.Value, paramOffset int) bool {
	if len(function.ParamTypes) == 0 {
		return true
	}
	typeParams := function.typeParamSet
	for i, arg := range args {
		paramIndex := i + paramOffset
		if paramIndex >= len(function.ParamTypes) {
			return false
		}
		if paramIndex < len(function.paramTypeSpecs) && function.paramTypeSpecs[paramIndex].raw != "" {
			if !vm.matchValueToTypeSpec(typeParams, arg, function.paramTypeSpecs[paramIndex]) {
				return false
			}
			continue
		}
		if !vm.matchValueToTypeStr(typeParams, arg, function.ParamTypes[paramIndex]) {
			return false
		}
	}
	return true
}

// descriptiveRuntimeTypeName returns a type name that includes element type info where detectable,
// e.g. "list<string>" instead of "list". For reified user-defined generic
// class instances it also unspools the recorded TypeBindings -
// "Container<Sub>" rather than the bare "Container" - so error messages
// about invariant-parameter mismatches surface the caller's actual
// binding rather than just the class name.
func (vm *VM) descriptiveRuntimeTypeName(value runtime.Value) string {
	switch v := value.(type) {
	case runtime.List:
		if len(v.Elements) > 0 {
			return "list<" + v.Elements[0].TypeName() + ">"
		}
		return "list"
	case runtime.Set:
		for _, entry := range v.Elements {
			return "set<" + entry.Value.TypeName() + ">"
		}
		return "set"
	case runtime.Dict:
		for _, entry := range v.Entries {
			return "dict<" + entry.Key.TypeName() + "," + entry.Value.TypeName() + ">"
		}
		return "dict"
	case *runtime.Instance:
		if v == nil || v.Class == nil || len(v.Class.TypeParameters) == 0 || len(v.TypeBindings) == 0 {
			return value.TypeName()
		}
		parts := make([]string, 0, len(v.Class.TypeParameters))
		for _, p := range v.Class.TypeParameters {
			if bound, ok := v.TypeBindings[p]; ok && bound != "" {
				parts = append(parts, bound)
			}
		}
		if len(parts) == 0 {
			return value.TypeName()
		}
		return v.Class.Name + "<" + strings.Join(parts, ", ") + ">"
	}
	return value.TypeName()
}

// parseTypeStr splits a generic type string like "list<dict<string,int>>" into
// base="list" and inner="dict<string,int>". Returns (s, "", false) for non-generic types.
func parseTypeStr(s string) (base, inner string, hasInner bool) {
	lt := strings.IndexByte(s, '<')
	if lt < 0 {
		return s, "", false
	}
	depth := 0
	for i := lt; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return s[:lt], s[lt+1 : i], true
			}
		}
	}
	return s, "", false
}

// splitTopLevelTypeOp scans for the supplied operator byte ('|' or
// '&') at depth zero in a type-string and splits on every
// occurrence. Returns ok=true and the trimmed branch list when the
// operator was found at the top level; ok=false leaves the input
// untouched. Generic argument lists (`<...>`) are skipped so that
// `dict<int, string>` doesn't tokenise its inner comma or any
// nested generic operators.
func splitTopLevelTypeOp(s string, op byte) ([]string, bool) {
	depth := 0
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			depth--
		case op:
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if len(parts) == 0 {
		return nil, false
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts, true
}

// splitTypeArgs splits a comma-separated type argument list respecting nested angle brackets.
// e.g. "string,dict<string,int>" → ["string", "dict<string,int>"]
func splitTypeArgs(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(parts, strings.TrimSpace(s[start:]))
}

// matchValueToTypeStr is the internal recursive implementation of VM type checking.
// typeParams is the pre-computed set of generic type parameter names (nil for non-generic contexts;
// a nil map is safe — Go map lookups on nil maps return the zero value).
func (vm *VM) matchValueToTypeStr(typeParams map[string]bool, value runtime.Value, typ string) bool {
	spec := vm.typeSpec(typ)
	return vm.matchValueToTypeSpec(typeParams, value, spec)
}

// matchValueToTypeStrWith is matchValueToTypeStr extended with an
// inheritedBindings map. When a type-parameter name in the spec is not
// declared by the function being entered but IS bound by the caller's
// outer generic frame, the binding's concrete type is substituted and
// re-checked. Used by the closure call path so that lambdas and
// named-generic-function references resolve T correctly.
func (vm *VM) matchValueToTypeStrWith(typeParams map[string]bool, inherited map[string]string, value runtime.Value, typ string) bool {
	spec := vm.typeSpec(typ)
	return vm.matchValueToTypeSpecWith(typeParams, inherited, value, spec)
}

func (vm *VM) matchValueToTypeSpecWith(typeParams map[string]bool, inherited map[string]string, value runtime.Value, spec vmTypeSpec) bool {
	if typeParams[spec.baseLower] {
		return true
	}
	if len(inherited) > 0 {
		if bound, ok := inherited[spec.base]; ok && bound != "" {
			return vm.matchValueToTypeSpec(typeParams, value, vm.typeSpec(bound))
		}
		if bound, ok := inherited[spec.baseLower]; ok && bound != "" {
			return vm.matchValueToTypeSpec(typeParams, value, vm.typeSpec(bound))
		}
	}
	return vm.matchValueToTypeSpec(typeParams, value, spec)
}

func (vm *VM) typeSpec(typ string) vmTypeSpec {
	typ = strings.TrimSpace(typ)
	if vm.typeSpecCache == nil {
		vm.typeSpecCache = map[string]vmTypeSpec{}
	}
	if spec, ok := vm.typeSpecCache[typ]; ok {
		return spec
	}
	spec := parseVMTypeSpec(typ)
	vm.typeSpecCache[typ] = spec
	return spec
}

func parseVMTypeSpec(typ string) vmTypeSpec {
	raw := strings.TrimSpace(typ)
	// Top-level `|` or `&` (outside angle brackets) builds a
	// union / intersection spec whose args are the branches.
	if branches, op := splitTopLevelTypeOp(raw, '|'); op {
		spec := vmTypeSpec{raw: raw, kind: vmTypeUnion}
		for _, b := range branches {
			spec.args = append(spec.args, parseVMTypeSpec(b))
		}
		// A union is "nullable" when any branch is the explicit
		// null type or a ?T sigil. This lets the early-return
		// for VMKindNull in matchValueToTypeSpec stay accurate.
		for _, arg := range spec.args {
			if arg.nullable || arg.baseLower == "null" {
				spec.nullable = true
				break
			}
		}
		return spec
	}
	if branches, op := splitTopLevelTypeOp(raw, '&'); op {
		spec := vmTypeSpec{raw: raw, kind: vmTypeIntersection}
		for _, b := range branches {
			spec.args = append(spec.args, parseVMTypeSpec(b))
		}
		return spec
	}
	baseTyp, innerTyp, hasInner := parseTypeStr(raw)
	base := strings.TrimSpace(baseTyp)
	nullable := strings.HasPrefix(base, "?")
	/* Strip the leading `?` from `base` so callers comparing it to a
	 * value's TypeName / class name don't have to handle the nullable
	 * sigil themselves. The `nullable` flag carries that bit. */
	base = strings.TrimPrefix(base, "?")
	baseLower := strings.ToLower(base)
	spec := vmTypeSpec{
		raw:       raw,
		base:      base,
		baseLower: baseLower,
		nullable:  nullable,
		kind:      vmTypeKindForBase(baseLower),
	}
	if hasInner {
		for _, arg := range splitTypeArgs(innerTyp) {
			if arg != "" {
				spec.args = append(spec.args, parseVMTypeSpec(arg))
			}
		}
	}
	return spec
}

func vmTypeKindForBase(baseLower string) vmTypeKind {
	switch normalizeCallableTypeName(baseLower) {
	case "", "any":
		return vmTypeAny
	case "int":
		return vmTypeInt
	case "string":
		return vmTypeString
	case "bool":
		return vmTypeBool
	case "float":
		return vmTypeFloat
	case "decimal":
		return vmTypeDecimal
	case "list":
		return vmTypeList
	case "set":
		return vmTypeSet
	case "dict":
		return vmTypeDict
	case "func":
		return vmTypeCallable
	case "generator", "iterable":
		return vmTypeGenerator
	default:
		return vmTypeOther
	}
}

func (vm *VM) matchValueToTypeSpec(typeParams map[string]bool, value runtime.Value, spec vmTypeSpec) bool {
	if typeParams[spec.baseLower] {
		return true
	}
	if spec.kind == vmTypeAny {
		return true
	}
	if spec.kind == vmTypeUnion {
		if _, isNull := value.(runtime.Null); isNull && spec.nullable {
			return true
		}
		for _, branch := range spec.args {
			if vm.matchValueToTypeSpec(typeParams, value, branch) {
				return true
			}
		}
		return false
	}
	if spec.kind == vmTypeIntersection {
		for _, branch := range spec.args {
			if !vm.matchValueToTypeSpec(typeParams, value, branch) {
				return false
			}
		}
		return true
	}
	// Null is assignable to any nullable type, regardless of element
	// parameterisation. The element walk below would otherwise type-assert
	// the null as a List/Set/Dict and panic.
	if _, isNull := value.(runtime.Null); isNull {
		return spec.nullable
	}
	if !vm.runtimeValueMatchesTypeSpec(value, spec) {
		return false
	}
	if len(spec.args) == 0 {
		return true
	}
	switch spec.baseLower {
	case "list":
		elemSpec := spec.args[0]
		if typeParams[elemSpec.baseLower] {
			break
		}
		lst := value.(runtime.List)
		for _, elem := range lst.Elements {
			if !vm.matchValueToTypeSpec(typeParams, elem, elemSpec) {
				return false
			}
		}
	case "set":
		elemSpec := spec.args[0]
		if typeParams[elemSpec.baseLower] {
			break
		}
		s := value.(runtime.Set)
		for _, entry := range s.Elements {
			if !vm.matchValueToTypeSpec(typeParams, entry.Value, elemSpec) {
				return false
			}
		}
	case "dict":
		if len(spec.args) == 2 {
			keySpec := spec.args[0]
			valSpec := spec.args[1]
			d := value.(runtime.Dict)
			for _, entry := range d.Entries {
				if !typeParams[keySpec.baseLower] && !vm.matchValueToTypeSpec(typeParams, entry.Key, keySpec) {
					return false
				}
				if !typeParams[valSpec.baseLower] && !vm.matchValueToTypeSpec(typeParams, entry.Value, valSpec) {
					return false
				}
			}
		}
	default:
		// Reified user-defined generic class instance: enforce invariance
		// on the bound type parameters. A typed parameter declared as
		// `Box<Base>` must NOT accept a `Box<Sub>` value, because
		// mutating methods on the parameter could otherwise insert a
		// sibling `Base` subtype that violates the original container's
		// declared element type (the same unsoundness that motivates
		// invariance in Kotlin/Java).
		if instance, ok := value.(*runtime.Instance); ok && instance.Class != nil &&
			len(instance.Class.TypeParameters) > 0 && len(instance.TypeBindings) > 0 {
			for i, argSpec := range spec.args {
				if i >= len(instance.Class.TypeParameters) {
					break
				}
				if typeParams[argSpec.baseLower] || argSpec.baseLower == "" || argSpec.kind == vmTypeAny {
					continue
				}
				paramName := instance.Class.TypeParameters[i]
				bound, ok := instance.TypeBindings[paramName]
				if !ok || bound == "" {
					continue
				}
				if !strings.EqualFold(bound, argSpec.base) {
					return false
				}
			}
		}
	}
	return true
}

// collectionMismatchSuffixStr returns a detail string like " (element at index 1 is string)"
// describing the first element that violates the type constraint, or "" when there is no mismatch.
func (vm *VM) collectionMismatchSuffixStr(value runtime.Value, typ string) string {
	spec := vm.typeSpec(typ)
	if len(spec.args) == 0 {
		return ""
	}
	switch v := value.(type) {
	case runtime.List:
		elemSpec := spec.args[0]
		for i, elem := range v.Elements {
			if !vm.matchValueToTypeSpec(nil, elem, elemSpec) {
				return fmt.Sprintf(" (element at index %d is %s)", i, elem.TypeName())
			}
		}
	case runtime.Set:
		elemSpec := spec.args[0]
		for _, entry := range v.Elements {
			if !vm.matchValueToTypeSpec(nil, entry.Value, elemSpec) {
				return fmt.Sprintf(" (element %s is %s)", entry.Value.Inspect(), entry.Value.TypeName())
			}
		}
	case runtime.Dict:
		if len(spec.args) == 2 {
			valSpec := spec.args[1]
			for _, entry := range v.Entries {
				if !vm.matchValueToTypeSpec(nil, entry.Value, valSpec) {
					return fmt.Sprintf(" (value for key %s is %s)", entry.Key.Inspect(), entry.Value.TypeName())
				}
			}
		}
	}
	return ""
}

func (vm *VM) runtimeValueMatchesFunctionType(function FunctionInfo, value runtime.Value, typ string) bool {
	typeParams := function.typeParamSet
	return vm.matchValueToTypeStr(typeParams, value, typ)
}

func (vm *VM) runtimeValueMatchesTypeSpec(value runtime.Value, spec vmTypeSpec) bool {
	if spec.kind == vmTypeAny {
		return true
	}
	if _, ok := value.(runtime.Null); ok {
		return spec.nullable
	}
	switch spec.kind {
	case vmTypeInt:
		switch value.(type) {
		case runtime.SmallInt, runtime.Int:
			return true
		}
		return false
	case vmTypeString:
		_, ok := value.(runtime.String)
		return ok
	case vmTypeBool:
		_, ok := value.(runtime.Bool)
		return ok
	case vmTypeFloat:
		_, ok := value.(runtime.Float)
		return ok
	case vmTypeDecimal:
		_, ok := value.(runtime.Decimal)
		return ok
	case vmTypeList:
		_, ok := value.(runtime.List)
		return ok
	case vmTypeSet:
		_, ok := value.(runtime.Set)
		return ok
	case vmTypeDict:
		_, ok := value.(runtime.Dict)
		return ok
	case vmTypeCallable:
		return runtime.IsCallableValue(value)
	case vmTypeGenerator:
		_, ok := value.(*runtime.Generator)
		return ok
	}
	if value.TypeName() == spec.base {
		return true
	}
	stripped := stripModulePrefix(spec.base)
	// Error-derived values: walk the captured parent chain so a
	// parameter typed `HttpException` accepts a `BadRequestError`.
	if errValue, ok := value.(runtime.Error); ok {
		if strings.EqualFold(errValue.Class, stripped) {
			return true
		}
		for _, ancestor := range errValue.Parents {
			if strings.EqualFold(ancestor, stripped) {
				return true
			}
		}
		return false
	}
	instance, ok := value.(*runtime.Instance)
	if !ok {
		return false
	}
	if classInfo, ok := vm.classInfo(instance.Class.Name); ok {
		if vm.classMatches(classInfo, stripped) || vm.classImplements(classInfo, stripped) {
			return true
		}
	}
	// Fall back to the cross-chunk runtime.Class chain (set up at
	// instance construction) so parameters typed with an imported
	// class still match.
	return runtimeClassMatches(instance.Class, stripped)
}

func (vm *VM) methodCallSpread(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.runtimeError(instruction, "method-call-spread instruction has invalid operands")
	}
	staticArgCount := int(instruction.Operands[1])
	spreadVal, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	staticArgs := make([]runtime.Value, staticArgCount)
	for i := staticArgCount - 1; i >= 0; i-- {
		val, err := vm.pop()
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		staticArgs[i] = val
	}
	spreadList, ok := spreadVal.(runtime.List)
	if !ok {
		return 0, vm.runtimeError(instruction, "spread argument must be a list")
	}
	args := append(staticArgs, spreadList.Elements...)
	for _, a := range args {
		vm.push(a)
	}
	rebuilt := Instruction{
		Op:       OpMethodCall,
		Operands: []int64{instruction.Operands[0], int64(len(args))},
		Line:     instruction.Line,
		Column:   instruction.Column,
	}
	return vm.methodCall(rebuilt, ip)
}

// callResolvedMethod skips classInfo/methodLookup/overload-selection
// when the compiler proved the receiver's class statically.
func (vm *VM) callResolvedMethod(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.runtimeError(instruction, "resolved method call has invalid operands")
	}
	functionIndex := instruction.Operands[0]
	argc := int(instruction.Operands[1])
	if functionIndex < 0 || int(functionIndex) >= len(vm.chunk.Functions) {
		return 0, vm.runtimeError(instruction, "function index out of range")
	}
	n := len(vm.stack)
	if argc+1 > n {
		return 0, vm.runtimeError(instruction, "stack underflow")
	}
	base := n - argc - 1
	function := &vm.chunk.Functions[functionIndex]
	if function.IsGenerator && !vm.generatorExecution {
		callArgs := make([]runtime.Value, argc+1)
		for i := 0; i <= argc; i++ {
			callArgs[i] = vm.stack[base+i].ToValue()
		}
		vm.stack = vm.stack[:base]
		vm.push(vm.lazyGenerator(functionIndex, callArgs))
		return ip, nil
	}
	// Fast path: skip the per-arg ToValue() conversion when
	// startFunctionVMValue can take VMValues directly. Receiver +
	// argc must match the callee's full param count, and we must
	// be able to recover the *Instance receiver from a boxed slot
	// (the only kind the wrapper sets).
	if !function.Variadic && argc+1 == len(function.ParamSlots) {
		receiverSlot := vm.stack[base]
		if receiverSlot.Kind == runtime.VMKindBoxed {
			if instance, ok := receiverSlot.Boxed.(*runtime.Instance); ok {
				stackArgs := vm.stack[base : base+argc+1]
				vm.stack = vm.stack[:base]
				nextIP, err := vm.startFunctionVMValue(instruction, ip, function, stackArgs, nil)
				if err != nil {
					return 0, err
				}
				vm.inheritInstanceTypeBindings(instance)
				return nextIP, nil
			}
		}
	}
	callArgs := make([]runtime.Value, argc+1)
	for i := 0; i <= argc; i++ {
		callArgs[i] = vm.stack[base+i].ToValue()
	}
	vm.stack = vm.stack[:base]
	instance, ok := callArgs[0].(*runtime.Instance)
	if !ok {
		return 0, vm.runtimeError(instruction, "resolved method receiver is not an instance, got %s", callArgs[0].TypeName())
	}
	nextIP, err := vm.startPrevalidatedFunction(instruction, ip, function, callArgs, nil)
	if err != nil {
		return 0, err
	}
	vm.inheritInstanceTypeBindings(instance)
	return nextIP, nil
}

func (vm *VM) methodCall(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.runtimeError(instruction, "method call instruction has invalid operands")
	}
	nameIndex := instruction.Operands[0]
	argc := int(instruction.Operands[1])
	if nameIndex < 0 || int(nameIndex) >= len(vm.chunk.Constants) {
		return 0, vm.runtimeError(instruction, "method name constant out of range")
	}
	nameValue, ok := vm.chunk.Constants[nameIndex].(runtime.String)
	if !ok {
		return 0, vm.runtimeError(instruction, "method name constant must be string")
	}
	// slots[0]=receiver, slots[1:]=args — one alloc instead of two.
	slots := make([]runtime.Value, argc+1)
	for i := argc - 1; i >= 0; i-- {
		value, err := vm.pop()
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		slots[i+1] = value
	}
	receiver, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	slots[0] = receiver
	args := slots[1:]
	if target, ok := receiver.(runtime.DecoratorTarget); ok {
		if nameValue.Value != "__invoke" {
			return 0, vm.runtimeError(instruction, "reflect target has no method %s", nameValue.Value)
		}
		if target.Callable == nil {
			return 0, vm.runtimeError(instruction, "reflect target is not callable")
		}
		result, err := vm.callCallable(target.Callable, args)
		if err != nil {
			return vm.propagateModuleError(instruction, ip, err)
		}
		vm.push(result)
		return ip, nil
	}
	if function, ok := receiver.(runtime.Function); ok {
		if nameValue.Value != "__invoke" {
			return 0, vm.runtimeError(instruction, "func has no method %s", nameValue.Value)
		}
		result, err := vm.callCallable(function, args)
		if err != nil {
			return vm.propagateModuleError(instruction, ip, err)
		}
		vm.push(result)
		return ip, nil
	}
	if overloaded, ok := receiver.(runtime.OverloadedFunction); ok {
		if nameValue.Value != "__invoke" {
			return 0, vm.runtimeError(instruction, "func has no method %s", nameValue.Value)
		}
		result, err := vm.callCallable(overloaded, args)
		if err != nil {
			return vm.propagateModuleError(instruction, ip, err)
		}
		vm.push(result)
		return ip, nil
	}
	if instance, ok := receiver.(*runtime.Instance); ok {
		// Dispatch foreign-class methods through the module loader. A
		// class is foreign when its declaring chunk differs from the
		// chunk this VM is currently executing. This covers stdlib
		// sub-VMs calling main-chunk classes (e.g. a user class
		// passed to streams.copy implementing __read) as well as
		// cross-stdlib-module method calls.
		if instance.Class.Module != vm.moduleName {
			// For classes from foreign modules, prefer native Go methods (e.g.
			// process.Process, http.Response) before falling through to the
			// bytecode module loader, which has no chunk for native modules.
			if nativeMethods := instance.Class.Methods[strings.ToLower(nameValue.Value)]; len(nativeMethods) > 0 && nativeMethods[0].Native != nil {
				result, err := nativeMethods[0].Native(instance, args)
				if err != nil {
					return 0, vm.runtimeError(instruction, "%s", err.Error())
				}
				vm.push(result)
				return ip, nil
			}
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			result, err := vm.moduleLoader.CallModuleMethod(instance.Class.Module, instance.Class.Name, nameValue.Value, instance, vm.wrapStatefulNativeArgs(args))
			if err != nil {
				var notFound *runtime.MethodNotFoundError
				if errors.As(err, &notFound) {
					return vm.throwTyped(instruction, ip, "RuntimeError", fmt.Sprintf("unknown method %s.%s", notFound.Class, notFound.Method))
				}
				return vm.propagateModuleError(instruction, ip, err)
			}
			vm.push(result)
			return ip, nil
		}
		classInfo, ok := vm.classInfo(instance.Class.Name)
		if !ok {
			return 0, vm.runtimeError(instruction, "unknown class %s", instance.Class.Name)
		}
		loweredName := vm.lowerConstantName(nameIndex, nameValue.Value)
		indices, ok := vm.lookupMethodLower(classInfo, loweredName)
		if !ok {
			if fallbackIndices, ok := vm.lookupMethodLower(classInfo, "__call"); ok {
				functionIndex, err := vm.selectRuntimeFunction(instruction, "__call", fallbackIndices, []runtime.Value{runtime.String{Value: nameValue.Value}, runtime.List{Elements: args}}, 1)
				if err != nil {
					return 0, err
				}
				callArgs := []runtime.Value{instance, runtime.String{Value: nameValue.Value}, runtime.List{Elements: args}}
				return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], callArgs, nil)
			}
			if result, handled, err := vm.callBuiltinParentMethod(classInfo, instance, nameValue.Value, args); handled {
				if err != nil {
					return 0, vm.runtimeError(instruction, "%s", err.Error())
				}
				vm.push(result)
				return ip, nil
			}
			// Cross-module parent: the method may live in the parent's
			// own chunk (e.g. `class B extends mod.A` then `b.foo()`).
			// Dispatch via the module loader so the parent's chunk
			// handles the lookup and execution.
			if classInfo.ParentIndex < 0 && strings.Contains(classInfo.ParentName, ".") && vm.moduleLoader != nil {
				if module, parentClass, ok := splitQualifiedClassName(classInfo.ParentName); ok {
					if _, err := vm.moduleLoader.LoadModule(module, module); err == nil {
						result, err := vm.moduleLoader.CallParentInModule(module, parentClass, nameValue.Value, instance, args)
						if err == nil {
							vm.push(result)
							return ip, nil
						}
						var notFound *runtime.MethodNotFoundError
						if !errors.As(err, &notFound) {
							return vm.propagateModuleError(instruction, ip, err)
						}
					}
				}
			}
			if nextIP, err, handled := vm.callInterfaceDefault(instruction, ip, instance, loweredName, args); handled {
				return nextIP, err
			}
			return vm.throwTyped(instruction, ip, "RuntimeError", fmt.Sprintf("unknown method %s.%s", instance.Class.Name, nameValue.Value))
		}
		functionIndex, err := vm.selectRuntimeFunction(instruction, nameValue.Value, indices, args, 1)
		if err != nil {
			return 0, err
		}
		if err := vm.ensureCallableDecorators(); err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		if decorated, ok := vm.decoratedFuncs[functionIndex]; ok {
			if vm.chunk.Functions[functionIndex].Async && !vm.syncMode {
				vm.push(vm.startAsyncCallableWithForwardThis(decorated, args, instance))
				return ip, nil
			}
			result, err := vm.callCallableWithForwardThis(decorated, args, instance)
			if err != nil {
				return 0, vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.push(result)
			return ip, nil
		}
		if vm.chunk.Functions[functionIndex].IsGenerator && !vm.generatorExecution {
			vm.push(vm.lazyGenerator(functionIndex, slots))
			return ip, nil
		}
		nextIP, err := vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], slots, nil)
		if err != nil {
			return 0, err
		}
		vm.inheritInstanceTypeBindings(instance)
		return nextIP, nil
	}
	if task, ok := receiver.(*runtime.Task); ok {
		if len(args) != 0 {
			return 0, vm.runtimeError(instruction, "Task.%s expects no arguments", nameValue.Value)
		}
		switch nameValue.Value {
		case "await":
			result := task.Await()
			if result.Err != nil {
				return 0, vm.runtimeError(instruction, "await: %v", result.Err)
			}
			if result.Value == nil {
				vm.push(runtime.Null{})
			} else {
				vm.push(result.Value)
			}
			return ip, nil
		case "done":
			vm.push(runtime.Bool{Value: task.Done()})
			return ip, nil
		case "cancel":
			task.Cancel()
			vm.push(runtime.Null{})
			return ip, nil
		case "cancelled":
			vm.push(runtime.Bool{Value: task.Cancelled()})
			return ip, nil
		default:
			return 0, vm.runtimeError(instruction, "Task has no method %s", nameValue.Value)
		}
	}
	if instant, ok := receiver.(runtime.DateTimeInstant); ok {
		result, err := native.DateTimeInstantMethod(instant, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if duration, ok := receiver.(runtime.DateTimeDuration); ok {
		result, err := native.DateTimeDurationMethod(duration, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if zone, ok := receiver.(runtime.DateTimeZone); ok {
		result, err := native.DateTimeZoneMethod(zone, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if urlValue, ok := receiver.(runtime.URLValue); ok {
		result, err := native.URLMethod(urlValue, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if headers, ok := receiver.(runtime.HTTPHeaders); ok {
		result, err := vmHTTPHeadersMethod(headers, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if cookie, ok := receiver.(runtime.HTTPCookie); ok {
		result, err := native.HTTPCookieMethod(cookie, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if tmpl, ok := receiver.(runtime.TemplateValue); ok {
		result, err := native.TemplateMethod(tmpl, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if engine, ok := receiver.(runtime.TemplateEngine); ok {
		result, err := native.TemplateEngineMethod(engine, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if errValue, ok := receiver.(runtime.Error); ok {
		result, err := native.ErrorMethod(errValue, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if trace, ok := receiver.(runtime.ErrorStackTrace); ok {
		result, err := native.ErrorStackTraceMethod(trace, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if frame, ok := receiver.(runtime.ErrorStackFrame); ok {
		result, err := native.ErrorStackFrameMethod(frame, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if module, ok := receiver.(*runtime.Module); ok {
		value, ok := module.Exports[nameValue.Value]
		if !ok {
			return 0, vm.runtimeError(instruction, "module %s has no export %s", module.Name, nameValue.Value)
		}
		if function, ok := value.(runtime.BytecodeFunction); ok {
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			result, err := vm.moduleLoader.CallModuleFunction(function, vm.wrapStatefulNativeArgs(args))
			if err != nil {
				return vm.propagateModuleError(instruction, ip, err)
			}
			vm.push(result)
			return ip, nil
		}
		if class, ok := value.(runtime.BytecodeClass); ok {
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			result, err := vm.moduleLoader.ConstructModuleClass(class, vm.wrapStatefulNativeArgs(args))
			if err != nil {
				return vm.propagateModuleError(instruction, ip, err)
			}
			vm.push(result)
			return ip, nil
		}
		return 0, vm.runtimeError(instruction, "%s.%s is not callable", module.Name, nameValue.Value)
	}
	if function, ok := receiver.(runtime.BytecodeFunction); ok {
		if nameValue.Value != "__invoke" {
			return 0, vm.runtimeError(instruction, "function has no method %s", nameValue.Value)
		}
		result, err := vm.callCallable(function, args)
		if err != nil {
			return vm.propagateModuleError(instruction, ip, err)
		}
		vm.push(result)
		return ip, nil
	}
	if class, ok := receiver.(runtime.BytecodeClass); ok {
		/* `Class(...args)` at runtime (e.g. a class ref passed
		 * through a variable, or one obtained from `reflect.class`)
		 * compiles to "push the value, OpMethodCall __invoke".
		 * Treat that as construction rather than a static-method
		 * lookup of a method literally named "__invoke". */
		if nameValue.Value == "__invoke" {
			/* Class declared in a different chunk: route through the
			 * moduleLoader so the index resolves against the right
			 * chunk. Main-chunk classes have Module="" so the check
			 * looks at both directions. */
			if class.Module != vm.moduleName && vm.moduleLoader != nil {
				result, err := vm.moduleLoader.ConstructModuleClass(class, vm.wrapStatefulNativeArgs(args))
				if err != nil {
					return vm.propagateModuleError(instruction, ip, err)
				}
				vm.push(result)
				return ip, nil
			}
			if class.Raw {
				return vm.constructClassWithArgs(instruction, ip, class.Index, args, true)
			}
			result, err := vm.ConstructClass(class.Index, args)
			if err != nil {
				return 0, vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.push(result)
			return ip, nil
		}
		if class.Module != "" && class.Module != vm.moduleName {
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			result, err := vm.moduleLoader.CallModuleStaticMethod(class, nameValue.Value, vm.wrapStatefulNativeArgs(args))
			if err != nil {
				return vm.propagateModuleError(instruction, ip, err)
			}
			vm.push(result)
			return ip, nil
		}
		classInfo, ok := vm.classInfo(class.Name)
		if !ok {
			return 0, vm.runtimeError(instruction, "unknown class %s", class.Name)
		}
		indices, ok := vm.lookupStaticMethod(classInfo, nameValue.Value)
		if !ok {
			return 0, vm.runtimeError(instruction, "unknown static method %s.%s", class.Name, nameValue.Value)
		}
		functionIndex, err := vm.selectRuntimeFunction(instruction, nameValue.Value, indices, args, 0)
		if err != nil {
			return 0, err
		}
		if err := vm.ensureCallableDecorators(); err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		if decorated, ok := vm.decoratedFuncs[functionIndex]; ok {
			result, err := vm.callCallable(decorated, args)
			if err != nil {
				return 0, vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.push(result)
			return ip, nil
		}
		return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], args, nil)
	}
	if closure, ok := receiver.(runtime.BytecodeClosure); ok {
		if nameValue.Value != "__invoke" {
			return 0, vm.runtimeError(instruction, "closure does not have method %s", nameValue.Value)
		}
		if closure.Module != vm.moduleName {
			// Cross-chunk closure invocation - route through the
			// module loader so the FunctionIndex resolves against
			// the chunk that defined the closure. Entry-script
			// closures (Module=="") flow through the same path.
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			result, err := vm.moduleLoader.CallModuleClosure(closure, vm.wrapStatefulNativeArgs(args))
			if err != nil {
				return vm.propagateModuleError(instruction, ip, err)
			}
			vm.push(result)
			return ip, nil
		}
		return vm.startClosureFunction(instruction, ip, closure, args)
	}
	if enumDef, ok := receiver.(*runtime.EnumDef); ok {
		variantName := nameValue.Value
		for _, v := range enumDef.Variants {
			if strings.EqualFold(v.Name, variantName) {
				if v.FieldCount != len(args) {
					return 0, vm.runtimeError(instruction, "enum variant %s.%s expects %d argument(s), got %d", enumDef.Name, v.Name, v.FieldCount, len(args))
				}
				vm.push(runtime.EnumVariant{Enum: enumDef, Variant: v.Name, Fields: args})
				return ip, nil
			}
		}
		return 0, vm.runtimeError(instruction, "enum %s has no variant %s", enumDef.Name, variantName)
	}
	if list, ok := receiver.(runtime.List); ok {
		result, handled, err := vm.listHigherOrderMethod(instruction, list, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		if handled {
			vm.push(result)
			return ip, nil
		}
	}
	if dict, ok := receiver.(runtime.Dict); ok {
		result, handled, err := vm.dictCollectionsMethod(dict, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		if handled {
			vm.push(result)
			return ip, nil
		}
	}
	value, err := primitiveMethod(receiver, nameValue.Value, args)
	if err != nil {
		var typed vmTypedError
		if errors.As(err, &typed) {
			return vm.throwTyped(instruction, ip, typed.class, typed.message)
		}
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	vm.push(value)
	return ip, nil
}

func (vm *VM) methodCallNamed(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) < 2 {
		return 0, vm.runtimeError(instruction, "named method call instruction has invalid operands")
	}
	nameIndex := instruction.Operands[0]
	argc := int(instruction.Operands[1])
	if len(instruction.Operands) != 2+argc {
		return 0, vm.runtimeError(instruction, "named method call argument metadata mismatch")
	}
	if nameIndex < 0 || int(nameIndex) >= len(vm.chunk.Constants) {
		return 0, vm.runtimeError(instruction, "method name constant out of range")
	}
	nameValue, ok := vm.chunk.Constants[nameIndex].(runtime.String)
	if !ok {
		return 0, vm.runtimeError(instruction, "method name constant must be string")
	}
	args := make([]runtime.Value, argc)
	for i := argc - 1; i >= 0; i-- {
		value, err := vm.pop()
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		args[i] = value
	}
	names := make([]string, argc)
	for i := 0; i < argc; i++ {
		argNameIndex := instruction.Operands[2+i]
		if argNameIndex < 0 {
			continue
		}
		name, err := vm.constantStringAt(instruction, argNameIndex, "argument name constant must be string")
		if err != nil {
			return 0, err
		}
		names[i] = name
	}
	receiver, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	if bytecodeFunction, ok := receiver.(runtime.BytecodeFunction); ok {
		if nameValue.Value != "__invoke" {
			return 0, vm.runtimeError(instruction, "function has no method %s", nameValue.Value)
		}
		if int(bytecodeFunction.Index) >= len(vm.chunk.Functions) {
			return 0, vm.runtimeError(instruction, "function index out of range")
		}
		function := vm.chunk.Functions[bytecodeFunction.Index]
		ordered, err := vm.orderRuntimeArguments(instruction, function, args, names, 0)
		if err != nil {
			return 0, err
		}
		result, err := vm.callCallable(bytecodeFunction, ordered)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if closure, ok := receiver.(runtime.BytecodeClosure); ok {
		if nameValue.Value != "__invoke" {
			return 0, vm.runtimeError(instruction, "closure does not have method %s", nameValue.Value)
		}
		if closure.Module != vm.moduleName {
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			result, err := vm.moduleLoader.CallModuleClosure(closure, vm.wrapStatefulNativeArgs(args))
			if err != nil {
				return vm.propagateModuleError(instruction, ip, err)
			}
			vm.push(result)
			return ip, nil
		}
		if int(closure.FunctionIndex) >= len(vm.chunk.Functions) {
			return 0, vm.runtimeError(instruction, "closure function index out of range")
		}
		function := vm.chunk.Functions[closure.FunctionIndex]
		paramOffset := int(function.UpvalueCount)
		ordered, err := vm.orderRuntimeArguments(instruction, function, args, names, paramOffset)
		if err != nil {
			return 0, err
		}
		return vm.startClosureFunction(instruction, ip, closure, ordered)
	}
	instance, ok := receiver.(*runtime.Instance)
	if !ok {
		return 0, vm.runtimeError(instruction, "named method arguments are only supported for class instances")
	}
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		return 0, vm.runtimeError(instruction, "unknown class %s", instance.Class.Name)
	}
	indices, ok := vm.lookupMethod(classInfo, nameValue.Value)
	if !ok {
		return vm.throwTyped(instruction, ip, "RuntimeError", fmt.Sprintf("unknown method %s.%s", instance.Class.Name, nameValue.Value))
	}
	functionIndex, ordered, err := vm.selectRuntimeNamedFunction(instruction, nameValue.Value, indices, args, names, 1)
	if err != nil {
		return 0, err
	}
	if err := vm.ensureCallableDecorators(); err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	if decorated, ok := vm.decoratedFuncs[functionIndex]; ok {
		if vm.chunk.Functions[functionIndex].Async && !vm.syncMode {
			vm.push(vm.startAsyncCallableWithForwardThis(decorated, ordered, instance))
			return ip, nil
		}
		result, err := vm.callCallableWithForwardThis(decorated, ordered, instance)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	callArgs := append([]runtime.Value{instance}, ordered...)
	nextIP, err := vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], callArgs, nil)
	if err != nil {
		return 0, err
	}
	vm.inheritInstanceTypeBindings(instance)
	return nextIP, nil
}

// inheritInstanceTypeBindings merges instance.TypeBindings into the current top call frame,
// adding any bindings not already set by inference from the function's own type params.
func (vm *VM) inheritInstanceTypeBindings(instance *runtime.Instance) {
	if len(instance.TypeBindings) == 0 || len(vm.frames) == 0 {
		return
	}
	frame := &vm.frames[len(vm.frames)-1]
	if frame.typeBindings == nil {
		frame.typeBindings = map[string]string{}
	}
	for name, typeName := range instance.TypeBindings {
		if _, exists := frame.typeBindings[name]; !exists {
			frame.typeBindings[name] = typeName
		}
	}
}

func (vm *VM) classInfo(name string) (ClassInfo, bool) {
	if cached, ok := vm.classInfoNameCache[name]; ok {
		return cached, true
	}
	index, ok := vm.classIndex[strings.ToLower(name)]
	if !ok || index < 0 || index >= len(vm.chunk.Classes) {
		return ClassInfo{}, false
	}
	info := vm.chunk.Classes[index]
	if vm.classInfoNameCache == nil {
		vm.classInfoNameCache = make(map[string]ClassInfo, 8)
	}
	vm.classInfoNameCache[name] = info
	return info, true
}

// lowerConstantName returns the lowercased form of a String constant
// at the given index, memoised in nameLowerCache so the lookup amortises
// to a slice read on the hot method-dispatch path.
func (vm *VM) lowerConstantName(index int64, original string) string {
	if index < 0 {
		return strings.ToLower(original)
	}
	if int(index) < len(vm.nameLowerCache) {
		if lowered := vm.nameLowerCache[index]; lowered != "" {
			return lowered
		}
	} else {
		// Grow lazily; at worst this happens once per distinct
		// method-name constant.
		grown := make([]string, len(vm.chunk.Constants))
		copy(grown, vm.nameLowerCache)
		vm.nameLowerCache = grown
	}
	lowered := strings.ToLower(original)
	if lowered == "" {
		lowered = original // preserve the "not cached" sentinel for empty original
	}
	vm.nameLowerCache[index] = lowered
	return lowered
}

func (vm *VM) lookupMethod(classInfo ClassInfo, name string) ([]int64, bool) {
	return vm.lookupMethodLower(classInfo, strings.ToLower(name))
}

// lookupMethodLower is the cache-friendly entry point: callers that
// already have the lowercased method name pass it directly so the
// dispatch loop avoids repeated strings.ToLower(...) on every call.
// A single-slot cache short-circuits repeated lookups on the same
// (class, method) pair (the common hot-loop case).
func (vm *VM) lookupMethodLower(classInfo ClassInfo, lowered string) ([]int64, bool) {
	if vm.methodLookupValid && vm.methodLookupName == lowered && vm.methodLookupClass == classInfo.Name {
		return vm.methodLookupIndices, true
	}
	indices, ok := vm.lookupMethodLowerUncached(classInfo, lowered)
	if ok {
		vm.methodLookupClass = classInfo.Name
		vm.methodLookupName = lowered
		vm.methodLookupIndices = indices
		vm.methodLookupValid = true
	}
	return indices, ok
}

func (vm *VM) lookupMethodLowerUncached(classInfo ClassInfo, lowered string) ([]int64, bool) {
	if indices, ok := classInfo.Methods[lowered]; ok {
		return indices, true
	}
	if classInfo.ParentIndex >= 0 && int(classInfo.ParentIndex) < len(vm.chunk.Classes) {
		return vm.lookupMethodLowerUncached(vm.chunk.Classes[classInfo.ParentIndex], lowered)
	}
	return nil, false
}

type crossModuleDefault struct {
	module string
	index  int64
}

type extraField struct {
	name string
	typ  string
}

func (vm *VM) callInterfaceDefault(instruction Instruction, ip int, instance *runtime.Instance, methodName string, args []runtime.Value) (int, error, bool) {
	idx, ok := vm.classIndex[strings.ToLower(instance.Class.Name)]
	if !ok {
		return 0, nil, false
	}
	fallback, ok := vm.interfaceFallbacks[int64(idx)][methodName]
	if !ok {
		return 0, nil, false
	}
	if vm.moduleLoader == nil {
		return 0, nil, false
	}
	fn := runtime.BytecodeFunction{Module: fallback.module, Index: fallback.index}
	callArgs := append([]runtime.Value{instance}, args...)
	result, err := vm.moduleLoader.CallModuleFunction(fn, callArgs)
	if err != nil {
		return vm.propagateModuleErrorReturn(instruction, ip, err)
	}
	vm.push(result)
	return ip, nil, true
}

func (vm *VM) propagateModuleErrorReturn(instruction Instruction, ip int, err error) (int, error, bool) {
	nextIP, perr := vm.propagateModuleError(instruction, ip, err)
	return nextIP, perr, true
}

func (vm *VM) resolveCrossModuleInterfaceMembers(instruction Instruction, classIndex int64, classInfo ClassInfo) error {
	if len(classInfo.Implements) == 0 {
		return nil
	}
	declaredFields := map[string]bool{}
	for _, name := range classInfo.FieldNames {
		declaredFields[strings.ToLower(name)] = true
	}
	defaultSource := map[string]string{}
	for _, ifaceRef := range classInfo.Implements {
		module, name, ok := splitQualifiedClassName(ifaceRef)
		if !ok {
			continue
		}
		if vm.moduleLoader == nil {
			continue
		}
		if _, err := vm.moduleLoader.LoadModule(module, module); err != nil {
			continue
		}
		iface, ok := vm.moduleLoader.LookupModuleInterface(module, name)
		if !ok {
			continue
		}
		for i, fieldName := range iface.Fields {
			lower := strings.ToLower(fieldName)
			if declaredFields[lower] {
				continue
			}
			declaredFields[lower] = true
			typ := ""
			if i < len(iface.FieldTypes) {
				typ = iface.FieldTypes[i]
			}
			vm.interfaceExtraFields[classIndex] = append(vm.interfaceExtraFields[classIndex], extraField{name: fieldName, typ: typ})
		}
		for methodName, fnIndex := range iface.Defaults {
			if _, exists := classInfo.Methods[methodName]; exists {
				continue
			}
			if existing, ok := vm.interfaceFallbacks[classIndex][methodName]; ok {
				if existing.module != module || existing.index != fnIndex {
					return vm.runtimeError(instruction, "class %s inherits multiple defaults for %s from %s and %s; class must override", classInfo.Name, methodName, defaultSource[methodName], ifaceRef)
				}
				continue
			}
			if vm.interfaceFallbacks[classIndex] == nil {
				vm.interfaceFallbacks[classIndex] = map[string]crossModuleDefault{}
			}
			vm.interfaceFallbacks[classIndex][methodName] = crossModuleDefault{module: module, index: fnIndex}
			defaultSource[methodName] = ifaceRef
		}
	}
	return nil
}

// Reports (reason, true) when the class is @abstract or carries an
// unoverridden @abstract method. Walks ParentIndex only.
func (vm *VM) classAbstractnessReason(classInfo ClassInfo) (string, bool) {
	for _, dec := range classInfo.Decorators {
		if strings.EqualFold(dec.Name, "abstract") {
			return "cannot instantiate abstract class " + classInfo.Name, true
		}
	}
	overridden := map[string]bool{}
	abstractDecl := map[string]string{}
	walk := func(ci ClassInfo) {
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
	}
	walk(classInfo)
	for ci := classInfo; ci.ParentIndex >= 0 && int(ci.ParentIndex) < len(vm.chunk.Classes); {
		ci = vm.chunk.Classes[ci.ParentIndex]
		walk(ci)
	}
	if len(abstractDecl) == 0 {
		return "", false
	}
	var sample, sampleClass string
	for name, owner := range abstractDecl {
		if sample == "" || name < sample {
			sample = name
			sampleClass = owner
		}
	}
	return "cannot instantiate " + classInfo.Name + ": abstract method " + sampleClass + "." + sample + " is not implemented", true
}

func (vm *VM) lookupDunder(classInfo ClassInfo, canonical, legacy string) ([]int64, string, bool) {
	if indices, ok := vm.lookupMethod(classInfo, canonical); ok {
		return indices, canonical, true
	}
	if indices, ok := vm.lookupMethod(classInfo, legacy); ok {
		return indices, legacy, true
	}
	return nil, "", false
}

func (vm *VM) lookupStaticDunder(classInfo ClassInfo, canonical, legacy string) ([]int64, string, bool) {
	if indices, ok := vm.lookupStaticMethod(classInfo, canonical); ok {
		return indices, canonical, true
	}
	if indices, ok := vm.lookupStaticMethod(classInfo, legacy); ok {
		return indices, legacy, true
	}
	return nil, "", false
}

func (vm *VM) lookupStaticValue(classInfo ClassInfo, name string) (int64, bool) {
	if index, ok := classInfo.StaticValues[name]; ok {
		return index, true
	}
	if classInfo.ParentIndex >= 0 && int(classInfo.ParentIndex) < len(vm.chunk.Classes) {
		return vm.lookupStaticValue(vm.chunk.Classes[classInfo.ParentIndex], name)
	}
	return 0, false
}

func (vm *VM) lookupStaticMethod(classInfo ClassInfo, name string) ([]int64, bool) {
	if indices, ok := classInfo.StaticMethods[strings.ToLower(name)]; ok {
		return indices, true
	}
	if classInfo.ParentIndex >= 0 && int(classInfo.ParentIndex) < len(vm.chunk.Classes) {
		return vm.lookupStaticMethod(vm.chunk.Classes[classInfo.ParentIndex], name)
	}
	return nil, false
}

func (vm *VM) classMatches(classInfo ClassInfo, target string) bool {
	target = stripModulePrefix(target)
	if strings.EqualFold(classInfo.Name, target) {
		return true
	}
	if classInfo.ParentIndex >= 0 && int(classInfo.ParentIndex) < len(vm.chunk.Classes) {
		return vm.classMatches(vm.chunk.Classes[classInfo.ParentIndex], target)
	}
	return false
}

// cloneContainerDefault returns a fresh copy of a mutable container
// value used as a function-parameter default. Sharing the same dict
// or set across calls would let one call's mutations leak into a
// later call (the Python "mutable default argument" trap). Empty
// containers (the common case for `dict opts = {}` / `list xs = []` /
// `set s = set()`) clone in constant time; non-container values are
// returned as-is. Lists are technically immutable in Geblang (push
// returns a new list), but the clone is cheap and defensive.
func cloneContainerDefault(v runtime.Value) runtime.Value {
	switch val := v.(type) {
	case runtime.Dict:
		cloned := make(map[string]runtime.DictEntry, len(val.Entries))
		for k, entry := range val.Entries {
			cloned[k] = entry
		}
		return runtime.Dict{Entries: cloned}
	case runtime.Set:
		cloned := make(map[string]runtime.SetEntry, len(val.Elements))
		for k, entry := range val.Elements {
			cloned[k] = entry
		}
		return runtime.Set{Elements: cloned}
	case runtime.List:
		if len(val.Elements) == 0 {
			return runtime.List{Elements: nil}
		}
		cloned := make([]runtime.Value, len(val.Elements))
		copy(cloned, val.Elements)
		return runtime.List{Elements: cloned}
	}
	return v
}

// stripModulePrefix returns the trailing identifier of a possibly
// qualified type name. "mod.Sub" -> "Sub"; "Sub" -> "Sub". Matches
// `simpleTypeName` in the evaluator.
func stripModulePrefix(typeName string) string {
	if dot := strings.LastIndexByte(typeName, '.'); dot >= 0 {
		return typeName[dot+1:]
	}
	return typeName
}

// dictKeyFor is a fast-path wrapper around native.DictKey for the
// dict / set hot paths. User code keys dicts overwhelmingly by
// string or small integer; both build their canonical key string
// without any allocation beyond the result, while
// `native.DictKey` dispatches through a type switch that also
// handles composite keys recursively. This helper inlines those
// two common cases and falls through to the canonical
// implementation for everything else; the Go compiler can inline
// the wrapper itself when the call site has a known key shape.
func dictKeyFor(value runtime.Value) string {
	switch v := value.(type) {
	case runtime.String:
		return "s" + v.Value
	case runtime.SmallInt:
		return "i" + strconv.FormatInt(v.Value, 10)
	}
	return native.DictKey(value)
}

// runtimeInterfacesForClass builds the runtime.Class.Implements slice
// for cross-chunk instanceof. Each implemented interface is captured by
// name (and a flattened parent chain when reachable in the current
// chunk) so `instance instanceof iface` resolves regardless of which
// module declared the interface.
func (vm *VM) runtimeInterfacesForClass(classInfo ClassInfo) []*runtime.Interface {
	if len(classInfo.Implements) == 0 {
		return nil
	}
	out := make([]*runtime.Interface, 0, len(classInfo.Implements))
	for _, name := range classInfo.Implements {
		out = append(out, vm.buildRuntimeInterface(name, map[string]bool{}))
	}
	return out
}

func (vm *VM) buildRuntimeInterface(name string, seen map[string]bool) *runtime.Interface {
	if name == "" || seen[strings.ToLower(name)] {
		return nil
	}
	seen[strings.ToLower(name)] = true
	iface := &runtime.Interface{Name: name}
	if info, ok := vm.lookupInterfaceInfo(name); ok {
		for _, parentName := range info.Parents {
			if p := vm.buildRuntimeInterface(parentName, seen); p != nil {
				iface.Parents = append(iface.Parents, p)
			}
		}
	}
	return iface
}

// runtimeClassForParent constructs a runtime.Class for the parent of the
// given ClassInfo, walking the ParentIndex chain. Returns nil when the
// class has no parent. The returned value's Parent field is set
// recursively so the full chain is reachable via Go pointers - this
// lets cross-module `instanceof` / `reflect.parent` find ancestors
// without falling back to the chunk-local classIndex.
func (vm *VM) runtimeClassForParent(classInfo ClassInfo) *runtime.Class {
	if classInfo.ParentIndex < 0 || int(classInfo.ParentIndex) >= len(vm.chunk.Classes) {
		return nil
	}
	parentInfo := vm.chunk.Classes[classInfo.ParentIndex]
	return &runtime.Class{
		Name:           parentInfo.Name,
		Module:         vm.moduleName,
		TypeParameters: append([]string(nil), parentInfo.TypeParameters...),
		Methods:        vm.runtimeMethodWrappers(classInfo.ParentIndex),
		MethodMetadata: vm.classFunctionMetadata(parentInfo.Methods, "method", 1),
		StaticMetadata: vm.classFunctionMetadata(parentInfo.StaticMethods, "staticMethod", 0),
		Parent:         vm.runtimeClassForParent(parentInfo),
		Implements:     vm.runtimeInterfacesForClass(parentInfo),
	}
}

// errorClassMatches walks an error class's parent chain so
// `instanceof BadRequestError` / `instanceof HttpException` /
// `instanceof RuntimeError` all match an error-derived value, even
// when the error class was defined in another module than the chunk
// currently executing. Match is case-insensitive.
func (vm *VM) errorClassMatches(errValue runtime.Error, target string) bool {
	if strings.EqualFold(errValue.Class, target) {
		return true
	}
	for _, ancestor := range errValue.Parents {
		if strings.EqualFold(ancestor, target) {
			return true
		}
	}
	return false
}

// reflectFieldsResult returns the per-field metadata list shape that
// reflect.fields produces - {name, type, nullable, hasDefault} dicts.
// Pulls type info from the chunk's class table when available, falling
// back to "any" for builtin classes or compile-time targets without
// type info.
func (vm *VM) reflectFieldsResult(target runtime.Value, metadata runtime.ClassMetadata) runtime.Value {
	if bc, ok := target.(runtime.BytecodeClass); ok && vm.moduleLoader != nil && bc.Module != vm.moduleName {
		if result, err := vm.moduleLoader.FieldsForModuleClass(bc); err == nil {
			return result
		}
	}
	if instance, ok := target.(*runtime.Instance); ok && instance != nil && instance.Class != nil {
		// Prefer the chunk-local ClassInfo when the instance's class
		// is in this VM's classIndex - it carries full FieldTypes
		// strings. Fall back to runtime.Class.Fields (populated at
		// construction in any originating chunk) for cross-chunk
		// instances; that path loses the type-string detail but
		// retains decorators so framework reflection still works
		// across module boundaries.
		if idx, ok := vm.classIndex[strings.ToLower(instance.Class.Name)]; ok && int(idx) < len(vm.chunk.Classes) {
			target = vm.bytecodeClassFromInfo(vm.chunk.Classes[idx], int64(idx))
		} else if len(instance.Class.Fields) > 0 {
			entries := make([]runtime.Value, 0, len(instance.Class.Fields))
			for _, field := range instance.Class.Fields {
				fieldType := "any"
				nullable := false
				if field.Type != nil {
					fieldType = field.Type.String()
					nullable = field.Type.Nullable
				}
				dictEntries := map[string]runtime.DictEntry{
					native.DictKey(runtime.String{Value: "name"}):       {Key: runtime.String{Value: "name"}, Value: runtime.String{Value: field.Name}},
					native.DictKey(runtime.String{Value: "type"}):       {Key: runtime.String{Value: "type"}, Value: runtime.String{Value: fieldType}},
					native.DictKey(runtime.String{Value: "nullable"}):   {Key: runtime.String{Value: "nullable"}, Value: runtime.Bool{Value: nullable}},
					native.DictKey(runtime.String{Value: "hasDefault"}): {Key: runtime.String{Value: "hasDefault"}, Value: runtime.Bool{Value: field.Default != nil}},
				}
				/* Cross-chunk reflection: the field decorators come
				 * from the originating chunk's ClassInfo and were
				 * persisted onto runtime.Class.Fields at instance
				 * construction time. Surface them on the same shape
				 * as the bytecode-class path. */
				if len(field.Decorators) > 0 {
					decValues := make([]runtime.Value, 0, len(field.Decorators))
					for _, dec := range field.Decorators {
						decValues = append(decValues, decoratorMetadataDictFromAST(dec))
					}
					key := runtime.String{Value: "decorators"}
					dictEntries[native.DictKey(key)] = runtime.DictEntry{Key: key, Value: runtime.List{Elements: decValues}}
				}
				entries = append(entries, runtime.Dict{Entries: dictEntries})
			}
			return runtime.List{Elements: entries}
		}
	}
	// Pull type info from the chunk's class table when reachable
	// (BytecodeClass / DecoratorTarget / string-name).
	var classInfo ClassInfo
	var haveClass bool
	switch v := target.(type) {
	case runtime.BytecodeClass:
		if v.Index >= 0 && int(v.Index) < len(vm.chunk.Classes) {
			classInfo = vm.chunk.Classes[v.Index]
			haveClass = true
		}
	case runtime.DecoratorTarget:
		if v.Class != nil {
			if idx, ok := vm.classIndex[strings.ToLower(v.Class.Name)]; ok && int(idx) < len(vm.chunk.Classes) {
				classInfo = vm.chunk.Classes[idx]
				haveClass = true
			}
		}
	case runtime.String:
		if idx, ok := vm.classIndex[strings.ToLower(v.Value)]; ok && int(idx) < len(vm.chunk.Classes) {
			classInfo = vm.chunk.Classes[idx]
			haveClass = true
		}
	}
	if haveClass {
		type fieldEntry struct {
			name string
			dict runtime.Value
		}
		entries := make([]fieldEntry, 0, len(classInfo.FieldNames))
		for i, name := range classInfo.FieldNames {
			fieldType := "any"
			nullable := false
			if i < len(classInfo.FieldTypes) && classInfo.FieldTypes[i] != "" {
				fieldType = classInfo.FieldTypes[i]
				nullable = strings.HasPrefix(fieldType, "?")
			}
			fieldDict := map[string]runtime.DictEntry{
				native.DictKey(runtime.String{Value: "name"}):       {Key: runtime.String{Value: "name"}, Value: runtime.String{Value: name}},
				native.DictKey(runtime.String{Value: "type"}):       {Key: runtime.String{Value: "type"}, Value: runtime.String{Value: fieldType}},
				native.DictKey(runtime.String{Value: "nullable"}):   {Key: runtime.String{Value: "nullable"}, Value: runtime.Bool{Value: nullable}},
				native.DictKey(runtime.String{Value: "hasDefault"}): {Key: runtime.String{Value: "hasDefault"}, Value: runtime.Bool{Value: false}},
			}
			if i < len(classInfo.FieldDecorators) {
				decValues := make([]runtime.Value, 0, len(classInfo.FieldDecorators[i]))
				for _, dec := range classInfo.FieldDecorators[i] {
					decValues = append(decValues, decoratorMetadataDict(dec))
				}
				key := runtime.String{Value: "decorators"}
				fieldDict[native.DictKey(key)] = runtime.DictEntry{Key: key, Value: runtime.List{Elements: decValues}}
			}
			entries = append(entries, fieldEntry{name: name, dict: runtime.Dict{Entries: fieldDict}})
		}
		// Sort alphabetically by name to match the evaluator's ordering.
		sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
		out := make([]runtime.Value, 0, len(entries))
		for _, e := range entries {
			out = append(out, e.dict)
		}
		return runtime.List{Elements: out}
	}
	// Last resort: name-only entries with type="any".
	entries := make([]runtime.Value, 0, len(metadata.Fields))
	for _, name := range metadata.Fields {
		entries = append(entries, runtime.Dict{Entries: map[string]runtime.DictEntry{
			native.DictKey(runtime.String{Value: "name"}):       {Key: runtime.String{Value: "name"}, Value: runtime.String{Value: name}},
			native.DictKey(runtime.String{Value: "type"}):       {Key: runtime.String{Value: "type"}, Value: runtime.String{Value: "any"}},
			native.DictKey(runtime.String{Value: "nullable"}):   {Key: runtime.String{Value: "nullable"}, Value: runtime.Bool{Value: false}},
			native.DictKey(runtime.String{Value: "hasDefault"}): {Key: runtime.String{Value: "hasDefault"}, Value: runtime.Bool{Value: false}},
		}})
	}
	return runtime.List{Elements: entries}
}

// errorParentChain returns the parent chain for an error-derived
// ClassInfo, immediate parent first, the built-in chain (RuntimeError
// -> Error) terminating at the root. Used at error-value construction
// to capture the chain so cross-module instanceof checks work without
// requiring access to the originating chunk.
func (vm *VM) errorParentChain(classInfo ClassInfo) []string {
	var parents []string
	visited := map[string]bool{}
	current := classInfo
	for {
		if current.ParentName == "" {
			break
		}
		if visited[current.ParentName] {
			break
		}
		visited[current.ParentName] = true
		parents = append(parents, current.ParentName)
		if classIndex, ok := vm.classIndex[strings.ToLower(current.ParentName)]; ok && int(classIndex) < len(vm.chunk.Classes) {
			current = vm.chunk.Classes[classIndex]
			continue
		}
		// Cross-chunk or built-in parent - extend via the static
		// built-in chain.
		for next := isBuiltinErrorChainParent(current.ParentName); next != ""; next = isBuiltinErrorChainParent(next) {
			if visited[next] {
				break
			}
			visited[next] = true
			parents = append(parents, next)
		}
		break
	}
	return parents
}

// isBuiltinErrorChainParent returns the static parent name for built-in
// error class names (RuntimeError -> Error etc.). Empty when the input
// has no static parent.
func isBuiltinErrorChainParent(class string) string {
	switch class {
	case "RuntimeError", "TypeError", "ValueError", "IOError", "ParseError", "MatchError", "ImmutableError":
		return "Error"
	}
	return ""
}

// runtimeClassMatches walks an instance's runtime.Class parent chain
// (Go pointer-following) and returns true if any ancestor name (or any
// implemented interface name) matches the target. Used as a fallback
// in instanceof / cast / catch when the instance's class was defined
// in a module other than the active chunk, so vm.classIndex doesn't
// have it.
func runtimeClassMatches(class *runtime.Class, target string) bool {
	for c := class; c != nil; c = c.Parent {
		if strings.EqualFold(stripModulePrefix(c.Name), target) {
			return true
		}
		for _, iface := range c.Implements {
			if iface != nil && runtimeInterfaceMatches(iface, target) {
				return true
			}
		}
	}
	return false
}

func runtimeInterfaceMatches(iface *runtime.Interface, target string) bool {
	if iface == nil {
		return false
	}
	if strings.EqualFold(stripModulePrefix(iface.Name), target) {
		return true
	}
	for _, parent := range iface.Parents {
		if runtimeInterfaceMatches(parent, target) {
			return true
		}
	}
	return false
}

func (vm *VM) classImplements(classInfo ClassInfo, target string) bool {
	target = stripModulePrefix(target)
	for _, implemented := range classInfo.Implements {
		if vm.interfaceMatches(implemented, target) {
			return true
		}
	}
	if classInfo.ParentIndex >= 0 && int(classInfo.ParentIndex) < len(vm.chunk.Classes) {
		return vm.classImplements(vm.chunk.Classes[classInfo.ParentIndex], target)
	}
	return false
}

func (vm *VM) checkTypeParamConstraints(instruction Instruction, function *FunctionInfo, typeBindings map[string]string) error {
	if len(function.TypeParamConstraintExprs) == 0 {
		return nil
	}
	for i, expr := range function.TypeParamConstraintExprs {
		if expr == "" || i >= len(function.TypeParameters) {
			continue
		}
		paramName := function.TypeParameters[i]
		boundName, ok := typeBindings[paramName]
		if !ok {
			continue
		}
		classInfo, found := vm.classInfo(boundName)
		if !found {
			return vm.runtimeError(instruction, "type %s does not implement constraint %s for type parameter %s", boundName, expr, paramName)
		}
		if !vm.classMatchesConstraintExpr(classInfo, expr) {
			return vm.runtimeError(instruction, "type %s does not implement constraint %s for type parameter %s", boundName, expr, paramName)
		}
	}
	return nil
}

func (vm *VM) classMatchesConstraintExpr(classInfo ClassInfo, expr string) bool {
	expr = stripOuterConstraintParens(strings.TrimSpace(expr))
	if idx := topLevelConstraintOperator(expr, "|"); idx >= 0 {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+1:])
		return vm.classMatchesConstraintExpr(classInfo, left) || vm.classMatchesConstraintExpr(classInfo, right)
	}
	if idx := topLevelConstraintOperator(expr, "&"); idx >= 0 {
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+1:])
		return vm.classMatchesConstraintExpr(classInfo, left) && vm.classMatchesConstraintExpr(classInfo, right)
	}
	return vm.classImplements(classInfo, strings.TrimSpace(expr))
}

func stripOuterConstraintParens(expr string) string {
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		depth := 0
		wraps := true
		for i, r := range expr {
			switch r {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 && i != len(expr)-1 {
					wraps = false
				}
			}
			if depth < 0 {
				return expr
			}
		}
		if !wraps || depth != 0 {
			return expr
		}
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	return expr
}

func topLevelConstraintOperator(expr string, op string) int {
	depth := 0
	for i, r := range expr {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		default:
			if depth == 0 && string(r) == op {
				return i
			}
		}
	}
	return -1
}

func (vm *VM) hasTestAncestor(classInfo ClassInfo) bool {
	if classInfo.ParentIndex >= 0 && int(classInfo.ParentIndex) < len(vm.chunk.Classes) {
		return vm.hasTestAncestor(vm.chunk.Classes[classInfo.ParentIndex])
	}
	return strings.EqualFold(classInfo.ParentName, "test.Test")
}

func (vm *VM) callBuiltinParentMethod(classInfo ClassInfo, instance *runtime.Instance, name string, args []runtime.Value) (runtime.Value, bool, error) {
	if !vm.hasTestAncestor(classInfo) {
		return nil, false, nil
	}
	if strings.EqualFold(name, "assertThrows") {
		value, err := vm.assertThrowsImpl(args)
		return value, true, err
	}
	if strings.EqualFold(name, "assertThrowsOf") {
		value, err := vm.assertThrowsOfImpl(args)
		return value, true, err
	}
	return runtime.RunTestAssertion(name, args)
}

func (vm *VM) assertThrowsOfImpl(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("Test.assertThrowsOf expects (callable, classOrName[, expectedSubstring])")
	}
	expectedClass, err := classNameFromArgValue(args[1])
	if err != nil {
		return nil, fmt.Errorf("Test.assertThrowsOf: %w", err)
	}
	var expectedSub string
	if len(args) == 3 {
		s, ok := args[2].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Test.assertThrowsOf: third argument must be a string substring")
		}
		expectedSub = s.Value
	}
	_, err = vm.callCallable(args[0], nil)
	if err == nil {
		return nil, fmt.Errorf("expected callable to throw %s, but it returned normally", expectedClass)
	}
	actualClass := extractThrownErrorClass(err)
	if !vm.errorTypeMatchesClass(actualClass, expectedClass) {
		return nil, fmt.Errorf("expected %s, got %s: %s", expectedClass, actualClass, err.Error())
	}
	if expectedSub != "" && !strings.Contains(err.Error(), expectedSub) {
		return nil, fmt.Errorf("expected error containing %q, got %q", expectedSub, err.Error())
	}
	return runtime.Null{}, nil
}

func classNameFromArgValue(v runtime.Value) (string, error) {
	switch x := v.(type) {
	case runtime.String:
		return x.Value, nil
	case runtime.BytecodeClass:
		return x.Name, nil
	}
	return "", fmt.Errorf("expected class value or class name string, got %s", v.TypeName())
}

func extractThrownErrorClass(err error) string {
	var typed runtime.TypedError
	if errors.As(err, &typed) {
		return typed.ErrorClass()
	}
	return runtime.RecoverableErrorClass(err)
}

// errorTypeMatchesClass mirrors the evaluator's errorTypeMatches
// but walks the chunk's class table for user-defined error
// hierarchies plus the built-in error chain for system classes.
func (vm *VM) errorTypeMatchesClass(actual, target string) bool {
	if target == "" || target == "Error" {
		return true
	}
	if actual == target {
		return true
	}
	for current := actual; current != ""; {
		if current == target {
			return true
		}
		next, ok := vm.lookupErrorParent(current)
		if !ok {
			break
		}
		current = next
	}
	return false
}

func (vm *VM) lookupErrorParent(class string) (string, bool) {
	for _, c := range vm.chunk.Classes {
		if c.Name == class {
			if c.ParentName != "" {
				return c.ParentName, true
			}
			break
		}
	}
	switch class {
	case "RuntimeError", "TypeError", "ValueError", "IOError", "ParseError", "MatchError", "ImmutableError", "PermissionError":
		return "Error", true
	}
	return "", false
}

// assertThrowsImpl mirrors the evaluator's assertThrows so VM-mode
// tests can use the same helper. Signature:
// assertThrows(callable) or assertThrows(callable, expectedSubstring).
func (vm *VM) assertThrowsImpl(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("Test.assertThrows expects (callable[, expectedSubstring])")
	}
	var expectedSub string
	if len(args) == 2 {
		s, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Test.assertThrows: second argument must be a string substring")
		}
		expectedSub = s.Value
	}
	_, err := vm.callCallable(args[0], nil)
	if err == nil {
		return nil, fmt.Errorf("expected callable to throw, but it returned normally")
	}
	if expectedSub != "" && !strings.Contains(err.Error(), expectedSub) {
		return nil, fmt.Errorf("expected error containing %q, got %q", expectedSub, err.Error())
	}
	return runtime.Null{}, nil
}

// HasInstanceMethod reports whether instance has a method with the given name,
// including methods inherited from a builtin parent class like test.Test.
func (vm *VM) HasInstanceMethod(instance *runtime.Instance, name string) bool {
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		return false
	}
	if _, ok := vm.lookupMethod(classInfo, name); ok {
		return true
	}
	_, handled, _ := vm.callBuiltinParentMethod(classInfo, instance, name, nil)
	return handled
}

// CallInstanceMethod calls a named method on instance via the VM.
func (vm *VM) CallInstanceMethod(instance *runtime.Instance, name string, args []runtime.Value) (runtime.Value, error) {
	return vm.CallMethod(instance, name, args)
}

// RunTestClass runs all @test-decorated methods on a bytecode class and returns a result dict.
// PatchNative installs a registry override so subsequent calls
// to `module.name` dispatch through `fn` instead of the originally
// registered native. Used by test.mock; the evaluator pairs this
// with NativeSnapshot / RestoreNatives so patches roll back at
// @test method boundaries.
func (vm *VM) PatchNative(module, name string, fn native.Function) {
	vm.natives.Patch(module, name, fn)
}

// UnpatchNative removes a single patch.
func (vm *VM) UnpatchNative(module, name string) {
	vm.natives.Unpatch(module, name)
}

// NativeSnapshot returns the active patch map.
func (vm *VM) NativeSnapshot() map[string]native.Function {
	return vm.natives.Snapshot()
}

// RestoreNatives replaces the active patch map with `snapshot`.
// Pass nil to clear every patch.
func (vm *VM) RestoreNatives(snapshot map[string]native.Function) {
	vm.natives.Restore(snapshot)
}

func (vm *VM) RunTestClass(classIndex int64, tagFilter []string) (runtime.Value, error) {
	if classIndex < 0 || int(classIndex) >= len(vm.chunk.Classes) {
		return nil, fmt.Errorf("class index out of range")
	}
	classInfo := vm.chunk.Classes[classIndex]

	tagSet := map[string]bool{}
	for _, t := range tagFilter {
		tagSet[strings.ToLower(t)] = true
	}

	// callKey is the lookup key; displayName is used in failure messages.
	type testMethod struct {
		callKey     string
		displayName string
	}
	var testMethods []testMethod
	seenMethods := map[string]bool{}

	var collectMethods func(info ClassInfo)
	collectMethods = func(info ClassInfo) {
		for methodKey, indices := range info.Methods {
			if seenMethods[methodKey] {
				continue
			}
			decs := info.MethodDecorators[methodKey]
			hasTest := false
			for _, dec := range decs {
				if strings.EqualFold(dec.Name, "test") {
					hasTest = true
					break
				}
			}
			if !hasTest {
				continue
			}
			if len(tagSet) > 0 {
				hasTag := false
				for _, dec := range decs {
					if strings.EqualFold(dec.Name, "tag") {
						for _, arg := range dec.Args {
							if s, ok := arg.(runtime.String); ok && tagSet[strings.ToLower(s.Value)] {
								hasTag = true
							}
						}
					}
				}
				if !hasTag {
					continue
				}
			}
			seenMethods[methodKey] = true
			displayName := methodKey
			if len(indices) > 0 && indices[0] >= 0 && int(indices[0]) < len(vm.chunk.Functions) {
				if n := vm.chunk.Functions[indices[0]].Name; n != "" {
					// Function names are stored as "ClassName.methodname" — strip class prefix.
					if dotIdx := strings.LastIndex(n, "."); dotIdx >= 0 {
						displayName = n[dotIdx+1:]
					} else {
						displayName = n
					}
				}
			}
			testMethods = append(testMethods, testMethod{callKey: methodKey, displayName: displayName})
		}
		if info.ParentIndex >= 0 && int(info.ParentIndex) < len(vm.chunk.Classes) {
			collectMethods(vm.chunk.Classes[info.ParentIndex])
		}
	}
	collectMethods(classInfo)

	instanceValue, err := vm.ConstructClass(classIndex, nil)
	if err != nil {
		return nil, err
	}
	instance, ok := instanceValue.(*runtime.Instance)
	if !ok {
		return nil, fmt.Errorf("ConstructClass did not return an instance")
	}

	hasMethod := func(name string) bool {
		return vm.HasInstanceMethod(instance, name)
	}
	callHook := func(name string) error {
		_, err := vm.CallMethod(instance, name, nil)
		return err
	}

	total := int64(0)
	passed := int64(0)
	failed := int64(0)
	failures := []runtime.Value{}
	tests := []runtime.Value{}

	buildTestEntry := func(name string, ok bool, message string) runtime.Value {
		entries := map[string]runtime.DictEntry{}
		k := runtime.String{Value: "name"}
		entries[native.DictKey(k)] = runtime.DictEntry{Key: k, Value: runtime.String{Value: name}}
		k = runtime.String{Value: "passed"}
		entries[native.DictKey(k)] = runtime.DictEntry{Key: k, Value: runtime.Bool{Value: ok}}
		if !ok {
			k = runtime.String{Value: "message"}
			entries[native.DictKey(k)] = runtime.DictEntry{Key: k, Value: runtime.String{Value: message}}
		}
		return runtime.Dict{Entries: entries}
	}

	setupFailed := false
	if hasMethod("setupClass") {
		if err := callHook("setupClass"); err != nil {
			setupFailed = true
			failed = int64(len(testMethods))
			for _, m := range testMethods {
				total++
				failures = append(failures, runtime.String{Value: m.displayName + ": setupClass: " + err.Error()})
				tests = append(tests, buildTestEntry(m.displayName, false, "setupClass: "+err.Error()))
			}
		}
	}

	if !setupFailed {
		for _, m := range testMethods {
			total++
			var testErr error
			if hasMethod("setup") {
				testErr = callHook("setup")
			}
			if testErr == nil {
				_, testErr = vm.CallMethod(instance, m.callKey, nil)
			}
			if hasMethod("teardown") {
				if tdErr := callHook("teardown"); tdErr != nil {
					if testErr != nil {
						testErr = fmt.Errorf("%v; teardown: %w", testErr, tdErr)
					} else {
						testErr = fmt.Errorf("teardown: %w", tdErr)
					}
				}
			}
			if testErr != nil {
				failed++
				failures = append(failures, runtime.String{Value: m.displayName + ": " + testErr.Error()})
				tests = append(tests, buildTestEntry(m.displayName, false, testErr.Error()))
			} else {
				passed++
				tests = append(tests, buildTestEntry(m.displayName, true, ""))
			}
		}
	}

	if hasMethod("teardownClass") {
		if err := callHook("teardownClass"); err != nil {
			failed++
			failures = append(failures, runtime.String{Value: "teardownClass: " + err.Error()})
			if passed > 0 {
				passed--
			}
			if total == 0 {
				total = 1
			}
		}
	}

	setEntry := func(entries map[string]runtime.DictEntry, key string, val runtime.Value) {
		k := runtime.String{Value: key}
		entries[native.DictKey(k)] = runtime.DictEntry{Key: k, Value: val}
	}
	entries := map[string]runtime.DictEntry{}
	setEntry(entries, "total", runtime.NewInt64(total))
	setEntry(entries, "passed", runtime.NewInt64(passed))
	setEntry(entries, "failed", runtime.NewInt64(failed))
	setEntry(entries, "failures", runtime.List{Elements: failures})
	setEntry(entries, "tests", runtime.List{Elements: tests})
	return runtime.Dict{Entries: entries}, nil
}

func (vm *VM) interfaceMatches(name string, target string) bool {
	/* Stored interface names may be module-qualified (e.g.
	 * "repository.Repository") for cross-module `implements` refs;
	 * the caller already strips the prefix from `target`, so match
	 * the stored name's trailing identifier too. */
	if strings.EqualFold(stripModulePrefix(name), target) {
		return true
	}
	for _, iface := range vm.chunk.Interfaces {
		if !strings.EqualFold(iface.Name, name) {
			continue
		}
		for _, parent := range iface.Parents {
			if vm.interfaceMatches(parent, target) {
				return true
			}
		}
	}
	return false
}

func (vm *VM) lookupInterfaceInfo(name string) (InterfaceInfo, bool) {
	for _, iface := range vm.chunk.Interfaces {
		if strings.EqualFold(iface.Name, name) {
			return iface, true
		}
	}
	return InterfaceInfo{}, false
}

func (vm *VM) dictCollectionsMethod(graph runtime.Dict, name string, args []runtime.Value) (runtime.Value, bool, error) {
	switch name {
	case "bfs":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("collections.bfs expects (graph, start)")
		}
		start := args[0]
		seen := map[string]bool{native.DictKey(start): true}
		queue := []runtime.Value{start}
		visited := []runtime.Value{}
		for len(queue) > 0 {
			node := queue[0]
			queue = queue[1:]
			visited = append(visited, node)
			if entry, ok := graph.Entries[native.DictKey(node)]; ok {
				if neighbors, ok := entry.Value.(runtime.List); ok {
					for _, nb := range neighbors.Elements {
						k := native.DictKey(nb)
						if !seen[k] {
							seen[k] = true
							queue = append(queue, nb)
						}
					}
				}
			}
		}
		return runtime.List{Elements: visited}, true, nil
	case "dfs":
		if len(args) != 1 {
			return nil, true, fmt.Errorf("collections.dfs expects (graph, start)")
		}
		start := args[0]
		seen := map[string]bool{}
		stack := []runtime.Value{start}
		visited := []runtime.Value{}
		for len(stack) > 0 {
			node := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			k := native.DictKey(node)
			if seen[k] {
				continue
			}
			seen[k] = true
			visited = append(visited, node)
			if entry, ok := graph.Entries[native.DictKey(node)]; ok {
				if neighbors, ok := entry.Value.(runtime.List); ok {
					for i := len(neighbors.Elements) - 1; i >= 0; i-- {
						nb := neighbors.Elements[i]
						if !seen[native.DictKey(nb)] {
							stack = append(stack, nb)
						}
					}
				}
			}
		}
		return runtime.List{Elements: visited}, true, nil
	case "topologicalSort":
		if len(args) != 0 {
			return nil, true, fmt.Errorf("collections.topologicalSort expects (graph)")
		}
		allNodes := map[string]runtime.Value{}
		inDegree := map[string]int{}
		for _, entry := range graph.Entries {
			k := native.DictKey(entry.Key)
			allNodes[k] = entry.Key
			if _, ok := inDegree[k]; !ok {
				inDegree[k] = 0
			}
			if neighbors, ok := entry.Value.(runtime.List); ok {
				for _, nb := range neighbors.Elements {
					nbKey := native.DictKey(nb)
					if _, exists := allNodes[nbKey]; !exists {
						allNodes[nbKey] = nb
					}
					inDegree[nbKey]++
				}
			}
		}
		zeroKeys := make([]string, 0)
		for k, deg := range inDegree {
			if deg == 0 {
				zeroKeys = append(zeroKeys, k)
			}
		}
		sort.Strings(zeroKeys)
		queue := make([]runtime.Value, 0, len(zeroKeys))
		for _, k := range zeroKeys {
			queue = append(queue, allNodes[k])
		}
		result := []runtime.Value{}
		for len(queue) > 0 {
			node := queue[0]
			queue = queue[1:]
			result = append(result, node)
			if entry, ok := graph.Entries[native.DictKey(node)]; ok {
				if neighbors, ok := entry.Value.(runtime.List); ok {
					for _, nb := range neighbors.Elements {
						nbKey := native.DictKey(nb)
						inDegree[nbKey]--
						if inDegree[nbKey] == 0 {
							queue = append(queue, nb)
						}
					}
				}
			}
		}
		if len(result) != len(allNodes) {
			return nil, true, fmt.Errorf("collections.topologicalSort: cycle detected")
		}
		return runtime.List{Elements: result}, true, nil
	case "shortestPath":
		if len(args) != 2 {
			return nil, true, fmt.Errorf("collections.shortestPath expects (graph, start, end)")
		}
		start, end := args[0], args[1]
		endKey := native.DictKey(end)
		parent := map[string]runtime.Value{}
		seen := map[string]bool{native.DictKey(start): true}
		queue := []runtime.Value{start}
		found := false
		for len(queue) > 0 && !found {
			node := queue[0]
			queue = queue[1:]
			if native.DictKey(node) == endKey {
				found = true
				break
			}
			if entry, ok := graph.Entries[native.DictKey(node)]; ok {
				if neighbors, ok := entry.Value.(runtime.List); ok {
					for _, nb := range neighbors.Elements {
						k := native.DictKey(nb)
						if !seen[k] {
							seen[k] = true
							parent[k] = node
							queue = append(queue, nb)
						}
					}
				}
			}
		}
		if !found {
			return runtime.Null{}, true, nil
		}
		path := []runtime.Value{end}
		cur := end
		for native.DictKey(cur) != native.DictKey(start) {
			p, ok := parent[native.DictKey(cur)]
			if !ok {
				return runtime.Null{}, true, nil
			}
			path = append([]runtime.Value{p}, path...)
			cur = p
		}
		return runtime.List{Elements: path}, true, nil
	}
	return nil, false, nil
}

func primitiveReSplit(patternArg runtime.Value, text string) (runtime.Value, error) {
	pattern, ok := patternArg.(runtime.String)
	if !ok {
		return nil, fmt.Errorf("regex pattern must be string")
	}
	re, err := native.CompileCachedRegex(pattern.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %v", err)
	}
	parts := re.Split(text, -1)
	out := make([]runtime.Value, len(parts))
	for i, p := range parts {
		out[i] = runtime.String{Value: p}
	}
	return runtime.List{Elements: out}, nil
}

func primitiveReReplace(patternArg runtime.Value, text string, replArg runtime.Value) (runtime.Value, error) {
	pattern, ok := patternArg.(runtime.String)
	if !ok {
		return nil, fmt.Errorf("regex pattern must be string")
	}
	repl, ok := replArg.(runtime.String)
	if !ok {
		return nil, fmt.Errorf("regex replacement must be string")
	}
	re, err := native.CompileCachedRegex(pattern.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %v", err)
	}
	return runtime.String{Value: re.ReplaceAllString(text, repl.Value)}, nil
}

func primitiveReMatches(patternArg runtime.Value, text string) (runtime.Value, error) {
	pattern, ok := patternArg.(runtime.String)
	if !ok {
		return nil, fmt.Errorf("regex pattern must be string")
	}
	re, err := native.CompileCachedRegex(pattern.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %v", err)
	}
	return runtime.Bool{Value: re.MatchString(text)}, nil
}

func primitiveMethod(receiver runtime.Value, name string, args []runtime.Value) (runtime.Value, error) {
	switch strings.ToLower(name) {
	case "toint", "todecimal", "tofloat", "tobool":
		if name == "toInt" || strings.ToLower(name) == "toint" {
			if text, ok := receiver.(runtime.String); ok && len(args) >= 1 {
				base, err := native.IntBaseArg(args, "string.toInt")
				if err != nil {
					return nil, err
				}
				return native.StringParseBase(text.Value, base, "string.toInt")
			}
		}
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.%s expects no arguments", receiver.TypeName(), name)
		}
		target, _ := primitiveConversionTarget(name)
		return castValue(receiver, target)
	case "length":
		if len(args) != 0 {
			return nil, fmt.Errorf("length expects no arguments")
		}
		switch value := receiver.(type) {
		case runtime.String:
			return runtime.SmallInt{Value: int64(len([]rune(value.Value)))}, nil
		case runtime.Bytes:
			return runtime.SmallInt{Value: int64(len(value.Value))}, nil
		case runtime.List:
			return runtime.SmallInt{Value: int64(len(value.Elements))}, nil
		case runtime.Dict:
			return runtime.SmallInt{Value: int64(len(value.Entries))}, nil
		case runtime.Set:
			return runtime.SmallInt{Value: int64(len(value.Elements))}, nil
		case runtime.Range:
			return runtime.Int{Value: value.Length()}, nil
		default:
			return nil, fmt.Errorf("%s has no method length", receiver.TypeName())
		}
	case "isempty":
		length, err := primitiveMethod(receiver, "length", args)
		if err != nil {
			return nil, err
		}
		switch n := length.(type) {
		case runtime.SmallInt:
			return runtime.Bool{Value: n.Value == 0}, nil
		case runtime.Int:
			return runtime.Bool{Value: n.Value.Sign() == 0}, nil
		}
		return runtime.Bool{Value: false}, nil
	case "contains":
		if len(args) != 1 {
			return nil, fmt.Errorf("contains expects one argument")
		}
		switch value := receiver.(type) {
		case runtime.String:
			arg, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.contains expects string")
			}
			return runtime.Bool{Value: strings.Contains(value.Value, arg.Value)}, nil
		case runtime.List:
			for _, element := range value.Elements {
				if valuesEqual(element, args[0]) {
					return runtime.Bool{Value: true}, nil
				}
			}
			return runtime.Bool{Value: false}, nil
		case runtime.Dict:
			_, ok := value.Entries[native.DictKey(args[0])]
			return runtime.Bool{Value: ok}, nil
		case runtime.Set:
			_, ok := value.Elements[native.DictKey(args[0])]
			return runtime.Bool{Value: ok}, nil
		case runtime.Bytes:
			if needle, ok := args[0].(runtime.Bytes); ok {
				return runtime.Bool{Value: bytes.Contains(value.Value, needle.Value)}, nil
			}
			byteVal, ok := native.AsInt64(args[0])
			if !ok {
				return nil, fmt.Errorf("bytes.contains expects bytes or int byte")
			}
			if byteVal < 0 || byteVal > 255 {
				return runtime.Bool{Value: false}, nil
			}
			return runtime.Bool{Value: bytes.Contains(value.Value, []byte{byte(byteVal)})}, nil
		case runtime.Range:
			nb, ok := native.IntValueToBigInt(args[0])
			if !ok {
				return runtime.Bool{Value: false}, nil
			}
			return runtime.Bool{Value: value.ContainsInt(nb)}, nil
		default:
			return nil, fmt.Errorf("%s has no method contains", receiver.TypeName())
		}
	case "startswith":
		if len(args) != 1 {
			return nil, fmt.Errorf("startsWith expects one argument")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method startsWith", receiver.TypeName())
		}
		arg, err := singleStringArg(args, "string.startsWith")
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: strings.HasPrefix(value.Value, arg)}, nil
	case "endswith":
		if len(args) != 1 {
			return nil, fmt.Errorf("endsWith expects one argument")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method endsWith", receiver.TypeName())
		}
		arg, err := singleStringArg(args, "string.endsWith")
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: strings.HasSuffix(value.Value, arg)}, nil
	case "trim":
		if len(args) != 0 {
			return nil, fmt.Errorf("string.trim expects no arguments")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method trim", receiver.TypeName())
		}
		return runtime.String{Value: strings.TrimSpace(value.Value)}, nil
	case "trimstart":
		if len(args) != 0 {
			return nil, fmt.Errorf("string.trimStart expects no arguments")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method trimStart", receiver.TypeName())
		}
		return runtime.String{Value: strings.TrimLeftFunc(value.Value, unicode.IsSpace)}, nil
	case "trimend":
		if len(args) != 0 {
			return nil, fmt.Errorf("string.trimEnd expects no arguments")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method trimEnd", receiver.TypeName())
		}
		return runtime.String{Value: strings.TrimRightFunc(value.Value, unicode.IsSpace)}, nil
	case "repeat":
		if len(args) != 1 {
			return nil, fmt.Errorf("string.repeat expects one argument")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method repeat", receiver.TypeName())
		}
		n, err := indexInt(args[0])
		if err != nil {
			return nil, fmt.Errorf("string.repeat: %v", err)
		}
		if n < 0 {
			n = 0
		}
		return runtime.String{Value: strings.Repeat(value.Value, n)}, nil
	case "padstart":
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("string.padStart expects (length[, pad])")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method padStart", receiver.TypeName())
		}
		targetLen, err := indexInt(args[0])
		if err != nil {
			return nil, fmt.Errorf("string.padStart: %v", err)
		}
		pad := " "
		if len(args) == 2 {
			padStr, ok := args[1].(runtime.String)
			if !ok || len(padStr.Value) == 0 {
				return nil, fmt.Errorf("string.padStart: pad must be a non-empty string")
			}
			pad = padStr.Value
		}
		runes := []rune(value.Value)
		for len(runes) < targetLen {
			padRunes := []rune(pad)
			needed := targetLen - len(runes)
			if needed < len(padRunes) {
				runes = append([]rune(pad[:needed]), runes...)
			} else {
				runes = append(padRunes, runes...)
			}
		}
		if len(runes) > targetLen {
			runes = runes[len(runes)-targetLen:]
		}
		return runtime.String{Value: string(runes)}, nil
	case "padend":
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("string.padEnd expects (length[, pad])")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method padEnd", receiver.TypeName())
		}
		targetLen, err := indexInt(args[0])
		if err != nil {
			return nil, fmt.Errorf("string.padEnd: %v", err)
		}
		pad := " "
		if len(args) == 2 {
			padStr, ok := args[1].(runtime.String)
			if !ok || len(padStr.Value) == 0 {
				return nil, fmt.Errorf("string.padEnd: pad must be a non-empty string")
			}
			pad = padStr.Value
		}
		runes := []rune(value.Value)
		padRunes := []rune(pad)
		for len(runes) < targetLen {
			needed := targetLen - len(runes)
			if needed < len(padRunes) {
				runes = append(runes, padRunes[:needed]...)
			} else {
				runes = append(runes, padRunes...)
			}
		}
		if len(runes) > targetLen {
			runes = runes[:targetLen]
		}
		return runtime.String{Value: string(runes)}, nil
	case "chars":
		if len(args) != 0 {
			return nil, fmt.Errorf("string.chars expects no arguments")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method chars", receiver.TypeName())
		}
		runes := []rune(value.Value)
		elements := make([]runtime.Value, len(runes))
		for i, r := range runes {
			elements[i] = runtime.String{Value: string(r)}
		}
		return runtime.List{Elements: elements}, nil
	case "codepointat":
		if len(args) != 1 {
			return nil, fmt.Errorf("string.codePointAt expects one argument")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method codePointAt", receiver.TypeName())
		}
		i, err := indexInt(args[0])
		if err != nil {
			return nil, fmt.Errorf("string.codePointAt: %v", err)
		}
		runes := []rune(value.Value)
		if i < 0 {
			i = len(runes) + i
		}
		if i < 0 || i >= len(runes) {
			return runtime.Null{}, nil
		}
		return runtime.NewInt64(int64(runes[i])), nil
	case "format":
		switch value := receiver.(type) {
		case runtime.String:
			formatted, err := formatString(value.Value, args)
			if err != nil {
				return nil, err
			}
			return runtime.String{Value: formatted}, nil
		case runtime.Decimal:
			if len(args) != 1 {
				return nil, fmt.Errorf("decimal.format expects scale")
			}
			scale, err := decimalFormatScale(args[0])
			if err != nil {
				return nil, err
			}
			return runtime.String{Value: value.Value.FloatString(scale)}, nil
		default:
			return nil, fmt.Errorf("%s has no method format", receiver.TypeName())
		}
	case "lower":
		if len(args) != 0 {
			return nil, fmt.Errorf("string.lower expects no arguments")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method lower", receiver.TypeName())
		}
		return runtime.String{Value: strings.ToLower(value.Value)}, nil
	case "upper":
		if len(args) != 0 {
			return nil, fmt.Errorf("string.upper expects no arguments")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method upper", receiver.TypeName())
		}
		return runtime.String{Value: strings.ToUpper(value.Value)}, nil
	case "replace":
		if len(args) != 2 && len(args) != 3 {
			return nil, fmt.Errorf("string.replace expects old, new, and optional count")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method replace", receiver.TypeName())
		}
		oldValue, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("string.replace old value must be string")
		}
		newValue, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("string.replace new value must be string")
		}
		count := -1
		if len(args) == 3 {
			var err error
			count, err = indexInt(args[2])
			if err != nil {
				return nil, err
			}
		}
		return runtime.String{Value: strings.Replace(value.Value, oldValue.Value, newValue.Value, count)}, nil
	case "splitregex":
		if len(args) != 1 {
			return nil, fmt.Errorf("string.splitRegex expects one argument")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method splitRegex", receiver.TypeName())
		}
		return primitiveReSplit(args[0], value.Value)
	case "replaceregex":
		if len(args) != 2 {
			return nil, fmt.Errorf("string.replaceRegex expects (pattern, replacement)")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method replaceRegex", receiver.TypeName())
		}
		return primitiveReReplace(args[0], value.Value, args[1])
	case "matchesregex":
		if len(args) != 1 {
			return nil, fmt.Errorf("string.matchesRegex expects one argument")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method matchesRegex", receiver.TypeName())
		}
		return primitiveReMatches(args[0], value.Value)
	case "split":
		if len(args) != 1 {
			return nil, fmt.Errorf("string.split expects one argument")
		}
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method split", receiver.TypeName())
		}
		sep, err := singleStringArg(args, "string.split")
		if err != nil {
			return nil, err
		}
		parts := strings.Split(value.Value, sep)
		out := make([]runtime.Value, 0, len(parts))
		for _, part := range parts {
			out = append(out, runtime.String{Value: part})
		}
		return runtime.List{Elements: out}, nil
	case "indexof":
		if len(args) != 1 {
			return nil, fmt.Errorf("indexOf expects one argument")
		}
		switch value := receiver.(type) {
		case runtime.String:
			needle, err := singleStringArg(args, "string.indexOf")
			if err != nil {
				return nil, err
			}
			byteIndex := strings.Index(value.Value, needle)
			if byteIndex < 0 {
				return runtime.NewInt64(-1), nil
			}
			return runtime.SmallInt{Value: int64(len([]rune(value.Value[:byteIndex])))}, nil
		case runtime.List:
			for i, element := range value.Elements {
				if valuesEqual(element, args[0]) {
					return runtime.NewInt64(int64(i)), nil
				}
			}
			return runtime.NewInt64(-1), nil
		default:
			return nil, fmt.Errorf("%s has no method indexOf", receiver.TypeName())
		}
	case "substring", "slice":
		switch value := receiver.(type) {
		case runtime.String:
			if len(args) < 1 || len(args) > 2 {
				return nil, fmt.Errorf("string.%s expects (start[, end])", name)
			}
			runes := []rune(value.Value)
			n := len(runes)
			start, err := indexInt(args[0])
			if err != nil {
				return nil, fmt.Errorf("string.%s: %v", name, err)
			}
			if start < 0 {
				start = n + start
			}
			if start < 0 {
				start = 0
			}
			if start > n {
				start = n
			}
			end := n
			if len(args) == 2 {
				end, err = indexInt(args[1])
				if err != nil {
					return nil, fmt.Errorf("string.%s: %v", name, err)
				}
				if end < 0 {
					end = n + end
				}
				if end < 0 {
					end = 0
				}
				if end > n {
					end = n
				}
			}
			if start >= end {
				return runtime.String{Value: ""}, nil
			}
			return runtime.String{Value: string(runes[start:end])}, nil
		case runtime.List:
			if len(args) < 1 || len(args) > 2 {
				return nil, fmt.Errorf("list.slice expects (start[, end])")
			}
			n := len(value.Elements)
			start, err := indexInt(args[0])
			if err != nil {
				return nil, fmt.Errorf("list.slice: %v", err)
			}
			if start < 0 {
				start = n + start
			}
			if start < 0 {
				start = 0
			}
			if start > n {
				start = n
			}
			end := n
			if len(args) == 2 {
				end, err = indexInt(args[1])
				if err != nil {
					return nil, fmt.Errorf("list.slice: %v", err)
				}
				if end < 0 {
					end = n + end
				}
				if end < 0 {
					end = 0
				}
				if end > n {
					end = n
				}
			}
			if start >= end {
				return runtime.List{Elements: nil}, nil
			}
			return runtime.List{Elements: value.Elements[start:end]}, nil
		default:
			return nil, fmt.Errorf("%s has no method %s", receiver.TypeName(), name)
		}
	case "lastindexof":
		value, ok := receiver.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s has no method lastIndexOf", receiver.TypeName())
		}
		if len(args) != 1 {
			return nil, fmt.Errorf("string.lastIndexOf expects one argument")
		}
		needle, ok2 := args[0].(runtime.String)
		if !ok2 {
			return nil, fmt.Errorf("string.lastIndexOf expects string")
		}
		byteIndex := strings.LastIndex(value.Value, needle.Value)
		if byteIndex < 0 {
			return runtime.NewInt64(-1), nil
		}
		return runtime.SmallInt{Value: int64(len([]rune(value.Value[:byteIndex])))}, nil
	case "reverse":
		switch value := receiver.(type) {
		case runtime.String:
			if len(args) != 0 {
				return nil, fmt.Errorf("string.reverse expects no arguments")
			}
			runes := []rune(value.Value)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return runtime.String{Value: string(runes)}, nil
		default:
			return nil, fmt.Errorf("%s has no method reverse", receiver.TypeName())
		}
	case "count":
		switch value := receiver.(type) {
		case runtime.String:
			if len(args) != 1 {
				return nil, fmt.Errorf("string.count expects one argument")
			}
			needle, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.count expects string")
			}
			return runtime.NewInt64(int64(strings.Count(value.Value, needle.Value))), nil
		default:
			return nil, fmt.Errorf("%s has no method count", receiver.TypeName())
		}
	case "get":
		if len(args) != 1 {
			return nil, fmt.Errorf("get expects one argument")
		}
		switch value := receiver.(type) {
		case runtime.List:
			i, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			if i < 0 {
				i = len(value.Elements) + i
			}
			if i < 0 || i >= len(value.Elements) {
				return runtime.Null{}, nil
			}
			return value.Elements[i], nil
		case runtime.Dict:
			entry, ok := value.Entries[dictKeyFor(args[0])]
			if !ok {
				return runtime.Null{}, nil
			}
			return entry.Value, nil
		case runtime.String:
			i, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			runes := []rune(value.Value)
			if i < 0 {
				i = len(runes) + i
			}
			if i < 0 || i >= len(runes) {
				return runtime.Null{}, nil
			}
			return runtime.String{Value: string(runes[i])}, nil
		case runtime.Bytes:
			i, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			if i < 0 {
				i = len(value.Value) + i
			}
			if i < 0 || i >= len(value.Value) {
				return runtime.Null{}, nil
			}
			return runtime.NewInt64(int64(value.Value[i])), nil
		default:
			return nil, fmt.Errorf("%s has no method get", receiver.TypeName())
		}
	case "copy":
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.copy expects no arguments", receiver.TypeName())
		}
		switch value := receiver.(type) {
		case runtime.List:
			elems := make([]runtime.Value, len(value.Elements))
			copy(elems, value.Elements)
			return runtime.List{Elements: elems}, nil
		case runtime.Dict:
			entries := make(map[string]runtime.DictEntry, len(value.Entries))
			for k, v := range value.Entries {
				entries[k] = v
			}
			return runtime.Dict{Entries: entries}, nil
		case runtime.Set:
			elements := make(map[string]runtime.SetEntry, len(value.Elements))
			for k, v := range value.Elements {
				elements[k] = v
			}
			return runtime.Set{Elements: elements}, nil
		default:
			return nil, fmt.Errorf("%s has no method copy", receiver.TypeName())
		}
	case "set":
		if len(args) != 2 {
			return nil, fmt.Errorf("set expects two arguments")
		}
		switch value := receiver.(type) {
		case runtime.List:
			if value.Frozen {
				return nil, vmTypedError{class: "ImmutableError", message: "cannot modify frozen list"}
			}
			i, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			if i < 0 {
				i = len(value.Elements) + i
			}
			if i < 0 || i >= len(value.Elements) {
				return nil, fmt.Errorf("list index out of range")
			}
			value.Elements[i] = args[1]
			return runtime.Null{}, nil
		case runtime.Dict:
			if value.Frozen {
				return nil, vmTypedError{class: "ImmutableError", message: "cannot modify frozen dict"}
			}
			value.Entries[native.DictKey(args[0])] = runtime.DictEntry{Key: args[0], Value: args[1]}
			return runtime.Null{}, nil
		default:
			return nil, fmt.Errorf("%s has no method set", receiver.TypeName())
		}
	case "delete":
		if len(args) != 1 {
			return nil, fmt.Errorf("dict.delete expects one argument")
		}
		value, ok := receiver.(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s has no method delete", receiver.TypeName())
		}
		if value.Frozen {
			return nil, vmTypedError{class: "ImmutableError", message: "cannot modify frozen dict"}
		}
		delete(value.Entries, native.DictKey(args[0]))
		return runtime.Null{}, nil
	case "add":
		if len(args) != 1 {
			return nil, fmt.Errorf("set.add expects one argument")
		}
		value, ok := receiver.(runtime.Set)
		if !ok {
			return nil, fmt.Errorf("%s has no method add", receiver.TypeName())
		}
		elements := cloneSetEntries(value.Elements)
		elements[native.DictKey(args[0])] = runtime.SetEntry{Value: args[0]}
		return runtime.Set{Elements: elements}, nil
	case "remove":
		if len(args) != 1 {
			return nil, fmt.Errorf("set.remove expects one argument")
		}
		value, ok := receiver.(runtime.Set)
		if !ok {
			return nil, fmt.Errorf("%s has no method remove", receiver.TypeName())
		}
		elements := cloneSetEntries(value.Elements)
		delete(elements, native.DictKey(args[0]))
		return runtime.Set{Elements: elements}, nil
	case "tolist":
		if len(args) != 0 {
			return nil, fmt.Errorf("toList expects no arguments")
		}
		switch value := receiver.(type) {
		case runtime.List:
			return value, nil
		case runtime.Set:
			return runtime.List{Elements: orderedSetValues(value)}, nil
		case runtime.Range:
			var elements []runtime.Value
			current := new(big.Int).Set(value.Start)
			step := value.Step
			for rangeContains(current, value.End, step, value.Exclusive) {
				elements = append(elements, runtime.Int{Value: new(big.Int).Set(current)})
				current.Add(current, step)
			}
			return runtime.List{Elements: elements}, nil
		default:
			return nil, fmt.Errorf("%s has no method toList", receiver.TypeName())
		}
	case "union":
		if len(args) != 1 {
			return nil, fmt.Errorf("set.union expects one argument")
		}
		value, ok := receiver.(runtime.Set)
		if !ok {
			return nil, fmt.Errorf("%s has no method union", receiver.TypeName())
		}
		other, ok := args[0].(runtime.Set)
		if !ok {
			return nil, fmt.Errorf("set.union expects set")
		}
		elements := cloneSetEntries(value.Elements)
		for key, entry := range other.Elements {
			elements[key] = entry
		}
		return runtime.Set{Elements: elements}, nil
	case "intersection":
		if len(args) != 1 {
			return nil, fmt.Errorf("set.intersection expects one argument")
		}
		value, ok := receiver.(runtime.Set)
		if !ok {
			return nil, fmt.Errorf("%s has no method intersection", receiver.TypeName())
		}
		other, ok := args[0].(runtime.Set)
		if !ok {
			return nil, fmt.Errorf("set.intersection expects set")
		}
		elements := map[string]runtime.SetEntry{}
		for key, entry := range value.Elements {
			if _, exists := other.Elements[key]; exists {
				elements[key] = entry
			}
		}
		return runtime.Set{Elements: elements}, nil
	case "difference":
		if len(args) != 1 {
			return nil, fmt.Errorf("set.difference expects one argument")
		}
		value, ok := receiver.(runtime.Set)
		if !ok {
			return nil, fmt.Errorf("%s has no method difference", receiver.TypeName())
		}
		other, ok := args[0].(runtime.Set)
		if !ok {
			return nil, fmt.Errorf("set.difference expects set")
		}
		elements := map[string]runtime.SetEntry{}
		for key, entry := range value.Elements {
			if _, exists := other.Elements[key]; !exists {
				elements[key] = entry
			}
		}
		return runtime.Set{Elements: elements}, nil
	case "haskey":
		if len(args) != 1 {
			return nil, fmt.Errorf("dict.hasKey expects one argument")
		}
		value, ok := receiver.(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s has no method hasKey", receiver.TypeName())
		}
		_, exists := value.Entries[native.DictKey(args[0])]
		return runtime.Bool{Value: exists}, nil
	case "keys":
		if len(args) != 0 {
			return nil, fmt.Errorf("dict.keys expects no arguments")
		}
		value, ok := receiver.(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s has no method keys", receiver.TypeName())
		}
		keys := make([]runtime.Value, 0, len(value.Entries))
		for _, entry := range value.Entries {
			keys = append(keys, entry.Key)
		}
		return runtime.List{Elements: keys}, nil
	case "values":
		if len(args) != 0 {
			return nil, fmt.Errorf("dict.values expects no arguments")
		}
		value, ok := receiver.(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s has no method values", receiver.TypeName())
		}
		values := make([]runtime.Value, 0, len(value.Entries))
		for _, entry := range value.Entries {
			values = append(values, entry.Value)
		}
		return runtime.List{Elements: values}, nil
	case "items":
		if len(args) != 0 {
			return nil, fmt.Errorf("dict.items expects no arguments")
		}
		value, ok := receiver.(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s has no method items", receiver.TypeName())
		}
		items := make([]runtime.Value, 0, len(value.Entries))
		for _, entry := range value.Entries {
			items = append(items, runtime.List{Elements: []runtime.Value{entry.Key, entry.Value}})
		}
		return runtime.List{Elements: items}, nil
	case "first":
		if len(args) != 0 {
			return nil, fmt.Errorf("first expects no arguments")
		}
		switch value := receiver.(type) {
		case runtime.List:
			if len(value.Elements) == 0 {
				return runtime.Null{}, nil
			}
			return value.Elements[0], nil
		case runtime.Range:
			if value.Length().Sign() == 0 {
				return runtime.Null{}, nil
			}
			return runtime.Int{Value: new(big.Int).Set(value.Start)}, nil
		default:
			return nil, fmt.Errorf("%s has no method first", receiver.TypeName())
		}
	case "last":
		if len(args) != 0 {
			return nil, fmt.Errorf("last expects no arguments")
		}
		switch value := receiver.(type) {
		case runtime.List:
			if len(value.Elements) == 0 {
				return runtime.Null{}, nil
			}
			return value.Elements[len(value.Elements)-1], nil
		case runtime.Range:
			n := value.Length()
			if n.Sign() == 0 {
				return runtime.Null{}, nil
			}
			last := new(big.Int).Mul(value.Step, new(big.Int).Sub(n, big.NewInt(1)))
			last.Add(last, value.Start)
			return runtime.Int{Value: last}, nil
		default:
			return nil, fmt.Errorf("%s has no method last", receiver.TypeName())
		}
	case "push":
		if len(args) != 1 {
			return nil, fmt.Errorf("list.push expects one argument")
		}
		list, ok := receiver.(runtime.List)
		if !ok {
			return nil, fmt.Errorf("%s has no method push", receiver.TypeName())
		}
		newElements := make([]runtime.Value, len(list.Elements)+1)
		copy(newElements, list.Elements)
		newElements[len(list.Elements)] = args[0]
		return runtime.List{Elements: newElements}, nil
	case "pop":
		if len(args) != 0 {
			return nil, fmt.Errorf("list.pop expects no arguments")
		}
		list, ok := receiver.(runtime.List)
		if !ok {
			return nil, fmt.Errorf("%s has no method pop", receiver.TypeName())
		}
		if len(list.Elements) == 0 {
			return runtime.List{Elements: []runtime.Value{}}, nil
		}
		newElements := make([]runtime.Value, len(list.Elements)-1)
		copy(newElements, list.Elements)
		return runtime.List{Elements: newElements}, nil
	case "insert":
		if len(args) != 2 {
			return nil, fmt.Errorf("list.insert expects two arguments (index, value)")
		}
		list, ok := receiver.(runtime.List)
		if !ok {
			return nil, fmt.Errorf("%s has no method insert", receiver.TypeName())
		}
		i, err := indexInt(args[0])
		if err != nil {
			return nil, fmt.Errorf("list.insert: %v", err)
		}
		if i < 0 {
			i = len(list.Elements) + i
		}
		if i < 0 {
			i = 0
		}
		if i > len(list.Elements) {
			i = len(list.Elements)
		}
		newElements := make([]runtime.Value, len(list.Elements)+1)
		copy(newElements, list.Elements[:i])
		newElements[i] = args[1]
		copy(newElements[i+1:], list.Elements[i:])
		return runtime.List{Elements: newElements}, nil
	case "removeat":
		if len(args) != 1 {
			return nil, fmt.Errorf("list.removeAt expects one argument")
		}
		list, ok := receiver.(runtime.List)
		if !ok {
			return nil, fmt.Errorf("%s has no method removeAt", receiver.TypeName())
		}
		i, err := indexInt(args[0])
		if err != nil {
			return nil, fmt.Errorf("list.removeAt: %v", err)
		}
		if i < 0 {
			i = len(list.Elements) + i
		}
		if i < 0 || i >= len(list.Elements) {
			return nil, fmt.Errorf("list.removeAt: index out of range")
		}
		newElements := make([]runtime.Value, len(list.Elements)-1)
		copy(newElements, list.Elements[:i])
		copy(newElements[i:], list.Elements[i+1:])
		return runtime.List{Elements: newElements}, nil
	case "concat":
		if len(args) != 1 {
			return nil, fmt.Errorf("list.concat expects one argument")
		}
		list, ok := receiver.(runtime.List)
		if !ok {
			return nil, fmt.Errorf("%s has no method concat", receiver.TypeName())
		}
		other, ok := args[0].(runtime.List)
		if !ok {
			return nil, fmt.Errorf("list.concat expects a list argument")
		}
		newElements := make([]runtime.Value, len(list.Elements)+len(other.Elements))
		copy(newElements, list.Elements)
		copy(newElements[len(list.Elements):], other.Elements)
		return runtime.List{Elements: newElements}, nil
	case "join":
		list, ok := receiver.(runtime.List)
		if !ok {
			return nil, fmt.Errorf("%s has no method join", receiver.TypeName())
		}
		if len(args) != 1 {
			return nil, fmt.Errorf("list.join expects one argument (separator)")
		}
		sep, ok2 := args[0].(runtime.String)
		if !ok2 {
			return nil, fmt.Errorf("list.join separator must be a string")
		}
		parts := make([]string, len(list.Elements))
		for i, el := range list.Elements {
			parts[i] = el.Inspect()
		}
		return runtime.String{Value: strings.Join(parts, sep.Value)}, nil
	case "reversed":
		list, ok := receiver.(runtime.List)
		if !ok {
			return nil, fmt.Errorf("%s has no method reversed", receiver.TypeName())
		}
		if len(args) != 0 {
			return nil, fmt.Errorf("list.reversed expects no arguments")
		}
		newElements := make([]runtime.Value, len(list.Elements))
		for i, el := range list.Elements {
			newElements[len(list.Elements)-1-i] = el
		}
		return runtime.List{Elements: newElements}, nil
	case "merge":
		dict, ok := receiver.(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s has no method merge", receiver.TypeName())
		}
		if len(args) != 1 {
			return nil, fmt.Errorf("dict.merge expects one argument")
		}
		other, ok2 := args[0].(runtime.Dict)
		if !ok2 {
			return nil, fmt.Errorf("dict.merge expects a dict argument")
		}
		merged := runtime.Dict{Entries: make(map[string]runtime.DictEntry, len(dict.Entries)+len(other.Entries))}
		for k, e := range dict.Entries {
			merged.Entries[k] = e
		}
		for k, e := range other.Entries {
			merged.Entries[k] = e
		}
		return merged, nil
	case "abs":
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.abs expects no arguments", receiver.TypeName())
		}
		return native.NumericAbs(receiver)
	case "iszero":
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.isZero expects no arguments", receiver.TypeName())
		}
		return numericSignCheck(receiver, func(sign int) bool { return sign == 0 })
	case "ispositive":
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.isPositive expects no arguments", receiver.TypeName())
		}
		return numericSignCheck(receiver, func(sign int) bool { return sign > 0 })
	case "isnegative":
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.isNegative expects no arguments", receiver.TypeName())
		}
		return numericSignCheck(receiver, func(sign int) bool { return sign < 0 })
	case "isnan":
		if len(args) != 0 {
			return nil, fmt.Errorf("float.isNaN expects no arguments")
		}
		value, ok := receiver.(runtime.Float)
		if !ok {
			return nil, fmt.Errorf("%s has no method isNaN", receiver.TypeName())
		}
		return runtime.Bool{Value: math.IsNaN(value.Value)}, nil
	case "isinf":
		if len(args) != 0 {
			return nil, fmt.Errorf("float.isInf expects no arguments")
		}
		value, ok := receiver.(runtime.Float)
		if !ok {
			return nil, fmt.Errorf("%s has no method isInf", receiver.TypeName())
		}
		return runtime.Bool{Value: math.IsInf(value.Value, 0)}, nil
	case "not":
		if len(args) != 0 {
			return nil, fmt.Errorf("bool.not expects no arguments")
		}
		value, ok := receiver.(runtime.Bool)
		if !ok {
			return nil, fmt.Errorf("%s has no method not", receiver.TypeName())
		}
		return runtime.Bool{Value: !value.Value}, nil
	case "tostring":
		if value, ok := receiver.(runtime.Decimal); ok {
			if len(args) > 1 {
				return nil, fmt.Errorf("decimal.toString expects optional scale")
			}
			scale := 10
			if len(args) == 1 {
				var err error
				scale, err = decimalFormatScale(args[0])
				if err != nil {
					return nil, err
				}
			}
			return runtime.String{Value: value.Value.FloatString(scale)}, nil
		}
		switch receiver.(type) {
		case runtime.SmallInt, runtime.Int:
			base, err := native.IntBaseArg(args, "int.toString")
			if err != nil {
				return nil, err
			}
			if base == 10 {
				return runtime.String{Value: receiver.Inspect()}, nil
			}
			s, err := native.IntFormatBase(receiver, base)
			if err != nil {
				return nil, err
			}
			return runtime.String{Value: s}, nil
		}
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.toString expects no arguments", receiver.TypeName())
		}
		if value, ok := receiver.(runtime.Bytes); ok {
			return runtime.String{Value: string(value.Value)}, nil
		}
		return runtime.String{Value: receiver.Inspect()}, nil
	case "tohex":
		if len(args) != 0 {
			return nil, fmt.Errorf("bytes.toHex expects no arguments")
		}
		value, ok := receiver.(runtime.Bytes)
		if !ok {
			return nil, fmt.Errorf("%s has no method toHex", receiver.TypeName())
		}
		return runtime.String{Value: hex.EncodeToString(value.Value)}, nil
	case "tobase64":
		if len(args) != 0 {
			return nil, fmt.Errorf("bytes.toBase64 expects no arguments")
		}
		value, ok := receiver.(runtime.Bytes)
		if !ok {
			return nil, fmt.Errorf("%s has no method toBase64", receiver.TypeName())
		}
		return runtime.String{Value: base64.StdEncoding.EncodeToString(value.Value)}, nil
	case "tobase64url":
		if len(args) != 0 {
			return nil, fmt.Errorf("bytes.toBase64Url expects no arguments")
		}
		value, ok := receiver.(runtime.Bytes)
		if !ok {
			return nil, fmt.Errorf("%s has no method toBase64Url", receiver.TypeName())
		}
		return runtime.String{Value: base64.RawURLEncoding.EncodeToString(value.Value)}, nil
	default:
		return nil, fmt.Errorf("%s has no method %s", receiver.TypeName(), name)
	}
}

func singleStringArg(args []runtime.Value, label string) (string, error) {
	value, ok := args[0].(runtime.String)
	if !ok {
		return "", fmt.Errorf("%s expects string", label)
	}
	return value.Value, nil
}

func formatArgs(args []runtime.Value) []any {
	out := make([]any, len(args))
	for i, arg := range args {
		out[i] = formatArg(arg)
	}
	return out
}

func formatString(format string, args []runtime.Value) (string, error) {
	formatted := fmt.Sprintf(format, formatArgs(args)...)
	if strings.Contains(formatted, "%!") {
		return "", fmt.Errorf("invalid string.format arguments for %q", format)
	}
	return formatted, nil
}

func decimalFormatScale(value runtime.Value) (int, error) {
	scale, err := indexInt(value)
	if err != nil {
		return 0, fmt.Errorf("decimal scale must be int")
	}
	if scale < 0 || scale > 10000 {
		return 0, fmt.Errorf("decimal scale must be between 0 and 10000")
	}
	return scale, nil
}

func formatArg(value runtime.Value) any {
	switch value := value.(type) {
	case runtime.Null:
		return nil
	case runtime.Bool:
		return value.Value
	case runtime.SmallInt:
		return value.Value
	case runtime.Int:
		if value.Value.IsInt64() {
			return value.Value.Int64()
		}
		return value.Value.String()
	case runtime.Decimal:
		f, _ := value.Value.Float64()
		return f
	case runtime.Float:
		return value.Value
	case runtime.String:
		return value.Value
	case runtime.Bytes:
		return value.Value
	default:
		return value.Inspect()
	}
}

func numericSignCheck(value runtime.Value, check func(int) bool) (runtime.Value, error) {
	switch value := value.(type) {
	case runtime.SmallInt:
		sign := 0
		if value.Value > 0 {
			sign = 1
		} else if value.Value < 0 {
			sign = -1
		}
		return runtime.Bool{Value: check(sign)}, nil
	case runtime.Int:
		return runtime.Bool{Value: check(value.Value.Sign())}, nil
	case runtime.Decimal:
		return runtime.Bool{Value: check(value.Value.Sign())}, nil
	case runtime.Float:
		sign := 0
		if value.Value > 0 {
			sign = 1
		} else if value.Value < 0 {
			sign = -1
		}
		return runtime.Bool{Value: check(sign)}, nil
	default:
		return nil, fmt.Errorf("%s has no numeric sign methods", value.TypeName())
	}
}

func valuesEqual(left runtime.Value, right runtime.Value) bool {
	switch leftValue := left.(type) {
	case runtime.List:
		rightValue, ok := right.(runtime.List)
		if !ok || len(leftValue.Elements) != len(rightValue.Elements) {
			return false
		}
		for i, element := range leftValue.Elements {
			if !valuesEqual(element, rightValue.Elements[i]) {
				return false
			}
		}
		return true
	case runtime.Dict:
		rightValue, ok := right.(runtime.Dict)
		if !ok || len(leftValue.Entries) != len(rightValue.Entries) {
			return false
		}
		for key, entry := range leftValue.Entries {
			other, ok := rightValue.Entries[key]
			if !ok || !valuesEqual(entry.Key, other.Key) || !valuesEqual(entry.Value, other.Value) {
				return false
			}
		}
		return true
	case runtime.Set:
		rightValue, ok := right.(runtime.Set)
		if !ok || len(leftValue.Elements) != len(rightValue.Elements) {
			return false
		}
		for key, entry := range leftValue.Elements {
			other, ok := rightValue.Elements[key]
			if !ok || !valuesEqual(entry.Value, other.Value) {
				return false
			}
		}
		return true
	case runtime.EnumVariant:
		rv, ok := right.(runtime.EnumVariant)
		if !ok || leftValue.Enum != rv.Enum || leftValue.Variant != rv.Variant || len(leftValue.Fields) != len(rv.Fields) {
			return false
		}
		for i, f := range leftValue.Fields {
			if !valuesEqual(f, rv.Fields[i]) {
				return false
			}
		}
		return true
	case *runtime.Instance:
		rightValue, ok := right.(*runtime.Instance)
		if !ok || !strings.EqualFold(leftValue.Class.Name, rightValue.Class.Name) || len(leftValue.Fields) != len(rightValue.Fields) {
			return false
		}
		for name, value := range leftValue.Fields {
			other, ok := rightValue.Fields[name]
			if !ok || !valuesEqual(value, other) {
				return false
			}
		}
		return true
	}
	return primitiveEqual(left, right)
}

func cloneSetEntries(elements map[string]runtime.SetEntry) map[string]runtime.SetEntry {
	out := make(map[string]runtime.SetEntry, len(elements))
	for key, entry := range elements {
		out[key] = entry
	}
	return out
}

func orderedSetValues(value runtime.Set) []runtime.Value {
	keys := make([]string, 0, len(value.Elements))
	for key := range value.Elements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]runtime.Value, 0, len(keys))
	for _, key := range keys {
		values = append(values, value.Elements[key].Value)
	}
	return values
}

func vmHTTPHeadersMethod(receiver runtime.HTTPHeaders, name string, args []runtime.Value) (runtime.Value, error) {
	headers := vmCopyHTTPHeaders(receiver)
	switch name {
	case "get":
		key, err := vmSingleHeaderName(name, args)
		if err != nil {
			return nil, err
		}
		values := headers.Values[key]
		if len(values) == 0 {
			return runtime.Null{}, nil
		}
		return runtime.String{Value: values[0]}, nil
	case "getAll":
		key, err := vmSingleHeaderName(name, args)
		if err != nil {
			return nil, err
		}
		values := headers.Values[key]
		elements := make([]runtime.Value, len(values))
		for i, value := range values {
			elements[i] = runtime.String{Value: value}
		}
		return runtime.List{Elements: elements}, nil
	case "has":
		key, err := vmSingleHeaderName(name, args)
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: len(headers.Values[key]) > 0}, nil
	case "set":
		key, value, err := vmHeaderNameValue(name, args)
		if err != nil {
			return nil, err
		}
		headers.Values[key] = []string{value}
		return headers, nil
	case "add":
		key, value, err := vmHeaderNameValue(name, args)
		if err != nil {
			return nil, err
		}
		headers.Values[key] = append(headers.Values[key], value)
		return headers, nil
	case "delete":
		key, err := vmSingleHeaderName(name, args)
		if err != nil {
			return nil, err
		}
		delete(headers.Values, key)
		return headers, nil
	case "keys":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Headers.keys expects no arguments")
		}
		keys := make([]string, 0, len(headers.Values))
		for key := range headers.Values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		elements := make([]runtime.Value, len(keys))
		for i, key := range keys {
			elements[i] = runtime.String{Value: key}
		}
		return runtime.List{Elements: elements}, nil
	case "toDict":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Headers.toDict expects no arguments")
		}
		return vmHTTPHeadersToDict(headers), nil
	default:
		return nil, fmt.Errorf("http.Headers has no method %s", name)
	}
}

func vmCopyHTTPHeaders(headers runtime.HTTPHeaders) runtime.HTTPHeaders {
	out := runtime.HTTPHeaders{Values: map[string][]string{}}
	for key, values := range headers.Values {
		out.Values[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	return out
}

func vmHTTPHeadersToDict(headers runtime.HTTPHeaders) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	for key, values := range headers.Values {
		keyValue := runtime.String{Value: http.CanonicalHeaderKey(key)}
		var value runtime.Value
		if len(values) == 1 {
			value = runtime.String{Value: values[0]}
		} else {
			elements := make([]runtime.Value, len(values))
			for i, item := range values {
				elements[i] = runtime.String{Value: item}
			}
			value = runtime.List{Elements: elements}
		}
		entries[native.DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: value}
	}
	return runtime.Dict{Entries: entries}
}

func vmSingleHeaderName(method string, args []runtime.Value) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("http.Headers.%s expects name", method)
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return "", fmt.Errorf("http.Headers.%s name must be string", method)
	}
	return http.CanonicalHeaderKey(name.Value), nil
}

func vmHeaderNameValue(method string, args []runtime.Value) (string, string, error) {
	if len(args) != 2 {
		return "", "", fmt.Errorf("http.Headers.%s expects name and value", method)
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return "", "", fmt.Errorf("http.Headers.%s name must be string", method)
	}
	value, ok := args[1].(runtime.String)
	if !ok {
		return "", "", fmt.Errorf("http.Headers.%s value must be string", method)
	}
	return http.CanonicalHeaderKey(name.Value), value.Value, nil
}

func valuesIdentical(left runtime.Value, right runtime.Value) bool {
	switch leftValue := left.(type) {
	case *runtime.Instance:
		rightValue, ok := right.(*runtime.Instance)
		return ok && leftValue == rightValue
	case *runtime.Class:
		rightValue, ok := right.(*runtime.Class)
		return ok && leftValue == rightValue
	case runtime.Null:
		_, ok := right.(runtime.Null)
		return ok
	default:
		return primitiveEqual(left, right)
	}
}

func primitiveEqual(left runtime.Value, right runtime.Value) bool {
	switch leftValue := left.(type) {
	case runtime.Null:
		_, ok := right.(runtime.Null)
		return ok
	case runtime.Bool:
		rightValue, ok := right.(runtime.Bool)
		return ok && leftValue.Value == rightValue.Value
	case runtime.SmallInt:
		switch rv := right.(type) {
		case runtime.SmallInt:
			return leftValue.Value == rv.Value
		case runtime.Int:
			return rv.Value.IsInt64() && rv.Value.Int64() == leftValue.Value
		}
		return false
	case runtime.Int:
		switch rv := right.(type) {
		case runtime.SmallInt:
			return leftValue.Value.IsInt64() && leftValue.Value.Int64() == rv.Value
		case runtime.Int:
			return leftValue.Value.Cmp(rv.Value) == 0
		}
		return false
	case runtime.Decimal:
		rightValue, ok := right.(runtime.Decimal)
		return ok && leftValue.Value.Cmp(rightValue.Value) == 0
	case runtime.Float:
		rightValue, ok := right.(runtime.Float)
		return ok && leftValue.Value == rightValue.Value
	case runtime.String:
		rightValue, ok := right.(runtime.String)
		return ok && leftValue.Value == rightValue.Value
	case runtime.Bytes:
		rightValue, ok := right.(runtime.Bytes)
		return ok && bytes.Equal(leftValue.Value, rightValue.Value)
	case runtime.DateTimeInstant:
		rightValue, ok := right.(runtime.DateTimeInstant)
		return ok && leftValue == rightValue
	case runtime.DateTimeDuration:
		rightValue, ok := right.(runtime.DateTimeDuration)
		return ok && leftValue == rightValue
	case runtime.DateTimeZone:
		rightValue, ok := right.(runtime.DateTimeZone)
		return ok && leftValue == rightValue
	case runtime.URLValue:
		rightValue, ok := right.(runtime.URLValue)
		return ok && leftValue == rightValue
	case runtime.HTTPHeaders:
		rightValue, ok := right.(runtime.HTTPHeaders)
		if !ok || len(leftValue.Values) != len(rightValue.Values) {
			return false
		}
		for key, values := range leftValue.Values {
			other := rightValue.Values[key]
			if len(values) != len(other) {
				return false
			}
			for i, value := range values {
				if value != other[i] {
					return false
				}
			}
		}
		return true
	case runtime.HTTPCookie:
		rightValue, ok := right.(runtime.HTTPCookie)
		return ok && leftValue == rightValue
	case runtime.TemplateValue:
		rightValue, ok := right.(runtime.TemplateValue)
		return ok && leftValue == rightValue
	case runtime.TemplateEngine:
		rightValue, ok := right.(runtime.TemplateEngine)
		return ok && leftValue == rightValue
	case runtime.Set:
		rightValue, ok := right.(runtime.Set)
		if !ok || len(leftValue.Elements) != len(rightValue.Elements) {
			return false
		}
		for key, entry := range leftValue.Elements {
			other, ok := rightValue.Elements[key]
			if !ok || !primitiveEqual(entry.Value, other.Value) {
				return false
			}
		}
		return true
	case runtime.Range:
		rightValue, ok := right.(runtime.Range)
		return ok &&
			leftValue.Exclusive == rightValue.Exclusive &&
			leftValue.Start.Cmp(rightValue.Start) == 0 &&
			leftValue.End.Cmp(rightValue.End) == 0 &&
			leftValue.Step.Cmp(rightValue.Step) == 0
	case runtime.BytecodeFunction:
		rightValue, ok := right.(runtime.BytecodeFunction)
		return ok && leftValue.Module == rightValue.Module && leftValue.Name == rightValue.Name && leftValue.Index == rightValue.Index
	case runtime.BytecodeClass:
		switch rv := right.(type) {
		case runtime.BytecodeClass:
			return leftValue.Module == rv.Module && leftValue.Name == rv.Name && leftValue.Index == rv.Index
		case runtime.Type:
			return leftValue.Name == rv.Name
		}
		return false
	case runtime.NativeObject:
		rightValue, ok := right.(runtime.NativeObject)
		return ok && leftValue == rightValue
	case runtime.Error:
		rightValue, ok := right.(runtime.Error)
		return ok && leftValue.Class == rightValue.Class && leftValue.Message == rightValue.Message
	case runtime.Type:
		switch rv := right.(type) {
		case runtime.Type:
			return leftValue.Name == rv.Name
		case runtime.BytecodeClass:
			return leftValue.Name == rv.Name
		case *runtime.Class:
			return leftValue.Name == rv.Name
		}
		return false
	case *runtime.Module:
		rightValue, ok := right.(*runtime.Module)
		return ok && leftValue == rightValue
	case *runtime.Class:
		switch rv := right.(type) {
		case *runtime.Class:
			return leftValue == rv
		case runtime.Type:
			return leftValue.Name == rv.Name
		}
		return false
	case *runtime.Interface:
		rightValue, ok := right.(*runtime.Interface)
		return ok && leftValue == rightValue
	case *runtime.Instance:
		rightValue, ok := right.(*runtime.Instance)
		return ok && leftValue == rightValue
	default:
		return false
	}
}

func (vm *VM) errorsIs(args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("errors.is expects two arguments")
	}
	err, ok := args[0].(runtime.Error)
	if !ok {
		return nil, fmt.Errorf("errors.is: first argument must be an error, got %s", args[0].TypeName())
	}
	target, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("errors.is: second argument must be a string class name")
	}
	return runtime.Bool{Value: vm.errorTypeMatches(err.Class, target.Value)}, nil
}

func (vm *VM) errorTypeMatches(class string, target string) bool {
	// Strip an optional module prefix - `catch (errors.HttpException e)`
	// or `instanceof errors.HttpException` matches the bare class name
	// the parent chain records.
	target = stripModulePrefix(target)
	if target == "" || target == "Error" {
		return true
	}
	seen := map[string]bool{}
	for current := class; current != ""; current = vm.errorParent(current) {
		key := strings.ToLower(current)
		if seen[key] {
			return false
		}
		seen[key] = true
		if strings.EqualFold(stripModulePrefix(current), target) {
			return true
		}
	}
	return false
}

// errorValueMatches is the value-aware variant: when the runtime.Error
// carries a Parents chain (populated at construction for error-derived
// classes) the chain takes precedence over vm.errorParent's chunk-local
// walk, so cross-module catch and `instanceof Parent` resolve correctly.
func (vm *VM) errorValueMatches(err runtime.Error, target string) bool {
	target = stripModulePrefix(target)
	if target == "" || target == "Error" {
		return true
	}
	if strings.EqualFold(stripModulePrefix(err.Class), target) {
		return true
	}
	for _, ancestor := range err.Parents {
		if strings.EqualFold(stripModulePrefix(ancestor), target) {
			return true
		}
	}
	// Fall back to the chunk-local walk for built-in error classes
	// whose parent chain isn't recorded in Parents.
	return vm.errorTypeMatches(err.Class, target)
}

func (vm *VM) errorParent(class string) string {
	for _, info := range vm.chunk.Classes {
		if !strings.EqualFold(info.Name, class) {
			continue
		}
		if info.ParentIndex >= 0 && int(info.ParentIndex) < len(vm.chunk.Classes) {
			return vm.chunk.Classes[info.ParentIndex].Name
		}
		return info.ParentName
	}
	switch class {
	case "RuntimeError", "TypeError", "ValueError", "IOError", "ParseError", "MatchError", "ImmutableError":
		return "Error"
	default:
		return ""
	}
}

func (vm *VM) classExtendsBuiltinError(classInfo ClassInfo) bool {
	for {
		if isBuiltinErrorClass(classInfo.ParentName) {
			return true
		}
		if classInfo.ParentIndex < 0 || int(classInfo.ParentIndex) >= len(vm.chunk.Classes) {
			return false
		}
		classInfo = vm.chunk.Classes[classInfo.ParentIndex]
	}
}

func (vm *VM) popString(instruction Instruction, message string) (string, error) {
	value, err := vm.pop()
	if err != nil {
		return "", vm.runtimeError(instruction, "%s", err.Error())
	}
	stringValue, ok := value.(runtime.String)
	if !ok {
		return "", vm.runtimeError(instruction, "%s", message)
	}
	return stringValue.Value, nil
}

func (vm *VM) constantString(instruction Instruction, message string) (string, error) {
	if len(instruction.Operands) < 1 {
		return "", vm.runtimeError(instruction, "instruction missing constant operand")
	}
	return vm.constantStringAt(instruction, instruction.Operands[0], message)
}

func (vm *VM) constantStringAt(instruction Instruction, index int64, message string) (string, error) {
	if index < 0 || int(index) >= len(vm.chunk.Constants) {
		return "", vm.runtimeError(instruction, "constant index out of range")
	}
	stringValue, ok := vm.chunk.Constants[index].(runtime.String)
	if !ok {
		return "", vm.runtimeError(instruction, "%s", message)
	}
	return stringValue.Value, nil
}

// castDunderName returns the dunder method name a class can define
// to control its `as TARGET` conversion. Empty string means no
// dunder is recognised for that target.
func castDunderName(target string) string {
	switch strings.ToLower(strings.TrimPrefix(target, "?")) {
	case "string":
		return "__string"
	case "int":
		return "__int"
	case "float":
		return "__float"
	case "bool":
		return "__bool"
	case "decimal":
		return "__decimal"
	case "bytes":
		return "__bytes"
	}
	return ""
}

// checkCastDunderReturn validates that a cast dunder produced a
// value compatible with the target type.
func checkCastDunderReturn(target string, value runtime.Value) error {
	want := strings.ToLower(strings.TrimPrefix(target, "?"))
	switch want {
	case "string":
		if _, ok := value.(runtime.String); !ok {
			return fmt.Errorf("__string must return string, got %s", value.TypeName())
		}
	case "int":
		switch value.(type) {
		case runtime.SmallInt, runtime.Int:
		default:
			return fmt.Errorf("__int must return int, got %s", value.TypeName())
		}
	case "float":
		if _, ok := value.(runtime.Float); !ok {
			return fmt.Errorf("__float must return float, got %s", value.TypeName())
		}
	case "bool":
		if _, ok := value.(runtime.Bool); !ok {
			return fmt.Errorf("__bool must return bool, got %s", value.TypeName())
		}
	case "decimal":
		if _, ok := value.(runtime.Decimal); !ok {
			return fmt.Errorf("__decimal must return decimal, got %s", value.TypeName())
		}
	case "bytes":
		if _, ok := value.(runtime.Bytes); !ok {
			return fmt.Errorf("__bytes must return bytes, got %s", value.TypeName())
		}
	}
	return nil
}

func castValue(value runtime.Value, target string) (runtime.Value, error) {
	if value.TypeName() == target {
		return value, nil
	}
	/* Nullable targets accept null directly; non-null values fall
	 * through to the underlying type's cast logic. */
	if strings.HasPrefix(target, "?") {
		if _, isNull := value.(runtime.Null); isNull {
			return runtime.Null{}, nil
		}
		return castValue(value, target[1:])
	}
	switch target {
	case "string":
		/* `bytes as string` decodes UTF-8 (errors on invalid bytes)
		 * rather than producing the hex form `value.Inspect()` returns
		 * for bytes. Other types still use `Inspect()` as the canonical
		 * string representation. */
		if v, ok := value.(runtime.Bytes); ok {
			if !utf8.Valid(v.Value) {
				return nil, fmt.Errorf("bytes value is not valid UTF-8")
			}
			return runtime.String{Value: string(v.Value)}, nil
		}
		return runtime.String{Value: value.Inspect()}, nil
	case "int":
		switch v := value.(type) {
		case runtime.SmallInt:
			return v, nil
		case runtime.Int:
			return v, nil
		case runtime.String:
			value, err := runtime.NewIntLiteral(v.Value)
			if err != nil {
				return nil, err
			}
			if value.Value.IsInt64() {
				return runtime.SmallInt{Value: value.Value.Int64()}, nil
			}
			return value, nil
		case runtime.Decimal:
			/* Truncate toward zero: big.Int.Quo handles arbitrary
			 * precision correctly. */
			num := new(big.Int).Set(v.Value.Num())
			den := v.Value.Denom()
			q := new(big.Int).Quo(num, den)
			if q.IsInt64() {
				return runtime.SmallInt{Value: q.Int64()}, nil
			}
			return runtime.Int{Value: q}, nil
		case runtime.Float:
			return runtime.SmallInt{Value: int64(math.Trunc(v.Value))}, nil
		case runtime.Bool:
			if v.Value {
				return runtime.SmallInt{Value: 1}, nil
			}
			return runtime.SmallInt{Value: 0}, nil
		}
	case "decimal":
		switch v := value.(type) {
		case runtime.SmallInt:
			return native.SmallIntToDecimal(v), nil
		case runtime.Int:
			return native.IntToDecimal(v), nil
		case runtime.Float:
			return runtime.NewDecimalLiteral(strconv.FormatFloat(v.Value, 'g', -1, 64))
		case runtime.String:
			return runtime.NewDecimalLiteral(v.Value)
		}
	case "float":
		switch v := value.(type) {
		case runtime.SmallInt:
			return runtime.Float{Value: float64(v.Value)}, nil
		case runtime.Int:
			f, _ := new(big.Rat).SetInt(v.Value).Float64()
			return runtime.Float{Value: f}, nil
		case runtime.Decimal:
			f, _ := v.Value.Float64()
			return runtime.Float{Value: f}, nil
		case runtime.String:
			f, err := strconv.ParseFloat(v.Value, 64)
			if err != nil {
				return nil, err
			}
			return runtime.Float{Value: f}, nil
		}
	case "bool":
		switch v := value.(type) {
		case runtime.Bool:
			return v, nil
		case runtime.SmallInt:
			return runtime.Bool{Value: v.Value != 0}, nil
		case runtime.Int:
			return runtime.Bool{Value: v.Value.Sign() != 0}, nil
		case runtime.Float:
			return runtime.Bool{Value: v.Value != 0}, nil
		case runtime.Decimal:
			return runtime.Bool{Value: v.Value.Sign() != 0}, nil
		case runtime.String:
			switch v.Value {
			case "true":
				return runtime.Bool{Value: true}, nil
			case "false":
				return runtime.Bool{Value: false}, nil
			}
		case runtime.Null:
			return runtime.Bool{Value: false}, nil
		}
	case "bytes":
		/* `string as bytes` encodes UTF-8. Go strings are already UTF-8,
		 * so we copy out the underlying byte sequence. The inverse
		 * (`bytes as string`) is handled in the "string" case above. */
		if v, ok := value.(runtime.String); ok {
			b := make([]byte, len(v.Value))
			copy(b, v.Value)
			return runtime.Bytes{Value: b}, nil
		}
	case "list":
		/* `set as list` materializes; the underlying map's range order
		 * means the resulting list ordering is unspecified (sets are
		 * unordered by design). */
		if v, ok := value.(runtime.Set); ok {
			out := make([]runtime.Value, 0, len(v.Elements))
			for _, entry := range v.Elements {
				out = append(out, entry.Value)
			}
			return runtime.List{Elements: out}, nil
		}
	case "set":
		/* `list as set` de-duplicates. First occurrence wins. */
		if v, ok := value.(runtime.List); ok {
			elements := make(map[string]runtime.SetEntry, len(v.Elements))
			for _, elem := range v.Elements {
				k := native.DictKey(elem)
				if _, seen := elements[k]; seen {
					continue
				}
				elements[k] = runtime.SetEntry{Value: elem}
			}
			return runtime.Set{Elements: elements}, nil
		}
	}
	return nil, fmt.Errorf("cannot cast %s to %s", value.TypeName(), target)
}

func primitiveConversionTarget(name string) (string, bool) {
	switch strings.ToLower(name) {
	case "toint":
		return "int", true
	case "todecimal":
		return "decimal", true
	case "tofloat":
		return "float", true
	case "tobool":
		return "bool", true
	}
	return "", false
}

func indexInt(value runtime.Value) (int, error) {
	switch v := value.(type) {
	case runtime.SmallInt:
		return int(v.Value), nil
	case runtime.Int:
		if !v.Value.IsInt64() {
			return 0, fmt.Errorf("index is out of range")
		}
		return int(v.Value.Int64()), nil
	}
	return 0, fmt.Errorf("index must be int, got %s", value.TypeName())
}

// push wraps a runtime.Value into a VMValue and appends it to the stack.
// Hot opcode handlers that already produce VMValue should call pushVM
// directly to skip the conversion.
func (vm *VM) push(value runtime.Value) {
	vm.stack = append(vm.stack, runtime.VMValueFromValue(value))
}

// pushVM pushes a pre-converted VMValue. Used by the hot integer/bool
// opcode fast paths to avoid round-tripping through the runtime.Value
// interface.
func (vm *VM) pushVM(value runtime.VMValue) {
	vm.stack = append(vm.stack, value)
}

func (vm *VM) pop() (runtime.Value, error) {
	if len(vm.stack) == 0 {
		return nil, fmt.Errorf("stack underflow")
	}
	value := vm.stack[len(vm.stack)-1]
	vm.stack = vm.stack[:len(vm.stack)-1]
	return value.ToValue(), nil
}

// popVM returns the top VMValue without converting to runtime.Value.
func (vm *VM) popVM() (runtime.VMValue, error) {
	if len(vm.stack) == 0 {
		return runtime.VMValueNull, fmt.Errorf("stack underflow")
	}
	value := vm.stack[len(vm.stack)-1]
	vm.stack = vm.stack[:len(vm.stack)-1]
	return value, nil
}

func (vm *VM) peek() (runtime.Value, error) {
	if len(vm.stack) == 0 {
		return nil, fmt.Errorf("stack underflow")
	}
	return vm.stack[len(vm.stack)-1].ToValue(), nil
}

// peekVM returns the top VMValue without converting to runtime.Value.
// Use when the caller only needs Kind/AsBool/I64 access and would
// otherwise pay an interface boxing in peek().
func (vm *VM) peekVM() (runtime.VMValue, error) {
	if len(vm.stack) == 0 {
		return runtime.VMValueNull, fmt.Errorf("stack underflow")
	}
	return vm.stack[len(vm.stack)-1], nil
}

func bitwiseMagicName(op Op) (string, bool) {
	switch op {
	case OpBitAnd:
		return "__bitand", true
	case OpBitOr:
		return "__bitor", true
	case OpBitXor:
		return "__bitxor", true
	case OpLShift:
		return "__lshift", true
	case OpRShift:
		return "__rshift", true
	}
	return "", false
}

func (vm *VM) bitwiseInfix(instruction Instruction, ip int) (int, error) {
	right, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	left, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	if instance, ok := left.(*runtime.Instance); ok {
		if methodName, ok := bitwiseMagicName(instruction.Op); ok {
			classInfo, ok := vm.classInfo(instance.Class.Name)
			if !ok {
				return 0, vm.runtimeError(instruction, "unknown class %s", instance.Class.Name)
			}
			if indices, ok := vm.lookupMethod(classInfo, methodName); ok {
				functionIndex, err := vm.selectRuntimeFunction(instruction, methodName, indices, []runtime.Value{right}, 1)
				if err != nil {
					return 0, err
				}
				return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{instance, right}, nil)
			}
		}
	}
	lb, lok := native.IntValueToBigInt(left)
	rb, rok := native.IntValueToBigInt(right)
	if !lok || !rok {
		return 0, vm.runtimeError(instruction, "bitwise operators require int operands, got %s and %s", left.TypeName(), right.TypeName())
	}
	var result *big.Int
	switch instruction.Op {
	case OpBitAnd:
		result = new(big.Int).And(lb, rb)
	case OpBitOr:
		result = new(big.Int).Or(lb, rb)
	case OpBitXor:
		result = new(big.Int).Xor(lb, rb)
	case OpLShift:
		if !rb.IsUint64() {
			return 0, vm.runtimeError(instruction, "shift amount must be a non-negative int")
		}
		result = new(big.Int).Lsh(lb, uint(rb.Uint64()))
	case OpRShift:
		if !rb.IsUint64() {
			return 0, vm.runtimeError(instruction, "shift amount must be a non-negative int")
		}
		result = new(big.Int).Rsh(lb, uint(rb.Uint64()))
	}
	if result.IsInt64() {
		vm.push(runtime.SmallInt{Value: result.Int64()})
	} else {
		vm.push(runtime.Int{Value: result})
	}
	return -1, nil
}

func (vm *VM) bitwiseNot(instruction Instruction, ip int) (int, error) {
	value, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	if instance, ok := value.(*runtime.Instance); ok {
		classInfo, ok := vm.classInfo(instance.Class.Name)
		if !ok {
			return 0, vm.runtimeError(instruction, "unknown class %s", instance.Class.Name)
		}
		if indices, ok := vm.lookupMethod(classInfo, "__bitnot"); ok {
			functionIndex, err := vm.selectRuntimeFunction(instruction, "__bitnot", indices, nil, 1)
			if err != nil {
				return 0, err
			}
			return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], []runtime.Value{instance}, nil)
		}
	}
	intVal, ok := native.IntValueToBigInt(value)
	if !ok {
		return 0, vm.runtimeError(instruction, "~ requires int, got %s", value.TypeName())
	}
	result := new(big.Int).Not(intVal)
	if result.IsInt64() {
		vm.push(runtime.SmallInt{Value: result.Int64()})
	} else {
		vm.push(runtime.Int{Value: result})
	}
	return -1, nil
}

func (vm *VM) setGlobal(slot int64, value runtime.Value) error {
	if int(slot) >= len(vm.globals) {
		if err := ensureSlot(&vm.globals, slot, "global"); err != nil {
			return err
		}
	}
	if !vm.bridgeActive.Load() {
		vm.globals[slot] = runtime.VMValueFromValue(value)
		return nil
	}
	vm.globalsMu.Lock()
	vm.globals[slot] = runtime.VMValueFromValue(value)
	vm.markGlobalDirtyLocked(int(slot))
	vm.globalsMu.Unlock()
	return nil
}

func (vm *VM) setGlobalVM(slot int64, value runtime.VMValue) error {
	// vm.globals is pre-sized to chunk.GlobalCount at NewVM. Hot path
	// can assume slot is in range. The defensive grow only fires when
	// a caller passes a slot beyond what the chunk advertised, which
	// would indicate either bytecode corruption or a runtime synthesis
	// path that hasn't claimed its slots upfront.
	if int(slot) >= len(vm.globals) {
		if err := ensureSlot(&vm.globals, slot, "global"); err != nil {
			return err
		}
	}
	// Fast path: no wrap-bridge worker can be touching this VM's
	// globals, so the write is safe without locking or dirty tracking.
	// bridgeActive flips false->true on this same goroutine before any
	// worker is observable, so a single atomic Load here is sufficient.
	if !vm.bridgeActive.Load() {
		vm.globals[slot] = value
		return nil
	}
	vm.globalsMu.Lock()
	vm.globals[slot] = value
	vm.markGlobalDirtyLocked(int(slot))
	vm.globalsMu.Unlock()
	return nil
}

// markGlobalDirtyLocked records that slot has been written and grows the
// dirty-bitset alongside vm.globals if a previous ensureSlot grew the
// globals slice beyond the bitset's capacity. Caller holds globalsMu.
func (vm *VM) markGlobalDirtyLocked(slot int) {
	if slot < 0 {
		return
	}
	if slot >= len(vm.dirtyGlobals) {
		grown := make([]bool, len(vm.globals))
		copy(grown, vm.dirtyGlobals)
		vm.dirtyGlobals = grown
	}
	vm.dirtyGlobals[slot] = true
}

func (vm *VM) getGlobal(slot int64) (runtime.Value, error) {
	if int(slot) >= len(vm.globals) {
		if err := ensureSlot(&vm.globals, slot, "global"); err != nil {
			return nil, err
		}
	}
	value := vm.globals[slot]
	if value.Kind == runtime.VMKindUnset {
		return nil, fmt.Errorf("global is undefined")
	}
	if value.Kind == runtime.VMKindBoxed {
		if acc, ok := value.Boxed.(*runtime.StringAccumulator); ok {
			s := acc.Materialize()
			vm.globals[slot] = runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: s}
			return s, nil
		}
	}
	return value.ToValue(), nil
}

func (vm *VM) getGlobalVM(slot int64) (runtime.VMValue, error) {
	if int(slot) >= len(vm.globals) {
		if err := ensureSlot(&vm.globals, slot, "global"); err != nil {
			return runtime.VMValueNull, err
		}
	}
	value := vm.globals[slot]
	if value.Kind == runtime.VMKindUnset {
		return runtime.VMValueNull, fmt.Errorf("global is undefined")
	}
	if value.Kind == runtime.VMKindBoxed {
		if acc, ok := value.Boxed.(*runtime.StringAccumulator); ok {
			materialized := runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: acc.Materialize()}
			vm.globals[slot] = materialized
			return materialized, nil
		}
	}
	return value, nil
}

func (vm *VM) setLocal(slot int64, value runtime.Value) error {
	idx := vm.currentFrameBP + int(slot)
	if idx >= len(vm.localsStack) {
		if err := vm.ensureLocalSlot(slot); err != nil {
			return err
		}
	}
	cur := vm.localsStack[idx]
	if cur.Kind == runtime.VMKindBoxed {
		if cell, ok := cur.Boxed.(*runtime.BytecodeCell); ok {
			if _, replacingCell := value.(*runtime.BytecodeCell); !replacingCell {
				cell.Value = value
				return nil
			}
		}
	}
	vm.localsStack[idx] = runtime.VMValueFromValue(value)
	return nil
}

func (vm *VM) setLocalVM(slot int64, value runtime.VMValue) error {
	idx := vm.currentFrameBP + int(slot)
	if idx >= len(vm.localsStack) {
		if err := vm.ensureLocalSlot(slot); err != nil {
			return err
		}
	}
	cur := vm.localsStack[idx]
	if cur.Kind == runtime.VMKindBoxed {
		if cell, ok := cur.Boxed.(*runtime.BytecodeCell); ok {
			if _, replacingCell := value.Boxed.(*runtime.BytecodeCell); !replacingCell || value.Kind != runtime.VMKindBoxed {
				cell.Value = value.ToValue()
				return nil
			}
		}
	}
	vm.localsStack[idx] = value
	return nil
}

func (vm *VM) getLocal(slot int64) (runtime.Value, error) {
	idx := vm.currentFrameBP + int(slot)
	if idx >= len(vm.localsStack) {
		if err := vm.ensureLocalSlot(slot); err != nil {
			return nil, err
		}
	}
	value := vm.localsStack[idx]
	if value.Kind == runtime.VMKindUnset {
		return nil, fmt.Errorf("local is undefined")
	}
	if value.Kind == runtime.VMKindBoxed {
		if cell, ok := value.Boxed.(*runtime.BytecodeCell); ok {
			if cell.Value == nil {
				return nil, fmt.Errorf("local is undefined")
			}
			return cell.Value, nil
		}
		if acc, ok := value.Boxed.(*runtime.StringAccumulator); ok {
			s := acc.Materialize()
			vm.localsStack[idx] = runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: s}
			return s, nil
		}
	}
	return value.ToValue(), nil
}

func (vm *VM) getLocalVM(slot int64) (runtime.VMValue, error) {
	idx := vm.currentFrameBP + int(slot)
	if idx >= len(vm.localsStack) {
		if err := vm.ensureLocalSlot(slot); err != nil {
			return runtime.VMValueNull, err
		}
	}
	value := vm.localsStack[idx]
	if value.Kind == runtime.VMKindUnset {
		return runtime.VMValueNull, fmt.Errorf("local is undefined")
	}
	if value.Kind == runtime.VMKindBoxed {
		if cell, ok := value.Boxed.(*runtime.BytecodeCell); ok {
			if cell.Value == nil {
				return runtime.VMValueNull, fmt.Errorf("local is undefined")
			}
			return runtime.VMValueFromValue(cell.Value), nil
		}
		if acc, ok := value.Boxed.(*runtime.StringAccumulator); ok {
			materialized := runtime.VMValue{Kind: runtime.VMKindBoxed, Boxed: acc.Materialize()}
			vm.localsStack[idx] = materialized
			return materialized, nil
		}
	}
	return value, nil
}

func (vm *VM) ensureLocalSlot(slot int64) error {
	if slot < 0 {
		return fmt.Errorf("local slot out of range")
	}
	maxInt := int64(int(^uint(0) >> 1))
	if slot > maxInt {
		return fmt.Errorf("local slot out of range")
	}
	end := vm.currentFrameBP + int(slot) + 1
	vm.ensureLocalsStack(end)
	return nil
}

func ensureSlot(values *[]runtime.VMValue, slot int64, label string) error {
	if slot < 0 {
		return fmt.Errorf("%s slot out of range", label)
	}
	maxInt := int64(int(^uint(0) >> 1))
	if slot > maxInt {
		return fmt.Errorf("%s slot out of range", label)
	}
	target := int(slot) + 1
	if target <= len(*values) {
		return nil
	}
	newLen := len(*values)
	if newLen == 0 {
		newLen = 1
	}
	for newLen < target {
		newLen *= 2
	}
	grown := make([]runtime.VMValue, newLen)
	copy(grown, *values)
	*values = grown
	return nil
}

func (vm *VM) vmStackTrace(errorLine int) string {
	if len(vm.frames) == 0 {
		return ""
	}
	var sb strings.Builder
	N := len(vm.frames)
	// Innermost: the currently executing function
	innerName := vm.frames[N-1].functionName
	if innerName == "" {
		innerName = "<anonymous>"
	}
	if errorLine > 0 {
		fmt.Fprintf(&sb, "\n  at %s (line %d)", innerName, errorLine)
	} else {
		fmt.Fprintf(&sb, "\n  at %s", innerName)
	}
	// Walk from innermost outward, showing where each was called from
	for i := N - 1; i >= 0; i-- {
		line := vm.frames[i].callLine
		var callerName string
		if i > 0 {
			callerName = vm.frames[i-1].functionName
			if callerName == "" {
				callerName = "<anonymous>"
			}
		} else {
			callerName = "<top level>"
		}
		if line > 0 {
			fmt.Fprintf(&sb, "\n  at %s (line %d)", callerName, line)
		} else {
			fmt.Fprintf(&sb, "\n  at %s", callerName)
		}
	}
	return sb.String()
}

func (vm *VM) runtimeError(instruction Instruction, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	trace := vm.vmStackTrace(instruction.Line)
	if instruction.Line > 0 {
		base := fmt.Sprintf("bytecode runtime error at %d:%d: %s", instruction.Line, instruction.Column, message)
		return fmt.Errorf("%s%s", base, trace)
	}
	return fmt.Errorf("bytecode runtime error: %s%s", message, trace)
}

// runtimeErrorWith wraps the formatted runtime-error message around a
// caller-supplied inner error so the chain can be unwrapped with
// errors.As. Used to thread a vmThrownError (carrying the underlying
// runtime.Error) across a VM boundary so the calling VM can catch it.
func (vm *VM) runtimeErrorWith(instruction Instruction, inner error, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	trace := vm.vmStackTrace(instruction.Line)
	var prefix string
	if instruction.Line > 0 {
		prefix = fmt.Sprintf("bytecode runtime error at %d:%d: %s%s", instruction.Line, instruction.Column, message, trace)
	} else {
		prefix = fmt.Sprintf("bytecode runtime error: %s%s", message, trace)
	}
	return &wrappedError{prefix: prefix, inner: inner}
}

// wrappedError preserves Unwrap support so callers can recover the
// inner vmThrownError via errors.As while still seeing the formatted
// runtime-error stack trace via Error().
type wrappedError struct {
	prefix string
	inner  error
}

func (e *wrappedError) Error() string { return e.prefix }
func (e *wrappedError) Unwrap() error { return e.inner }

// propagateModuleError converts a Go error returned by a moduleLoader
// dispatch into the calling VM's exception state. If the error wraps a
// vmThrownError, the embedded runtime.Error is set as the calling VM's
// pendingThrow and control jumps to the nearest exception handler so
// `catch` clauses see the original typed throw rather than a stringified
// "uncaught X: Y" message. All other errors are returned verbatim.
func (vm *VM) propagateModuleError(instruction Instruction, ip int, err error) (int, error) {
	var thrown vmThrownError
	if errors.As(err, &thrown) {
		captured := thrown.err
		vm.pendingThrow = &captured
		return vm.jumpToExceptionHandler(instruction, ip)
	}
	return 0, vm.runtimeError(instruction, "%s", err.Error())
}
