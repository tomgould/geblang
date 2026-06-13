//go:build unix

package native

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

func procPpid() int { return os.Getppid() }

func procUID() (int, error)  { return os.Getuid(), nil }
func procGID() (int, error)  { return os.Getgid(), nil }
func procEUID() (int, error) { return os.Geteuid(), nil }
func procEGID() (int, error) { return os.Getegid(), nil }

func procGroups() ([]int, error) { return os.Getgroups() }

func procSetuid(uid int) error {
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("process.setuid: %v", err)
	}
	return nil
}

func procSetgid(gid int) error {
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("process.setgid: %v", err)
	}
	return nil
}

// Signal 0 checks existence/permission without delivering a signal.
func procExists(pid int) (bool, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, nil
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	if err == os.ErrProcessDone || err == syscall.ESRCH {
		return false, nil
	}
	if err == syscall.EPERM {
		return true, nil
	}
	return false, nil
}

func procSignal(pid int, name string) error {
	sig, err := procSignalByName(name)
	if err != nil {
		return err
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process.signal: %v", err)
	}
	if err := proc.Signal(sig); err != nil {
		return fmt.Errorf("process.signal: %v", err)
	}
	return nil
}

func procSignalByName(name string) (os.Signal, error) {
	switch strings.TrimPrefix(strings.ToUpper(name), "SIG") {
	case "TERM":
		return syscall.SIGTERM, nil
	case "KILL":
		return syscall.SIGKILL, nil
	case "INT":
		return syscall.SIGINT, nil
	case "HUP":
		return syscall.SIGHUP, nil
	case "QUIT":
		return syscall.SIGQUIT, nil
	case "USR1":
		return syscall.SIGUSR1, nil
	case "USR2":
		return syscall.SIGUSR2, nil
	case "STOP":
		return syscall.SIGSTOP, nil
	case "CONT":
		return syscall.SIGCONT, nil
	default:
		return nil, fmt.Errorf("process.signal: unsupported signal %q", name)
	}
}
