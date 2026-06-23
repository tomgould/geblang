package runtime

import "strconv"

// Complex is a complex-number value backed by Go complex128.
type Complex struct {
	C complex128
}

func (v *Complex) TypeName() string { return "complex.Complex" }

func (v *Complex) Inspect() string {
	re := strconv.FormatFloat(real(v.C), 'g', -1, 64)
	im := imag(v.C)
	if im < 0 {
		return re + "-" + strconv.FormatFloat(-im, 'g', -1, 64) + "i"
	}
	return re + "+" + strconv.FormatFloat(im, 'g', -1, 64) + "i"
}
