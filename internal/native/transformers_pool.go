package native

import (
	"fmt"
	"math"

	"geblang/internal/runtime"
)

// transformers.pool reduces a [batch, seq, dim] hidden state + [batch, seq] mask to [batch, dim] sentence embeddings.
func registerPooling(r *Registry) {
	r.Register("transformers", "pool", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("transformers.pool expects (hidden, attentionMask[, opts])")
		}
		hidden, ok := args[0].(*runtime.NDArray)
		if !ok || len(hidden.Shape) != 3 {
			return nil, fmt.Errorf("transformers.pool: hidden must be a 3-D ndarray [batch, seq, dim]")
		}
		mask, ok := args[1].(*runtime.NDArray)
		if !ok || len(mask.Shape) != 2 {
			return nil, fmt.Errorf("transformers.pool: attentionMask must be a 2-D ndarray [batch, seq]")
		}
		batch, seq, dim := hidden.Shape[0], hidden.Shape[1], hidden.Shape[2]
		if mask.Shape[0] != batch || mask.Shape[1] != seq {
			return nil, fmt.Errorf("transformers.pool: mask shape %v does not match hidden [batch=%d, seq=%d]", mask.Shape, batch, seq)
		}
		pooling := "mean"
		normalize := true
		if len(args) == 3 {
			if opts, ok := args[2].(runtime.Dict); ok {
				if v := dictString(opts, "pooling"); v != "" {
					pooling = v
				}
				if _, present := dictLookup(opts, "normalize"); present {
					normalize = dictBool(opts, "normalize")
				}
			}
		}
		maskAt := func(b, t int) float64 {
			off := mask.ElemOffset([]int{b, t})
			if mask.I64 != nil {
				return float64(mask.I64[off])
			}
			return mask.F64[off]
		}
		hAt := func(b, t, d int) float64 { return hidden.F64[hidden.ElemOffset([]int{b, t, d})] }

		out := make([]float64, batch*dim)
		for b := 0; b < batch; b++ {
			switch pooling {
			case "cls":
				for d := 0; d < dim; d++ {
					out[b*dim+d] = hAt(b, 0, d)
				}
			case "max":
				for d := 0; d < dim; d++ {
					m := math.Inf(-1)
					for t := 0; t < seq; t++ {
						if maskAt(b, t) != 0 {
							if v := hAt(b, t, d); v > m {
								m = v
							}
						}
					}
					out[b*dim+d] = m
				}
			case "mean":
				denom := 0.0
				for t := 0; t < seq; t++ {
					denom += maskAt(b, t)
				}
				if denom == 0 {
					denom = 1
				}
				for d := 0; d < dim; d++ {
					s := 0.0
					for t := 0; t < seq; t++ {
						s += hAt(b, t, d) * maskAt(b, t)
					}
					out[b*dim+d] = s / denom
				}
			default:
				return nil, fmt.Errorf("transformers.pool: unknown pooling %q (use mean / cls / max)", pooling)
			}
			if normalize {
				norm := 0.0
				for d := 0; d < dim; d++ {
					norm += out[b*dim+d] * out[b*dim+d]
				}
				if norm = math.Sqrt(norm); norm > 0 {
					for d := 0; d < dim; d++ {
						out[b*dim+d] /= norm
					}
				}
			}
		}
		shape := []int{batch, dim}
		return &runtime.NDArray{F64: out, Dtype: runtime.NDFloat64, Shape: shape, Strides: runtime.RowMajorStrides(shape)}, nil
	})
}
