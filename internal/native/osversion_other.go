//go:build !linux && !darwin

package native

import (
	"fmt"
	"runtime"
)

func sysOSVersion() (string, error) {
	return "", fmt.Errorf("sys.osVersion is unsupported on %s", runtime.GOOS)
}
