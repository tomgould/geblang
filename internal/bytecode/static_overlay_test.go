package bytecode_test

import (
	"io"
	"sync"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/runtime"
)

const staticOverlaySource = `
class Counter {
    static let count = 0;
    static func bump(): int {
        Counter.count = Counter.count + 1;
        return Counter.count;
    }
    static func read(): int {
        return Counter.count;
    }
}
Counter.count = 10;
`

func compileStaticOverlay(t *testing.T) (bytecode.Chunk, int64, int64) {
	t.Helper()
	source := []byte(staticOverlaySource)
	program := parseProgram(t, staticOverlaySource)
	chunk, err := bytecode.Compile(program, source, "test")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var bumpIndex, readIndex int64 = -1, -1
	for index, fn := range chunk.Functions {
		switch fn.Name {
		case "counter.bump":
			bumpIndex = int64(index)
		case "counter.read":
			readIndex = int64(index)
		}
	}
	if bumpIndex < 0 || readIndex < 0 {
		t.Fatal("static methods not found")
	}
	return chunk, bumpIndex, readIndex
}

// Top-level static assignment (host VM) is visible to wrapper calls.
func TestStaticOverlayHostWriteVisibleToCalls(t *testing.T) {
	chunk, _, readIndex := compileStaticOverlay(t)
	vm := bytecode.NewVM(chunk, io.Discard)
	if err := vm.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := vm.CallFunction(readIndex, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Inspect() != "10" {
		t.Fatalf("read after host write: got %s, want 10", got.Inspect())
	}
}

// Static writes inside a CallFunction wrapper stay call-local: each
// call starts from the host's value (pre-overlay behaviour, pinned).
func TestStaticOverlayWrapperWritesAreCallLocal(t *testing.T) {
	chunk, bumpIndex, readIndex := compileStaticOverlay(t)
	vm := bytecode.NewVM(chunk, io.Discard)
	if err := vm.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	for i := 0; i < 2; i++ {
		got, err := vm.CallFunction(bumpIndex, nil)
		if err != nil {
			t.Fatalf("bump: %v", err)
		}
		if got.Inspect() != "11" {
			t.Fatalf("bump call %d: got %s, want 11", i, got.Inspect())
		}
	}
	got, err := vm.CallFunction(readIndex, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Inspect() != "10" {
		t.Fatalf("read after call-local bumps: got %s, want 10", got.Inspect())
	}
}

// Concurrent wrapper calls touching statics must be race-free (the
// overlay is synchronized; run under -race to enforce).
func TestStaticOverlayConcurrentCalls(t *testing.T) {
	chunk, bumpIndex, _ := compileStaticOverlay(t)
	vm := bytecode.NewVM(chunk, io.Discard)
	if err := vm.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				got, err := vm.CallFunction(bumpIndex, nil)
				if err != nil {
					errs <- err
					return
				}
				if _, ok := got.(runtime.SmallInt); !ok {
					if _, ok := got.(runtime.Int); !ok {
						errs <- err
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent bump: %v", err)
	}
}
