//go:build !windows

package native

import (
	"fmt"
	"sync"

	"github.com/coder/hnsw"
)

type hnswGraphIndex struct {
	mu       sync.Mutex
	graph    *hnsw.Graph[string]
	metric   string
	m        int
	efSearch int
}

func newAnnIndex(metric string, m, ef int) (annIndex, error) {
	return newHnswGraphIndex(metric, m, ef)
}

func newHnswGraphIndex(metric string, m, ef int) (*hnswGraphIndex, error) {
	dist, err := hnswDistanceFor(metric)
	if err != nil {
		return nil, err
	}
	g := hnsw.NewGraph[string]()
	g.Distance = dist
	if m > 0 {
		g.M = m
	}
	if ef > 0 {
		g.EfSearch = ef
	}
	return &hnswGraphIndex{graph: g, metric: metric, m: g.M, efSearch: g.EfSearch}, nil
}

func (idx *hnswGraphIndex) metricName() string { return idx.metric }

// rebuildIfEmpty: the library leaves empty-but-non-nil layers after the last node is deleted, crashing its own Dims/Add; a fresh graph avoids it. Caller holds mu.
func (idx *hnswGraphIndex) rebuildIfEmpty() error {
	if idx.graph.Len() != 0 {
		return nil
	}
	fresh, err := newHnswGraphIndex(idx.metric, idx.m, idx.efSearch)
	if err != nil {
		return err
	}
	idx.graph = fresh.graph
	return nil
}

func (idx *hnswGraphIndex) add(id string, vec []float32) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if err := idx.rebuildIfEmpty(); err != nil {
		return err
	}
	if d := idx.graph.Dims(); d != 0 && d != len(vec) {
		return fmt.Errorf("hnsw.add: vector dimension mismatch (%d vs %d)", len(vec), d)
	}
	idx.graph.Delete(id)
	if err := idx.rebuildIfEmpty(); err != nil {
		return err
	}
	idx.graph.Add(hnsw.MakeNode(id, vec))
	return nil
}

func (idx *hnswGraphIndex) lookup(id string) ([]float32, bool) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.graph.Lookup(id)
}

func (idx *hnswGraphIndex) remove(id string) (bool, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	removed := idx.graph.Delete(id)
	if err := idx.rebuildIfEmpty(); err != nil {
		return removed, err
	}
	return removed, nil
}

func (idx *hnswGraphIndex) count() int {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.graph.Len()
}

func (idx *hnswGraphIndex) clear() error {
	fresh, err := newHnswGraphIndex(idx.metric, idx.m, idx.efSearch)
	if err != nil {
		return err
	}
	idx.mu.Lock()
	idx.graph = fresh.graph
	idx.mu.Unlock()
	return nil
}

func (idx *hnswGraphIndex) search(query []float32, k int) ([]annResult, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.graph.Len() == 0 {
		return nil, nil
	}
	if d := idx.graph.Dims(); d != 0 && d != len(query) {
		return nil, fmt.Errorf("hnsw.search: query dimension mismatch (%d vs %d)", len(query), d)
	}
	nodes := idx.graph.Search(query, k)
	out := make([]annResult, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, annResult{key: node.Key, vec: node.Value})
	}
	return out, nil
}

func hnswDistanceFor(metric string) (hnsw.DistanceFunc, error) {
	switch metric {
	case "cosine", "":
		return hnsw.CosineDistance, nil
	case "euclidean":
		return hnsw.EuclideanDistance, nil
	case "dot":
		// HNSW expects a distance (smaller = closer); negate the inner product.
		return func(a, b []float32) float32 {
			var sum float32
			for i := range a {
				sum += a[i] * b[i]
			}
			return -sum
		}, nil
	default:
		return nil, fmt.Errorf("hnsw: unknown metric %q", metric)
	}
}
