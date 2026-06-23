package bytecode

import (
	"errors"
	"fmt"
	argbinding "geblang/internal/binding"
	"geblang/internal/native"
	"geblang/internal/runtime"
	"strings"
)

func (vm *VM) methodCallSpread(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.fatalError(instruction, "method-call-spread instruction has invalid operands")
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
	if spreadList, ok := spreadVal.(*runtime.List); ok {
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
	spreadDict, ok := spreadVal.(runtime.Dict)
	if !ok {
		return 0, vm.runtimeError(instruction, "spread argument must be a list or dict")
	}
	name, err := vm.constantStringAt(instruction, instruction.Operands[0], "method name constant must be string")
	if err != nil {
		return 0, err
	}
	receiver, err := vm.pop()
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	paramNames, err := vm.receiverParamNames(instruction, receiver, name)
	if err != nil {
		return 0, err
	}
	args, names, err := spreadDictNamedArguments(spreadDict, staticArgs, paramNames)
	if err != nil {
		return 0, vm.runtimeError(instruction, "%s", err.Error())
	}
	return vm.dispatchNamedCall(instruction, ip, receiver, name, args, names)
}

// orderNamedByParamNames places named/positional args into declared
// parameter order; trailing unfilled slots are trimmed so the callee's
// declared defaults engage, middle holes are an error.
func orderNamedByParamNames(fnName string, args []runtime.Value, names []string, paramNames []string) ([]runtime.Value, error) {
	// This path has no default metadata (reflect targets and native
	// wrappers carry names only), so every slot is treated as
	// defaultable for ordering; trailing holes are trimmed for the
	// downstream default fill and middle holes are rejected here -
	// the documented defaults-must-trail contract for this dispatch
	// shape.
	sig := argbinding.Signature{
		FuncName:   fnName,
		ParamNames: paramNames,
		HasDefault: make([]bool, len(paramNames)),
	}
	for i := range sig.HasDefault {
		sig.HasDefault[i] = true
	}
	bargs := make([]argbinding.Arg, len(args))
	for i, name := range names {
		bargs[i].Name = name
	}
	result, err := argbinding.Order(sig, bargs)
	if err != nil {
		return nil, err
	}
	ordered := make([]runtime.Value, len(result.Slots))
	filled := make([]bool, len(result.Slots))
	for i, slot := range result.Slots {
		if slot != argbinding.DefaultSlot {
			ordered[i] = args[slot]
			filled[i] = true
		}
	}
	end := len(ordered)
	for end > 0 && !filled[end-1] {
		end--
	}
	for i := 0; i < end; i++ {
		if !filled[i] {
			return nil, fmt.Errorf("%s missing argument %s", fnName, paramNames[i])
		}
	}
	return ordered[:end], nil
}

// receiverParamNames returns a callable receiver's parameter names
// (sans receiver slot) so dict spread can drop unknown keys before
// named dispatch.
func (vm *VM) receiverParamNames(instruction Instruction, receiver runtime.Value, methodName string) ([]string, error) {
	switch r := receiver.(type) {
	case runtime.DecoratorTarget:
		// Reflect targets carry the declared parameter metadata directly.
		if r.Function != nil && len(r.Function.Parameters) > 0 {
			names := make([]string, 0, len(r.Function.Parameters))
			for _, p := range r.Function.Parameters {
				names = append(names, p.Name)
			}
			return names, nil
		}
		if r.Callable != nil {
			return vm.receiverParamNames(instruction, r.Callable, methodName)
		}
	case *runtime.Instance:
		if r.Class == nil {
			return nil, vm.runtimeError(instruction, "cannot use dict spread with a cross-module method")
		}
		if r.Class.Module != vm.moduleName {
			if vm.moduleLoader == nil {
				return nil, vm.runtimeError(instruction, "cannot use dict spread with a cross-module method")
			}
			names, err := vm.moduleLoader.ModuleMethodParamNames(r.Class.Module, r.Class.Name, methodName)
			if err != nil {
				return nil, vm.runtimeError(instruction, "%s", err.Error())
			}
			return names, nil
		}
		classInfo, ok := vm.classInfo(r.Class.Name)
		if !ok {
			return nil, vm.runtimeError(instruction, "unknown class %s", r.Class.Name)
		}
		indices, ok := vm.lookupMethod(classInfo, methodName)
		if !ok || len(indices) != 1 {
			return nil, vm.runtimeError(instruction, "cannot use dict spread with method %s.%s", r.Class.Name, methodName)
		}
		fn := vm.chunk.Functions[indices[0]]
		if len(fn.ParamNames) > 0 {
			return fn.ParamNames[1:], nil
		}
		return nil, nil
	case runtime.BytecodeFunction:
		if r.Module != vm.moduleName {
			return nil, vm.runtimeError(instruction, "cannot use dict spread with a cross-module function value")
		}
		if int(r.Index) >= len(vm.chunk.Functions) {
			return nil, vm.runtimeError(instruction, "function index out of range")
		}
		return vm.chunk.Functions[r.Index].ParamNames, nil
	case runtime.BytecodeClosure:
		if r.Module != vm.moduleName {
			return nil, vm.runtimeError(instruction, "cannot use dict spread with a cross-module closure")
		}
		if int(r.FunctionIndex) >= len(vm.chunk.Functions) {
			return nil, vm.runtimeError(instruction, "closure function index out of range")
		}
		fn := vm.chunk.Functions[r.FunctionIndex]
		offset := int(fn.UpvalueCount)
		if offset > len(fn.ParamNames) {
			offset = len(fn.ParamNames)
		}
		return fn.ParamNames[offset:], nil
	case runtime.Function:
		names := make([]string, 0, len(r.Parameters))
		for _, p := range r.Parameters {
			if p.Name != nil {
				names = append(names, p.Name.Value)
			}
		}
		if len(names) > 0 {
			return names, nil
		}
	}
	return nil, vm.runtimeError(instruction, "dict spread is not supported for this callable")
}

// callResolvedMethod skips classInfo/methodLookup/overload-selection
// when the compiler proved the receiver's class statically.
func (vm *VM) callResolvedMethod(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.fatalError(instruction, "resolved method call has invalid operands")
	}
	functionIndex := instruction.Operands[0]
	argc := int(instruction.Operands[1])
	if functionIndex < 0 || int(functionIndex) >= len(vm.chunk.Functions) {
		return 0, vm.runtimeError(instruction, "function index out of range")
	}
	n := len(vm.stack)
	if argc+1 > n {
		return 0, vm.fatalError(instruction, "stack underflow")
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

// propagateCallbackError surfaces an error from a native method that ran a
// Geblang callback (e.g. dataframe.filterFn): a typed error is re-thrown, an
// already-formed VM error keeps its thrown class/message and frames, and a plain
// error is rendered. Re-rendering a formed error would double its prefix.
func (vm *VM) propagateCallbackError(instruction Instruction, ip int, err error) (int, error) {
	var typed vmTypedError
	if errors.As(err, &typed) {
		return vm.throwTyped(instruction, ip, typed.class, typed.message)
	}
	var rtErr *vmRuntimeError
	var wrErr *wrappedError
	if errors.As(err, &rtErr) || errors.As(err, &wrErr) {
		return 0, err
	}
	return 0, vm.runtimeError(instruction, "%s", err.Error())
}

func (vm *VM) methodCall(instruction Instruction, ip int) (int, error) {
	if len(instruction.Operands) != 2 {
		return 0, vm.fatalError(instruction, "method call instruction has invalid operands")
	}
	// Consumed here so planted type args cannot leak into a later call.
	callTypeArgs := vm.pendingCallTypeArgs
	vm.pendingCallTypeArgs = nil
	nameIndex := instruction.Operands[0]
	argc := int(instruction.Operands[1])
	if nameIndex < 0 || int(nameIndex) >= vm.constantsLen() {
		return 0, vm.runtimeError(instruction, "method name constant out of range")
	}
	nameValue, ok := vm.constantValue(nameIndex).(runtime.String)
	if !ok {
		return 0, vm.runtimeError(instruction, "method name constant must be string")
	}
	// slots[0]=receiver, slots[1:]=args - one alloc instead of two.
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
					return vm.propagateModuleError(instruction, ip, err)
				}
				vm.push(result)
				return ip, nil
			}
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			result, err := vm.moduleLoader.CallModuleMethod(instance.Class.Module, instance.Class.Name, nameValue.Value, instance, vm.wrapStatefulNativeArgs("", "", args))
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
				functionIndex, err := vm.selectRuntimeFunction(instruction, "__call", fallbackIndices, []runtime.Value{runtime.String{Value: nameValue.Value}, &runtime.List{Elements: args}}, 1)
				if err != nil {
					return 0, err
				}
				callArgs := []runtime.Value{instance, runtime.String{Value: nameValue.Value}, &runtime.List{Elements: args}}
				return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], callArgs, nil)
			}
			if result, handled, err := vm.callBuiltinParentMethod(classInfo, instance, nameValue.Value, args); handled {
				if err != nil {
					/* preserve the TestSkip class so the runner records a
					 * skip, not a failure; other assertion errors stay on
					 * the generic runtime-error path */
					if rerr := runtime.NewRecoverableError(err); rerr.Class == "TestSkip" {
						return vm.throwRecoverableError(instruction, ip, err)
					}
					return 0, vm.runtimeError(instruction, "%s", err.Error())
				}
				vm.push(result)
				return ip, nil
			}
			// Cross-module parent: the method may live in the parent's
			// own chunk (e.g. `class B extends mod.A` then `b.foo()`).
			// Dispatch via the module loader so the parent's chunk
			// handles the lookup and execution.
			if module, parentClass, ok := vm.crossModuleBoundary(classInfo); ok && vm.moduleLoader != nil {
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
				return vm.throwRecoverableError(instruction, ip, result.Err)
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
	if gen, ok := receiver.(*runtime.Generator); ok {
		result, err := native.GeneratorMethod(gen, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if arr, ok := receiver.(*runtime.NDArray); ok {
		result, err := native.NDArrayMethod(arr, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if dist, ok := receiver.(*runtime.Distribution); ok {
		result, err := native.DistributionMethod(dist, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if frame, ok := receiver.(*runtime.DataFrame); ok {
		result, err := native.DataFrameMethod(frame, nameValue.Value, args)
		if err != nil {
			return vm.propagateCallbackError(instruction, ip, err)
		}
		vm.push(result)
		return ip, nil
	}
	if series, ok := receiver.(*runtime.DFSeries); ok {
		result, err := native.DFSeriesMethod(series, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if expr, ok := receiver.(*runtime.DFExpr); ok {
		result, err := native.DFExprMethod(expr, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if group, ok := receiver.(*runtime.DFGroupBy); ok {
		result, err := native.DFGroupByMethod(group, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
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
			if module.Canonical != "" && vm.statefulNative != nil {
				result, err := vm.statefulNative.CallBuiltin(module.Canonical, nameValue.Value, vm.wrapStatefulNativeArgs(module.Canonical, nameValue.Value, args), nil)
				if err == nil {
					vm.push(result)
					return ip, nil
				}
			}
			return 0, vm.runtimeError(instruction, "module %s has no export %s", module.Name, nameValue.Value)
		}
		if function, ok := value.(runtime.BytecodeFunction); ok {
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			result, err := vm.moduleLoader.CallModuleFunction(function, vm.wrapStatefulNativeArgs("", "", args))
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
			result, err := vm.moduleLoader.ConstructModuleClass(class, vm.wrapStatefulNativeArgs("", "", args), callTypeArgs)
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
				result, err := vm.moduleLoader.ConstructModuleClass(class, vm.wrapStatefulNativeArgs("", "", args), callTypeArgs)
				if err != nil {
					return vm.propagateModuleError(instruction, ip, err)
				}
				vm.push(result)
				return ip, nil
			}
			if class.Raw {
				vm.stageTypeArgsAsBindings(class.Index, callTypeArgs)
				return vm.constructClassWithArgs(instruction, ip, class.Index, args, true)
			}
			result, err := vm.ConstructClassWithTypeArgs(class.Index, args, callTypeArgs)
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
			result, err := vm.moduleLoader.CallModuleStaticMethod(class, nameValue.Value, vm.wrapStatefulNativeArgs("", "", args))
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
			result, err := vm.moduleLoader.CallModuleClosure(closure, vm.wrapStatefulNativeArgs("", "", args))
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
		if value, handled, err := runtime.EnumStaticMethod(enumDef, variantName, args); handled {
			if err != nil {
				return 0, vm.runtimeError(instruction, "%s", err.Error())
			}
			vm.push(value)
			return ip, nil
		}
		return 0, vm.runtimeError(instruction, "enum %s has no variant %s", enumDef.Name, variantName)
	}
	if variant, ok := receiver.(runtime.EnumVariant); ok {
		indices, ok := variant.Enum.MethodIndices[strings.ToLower(nameValue.Value)]
		if !ok || len(indices) == 0 {
			return vm.throwTyped(instruction, ip, "RuntimeError", fmt.Sprintf("unknown method %s.%s", variant.Enum.Name, nameValue.Value))
		}
		// A foreign enum's method index resolves against its home chunk, not
		// this one; route through the module loader.
		if variant.Enum.Module != "" && variant.Enum.Module != vm.moduleName {
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			fn := runtime.BytecodeFunction{Module: variant.Enum.Module, Index: indices[0]}
			result, err := vm.moduleLoader.CallModuleFunction(fn, slots)
			if err != nil {
				return vm.propagateModuleError(instruction, ip, err)
			}
			vm.push(result)
			return ip, nil
		}
		functionIndex, err := vm.selectRuntimeFunction(instruction, nameValue.Value, indices, args, 1)
		if err != nil {
			return 0, err
		}
		if vm.chunk.Functions[functionIndex].IsGenerator && !vm.generatorExecution {
			vm.push(vm.lazyGenerator(functionIndex, slots))
			return ip, nil
		}
		return vm.startPrevalidatedFunction(instruction, ip, &vm.chunk.Functions[functionIndex], slots, nil)
	}
	if list, ok := receiver.(*runtime.List); ok {
		result, handled, err := vm.listHigherOrderMethod(instruction, list, nameValue.Value, args)
		if err != nil {
			var typed vmTypedError
			if errors.As(err, &typed) {
				return vm.throwTyped(instruction, ip, typed.class, typed.message)
			}
			// A callback's runtime error is already a formed VM error carrying the
			// throw-site frames (a vmRuntimeError, or a wrappedError from the
			// closure's own uncaught render); propagate it rather than re-rendering
			// its string into a fresh error, which doubled the prefix and frames.
			var rtErr *vmRuntimeError
			var wrErr *wrappedError
			if errors.As(err, &rtErr) || errors.As(err, &wrErr) {
				return 0, err
			}
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
	if nativeObject, ok := receiver.(runtime.NativeObject); ok {
		caller, ok := vm.statefulNative.(nativeObjectMethodCaller)
		if !ok {
			return 0, vm.runtimeError(instruction, "%s", native.UnknownMethodError(nativeObject.TypeName(), nameValue.Value).Error())
		}
		result, err := caller.NativeObjectMethod(nativeObject, nameValue.Value, args)
		if err != nil {
			return 0, vm.runtimeError(instruction, "%s", err.Error())
		}
		vm.push(result)
		return ip, nil
	}
	if str, ok := receiver.(runtime.String); ok {
		if handled, result, err := vm.stringSearchMethod(str, nameValue.Value, args); handled {
			if err != nil {
				var typed vmTypedError
				if errors.As(err, &typed) {
					return vm.throwTyped(instruction, ip, typed.class, typed.message)
				}
				return 0, vm.runtimeError(instruction, "%s", err.Error())
			}
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
		return 0, vm.fatalError(instruction, "named method call instruction has invalid operands")
	}
	nameIndex := instruction.Operands[0]
	argc := int(instruction.Operands[1])
	if len(instruction.Operands) != 2+argc {
		return 0, vm.runtimeError(instruction, "named method call argument metadata mismatch")
	}
	if nameIndex < 0 || int(nameIndex) >= vm.constantsLen() {
		return 0, vm.runtimeError(instruction, "method name constant out of range")
	}
	nameValue, ok := vm.constantValue(nameIndex).(runtime.String)
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
	return vm.dispatchNamedCall(instruction, ip, receiver, nameValue.Value, args, names)
}

// dispatchNamedCall invokes a callable receiver with named/positional
// argument metadata. Shared by OpMethodCallNamed and the dict-spread
// path of OpMethodCallSpread.
func (vm *VM) dispatchNamedCall(instruction Instruction, ip int, receiver runtime.Value, methodName string, args []runtime.Value, names []string) (int, error) {
	if target, ok := receiver.(runtime.DecoratorTarget); ok && target.Callable != nil {
		if methodName == "__invoke" && target.Function != nil && len(target.Function.Parameters) > 0 {
			// The reflect metadata is the only carrier of the wrapped
			// callable's parameter names; order here before it is lost.
			paramNames := make([]string, 0, len(target.Function.Parameters))
			for _, p := range target.Function.Parameters {
				paramNames = append(paramNames, p.Name)
			}
			ordered, oerr := orderNamedByParamNames(methodName, args, names, paramNames)
			if oerr != nil {
				return 0, vm.runtimeError(instruction, "%s", oerr.Error())
			}
			result, cerr := vm.callCallable(target.Callable, ordered)
			if cerr != nil {
				return vm.propagateModuleError(instruction, ip, cerr)
			}
			vm.push(result)
			return ip, nil
		}
		return vm.dispatchNamedCall(instruction, ip, target.Callable, methodName, args, names)
	}
	if bytecodeFunction, ok := receiver.(runtime.BytecodeFunction); ok {
		if methodName != "__invoke" {
			return 0, vm.runtimeError(instruction, "function has no method %s", methodName)
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
		if methodName != "__invoke" {
			return 0, vm.runtimeError(instruction, "closure does not have method %s", methodName)
		}
		if closure.Module != vm.moduleName {
			if vm.moduleLoader == nil {
				return 0, vm.runtimeError(instruction, "bytecode module loader is not configured")
			}
			result, err := vm.moduleLoader.CallModuleClosure(closure, vm.wrapStatefulNativeArgs("", "", args))
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
		// Generic callables (reflect targets, native-wrapped functions):
		// order by declared parameter names and dispatch positionally;
		// the callee's own binding fills trailing defaults.
		if methodName == "__invoke" {
			paramNames, perr := vm.receiverParamNames(instruction, receiver, methodName)
			if perr == nil {
				ordered, oerr := orderNamedByParamNames(methodName, args, names, paramNames)
				if oerr != nil {
					return 0, vm.runtimeError(instruction, "%s", oerr.Error())
				}
				result, cerr := vm.callCallable(receiver, ordered)
				if cerr != nil {
					return vm.propagateModuleError(instruction, ip, cerr)
				}
				vm.push(result)
				return ip, nil
			}
		}
		return 0, vm.runtimeError(instruction, "named method arguments are only supported for class instances")
	}
	if instance.Class != nil && instance.Class.Module != vm.moduleName && vm.moduleLoader != nil {
		paramNames, perr := vm.moduleLoader.ModuleMethodParamNames(instance.Class.Module, instance.Class.Name, methodName)
		if perr != nil {
			return 0, vm.runtimeError(instruction, "%s", perr.Error())
		}
		ordered, oerr := orderNamedByParamNames(instance.Class.Name+"."+methodName, args, names, paramNames)
		if oerr != nil {
			return 0, vm.runtimeError(instruction, "%s", oerr.Error())
		}
		result, cerr := vm.moduleLoader.CallModuleMethod(instance.Class.Module, instance.Class.Name, methodName, instance, ordered)
		if cerr != nil {
			return vm.propagateModuleError(instruction, ip, cerr)
		}
		vm.push(result)
		return ip, nil
	}
	classInfo, ok := vm.classInfo(instance.Class.Name)
	if !ok {
		return 0, vm.runtimeError(instruction, "unknown class %s", instance.Class.Name)
	}
	indices, ok := vm.lookupMethod(classInfo, methodName)
	if !ok {
		return vm.throwTyped(instruction, ip, "RuntimeError", fmt.Sprintf("unknown method %s.%s", instance.Class.Name, methodName))
	}
	functionIndex, ordered, err := vm.selectRuntimeNamedFunction(instruction, methodName, indices, args, names, 1)
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
