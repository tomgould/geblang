package evaluator

import (
	"fmt"
	"math/big"
	"sync"

	"geblang/internal/ast"
	"geblang/internal/ffi"
	"geblang/internal/runtime"
)

// ffiState owns the per-evaluator FFI runtime: the active capability
// policy plus the registry of opened library handles.
type ffiState struct {
	mu          sync.Mutex
	policy      *ffi.Policy
	libs        map[int64]*ffi.Library
	nextID      int64
	structs     map[int64]*ffi.StructLayout
	nextStruct  int64
	callbacks   []*ffiCallback
}

func newFFIState() *ffiState {
	return &ffiState{
		libs:    map[int64]*ffi.Library{},
		structs: map[int64]*ffi.StructLayout{},
	}
}

func init() {
	ffi.SetCallableInvoker(invokeFFICallback)
}

// invokeFFICallback is the bridge purego calls when C dispatches a
// Geblang-defined callback. The token is the runtime callable that
// was registered at ffi.callback() time; args are Go scalars in
// the declared callback-signature order.
func invokeFFICallback(token any, args []any) (any, error) {
	cb, ok := token.(*ffiCallback)
	if !ok || cb == nil {
		return nil, fmt.Errorf("ffi.callback: invalid token")
	}
	runtimeArgs := make([]runtime.Value, len(args))
	for i, a := range args {
		v, err := ffiCallbackArgToRuntime(cb.argTypes[i], a)
		if err != nil {
			return nil, fmt.Errorf("ffi.callback arg %d: %w", i, err)
		}
		runtimeArgs[i] = v
	}
	result, err := cb.evaluator.applyFunctionWithThis(cb.fn, runtimeArgs, nil)
	if err != nil {
		return nil, err
	}
	if cb.retType == ffi.Void {
		return nil, nil
	}
	return ffiCallbackReturnToGo(cb.retType, result)
}

type ffiCallback struct {
	fn        runtime.Function
	argTypes  []ffi.Type
	retType   ffi.Type
	evaluator *Evaluator
	cPtr      uintptr
}

func ffiCallbackArgToRuntime(t ffi.Type, v any) (runtime.Value, error) {
	switch t {
	case ffi.Int8, ffi.Int16, ffi.Int32, ffi.Int64:
		n, _ := v.(int64)
		return runtime.NewInt64(n), nil
	case ffi.Uint8, ffi.Uint16, ffi.Uint32, ffi.Uint64:
		u, _ := v.(uint64)
		return runtime.NewInt64(int64(u)), nil
	case ffi.Ptr:
		p, _ := v.(uintptr)
		return runtime.NewInt64(int64(p)), nil
	}
	return nil, fmt.Errorf("unsupported callback arg type %s", t)
}

func ffiCallbackReturnToGo(t ffi.Type, v runtime.Value) (any, error) {
	switch t {
	case ffi.Int8, ffi.Int16, ffi.Int32, ffi.Int64:
		n, ok := toInt64(v)
		if !ok {
			return int64(0), fmt.Errorf("callback return: expected int, got %s", v.TypeName())
		}
		return n, nil
	case ffi.Uint8, ffi.Uint16, ffi.Uint32, ffi.Uint64:
		n, ok := toInt64(v)
		if !ok {
			return uint64(0), fmt.Errorf("callback return: expected int, got %s", v.TypeName())
		}
		return uint64(n), nil
	case ffi.Ptr:
		n, ok := toInt64(v)
		if !ok {
			return uintptr(0), fmt.Errorf("callback return: expected int, got %s", v.TypeName())
		}
		return uintptr(n), nil
	}
	return nil, fmt.Errorf("unsupported callback return type %s", t)
}

// SetFFIPolicy installs the capability policy that gates future
// ffi.dlopen calls. Pass nil to revert to deny-all. Callers
// (typically cmd/geblang) construct the policy from the project
// manifest + --allow-ffi CLI flags before starting the script.
func (e *Evaluator) SetFFIPolicy(p *ffi.Policy) {
	e.ffi.mu.Lock()
	e.ffi.policy = p
	e.ffi.mu.Unlock()
}

// BuildFFIPolicy assembles the policy for a script: walks up from
// scriptDir to find a project manifest, parses its
// permissions.ffi block, and overlays it with the CLI flag
// patterns. The returned policy is what callers pass to
// SetFFIPolicy before running the script.
//
// scriptDir empty means "no manifest search"; only the CLI
// overlay applies, which lets one-off scripts opt in via
// --allow-ffi without needing a geblang.yaml.
func (e *Evaluator) BuildFFIPolicy(scriptDir string, cliPatterns []string) (*ffi.Policy, error) {
	var base *ffi.Policy
	var projectRoot string
	if scriptDir != "" {
		manifest, err := e.findPackageManifest(scriptDir)
		if err == nil && manifest != nil {
			projectRoot = manifest.Root
			base, err = ffi.NewPolicyFromConfig(manifest.Permissions.FFI, manifest.Root)
			if err != nil {
				return nil, err
			}
		}
	}
	if base == nil {
		base = &ffi.Policy{ProjectRoot: projectRoot}
	}
	overlay, err := ffi.NewPolicyFromCLI(cliPatterns, projectRoot)
	if err != nil {
		return nil, err
	}
	return base.Overlay(overlay), nil
}

// FFIPolicy returns the currently-installed policy. Used by
// `geblang doctor` to surface the active rules.
func (e *Evaluator) FFIPolicy() *ffi.Policy {
	e.ffi.mu.Lock()
	defer e.ffi.mu.Unlock()
	return e.ffi.policy
}

// FFILoadedLibraries returns the paths of currently-open libraries,
// in id order. Used by `geblang doctor` to surface what scripts
// have actually loaded.
func (e *Evaluator) FFILoadedLibraries() []string {
	e.ffi.mu.Lock()
	defer e.ffi.mu.Unlock()
	out := make([]string, 0, len(e.ffi.libs))
	for _, lib := range e.ffi.libs {
		out = append(out, lib.Path)
	}
	return out
}

func (e *Evaluator) ffiBuiltins() map[string]builtinFunc {
	return map[string]builtinFunc{
		"dlopen":      e.ffiDlopen,
		"symbol":      e.ffiSymbol,
		"close":       e.ffiClose,
		"alloc":       e.ffiAlloc,
		"free":        e.ffiFree,
		"readBytes":   e.ffiReadBytes,
		"writeBytes":  e.ffiWriteBytes,
		"readCString": e.ffiReadCString,
		"cString":     e.ffiCString,
		"errno":       e.ffiErrno,
		"newStruct":   e.ffiNewStruct,
		"structSize":  e.ffiStructSize,
		"structGet":   e.ffiStructGet,
		"structSet":   e.ffiStructSet,
		"callback":    e.ffiCallback,
		"sizeOf":      e.ffiSizeOf,
		"writeArray":  e.ffiWriteArray,
		"readArray":   e.ffiReadArray,
		"bytesView":   e.ffiBytesView,
	}
}

func (e *Evaluator) ffiSizeOf(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ffi.sizeOf expects (type: int)")
	}
	t, err := ffiTypeFromValue(args[0])
	if err != nil {
		return nil, err
	}
	sz, err := ffi.SizeOf(t)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(sz)), nil
}

func (e *Evaluator) ffiWriteArray(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("ffi.writeArray expects (ptr: int, type: int, values: list<any>)")
	}
	ptr, ok := toInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("ffi.writeArray ptr must be int")
	}
	t, err := ffiTypeFromValue(args[1])
	if err != nil {
		return nil, err
	}
	list, ok := args[2].(runtime.List)
	if !ok {
		return nil, fmt.Errorf("ffi.writeArray values must be list")
	}
	goVals := make([]any, len(list.Elements))
	for i, elem := range list.Elements {
		g, err := ffiValueToGo(t, elem)
		if err != nil {
			return nil, fmt.Errorf("ffi.writeArray element %d: %w", i, err)
		}
		goVals[i] = g
	}
	if err := ffi.WriteArray(uintptr(ptr), t, goVals); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) ffiReadArray(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("ffi.readArray expects (ptr: int, type: int, length: int)")
	}
	ptr, ok := toInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("ffi.readArray ptr must be int")
	}
	t, err := ffiTypeFromValue(args[1])
	if err != nil {
		return nil, err
	}
	length, ok := toInt64(args[2])
	if !ok {
		return nil, fmt.Errorf("ffi.readArray length must be int")
	}
	raw, err := ffi.ReadArray(uintptr(ptr), t, int(length))
	if err != nil {
		return nil, err
	}
	elems := make([]runtime.Value, len(raw))
	for i, v := range raw {
		rv, err := ffiValueToRuntime(t, v)
		if err != nil {
			return nil, fmt.Errorf("ffi.readArray element %d: %w", i, err)
		}
		elems[i] = rv
	}
	return runtime.List{Elements: elems}, nil
}

func (e *Evaluator) ffiBytesView(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ffi.bytesView expects (ptr: int, length: int)")
	}
	ptr, ok := toInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("ffi.bytesView ptr must be int")
	}
	length, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("ffi.bytesView length must be int")
	}
	view, err := ffi.BytesView(uintptr(ptr), int(length))
	if err != nil {
		return nil, err
	}
	return runtime.Bytes{Value: view}, nil
}

func (e *Evaluator) ffiCallback(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("ffi.callback expects (fn: callable, argTypes: list<int>, retType: int)")
	}
	fn, ok := args[0].(runtime.Function)
	if !ok {
		return nil, fmt.Errorf("ffi.callback fn must be a callable")
	}
	argList, ok := args[1].(runtime.List)
	if !ok {
		return nil, fmt.Errorf("ffi.callback argTypes must be list<int>")
	}
	argTypes := make([]ffi.Type, len(argList.Elements))
	for i, elem := range argList.Elements {
		t, err := ffiTypeFromValue(elem)
		if err != nil {
			return nil, fmt.Errorf("ffi.callback argTypes[%d]: %w", i, err)
		}
		argTypes[i] = t
	}
	retType, err := ffiTypeFromValue(args[2])
	if err != nil {
		return nil, fmt.Errorf("ffi.callback retType: %w", err)
	}
	cb := &ffiCallback{fn: fn, argTypes: argTypes, retType: retType, evaluator: e}
	ptr, err := ffi.NewCallback(cb, argTypes, retType)
	if err != nil {
		return nil, err
	}
	cb.cPtr = ptr
	e.ffi.mu.Lock()
	e.ffi.callbacks = append(e.ffi.callbacks, cb)
	e.ffi.mu.Unlock()
	return runtime.NewInt64(int64(ptr)), nil
}

func (e *Evaluator) ffiDlopen(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ffi.dlopen expects (path: string)")
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("ffi.dlopen path must be string")
	}

	e.ffi.mu.Lock()
	policy := e.ffi.policy
	e.ffi.mu.Unlock()
	if err := policy.Allow(path.Value); err != nil {
		return nil, e.wrapFFIPolicyError(err)
	}

	lib, err := ffi.Open(path.Value)
	if err != nil {
		return nil, fmt.Errorf("ffi.dlopen: %w", err)
	}

	e.ffi.mu.Lock()
	e.ffi.nextID++
	id := e.ffi.nextID
	e.ffi.libs[id] = lib
	e.ffi.mu.Unlock()

	return runtime.NewInt64(id), nil
}

func (e *Evaluator) ffiLibByHandle(v runtime.Value) (*ffi.Library, int64, error) {
	id, ok := toInt64(v)
	if !ok {
		return nil, 0, fmt.Errorf("ffi: library handle must be int")
	}
	e.ffi.mu.Lock()
	lib, ok := e.ffi.libs[id]
	e.ffi.mu.Unlock()
	if !ok {
		return nil, 0, fmt.Errorf("ffi: unknown or closed library handle %d", id)
	}
	return lib, id, nil
}

func (e *Evaluator) ffiSymbol(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("ffi.symbol expects (libHandle: int, name: string, argTypes: list<int>, retType: int)")
	}
	lib, _, err := e.ffiLibByHandle(args[0])
	if err != nil {
		return nil, err
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("ffi.symbol name must be string")
	}
	argTypesList, ok := args[2].(runtime.List)
	if !ok {
		return nil, fmt.Errorf("ffi.symbol argTypes must be list<int>")
	}
	argTypes := make([]ffi.Type, len(argTypesList.Elements))
	for i, elem := range argTypesList.Elements {
		t, err := ffiTypeFromValue(elem)
		if err != nil {
			return nil, fmt.Errorf("ffi.symbol argTypes[%d]: %w", i, err)
		}
		argTypes[i] = t
	}
	retType, err := ffiTypeFromValue(args[3])
	if err != nil {
		return nil, fmt.Errorf("ffi.symbol retType: %w", err)
	}

	sym, err := lib.Symbol(name.Value, argTypes, retType)
	if err != nil {
		return nil, fmt.Errorf("ffi.symbol: %w", err)
	}

	return e.makeFFICallable(sym), nil
}

func (e *Evaluator) makeFFICallable(sym *ffi.Symbol) runtime.Value {
	params := make([]ast.Parameter, len(sym.ArgTypes))
	for i := range params {
		params[i] = ast.Parameter{Name: &ast.Identifier{Value: fmt.Sprintf("a%d", i)}}
	}
	return runtime.Function{
		Name:       "ffi." + sym.Name,
		Parameters: params,
		Native: func(_ *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
			if len(args) != len(sym.ArgTypes) {
				return nil, fmt.Errorf("ffi.%s expects %d args, got %d", sym.Name, len(sym.ArgTypes), len(args))
			}
			goArgs := make([]any, len(args))
			for i, a := range args {
				v, err := ffiValueToGo(sym.ArgTypes[i], a)
				if err != nil {
					return nil, fmt.Errorf("ffi.%s arg %d: %w", sym.Name, i, err)
				}
				goArgs[i] = v
			}
			result, err := sym.Call(goArgs)
			if err != nil {
				return nil, err
			}
			return ffiValueToRuntime(sym.RetType, result)
		},
	}
}

func (e *Evaluator) ffiClose(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ffi.close expects (libHandle: int)")
	}
	lib, id, err := e.ffiLibByHandle(args[0])
	if err != nil {
		// Closing an already-closed handle is a no-op for symmetry
		// with libc's free(NULL).
		return runtime.Null{}, nil
	}
	if err := lib.Close(); err != nil {
		return nil, fmt.Errorf("ffi.close: %w", err)
	}
	e.ffi.mu.Lock()
	delete(e.ffi.libs, id)
	e.ffi.mu.Unlock()
	return runtime.Null{}, nil
}

func (e *Evaluator) ffiAlloc(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ffi.alloc expects (bytes: int)")
	}
	n, ok := toInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("ffi.alloc bytes must be int")
	}
	ptr, err := ffi.Alloc(int(n))
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(ptr)), nil
}

func (e *Evaluator) ffiFree(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ffi.free expects (ptr: int)")
	}
	ptr, ok := toInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("ffi.free ptr must be int")
	}
	if err := ffi.Free(uintptr(ptr)); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) ffiReadBytes(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ffi.readBytes expects (ptr: int, n: int)")
	}
	ptr, ok := toInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("ffi.readBytes ptr must be int")
	}
	n, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("ffi.readBytes n must be int")
	}
	data, err := ffi.ReadBytes(uintptr(ptr), int(n))
	if err != nil {
		return nil, err
	}
	return runtime.Bytes{Value: data}, nil
}

func (e *Evaluator) ffiWriteBytes(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ffi.writeBytes expects (ptr: int, data: bytes)")
	}
	ptr, ok := toInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("ffi.writeBytes ptr must be int")
	}
	data, ok := args[1].(runtime.Bytes)
	if !ok {
		return nil, fmt.Errorf("ffi.writeBytes data must be bytes")
	}
	if err := ffi.WriteBytes(uintptr(ptr), data.Value); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) ffiReadCString(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ffi.readCString expects (ptr: int)")
	}
	ptr, ok := toInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("ffi.readCString ptr must be int")
	}
	s, err := ffi.ReadCString(uintptr(ptr))
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: s}, nil
}

func (e *Evaluator) ffiCString(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ffi.cString expects (s: string)")
	}
	s, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("ffi.cString arg must be string")
	}
	ptr, err := ffi.NewCString(s.Value)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(ptr)), nil
}

func (e *Evaluator) ffiErrno(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("ffi.errno expects no arguments")
	}
	n, err := ffi.Errno()
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(n)), nil
}

// wrapFFIPolicyError projects an ffi.PolicyError into a Geblang
// PermissionError throw so user code can catch it specifically.
// Other errors flow through as regular RuntimeError instances built
// from the underlying Go error.
func (e *Evaluator) wrapFFIPolicyError(err error) error {
	if _, ok := err.(*ffi.PolicyError); ok {
		return thrownError{value: runtime.Error{
			Class:   "PermissionError",
			Message: err.Error(),
			Parents: e.errorParentChain("PermissionError"),
		}}
	}
	return err
}

// ffiTypeFromValue extracts an ffi.Type from a Geblang int. The
// integer ordering matches internal/ffi/ffi.go's Type enum; the
// stdlib ffi.gb exports type constants that pin those values.
func ffiTypeFromValue(v runtime.Value) (ffi.Type, error) {
	n, ok := toInt64(v)
	if !ok {
		return 0, fmt.Errorf("FFI type must be int")
	}
	if n < int64(ffi.Void) || n > int64(ffi.Bytes) {
		return 0, fmt.Errorf("unknown FFI type code %d", n)
	}
	return ffi.Type(n), nil
}

// ffiValueToGo converts a Geblang runtime value into the Go-native
// scalar that internal/ffi's Symbol.Call expects.
func ffiValueToGo(t ffi.Type, v runtime.Value) (any, error) {
	switch t {
	case ffi.Int8, ffi.Int16, ffi.Int32, ffi.Int64,
		ffi.Uint8, ffi.Uint16, ffi.Uint32, ffi.Uint64,
		ffi.Ptr:
		n, ok := toInt64(v)
		if !ok {
			return nil, fmt.Errorf("expected int, got %s", v.TypeName())
		}
		return n, nil
	case ffi.Float32, ffi.Float64:
		switch x := v.(type) {
		case runtime.Decimal:
			f, _ := x.Value.Float64()
			return f, nil
		case runtime.SmallInt:
			return float64(x.Value), nil
		case runtime.Int:
			r, _ := new(big.Rat).SetInt(x.Value).Float64()
			return r, nil
		case runtime.Float:
			return x.Value, nil
		}
		return nil, fmt.Errorf("expected decimal or int, got %s", v.TypeName())
	case ffi.CString:
		s, ok := v.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("expected string, got %s", v.TypeName())
		}
		return s.Value, nil
	case ffi.Bytes:
		b, ok := v.(runtime.Bytes)
		if !ok {
			return nil, fmt.Errorf("expected bytes, got %s", v.TypeName())
		}
		return b.Value, nil
	}
	return nil, fmt.Errorf("unsupported FFI type %s", t)
}

func (e *Evaluator) ffiNewStruct(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ffi.newStruct expects (fields: list<[string, int]>)")
	}
	list, ok := args[0].(runtime.List)
	if !ok {
		return nil, fmt.Errorf("ffi.newStruct fields must be list")
	}
	fields := make([]ffi.StructField, 0, len(list.Elements))
	for i, elem := range list.Elements {
		pair, ok := elem.(runtime.List)
		if !ok || len(pair.Elements) != 2 {
			return nil, fmt.Errorf("ffi.newStruct field %d: expected [name, type] pair", i)
		}
		name, ok := pair.Elements[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("ffi.newStruct field %d: name must be string", i)
		}
		t, err := ffiTypeFromValue(pair.Elements[1])
		if err != nil {
			return nil, fmt.Errorf("ffi.newStruct field %d (%q): %w", i, name.Value, err)
		}
		fields = append(fields, ffi.StructField{Name: name.Value, Type: t})
	}
	layout, err := ffi.NewStruct(fields)
	if err != nil {
		return nil, err
	}
	e.ffi.mu.Lock()
	e.ffi.nextStruct++
	id := e.ffi.nextStruct
	e.ffi.structs[id] = layout
	e.ffi.mu.Unlock()
	return runtime.NewInt64(id), nil
}

func (e *Evaluator) ffiStructLayout(v runtime.Value) (*ffi.StructLayout, error) {
	id, ok := toInt64(v)
	if !ok {
		return nil, fmt.Errorf("ffi: struct handle must be int")
	}
	e.ffi.mu.Lock()
	layout, ok := e.ffi.structs[id]
	e.ffi.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("ffi: unknown struct handle %d", id)
	}
	return layout, nil
}

func (e *Evaluator) ffiStructSize(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ffi.structSize expects (handle: int)")
	}
	layout, err := e.ffiStructLayout(args[0])
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(layout.Size)), nil
}

func (e *Evaluator) ffiStructGet(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("ffi.structGet expects (handle: int, ptr: int, name: string)")
	}
	layout, err := e.ffiStructLayout(args[0])
	if err != nil {
		return nil, err
	}
	ptr, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("ffi.structGet ptr must be int")
	}
	name, ok := args[2].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("ffi.structGet name must be string")
	}
	got, err := layout.Get(uintptr(ptr), name.Value)
	if err != nil {
		return nil, err
	}
	_, t, _ := layout.FieldOffset(name.Value)
	return ffiValueToRuntime(t, got)
}

func (e *Evaluator) ffiStructSet(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("ffi.structSet expects (handle: int, ptr: int, name: string, value: any)")
	}
	layout, err := e.ffiStructLayout(args[0])
	if err != nil {
		return nil, err
	}
	ptr, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("ffi.structSet ptr must be int")
	}
	name, ok := args[2].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("ffi.structSet name must be string")
	}
	_, t, ok := layout.FieldOffset(name.Value)
	if !ok {
		return nil, fmt.Errorf("ffi.structSet: unknown field %q", name.Value)
	}
	go_, err := ffiValueToGo(t, args[3])
	if err != nil {
		return nil, fmt.Errorf("ffi.structSet field %q: %w", name.Value, err)
	}
	if err := layout.Set(uintptr(ptr), name.Value, go_); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

// ffiValueToRuntime converts the Go value returned by Symbol.Call
// back into a Geblang runtime value.
func ffiValueToRuntime(t ffi.Type, v any) (runtime.Value, error) {
	switch t {
	case ffi.Void:
		return runtime.Null{}, nil
	case ffi.Int8, ffi.Int16, ffi.Int32, ffi.Int64:
		n, _ := v.(int64)
		return runtime.NewInt64(n), nil
	case ffi.Uint8, ffi.Uint16, ffi.Uint32, ffi.Uint64:
		u, _ := v.(uint64)
		return runtime.NewInt64(int64(u)), nil
	case ffi.Float32, ffi.Float64:
		f, _ := v.(float64)
		return runtime.Decimal{Value: new(big.Rat).SetFloat64(f)}, nil
	case ffi.Ptr:
		switch p := v.(type) {
		case uintptr:
			return runtime.NewInt64(int64(p)), nil
		case int64:
			return runtime.NewInt64(p), nil
		}
		return runtime.NewInt64(0), nil
	case ffi.CString:
		s, _ := v.(string)
		return runtime.String{Value: s}, nil
	case ffi.Bytes:
		return nil, fmt.Errorf("BYTES is not a valid return type")
	}
	return nil, fmt.Errorf("unsupported FFI return type %s", t)
}
