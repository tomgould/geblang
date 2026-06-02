package semantic

import (
	"strings"

	"geblang/internal/ast"
)

// ClassDecl is the resolved shape of a class used by cross-module
// method-existence checks. Method/field names are stored lowercased
// (dispatch is case-insensitive at storage level).
type ClassDecl struct {
	Name       string
	Parent     string   // declared parent class name, or ""
	Implements []string // implemented interface names
	Methods    map[string]bool
	Fields     map[string]bool
	// HasCall marks a `__call` method (dynamic dispatch): callers must
	// not flag unknown methods on such a class.
	HasCall bool
	// Decorated marks class-level decorators, which may inject members:
	// callers must bail rather than risk a false positive.
	Decorated bool
}

// InterfaceDecl is the resolved shape of an interface. Methods include
// both abstract signatures and default-implementation names; an
// instance responds to all of them.
type InterfaceDecl struct {
	Name    string
	Parents []string
	Methods map[string]bool
	Fields  map[string]bool
}

// ModuleModel is the class/interface surface of one parsed module.
type ModuleModel struct {
	Classes    map[string]ClassDecl
	Interfaces map[string]InterfaceDecl
}

// ExtractModel walks a parsed program and returns its class/interface
// declarations. Pure (no IO); the check package calls it per module to
// build a cross-module class graph. Keys are the declared names (as
// written); member sets are lowercased.
func ExtractModel(program *ast.Program) ModuleModel {
	model := ModuleModel{
		Classes:    map[string]ClassDecl{},
		Interfaces: map[string]InterfaceDecl{},
	}
	if program == nil {
		return model
	}
	var walk func(stmt ast.Statement)
	walk = func(stmt ast.Statement) {
		switch s := stmt.(type) {
		case *ast.ExportStatement:
			walk(s.Statement)
		case *ast.ClassStatement:
			if s.Name == nil {
				return
			}
			decl := ClassDecl{
				Name:      s.Name.Value,
				Methods:   map[string]bool{},
				Fields:    map[string]bool{},
				Decorated: len(s.Decorators) > 0,
			}
			if s.Extends != nil {
				decl.Parent = s.Extends.Name
			}
			for _, iface := range s.Implements {
				decl.Implements = append(decl.Implements, iface.Name)
			}
			for _, member := range s.Members {
				switch m := member.(type) {
				case *ast.FunctionStatement:
					if m.Name == nil {
						continue
					}
					name := strings.ToLower(m.Name.Value)
					decl.Methods[name] = true
					if name == "__call" {
						decl.HasCall = true
					}
				case *ast.DeclarationStatement:
					if m.Name != nil {
						decl.Fields[strings.ToLower(m.Name.Value)] = true
					}
				}
			}
			model.Classes[s.Name.Value] = decl
		case *ast.InterfaceStatement:
			if s.Name == nil {
				return
			}
			decl := InterfaceDecl{Name: s.Name.Value, Methods: map[string]bool{}, Fields: map[string]bool{}}
			for _, parent := range s.Parents {
				decl.Parents = append(decl.Parents, parent.Name)
			}
			for _, method := range s.Methods {
				if method.Name != nil {
					decl.Methods[strings.ToLower(method.Name.Value)] = true
				}
			}
			for _, def := range s.Defaults {
				if def.Name != nil {
					decl.Methods[strings.ToLower(def.Name.Value)] = true
				}
			}
			for _, field := range s.Fields {
				if field.Name != nil {
					decl.Fields[strings.ToLower(field.Name.Value)] = true
				}
			}
			model.Interfaces[s.Name.Value] = decl
		}
	}
	for _, stmt := range program.Statements {
		walk(stmt)
	}
	return model
}
