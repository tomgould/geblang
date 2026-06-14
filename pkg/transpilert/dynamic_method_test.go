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

func TestCallMethodSortBy(t *testing.T) {
	pairs := []any{[]any{"b", int64(1)}, []any{"a", int64(2)}, []any{"a", int64(1)}}
	key := func(p any) any { return p.([]any)[0] }
	// Stable ascending: equal keys keep input order.
	got := CallMethod(pairs, "sortBy", []any{key}).([]any)
	if Show(got) != `[["a", 2], ["a", 1], ["b", 1]]` {
		t.Errorf("sortBy = %s", Show(got))
	}
	// Descending flag.
	got = CallMethod(got, "sortBy", []any{key, true}).([]any)
	if Show(got) != `[["b", 1], ["a", 2], ["a", 1]]` {
		t.Errorf("sortBy desc = %s", Show(got))
	}
}

func TestCallMethodGroupBy(t *testing.T) {
	xs := []any{
		map3("g", "a", "n", int64(3)),
		map3("g", "b", "n", int64(1)),
		map3("g", "a", "n", int64(2)),
	}
	got := CallMethod(xs, "groupBy", []any{func(r any) any { return dget(r, "g") }})
	if Show(got) != `{"a": [{"g": "a", "n": 3}, {"g": "a", "n": 2}], "b": [{"g": "b", "n": 1}]}` {
		t.Errorf("groupBy = %s", Show(got))
	}
}

func TestCallMethodPartition(t *testing.T) {
	xs := []any{int64(1), int64(2), int64(3), int64(4)}
	got := CallMethod(xs, "partition", []any{func(v any) bool { return v.(int64) > 2 }})
	if Show(got) != `[[3, 4], [1, 2]]` {
		t.Errorf("partition = %s", Show(got))
	}
}

func TestCallMethodMaxMinBy(t *testing.T) {
	xs := []any{int64(3), int64(1), int64(3), int64(2)}
	id := func(v any) any { return v }
	if got := CallMethod(xs, "maxBy", []any{id}); got != int64(3) {
		t.Errorf("maxBy = %v, want first 3", got)
	}
	if got := CallMethod(xs, "minBy", []any{id}); got != int64(1) {
		t.Errorf("minBy = %v, want 1", got)
	}
	if got := CallMethod([]any{}, "maxBy", []any{id}); got != nil {
		t.Errorf("maxBy empty = %v, want nil", got)
	}
}

func TestCallMethodSumByIntStaysInt(t *testing.T) {
	// The int-sum path the three-way corpus cannot reach (a json int parses to a
	// small int the evaluator's sumBy rejects); native keeps an exact int.
	xs := []any{int64(1), int64(2), int64(3)}
	got := CallMethod(xs, "sumBy", []any{func(v any) any { return v }})
	if Show(got) != "6" {
		t.Errorf("sumBy int = %s, want 6", Show(got))
	}
	if got := CallMethod([]any{}, "sumBy", []any{func(v any) any { return v }}); Show(got) != "0" {
		t.Errorf("sumBy empty = %s, want 0", Show(got))
	}
}

func TestCallMethodAverageBy(t *testing.T) {
	xs := []any{int64(1), int64(2), int64(4)}
	// Non-integral average is a decimal.
	got := CallMethod(xs, "averageBy", []any{func(v any) any { return v }})
	if Show(got) != "2.3333333333" {
		t.Errorf("averageBy = %s", Show(got))
	}
	if got := CallMethod([]any{}, "averageBy", []any{func(v any) any { return v }}); got != nil {
		t.Errorf("averageBy empty = %v, want nil", got)
	}
}

func TestCallMethodUniqueBy(t *testing.T) {
	xs := []any{"aa", "bb", "ac", "bd"}
	got := CallMethod(xs, "uniqueBy", []any{func(s any) any { return s.(string)[:1] }}).([]any)
	if Show(got) != `["aa", "bb"]` {
		t.Errorf("uniqueBy = %s", Show(got))
	}
}

func TestCallMethodSumByNonNumberPanics(t *testing.T) {
	defer func() {
		e, ok := recover().(*Error)
		if !ok || e.Message != "list.sumBy: selector must return a number, got string" {
			t.Fatalf("panic = %v", recover())
		}
	}()
	CallMethod([]any{"x"}, "sumBy", []any{func(v any) any { return v }})
}

func TestCallMethodByConcreteCallbackPanics(t *testing.T) {
	defer func() {
		e, ok := recover().(*Error)
		if !ok || e.Class != "RuntimeError" {
			t.Fatalf("panic = %v", recover())
		}
	}()
	CallMethod([]any{int64(1)}, "groupBy", []any{func(v int64) int64 { return v }})
}

func map3(k1 string, v1 any, k2 string, v2 any) *OrderedDict[string, any] {
	d := NewOrderedDict[string, any]()
	d.Set(k1, v1)
	d.Set(k2, v2)
	return d
}

func dget(r any, k string) any {
	v, _ := r.(*OrderedDict[string, any]).Get(k)
	return v
}
