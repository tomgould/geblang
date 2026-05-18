package runtime

import (
	"fmt"
	"sort"
)

type Binding struct {
	Value    Value
	Constant bool
}

type Environment struct {
	store        map[string]Binding
	typeBindings map[string]string
	outer        *Environment
}

func NewEnvironment() *Environment {
	return &Environment{store: map[string]Binding{}}
}

func NewEnclosedEnvironment(outer *Environment) *Environment {
	return &Environment{store: map[string]Binding{}, outer: outer}
}

func (e *Environment) Define(name string, value Value, constant bool) error {
	if _, exists := e.store[name]; exists {
		return fmt.Errorf("%q is already declared in this scope", name)
	}
	if constant {
		value = ShallowFreeze(value)
	}
	e.store[name] = Binding{Value: value, Constant: constant}
	return nil
}

func (e *Environment) DefineFunction(name string, fn Function) error {
	if binding, exists := e.store[name]; exists {
		switch value := binding.Value.(type) {
		case Function:
			e.store[name] = Binding{Value: OverloadedFunction{Name: name, Overloads: []Function{value, fn}}, Constant: true}
			return nil
		case OverloadedFunction:
			value.Overloads = append(value.Overloads, fn)
			e.store[name] = Binding{Value: value, Constant: true}
			return nil
		default:
			return fmt.Errorf("%q is already declared in this scope", name)
		}
	}
	e.store[name] = Binding{Value: fn, Constant: true}
	return nil
}

func (e *Environment) Assign(name string, value Value) error {
	if binding, exists := e.store[name]; exists {
		if binding.Constant {
			return fmt.Errorf("cannot assign to constant %q", name)
		}
		binding.Value = value
		e.store[name] = binding
		return nil
	}
	if e.outer != nil {
		return e.outer.Assign(name, value)
	}
	return fmt.Errorf("%q is not declared", name)
}

func (e *Environment) Get(name string) (Value, bool) {
	if binding, exists := e.store[name]; exists {
		return binding.Value, true
	}
	if e.outer != nil {
		return e.outer.Get(name)
	}
	return nil, false
}

// Delete removes the named binding from the first scope that
// contains it. Returns true when a binding was found and removed.
// Used by `del x` to retire a binding after its destructor has
// fired.
func (e *Environment) Delete(name string) bool {
	if _, exists := e.store[name]; exists {
		delete(e.store, name)
		if e.typeBindings != nil {
			delete(e.typeBindings, name)
		}
		return true
	}
	if e.outer != nil {
		return e.outer.Delete(name)
	}
	return false
}

func (e *Environment) DefineTypeBinding(name, typeName string) {
	if e.typeBindings == nil {
		e.typeBindings = map[string]string{}
	}
	if _, exists := e.typeBindings[name]; !exists {
		e.typeBindings[name] = typeName
	}
}

func (e *Environment) GetTypeBinding(name string) (string, bool) {
	if e == nil {
		return "", false
	}
	if e.typeBindings != nil {
		if t, ok := e.typeBindings[name]; ok {
			return t, true
		}
	}
	if e.outer != nil {
		return e.outer.GetTypeBinding(name)
	}
	return "", false
}

// TypeBindingNames returns all type-binding names reachable through
// this environment chain. Order is unspecified; callers should not
// rely on it.
func (e *Environment) TypeBindingNames() []string {
	if e == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var names []string
	for env := e; env != nil; env = env.outer {
		for name := range env.typeBindings {
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	return names
}

func (e *Environment) Names() []string {
	names := make([]string, 0, len(e.store))
	for name := range e.store {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (e *Environment) VisibleNames() []string {
	seen := map[string]bool{}
	var names []string
	for env := e; env != nil; env = env.outer {
		for name := range env.store {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
