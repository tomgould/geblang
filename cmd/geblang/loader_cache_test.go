package main

import (
	"io"
	"testing"

	"geblang/internal/bcloader"
	"geblang/internal/evaluator"
	"geblang/internal/runtime"
)

func TestLoaderCachesNativeModuleValue(t *testing.T) {
	ev := evaluator.New(io.Discard)
	loader := bcloader.New(io.Discard, nil, ev, bcloader.Options{
		LookupBuiltin: func(canonical, alias string) *runtime.Module {
			return ev.BuiltinModule(canonical, alias)
		},
	})

	first, err := loader.LoadModule("math", "")
	if err != nil {
		t.Fatalf("first LoadModule: %v", err)
	}
	if first == nil {
		t.Fatal("first LoadModule returned nil module")
	}
	second, err := loader.LoadModule("math", "")
	if err != nil {
		t.Fatalf("second LoadModule: %v", err)
	}
	if first != second {
		t.Fatalf("native module not cached: got distinct pointers %p and %p", first, second)
	}
}
