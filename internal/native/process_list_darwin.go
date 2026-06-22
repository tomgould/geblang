//go:build darwin

package native

import "golang.org/x/sys/unix"

func procList() ([]processInfo, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	out := make([]processInfo, 0, len(procs))
	for i := range procs {
		out = append(out, kinfoToProcessInfo(&procs[i]))
	}
	return out, nil
}

func procInfo(pid int) (processInfo, bool, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return processInfo{}, false, err
	}
	if len(procs) == 0 {
		return processInfo{}, false, nil
	}
	return kinfoToProcessInfo(&procs[0]), true, nil
}

func kinfoToProcessInfo(kp *unix.KinfoProc) processInfo {
	return processInfo{
		pid:   int(kp.Proc.P_pid),
		ppid:  int(kp.Eproc.Ppid),
		name:  nulTermString(kp.Proc.P_comm[:]),
		state: darwinProcState(kp.Proc.P_stat),
	}
}

// darwinProcState maps the BSD p_stat code to a single-letter state.
func darwinProcState(s int8) string {
	switch s {
	case 1:
		return "I"
	case 2:
		return "R"
	case 3:
		return "S"
	case 4:
		return "T"
	case 5:
		return "Z"
	}
	return "?"
}

func nulTermString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
