//go:build windows

package evaluator

import "errors"

// Advisory file locking (flock) is unix-only; Windows reports a clear error.
var errNoFlock = errors.New("file locking is not supported on Windows")

func lockFile(fd uintptr, exclusive, nonblocking bool) (bool, error) {
	return false, errNoFlock
}

func unlockFile(fd uintptr) error {
	return errNoFlock
}
