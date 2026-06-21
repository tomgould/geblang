// Package memwatch returns freed heap to the OS on a cadence so a long-running server's RSS does not stay pinned at its burst high-water mark.
package memwatch

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"time"
)

const (
	defaultInterval  = 30 * time.Second
	defaultThreshold = 64 << 20 // 64 MiB
)

var startOnce sync.Once

// Start launches the background sweeper once; no-op when disabled or for a short program that exits before the first tick.
func Start() {
	startOnce.Do(func() {
		if cfg, ok := loadConfig(); ok {
			go run(cfg)
		}
	})
}

type config struct {
	interval  time.Duration
	threshold uint64
}

func loadConfig() (config, bool) {
	switch os.Getenv("GEBLANG_GC") {
	case "off", "0", "false":
		return config{}, false
	}
	c := config{interval: defaultInterval, threshold: defaultThreshold}
	if v := os.Getenv("GEBLANG_GC_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.interval = d
		}
	}
	if v := os.Getenv("GEBLANG_GC_THRESHOLD_MB"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			c.threshold = n << 20
		}
	}
	return c, true
}

func run(c config) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	var last uint64
	var m runtime.MemStats
	for range ticker.C {
		runtime.ReadMemStats(&m)
		if shouldSweep(m, last, c.threshold) {
			debug.FreeOSMemory()
			runtime.ReadMemStats(&m)
		}
		last = m.HeapAlloc
	}
}

// shouldSweep fires after notable heap growth since the last sweep, or when Go holds a notable amount of freed-but-unreturned memory.
func shouldSweep(m runtime.MemStats, lastHeapAlloc, threshold uint64) bool {
	grew := m.HeapAlloc > lastHeapAlloc+threshold
	slack := m.HeapIdle - m.HeapReleased
	return grew || slack > threshold
}
