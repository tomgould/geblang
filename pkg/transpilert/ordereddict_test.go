package transpilert

import (
	"reflect"
	"testing"
)

func TestOrderedDictInsertionOrder(t *testing.T) {
	d := NewOrderedDict[string, int]()
	d.Set("b", 2)
	d.Set("a", 1)
	d.Set("c", 3)
	if got := d.Keys(); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Fatalf("keys = %v, want [b a c]", got)
	}
	if got := d.Values(); !reflect.DeepEqual(got, []int{2, 1, 3}) {
		t.Fatalf("values = %v, want [2 1 3]", got)
	}
}

func TestOrderedDictUpdateKeepsPosition(t *testing.T) {
	d := NewOrderedDict[string, int]()
	d.Set("a", 1)
	d.Set("b", 2)
	d.Set("a", 99)
	if got := d.Keys(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("keys = %v, want [a b]", got)
	}
	if v, _ := d.Get("a"); v != 99 {
		t.Fatalf("a = %d, want 99", v)
	}
}

func TestOrderedDictDeleteThenReinsertGoesToEnd(t *testing.T) {
	d := NewOrderedDict[string, int]()
	d.Set("a", 1)
	d.Set("b", 2)
	d.Set("c", 3)
	d.Delete("a")
	if got := d.Keys(); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("after delete keys = %v, want [b c]", got)
	}
	d.Set("a", 4)
	if got := d.Keys(); !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("after reinsert keys = %v, want [b c a]", got)
	}
}

func TestOrderedDictGetDeleteLen(t *testing.T) {
	d := NewOrderedDict[int64, string]()
	d.Set(10, "x")
	d.Set(20, "y")
	if d.Len() != 2 {
		t.Fatalf("len = %d, want 2", d.Len())
	}
	if _, ok := d.Get(99); ok {
		t.Fatal("missing key reported present")
	}
	d.Delete(99) // no-op
	if d.Len() != 2 {
		t.Fatalf("len after no-op delete = %d, want 2", d.Len())
	}
	d.Delete(10)
	if _, ok := d.Get(10); ok {
		t.Fatal("deleted key still present")
	}
}

func TestOrderedDictEntriesEarlyStop(t *testing.T) {
	d := NewOrderedDict[string, int]()
	d.Set("a", 1)
	d.Set("b", 2)
	d.Set("c", 3)
	var seen []string
	d.Entries(func(k string, _ int) bool {
		seen = append(seen, k)
		return k != "b"
	})
	if !reflect.DeepEqual(seen, []string{"a", "b"}) {
		t.Fatalf("entries visited = %v, want [a b]", seen)
	}
}

func TestOrderedDictBoolKey(t *testing.T) {
	d := NewOrderedDict[bool, string]()
	d.Set(true, "t")
	d.Set(false, "f")
	if v, _ := d.Get(false); v != "f" {
		t.Fatalf("false = %q, want f", v)
	}
	if !reflect.DeepEqual(d.Keys(), []bool{true, false}) {
		t.Fatalf("keys = %v, want [true false]", d.Keys())
	}
}
