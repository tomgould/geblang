//go:build !linux && !darwin

package native

import (
	"fmt"
	"runtime"
)

func procList() ([]processInfo, error) {
	return nil, fmt.Errorf("process.list is unsupported on %s", runtime.GOOS)
}

func procInfo(int) (processInfo, bool, error) {
	return processInfo{}, false, fmt.Errorf("process.info is unsupported on %s", runtime.GOOS)
}
