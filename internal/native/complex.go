package native

import (
	"fmt"
	"math/cmplx"

	"geblang/internal/runtime"
)

// ComplexMethods is the canonical method list for dir/catalog guards.
var ComplexMethods = []string{"re", "im", "abs", "arg", "conj", "neg", "exp", "sqrt", "add", "sub", "mul", "div", "pow", "equals"}

// complexOperand coerces a Complex or a real number to complex128.
func complexOperand(v runtime.Value) (complex128, bool) {
	if c, ok := v.(*runtime.Complex); ok {
		return c.C, true
	}
	if f, err := FloatLike(v); err == nil {
		return complex(f, 0), true
	}
	return 0, false
}

// complexArith applies a named binary op to two complex128 values.
func complexArith(op string, a, b complex128) complex128 {
	switch op {
	case "add":
		return a + b
	case "sub":
		return a - b
	case "mul":
		return a * b
	case "div":
		return a / b
	case "pow":
		return cmplx.Pow(a, b)
	}
	return 0
}

func registerComplex(r *Registry) {
	r.Register("complex", "of", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("complex.of expects (re, im)")
		}
		re, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		im, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		return &runtime.Complex{C: complex(re, im)}, nil
	})
	r.Register("complex", "fromPolar", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("complex.fromPolar expects (r, theta)")
		}
		rad, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		theta, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		return &runtime.Complex{C: cmplx.Rect(rad, theta)}, nil
	})
}

// ComplexMethod dispatches a method call on a Complex; shared by both backends.
func ComplexMethod(z *runtime.Complex, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "re":
		return runtime.Float{Value: real(z.C)}, nil
	case "im":
		return runtime.Float{Value: imag(z.C)}, nil
	case "abs":
		return runtime.Float{Value: cmplx.Abs(z.C)}, nil
	case "arg":
		return runtime.Float{Value: cmplx.Phase(z.C)}, nil
	case "conj":
		return &runtime.Complex{C: cmplx.Conj(z.C)}, nil
	case "neg":
		return &runtime.Complex{C: -z.C}, nil
	case "exp":
		return &runtime.Complex{C: cmplx.Exp(z.C)}, nil
	case "sqrt":
		return &runtime.Complex{C: cmplx.Sqrt(z.C)}, nil
	case "add", "sub", "mul", "div", "pow":
		if len(args) != 1 {
			return nil, fmt.Errorf("complex.%s expects one argument", name)
		}
		other, ok := complexOperand(args[0])
		if !ok {
			return nil, fmt.Errorf("complex.%s: argument must be a complex or a number", name)
		}
		return &runtime.Complex{C: complexArith(name, z.C, other)}, nil
	case "equals":
		if len(args) != 1 {
			return nil, fmt.Errorf("complex.equals expects one argument")
		}
		other, ok := complexOperand(args[0])
		return runtime.Bool{Value: ok && z.C == other}, nil
	default:
		return nil, fmt.Errorf("complex.Complex has no method %s", name)
	}
}
