//go:build !windows

package native

import "github.com/ebitengine/purego"

func dlOpen(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

func dlSym(handle uintptr, name string) (uintptr, error) {
	return purego.Dlsym(handle, name)
}
