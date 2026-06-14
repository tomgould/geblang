package transpilert

import (
	"math/big"
	"reflect"
	"testing"
)

func TestCallMethodDict(t *testing.T) {
	d := NewOrderedDict[string, any]()
	d.Set("name", "widget")
	d.Set("qty", int64(12))

	if got := CallMethod(d, "length", nil); got != int64(2) {
		t.Errorf("length = %v, want 2", got)
	}
	if got := CallMethod(d, "hasKey", []any{"qty"}); got != true {
		t.Errorf("hasKey(qty) = %v, want true", got)
	}
	if got := CallMethod(d, "hasKey", []any{"absent"}); got != false {
		t.Errorf("hasKey(absent) = %v, want false", got)
	}
	if got := CallMethod(d, "get", []any{"name"}); got != "widget" {
		t.Errorf("get(name) = %v, want widget", got)
	}
	if got := CallMethod(d, "get", []any{"absent"}); got != nil {
		t.Errorf("get(absent) = %v, want nil", got)
	}
	if got := CallMethod(d, "keys", nil); !reflect.DeepEqual(got, []any{"name", "qty"}) {
		t.Errorf("keys = %v, want [name qty]", got)
	}
}

func TestCallMethodList(t *testing.T) {
	xs := []any{"a", "b", "c"}
	if got := CallMethod(xs, "length", nil); got != int64(3) {
		t.Errorf("length = %v, want 3", got)
	}
	if got := CallMethod(xs, "contains", []any{"b"}); got != true {
		t.Errorf("contains(b) = %v, want true", got)
	}
	if got := CallMethod(xs, "indexOf", []any{"c"}); got != int64(2) {
		t.Errorf("indexOf(c) = %v, want 2", got)
	}
	if got := CallMethod(xs, "join", []any{"-"}); got != "a-b-c" {
		t.Errorf("join = %v, want a-b-c", got)
	}
	if got := CallMethod(xs, "first", nil); got != "a" {
		t.Errorf("first = %v, want a", got)
	}
}

func TestCallMethodStringChained(t *testing.T) {
	// upper() returns any, so chaining length() composes through CallMethod.
	upper := CallMethod("widget", "upper", nil)
	if upper != "WIDGET" {
		t.Fatalf("upper = %v, want WIDGET", upper)
	}
	if got := CallMethod(upper, "length", nil); got != int64(6) {
		t.Errorf("length = %v, want 6", got)
	}
}

func TestCallMethodNumeric(t *testing.T) {
	if got := CallMethod(int64(12), "isEven", nil); got != true {
		t.Errorf("isEven = %v, want true", got)
	}
	if got := CallMethod(big.NewRat(7, 2), "toInt", nil); got != int64(3) {
		t.Errorf("decimal.toInt = %v, want 3", got)
	}
	if got := CallMethod(true, "not", nil); got != false {
		t.Errorf("bool.not = %v, want false", got)
	}
}

func TestCallMethodUnknownMethodPanics(t *testing.T) {
	defer func() {
		e, ok := recover().(*Error)
		if !ok || e.Class != "RuntimeError" || e.Message != "unknown method string.nope" {
			t.Fatalf("panic = %v, want unknown method string.nope", recover())
		}
	}()
	CallMethod("x", "nope", nil)
}

func TestCallMethodUnknownReceiverUsesTypeName(t *testing.T) {
	defer func() {
		e, ok := recover().(*Error)
		if !ok || e.Message != "unknown method int.nope" {
			t.Fatalf("panic = %v, want unknown method int.nope", recover())
		}
	}()
	CallMethod(int64(5), "nope", nil)
}
