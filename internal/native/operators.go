package native

import (
	"fmt"

	"geblang/internal/runtime"
)

var infixToNDArith = map[string]string{"+": "add", "-": "sub", "*": "mul", "/": "div", "**": "pow"}

var infixToNDCompare = map[string]func(x, y float64) bool{
	"<":  func(x, y float64) bool { return x < y },
	">":  func(x, y float64) bool { return x > y },
	"<=": func(x, y float64) bool { return x <= y },
	">=": func(x, y float64) bool { return x >= y },
}

var infixToExprOp = map[string]string{
	"+": "add", "-": "sub", "*": "mul", "/": "div",
	"<": "lt", ">": "gt", "<=": "lte", ">=": "gte",
}

// BinaryOperatorValue dispatches infix operators over the native value
// classes (NDArray, numeric Series, dataframe Expr) for both backends.
// handled=false when neither operand is one.
func BinaryOperatorValue(op string, left, right runtime.Value) (runtime.Value, bool, error) {
	_, leftExpr := left.(*runtime.DFExpr)
	_, rightExpr := right.(*runtime.DFExpr)
	if leftExpr || rightExpr {
		exprOp, ok := infixToExprOp[op]
		if !ok {
			return nil, true, UnsupportedOperandsError(op, left.TypeName(), right.TypeName())
		}
		return &runtime.DFExpr{Kind: "bin", Op: exprOp, Left: exprOperand(left), Right: exprOperand(right)}, true, nil
	}
	leftArr, leftIs, err := operandAsNDArray(left)
	if err != nil {
		return nil, true, err
	}
	rightArr, rightIs, err := operandAsNDArray(right)
	if err != nil {
		return nil, true, err
	}
	if !leftIs && !rightIs {
		return nil, false, nil
	}
	if !leftIs {
		if leftArr, err = ndScalarOperand(rightArr, left); err != nil {
			return nil, true, UnsupportedOperandsError(op, left.TypeName(), right.TypeName())
		}
	}
	if !rightIs {
		if rightArr, err = ndScalarOperand(leftArr, right); err != nil {
			return nil, true, UnsupportedOperandsError(op, left.TypeName(), right.TypeName())
		}
	}
	if name, ok := infixToNDArith[op]; ok {
		out, err := ndArith(name, leftArr, rightArr)
		if err != nil {
			return nil, true, err
		}
		return out, true, nil
	}
	if cmp, ok := infixToNDCompare[op]; ok {
		out, err := ndCompare(leftArr, rightArr, cmp)
		if err != nil {
			return nil, true, err
		}
		return out, true, nil
	}
	return nil, true, UnsupportedOperandsError(op, left.TypeName(), right.TypeName())
}

// UnaryMinusValue dispatches prefix `-` over NDArray and numeric Series.
func UnaryMinusValue(operand runtime.Value) (runtime.Value, bool, error) {
	arr, ok, err := operandAsNDArray(operand)
	if err != nil {
		return nil, true, err
	}
	if !ok {
		return nil, false, nil
	}
	return ndUnarySameDtype(arr, func(x float64) float64 { return -x }, func(x int64) int64 { return -x }), true, nil
}

func exprOperand(v runtime.Value) *runtime.DFExpr {
	if e, ok := v.(*runtime.DFExpr); ok {
		return e
	}
	return &runtime.DFExpr{Kind: "lit", Lit: v}
}

// operandAsNDArray returns the operand as an NDArray when it is one or
// a numeric Series; (nil, false, nil) for every other value.
func operandAsNDArray(v runtime.Value) (*runtime.NDArray, bool, error) {
	switch t := v.(type) {
	case *runtime.NDArray:
		return t, true, nil
	case *runtime.DFSeries:
		c := t.Col
		if !dfNumeric(c) {
			return nil, true, fmt.Errorf("cannot use non-numeric series %q in arithmetic", c.Name)
		}
		out := ndAllocF64([]int{c.Len()})
		for i := 0; i < c.Len(); i++ {
			if !c.IsNull(i) {
				out.F64[i] = dfCellF64(c, i)
			}
		}
		return out, true, nil
	}
	return nil, false, nil
}
