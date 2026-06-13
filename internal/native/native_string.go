package native

import (
	"fmt"
	"geblang/internal/runtime"
	"strings"
)

func registerString(r *Registry) {
	r.Register("string", "fromCodePoint", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("string.fromCodePoint expects 1 argument")
		}
		code, err := codePointInt(args[0], "string.fromCodePoint")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: string(rune(code))}, nil
	})
	r.Register("string", "fromCodePoints", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("string.fromCodePoints expects 1 argument")
		}
		list, ok := args[0].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("string.fromCodePoints expects a list of int codepoints")
		}
		var sb strings.Builder
		sb.Grow(len(list.Elements) * 2)
		for i, elem := range list.Elements {
			code, err := codePointInt(elem, fmt.Sprintf("string.fromCodePoints element %d", i))
			if err != nil {
				return nil, err
			}
			sb.WriteRune(rune(code))
		}
		return runtime.String{Value: sb.String()}, nil
	})
	r.Register("string", "compare", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("string.compare expects 2 arguments")
		}
		a, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("string.compare expects string arguments")
		}
		b, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("string.compare expects string arguments")
		}
		return runtime.SmallInt{Value: int64(strings.Compare(a.Value, b.Value))}, nil
	})
	r.Register("string", "equalsFold", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("string.equalsFold expects 2 arguments")
		}
		a, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("string.equalsFold expects string arguments")
		}
		b, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("string.equalsFold expects string arguments")
		}
		return runtime.Bool{Value: strings.EqualFold(a.Value, b.Value)}, nil
	})
}

// codePointInt validates a Geblang int argument as a Unicode codepoint
// in the U+0000..U+10FFFF range, rejecting the UTF-16 surrogate half.
func codePointInt(value runtime.Value, label string) (int64, error) {
	var code int64
	switch v := value.(type) {
	case runtime.SmallInt:
		code = v.Value
	case runtime.Int:
		if !v.Value.IsInt64() {
			return 0, fmt.Errorf("%s: codepoint out of range", label)
		}
		code = v.Value.Int64()
	default:
		return 0, fmt.Errorf("%s expects an int codepoint", label)
	}
	if code < 0 || code > 0x10FFFF || (code >= 0xD800 && code <= 0xDFFF) {
		return 0, fmt.Errorf("%s: %d is not a valid Unicode codepoint", label, code)
	}
	return code, nil
}

func byteValueInt(value runtime.Value, label string) (int64, error) {
	var n int64
	switch v := value.(type) {
	case runtime.SmallInt:
		n = v.Value
	case runtime.Int:
		if !v.Value.IsInt64() {
			return 0, fmt.Errorf("%s: byte value out of range", label)
		}
		n = v.Value.Int64()
	default:
		return 0, fmt.Errorf("%s expects an int byte value", label)
	}
	if n < 0 || n > 255 {
		return 0, fmt.Errorf("%s: %d is not a byte value (0-255)", label, n)
	}
	return n, nil
}
