//go:build windows

package native

func profilerCPUNanos() (userNS, sysNS int64) { return 0, 0 }
