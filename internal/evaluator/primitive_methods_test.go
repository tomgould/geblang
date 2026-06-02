package evaluator_test

import (
	"io"
	"strings"
	"testing"

	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/native"
	"geblang/internal/parser"
)

// primitiveLiterals gives a constructor expression for a value of each
// primitive type, used to exercise its methods.
var primitiveLiterals = map[string]string{
	"string":  `"abc"`,
	"list":    `[1, 2, 3]`,
	"dict":    `{"a": 1, "b": 2}`,
	"set":     `[1, 2, 3] as set`,
	"bytes":   `"abc" as bytes`,
	"range":   `0..3`,
	"int":     `42`,
	"decimal": `1.5`,
	"float":   `1.5f`,
	"bool":    `true`,
}

// TestPrimitiveMethodsAreCallable verifies every name in
// native.PrimitiveMethods is recognised by the runtime for its type.
// Calling with no/empty args may raise an arity or value error - those
// are fine; only an "unknown/no method" error means the entry is a
// phantom that must be removed.
func TestPrimitiveMethodsAreCallable(t *testing.T) {
	for typeName, methods := range native.PrimitiveMethods {
		literal, ok := primitiveLiterals[typeName]
		if !ok {
			t.Fatalf("no test literal for primitive type %q", typeName)
		}
		for _, method := range methods {
			src := "let v = " + literal + ";\nv." + method + "();\n"
			err := runSnippet(src)
			if err != nil && isUnknownMethodError(err.Error()) {
				t.Errorf("%s.%s listed in PrimitiveMethods but not recognised: %v", typeName, method, err)
			}
		}
	}
}

// TestPrimitiveMethodsRegistryIsComplete is the completeness tripwire:
// it probes the full vocabulary of known primitive method names against
// a value of each type, and fails if any name the engine RECOGNISES on
// that type is absent from native.PrimitiveMethods for it. This catches
// the "method added to the dispatch but not the registry" drift that
// the per-entry callable check cannot. (It cannot catch a wholly novel
// name present in no type's list, but such a name would also be absent
// from dir/completion, making the gap self-evident.)
func TestPrimitiveMethodsRegistryIsComplete(t *testing.T) {
	vocab := map[string]bool{}
	for _, methods := range native.PrimitiveMethods {
		for _, m := range methods {
			vocab[m] = true
		}
	}
	for _, m := range native.PrimitiveConversionMethods {
		vocab[m] = true
	}

	for typeName, literal := range primitiveLiterals {
		listed := map[string]bool{}
		for _, m := range native.PrimitiveMethods[typeName] {
			listed[strings.ToLower(m)] = true
		}
		for _, m := range native.PrimitiveConversionMethods {
			listed[strings.ToLower(m)] = true
		}
		for candidate := range vocab {
			if listed[strings.ToLower(candidate)] {
				continue
			}
			// A non-"unknown method" outcome means the engine recognises
			// candidate on this type, so it must be in the registry.
			err := runSnippet("let v = " + literal + ";\nv." + candidate + "();\n")
			if err == nil || !isUnknownMethodError(err.Error()) {
				t.Errorf("%s.%s is recognised by the engine but missing from native.PrimitiveMethods[%q]", typeName, candidate, typeName)
			}
		}
	}
}

func runSnippet(src string) error {
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return &snippetError{strings.Join(p.Errors(), "; ")}
	}
	_, err := evaluator.New(io.Discard).Eval(program)
	return err
}

func isUnknownMethodError(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "unknown method") ||
		strings.Contains(m, "has no method") ||
		strings.Contains(m, "unknown bytecode function") ||
		strings.Contains(m, "no method named")
}

type snippetError struct{ msg string }

func (e *snippetError) Error() string { return e.msg }
