package native

import "sync/atomic"

// Process-global ONNX capability, mirroring the FFI / process-control gates; set from --allow-onnx. Default-deny since local inference dlopen's a native shared library.
var onnxEnabled atomic.Bool

func SetOnnxEnabled(on bool) { onnxEnabled.Store(on) }

func RequireOnnx(op string) error {
	if onnxEnabled.Load() {
		return nil
	}
	return &permissionError{msg: op + " requires the --allow-onnx launch flag"}
}
