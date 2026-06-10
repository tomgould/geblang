//go:build unix

package evaluator

import (
	"os"
	"syscall"
)

func platformSignalByName(name string) (os.Signal, bool) {
	switch name {
	case "USR1":
		return syscall.SIGUSR1, true
	case "USR2":
		return syscall.SIGUSR2, true
	default:
		return nil, false
	}
}
