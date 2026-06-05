package native_test

import (
	"fmt"
	"sync"
	"testing"

	"geblang/internal/native"
	"geblang/internal/runtime"
)

// TestStoreConcurrentIncrIsRaceFree is the proof that the Store is the safe
// shared-state mechanism: concurrent atomic increments lose no updates and do
// not race (run with -race). A plain Geblang dict mutated the same way fatally
// crashes the process (concurrent map read/write).
func TestStoreConcurrentIncrIsRaceFree(t *testing.T) {
	r := native.NewBuiltinRegistry()
	h, err := r.Call("store", "new", nil)
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 64
	const perG = 250
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				if _, err := r.Call("store", "incr", []runtime.Value{h, runtime.String{Value: "k"}, runtime.SmallInt{Value: 1}}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	got, err := r.Call("store", "get", []runtime.Value{h, runtime.String{Value: "k"}})
	if err != nil {
		t.Fatal(err)
	}
	want := int64(goroutines * perG)
	si, ok := got.(runtime.SmallInt)
	if !ok || si.Value != want {
		t.Fatalf("incr lost updates: got %v, want %d", got, want)
	}
}

// TestStoreConcurrentMixedAccessIsRaceFree hammers set/get/delete on many keys
// from many goroutines: the read+write mix that fatally crashes a plain map.
func TestStoreConcurrentMixedAccessIsRaceFree(t *testing.T) {
	r := native.NewBuiltinRegistry()
	h, err := r.Call("store", "new", nil)
	if err != nil {
		t.Fatal(err)
	}
	const goroutines = 32
	const perG = 300
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				key := runtime.String{Value: fmt.Sprintf("k%d", (base+j)%17)}
				_, _ = r.Call("store", "set", []runtime.Value{h, key, runtime.SmallInt{Value: int64(j)}})
				_, _ = r.Call("store", "get", []runtime.Value{h, key})
				_, _ = r.Call("store", "keys", []runtime.Value{h})
				if j%3 == 0 {
					_, _ = r.Call("store", "delete", []runtime.Value{h, key})
				}
			}
		}(i * 7)
	}
	wg.Wait()
}
