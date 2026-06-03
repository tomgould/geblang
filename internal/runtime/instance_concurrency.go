package runtime

import "sync/atomic"

// asyncDepth counts live async tasks. While it is nonzero, instance field
// access takes the per-instance lock so parallel goroutines cannot trip Go's
// concurrent-map fatal. A purely sequential program keeps it at zero and pays
// only one atomic load per field access (no locking).
var asyncDepth atomic.Int64

// AsyncEnter / AsyncLeave bracket a spawned async task. AsyncEnter must run on
// the parent goroutine before the task goroutine starts.
func AsyncEnter() { asyncDepth.Add(1) }
func AsyncLeave() { asyncDepth.Add(-1) }

func asyncActive() bool { return asyncDepth.Load() > 0 }

// GetField reads a field, locking only while async tasks are live.
func (i *Instance) GetField(name string) (Value, bool) {
	if asyncActive() {
		i.mu.Lock()
		v, ok := i.Fields[name]
		i.mu.Unlock()
		return v, ok
	}
	v, ok := i.Fields[name]
	return v, ok
}

// SetField writes a field, locking only while async tasks are live.
func (i *Instance) SetField(name string, value Value) {
	if asyncActive() {
		i.mu.Lock()
		i.Fields[name] = value
		i.mu.Unlock()
		return
	}
	i.Fields[name] = value
}

// HasField reports whether a field is present.
func (i *Instance) HasField(name string) bool {
	if asyncActive() {
		i.mu.Lock()
		_, ok := i.Fields[name]
		i.mu.Unlock()
		return ok
	}
	_, ok := i.Fields[name]
	return ok
}

// DeleteField removes a field.
func (i *Instance) DeleteField(name string) {
	if asyncActive() {
		i.mu.Lock()
		delete(i.Fields, name)
		i.mu.Unlock()
		return
	}
	delete(i.Fields, name)
}

// FieldCount returns the number of fields.
func (i *Instance) FieldCount() int {
	if asyncActive() {
		i.mu.Lock()
		n := len(i.Fields)
		i.mu.Unlock()
		return n
	}
	return len(i.Fields)
}

// SnapshotFields returns a shallow copy of the field map, taken under the lock
// when async tasks are live. Use this instead of ranging i.Fields directly so
// iteration never runs concurrently with a writer (and never holds the lock
// across a callback into the interpreter).
func (i *Instance) SnapshotFields() map[string]Value {
	if asyncActive() {
		i.mu.Lock()
		defer i.mu.Unlock()
	}
	out := make(map[string]Value, len(i.Fields))
	for k, v := range i.Fields {
		out[k] = v
	}
	return out
}
