package lsp

import (
	"strings"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// ---- declaration (alias of definition) ----

// declaration handles textDocument/declaration with single-file scope.
// In Geblang a symbol has exactly one place it's introduced - there is
// no separate forward-declaration/definition split like C's header vs.
// source file - so a symbol's declaration IS its definition. Rather
// than duplicate findDefinition's logic, this is a thin alias that
// returns exactly what the definition handler returns for the same
// params.
func (s *server) declaration(params TextDocumentPositionParams) any {
	return s.definition(params)
}

// ---- typeDefinition ----

// typeDefinition handles textDocument/typeDefinition with single-file
// scope. It resolves the identifier under the cursor to its declared
// type name using only unambiguous syntactic evidence - a `Type name`
// parameter or a typed `Type name = ...;` / `Type name;` declaration -
// and then jumps to that type's own definition (a class/interface/enum
// of the same name).
//
// Resolution is deliberately conservative and correct-or-nothing:
// inferred bindings (`let x = ...;`), untyped bindings, and identifiers
// that aren't parameters or typed declarations at all return an empty
// result rather than a guess. A resolved type name that doesn't match
// any class/interface/enum defined in this file (built-ins, primitives,
// imported types) also returns empty - this server has no cross-file
// type index to jump into safely.
func (s *server) typeDefinition(params TextDocumentPositionParams) any {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return []Location{}
	}
	word := wordAtPosition(source, params.Position.Line, params.Position.Character)
	if word == "" {
		return []Location{}
	}
	prog := parser.New(lexer.New(source)).ParseProgram()
	if prog == nil {
		return []Location{}
	}
	typeName := declaredTypeName(prog, word)
	if typeName == "" {
		return []Location{}
	}
	defLine := findTypeDefinition(source, typeName)
	if defLine < 0 {
		return []Location{}
	}
	lines := strings.Split(source, "\n")
	col := 0
	if defLine < len(lines) {
		if idx := strings.Index(lines[defLine], typeName); idx >= 0 {
			col = idx
		}
	}
	return []Location{{
		URI: params.TextDocument.URI,
		Range: Range{
			Start: Position{Line: defLine, Character: col},
			End:   Position{Line: defLine, Character: col + len(typeName)},
		},
	}}
}

// declaredTypeName searches prog for a parameter or declaration named
// name that carries an explicit, simple type annotation, and returns
// that type's name. It returns "" when no such unambiguous declaration
// exists: name isn't found, name is bound with an inferred `let`/`const`
// (no Type), or the declared type isn't a plain named type (nullable,
// generic-argumented, list-alias, or a union/intersection) - those
// shapes aren't a single unambiguous type definition to jump to.
func declaredTypeName(prog *ast.Program, name string) string {
	var found string
	var visitStmt func(stmt ast.Statement)
	var visitBlock func(block *ast.BlockStatement)

	checkTyped := func(declName string, typ *ast.TypeRef) {
		if found != "" || declName != name {
			return
		}
		if tn := simpleTypeName(typ); tn != "" {
			found = tn
		}
	}

	visitBlock = func(block *ast.BlockStatement) {
		if block == nil {
			return
		}
		for _, stmt := range block.Statements {
			visitStmt(stmt)
		}
	}

	visitStmt = func(stmt ast.Statement) {
		if found != "" || stmt == nil {
			return
		}
		switch st := stmt.(type) {
		case *ast.ExportStatement:
			visitStmt(st.Statement)
		case *ast.DeclarationStatement:
			if st.Name != nil {
				checkTyped(st.Name.Value, st.Type)
			}
		case *ast.FunctionStatement:
			for _, p := range st.Parameters {
				if p.Name != nil {
					checkTyped(p.Name.Value, p.Type)
				}
			}
			visitBlock(st.Body)
		case *ast.ClassStatement:
			for _, member := range st.Members {
				visitStmt(member)
			}
			if st.Destructor != nil {
				visitBlock(st.Destructor.Body)
			}
		case *ast.InterfaceStatement:
			for _, field := range st.Fields {
				if field.Name != nil {
					checkTyped(field.Name.Value, field.Type)
				}
			}
			for _, def := range st.Defaults {
				visitStmt(def)
			}
		case *ast.EnumStatement:
			for _, m := range st.Methods {
				visitStmt(m)
			}
		case *ast.IfStatement:
			visitBlock(st.Consequence)
			for _, clause := range st.ElseIfs {
				visitBlock(clause.Body)
			}
			visitBlock(st.Alternative)
		case *ast.WhileStatement:
			visitBlock(st.Body)
		case *ast.WithStatement:
			visitBlock(st.Body)
		case *ast.ForStatement:
			if st.VarName != nil {
				checkTyped(st.VarName.Value, st.VarType)
			}
			visitStmt(st.Init)
			visitStmt(st.Update)
			visitBlock(st.Body)
		case *ast.TryStatement:
			visitBlock(st.Body)
			for _, c := range st.Catches {
				visitBlock(c.Body)
			}
			visitBlock(st.Finally)
		case *ast.MatchStatement:
			for _, c := range st.Cases {
				visitBlock(c.Body)
			}
		case *ast.SelectStatement:
			for _, c := range st.Cases {
				visitBlock(c.Body)
			}
			visitBlock(st.Default)
		case *ast.BlockStatement:
			visitBlock(st)
		}
	}

	for _, stmt := range prog.Statements {
		visitStmt(stmt)
		if found != "" {
			break
		}
	}
	return found
}

// simpleTypeName returns t's name when t is a plain named type - not
// nullable, not generic-argumented, not a list alias, and not a union
// or intersection - and the name looks like a user type (starts
// uppercase, as Geblang's built-in/primitive types are lowercase:
// string, int, float, bool, void, any, dict, list, etc.). Every other
// shape returns "" because there is no single unambiguous type to jump
// to (a `T | U` union has two candidate definitions; `list<T>` names a
// built-in container, not a user type; primitives have no definition
// site at all).
func simpleTypeName(t *ast.TypeRef) string {
	if t == nil {
		return ""
	}
	if t.Nullable || t.Operator != "" || len(t.Arguments) > 0 || t.ListAlias {
		return ""
	}
	if t.Name == "" || strings.Contains(t.Name, ".") {
		return ""
	}
	r := t.Name[0]
	if r < 'A' || r > 'Z' {
		return ""
	}
	return t.Name
}

// findTypeDefinition returns the line (0-based) where a class,
// interface, or enum named typeName is declared in source, or -1.
func findTypeDefinition(source, typeName string) int {
	syms := extractSymbols(source)
	for _, sym := range syms {
		if sym.name != typeName {
			continue
		}
		switch sym.kind {
		case symbolKindClass, symbolKindInterface, symbolKindEnum:
			return sym.line - 1 // convert to 0-based
		}
	}
	return -1
}

// ---- implementation ----

// implementation handles textDocument/implementation with single-file
// scope. If the identifier under the cursor names a class or interface
// declared in this document, it returns the declaration site of every
// class/interface/enum in the document whose `extends`/`implements`
// clause references that name. Anything else - the identifier isn't a
// type, or no declaration in scope references it - returns an empty
// result.
func (s *server) implementation(params TextDocumentPositionParams) any {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return []Location{}
	}
	word := wordAtPosition(source, params.Position.Line, params.Position.Character)
	if word == "" {
		return []Location{}
	}
	if !isUserType(source, word) {
		return []Location{}
	}
	prog := parser.New(lexer.New(source)).ParseProgram()
	if prog == nil {
		return []Location{}
	}
	lines := strings.Split(source, "\n")
	out := []Location{}
	for _, stmt := range prog.Statements {
		out = append(out, implementersOf(stmt, word, lines, params.TextDocument.URI)...)
	}
	return out
}

// isUserType reports whether name is a class, interface, or enum
// declared in source.
func isUserType(source, name string) bool {
	for _, sym := range extractSymbols(source) {
		if sym.name != name {
			continue
		}
		switch sym.kind {
		case symbolKindClass, symbolKindInterface, symbolKindEnum:
			return true
		}
	}
	return false
}

// implementersOf reports, for a single top-level statement, whether it
// declares a class/interface/enum whose supertype list (extends and/or
// implements, as applicable to that statement kind) references
// typeName. ExportStatement is unwrapped transparently since exporting
// a declaration doesn't change its inheritance.
func implementersOf(stmt ast.Statement, typeName string, lines []string, uri string) []Location {
	switch st := stmt.(type) {
	case *ast.ExportStatement:
		return implementersOf(st.Statement, typeName, lines, uri)
	case *ast.ClassStatement:
		if st.Name == nil {
			return nil
		}
		if typeRefListNames(st.Extends) == typeName || containsTypeName(st.Implements, typeName) {
			return []Location{symbolLocation(st.Name.Value, st.Token.Line, lines, uri)}
		}
	case *ast.InterfaceStatement:
		if st.Name == nil {
			return nil
		}
		if containsTypeName(st.Parents, typeName) {
			return []Location{symbolLocation(st.Name.Value, st.Token.Line, lines, uri)}
		}
	case *ast.EnumStatement:
		if st.Name == nil {
			return nil
		}
		if containsTypeName(st.Implements, typeName) {
			return []Location{symbolLocation(st.Name.Value, st.Token.Line, lines, uri)}
		}
	}
	return nil
}

// typeRefListNames returns t's simple name, or "" for nil/complex refs.
// Used for the single Extends type on a class.
func typeRefListNames(t *ast.TypeRef) string {
	if t == nil {
		return ""
	}
	return t.Name
}

// containsTypeName reports whether any TypeRef in refs has the exact
// simple name typeName.
func containsTypeName(refs []*ast.TypeRef, typeName string) bool {
	for _, r := range refs {
		if r != nil && r.Name == typeName {
			return true
		}
	}
	return false
}

// symbolLocation builds the Location for a top-level symbol name whose
// declaration keyword starts on line (1-based, from the statement's
// token), reusing the same name-search-on-line approach as findDefinition/
// definition.
func symbolLocation(name string, line int, lines []string, uri string) Location {
	l := line - 1
	col := 0
	if l >= 0 && l < len(lines) {
		if idx := strings.Index(lines[l], name); idx >= 0 {
			col = idx
		}
	}
	return Location{
		URI: uri,
		Range: Range{
			Start: Position{Line: l, Character: col},
			End:   Position{Line: l, Character: col + len(name)},
		},
	}
}
