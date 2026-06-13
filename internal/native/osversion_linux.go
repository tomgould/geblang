//go:build linux

package native

import (
	"fmt"
	"syscall"
)

func sysOSVersion() (string, error) {
	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err != nil {
		return "", fmt.Errorf("sys.osVersion: %v", err)
	}
	return int8ToString(uname.Release[:]), nil
}

func int8ToString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}
