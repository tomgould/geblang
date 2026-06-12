package runtime

import (
	"fmt"
	"strings"
)

// NDArray dtype names.
const (
	NDFloat64 = "float64"
	NDInt64   = "int64"
)

// NDArray is an N-dimensional array over one contiguous typed buffer;
// slices/transposes are views sharing the buffer via shape/strides/offset.
type NDArray struct {
	F64     []float64
	I64     []int64
	Dtype   string
	Shape   []int
	Strides []int
	Offset  int
}

func (v *NDArray) TypeName() string { return "ndarray.NDArray" }

func (v *NDArray) Inspect() string {
	dims := make([]string, len(v.Shape))
	for i, d := range v.Shape {
		dims[i] = fmt.Sprintf("%d", d)
	}
	return "<ndarray " + v.Dtype + " [" + strings.Join(dims, "x") + "]>"
}

// Size returns the element count described by the shape.
func (v *NDArray) Size() int {
	n := 1
	for _, d := range v.Shape {
		n *= d
	}
	return n
}

// ElemOffset converts a multi-index to the buffer offset.
func (v *NDArray) ElemOffset(index []int) int {
	off := v.Offset
	for i, ix := range index {
		off += ix * v.Strides[i]
	}
	return off
}

// IsContiguous reports row-major contiguity (reshape can be a view).
func (v *NDArray) IsContiguous() bool {
	stride := 1
	for i := len(v.Shape) - 1; i >= 0; i-- {
		if v.Shape[i] != 1 && v.Strides[i] != stride {
			return false
		}
		stride *= v.Shape[i]
	}
	return true
}

// RowMajorStrides builds contiguous strides for a shape.
func RowMajorStrides(shape []int) []int {
	strides := make([]int, len(shape))
	stride := 1
	for i := len(shape) - 1; i >= 0; i-- {
		strides[i] = stride
		stride *= shape[i]
	}
	return strides
}
