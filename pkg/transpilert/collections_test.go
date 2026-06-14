package transpilert

import (
	"reflect"
	"testing"
)

func TestMapFilterReduce(t *testing.T) {
	xs := []int64{1, 2, 3}
	if got := Map(xs, func(x int64) int64 { return x * 2 }); !reflect.DeepEqual(got, []int64{2, 4, 6}) {
		t.Errorf("Map = %v", got)
	}
	if got := Filter(xs, func(x int64) bool { return x > 1 }); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Errorf("Filter = %v", got)
	}
	if got := Reduce(xs, func(a, x int64) int64 { return a + x }, int64(0)); got != 6 {
		t.Errorf("Reduce = %v", got)
	}
}

func TestFindReturnsNilOnMiss(t *testing.T) {
	xs := []int64{1, 2, 3}
	if got := Find(xs, func(x int64) bool { return x == 2 }); got != int64(2) {
		t.Errorf("Find hit = %v", got)
	}
	if got := Find(xs, func(x int64) bool { return x == 99 }); got != nil {
		t.Errorf("Find miss = %v, want nil", got)
	}
	if got := FindLast(xs, func(x int64) bool { return x < 3 }); got != int64(2) {
		t.Errorf("FindLast = %v", got)
	}
}

func TestAnyAllCount(t *testing.T) {
	xs := []int64{1, 2, 3}
	if !AnyMatch(xs, func(x int64) bool { return x > 2 }) {
		t.Error("AnyMatch should be true")
	}
	if !AllMatch(xs, func(x int64) bool { return x > 0 }) {
		t.Error("AllMatch should be true")
	}
	if got := Count(xs, func(x int64) bool { return x > 1 }); got != 2 {
		t.Errorf("Count = %v", got)
	}
}

func TestFlatMap(t *testing.T) {
	xs := []int64{1, 2}
	got := FlatMap(xs, func(x int64) []int64 { return []int64{x, x * 10} })
	if !reflect.DeepEqual(got, []int64{1, 10, 2, 20}) {
		t.Errorf("FlatMap = %v", got)
	}
}

func TestSortInPlaceCmp(t *testing.T) {
	xs := []int64{3, 1, 2}
	SortInPlaceCmp(&xs, func(a, b int64) bool { return a > b })
	if !reflect.DeepEqual(xs, []int64{3, 2, 1}) {
		t.Errorf("SortInPlaceCmp = %v", xs)
	}
}

func TestSortInPlaceByStable(t *testing.T) {
	xs := [][]string{{"b", "z"}, {"a", "x"}, {"a", "y"}}
	SortInPlaceBy(&xs, func(p []string) string { return p[0] }, func(a, b string) bool { return a < b }, false)
	want := [][]string{{"a", "x"}, {"a", "y"}, {"b", "z"}}
	if !reflect.DeepEqual(xs, want) {
		t.Errorf("SortInPlaceBy = %v", xs)
	}
}

func TestSortInPlaceByDesc(t *testing.T) {
	xs := []int64{3, 1, 2}
	SortInPlaceBy(&xs, func(x int64) int64 { return x }, func(a, b int64) bool { return a < b }, true)
	if !reflect.DeepEqual(xs, []int64{3, 2, 1}) {
		t.Errorf("SortInPlaceBy desc = %v", xs)
	}
}
