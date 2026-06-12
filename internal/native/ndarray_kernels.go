package native

import (
	"fmt"
	"math"

	"geblang/internal/runtime"
)

// ndIter walks a (possibly strided/broadcast) array in row-major logical
// order, yielding buffer offsets.
type ndIter struct {
	shape   []int
	strides []int
	index   []int
	offset  int
	done    bool
}

func newNDIter(shape, strides []int, offset int) *ndIter {
	it := &ndIter{shape: shape, strides: strides, index: make([]int, len(shape)), offset: offset}
	for _, d := range shape {
		if d == 0 {
			it.done = true
		}
	}
	return it
}

func (it *ndIter) next() (int, bool) {
	if it.done {
		return 0, false
	}
	current := it.offset
	for axis := len(it.shape) - 1; axis >= 0; axis-- {
		it.index[axis]++
		it.offset += it.strides[axis]
		if it.index[axis] < it.shape[axis] {
			return current, true
		}
		it.index[axis] = 0
		it.offset -= it.strides[axis] * it.shape[axis]
	}
	it.done = true
	return current, true
}

func ndShapeEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ndBroadcastShape applies the trailing-dimension broadcast rule.
func ndBroadcastShape(a, b []int) ([]int, error) {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	out := make([]int, n)
	for i := 0; i < n; i++ {
		da, db := 1, 1
		if i >= n-len(a) {
			da = a[i-(n-len(a))]
		}
		if i >= n-len(b) {
			db = b[i-(n-len(b))]
		}
		switch {
		case da == db:
			out[i] = da
		case da == 1:
			out[i] = db
		case db == 1:
			out[i] = da
		default:
			return nil, fmt.Errorf("shapes %v and %v are not broadcastable", a, b)
		}
	}
	return out, nil
}

// ndBroadcastStrides pads/zeroes strides so the array iterates as shape out.
func ndBroadcastStrides(arr *runtime.NDArray, out []int) []int {
	strides := make([]int, len(out))
	pad := len(out) - len(arr.Shape)
	for i := range out {
		if i < pad {
			strides[i] = 0
			continue
		}
		if arr.Shape[i-pad] == 1 && out[i] != 1 {
			strides[i] = 0
		} else {
			strides[i] = arr.Strides[i-pad]
		}
	}
	return strides
}

func ndAllocF64(shape []int) *runtime.NDArray {
	return &runtime.NDArray{F64: make([]float64, ndSize(shape)), Dtype: runtime.NDFloat64, Shape: append([]int(nil), shape...), Strides: runtime.RowMajorStrides(shape)}
}

func ndAllocI64(shape []int) *runtime.NDArray {
	return &runtime.NDArray{I64: make([]int64, ndSize(shape)), Dtype: runtime.NDInt64, Shape: append([]int(nil), shape...), Strides: runtime.RowMajorStrides(shape)}
}

func ndSize(shape []int) int {
	n := 1
	for _, d := range shape {
		n *= d
	}
	return n
}

func ndF64At(a *runtime.NDArray, off int) float64 {
	if a.Dtype == runtime.NDFloat64 {
		return a.F64[off]
	}
	return float64(a.I64[off])
}

// ndMaterialize returns a contiguous row-major copy of a view.
func ndMaterialize(a *runtime.NDArray) *runtime.NDArray {
	if a.Dtype == runtime.NDFloat64 {
		out := ndAllocF64(a.Shape)
		it := newNDIter(a.Shape, a.Strides, a.Offset)
		i := 0
		for off, ok := it.next(); ok; off, ok = it.next() {
			out.F64[i] = a.F64[off]
			i++
		}
		return out
	}
	out := ndAllocI64(a.Shape)
	it := newNDIter(a.Shape, a.Strides, a.Offset)
	i := 0
	for off, ok := it.next(); ok; off, ok = it.next() {
		out.I64[i] = a.I64[off]
		i++
	}
	return out
}

// ndBinary applies op elementwise with broadcasting. Result dtype is
// int64 only when both operands are int64 and intResult is true
// (div and pow always produce float64).
func ndBinary(a, b *runtime.NDArray, intResult bool, fop func(x, y float64) float64, iop func(x, y int64) int64) (*runtime.NDArray, error) {
	shape, err := ndBroadcastShape(a.Shape, b.Shape)
	if err != nil {
		return nil, err
	}
	sa := ndBroadcastStrides(a, shape)
	sb := ndBroadcastStrides(b, shape)
	if a.Dtype == runtime.NDInt64 && b.Dtype == runtime.NDInt64 && intResult {
		out := ndAllocI64(shape)
		ita := newNDIter(shape, sa, a.Offset)
		itb := newNDIter(shape, sb, b.Offset)
		i := 0
		for {
			oa, ok := ita.next()
			if !ok {
				break
			}
			ob, _ := itb.next()
			out.I64[i] = iop(a.I64[oa], b.I64[ob])
			i++
		}
		return out, nil
	}
	out := ndAllocF64(shape)
	ita := newNDIter(shape, sa, a.Offset)
	itb := newNDIter(shape, sb, b.Offset)
	i := 0
	for {
		oa, ok := ita.next()
		if !ok {
			break
		}
		ob, _ := itb.next()
		out.F64[i] = fop(ndF64At(a, oa), ndF64At(b, ob))
		i++
	}
	return out, nil
}

// ndCompare yields an int64 0/1 mask with broadcasting.
func ndCompare(a, b *runtime.NDArray, cmp func(x, y float64) bool) (*runtime.NDArray, error) {
	shape, err := ndBroadcastShape(a.Shape, b.Shape)
	if err != nil {
		return nil, err
	}
	sa := ndBroadcastStrides(a, shape)
	sb := ndBroadcastStrides(b, shape)
	out := ndAllocI64(shape)
	ita := newNDIter(shape, sa, a.Offset)
	itb := newNDIter(shape, sb, b.Offset)
	i := 0
	for {
		oa, ok := ita.next()
		if !ok {
			break
		}
		ob, _ := itb.next()
		if cmp(ndF64At(a, oa), ndF64At(b, ob)) {
			out.I64[i] = 1
		}
		i++
	}
	return out, nil
}

// ndUnaryF64 applies op elementwise producing float64.
func ndUnaryF64(a *runtime.NDArray, op func(float64) float64) *runtime.NDArray {
	out := ndAllocF64(a.Shape)
	it := newNDIter(a.Shape, a.Strides, a.Offset)
	i := 0
	for off, ok := it.next(); ok; off, ok = it.next() {
		out.F64[i] = op(ndF64At(a, off))
		i++
	}
	return out
}

// ndUnarySameDtype applies op keeping the dtype (neg, abs, clip).
func ndUnarySameDtype(a *runtime.NDArray, fop func(float64) float64, iop func(int64) int64) *runtime.NDArray {
	if a.Dtype == runtime.NDInt64 {
		out := ndAllocI64(a.Shape)
		it := newNDIter(a.Shape, a.Strides, a.Offset)
		i := 0
		for off, ok := it.next(); ok; off, ok = it.next() {
			out.I64[i] = iop(a.I64[off])
			i++
		}
		return out
	}
	out := ndAllocF64(a.Shape)
	it := newNDIter(a.Shape, a.Strides, a.Offset)
	i := 0
	for off, ok := it.next(); ok; off, ok = it.next() {
		out.F64[i] = fop(a.F64[off])
		i++
	}
	return out
}

// ndReduceAll folds every element; returns (f64 accumulator, i64
// accumulator, count). Welford mean/std use ndMeanStd instead.
func ndReduceAll(a *runtime.NDArray, finit float64, iinit int64, fop func(acc, x float64) float64, iop func(acc, x int64) int64) (float64, int64) {
	facc, iacc := finit, iinit
	it := newNDIter(a.Shape, a.Strides, a.Offset)
	if a.Dtype == runtime.NDInt64 {
		for off, ok := it.next(); ok; off, ok = it.next() {
			iacc = iop(iacc, a.I64[off])
		}
		return 0, iacc
	}
	for off, ok := it.next(); ok; off, ok = it.next() {
		facc = fop(facc, a.F64[off])
	}
	return facc, 0
}

// ndMeanStd is the Welford one-pass mean / sample variance.
func ndMeanStd(a *runtime.NDArray) (mean, variance float64, n int) {
	var m, m2 float64
	it := newNDIter(a.Shape, a.Strides, a.Offset)
	for off, ok := it.next(); ok; off, ok = it.next() {
		n++
		x := ndF64At(a, off)
		d := x - m
		m += d / float64(n)
		m2 += d * (x - m)
	}
	if n < 2 {
		return m, 0, n
	}
	return m, m2 / float64(n-1), n
}

// ndReduceAxis reduces along one axis with a float64 fold (sum/min/max);
// the result keeps the remaining dims.
func ndReduceAxis(a *runtime.NDArray, axis int, init func() float64, fold func(acc, x float64) float64) *runtime.NDArray {
	outShape := make([]int, 0, len(a.Shape)-1)
	outShape = append(outShape, a.Shape[:axis]...)
	outShape = append(outShape, a.Shape[axis+1:]...)
	if len(outShape) == 0 {
		outShape = []int{1}
	}
	out := ndAllocF64(outShape)
	outerShape := append(append([]int{}, a.Shape[:axis]...), a.Shape[axis+1:]...)
	outerStrides := append(append([]int{}, a.Strides[:axis]...), a.Strides[axis+1:]...)
	it := newNDIter(outerShape, outerStrides, a.Offset)
	i := 0
	for off, ok := it.next(); ok; off, ok = it.next() {
		acc := init()
		for k := 0; k < a.Shape[axis]; k++ {
			acc = fold(acc, ndF64At(a, off+k*a.Strides[axis]))
		}
		out.F64[i] = acc
		i++
	}
	return out
}

// ndCumsum flattens in logical order and accumulates.
func ndCumsum(a *runtime.NDArray) *runtime.NDArray {
	n := a.Size()
	if a.Dtype == runtime.NDInt64 {
		out := ndAllocI64([]int{n})
		it := newNDIter(a.Shape, a.Strides, a.Offset)
		var acc int64
		i := 0
		for off, ok := it.next(); ok; off, ok = it.next() {
			acc += a.I64[off]
			out.I64[i] = acc
			i++
		}
		return out
	}
	out := ndAllocF64([]int{n})
	it := newNDIter(a.Shape, a.Strides, a.Offset)
	var acc float64
	i := 0
	for off, ok := it.next(); ok; off, ok = it.next() {
		acc += a.F64[off]
		out.F64[i] = acc
		i++
	}
	return out
}

// ndArgExtreme returns the flat index of the min (or max) element.
func ndArgExtreme(a *runtime.NDArray, wantMax bool) int64 {
	best := math.Inf(1)
	if wantMax {
		best = math.Inf(-1)
	}
	bestIdx := int64(0)
	it := newNDIter(a.Shape, a.Strides, a.Offset)
	i := int64(0)
	for off, ok := it.next(); ok; off, ok = it.next() {
		x := ndF64At(a, off)
		if (wantMax && x > best) || (!wantMax && x < best) {
			best = x
			bestIdx = i
		}
		i++
	}
	return bestIdx
}

// ndWhere returns a 1-D array of a's elements where mask is non-zero.
func ndWhere(a, mask *runtime.NDArray) (*runtime.NDArray, error) {
	if !ndShapeEqual(a.Shape, mask.Shape) {
		return nil, fmt.Errorf("where mask shape %v does not match array shape %v", mask.Shape, a.Shape)
	}
	var f []float64
	var iv []int64
	ita := newNDIter(a.Shape, a.Strides, a.Offset)
	itm := newNDIter(mask.Shape, mask.Strides, mask.Offset)
	for {
		oa, ok := ita.next()
		if !ok {
			break
		}
		om, _ := itm.next()
		selected := false
		if mask.Dtype == runtime.NDInt64 {
			selected = mask.I64[om] != 0
		} else {
			selected = mask.F64[om] != 0
		}
		if !selected {
			continue
		}
		if a.Dtype == runtime.NDInt64 {
			iv = append(iv, a.I64[oa])
		} else {
			f = append(f, a.F64[oa])
		}
	}
	if a.Dtype == runtime.NDInt64 {
		out := &runtime.NDArray{I64: iv, Dtype: runtime.NDInt64, Shape: []int{len(iv)}, Strides: []int{1}}
		if iv == nil {
			out.I64 = []int64{}
		}
		return out, nil
	}
	out := &runtime.NDArray{F64: f, Dtype: runtime.NDFloat64, Shape: []int{len(f)}, Strides: []int{1}}
	if f == nil {
		out.F64 = []float64{}
	}
	return out, nil
}
