//go:build windows

package ffi

import "errors"

// Windows has no dlopen; FFI reports a clear error here instead of failing the build.
var errFFIWindows = errors.New("FFI is not supported on Windows")

func dlOpen(path string) (uintptr, error) {
	return 0, errFFIWindows
}

func dlSym(handle uintptr, name string) (uintptr, error) {
	return 0, errFFIWindows
}

func dlClose(handle uintptr) error {
	return errFFIWindows
}
