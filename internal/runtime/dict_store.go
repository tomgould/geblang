package runtime

// dictData is the shared backing store for dicts built through
// NewDict / NewDictHint. Small dicts keep their entries in inline
// arrays (no map, no separate order slice - one allocation), and spill
// to a map plus an order slice once they exceed dictInlineMax. Because
// a Dict value holds *dictData (a pointer), value copies share the
// store, so PutEntry and the inline->map spill are visible across
// copies - the same value-copy-shares-storage contract the Entries
// map and Order pointer provided. Dicts built from Dict{Entries: ...}
// literals have a nil data pointer and use the legacy map/order
// fields directly.
const dictInlineMax = 8

type dictData struct {
	n     uint8
	keys  [dictInlineMax]string
	vals  [dictInlineMax]DictEntry
	m     map[string]DictEntry // nil until spill
	order []string             // insertion order, set on spill
}

func (d *dictData) length() int {
	if d.m != nil {
		return len(d.m)
	}
	return int(d.n)
}

func (d *dictData) get(key string) (DictEntry, bool) {
	if d.m != nil {
		e, ok := d.m[key]
		return e, ok
	}
	for i := uint8(0); i < d.n; i++ {
		if d.keys[i] == key {
			return d.vals[i], true
		}
	}
	return DictEntry{}, false
}

func (d *dictData) set(key string, entry DictEntry) {
	if d.m != nil {
		if _, hit := d.m[key]; !hit {
			d.order = append(d.order, key)
		}
		d.m[key] = entry
		return
	}
	for i := uint8(0); i < d.n; i++ {
		if d.keys[i] == key {
			d.vals[i] = entry
			return
		}
	}
	if int(d.n) < dictInlineMax {
		d.keys[d.n] = key
		d.vals[d.n] = entry
		d.n++
		return
	}
	// Spill the inline arrays into a map keyed store, preserving order.
	d.m = make(map[string]DictEntry, dictInlineMax*2)
	d.order = make([]string, 0, dictInlineMax*2)
	for i := uint8(0); i < d.n; i++ {
		d.m[d.keys[i]] = d.vals[i]
		d.order = append(d.order, d.keys[i])
	}
	d.n = 0
	d.order = append(d.order, key)
	d.m[key] = entry
}

func (d *dictData) del(key string) {
	if d.m != nil {
		if _, hit := d.m[key]; !hit {
			return
		}
		delete(d.m, key)
		for i, k := range d.order {
			if k == key {
				d.order = append(d.order[:i], d.order[i+1:]...)
				break
			}
		}
		return
	}
	for i := uint8(0); i < d.n; i++ {
		if d.keys[i] == key {
			copy(d.keys[i:], d.keys[i+1:d.n])
			copy(d.vals[i:], d.vals[i+1:d.n])
			d.n--
			d.keys[d.n] = ""
			d.vals[d.n] = DictEntry{}
			return
		}
	}
}

func (d *dictData) clear() {
	if d.m != nil {
		d.m = nil
		d.order = nil
	}
	for i := uint8(0); i < d.n; i++ {
		d.keys[i] = ""
		d.vals[i] = DictEntry{}
	}
	d.n = 0
}

// forEach visits entries in insertion order; stops when fn returns false.
func (d *dictData) forEach(fn func(key string, entry DictEntry) bool) {
	if d.m != nil {
		for _, k := range d.order {
			if e, ok := d.m[k]; ok {
				if !fn(k, e) {
					return
				}
			}
		}
		return
	}
	for i := uint8(0); i < d.n; i++ {
		if !fn(d.keys[i], d.vals[i]) {
			return
		}
	}
}

// orderedKeys returns a fresh slice of keys in insertion order.
func (d *dictData) orderedKeys() []string {
	out := make([]string, 0, d.length())
	if d.m != nil {
		out = append(out, d.order...)
		return out
	}
	for i := uint8(0); i < d.n; i++ {
		out = append(out, d.keys[i])
	}
	return out
}
