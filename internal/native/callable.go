package native

import (
	"fmt"
	"sync"
	"sync/atomic"

	"geblang/internal/runtime"
)

// CallableInvokerFunc is the callback each backend installs so a shared native
// registry function can call a Geblang callable (closure / lambda / function
// value) back. Sibling of InstanceInvokerFunc.
type CallableInvokerFunc func(callable runtime.Value, args []runtime.Value) (runtime.Value, error)

type callableInvokerCell struct{ fn CallableInvokerFunc }

var (
	// callableInvokerPtr is the process default, used on a goroutine that has no
	// per-goroutine entry (the synchronous main execution path).
	callableInvokerPtr atomic.Pointer[callableInvokerCell]
	// callableInvokers binds a goroutine id to its invoker so a callback fired
	// from a concurrent async task dispatches on that task's own backend, not a
	// shared one (which would race the backend's call-depth and frame state).
	callableInvokers sync.Map
)

// SetCallableInvoker installs the process-default callable invoker.
func SetCallableInvoker(fn CallableInvokerFunc) {
	callableInvokerPtr.Store(&callableInvokerCell{fn: fn})
}

// RegisterCallableInvoker binds fn to the calling goroutine. The caller MUST
// UnregisterCallableInvoker it when the goroutine's work ends (ids are reused
// after a goroutine exits).
func RegisterCallableInvoker(fn CallableInvokerFunc) int64 {
	gid := sysGoroutineID()
	callableInvokers.Store(gid, fn)
	return gid
}

func UnregisterCallableInvoker(gid int64) {
	callableInvokers.Delete(gid)
}

// SwapCallableInvoker registers fn for the calling goroutine and returns the
// previously-registered entry (and whether one existed), so a nested execution
// can RestoreCallableInvoker it on exit. Scopes the invoker to one VM/evaluator
// run without a process-global default that concurrent runs would clobber.
func SwapCallableInvoker(fn CallableInvokerFunc) (CallableInvokerFunc, bool) {
	var prev CallableInvokerFunc
	had := false
	if v, ok := callableInvokers.Load(sysGoroutineID()); ok {
		prev, had = v.(CallableInvokerFunc), true
	}
	callableInvokers.Store(sysGoroutineID(), fn)
	return prev, had
}

func RestoreCallableInvoker(prev CallableInvokerFunc, had bool) {
	if had {
		callableInvokers.Store(sysGoroutineID(), prev)
	} else {
		callableInvokers.Delete(sysGoroutineID())
	}
}

// InvokeCallable dispatches a Geblang callable, preferring the calling
// goroutine's registered invoker (async isolation) and falling back to the
// process default. A non-callable value, or no installed invoker, yields a clear
// error.
func InvokeCallable(callable runtime.Value, args []runtime.Value) (runtime.Value, error) {
	if v, ok := callableInvokers.Load(sysGoroutineID()); ok {
		return v.(CallableInvokerFunc)(callable, args)
	}
	if c := callableInvokerPtr.Load(); c != nil && c.fn != nil {
		return c.fn(callable, args)
	}
	return nil, fmt.Errorf("no callable invoker is installed")
}
