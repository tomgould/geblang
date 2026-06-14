package transpilert

// OrderedDict is an insertion-ordered map; Geblang dicts guarantee iteration
// in insertion order, which Go maps do not. K must be one of the comparable
// primitive key types the transpiler emits: string, int64, or bool. The
// interpreter's type-prefixed key encoding is unneeded because K already
// distinguishes key types at the Go level.
type OrderedDict[K comparable, V any] struct {
	keys   []K
	values map[K]V
}

// NewOrderedDict returns an empty ordered dict.
func NewOrderedDict[K comparable, V any]() *OrderedDict[K, V] {
	return &OrderedDict[K, V]{values: make(map[K]V)}
}

// Set inserts or updates key; a new key is appended to the order, an existing
// key keeps its original position (matching the interpreter's dict semantics).
func (d *OrderedDict[K, V]) Set(key K, value V) {
	if _, ok := d.values[key]; !ok {
		d.keys = append(d.keys, key)
	}
	d.values[key] = value
}

// Get returns the value for key and whether it was present.
func (d *OrderedDict[K, V]) Get(key K) (V, bool) {
	v, ok := d.values[key]
	return v, ok
}

// Delete removes key, preserving the order of the remaining keys.
func (d *OrderedDict[K, V]) Delete(key K) {
	if _, ok := d.values[key]; !ok {
		return
	}
	delete(d.values, key)
	for i, k := range d.keys {
		if k == key {
			d.keys = append(d.keys[:i], d.keys[i+1:]...)
			break
		}
	}
}

// Len returns the number of entries.
func (d *OrderedDict[K, V]) Len() int { return len(d.keys) }

// Keys returns the keys in insertion order.
func (d *OrderedDict[K, V]) Keys() []K {
	out := make([]K, len(d.keys))
	copy(out, d.keys)
	return out
}

// Values returns the values in insertion order.
func (d *OrderedDict[K, V]) Values() []V {
	out := make([]V, len(d.keys))
	for i, k := range d.keys {
		out[i] = d.values[k]
	}
	return out
}

// Entries calls fn for each key/value in insertion order; stops if fn returns false.
func (d *OrderedDict[K, V]) Entries(fn func(key K, value V) bool) {
	for _, k := range d.keys {
		if !fn(k, d.values[k]) {
			return
		}
	}
}
