package runtime

import "sync/atomic"

// asyncDepth counts live async tasks; while nonzero, field access locks so
// parallel goroutines can't trip Go's concurrent-map fatal. Zero (sequential)
// pays only an atomic load.
var asyncDepth atomic.Int64

// AsyncEnter must run on the parent goroutine before the task goroutine starts.
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

// SnapshotFields copies the field map under the lock, so callers can range it
// without racing a writer or holding the lock across an interpreter callback.
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
