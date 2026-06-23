//go:build windows

package main

import (
	"errors"
	"os"
)

// The raw-mode line editor is unix-only; on Windows isTerminal is false, so the REPL uses the plain scanner reader.
var errNoReplRawMode = errors.New("terminal raw mode is not supported on Windows")

func enterReplRawMode(fd int) (func(), error) {
	return nil, errNoReplRawMode
}

func isTerminal(file *os.File) bool {
	return false
}

func terminalColumns(fd int) int {
	return 80
}
