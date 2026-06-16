package bytecode_test

// Parity tests run each snippet through both the evaluator and the VM and
// assert that the printed output matches.  They pin the intersection of
// features that both execution paths are expected to support identically.

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/native"
	"geblang/internal/parser"
	"geblang/internal/runtime"
	"geblang/internal/semantic"

	"golang.org/x/crypto/ssh"
)

// stdlibModuleLoader is a minimal bytecode-side module loader used by
// parity tests for source-distributed stdlib modules (time.scheduler,
// async.rate, etc.). It resolves canonical module paths via the standard
// resolver (which walks ancestors to find stdlib/), compiles each
// imported source module to bytecode on demand, and caches the result
// inside the loader struct.

type stdlibModuleLoader struct {
	stdout      io.Writer
	stateful    bytecode.StatefulNativeCaller
	modulePaths []string
	modules     map[string]*runtime.Module
	chunks      map[string]bytecode.Chunk
	globals     map[string][]runtime.Value
	decorators  map[string]bytecode.FunctionDecoratorState
	loading     map[string]bool
	// mainChunk lets sub-VMs (stdlib modules) call back into the
	// entry chunk when a foreign instance's method needs dispatch,
	// e.g. streams.copy invoking __read on a user-defined class.
	mainChunk    bytecode.Chunk
	hasMainChunk bool
}

func newStdlibModuleLoader(stdout io.Writer, stateful bytecode.StatefulNativeCaller) *stdlibModuleLoader {
	return &stdlibModuleLoader{
		stdout:     stdout,
		stateful:   stateful,
		modules:    map[string]*runtime.Module{},
		chunks:     map[string]bytecode.Chunk{},
		globals:    map[string][]runtime.Value{},
		decorators: map[string]bytecode.FunctionDecoratorState{},
		loading:    map[string]bool{},
	}
}

func (l *stdlibModuleLoader) lookupBuiltin(canonical, alias string) *runtime.Module {
	if e, ok := l.stateful.(*evaluator.Evaluator); ok {
		return e.BuiltinModule(canonical, alias)
	}
	return nil
}

func (l *stdlibModuleLoader) LoadModule(canonical, alias string) (*runtime.Module, error) {
	if module, ok := l.modules[canonical]; ok {
		return module, nil
	}
	resolver := modules.NewResolver(l.modulePaths)
	path, err := resolver.Resolve(canonical)
	if err != nil {
		if native := l.lookupBuiltin(canonical, alias); native != nil {
			return native, nil
		}
		return nil, err
	}
	if l.loading[path] {
		if native := l.lookupBuiltin(canonical, alias); native != nil {
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
	if diagnostics := semantic.New().Analyze(program); len(diagnostics) > 0 {
		messages := make([]string, 0, len(diagnostics))
		for _, d := range diagnostics {
			messages = append(messages, d.Message)
		}
		return nil, fmt.Errorf("analyze module %s: %s", canonical, strings.Join(messages, "\n"))
	}
	chunk, err := bytecode.Compile(program, source, canonical)
	if err != nil {
		return nil, fmt.Errorf("compile module %s: %w", canonical, err)
	}
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(canonical)
	vm.SetModulePaths(l.modulePaths)
	if l.stateful != nil {
		vm.SetStatefulNativeCaller(l.stateful)
	}
	if err := vm.Run(); err != nil {
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
	return module, nil
}

// newSubVM returns a VM bound to either a stdlib module's chunk or
// the main chunk (when moduleName == "" and mainChunk is registered).
// Errors when no matching chunk exists.
func (l *stdlibModuleLoader) newSubVM(moduleName string) (*bytecode.VM, error) {
	var chunk bytecode.Chunk
	if moduleName == "" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("entry-script chunk not registered with loader")
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[moduleName]
		if !ok {
			return nil, fmt.Errorf("module %s is not loaded", moduleName)
		}
		chunk = c
	}
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(moduleName)
	vm.SetModulePaths(l.modulePaths)
	if l.stateful != nil {
		vm.SetStatefulNativeCaller(l.stateful)
	}
	vm.RestoreGlobals(l.globals[moduleName])
	vm.RestoreFunctionDecoratorState(l.decorators[moduleName])
	return vm, nil
}

func (l *stdlibModuleLoader) CallModuleFunction(function runtime.BytecodeFunction, args []runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(function.Module)
	if err != nil {
		return nil, err
	}
	return vm.CallFunction(function.Index, args)
}

func (l *stdlibModuleLoader) CallModuleClosure(closure runtime.BytecodeClosure, args []runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(closure.Module)
	if err != nil {
		return nil, err
	}
	return vm.CallClosure(closure, args)
}

func (l *stdlibModuleLoader) ConstructModuleClass(class runtime.BytecodeClass, args []runtime.Value, typeArgs []string) (runtime.Value, error) {
	vm, err := l.newSubVM(class.Module)
	if err != nil {
		return nil, err
	}
	return vm.ConstructClassWithTypeArgs(class.Index, args, typeArgs)
}

func (l *stdlibModuleLoader) DeserializeModuleClass(class runtime.BytecodeClass, value runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(class.Module)
	if err != nil {
		return nil, err
	}
	return vm.DeserializeIntoChunkClass(class, value)
}

func (l *stdlibModuleLoader) ConstructorsForModuleClass(class runtime.BytecodeClass) (runtime.Value, error) {
	vm, err := l.newSubVM(class.Module)
	if err != nil {
		return nil, err
	}
	return vm.ReflectConstructorsForChunkClass(class)
}

func (l *stdlibModuleLoader) FieldsForModuleClass(class runtime.BytecodeClass) (runtime.Value, error) {
	if class.Module == "" || class.Module == "parity" {
		if !l.hasMainChunk {
			return nil, fmt.Errorf("reflect.fields %s: main chunk not registered in test loader", class.Name)
		}
		vm := bytecode.NewVMWithModuleLoader(l.mainChunk, l.stdout, l)
		vm.SetModuleName(class.Module)
		vm.SetModulePaths(l.modulePaths)
		if l.stateful != nil {
			vm.SetStatefulNativeCaller(l.stateful)
		}
		return vm.ReflectFieldsForChunkClass(class)
	}
	vm, err := l.newSubVM(class.Module)
	if err != nil {
		return nil, err
	}
	return vm.ReflectFieldsForChunkClass(class)
}

func (l *stdlibModuleLoader) registerMainChunk(chunk bytecode.Chunk) {
	l.mainChunk = chunk
	l.hasMainChunk = true
}

func (l *stdlibModuleLoader) CallModuleStaticMethod(class runtime.BytecodeClass, methodName string, args []runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(class.Module)
	if err != nil {
		return nil, err
	}
	return vm.CallStaticMethod(class.Index, methodName, args)
}

func (l *stdlibModuleLoader) CallModuleMethod(module string, className string, methodName string, instance *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if _, ok := l.chunks[module]; !ok && module != "" && native.IsNativeModule(module) {
		return nil, &runtime.MethodNotFoundError{Class: className, Method: methodName}
	}
	vm, err := l.newSubVM(module)
	if err != nil {
		return nil, err
	}
	return vm.CallInstanceMethod(instance, methodName, args)
}

func (l *stdlibModuleLoader) ModuleMethodParamNames(module string, className string, methodName string) ([]string, error) {
	chunk, ok := l.chunks[module]
	if !ok {
		return nil, fmt.Errorf("module %s is not loaded", module)
	}
	return chunk.MethodParamNames(className, methodName)
}

func (l *stdlibModuleLoader) CallParentInModule(module string, className string, methodName string, instance *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	vm, err := l.newSubVM(module)
	if err != nil {
		return nil, err
	}
	return vm.CallMethodAs(className, instance, methodName, args)
}

func (l *stdlibModuleLoader) ImmutableFieldsForModuleClass(module string, className string) []string {
	chunk, ok := l.chunks[module]
	if !ok {
		return nil
	}
	for i := range chunk.Classes {
		if chunk.Classes[i].Name == className {
			return chunk.Classes[i].ImmutableFields
		}
	}
	return nil
}

func (l *stdlibModuleLoader) ModuleClassDescendsFrom(module, className, targetSimpleName string) bool {
	var chunk bytecode.Chunk
	if module == "" {
		if !l.hasMainChunk {
			return false
		}
		chunk = l.mainChunk
	} else {
		c, ok := l.chunks[module]
		if !ok {
			return false
		}
		chunk = c
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

func (l *stdlibModuleLoader) chunkForTest(module string) (bytecode.Chunk, bool) {
	if module == "" {
		if !l.hasMainChunk {
			return bytecode.Chunk{}, false
		}
		return l.mainChunk, true
	}
	c, ok := l.chunks[module]
	return c, ok
}

func (l *stdlibModuleLoader) StaticValueForModuleClass(module, className, name string) (runtime.Value, bool) {
	chunk, ok := l.chunkForTest(module)
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

func (l *stdlibModuleLoader) CallModuleStaticMethodByName(module, className, methodName string, args []runtime.Value) (runtime.Value, bool, error) {
	chunk, ok := l.chunkForTest(module)
	if !ok {
		return nil, false, nil
	}
	for i := range chunk.Classes {
		if !strings.EqualFold(chunk.Classes[i].Name, className) {
			continue
		}
		for ci := chunk.Classes[i]; ; {
			if _, present := ci.StaticMethods[strings.ToLower(methodName)]; present {
				vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
				vm.SetModuleName(module)
				vm.SetModulePaths(l.modulePaths)
				vm.SetStatefulNativeCaller(l.stateful)
				vm.RestoreGlobals(l.globals[module])
				vm.RestoreFunctionDecoratorState(l.decorators[module])
				result, err := vm.CallStaticMethod(int64(i), methodName, args)
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

func (l *stdlibModuleLoader) UnimplementedAbstractMethods(module, className string) map[string]string {
	chunk, ok := l.chunkForTest(module)
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

func (l *stdlibModuleLoader) ListAllClasses() []runtime.Value {
	out := []runtime.Value{}
	appendChunkClasses := func(module string, chunk bytecode.Chunk) {
		for i, classInfo := range chunk.Classes {
			out = append(out, runtime.BytecodeClass{
				Name: classInfo.Name, Index: int64(i), Module: module,
				Decorators:       classInfo.Decorators,
				MethodDecorators: classInfo.MethodDecorators,
			})
		}
	}
	for module, chunk := range l.chunks {
		appendChunkClasses(module, chunk)
	}
	if l.hasMainChunk {
		appendChunkClasses("", l.mainChunk)
	}
	return out
}

func (l *stdlibModuleLoader) LookupModuleInterface(module, name string) (bytecode.InterfaceInfo, bool) {
	chunk, ok := l.chunks[module]
	if !ok {
		return bytecode.InterfaceInfo{}, false
	}
	for _, iface := range chunk.Interfaces {
		if strings.EqualFold(iface.Name, name) {
			return iface, true
		}
	}
	return bytecode.InterfaceInfo{}, false
}

func (l *stdlibModuleLoader) FindFunctionByName(name string) (runtime.Value, bool) {
	for _, module := range l.modules {
		if module == nil {
			continue
		}
		if v, ok := module.Exports[name]; ok {
			switch v := v.(type) {
			case runtime.Function, runtime.OverloadedFunction, runtime.BytecodeFunction, runtime.DecoratorTarget:
				return v, true
			default:
				_ = v
			}
		}
	}
	return nil, false
}

func (l *stdlibModuleLoader) FindClassByName(name string) (runtime.Value, bool) {
	chunks := map[string]bytecode.Chunk{}
	for module, chunk := range l.chunks {
		chunks[module] = chunk
	}
	// Mirror the production loader: the main program's classes are also
	// resolvable by name (a sub-module reflecting a main-chunk class).
	if l.hasMainChunk {
		chunks[""] = l.mainChunk
	}
	for module, chunk := range chunks {
		for idx, classInfo := range chunk.Classes {
			if classInfo.Name == name {
				return runtime.BytecodeClass{
					Name:             classInfo.Name,
					Doc:              classInfo.Doc,
					Index:            int64(idx),
					Module:           module,
					Parent:           classInfo.ParentName,
					Fields:           append([]string(nil), classInfo.FieldNames...),
					Interfaces:       append([]string(nil), classInfo.Implements...),
					Decorators:       classInfo.Decorators,
					MethodDecorators: classInfo.MethodDecorators,
					StaticDecorators: classInfo.StaticDecorators,
				}, true
			}
		}
	}
	return nil, false
}

// runParityWithStdlib is like runParityStateful but additionally wires
// a bytecode-side module loader so source-distributed stdlib modules
// (time.scheduler, async.rate, ...) can be imported in the test source.
func runParityWithStdlib(t *testing.T, source string, want string) {
	t.Helper()

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	if _, err := evaluator.New(&evOut).Eval(program); err != nil {
		t.Fatalf("evaluator error: %v", err)
	}

	src := []byte(source)
	chunk, err := bytecode.Compile(program, src, "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	stateful := evaluator.New(&vmOut)
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.mainChunk = chunk
	loader.hasMainChunk = true
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetStatefulNativeCaller(stateful)
	/* Without the dispatcher wiring, the evaluator can't reach
	 * back into the VM for test.mock + RunTestClass. Production
	 * (cmd/geblang/main.go) sets this; the parity harness
	 * mirrors that so feature-parity coverage is honest. */
	stateful.SetMethodDispatcher(vm)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}

	if evOut.String() != vmOut.String() {
		t.Errorf("output mismatch:\n  evaluator: %q\n  vm:        %q", evOut.String(), vmOut.String())
	}
	if want != "" && evOut.String() != want {
		t.Errorf("wrong output: got %q, want %q", evOut.String(), want)
	}
}

// runParity compiles and runs source through both the evaluator and the VM,
// then checks that their outputs agree.  If want is non-empty it also asserts
// the exact expected output.
// runErrorParity runs source through both the evaluator and VM, expects both
// to produce errors, and checks that each error contains all substrings in
// wantSubstrings.  Use this for error-path tests where the two engines format
// messages differently but must include the same key information.
func runErrorParity(t *testing.T, source string, wantSubstrings ...string) {
	t.Helper()

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	_, evErr := evaluator.New(&evOut).Eval(program)
	if evErr == nil {
		t.Fatal("evaluator: expected error, got nil")
	}

	src := []byte(source)
	chunk, err := bytecode.Compile(program, src, "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	vmErr := bytecode.NewVM(chunk, &vmOut).Run()
	if vmErr == nil {
		t.Fatal("vm: expected error, got nil")
	}

	for _, sub := range wantSubstrings {
		if !strings.Contains(evErr.Error(), sub) {
			t.Errorf("evaluator error missing %q: %v", sub, evErr)
		}
		if !strings.Contains(vmErr.Error(), sub) {
			t.Errorf("vm error missing %q: %v", sub, vmErr)
		}
	}
}

func runParity(t *testing.T, source string, want string) {
	t.Helper()

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	if _, err := evaluator.New(&evOut).Eval(program); err != nil {
		t.Fatalf("evaluator error: %v", err)
	}

	src := []byte(source)
	chunk, err := bytecode.Compile(program, src, "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	if err := bytecode.NewVM(chunk, &vmOut).Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}

	if evOut.String() != vmOut.String() {
		t.Errorf("output mismatch:\n  evaluator: %q\n  vm:        %q", evOut.String(), vmOut.String())
	}
	if want != "" && evOut.String() != want {
		t.Errorf("wrong output: got %q, want %q", evOut.String(), want)
	}
}

// runParityStateful is like runParity but wires an evaluator as the VM's
// StatefulNativeCaller so modules like schema and serde work in both paths.
func runParityStateful(t *testing.T, source string, want string) {
	t.Helper()

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	if _, err := evaluator.New(&evOut).Eval(program); err != nil {
		t.Fatalf("evaluator error: %v", err)
	}

	src := []byte(source)
	chunk, err := bytecode.Compile(program, src, "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	vm := bytecode.NewVM(chunk, &vmOut)
	vm.SetStatefulNativeCaller(evaluator.New(&vmOut))
	if err := vm.Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}

	if evOut.String() != vmOut.String() {
		t.Errorf("output mismatch:\n  evaluator: %q\n  vm:        %q", evOut.String(), vmOut.String())
	}
	if want != "" && evOut.String() != want {
		t.Errorf("wrong output: got %q, want %q", evOut.String(), want)
	}
}

// runParityStatefulWithFile writes fileContent to a temp file, substitutes
// its path into source (replacing the placeholder "TMPFILE"), then runs
// through both evaluator and VM in stateful mode.
func runParityStatefulWithFile(t *testing.T, source string, fileContent string, want string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "parity_*.txt")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(fileContent); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	source = strings.ReplaceAll(source, "TMPFILE", f.Name())
	runParityStateful(t, source, want)
}

// sshTestServer runs an in-process SSH server for hermetic SSH
// parity tests. The server accepts password "secret" for any
// username, signs an ephemeral RSA host key, and handles "exec"
// requests by running a tiny set of canned commands in a
// goroutine. Returns (addr, hostKeyPath, stopFn). The
// hostKeyPath is a temporary file containing the server's host
// key in known_hosts format so the test can verify without
// relying on insecureSkipHostKey.
type sshTestServer struct {
	listener net.Listener
	hostKey  ssh.Signer
	wg       sync.WaitGroup
	stopCh   chan struct{}
}

func startSSHTestServer(t *testing.T) *sshTestServer {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("ssh server rsa key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatalf("ssh server signer: %v", err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected")
		},
	}
	cfg.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ssh server listen: %v", err)
	}
	srv := &sshTestServer{listener: listener, hostKey: signer, stopCh: make(chan struct{})}
	srv.wg.Add(1)
	go srv.acceptLoop(cfg)
	return srv
}

func (s *sshTestServer) acceptLoop(cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn, cfg)
	}
}

func (s *sshTestServer) handleConn(rawConn net.Conn, cfg *ssh.ServerConfig) {
	defer rawConn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(rawConn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		ch, reqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(ch, reqs)
	}
}

func (s *sshTestServer) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "exec":
			cmd := string(req.Payload[4:])
			_ = req.Reply(true, nil)
			s.runCommand(ch, cmd)
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			return
		case "shell":
			_ = req.Reply(true, nil)
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *sshTestServer) runCommand(ch ssh.Channel, cmd string) {
	// Tiny canned responses so the test doesn't depend on a real
	// shell. The expectations stay readable in the parity test
	// source.
	switch {
	case cmd == "echo hello":
		_, _ = ch.Write([]byte("hello\n"))
	case cmd == "echo err 1>&2":
		_, _ = ch.Stderr().Write([]byte("err\n"))
	case strings.HasPrefix(cmd, "cat"):
		// Stream stdin to stdout so spawn/stdin tests can pipe.
		_, _ = io.Copy(ch, ch)
	}
}

func (s *sshTestServer) addr() string {
	return s.listener.Addr().String()
}

func (s *sshTestServer) port() string {
	_, port, _ := net.SplitHostPort(s.addr())
	return port
}

func (s *sshTestServer) stop() {
	_ = s.listener.Close()
	s.wg.Wait()
}

// reflect.classes() from an imported module must see entry-file classes on both backends.
func TestParityReflectClassesCrossModule(t *testing.T) {
	dir := t.TempDir()
	scan := "module scan;\n" +
		"import reflect;\n" +
		"export func sawEntry(string name): bool {\n" +
		"    for (c in reflect.classes()) {\n" +
		"        if (reflect.className(c) == name) { return true; }\n" +
		"    }\n" +
		"    return false;\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "scan.gb"), []byte(scan), 0o644); err != nil {
		t.Fatalf("write scan module: %v", err)
	}
	source := "import io;\n" +
		"import scan;\n" +
		"class EntryWidget { func EntryWidget() {} }\n" +
		"io.println(scan.sawEntry(\"EntryWidget\"));\n"

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("evaluator error: %v", err)
	}

	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	stateful := evaluator.NewWithArgsAndModulePaths(&vmOut, nil, []string{dir})
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	loader.registerMainChunk(chunk)
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}

	if evOut.String() != vmOut.String() {
		t.Errorf("reflect.classes() cross-module divergence:\n  evaluator: %q\n  vm:        %q", evOut.String(), vmOut.String())
	}
	if evOut.String() != "true\n" {
		t.Errorf("entry class should be visible from an imported module; got eval=%q vm=%q", evOut.String(), vmOut.String())
	}
}

// assertImportedModuleRejected writes moduleBody as module `lib`, imports it
// from a main program, and asserts both backends fail to load it.
func assertImportedModuleRejected(t *testing.T, moduleBody string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.gb"), []byte("module lib;\n"+moduleBody), 0o644); err != nil {
		t.Fatalf("write lib: %v", err)
	}
	source := "import io;\nimport lib;\nio.println(\"loaded\");\n"
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	var evOut bytes.Buffer
	ev := evaluator.NewWithArgsAndModulePaths(&evOut, nil, []string{dir})
	if _, err := ev.Eval(program); err == nil {
		t.Fatalf("evaluator accepted a colliding module: %q", evOut.String())
	}
	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("main compile: %v", err)
	}
	var vmOut bytes.Buffer
	stateful := evaluator.NewWithArgsAndModulePaths(&vmOut, nil, []string{dir})
	loader := newStdlibModuleLoader(&vmOut, stateful)
	loader.modulePaths = []string{dir}
	vm := bytecode.NewVMWithModuleLoader(chunk, &vmOut, loader)
	vm.SetModulePaths([]string{dir})
	vm.SetStatefulNativeCaller(stateful)
	if err := vm.Run(); err == nil {
		t.Fatalf("vm accepted a colliding module: %q", vmOut.String())
	}
}
