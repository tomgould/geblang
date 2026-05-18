package runtime

import "testing"

func TestCloneFunctionDeepCopiesCapturedEnvironment(t *testing.T) {
	outer := NewEnvironment()
	sharedList := List{Elements: []Value{NewInt64(1)}}
	if err := outer.Define("items", sharedList, false); err != nil {
		t.Fatal(err)
	}

	original := Function{Name: "handler", Env: outer}
	cloned := CloneFunction(original)

	clonedListValue, ok := cloned.Env.Get("items")
	if !ok {
		t.Fatal("cloned environment is missing captured list")
	}
	clonedList, ok := clonedListValue.(List)
	if !ok {
		t.Fatalf("cloned captured value has type %T, want List", clonedListValue)
	}
	clonedList.Elements[0] = NewInt64(99)

	originalListValue, ok := original.Env.Get("items")
	if !ok {
		t.Fatal("original environment is missing captured list")
	}
	originalList := originalListValue.(List)
	if got := originalList.Elements[0].(Int).Value.Int64(); got != 1 {
		t.Fatalf("original captured list was mutated: got %d, want 1", got)
	}
}

func TestCloneFunctionPreservesEnvironmentCycles(t *testing.T) {
	env := NewEnvironment()
	fn := Function{Name: "handler", Env: env}
	if err := env.Define("handler", fn, true); err != nil {
		t.Fatal(err)
	}

	cloned := CloneFunction(fn)
	captured, ok := cloned.Env.Get("handler")
	if !ok {
		t.Fatal("cloned environment is missing recursive function binding")
	}
	capturedFn, ok := captured.(Function)
	if !ok {
		t.Fatalf("captured handler has type %T, want Function", captured)
	}
	if capturedFn.Env != cloned.Env {
		t.Fatal("recursive function binding does not point at the cloned environment")
	}
}
