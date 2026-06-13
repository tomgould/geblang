//go:build !unix

package native

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

func unsupportedOnPlatform(op string) error {
	return fmt.Errorf("%s is unsupported on %s", op, runtime.GOOS)
}

func procPpid() int { return os.Getppid() }

func procUID() (int, error)  { return 0, unsupportedOnPlatform("process.uid") }
func procGID() (int, error)  { return 0, unsupportedOnPlatform("process.gid") }
func procEUID() (int, error) { return 0, unsupportedOnPlatform("process.euid") }
func procEGID() (int, error) { return 0, unsupportedOnPlatform("process.egid") }

func procGroups() ([]int, error) { return nil, unsupportedOnPlatform("process.groups") }

func procSetuid(int) error { return unsupportedOnPlatform("process.setuid") }
func procSetgid(int) error { return unsupportedOnPlatform("process.setgid") }

func procExists(pid int) (bool, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, nil
	}
	return proc != nil, nil
}

func procSignal(pid int, name string) error {
	if strings.TrimPrefix(strings.ToUpper(name), "SIG") != "KILL" {
		return fmt.Errorf("process.signal: signal %q is unsupported on %s", name, runtime.GOOS)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process.signal: %v", err)
	}
	if err := proc.Kill(); err != nil {
		return fmt.Errorf("process.signal: %v", err)
	}
	return nil
}
