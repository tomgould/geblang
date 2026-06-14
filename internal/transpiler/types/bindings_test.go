package types_test

import (
	"testing"

	"geblang/internal/transpiler/types"
)

func TestScopeDefineAndLookup(t *testing.T) {
	s := types.NewScope()
	s.Define(&types.Binding{Name: "x", Type: &types.Type{Kind: types.KindInt}})

	got, ok := s.Lookup("x")
	if !ok {
		t.Fatalf("lookup x: not found")
	}
	if got.Type.Kind != types.KindInt {
		t.Errorf("got Kind %v, want KindInt", got.Type.Kind)
	}
}

func TestScopeLookupReturnsFalseForUnknown(t *testing.T) {
	s := types.NewScope()
	if _, ok := s.Lookup("missing"); ok {
		t.Errorf("expected lookup miss, got hit")
	}
}

func TestScopeChildSeesParentBindings(t *testing.T) {
	parent := types.NewScope()
	parent.Define(&types.Binding{Name: "x", Type: &types.Type{Kind: types.KindInt}})
	child := parent.Child()

	got, ok := child.Lookup("x")
	if !ok {
		t.Fatalf("child lookup x: not found")
	}
	if got.Type.Kind != types.KindInt {
		t.Errorf("got Kind %v, want KindInt", got.Type.Kind)
	}
}

func TestScopeChildShadowsParent(t *testing.T) {
	parent := types.NewScope()
	parent.Define(&types.Binding{Name: "x", Type: &types.Type{Kind: types.KindInt}})
	child := parent.Child()
	child.Define(&types.Binding{Name: "x", Type: &types.Type{Kind: types.KindString}})

	got, _ := child.Lookup("x")
	if got.Type.Kind != types.KindString {
		t.Errorf("child shadow: got Kind %v, want KindString", got.Type.Kind)
	}

	gotParent, _ := parent.Lookup("x")
	if gotParent.Type.Kind != types.KindInt {
		t.Errorf("parent unchanged: got Kind %v, want KindInt", gotParent.Type.Kind)
	}
}

func TestScopeLocalNames(t *testing.T) {
	s := types.NewScope()
	s.Define(&types.Binding{Name: "a", Type: types.Any()})
	s.Define(&types.Binding{Name: "b", Type: types.Any()})

	names := s.LocalNames()
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2", len(names))
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	if !have["a"] || !have["b"] {
		t.Errorf("LocalNames missing entries: got %v", names)
	}
}

func TestScopeParentReturnsParent(t *testing.T) {
	parent := types.NewScope()
	child := parent.Child()
	if child.Parent() != parent {
		t.Errorf("Parent() did not return the parent scope")
	}
	if parent.Parent() != nil {
		t.Errorf("root scope Parent() should be nil")
	}
}
