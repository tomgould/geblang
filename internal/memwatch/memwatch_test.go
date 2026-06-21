package memwatch

import (
	"runtime"
	"testing"
	"time"
)

func TestShouldSweep(t *testing.T) {
	const th = 64 << 20
	cases := []struct {
		name string
		m    runtime.MemStats
		last uint64
		want bool
	}{
		{"stable, no slack", runtime.MemStats{HeapAlloc: 10 << 20, HeapIdle: 5 << 20, HeapReleased: 5 << 20}, 10 << 20, false},
		{"grew past threshold", runtime.MemStats{HeapAlloc: 200 << 20}, 10 << 20, true},
		{"unreturned slack past threshold", runtime.MemStats{HeapAlloc: 10 << 20, HeapIdle: 100 << 20, HeapReleased: 10 << 20}, 10 << 20, true},
		{"small slack stays", runtime.MemStats{HeapAlloc: 10 << 20, HeapIdle: 12 << 20, HeapReleased: 10 << 20}, 10 << 20, false},
	}
	for _, c := range cases {
		if got := shouldSweep(c.m, c.last, th); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("GEBLANG_GC", "off")
	if _, ok := loadConfig(); ok {
		t.Error("GEBLANG_GC=off should disable the sweeper")
	}

	t.Setenv("GEBLANG_GC", "")
	t.Setenv("GEBLANG_GC_INTERVAL", "5s")
	t.Setenv("GEBLANG_GC_THRESHOLD_MB", "128")
	c, ok := loadConfig()
	if !ok {
		t.Fatal("default should be enabled")
	}
	if c.interval != 5*time.Second {
		t.Errorf("interval: got %v, want 5s", c.interval)
	}
	if c.threshold != 128<<20 {
		t.Errorf("threshold: got %d, want %d", c.threshold, 128<<20)
	}
}
