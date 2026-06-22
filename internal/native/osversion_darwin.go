//go:build darwin

package native

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func sysOSVersion() (string, error) {
	var uname unix.Utsname
	if err := unix.Uname(&uname); err != nil {
		return "", fmt.Errorf("sys.osVersion: %v", err)
	}
	return nulTermString(uname.Release[:]), nil
}
