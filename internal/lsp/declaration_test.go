package lsp

import (
	"testing"
)

func TestDeclarationMatchesDefinitionForFunction(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "func greet(): void {\n    io.println(\"hi\");\n}\ngreet();\n"
	s.docs[uri] = source
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 3, Character: 0}, // "greet" in the call
	}
	declResult := s.declaration(params)
	defResult := s.definition(params)
	declLoc, ok := declResult.(Location)
	if !ok {
		t.Fatalf("expected Location from declaration, got %T", declResult)
	}
	defLoc, ok := defResult.(Location)
	if !ok {
		t.Fatalf("expected Location from definition, got %T", defResult)
	}
	if declLoc != defLoc {
		t.Fatalf("declaration() = %+v, want same as definition() = %+v", declLoc, defLoc)
	}
	want := Location{URI: uri, Range: Range{
		Start: Position{Line: 0, Character: 5},
		End:   Position{Line: 0, Character: 10},
	}}
	if declLoc != want {
		t.Fatalf("declaration() = %+v, want %+v", declLoc, want)
	}
}

func TestDeclarationEmptyForMissingDocument(t *testing.T) {
	s, _ := newTestServer()
	result := s.declaration(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///tmp/missing.gb"},
		Position:     Position{Line: 0, Character: 0},
	})
	if result != nil {
		t.Fatalf("expected nil for missing document, got %+v", result)
	}
}

func TestTypeDefinitionOnTypedParameterJumpsToClass(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "class User {\n    string name;\n}\n\nfunc greet(User u): void {\n    io.println(u.name);\n}\n"
	s.docs[uri] = source
	// Line 4: "func greet(User u): void {" - "u" (the parameter) at char 17.
	result := s.typeDefinition(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 4, Character: 17},
	})
	locs, ok := result.([]Location)
	if !ok {
		t.Fatalf("expected []Location, got %T", result)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d: %+v", len(locs), locs)
	}
	want := Location{URI: uri, Range: Range{
		Start: Position{Line: 0, Character: 6},
		End:   Position{Line: 0, Character: 10},
	}}
	if locs[0] != want {
		t.Fatalf("typeDefinition() = %+v, want %+v", locs[0], want)
	}
}

func TestTypeDefinitionOnTypedVariableJumpsToClass(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "class User {\n    string name;\n}\n\nfunc make(): void {\n    User u = User();\n    io.println(u.name);\n}\n"
	s.docs[uri] = source
	// Line 5: "    User u = User();" - "u" at char 9.
	result := s.typeDefinition(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 5, Character: 9},
	})
	locs, ok := result.([]Location)
	if !ok {
		t.Fatalf("expected []Location, got %T", result)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d: %+v", len(locs), locs)
	}
	want := Location{URI: uri, Range: Range{
		Start: Position{Line: 0, Character: 6},
		End:   Position{Line: 0, Character: 10},
	}}
	if locs[0] != want {
		t.Fatalf("typeDefinition() = %+v, want %+v", locs[0], want)
	}
}

func TestTypeDefinitionOnInferredLetReturnsEmpty(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "func foo(): int { return 1; }\nfunc make(): void {\n    let x = foo();\n    io.println(x);\n}\n"
	s.docs[uri] = source
	// Line 2: "    let x = foo();" - "x" at char 8.
	result := s.typeDefinition(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 2, Character: 8},
	})
	locs, ok := result.([]Location)
	if !ok {
		t.Fatalf("expected []Location, got %T", result)
	}
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations for inferred let, got %d: %+v", len(locs), locs)
	}
}

func TestTypeDefinitionOnPrimitiveTypeReturnsEmpty(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "func make(): void {\n    int count = 0;\n    io.println(count);\n}\n"
	s.docs[uri] = source
	// Line 1: "    int count = 0;" - "count" at char 8.
	result := s.typeDefinition(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 1, Character: 8},
	})
	locs := result.([]Location)
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations for primitive-typed var, got %d: %+v", len(locs), locs)
	}
}

func TestTypeDefinitionEmptyForMissingDocument(t *testing.T) {
	s, _ := newTestServer()
	result := s.typeDefinition(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///tmp/missing.gb"},
		Position:     Position{Line: 0, Character: 0},
	})
	locs, ok := result.([]Location)
	if !ok {
		t.Fatalf("expected []Location, got %T", result)
	}
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations for missing document, got %d: %+v", len(locs), locs)
	}
}

func TestImplementationOnInterfaceFindsImplementingClass(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "interface Shape {\n    func area(): float;\n}\n\nclass Circle implements Shape {\n    float radius;\n    func area(): float { return 0.0; }\n}\n"
	s.docs[uri] = source
	// Line 0: "interface Shape {" - "Shape" at char 10.
	result := s.implementation(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 0, Character: 10},
	})
	locs, ok := result.([]Location)
	if !ok {
		t.Fatalf("expected []Location, got %T", result)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 implementing class, got %d: %+v", len(locs), locs)
	}
	want := Location{URI: uri, Range: Range{
		Start: Position{Line: 4, Character: 6},
		End:   Position{Line: 4, Character: 12},
	}}
	if locs[0] != want {
		t.Fatalf("implementation() = %+v, want %+v", locs[0], want)
	}
}

func TestImplementationOnBaseClassFindsExtendingClass(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "class Animal {\n    func speak(): void {}\n}\n\nclass Dog extends Animal {\n    func speak(): void {}\n}\n"
	s.docs[uri] = source
	// Line 0: "class Animal {" - "Animal" at char 6.
	result := s.implementation(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 0, Character: 6},
	})
	locs, ok := result.([]Location)
	if !ok {
		t.Fatalf("expected []Location, got %T", result)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 extending class, got %d: %+v", len(locs), locs)
	}
	want := Location{URI: uri, Range: Range{
		Start: Position{Line: 4, Character: 6},
		End:   Position{Line: 4, Character: 9},
	}}
	if locs[0] != want {
		t.Fatalf("implementation() = %+v, want %+v", locs[0], want)
	}
}

func TestImplementationOnNonTypeReturnsEmpty(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "let foo = 1;\nio.println(foo);\n"
	s.docs[uri] = source
	result := s.implementation(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 0, Character: 4},
	})
	locs, ok := result.([]Location)
	if !ok {
		t.Fatalf("expected []Location, got %T", result)
	}
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations for non-type identifier, got %d: %+v", len(locs), locs)
	}
}

func TestImplementationOnTypeWithNoImplementersReturnsEmpty(t *testing.T) {
	s, _ := newTestServer()
	uri := "file:///tmp/foo.gb"
	source := "interface Shape {\n    func area(): float;\n}\n"
	s.docs[uri] = source
	result := s.implementation(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: 0, Character: 10},
	})
	locs, ok := result.([]Location)
	if !ok {
		t.Fatalf("expected []Location, got %T", result)
	}
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations for unimplemented interface, got %d: %+v", len(locs), locs)
	}
}

func TestImplementationEmptyForMissingDocument(t *testing.T) {
	s, _ := newTestServer()
	result := s.implementation(TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: "file:///tmp/missing.gb"},
		Position:     Position{Line: 0, Character: 0},
	})
	locs, ok := result.([]Location)
	if !ok {
		t.Fatalf("expected []Location, got %T", result)
	}
	if len(locs) != 0 {
		t.Fatalf("expected 0 locations for missing document, got %d: %+v", len(locs), locs)
	}
}
