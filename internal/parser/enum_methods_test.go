package parser_test

import (
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

func parseEnumOnly(t *testing.T, input string) *ast.EnumStatement {
	t.Helper()
	p := parser.New(lexer.New(input))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	if len(program.Statements) != 1 {
		t.Fatalf("statement count: got %d, want 1", len(program.Statements))
	}
	stmt, ok := program.Statements[0].(*ast.EnumStatement)
	if !ok {
		t.Fatalf("got %T, want *ast.EnumStatement", program.Statements[0])
	}
	return stmt
}

func TestParseBareEnumBackCompat(t *testing.T) {
	stmt := parseEnumOnly(t, `enum Color { Red, Green, Blue }`)
	if len(stmt.Variants) != 3 {
		t.Fatalf("variants: got %d, want 3", len(stmt.Variants))
	}
	if len(stmt.Methods) != 0 || len(stmt.Implements) != 0 {
		t.Fatalf("bare enum should have no methods/implements")
	}
}

func TestParseEnumWithAssociatedValuesBackCompat(t *testing.T) {
	stmt := parseEnumOnly(t, `enum Status { Active, Closed(string) }`)
	if len(stmt.Variants) != 2 {
		t.Fatalf("variants: got %d, want 2", len(stmt.Variants))
	}
	if len(stmt.Variants[1].FieldTypes) != 1 {
		t.Fatalf("Closed should carry one field type")
	}
}

func TestParseEnumWithMethods(t *testing.T) {
	stmt := parseEnumOnly(t, `enum Status {
		Active, Closed(string);

		func isTerminal(): bool { return false; }
		func describe(): string { return "x"; }
	}`)
	if len(stmt.Variants) != 2 {
		t.Fatalf("variants: got %d, want 2", len(stmt.Variants))
	}
	if len(stmt.Methods) != 2 {
		t.Fatalf("methods: got %d, want 2", len(stmt.Methods))
	}
	if stmt.Methods[0].Name.Value != "isTerminal" {
		t.Fatalf("method 0 name: got %q", stmt.Methods[0].Name.Value)
	}
}

func TestParseEnumImplements(t *testing.T) {
	stmt := parseEnumOnly(t, `enum Status implements Describable, Named {
		Active;
		func describe(): string { return "x"; }
	}`)
	if len(stmt.Implements) != 2 {
		t.Fatalf("implements: got %d, want 2", len(stmt.Implements))
	}
	if stmt.Implements[0].Name != "Describable" || stmt.Implements[1].Name != "Named" {
		t.Fatalf("implements names: %q %q", stmt.Implements[0].Name, stmt.Implements[1].Name)
	}
}
