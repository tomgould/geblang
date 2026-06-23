//go:build windows

package native

import "errors"

// Loading native shared libraries (ONNX Runtime) via dlopen is unix-only; Windows reports a clear error.
var errDlopenWindows = errors.New("loading native shared libraries is not supported on Windows")

func dlOpen(path string) (uintptr, error) {
	return 0, errDlopenWindows
}

func dlSym(handle uintptr, name string) (uintptr, error) {
	return 0, errDlopenWindows
}
