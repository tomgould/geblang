// Package desugar rewrites decorators into plain AST before either backend
// compiles or evaluates, so both share one expansion.
package desugar

import (
	"fmt"
	"strings"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// Dataclasses synthesizes a constructor, __eq, __string, and with() for each
// @dataclass (frozen also makes instances immutable). User members win. Idempotent.
func Dataclasses(program *ast.Program) error {
	for _, stmt := range program.Statements {
		if class := classFromStatement(stmt); class != nil {
			if err := desugarDataclass(class); err != nil {
				return err
			}
		}
	}
	return nil
}

func classFromStatement(stmt ast.Statement) *ast.ClassStatement {
	switch s := stmt.(type) {
	case *ast.ClassStatement:
		return s
	case *ast.ExportStatement:
		if c, ok := s.Statement.(*ast.ClassStatement); ok {
			return c
		}
	}
	return nil
}

type dataclassField struct{ name, typ, def string }

func desugarDataclass(class *ast.ClassStatement) error {
	dec, ok := dataclassDecorator(class)
	if !ok || class.DataclassDesugared {
		return nil
	}
	class.DataclassDesugared = true
	frozen, err := dataclassFrozen(dec)
	if err != nil {
		return fmt.Errorf("@dataclass %s: %w", class.Name.Value, err)
	}

	fields := dataclassFields(class)
	defined := memberMethodNames(class)

	var src strings.Builder
	if !defined[strings.ToLower(class.Name.Value)] && class.Extends == nil {
		src.WriteString(genConstructor(class.Name.Value, fields))
	}
	if !defined["__eq"] {
		src.WriteString(genEq(class.Name.Value, fields))
	}
	if !defined["__string"] {
		src.WriteString(genString(class.Name.Value, fields))
	}
	if !defined["with"] {
		src.WriteString(genWith(class.Name.Value, fields))
	}

	members, err := parseMembers(src.String())
	if err != nil {
		return fmt.Errorf("@dataclass %s: %w", class.Name.Value, err)
	}
	class.Members = append(class.Members, members...)

	if frozen {
		addImmutableDecorator(class)
	}
	return nil
}

func dataclassDecorator(class *ast.ClassStatement) (ast.Decorator, bool) {
	for _, d := range class.Decorators {
		if d.Name != nil && d.Name.Value == "dataclass" {
			return d, true
		}
	}
	return ast.Decorator{}, false
}

func dataclassFrozen(dec ast.Decorator) (bool, error) {
	for _, arg := range dec.Arguments {
		if arg.Name == nil || arg.Name.Value != "frozen" {
			continue
		}
		switch arg.Value.String() {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return false, fmt.Errorf("frozen must be a boolean literal")
		}
	}
	return false, nil
}

func dataclassFields(class *ast.ClassStatement) []dataclassField {
	var out []dataclassField
	for _, m := range class.Members {
		d, ok := m.(*ast.DeclarationStatement)
		if !ok || strings.HasPrefix(d.Kind, "static") {
			continue
		}
		f := dataclassField{name: d.Name.Value, typ: "any"}
		if d.Type != nil {
			f.typ = d.Type.String()
		}
		if d.Value != nil {
			f.def = d.Value.String()
		}
		out = append(out, f)
	}
	return out
}

func memberMethodNames(class *ast.ClassStatement) map[string]bool {
	names := map[string]bool{}
	for _, m := range class.Members {
		if fn, ok := m.(*ast.FunctionStatement); ok && fn.Name != nil {
			names[strings.ToLower(fn.Name.Value)] = true
		}
	}
	return names
}

func genConstructor(name string, fields []dataclassField) string {
	params := make([]string, len(fields))
	assigns := make([]string, len(fields))
	for i, f := range fields {
		params[i] = f.typ + " " + f.name
		if f.def != "" {
			params[i] += " = " + f.def
		}
		assigns[i] = "this." + f.name + " = " + f.name + ";"
	}
	return "func " + name + "(" + strings.Join(params, ", ") + ") { " + strings.Join(assigns, " ") + " }\n"
}

func genEq(name string, fields []dataclassField) string {
	if len(fields) == 0 {
		return "func __eq(any other): bool { return other instanceof " + name + "; }\n"
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = "this." + f.name + " == o." + f.name
	}
	return "func __eq(any other): bool { if (!(other instanceof " + name + ")) { return false; } let o = other as " + name + "; return " + strings.Join(parts, " && ") + "; }\n"
}

func genString(name string, fields []dataclassField) string {
	if len(fields) == 0 {
		return "func __string(): string { return \"" + name + "()\"; }\n"
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = "\"" + f.name + "=\" + (this." + f.name + " as string)"
	}
	body := "\"" + name + "(\" + " + strings.Join(parts, " + \", \" + ") + " + \")\""
	return "func __string(): string { return " + body + "; }\n"
}

func genWith(name string, fields []dataclassField) string {
	args := make([]string, len(fields))
	for i, f := range fields {
		args[i] = "changes.contains(\"" + f.name + "\") ? changes[\"" + f.name + "\"] as " + f.typ + " : this." + f.name
	}
	return "func with(dict<string, any> changes): " + name + " { return " + name + "(" + strings.Join(args, ", ") + "); }\n"
}

func addImmutableDecorator(class *ast.ClassStatement) {
	for _, d := range class.Decorators {
		if d.Name != nil && d.Name.Value == "immutable" && len(d.Arguments) == 0 {
			return
		}
	}
	class.Decorators = append(class.Decorators, ast.Decorator{Name: &ast.Identifier{Value: "immutable"}})
}

func parseMembers(src string) ([]ast.Statement, error) {
	if strings.TrimSpace(src) == "" {
		return nil, nil
	}
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return prog.Statements, nil
}
