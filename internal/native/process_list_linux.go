//go:build linux

package native

import (
	"os"
	"strconv"
	"strings"
)

func procList() ([]processInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	out := make([]processInfo, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if info, ok := readProcInfo(pid); ok {
			out = append(out, info)
		}
	}
	return out, nil
}

func procInfo(pid int) (processInfo, bool, error) {
	info, ok := readProcInfo(pid)
	return info, ok, nil
}

func readProcInfo(pid int) (processInfo, bool) {
	statBytes, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return processInfo{}, false
	}
	info := processInfo{pid: pid}
	stat := string(statBytes)
	// comm may contain spaces/parens, so parse fields after the final ')'.
	if close := strings.LastIndexByte(stat, ')'); close >= 0 && close+2 < len(stat) {
		fields := strings.Fields(stat[close+1:])
		if len(fields) >= 1 {
			info.state = fields[0]
		}
		if len(fields) >= 2 {
			info.ppid, _ = strconv.Atoi(fields[1])
		}
	}
	if comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm"); err == nil {
		info.name = strings.TrimRight(string(comm), "\n")
	}
	if cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil {
		info.cmdline = strings.TrimRight(strings.ReplaceAll(string(cmdline), "\x00", " "), " ")
	}
	return info, true
}
