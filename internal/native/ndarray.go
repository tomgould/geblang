package native

import (
	"fmt"
	"math"
	mrand "math/rand"
	"time"

	"geblang/internal/runtime"
)

// NDArrayMethods is the canonical method list for dir/catalog guards.
var NDArrayMethods = []string{
	"shape", "dtype", "size", "get", "set", "reshape", "t", "slice",
	"copy", "astype", "toList",
	"add", "sub", "mul", "div", "pow",
	"addScalar", "subScalar", "mulScalar", "divScalar",
	"neg", "abs", "sqrt", "exp", "log", "clip",
	"gt", "lt", "gte", "lte", "eq", "ne", "where",
	"sum", "mean", "min", "max", "std", "variance",
	"argmin", "argmax", "cumsum",
	"matmul", "dot",
}

func registerNDArray(r *Registry) {
	r.Register("ndarray", "array", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("ndarray.array expects one (possibly nested) list argument")
		}
		return ndFromValue(args[0])
	})
	r.Register("ndarray", "zeros", func(args []runtime.Value) (runtime.Value, error) {
		shape, err := ndShapeArg("ndarray.zeros", args, 1, 0)
		if err != nil {
			return nil, err
		}
		return ndAllocF64(shape), nil
	})
	r.Register("ndarray", "ones", func(args []runtime.Value) (runtime.Value, error) {
		shape, err := ndShapeArg("ndarray.ones", args, 1, 0)
		if err != nil {
			return nil, err
		}
		out := ndAllocF64(shape)
		for i := range out.F64 {
			out.F64[i] = 1
		}
		return out, nil
	})
	r.Register("ndarray", "full", func(args []runtime.Value) (runtime.Value, error) {
		shape, err := ndShapeArg("ndarray.full", args, 2, 0)
		if err != nil {
			return nil, err
		}
		fill, _, isFloat, err := ndScalarArg("ndarray.full", args[1])
		if err != nil {
			return nil, err
		}
		if !isFloat {
			ival, _ := AsInt64(args[1])
			out := ndAllocI64(shape)
			for i := range out.I64 {
				out.I64[i] = ival
			}
			return out, nil
		}
		out := ndAllocF64(shape)
		for i := range out.F64 {
			out.F64[i] = fill
		}
		return out, nil
	})
	r.Register("ndarray", "eye", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("ndarray.eye expects a size")
		}
		n, ok := AsInt64(args[0])
		if !ok || n < 0 {
			return nil, fmt.Errorf("ndarray.eye size must be a non-negative int")
		}
		out := ndAllocF64([]int{int(n), int(n)})
		for i := 0; i < int(n); i++ {
			out.F64[i*int(n)+i] = 1
		}
		return out, nil
	})
	r.Register("ndarray", "arange", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("ndarray.arange expects start, stop, and optional step")
		}
		start, sok := AsInt64(args[0])
		stop, eok := AsInt64(args[1])
		step := int64(1)
		if len(args) == 3 {
			s, ok := AsInt64(args[2])
			if !ok || s == 0 {
				return nil, fmt.Errorf("ndarray.arange step must be a non-zero int")
			}
			step = s
		}
		if !sok || !eok {
			return nil, fmt.Errorf("ndarray.arange start and stop must be ints")
		}
		var vals []int64
		if step > 0 {
			for v := start; v < stop; v += step {
				vals = append(vals, v)
			}
		} else {
			for v := start; v > stop; v += step {
				vals = append(vals, v)
			}
		}
		if vals == nil {
			vals = []int64{}
		}
		return &runtime.NDArray{I64: vals, Dtype: runtime.NDInt64, Shape: []int{len(vals)}, Strides: []int{1}}, nil
	})
	r.Register("ndarray", "linspace", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("ndarray.linspace expects start, stop, count")
		}
		start, sok := ndAsFloat(args[0])
		stop, eok := ndAsFloat(args[1])
		count, cok := AsInt64(args[2])
		if !sok || !eok || !cok || count < 2 {
			return nil, fmt.Errorf("ndarray.linspace expects numeric start/stop and an int count >= 2")
		}
		out := ndAllocF64([]int{int(count)})
		step := (stop - start) / float64(count-1)
		for i := range out.F64 {
			out.F64[i] = start + float64(i)*step
		}
		out.F64[count-1] = stop
		return out, nil
	})
	r.Register("ndarray", "random", func(args []runtime.Value) (runtime.Value, error) {
		return ndRandom("ndarray.random", args, false)
	})
	r.Register("ndarray", "randn", func(args []runtime.Value) (runtime.Value, error) {
		return ndRandom("ndarray.randn", args, true)
	})
	r.Register("ndarray", "solve", func(args []runtime.Value) (runtime.Value, error) {
		a, b, err := ndTwoArrayArgs("ndarray.solve", args)
		if err != nil {
			return nil, err
		}
		return ndSolve(a, b)
	})
	r.Register("ndarray", "inv", func(args []runtime.Value) (runtime.Value, error) {
		a, err := ndOneArrayArg("ndarray.inv", args)
		if err != nil {
			return nil, err
		}
		return ndInv(a)
	})
	r.Register("ndarray", "det", func(args []runtime.Value) (runtime.Value, error) {
		a, err := ndOneArrayArg("ndarray.det", args)
		if err != nil {
			return nil, err
		}
		d, err := ndDet(a)
		if err != nil {
			return nil, err
		}
		return runtime.Float{Value: d}, nil
	})
}

// NDArrayMethod is the single method dispatcher shared by both backends.
func NDArrayMethod(receiver *runtime.NDArray, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "shape":
		elems := make([]runtime.Value, len(receiver.Shape))
		for i, d := range receiver.Shape {
			elems[i] = runtime.SmallInt{Value: int64(d)}
		}
		return &runtime.List{Elements: elems}, nil
	case "dtype":
		return runtime.String{Value: receiver.Dtype}, nil
	case "size":
		return runtime.SmallInt{Value: int64(receiver.Size())}, nil
	case "get":
		index, err := ndIndexArg("get", receiver, args)
		if err != nil {
			return nil, err
		}
		off := receiver.ElemOffset(index)
		if receiver.Dtype == runtime.NDInt64 {
			return runtime.SmallInt{Value: receiver.I64[off]}, nil
		}
		return runtime.Float{Value: receiver.F64[off]}, nil
	case "set":
		if len(args) != 2 {
			return nil, fmt.Errorf("ndarray.set expects an index list and a value")
		}
		index, err := ndIndexArg("set", receiver, args[:1])
		if err != nil {
			return nil, err
		}
		off := receiver.ElemOffset(index)
		if receiver.Dtype == runtime.NDInt64 {
			iv, ok := AsInt64(args[1])
			if !ok {
				return nil, fmt.Errorf("ndarray.set value must be an int for an int64 array")
			}
			receiver.I64[off] = iv
			return receiver, nil
		}
		fv, ok := ndAsFloat(args[1])
		if !ok {
			return nil, fmt.Errorf("ndarray.set value must be numeric")
		}
		receiver.F64[off] = fv
		return receiver, nil
	case "reshape":
		shape, err := ndShapeArg("ndarray.reshape", args, 1, 0)
		if err != nil {
			return nil, err
		}
		if ndSize(shape) != receiver.Size() {
			return nil, fmt.Errorf("ndarray.reshape from %v to %v changes the element count", receiver.Shape, shape)
		}
		base := receiver
		if !base.IsContiguous() {
			base = ndMaterialize(base)
		}
		return &runtime.NDArray{F64: base.F64, I64: base.I64, Dtype: base.Dtype, Shape: shape, Strides: runtime.RowMajorStrides(shape), Offset: base.Offset}, nil
	case "t":
		if len(args) != 0 {
			return nil, fmt.Errorf("ndarray.t expects no arguments")
		}
		shape := make([]int, len(receiver.Shape))
		strides := make([]int, len(receiver.Strides))
		for i := range receiver.Shape {
			shape[i] = receiver.Shape[len(receiver.Shape)-1-i]
			strides[i] = receiver.Strides[len(receiver.Strides)-1-i]
		}
		return &runtime.NDArray{F64: receiver.F64, I64: receiver.I64, Dtype: receiver.Dtype, Shape: shape, Strides: strides, Offset: receiver.Offset}, nil
	case "slice":
		return ndSlice(receiver, args)
	case "copy":
		if len(args) != 0 {
			return nil, fmt.Errorf("ndarray.copy expects no arguments")
		}
		return ndMaterialize(receiver), nil
	case "astype":
		if len(args) != 1 {
			return nil, fmt.Errorf("ndarray.astype expects a dtype string")
		}
		want, ok := args[0].(runtime.String)
		if !ok || (want.Value != runtime.NDFloat64 && want.Value != runtime.NDInt64) {
			return nil, fmt.Errorf("ndarray.astype dtype must be %q or %q", runtime.NDFloat64, runtime.NDInt64)
		}
		if want.Value == receiver.Dtype {
			return ndMaterialize(receiver), nil
		}
		if want.Value == runtime.NDFloat64 {
			out := ndAllocF64(receiver.Shape)
			it := newNDIter(receiver.Shape, receiver.Strides, receiver.Offset)
			i := 0
			for off, ok := it.next(); ok; off, ok = it.next() {
				out.F64[i] = float64(receiver.I64[off])
				i++
			}
			return out, nil
		}
		out := ndAllocI64(receiver.Shape)
		it := newNDIter(receiver.Shape, receiver.Strides, receiver.Offset)
		i := 0
		for off, ok := it.next(); ok; off, ok = it.next() {
			out.I64[i] = int64(receiver.F64[off])
			i++
		}
		return out, nil
	case "toList":
		if len(args) != 0 {
			return nil, fmt.Errorf("ndarray.toList expects no arguments")
		}
		return ndToList(receiver, 0, receiver.Offset), nil
	case "add", "sub", "mul", "div", "pow":
		other, err := ndOperandArg("ndarray."+name, receiver, args)
		if err != nil {
			return nil, err
		}
		return ndArith(name, receiver, other)
	case "addScalar", "subScalar", "mulScalar", "divScalar":
		if len(args) != 1 {
			return nil, fmt.Errorf("ndarray.%s expects a scalar", name)
		}
		other, err := ndScalarOperand(receiver, args[0])
		if err != nil {
			return nil, err
		}
		return ndArith(map[string]string{"addScalar": "add", "subScalar": "sub", "mulScalar": "mul", "divScalar": "div"}[name], receiver, other)
	case "neg":
		return ndUnarySameDtype(receiver, func(x float64) float64 { return -x }, func(x int64) int64 { return -x }), nil
	case "abs":
		return ndUnarySameDtype(receiver, math.Abs, func(x int64) int64 {
			if x < 0 {
				return -x
			}
			return x
		}), nil
	case "sqrt":
		return ndUnaryF64(receiver, math.Sqrt), nil
	case "exp":
		return ndUnaryF64(receiver, math.Exp), nil
	case "log":
		return ndUnaryF64(receiver, math.Log), nil
	case "clip":
		if len(args) != 2 {
			return nil, fmt.Errorf("ndarray.clip expects lo and hi")
		}
		lo, lok := ndAsFloat(args[0])
		hi, hok := ndAsFloat(args[1])
		if !lok || !hok {
			return nil, fmt.Errorf("ndarray.clip bounds must be numeric")
		}
		clip := func(x float64) float64 { return math.Min(math.Max(x, lo), hi) }
		return ndUnarySameDtype(receiver, clip, func(x int64) int64 { return int64(clip(float64(x))) }), nil
	case "gt", "lt", "gte", "lte", "eq", "ne":
		other, err := ndOperandArg("ndarray."+name, receiver, args)
		if err != nil {
			return nil, err
		}
		cmp := map[string]func(x, y float64) bool{
			"gt": func(x, y float64) bool { return x > y }, "lt": func(x, y float64) bool { return x < y },
			"gte": func(x, y float64) bool { return x >= y }, "lte": func(x, y float64) bool { return x <= y },
			"eq": func(x, y float64) bool { return x == y }, "ne": func(x, y float64) bool { return x != y },
		}[name]
		return ndCompare(receiver, other, cmp)
	case "where":
		if len(args) != 1 {
			return nil, fmt.Errorf("ndarray.where expects a mask array")
		}
		mask, ok := args[0].(*runtime.NDArray)
		if !ok {
			return nil, fmt.Errorf("ndarray.where mask must be an ndarray")
		}
		return ndWhere(receiver, mask)
	case "sum", "mean", "min", "max":
		return ndReduction(receiver, name, args)
	case "std", "variance":
		if len(args) != 0 {
			return nil, fmt.Errorf("ndarray.%s expects no arguments", name)
		}
		_, v, n := ndMeanStd(receiver)
		if n == 0 {
			return nil, fmt.Errorf("ndarray.%s of an empty array", name)
		}
		if name == "variance" {
			return runtime.Float{Value: v}, nil
		}
		return runtime.Float{Value: math.Sqrt(v)}, nil
	case "argmin":
		return runtime.SmallInt{Value: ndArgExtreme(receiver, false)}, nil
	case "argmax":
		return runtime.SmallInt{Value: ndArgExtreme(receiver, true)}, nil
	case "cumsum":
		return ndCumsum(receiver), nil
	case "matmul":
		other, err := ndOneArrayOnly("ndarray.matmul", args)
		if err != nil {
			return nil, err
		}
		return ndMatmul(receiver, other)
	case "dot":
		other, err := ndOneArrayOnly("ndarray.dot", args)
		if err != nil {
			return nil, err
		}
		return ndDot(receiver, other)
	default:
		return nil, fmt.Errorf("ndarray.NDArray has no method %s", name)
	}
}

func ndArith(name string, a, b *runtime.NDArray) (*runtime.NDArray, error) {
	switch name {
	case "add":
		return ndBinary(a, b, true, func(x, y float64) float64 { return x + y }, func(x, y int64) int64 { return x + y })
	case "sub":
		return ndBinary(a, b, true, func(x, y float64) float64 { return x - y }, func(x, y int64) int64 { return x - y })
	case "mul":
		return ndBinary(a, b, true, func(x, y float64) float64 { return x * y }, func(x, y int64) int64 { return x * y })
	case "div":
		return ndBinary(a, b, false, func(x, y float64) float64 { return x / y }, nil)
	default:
		return ndBinary(a, b, false, math.Pow, nil)
	}
}

// ndReduction handles sum/mean/min/max with an optional {"axis": n} dict.
func ndReduction(a *runtime.NDArray, name string, args []runtime.Value) (runtime.Value, error) {
	axis := -1
	if len(args) == 1 {
		opts, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("ndarray.%s options must be a dict", name)
		}
		if v, ok := ndDictValue(opts, "axis"); ok {
			ax, ok := AsInt64(v)
			if !ok || ax < 0 || int(ax) >= len(a.Shape) {
				return nil, fmt.Errorf("ndarray.%s axis out of range for shape %v", name, a.Shape)
			}
			axis = int(ax)
		}
	} else if len(args) != 0 {
		return nil, fmt.Errorf("ndarray.%s expects no arguments or an options dict", name)
	}
	if axis >= 0 {
		switch name {
		case "sum":
			return ndReduceAxis(a, axis, func() float64 { return 0 }, func(acc, x float64) float64 { return acc + x }), nil
		case "mean":
			sums := ndReduceAxis(a, axis, func() float64 { return 0 }, func(acc, x float64) float64 { return acc + x })
			n := float64(a.Shape[axis])
			for i := range sums.F64 {
				sums.F64[i] /= n
			}
			return sums, nil
		case "min":
			return ndReduceAxis(a, axis, func() float64 { return math.Inf(1) }, math.Min), nil
		default:
			return ndReduceAxis(a, axis, func() float64 { return math.Inf(-1) }, math.Max), nil
		}
	}
	if a.Size() == 0 {
		if name == "sum" {
			if a.Dtype == runtime.NDInt64 {
				return runtime.SmallInt{Value: 0}, nil
			}
			return runtime.Float{Value: 0}, nil
		}
		return nil, fmt.Errorf("ndarray.%s of an empty array", name)
	}
	switch name {
	case "sum":
		f, iv := ndReduceAll(a, 0, 0, func(acc, x float64) float64 { return acc + x }, func(acc, x int64) int64 { return acc + x })
		if a.Dtype == runtime.NDInt64 {
			return runtime.SmallInt{Value: iv}, nil
		}
		return runtime.Float{Value: f}, nil
	case "mean":
		m, _, _ := ndMeanStd(a)
		return runtime.Float{Value: m}, nil
	case "min":
		f, iv := ndReduceAll(a, math.Inf(1), math.MaxInt64, math.Min, func(acc, x int64) int64 {
			if x < acc {
				return x
			}
			return acc
		})
		if a.Dtype == runtime.NDInt64 {
			return runtime.SmallInt{Value: iv}, nil
		}
		return runtime.Float{Value: f}, nil
	default:
		f, iv := ndReduceAll(a, math.Inf(-1), math.MinInt64, math.Max, func(acc, x int64) int64 {
			if x > acc {
				return x
			}
			return acc
		})
		if a.Dtype == runtime.NDInt64 {
			return runtime.SmallInt{Value: iv}, nil
		}
		return runtime.Float{Value: f}, nil
	}
}

// ndSlice builds a view from [[start, stop], ...] per-axis bounds.
func ndSlice(a *runtime.NDArray, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ndarray.slice expects a list of [start, stop] pairs")
	}
	bounds, ok := args[0].(*runtime.List)
	if !ok || len(bounds.Elements) > len(a.Shape) {
		return nil, fmt.Errorf("ndarray.slice expects at most %d [start, stop] pairs", len(a.Shape))
	}
	shape := append([]int(nil), a.Shape...)
	offset := a.Offset
	for axis, bound := range bounds.Elements {
		pair, ok := bound.(*runtime.List)
		if !ok || len(pair.Elements) != 2 {
			return nil, fmt.Errorf("ndarray.slice bounds must be [start, stop] pairs")
		}
		start, sok := AsInt64(pair.Elements[0])
		stop, eok := AsInt64(pair.Elements[1])
		if !sok || !eok || start < 0 || stop < start || int(stop) > a.Shape[axis] {
			return nil, fmt.Errorf("ndarray.slice bounds [%d, %d] out of range for axis %d (size %d)", start, stop, axis, a.Shape[axis])
		}
		offset += int(start) * a.Strides[axis]
		shape[axis] = int(stop - start)
	}
	return &runtime.NDArray{F64: a.F64, I64: a.I64, Dtype: a.Dtype, Shape: shape, Strides: append([]int(nil), a.Strides...), Offset: offset}, nil
}

func ndToList(a *runtime.NDArray, axis, offset int) runtime.Value {
	if len(a.Shape) == 0 {
		if a.Dtype == runtime.NDInt64 {
			return runtime.SmallInt{Value: a.I64[offset]}
		}
		return runtime.Float{Value: a.F64[offset]}
	}
	elems := make([]runtime.Value, a.Shape[axis])
	last := axis == len(a.Shape)-1
	for i := 0; i < a.Shape[axis]; i++ {
		off := offset + i*a.Strides[axis]
		if last {
			if a.Dtype == runtime.NDInt64 {
				elems[i] = runtime.SmallInt{Value: a.I64[off]}
			} else {
				elems[i] = runtime.Float{Value: a.F64[off]}
			}
		} else {
			elems[i] = ndToList(a, axis+1, off)
		}
	}
	return &runtime.List{Elements: elems}
}

// ndFromValue builds an array from a (possibly nested) Geblang list.
func ndFromValue(v runtime.Value) (*runtime.NDArray, error) {
	shape, err := ndInferShape(v, 0)
	if err != nil {
		return nil, err
	}
	var f []float64
	var iv []int64
	isFloat := false
	var walk func(node runtime.Value, depth int) error
	walk = func(node runtime.Value, depth int) error {
		if depth == len(shape) {
			if fv, ok := ndFloatTyped(node); ok {
				if !isFloat {
					isFloat = true
					for _, x := range iv {
						f = append(f, float64(x))
					}
					iv = nil
				}
				f = append(f, fv)
				return nil
			}
			if n, ok := AsInt64(node); ok {
				if isFloat {
					f = append(f, float64(n))
				} else {
					iv = append(iv, n)
				}
				return nil
			}
			return fmt.Errorf("ndarray.array elements must be numeric, got %s", node.TypeName())
		}
		list, ok := node.(*runtime.List)
		if !ok || len(list.Elements) != shape[depth] {
			return fmt.Errorf("ndarray.array rows must be lists of equal length")
		}
		for _, el := range list.Elements {
			if err := walk(el, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(v, 0); err != nil {
		return nil, err
	}
	if isFloat {
		if f == nil {
			f = []float64{}
		}
		return &runtime.NDArray{F64: f, Dtype: runtime.NDFloat64, Shape: shape, Strides: runtime.RowMajorStrides(shape)}, nil
	}
	if iv == nil {
		iv = []int64{}
	}
	return &runtime.NDArray{I64: iv, Dtype: runtime.NDInt64, Shape: shape, Strides: runtime.RowMajorStrides(shape)}, nil
}

func ndInferShape(v runtime.Value, depth int) ([]int, error) {
	list, ok := v.(*runtime.List)
	if !ok {
		return nil, nil
	}
	if depth > 8 {
		return nil, fmt.Errorf("ndarray.array supports at most 8 dimensions")
	}
	if len(list.Elements) == 0 {
		return []int{0}, nil
	}
	inner, err := ndInferShape(list.Elements[0], depth+1)
	if err != nil {
		return nil, err
	}
	return append([]int{len(list.Elements)}, inner...), nil
}

// --- argument helpers ---

func ndAsFloat(v runtime.Value) (float64, bool) {
	return asFloat64Strict(v)
}

// ndFloatTyped reports a value that is float-typed (Float or Decimal), as
// opposed to int-typed, for dtype inference.
func ndFloatTyped(v runtime.Value) (float64, bool) {
	switch f := v.(type) {
	case runtime.Float:
		return f.Value, true
	case runtime.Decimal:
		x, _ := f.Value.Float64()
		return x, true
	}
	return 0, false
}

func ndShapeArg(label string, args []runtime.Value, want, at int) ([]int, error) {
	if len(args) != want {
		return nil, fmt.Errorf("%s expects %d argument(s)", label, want)
	}
	list, ok := args[at].(*runtime.List)
	if !ok || len(list.Elements) == 0 {
		return nil, fmt.Errorf("%s shape must be a non-empty list of ints", label)
	}
	shape := make([]int, len(list.Elements))
	for i, el := range list.Elements {
		n, ok := AsInt64(el)
		if !ok || n < 0 {
			return nil, fmt.Errorf("%s shape entries must be non-negative ints", label)
		}
		shape[i] = int(n)
	}
	return shape, nil
}

func ndIndexArg(label string, a *runtime.NDArray, args []runtime.Value) ([]int, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ndarray.%s expects an index list", label)
	}
	list, ok := args[0].(*runtime.List)
	if !ok || len(list.Elements) != len(a.Shape) {
		return nil, fmt.Errorf("ndarray.%s index must list one position per dimension (%d)", label, len(a.Shape))
	}
	index := make([]int, len(list.Elements))
	for i, el := range list.Elements {
		n, ok := AsInt64(el)
		if !ok || n < 0 || int(n) >= a.Shape[i] {
			return nil, fmt.Errorf("ndarray.%s index out of range for axis %d (size %d)", label, i, a.Shape[i])
		}
		index[i] = int(n)
	}
	return index, nil
}

func ndScalarArg(label string, v runtime.Value) (float64, int64, bool, error) {
	if f, ok := ndFloatTyped(v); ok {
		return f, 0, true, nil
	}
	if n, ok := AsInt64(v); ok {
		return float64(n), n, false, nil
	}
	return 0, 0, false, fmt.Errorf("%s value must be numeric", label)
}

// ndScalarOperand wraps a scalar as a broadcastable 1-element array.
func ndScalarOperand(like *runtime.NDArray, v runtime.Value) (*runtime.NDArray, error) {
	if f, ok := ndFloatTyped(v); ok {
		return &runtime.NDArray{F64: []float64{f}, Dtype: runtime.NDFloat64, Shape: []int{1}, Strides: []int{1}}, nil
	}
	if n, ok := AsInt64(v); ok {
		if like.Dtype == runtime.NDFloat64 {
			return &runtime.NDArray{F64: []float64{float64(n)}, Dtype: runtime.NDFloat64, Shape: []int{1}, Strides: []int{1}}, nil
		}
		return &runtime.NDArray{I64: []int64{n}, Dtype: runtime.NDInt64, Shape: []int{1}, Strides: []int{1}}, nil
	}
	return nil, fmt.Errorf("operand must be an ndarray or a number, got %s", v.TypeName())
}

func ndOperandArg(label string, receiver *runtime.NDArray, args []runtime.Value) (*runtime.NDArray, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one operand", label)
	}
	if other, ok := args[0].(*runtime.NDArray); ok {
		return other, nil
	}
	return ndScalarOperand(receiver, args[0])
}

func ndOneArrayArg(label string, args []runtime.Value) (*runtime.NDArray, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one ndarray", label)
	}
	a, ok := args[0].(*runtime.NDArray)
	if !ok {
		return nil, fmt.Errorf("%s expects an ndarray, got %s", label, args[0].TypeName())
	}
	return a, nil
}

func ndOneArrayOnly(label string, args []runtime.Value) (*runtime.NDArray, error) {
	return ndOneArrayArg(label, args)
}

func ndTwoArrayArgs(label string, args []runtime.Value) (*runtime.NDArray, *runtime.NDArray, error) {
	if len(args) != 2 {
		return nil, nil, fmt.Errorf("%s expects two ndarrays", label)
	}
	a, ok := args[0].(*runtime.NDArray)
	if !ok {
		return nil, nil, fmt.Errorf("%s expects ndarrays, got %s", label, args[0].TypeName())
	}
	b, ok := args[1].(*runtime.NDArray)
	if !ok {
		return nil, nil, fmt.Errorf("%s expects ndarrays, got %s", label, args[1].TypeName())
	}
	return a, b, nil
}

// --- linalg ---

// ndMatmul multiplies 2-D arrays with 64x64 blocking; result is float64.
func ndMatmul(a, b *runtime.NDArray) (*runtime.NDArray, error) {
	if len(a.Shape) != 2 || len(b.Shape) != 2 || a.Shape[1] != b.Shape[0] {
		return nil, fmt.Errorf("ndarray.matmul shapes %v and %v are incompatible", a.Shape, b.Shape)
	}
	am := ndMaterializeF64(a)
	bm := ndMaterializeF64(b)
	m, k, n := a.Shape[0], a.Shape[1], b.Shape[1]
	out := ndAllocF64([]int{m, n})
	const block = 64
	for i0 := 0; i0 < m; i0 += block {
		iMax := min(i0+block, m)
		for k0 := 0; k0 < k; k0 += block {
			kMax := min(k0+block, k)
			for j0 := 0; j0 < n; j0 += block {
				jMax := min(j0+block, n)
				for i := i0; i < iMax; i++ {
					for kk := k0; kk < kMax; kk++ {
						aik := am.F64[i*k+kk]
						if aik == 0 {
							continue
						}
						row := kk * n
						outRow := i * n
						for j := j0; j < jMax; j++ {
							out.F64[outRow+j] += aik * bm.F64[row+j]
						}
					}
				}
			}
		}
	}
	return out, nil
}

func ndMaterializeF64(a *runtime.NDArray) *runtime.NDArray {
	if a.Dtype == runtime.NDFloat64 && a.IsContiguous() && a.Offset == 0 {
		return a
	}
	m := ndMaterialize(a)
	if m.Dtype == runtime.NDInt64 {
		out := ndAllocF64(m.Shape)
		for i, x := range m.I64 {
			out.F64[i] = float64(x)
		}
		return out
	}
	return m
}

func ndDot(a, b *runtime.NDArray) (runtime.Value, error) {
	if len(a.Shape) != 1 || len(b.Shape) != 1 || a.Shape[0] != b.Shape[0] {
		return nil, fmt.Errorf("ndarray.dot expects two 1-D arrays of equal length")
	}
	var acc float64
	ita := newNDIter(a.Shape, a.Strides, a.Offset)
	itb := newNDIter(b.Shape, b.Strides, b.Offset)
	for {
		oa, ok := ita.next()
		if !ok {
			break
		}
		ob, _ := itb.next()
		acc += ndF64At(a, oa) * ndF64At(b, ob)
	}
	return runtime.Float{Value: acc}, nil
}

// ndLU performs in-place Gaussian elimination with partial pivoting on a
// copy; returns the factored matrix, pivot rows, and the permutation sign.
func ndLU(a *runtime.NDArray) ([]float64, int, []int, float64, error) {
	if len(a.Shape) != 2 || a.Shape[0] != a.Shape[1] {
		return nil, 0, nil, 0, fmt.Errorf("expected a square matrix, got shape %v", a.Shape)
	}
	n := a.Shape[0]
	m := append([]float64(nil), ndMaterializeF64(a).F64...)
	pivots := make([]int, n)
	sign := 1.0
	for col := 0; col < n; col++ {
		best, bestRow := math.Abs(m[col*n+col]), col
		for r := col + 1; r < n; r++ {
			if v := math.Abs(m[r*n+col]); v > best {
				best, bestRow = v, r
			}
		}
		if best == 0 {
			return nil, 0, nil, 0, fmt.Errorf("matrix is singular")
		}
		pivots[col] = bestRow
		if bestRow != col {
			sign = -sign
			for j := 0; j < n; j++ {
				m[col*n+j], m[bestRow*n+j] = m[bestRow*n+j], m[col*n+j]
			}
		}
		piv := m[col*n+col]
		for r := col + 1; r < n; r++ {
			factor := m[r*n+col] / piv
			m[r*n+col] = factor
			for j := col + 1; j < n; j++ {
				m[r*n+j] -= factor * m[col*n+j]
			}
		}
	}
	return m, n, pivots, sign, nil
}

func ndSolve(a, b *runtime.NDArray) (*runtime.NDArray, error) {
	lu, n, pivots, _, err := ndLU(a)
	if err != nil {
		return nil, err
	}
	rhsCols := 1
	if len(b.Shape) == 2 {
		rhsCols = b.Shape[1]
	} else if len(b.Shape) != 1 {
		return nil, fmt.Errorf("ndarray.solve rhs must be 1-D or 2-D")
	}
	if b.Shape[0] != n {
		return nil, fmt.Errorf("ndarray.solve rhs has %d rows, want %d", b.Shape[0], n)
	}
	x := append([]float64(nil), ndMaterializeF64(b).F64...)
	for col := 0; col < n; col++ {
		if pivots[col] != col {
			for c := 0; c < rhsCols; c++ {
				x[col*rhsCols+c], x[pivots[col]*rhsCols+c] = x[pivots[col]*rhsCols+c], x[col*rhsCols+c]
			}
		}
		for r := col + 1; r < n; r++ {
			factor := lu[r*n+col]
			for c := 0; c < rhsCols; c++ {
				x[r*rhsCols+c] -= factor * x[col*rhsCols+c]
			}
		}
	}
	for r := n - 1; r >= 0; r-- {
		for c := 0; c < rhsCols; c++ {
			sum := x[r*rhsCols+c]
			for j := r + 1; j < n; j++ {
				sum -= lu[r*n+j] * x[j*rhsCols+c]
			}
			x[r*rhsCols+c] = sum / lu[r*n+r]
		}
	}
	shape := []int{n}
	if len(b.Shape) == 2 {
		shape = []int{n, rhsCols}
	}
	return &runtime.NDArray{F64: x, Dtype: runtime.NDFloat64, Shape: shape, Strides: runtime.RowMajorStrides(shape)}, nil
}

func ndInv(a *runtime.NDArray) (*runtime.NDArray, error) {
	if len(a.Shape) != 2 || a.Shape[0] != a.Shape[1] {
		return nil, fmt.Errorf("ndarray.inv expects a square matrix")
	}
	n := a.Shape[0]
	eye := ndAllocF64([]int{n, n})
	for i := 0; i < n; i++ {
		eye.F64[i*n+i] = 1
	}
	return ndSolve(a, eye)
}

func ndDet(a *runtime.NDArray) (float64, error) {
	lu, n, _, sign, err := ndLU(a)
	if err != nil {
		if err.Error() == "matrix is singular" {
			return 0, nil
		}
		return 0, err
	}
	det := sign
	for i := 0; i < n; i++ {
		det *= lu[i*n+i]
	}
	return det, nil
}

// --- random ---

// ndRandom fills arrays from math/rand, the same generator family the
// random module uses; the seed opt exists because pure-registry code
// cannot reach random.Generator handles (stateful, evaluator-hosted).
func ndRandom(label string, args []runtime.Value, normal bool) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("%s expects a shape and an optional options dict", label)
	}
	shape, err := ndShapeArg(label, args[:1], 1, 0)
	if err != nil {
		return nil, err
	}
	seed := time.Now().UnixNano()
	if len(args) == 2 {
		opts, ok := args[1].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s options must be a dict", label)
		}
		if v, ok := ndDictValue(opts, "seed"); ok {
			n, ok := AsInt64(v)
			if !ok {
				return nil, fmt.Errorf("%s seed must be an int", label)
			}
			seed = n
		}
	}
	rng := mrand.New(mrand.NewSource(seed))
	out := ndAllocF64(shape)
	if normal {
		for i := range out.F64 {
			out.F64[i] = rng.NormFloat64()
		}
		return out, nil
	}
	for i := range out.F64 {
		out.F64[i] = rng.Float64()
	}
	return out, nil
}

func ndDictValue(d runtime.Dict, key string) (runtime.Value, bool) {
	entry, ok := d.GetEntry(DictKey(runtime.String{Value: key}))
	if !ok {
		return nil, false
	}
	return entry.Value, true
}
