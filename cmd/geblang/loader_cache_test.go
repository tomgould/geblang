package main

import (
	"io"
	"testing"

	"geblang/internal/evaluator"
)

func TestLoaderCachesNativeModuleValue(t *testing.T) {
	loader := newBytecodeModuleLoader(io.Discard, nil)
	loader.stateful = evaluator.New(io.Discard)

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
