package native

import (
	"fmt"
	"math"
	"math/big"
	"sort"

	"geblang/internal/runtime"
)

// vecmath: float32 similarity kernels for the vectorstore module. Vectors are
// either a list of numbers or a little-endian float32 BLOB (the store's packed
// form); scoring runs in Go to avoid the interpreted per-element loop.

func registerVecmath(r *Registry) {
	r.Register("vecmath", "score", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("vecmath.score expects (metric, a, b)")
		}
		metric, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("vecmath.score: metric must be a string")
		}
		a, err := vecToFloat32(args[1])
		if err != nil {
			return nil, err
		}
		b, err := vecToFloat32(args[2])
		if err != nil {
			return nil, err
		}
		s, err := vecScore(metric.Value, a, b, math.Sqrt(dotF32(a, a)))
		if err != nil {
			return nil, err
		}
		return runtime.Float{Value: s}, nil
	})

	r.Register("vecmath", "topK", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 4 {
			return nil, fmt.Errorf("vecmath.topK expects (vectors, query, k, metric)")
		}
		vectors, ok := args[0].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("vecmath.topK: vectors must be a list")
		}
		query, err := vecToFloat32(args[1])
		if err != nil {
			return nil, err
		}
		k, ok := vecToInt(args[2])
		if !ok {
			return nil, fmt.Errorf("vecmath.topK: k must be an integer")
		}
		metric, ok := args[3].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("vecmath.topK: metric must be a string")
		}
		qNorm := math.Sqrt(dotF32(query, query))
		type hit struct {
			idx   int
			score float64
		}
		hits := make([]hit, 0, len(vectors.Elements))
		for i, el := range vectors.Elements {
			v, err := vecToFloat32(el)
			if err != nil {
				return nil, err
			}
			s, err := vecScore(metric.Value, query, v, qNorm)
			if err != nil {
				return nil, err
			}
			hits = append(hits, hit{i, s})
		}
		sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
		if k < 0 {
			k = 0
		}
		if k > len(hits) {
			k = len(hits)
		}
		out := make([]runtime.Value, k)
		for i := 0; i < k; i++ {
			d := runtime.NewDictHint(2)
			idxKey := runtime.String{Value: "index"}
			scoreKey := runtime.String{Value: "score"}
			d.PutEntry(DictKey(idxKey), runtime.DictEntry{Key: idxKey, Value: runtime.SmallInt{Value: int64(hits[i].idx)}})
			d.PutEntry(DictKey(scoreKey), runtime.DictEntry{Key: scoreKey, Value: runtime.Float{Value: hits[i].score}})
			out[i] = d
		}
		return &runtime.List{Elements: out}, nil
	})

	r.Register("vecmath", "normalize", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("vecmath.normalize expects (vector) or (list of vectors)")
		}
		// A list whose elements are themselves vectors is a batch; otherwise one vector.
		if lst, ok := args[0].(*runtime.List); ok && len(lst.Elements) > 0 && isVectorValue(lst.Elements[0]) {
			out := make([]runtime.Value, len(lst.Elements))
			for i, el := range lst.Elements {
				v, err := vecToFloat32(el)
				if err != nil {
					return nil, err
				}
				out[i] = normalizedList(v)
			}
			return &runtime.List{Elements: out}, nil
		}
		v, err := vecToFloat32(args[0])
		if err != nil {
			return nil, err
		}
		return normalizedList(v), nil
	})

	r.Register("vecmath", "semanticSearch", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 3 || len(args) > 4 {
			return nil, fmt.Errorf("vecmath.semanticSearch expects (queries, corpus, k[, metric])")
		}
		queries, ok := args[0].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("vecmath.semanticSearch: queries must be a list of vectors")
		}
		corpus, ok := args[1].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("vecmath.semanticSearch: corpus must be a list of vectors")
		}
		k, ok := vecToInt(args[2])
		if !ok {
			return nil, fmt.Errorf("vecmath.semanticSearch: k must be an integer")
		}
		metric := "cosine"
		if len(args) == 4 {
			m, ok := args[3].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("vecmath.semanticSearch: metric must be a string")
			}
			metric = m.Value
		}
		corpusVecs := make([][]float32, len(corpus.Elements))
		for i, el := range corpus.Elements {
			v, err := vecToFloat32(el)
			if err != nil {
				return nil, err
			}
			corpusVecs[i] = v
		}
		results := make([]runtime.Value, len(queries.Elements))
		for qi, qel := range queries.Elements {
			q, err := vecToFloat32(qel)
			if err != nil {
				return nil, err
			}
			hits, err := vecTopKHits(corpusVecs, q, k, metric)
			if err != nil {
				return nil, err
			}
			results[qi] = &runtime.List{Elements: hits}
		}
		return &runtime.List{Elements: results}, nil
	})
}

func isVectorValue(v runtime.Value) bool {
	switch v.(type) {
	case *runtime.List, runtime.Bytes:
		return true
	}
	return false
}

func normalizedList(v []float32) *runtime.List {
	norm := math.Sqrt(dotF32(v, v))
	out := make([]runtime.Value, len(v))
	for i := range v {
		if norm == 0 {
			out[i] = runtime.Float{Value: 0}
		} else {
			out[i] = runtime.Float{Value: float64(v[i]) / norm}
		}
	}
	return &runtime.List{Elements: out}
}

func vecTopKHits(corpusVecs [][]float32, q []float32, k int, metric string) ([]runtime.Value, error) {
	qNorm := math.Sqrt(dotF32(q, q))
	type hit struct {
		idx   int
		score float64
	}
	hits := make([]hit, 0, len(corpusVecs))
	for i, v := range corpusVecs {
		s, err := vecScore(metric, q, v, qNorm)
		if err != nil {
			return nil, err
		}
		hits = append(hits, hit{i, s})
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	if k < 0 {
		k = 0
	}
	if k > len(hits) {
		k = len(hits)
	}
	out := make([]runtime.Value, k)
	for i := 0; i < k; i++ {
		d := runtime.NewDictHint(2)
		idxKey := runtime.String{Value: "index"}
		scoreKey := runtime.String{Value: "score"}
		d.PutEntry(DictKey(idxKey), runtime.DictEntry{Key: idxKey, Value: runtime.SmallInt{Value: int64(hits[i].idx)}})
		d.PutEntry(DictKey(scoreKey), runtime.DictEntry{Key: scoreKey, Value: runtime.Float{Value: hits[i].score}})
		out[i] = d
	}
	return out, nil
}

func vecScore(metric string, q, v []float32, qNorm float64) (float64, error) {
	if len(q) != len(v) {
		return 0, fmt.Errorf("vecmath: vector dimension mismatch (%d vs %d)", len(q), len(v))
	}
	switch metric {
	case "dot":
		return dotF32(q, v), nil
	case "euclidean":
		var sum float64
		for i := range q {
			d := float64(q[i]) - float64(v[i])
			sum += d * d
		}
		return -math.Sqrt(sum), nil
	case "cosine", "":
		vNorm := math.Sqrt(dotF32(v, v))
		if qNorm == 0 || vNorm == 0 {
			return 0, nil
		}
		return dotF32(q, v) / (qNorm * vNorm), nil
	default:
		return 0, fmt.Errorf("vecmath: unknown metric %q", metric)
	}
}

func dotF32(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

func vecToFloat32(v runtime.Value) ([]float32, error) {
	switch vv := v.(type) {
	case runtime.Bytes:
		b := vv.Value
		if len(b)%4 != 0 {
			return nil, fmt.Errorf("vecmath: float32 blob length %d is not a multiple of 4", len(b))
		}
		out := make([]float32, len(b)/4)
		for i := range out {
			bits := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
			out[i] = math.Float32frombits(bits)
		}
		return out, nil
	case *runtime.List:
		out := make([]float32, len(vv.Elements))
		for i, el := range vv.Elements {
			f, ok := vecNumToFloat64(el)
			if !ok {
				return nil, fmt.Errorf("vecmath: vector element %d is %s, expected a number", i, el.TypeName())
			}
			out[i] = float32(f)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("vecmath: expected a list or float32 blob, got %s", v.TypeName())
	}
}

func vecNumToFloat64(v runtime.Value) (float64, bool) {
	switch n := v.(type) {
	case runtime.SmallInt:
		return float64(n.Value), true
	case runtime.Int:
		f, _ := new(big.Float).SetInt(n.Value).Float64()
		return f, true
	case runtime.Float:
		return n.Value, true
	case runtime.Decimal:
		f, _ := n.Value.Float64()
		return f, true
	default:
		return 0, false
	}
}

func vecToInt(v runtime.Value) (int, bool) {
	switch n := v.(type) {
	case runtime.SmallInt:
		return int(n.Value), true
	case runtime.Int:
		if !n.Value.IsInt64() {
			return 0, false
		}
		return int(n.Value.Int64()), true
	default:
		return 0, false
	}
}
