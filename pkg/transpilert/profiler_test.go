package transpilert

import "testing"

func TestProfilerSnapshotKeys(t *testing.T) {
	snap := ProfilerSnapshot()
	for _, k := range []string{"wall_ns", "heap_alloc", "peak_alloc", "total_alloc", "num_gc", "cpu_user_ns", "cpu_sys_ns"} {
		if _, ok := snap.Get(k); !ok {
			t.Errorf("snapshot missing key %q", k)
		}
	}
}

func TestProfilerDeltaKeysAndNonNegative(t *testing.T) {
	snap := ProfilerSnapshot()
	d := ProfilerDelta(snap)
	for _, k := range []string{"elapsed_ms", "cpu_ms", "heap_alloc", "allocs", "gc_count"} {
		if _, ok := d.Get(k); !ok {
			t.Errorf("delta missing key %q", k)
		}
	}
	if v, _ := d.Get("elapsed_ms"); v.(float64) < 0 {
		t.Errorf("elapsed_ms negative: %v", v)
	}
}
