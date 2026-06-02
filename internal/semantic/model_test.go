package semantic_test

import (
	"testing"

	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/semantic"
)

func TestExtractModelCapturesClassShape(t *testing.T) {
	input := `interface Greeter {
    func greet(): string;
}

@Service
class Base {
    string id;
    func ping(): bool { return true; }
}

class User extends Base implements Greeter {
    func greet(): string { return "hi"; }
    func __call(string m, list<any> a): any { return null; }
}
`
	p := parser.New(lexer.New(input))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	model := semantic.ExtractModel(program)

	base, ok := model.Classes["Base"]
	if !ok {
		t.Fatalf("Base not extracted")
	}
	if !base.Decorated {
		t.Errorf("Base should be marked decorated (@Service)")
	}
	if !base.Methods["ping"] {
		t.Errorf("Base.ping method missing: %v", base.Methods)
	}
	if !base.Fields["id"] {
		t.Errorf("Base.id field missing: %v", base.Fields)
	}

	user, ok := model.Classes["User"]
	if !ok {
		t.Fatalf("User not extracted")
	}
	if user.Parent != "Base" {
		t.Errorf("User.Parent: got %q, want Base", user.Parent)
	}
	if len(user.Implements) != 1 || user.Implements[0] != "Greeter" {
		t.Errorf("User.Implements: got %v, want [Greeter]", user.Implements)
	}
	if !user.Methods["greet"] {
		t.Errorf("User.greet missing")
	}
	if !user.HasCall {
		t.Errorf("User should be marked HasCall (__call present)")
	}

	iface, ok := model.Interfaces["Greeter"]
	if !ok {
		t.Fatalf("Greeter interface not extracted")
	}
	if !iface.Methods["greet"] {
		t.Errorf("Greeter.greet missing: %v", iface.Methods)
	}
}
