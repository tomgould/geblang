package native

import (
	"fmt"
	"math"
	"sync"

	"github.com/coder/hnsw"

	"geblang/internal/runtime"
)

// hnsw: a stateful native wrapper over the coder/hnsw in-memory ANN index,
// backing vectorstore.HnswVectorStore. Indexes live in a package-global map
// keyed by handle id (like the store module); each is mutex-guarded because the
// graph is not safe for concurrent use.

type hnswIndex struct {
	mu       sync.Mutex
	graph    *hnsw.Graph[string]
	metric   string
	m        int
	efSearch int
}

var (
	hnswRegMu sync.Mutex
	hnswReg   = map[int64]*hnswIndex{}
)

func registerHnsw(r *Registry) {
	r.Register("hnsw", "new", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("hnsw.new expects (metric, m, efSearch)")
		}
		metric, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("hnsw.new: metric must be a string")
		}
		m, ok := vecToInt(args[1])
		if !ok {
			return nil, fmt.Errorf("hnsw.new: m must be an integer")
		}
		ef, ok := vecToInt(args[2])
		if !ok {
			return nil, fmt.Errorf("hnsw.new: efSearch must be an integer")
		}
		idx, err := newHnswIndex(metric.Value, m, ef)
		if err != nil {
			return nil, err
		}
		id := nextSyncID()
		hnswRegMu.Lock()
		hnswReg[id] = idx
		hnswRegMu.Unlock()
		return runtime.NativeObject{Kind: "HnswIndex", ID: id}, nil
	})

	r.Register("hnsw", "add", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("hnsw.add expects (handle, id, vector)")
		}
		idx, err := hnswFromHandle(args[0], "hnsw.add")
		if err != nil {
			return nil, err
		}
		id, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("hnsw.add: id must be a string")
		}
		vec, err := vecToFloat32(args[2])
		if err != nil {
			return nil, err
		}
		idx.mu.Lock()
		defer idx.mu.Unlock()
		if err := idx.rebuildIfEmpty(); err != nil {
			return nil, err
		}
		if d := idx.graph.Dims(); d != 0 && d != len(vec) {
			return nil, fmt.Errorf("hnsw.add: vector dimension mismatch (%d vs %d)", len(vec), d)
		}
		/* Delete-then-add for clean upsert: a bare re-add can leave stale links. */
		idx.graph.Delete(id.Value)
		if err := idx.rebuildIfEmpty(); err != nil {
			return nil, err
		}
		idx.graph.Add(hnsw.MakeNode(id.Value, vec))
		return runtime.Null{}, nil
	})

	r.Register("hnsw", "get", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("hnsw.get expects (handle, id)")
		}
		idx, err := hnswFromHandle(args[0], "hnsw.get")
		if err != nil {
			return nil, err
		}
		id, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("hnsw.get: id must be a string")
		}
		idx.mu.Lock()
		vec, found := idx.graph.Lookup(id.Value)
		idx.mu.Unlock()
		if !found {
			return runtime.Null{}, nil
		}
		return float32sToList(vec), nil
	})

	r.Register("hnsw", "delete", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("hnsw.delete expects (handle, id)")
		}
		idx, err := hnswFromHandle(args[0], "hnsw.delete")
		if err != nil {
			return nil, err
		}
		id, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("hnsw.delete: id must be a string")
		}
		idx.mu.Lock()
		removed := idx.graph.Delete(id.Value)
		err = idx.rebuildIfEmpty()
		idx.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: removed}, nil
	})

	r.Register("hnsw", "count", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("hnsw.count expects (handle)")
		}
		idx, err := hnswFromHandle(args[0], "hnsw.count")
		if err != nil {
			return nil, err
		}
		idx.mu.Lock()
		n := idx.graph.Len()
		idx.mu.Unlock()
		return runtime.NewInt64(int64(n)), nil
	})

	r.Register("hnsw", "clear", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("hnsw.clear expects (handle)")
		}
		idx, err := hnswFromHandle(args[0], "hnsw.clear")
		if err != nil {
			return nil, err
		}
		fresh, err := newHnswIndex(idx.metric, idx.m, idx.efSearch)
		if err != nil {
			return nil, err
		}
		idx.mu.Lock()
		idx.graph = fresh.graph
		idx.mu.Unlock()
		return runtime.Null{}, nil
	})

	r.Register("hnsw", "search", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("hnsw.search expects (handle, query, k)")
		}
		idx, err := hnswFromHandle(args[0], "hnsw.search")
		if err != nil {
			return nil, err
		}
		query, err := vecToFloat32(args[1])
		if err != nil {
			return nil, err
		}
		k, ok := vecToInt(args[2])
		if !ok {
			return nil, fmt.Errorf("hnsw.search: k must be an integer")
		}
		idx.mu.Lock()
		defer idx.mu.Unlock()
		if idx.graph.Len() == 0 {
			return &runtime.List{Elements: []runtime.Value{}}, nil
		}
		if d := idx.graph.Dims(); d != 0 && d != len(query) {
			return nil, fmt.Errorf("hnsw.search: query dimension mismatch (%d vs %d)", len(query), d)
		}
		if k < 0 {
			k = 0
		}
		nodes := idx.graph.Search(query, k)
		qNorm := vecNormF32(query)
		out := make([]runtime.Value, 0, len(nodes))
		for _, node := range nodes {
			score, err := vecScore(idx.metric, query, node.Value, qNorm)
			if err != nil {
				return nil, err
			}
			d := runtime.NewDictHint(3)
			idKey := runtime.String{Value: "id"}
			scoreKey := runtime.String{Value: "score"}
			vecKey := runtime.String{Value: "vector"}
			d.PutEntry(DictKey(idKey), runtime.DictEntry{Key: idKey, Value: runtime.String{Value: node.Key}})
			d.PutEntry(DictKey(scoreKey), runtime.DictEntry{Key: scoreKey, Value: runtime.Float{Value: score}})
			d.PutEntry(DictKey(vecKey), runtime.DictEntry{Key: vecKey, Value: float32sToList(node.Value)})
			out = append(out, d)
		}
		return &runtime.List{Elements: out}, nil
	})
}

// rebuildIfEmpty replaces an emptied graph with a fresh one. The library leaves
// empty-but-non-nil layers after the last node is deleted, which crashes its
// own Dims/Add; keeping an empty index fresh avoids that state. Caller holds mu.
func (idx *hnswIndex) rebuildIfEmpty() error {
	if idx.graph.Len() != 0 {
		return nil
	}
	fresh, err := newHnswIndex(idx.metric, idx.m, idx.efSearch)
	if err != nil {
		return err
	}
	idx.graph = fresh.graph
	return nil
}

func newHnswIndex(metric string, m, ef int) (*hnswIndex, error) {
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
	return &hnswIndex{graph: g, metric: metric, m: g.M, efSearch: g.EfSearch}, nil
}

func hnswDistanceFor(metric string) (hnsw.DistanceFunc, error) {
	switch metric {
	case "cosine", "":
		return hnsw.CosineDistance, nil
	case "euclidean":
		return hnsw.EuclideanDistance, nil
	case "dot":
		/* HNSW expects a distance (smaller = closer); negate the inner product */
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

func hnswFromHandle(v runtime.Value, fn string) (*hnswIndex, error) {
	obj, ok := v.(runtime.NativeObject)
	if !ok || obj.Kind != "HnswIndex" {
		return nil, fmt.Errorf("%s expects an HnswIndex handle", fn)
	}
	hnswRegMu.Lock()
	idx, ok := hnswReg[obj.ID]
	hnswRegMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%s: unknown HnswIndex handle", fn)
	}
	return idx, nil
}

func float32sToList(vec []float32) runtime.Value {
	out := make([]runtime.Value, len(vec))
	for i, f := range vec {
		out[i] = runtime.Float{Value: float64(f)}
	}
	return &runtime.List{Elements: out}
}

func vecNormF32(v []float32) float64 {
	return math.Sqrt(dotF32(v, v))
}
