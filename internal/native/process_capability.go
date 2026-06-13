package native

import "sync/atomic"

// Process-global so both backends consult one capability, mirroring the
// FFI gate; set from the CLI --allow-process-control flag before a run.
var processControlEnabled atomic.Bool

func SetProcessControlEnabled(on bool) { processControlEnabled.Store(on) }

// permissionError surfaces as a catchable Geblang PermissionError via
// the runtime.TypedError contract.
type permissionError struct{ msg string }

func (e *permissionError) Error() string      { return e.msg }
func (e *permissionError) ErrorClass() string { return "PermissionError" }

func requireProcessControl(op string) error {
	if processControlEnabled.Load() {
		return nil
	}
	return &permissionError{msg: op + " requires the --allow-process-control launch flag"}
}
