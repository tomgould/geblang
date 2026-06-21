package native

import "sync/atomic"

// Process-global headless-browser capability, mirroring the onnx / ffi gates; set from --allow-browser. Default-deny since it launches a browser subprocess and opens sockets.
var browserEnabled atomic.Bool

func SetBrowserEnabled(on bool) { browserEnabled.Store(on) }

func RequireBrowser(op string) error {
	if browserEnabled.Load() {
		return nil
	}
	return &permissionError{msg: op + " requires the --allow-browser launch flag"}
}
