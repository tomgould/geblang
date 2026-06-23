//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func enterReplRawMode(fd int) (func(), error) {
	original, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, err
	}
	raw := *original
	raw.Iflag &^= unix.ICRNL | unix.IXON
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &raw); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, original) }, nil
}

func isTerminal(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), ioctlGetTermios)
	return err == nil
}

func terminalColumns(fd int) int {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil || ws == nil || ws.Col == 0 {
		return 80
	}
	return int(ws.Col)
}
