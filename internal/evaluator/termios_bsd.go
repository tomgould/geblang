//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package evaluator

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
