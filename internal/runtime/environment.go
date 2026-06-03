package runtime

import (
	"fmt"
	"sort"
	"sync"
)

type Binding struct {
	Value    Value
	Constant bool
	// Imported marks a name brought in by `from M import X`. Such a name is
	// immutable: it cannot be locally redeclared or overloaded. ImportSource
	// is "module.symbol", so re-importing the same symbol is idempotent.
	Imported     bool
	ImportSource string
}

// Environment is the evaluator's lexical scope. The mutex guards
// concurrent access from async goroutines that share a closure
// environment with the parent evaluator.
type Environment struct {
	mu           sync.RWMutex
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
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.store[name]; exists {
		return fmt.Errorf("%q is already declared in this scope", name)
	}
	if constant {
		value = ShallowFreeze(value)
	}
	e.store[name] = Binding{Value: value, Constant: constant}
	return nil
}

// DefineImported binds a from-imported name. Re-importing the same symbol is
// idempotent; any other existing binding (or a different import) is a clash.
func (e *Environment) DefineImported(name string, value Value, source string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if existing, exists := e.store[name]; exists && !(existing.Imported && existing.ImportSource == source) {
		return fmt.Errorf("%q is already declared in this scope", name)
	}
	e.store[name] = Binding{Value: ShallowFreeze(value), Constant: true, Imported: true, ImportSource: source}
	return nil
}

func (e *Environment) DefineFunction(name string, fn Function) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if binding, exists := e.store[name]; exists {
		if binding.Imported {
			return fmt.Errorf("%q is already declared in this scope", name)
		}
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
	e.mu.Lock()
	if binding, exists := e.store[name]; exists {
		if binding.Constant {
			e.mu.Unlock()
			return fmt.Errorf("cannot assign to constant %q", name)
		}
		binding.Value = value
		e.store[name] = binding
		e.mu.Unlock()
		return nil
	}
	outer := e.outer
	e.mu.Unlock()
	if outer != nil {
		return outer.Assign(name, value)
	}
	return fmt.Errorf("%q is not declared", name)
}

func (e *Environment) Get(name string) (Value, bool) {
	e.mu.RLock()
	if binding, exists := e.store[name]; exists {
		e.mu.RUnlock()
		return binding.Value, true
	}
	outer := e.outer
	e.mu.RUnlock()
	if outer != nil {
		return outer.Get(name)
	}
	return nil, false
}

// Delete removes the named binding from the first scope that
// contains it. Returns true when a binding was found and removed.
// Used by `del x` to retire a binding after its destructor has
// fired.
func (e *Environment) Delete(name string) bool {
	e.mu.Lock()
	if _, exists := e.store[name]; exists {
		delete(e.store, name)
		if e.typeBindings != nil {
			delete(e.typeBindings, name)
		}
		e.mu.Unlock()
		return true
	}
	outer := e.outer
	e.mu.Unlock()
	if outer != nil {
		return outer.Delete(name)
	}
	return false
}

func (e *Environment) DefineTypeBinding(name, typeName string) {
	e.mu.Lock()
	defer e.mu.Unlock()
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
	e.mu.RLock()
	if e.typeBindings != nil {
		if t, ok := e.typeBindings[name]; ok {
			e.mu.RUnlock()
			return t, true
		}
	}
	outer := e.outer
	e.mu.RUnlock()
	if outer != nil {
		return outer.GetTypeBinding(name)
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
		env.mu.RLock()
		for name := range env.typeBindings {
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		env.mu.RUnlock()
	}
	return names
}

func (e *Environment) Names() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
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
		env.mu.RLock()
		for name := range env.store {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
		env.mu.RUnlock()
	}
	sort.Strings(names)
	return names
}
