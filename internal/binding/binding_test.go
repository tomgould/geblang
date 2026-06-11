package binding

import (
	"strings"
	"testing"
)

func sig(variadic bool, defaults []bool, names ...string) Signature {
	return Signature{FuncName: "f", ParamNames: names, HasDefault: defaults, Variadic: variadic}
}

func args(names ...string) []Arg {
	out := make([]Arg, len(names))
	for i, n := range names {
		out[i] = Arg{Name: n}
	}
	return out
}

func TestOrderPositional(t *testing.T) {
	cases := []struct {
		name     string
		sig      Signature
		args     []Arg
		slots    []int
		tail     []int
		errPart  string
	}{
		{"exact", sig(false, nil, "a", "b"), args("", ""), []int{0, 1}, nil, ""},
		{"defaultFill", sig(false, []bool{false, true}, "a", "b"), args(""), []int{0, DefaultSlot}, nil, ""},
		{"missing", sig(false, nil, "a", "b"), args(""), nil, nil, "f missing argument b"},
		{"tooMany", sig(false, nil, "a"), args("", ""), nil, nil, "f expects at most 1 arguments, got 2"},
		{"variadicPack", sig(true, nil, "a", "rest"), args("", "", ""), []int{0, DefaultSlot}, []int{1, 2}, ""},
		{"variadicEmpty", sig(true, nil, "a", "rest"), args(""), []int{0, DefaultSlot}, nil, ""},
		{"defvarMiddle", sig(true, []bool{false, true, false}, "a", "b", "rest"), args("", "", "", ""), []int{0, 1, DefaultSlot}, []int{2, 3}, ""},
		{"zeroArgsAllDefault", sig(false, []bool{true, true}, "a", "b"), nil, []int{DefaultSlot, DefaultSlot}, nil, ""},
		{"variadicSigMissingRequired", sig(true, nil, "a", "rest"), nil, nil, nil, "f missing argument a"},
	}
	runOrderCases(t, cases)
}

func TestOrderNamed(t *testing.T) {
	cases := []struct {
		name     string
		sig      Signature
		args     []Arg
		slots    []int
		tail     []int
		errPart  string
	}{
		{"reorder", sig(false, nil, "a", "b"), args("b", "a"), []int{1, 0}, nil, ""},
		{"mixed", sig(false, []bool{false, true}, "a", "b"), args("", "b"), []int{0, 1}, nil, ""},
		{"namedDefault", sig(false, []bool{false, true}, "a", "b"), args("a"), []int{0, DefaultSlot}, nil, ""},
		{"caseInsensitive", sig(false, nil, "alpha"), args("Alpha"), []int{0}, nil, ""},
		{"unknown", sig(false, nil, "a"), args("c"), nil, nil, "f has no parameter c"},
		{"duplicate", sig(false, nil, "a", "b"), args("", "a"), nil, nil, "f parameter a passed more than once"},
		{"missingNamed", sig(false, nil, "a", "b"), args("b"), nil, nil, "f missing argument a"},
		{"variadicNameable", sig(true, []bool{true, false}, "a", "rest"), args("a", "rest"), []int{0, 1}, nil, ""},
		{"namedFillsThenPositional", sig(true, []bool{false, true, false}, "a", "b", "rest"), args("b", "", ""), []int{1, 0, 2}, nil, ""},
		{"tooManyPositionalWithNamed", sig(false, nil, "a"), args("a", ""), nil, nil, "expects at most 1 arguments, got 2"},
	}
	runOrderCases(t, cases)
}

func runOrderCases(t *testing.T, cases []struct {
	name     string
	sig      Signature
	args     []Arg
	slots    []int
	tail     []int
	errPart  string
}) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Order(tc.sig, tc.args)
			if tc.errPart != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errPart) {
					t.Fatalf("want error containing %q, got %v", tc.errPart, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !intsEqual(result.Slots, tc.slots) {
				t.Fatalf("slots: got %v want %v", result.Slots, tc.slots)
			}
			if !intsEqual(result.TailArgs, tc.tail) {
				t.Fatalf("tail: got %v want %v", result.TailArgs, tc.tail)
			}
		})
	}
}

func intsEqual(a, b []int) bool {
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
