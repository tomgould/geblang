package transpilert

import (
	goruntime "runtime"
	"sync/atomic"
	"time"
)

// Typed adapters for the native profiler module (snapshot/delta), measuring
// wall time, CPU, heap, allocations, and GC across a bracketed block. Field
// names and arithmetic mirror the interpreter; CPU is unavailable through the
// Go stdlib alone, so cpu_* readings are zero (the interpreter reads getrusage
// via cgo, which transpilert avoids to stay pure-stdlib).

var profilerPeakAlloc int64

func profilerUpdatePeak(current int64) {
	for {
		old := atomic.LoadInt64(&profilerPeakAlloc)
		if current <= old {
			return
		}
		if atomic.CompareAndSwapInt64(&profilerPeakAlloc, old, current) {
			return
		}
	}
}

// ProfilerSnapshot captures memory and timing counters at a point in time.
func ProfilerSnapshot() *OrderedDict[string, any] {
	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)
	profilerUpdatePeak(int64(ms.HeapAlloc))
	d := NewOrderedDict[string, any]()
	d.Set("wall_ns", time.Now().UnixNano())
	d.Set("heap_alloc", int64(ms.HeapAlloc))
	d.Set("peak_alloc", atomic.LoadInt64(&profilerPeakAlloc))
	d.Set("total_alloc", int64(ms.TotalAlloc))
	d.Set("num_gc", int64(ms.NumGC))
	d.Set("cpu_user_ns", int64(0))
	d.Set("cpu_sys_ns", int64(0))
	return d
}

// ProfilerDelta computes the change between snap and now.
func ProfilerDelta(snap any) *OrderedDict[string, any] {
	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)
	nowNS := time.Now().UnixNano()

	snapWall := profilerSnapInt(snap, "wall_ns")
	snapHeap := profilerSnapInt(snap, "heap_alloc")
	snapTotal := profilerSnapInt(snap, "total_alloc")
	snapGC := profilerSnapInt(snap, "num_gc")
	snapUser := profilerSnapInt(snap, "cpu_user_ns")
	snapSys := profilerSnapInt(snap, "cpu_sys_ns")

	elapsedNS := nowNS - snapWall
	cpuDelta := int64(0) - (snapUser + snapSys)
	d := NewOrderedDict[string, any]()
	d.Set("elapsed_ms", float64(elapsedNS)/1e6)
	d.Set("cpu_ms", float64(cpuDelta)/1e6)
	d.Set("heap_alloc", int64(ms.HeapAlloc)-snapHeap)
	d.Set("allocs", int64(ms.TotalAlloc)-snapTotal)
	d.Set("gc_count", int64(ms.NumGC)-snapGC)
	return d
}

func profilerSnapInt(snap any, key string) int64 {
	d, ok := snap.(*OrderedDict[string, any])
	if !ok {
		return 0
	}
	v, ok := d.Get(key)
	if !ok {
		return 0
	}
	if iv, ok := v.(int64); ok {
		return iv
	}
	return 0
}
