package runtime

import (
	"errors"
	"net"
	"os"
)

func NewRecoverableError(err error) Error {
	return Error{Class: RecoverableErrorClass(err), Message: err.Error()}
}

func RecoverableErrorClass(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return "IOError"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "IOError"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return "IOError"
	}
	return "RuntimeError"
}
