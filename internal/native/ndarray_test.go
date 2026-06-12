package native

import (
	"math"
	mrand "math/rand"
	"testing"

	"geblang/internal/runtime"
)

func ndMustF64(t *testing.T, vals []float64, shape []int) *runtime.NDArray {
	t.Helper()
	return &runtime.NDArray{F64: vals, Dtype: runtime.NDFloat64, Shape: shape, Strides: runtime.RowMajorStrides(shape)}
}

// TestNDMatmulBlockedMatchesNaive cross-checks the blocked kernel against
// a plain triple loop on a non-multiple-of-block size.
func TestNDMatmulBlockedMatchesNaive(t *testing.T) {
	const m, k, n = 70, 65, 67
	rng := mrand.New(mrand.NewSource(99))
	a := ndAllocF64([]int{m, k})
	b := ndAllocF64([]int{k, n})
	for i := range a.F64 {
		a.F64[i] = rng.Float64()
	}
	for i := range b.F64 {
		b.F64[i] = rng.Float64()
	}
	got, err := ndMatmul(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			var want float64
			for kk := 0; kk < k; kk++ {
				want += a.F64[i*k+kk] * b.F64[kk*n+j]
			}
			if math.Abs(got.F64[i*n+j]-want) > 1e-9 {
				t.Fatalf("matmul[%d,%d] = %v, want %v", i, j, got.F64[i*n+j], want)
			}
		}
	}
}

func TestNDSolveRoundTrip(t *testing.T) {
	a := ndMustF64(t, []float64{4, 1, 2, 3, 5, 1, 1, 1, 3}, []int{3, 3})
	x := ndMustF64(t, []float64{1, -2, 3}, []int{3})
	bVal, err := ndMatmul(a, &runtime.NDArray{F64: x.F64, Dtype: runtime.NDFloat64, Shape: []int{3, 1}, Strides: []int{1, 1}})
	if err != nil {
		t.Fatal(err)
	}
	b := ndMustF64(t, bVal.F64, []int{3})
	got, err := ndSolve(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for i := range x.F64 {
		if math.Abs(got.F64[i]-x.F64[i]) > 1e-9 {
			t.Fatalf("solve x[%d] = %v, want %v", i, got.F64[i], x.F64[i])
		}
	}
}

func TestNDInvTimesAIsIdentity(t *testing.T) {
	a := ndMustF64(t, []float64{2, 1, 0, 1, 3, 1, 0, 1, 2}, []int{3, 3})
	inv, err := ndInv(a)
	if err != nil {
		t.Fatal(err)
	}
	prod, err := ndMatmul(a, inv)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			want := 0.0
			if i == j {
				want = 1
			}
			if math.Abs(prod.F64[i*3+j]-want) > 1e-9 {
				t.Fatalf("(a*inv)[%d,%d] = %v, want %v", i, j, prod.F64[i*3+j], want)
			}
		}
	}
}

func TestNDDetSingularIsZero(t *testing.T) {
	a := ndMustF64(t, []float64{1, 2, 2, 4}, []int{2, 2})
	d, err := ndDet(a)
	if err != nil {
		t.Fatal(err)
	}
	if d != 0 {
		t.Fatalf("det of singular matrix = %v, want 0", d)
	}
}

// TestNDViewSharesStorage pins the view semantics: slice/t mutate through.
func TestNDViewSharesStorage(t *testing.T) {
	a := ndMustF64(t, []float64{1, 2, 3, 4}, []int{2, 2})
	view, err := NDArrayMethod(a, "slice", []runtime.Value{&runtime.List{Elements: []runtime.Value{
		&runtime.List{Elements: []runtime.Value{runtime.SmallInt{Value: 1}, runtime.SmallInt{Value: 2}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	v := view.(*runtime.NDArray)
	if _, err := NDArrayMethod(v, "set", []runtime.Value{
		&runtime.List{Elements: []runtime.Value{runtime.SmallInt{Value: 0}, runtime.SmallInt{Value: 0}}},
		runtime.Float{Value: 99},
	}); err != nil {
		t.Fatal(err)
	}
	if a.F64[2] != 99 {
		t.Fatalf("view mutation did not write through: %v", a.F64)
	}
	if _, err := NDArrayMethod(a, "copy", nil); err != nil {
		t.Fatal(err)
	}
}

func TestNDBroadcastRules(t *testing.T) {
	row := ndMustF64(t, []float64{1, 2, 3}, []int{1, 3})
	col := ndMustF64(t, []float64{10, 20}, []int{2, 1})
	out, err := ndBinary(row, col, true, func(x, y float64) float64 { return x + y }, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{11, 12, 13, 21, 22, 23}
	for i := range want {
		if out.F64[i] != want[i] {
			t.Fatalf("broadcast add = %v, want %v", out.F64, want)
		}
	}
	if _, err := ndBinary(row, ndMustF64(t, []float64{1, 2}, []int{2}), true, func(x, y float64) float64 { return x }, nil); err == nil {
		t.Fatal("incompatible shapes must error")
	}
}

func BenchmarkNDMatmul256(b *testing.B) {
	rng := mrand.New(mrand.NewSource(1))
	a := ndAllocF64([]int{256, 256})
	m := ndAllocF64([]int{256, 256})
	for i := range a.F64 {
		a.F64[i] = rng.Float64()
		m.F64[i] = rng.Float64()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ndMatmul(a, m); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNDElementwiseAdd1M(b *testing.B) {
	a := ndAllocF64([]int{1000, 1000})
	for i := range a.F64 {
		a.F64[i] = float64(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ndBinary(a, a, true, func(x, y float64) float64 { return x + y }, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNDSumStrided1M(b *testing.B) {
	a := ndAllocF64([]int{1000, 1000})
	for i := range a.F64 {
		a.F64[i] = 1
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ndMeanStd(a)
	}
}
