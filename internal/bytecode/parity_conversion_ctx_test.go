package bytecode_test

import (
	"bytes"
	"io"
	"testing"

	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// Followup #1: json/yaml/toml stringify of an instance dispatches __serialize through the calling backend's own context on both backends (not the public-field fallback).
func TestParityStringifyInstanceSerialize(t *testing.T) {
	runParity(t, `import io;
import json;
class Box {
    int v;
    func Box(int v) { this.v = v; }
    func __serialize(): dict<string, any> { return {"v": this.v}; }
}
io.println(json.stringify(Box(5)));
`, "{\"v\":5}\n")

	runParity(t, `import io;
import yaml;
class Box {
    int v;
    func Box(int v) { this.v = v; }
    func __serialize(): dict<string, any> { return {"v": this.v}; }
}
io.println(yaml.stringify(Box(7)));
`, "v: 7\n")
}

// Followup #1: an instance whose __serialize calls a builtin serializes through its OWN evaluator even when a second independent evaluator exists. Under the old process-global, the last-constructed evaluator hijacked the callback and serialization failed with "unknown method".
func TestSerializeOwnershipIndependentEvaluators(t *testing.T) {
	src := `import io;
import json;
class Box {
    int v;
    func Box(int v) { this.v = v; }
    func __serialize(): dict<string, any> {
        io.println("ser");
        return {"v": this.v};
    }
}
io.println(json.stringify(Box(5)));
`
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	var outA bytes.Buffer
	a := evaluator.NewWithArgsAndModulePaths(&outA, nil, nil)
	// Construct a second independent evaluator AFTER A; the old global would now own serialization.
	_ = evaluator.NewWithArgsAndModulePaths(io.Discard, nil, nil)
	if _, err := a.Eval(program); err != nil {
		t.Fatalf("evaluator A serialize: %v", err)
	}
	if got, want := outA.String(), "ser\n{\"v\":5}\n"; got != want {
		t.Fatalf("evaluator A output: got %q, want %q", got, want)
	}
}
