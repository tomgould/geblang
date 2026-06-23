package native

import "testing"

func TestFlatIndexAddSearchAndOrdering(t *testing.T) {
	idx, err := newFlatIndex("cosine")
	if err != nil {
		t.Fatalf("newFlatIndex: %v", err)
	}
	if err := idx.add("a", []float32{1, 0}); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if err := idx.add("b", []float32{0, 1}); err != nil {
		t.Fatalf("add b: %v", err)
	}
	if err := idx.add("c", []float32{0.9, 0.1}); err != nil {
		t.Fatalf("add c: %v", err)
	}
	if idx.count() != 3 {
		t.Fatalf("count: got %d, want 3", idx.count())
	}
	res, err := idx.search([]float32{1, 0}, 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("search returned %d, want 2", len(res))
	}
	// Nearest to [1,0] under cosine is "a" (identical direction), then "c".
	if res[0].key != "a" || res[1].key != "c" {
		t.Fatalf("ordering: got %q,%q want a,c", res[0].key, res[1].key)
	}
}

func TestFlatIndexUpsertGetDeleteClear(t *testing.T) {
	idx, _ := newFlatIndex("euclidean")
	_ = idx.add("x", []float32{1, 2, 3})
	if v, ok := idx.lookup("x"); !ok || len(v) != 3 || v[2] != 3 {
		t.Fatalf("lookup x: got %v ok=%v", v, ok)
	}
	// Upsert replaces in place; count stays 1.
	_ = idx.add("x", []float32{4, 5, 6})
	if idx.count() != 1 {
		t.Fatalf("after upsert count: got %d want 1", idx.count())
	}
	if v, _ := idx.lookup("x"); v[0] != 4 {
		t.Fatalf("upsert value not replaced: %v", v)
	}
	removed, err := idx.remove("x")
	if err != nil || !removed {
		t.Fatalf("remove x: removed=%v err=%v", removed, err)
	}
	if idx.count() != 0 {
		t.Fatalf("after remove count: got %d want 0", idx.count())
	}
	// Dim resets once empty: a different dimension is now accepted.
	if err := idx.add("y", []float32{1, 2}); err != nil {
		t.Fatalf("add y after empty: %v", err)
	}
	_ = idx.clear()
	if idx.count() != 0 {
		t.Fatalf("after clear count: got %d want 0", idx.count())
	}
}

func TestFlatIndexDimMismatchAndMetricValidation(t *testing.T) {
	idx, _ := newFlatIndex("cosine")
	_ = idx.add("a", []float32{1, 0, 0})
	if err := idx.add("b", []float32{1, 0}); err == nil {
		t.Fatal("expected dimension-mismatch error on add")
	}
	if _, err := idx.search([]float32{1, 0}, 1); err == nil {
		t.Fatal("expected dimension-mismatch error on search")
	}
	if _, err := newFlatIndex("bogus"); err == nil {
		t.Fatal("expected unknown-metric error")
	}
}
