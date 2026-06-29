package runtime

import "strings"

// ClassImplementsInterface walks the class parent chain and each
// class's interface graph for a name match.
func ClassImplementsInterface(class *Class, name string) bool {
	for current := class; current != nil; current = current.Parent {
		for _, iface := range current.Implements {
			if InterfaceMatches(iface, name) {
				return true
			}
		}
	}
	return false
}

// InterfaceMatches reports whether iface or any of its parents has name.
func InterfaceMatches(iface *Interface, name string) bool {
	if iface == nil {
		return false
	}
	if strings.EqualFold(iface.Name, name) {
		return true
	}
	for _, parent := range iface.Parents {
		if InterfaceMatches(parent, name) {
			return true
		}
	}
	return false
}

// ValueSatisfiesHierarchyLeaf reports whether value's class hierarchy
// (parent chain, implemented interfaces, error parent names) satisfies
// the bare type name leaf. Both backends' element-tag write barriers
// consult this after the name-level check misses.
func ValueSatisfiesHierarchyLeaf(value Value, leaf string) bool {
	switch v := value.(type) {
	case *Instance:
		for c := v.Class; c != nil; c = c.Parent {
			if strings.EqualFold(c.Name, leaf) || ClassImplementsInterface(c, leaf) {
				return true
			}
		}
	case Error:
		if strings.EqualFold(v.Class, leaf) {
			return true
		}
		for _, p := range v.Parents {
			if strings.EqualFold(p, leaf) {
				return true
			}
		}
	case EnumVariant:
		if v.Enum != nil {
			if strings.EqualFold(v.Enum.Name, leaf) {
				return true
			}
			for _, iface := range v.Enum.Implements {
				if strings.EqualFold(iface, leaf) {
					return true
				}
			}
		}
	}
	return false
}
