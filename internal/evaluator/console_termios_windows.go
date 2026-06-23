//go:build windows

package evaluator

import "errors"

// Terminal raw mode and echo control are unix-only; callers fall back to line input on Windows.
var errNoTermios = errors.New("terminal mode control is not supported on Windows")

func enterRawMode(fd int) (func(), error) { return nil, errNoTermios }

func disableEcho(fd int) (func(), error) { return nil, errNoTermios }
