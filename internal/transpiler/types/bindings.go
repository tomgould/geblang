package types

type Binding struct {
	Name    string
	Type    *Type
	Mutable bool
	// IsParam marks a function parameter; Go passes slice headers by value, so
	// a list mutation through a parameter is not visible to the caller.
	IsParam bool
}

type Scope struct {
	parent   *Scope
	bindings map[string]*Binding
}

func NewScope() *Scope {
	return &Scope{bindings: map[string]*Binding{}}
}

func (s *Scope) Child() *Scope {
	return &Scope{parent: s, bindings: map[string]*Binding{}}
}

func (s *Scope) Parent() *Scope { return s.parent }

func (s *Scope) Define(b *Binding) {
	s.bindings[b.Name] = b
}

func (s *Scope) Lookup(name string) (*Binding, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if b, ok := cur.bindings[name]; ok {
			return b, true
		}
	}
	return nil, false
}

func (s *Scope) LocalNames() []string {
	out := make([]string, 0, len(s.bindings))
	for k := range s.bindings {
		out = append(out, k)
	}
	return out
}
