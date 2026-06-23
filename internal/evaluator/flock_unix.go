//go:build !windows

package evaluator

import "syscall"

func lockFile(fd uintptr, exclusive, nonblocking bool) (bool, error) {
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if nonblocking {
		mode |= syscall.LOCK_NB
	}
	err := syscall.Flock(int(fd), mode)
	if err == nil {
		return true, nil
	}
	if nonblocking && (err == syscall.EWOULDBLOCK || err == syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unlockFile(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_UN)
}
