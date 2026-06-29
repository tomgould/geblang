package native

import "geblang/internal/runtime"

// CallableInvokerFunc lets a shared native function call a Geblang callable back; backends pass their own at the call site (see DataFrameMethod), so there is no goroutine-keyed registry.
type CallableInvokerFunc func(callable runtime.Value, args []runtime.Value) (runtime.Value, error)
