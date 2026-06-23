package native

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

// flatIndex is an exact brute-force annIndex, used where HNSW is unavailable (Windows).

type flatIndex struct {
	mu     sync.Mutex
	metric string
	dim    int
	items  map[string][]float32
}

func newFlatIndex(metric string) (*flatIndex, error) {
	switch metric {
	case "cosine", "", "euclidean", "dot":
	default:
		return nil, fmt.Errorf("hnsw: unknown metric %q", metric)
	}
	return &flatIndex{metric: metric, items: map[string][]float32{}}, nil
}

func (idx *flatIndex) metricName() string { return idx.metric }

func (idx *flatIndex) add(id string, vec []float32) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.items, id)
	if len(idx.items) > 0 && len(vec) != idx.dim {
		return fmt.Errorf("hnsw.add: vector dimension mismatch (%d vs %d)", len(vec), idx.dim)
	}
	cp := make([]float32, len(vec))
	copy(cp, vec)
	idx.items[id] = cp
	idx.dim = len(vec)
	return nil
}

func (idx *flatIndex) lookup(id string) ([]float32, bool) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	v, ok := idx.items[id]
	if !ok {
		return nil, false
	}
	cp := make([]float32, len(v))
	copy(cp, v)
	return cp, true
}

func (idx *flatIndex) remove(id string) (bool, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	_, existed := idx.items[id]
	delete(idx.items, id)
	if len(idx.items) == 0 {
		idx.dim = 0
	}
	return existed, nil
}

func (idx *flatIndex) count() int {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return len(idx.items)
}

func (idx *flatIndex) clear() error {
	idx.mu.Lock()
	idx.items = map[string][]float32{}
	idx.dim = 0
	idx.mu.Unlock()
	return nil
}

func (idx *flatIndex) search(query []float32, k int) ([]annResult, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if len(idx.items) == 0 {
		return nil, nil
	}
	if idx.dim != 0 && idx.dim != len(query) {
		return nil, fmt.Errorf("hnsw.search: query dimension mismatch (%d vs %d)", len(query), idx.dim)
	}
	type scored struct {
		key  string
		vec  []float32
		dist float64
	}
	all := make([]scored, 0, len(idx.items))
	for key, vec := range idx.items {
		all = append(all, scored{key: key, vec: vec, dist: flatDistance(idx.metric, query, vec)})
	}
	// Stable tie-break by key keeps results deterministic across runs.
	sort.Slice(all, func(i, j int) bool {
		if all[i].dist == all[j].dist {
			return all[i].key < all[j].key
		}
		return all[i].dist < all[j].dist
	})
	if k > len(all) {
		k = len(all)
	}
	out := make([]annResult, 0, k)
	for i := 0; i < k; i++ {
		cp := make([]float32, len(all[i].vec))
		copy(cp, all[i].vec)
		out = append(out, annResult{key: all[i].key, vec: cp})
	}
	return out, nil
}

// flatDistance mirrors the hnsw metric semantics (smaller is closer).
func flatDistance(metric string, a, b []float32) float64 {
	switch metric {
	case "euclidean":
		var sum float64
		for i := range a {
			d := float64(a[i] - b[i])
			sum += d * d
		}
		return math.Sqrt(sum)
	case "dot":
		return -dotF32(a, b)
	default: // cosine or ""
		na := math.Sqrt(dotF32(a, a))
		nb := math.Sqrt(dotF32(b, b))
		if na == 0 || nb == 0 {
			return 1
		}
		return 1 - dotF32(a, b)/(na*nb)
	}
}
