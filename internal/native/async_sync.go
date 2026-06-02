package native

import (
	"fmt"
	"sync"
	"sync/atomic"

	"geblang/internal/runtime"
)

// Native handle modules for the C2-C4 synchronisation primitives.
//
// `async.sync` exposes Mutex / RWMutex / Semaphore / WaitGroup.
// `async.atomic` exposes AtomicInt / AtomicBool (in async_atomic.go).
//
// Each constructor returns an opaque NativeObject keyed by an ID;
// the Geblang-side wrapper class in `stdlib/async/sync.gb` carries
// the handle and delegates method calls to the free functions
// registered here.

var (
	syncMu          sync.Mutex
	syncNextID      int64
	syncMutexes     = map[int64]*sync.Mutex{}
	syncRWMutexes   = map[int64]*sync.RWMutex{}
	syncSemaphores  = map[int64]chan struct{}{}
	syncWaitGroups  = map[int64]*sync.WaitGroup{}
	syncAtomicInts  = map[int64]*atomic.Int64{}
	syncAtomicBools = map[int64]*atomic.Bool{}
)

func registerAsyncSync(r *Registry) {
	registerAsyncSyncMutex(r)
	registerAsyncSyncRWMutex(r)
	registerAsyncSyncSemaphore(r)
	registerAsyncSyncWaitGroup(r)
}

// ---- Mutex ----

func registerAsyncSyncMutex(r *Registry) {
	r.Register("asyncsync", "mutexNew", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("async.sync.mutexNew expects no arguments")
		}
		id := nextSyncID()
		syncMu.Lock()
		syncMutexes[id] = &sync.Mutex{}
		syncMu.Unlock()
		return runtime.NativeObject{Kind: "AsyncMutex", ID: id}, nil
	})
	r.Register("asyncsync", "mutexLock", func(args []runtime.Value) (runtime.Value, error) {
		m, err := lookupMutex(args, "async.sync.mutexLock")
		if err != nil {
			return nil, err
		}
		m.Lock()
		return runtime.Null{}, nil
	})
	r.Register("asyncsync", "mutexUnlock", func(args []runtime.Value) (runtime.Value, error) {
		m, err := lookupMutex(args, "async.sync.mutexUnlock")
		if err != nil {
			return nil, err
		}
		m.Unlock()
		return runtime.Null{}, nil
	})
	r.Register("asyncsync", "mutexTryLock", func(args []runtime.Value) (runtime.Value, error) {
		m, err := lookupMutex(args, "async.sync.mutexTryLock")
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: m.TryLock()}, nil
	})
}

func lookupMutex(args []runtime.Value, label string) (*sync.Mutex, error) {
	id, err := singleHandle(args, "AsyncMutex", label)
	if err != nil {
		return nil, err
	}
	syncMu.Lock()
	m, ok := syncMutexes[id]
	syncMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%s: unknown handle", label)
	}
	return m, nil
}

// ---- RWMutex ----

func registerAsyncSyncRWMutex(r *Registry) {
	r.Register("asyncsync", "rwmutexNew", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("async.sync.rwmutexNew expects no arguments")
		}
		id := nextSyncID()
		syncMu.Lock()
		syncRWMutexes[id] = &sync.RWMutex{}
		syncMu.Unlock()
		return runtime.NativeObject{Kind: "AsyncRWMutex", ID: id}, nil
	})
	r.Register("asyncsync", "rwmutexLock", func(args []runtime.Value) (runtime.Value, error) {
		m, err := lookupRWMutex(args, "async.sync.rwmutexLock")
		if err != nil {
			return nil, err
		}
		m.Lock()
		return runtime.Null{}, nil
	})
	r.Register("asyncsync", "rwmutexUnlock", func(args []runtime.Value) (runtime.Value, error) {
		m, err := lookupRWMutex(args, "async.sync.rwmutexUnlock")
		if err != nil {
			return nil, err
		}
		m.Unlock()
		return runtime.Null{}, nil
	})
	r.Register("asyncsync", "rwmutexTryLock", func(args []runtime.Value) (runtime.Value, error) {
		m, err := lookupRWMutex(args, "async.sync.rwmutexTryLock")
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: m.TryLock()}, nil
	})
	r.Register("asyncsync", "rwmutexRLock", func(args []runtime.Value) (runtime.Value, error) {
		m, err := lookupRWMutex(args, "async.sync.rwmutexRLock")
		if err != nil {
			return nil, err
		}
		m.RLock()
		return runtime.Null{}, nil
	})
	r.Register("asyncsync", "rwmutexRUnlock", func(args []runtime.Value) (runtime.Value, error) {
		m, err := lookupRWMutex(args, "async.sync.rwmutexRUnlock")
		if err != nil {
			return nil, err
		}
		m.RUnlock()
		return runtime.Null{}, nil
	})
	r.Register("asyncsync", "rwmutexTryRLock", func(args []runtime.Value) (runtime.Value, error) {
		m, err := lookupRWMutex(args, "async.sync.rwmutexTryRLock")
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: m.TryRLock()}, nil
	})
}

func lookupRWMutex(args []runtime.Value, label string) (*sync.RWMutex, error) {
	id, err := singleHandle(args, "AsyncRWMutex", label)
	if err != nil {
		return nil, err
	}
	syncMu.Lock()
	m, ok := syncRWMutexes[id]
	syncMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%s: unknown handle", label)
	}
	return m, nil
}

// ---- Semaphore ----
//
// A counted semaphore backed by a buffered channel. acquire reads
// one slot (blocks if empty); release writes one slot (errors on
// overflow). Permits are an upper bound on outstanding holders.

func registerAsyncSyncSemaphore(r *Registry) {
	r.Register("asyncsync", "semaphoreNew", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("async.sync.semaphoreNew expects one int permits argument")
		}
		permits, ok := AsInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("async.sync.semaphoreNew permits must be int")
		}
		if permits < 1 {
			return nil, fmt.Errorf("async.sync.semaphoreNew permits must be >= 1")
		}
		ch := make(chan struct{}, int(permits))
		for i := int64(0); i < permits; i++ {
			ch <- struct{}{}
		}
		id := nextSyncID()
		syncMu.Lock()
		syncSemaphores[id] = ch
		syncMu.Unlock()
		return runtime.NativeObject{Kind: "AsyncSemaphore", ID: id}, nil
	})
	r.Register("asyncsync", "semaphoreAcquire", func(args []runtime.Value) (runtime.Value, error) {
		ch, err := lookupSemaphore(args, "async.sync.semaphoreAcquire")
		if err != nil {
			return nil, err
		}
		<-ch
		return runtime.Null{}, nil
	})
	r.Register("asyncsync", "semaphoreRelease", func(args []runtime.Value) (runtime.Value, error) {
		ch, err := lookupSemaphore(args, "async.sync.semaphoreRelease")
		if err != nil {
			return nil, err
		}
		select {
		case ch <- struct{}{}:
			return runtime.Null{}, nil
		default:
			return nil, fmt.Errorf("async.sync.semaphoreRelease: more releases than acquires")
		}
	})
	r.Register("asyncsync", "semaphoreTryAcquire", func(args []runtime.Value) (runtime.Value, error) {
		ch, err := lookupSemaphore(args, "async.sync.semaphoreTryAcquire")
		if err != nil {
			return nil, err
		}
		select {
		case <-ch:
			return runtime.Bool{Value: true}, nil
		default:
			return runtime.Bool{Value: false}, nil
		}
	})
}

func lookupSemaphore(args []runtime.Value, label string) (chan struct{}, error) {
	id, err := singleHandle(args, "AsyncSemaphore", label)
	if err != nil {
		return nil, err
	}
	syncMu.Lock()
	ch, ok := syncSemaphores[id]
	syncMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%s: unknown handle", label)
	}
	return ch, nil
}

// ---- WaitGroup ----

func registerAsyncSyncWaitGroup(r *Registry) {
	r.Register("asyncsync", "waitgroupNew", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("async.sync.waitgroupNew expects no arguments")
		}
		id := nextSyncID()
		syncMu.Lock()
		syncWaitGroups[id] = &sync.WaitGroup{}
		syncMu.Unlock()
		return runtime.NativeObject{Kind: "AsyncWaitGroup", ID: id}, nil
	})
	r.Register("asyncsync", "waitgroupAdd", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("async.sync.waitgroupAdd expects (handle, delta)")
		}
		obj, ok := args[0].(runtime.NativeObject)
		if !ok || obj.Kind != "AsyncWaitGroup" {
			return nil, fmt.Errorf("async.sync.waitgroupAdd: not a WaitGroup handle")
		}
		delta, ok := AsInt64(args[1])
		if !ok {
			return nil, fmt.Errorf("async.sync.waitgroupAdd delta must be int")
		}
		wg, err := lookupWaitGroupByID(obj.ID)
		if err != nil {
			return nil, fmt.Errorf("async.sync.waitgroupAdd: %s", err.Error())
		}
		wg.Add(int(delta))
		return runtime.Null{}, nil
	})
	r.Register("asyncsync", "waitgroupDone", func(args []runtime.Value) (runtime.Value, error) {
		wg, err := lookupWaitGroup(args, "async.sync.waitgroupDone")
		if err != nil {
			return nil, err
		}
		wg.Done()
		return runtime.Null{}, nil
	})
	r.Register("asyncsync", "waitgroupWait", func(args []runtime.Value) (runtime.Value, error) {
		wg, err := lookupWaitGroup(args, "async.sync.waitgroupWait")
		if err != nil {
			return nil, err
		}
		wg.Wait()
		return runtime.Null{}, nil
	})
}

func lookupWaitGroup(args []runtime.Value, label string) (*sync.WaitGroup, error) {
	id, err := singleHandle(args, "AsyncWaitGroup", label)
	if err != nil {
		return nil, err
	}
	return lookupWaitGroupByID(id)
}

func lookupWaitGroupByID(id int64) (*sync.WaitGroup, error) {
	syncMu.Lock()
	wg, ok := syncWaitGroups[id]
	syncMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown handle")
	}
	return wg, nil
}

// ---- helpers ----

func nextSyncID() int64 {
	syncMu.Lock()
	syncNextID++
	id := syncNextID
	syncMu.Unlock()
	return id
}

func singleHandle(args []runtime.Value, kind, label string) (int64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s expects (handle)", label)
	}
	obj, ok := args[0].(runtime.NativeObject)
	if !ok || obj.Kind != kind {
		return 0, fmt.Errorf("%s: argument is not a %s handle", label, kind)
	}
	return obj.ID, nil
}
