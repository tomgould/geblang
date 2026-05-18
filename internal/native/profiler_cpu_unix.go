//go:build linux || darwin || freebsd || openbsd || netbsd

package native

import "syscall"

func profilerCPUNanos() (userNS, sysNS int64) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0
	}
	toNS := func(tv syscall.Timeval) int64 {
		return tv.Sec*1e9 + int64(tv.Usec)*1e3
	}
	return toNS(ru.Utime), toNS(ru.Stime)
}
