package native_test

import (
	"testing"

	"geblang/internal/native"
	"geblang/internal/runtime"
)

func TestRegistryCall(t *testing.T) {
	registry := native.NewRegistry()
	registry.Register("demo", "id", func(args []runtime.Value) (runtime.Value, error) {
		return args[0], nil
	})

	got, err := registry.Call("demo", "id", []runtime.Value{runtime.String{Value: "ok"}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.Inspect() != "ok" {
		t.Fatalf("got %q", got.Inspect())
	}
}

func TestPureBuiltinMetadata(t *testing.T) {
	if !native.IsPureBuiltin("bytes", "fromString") {
		t.Fatal("bytes.fromString should be marked as a pure builtin")
	}
	if native.IsPureBuiltin("io", "readText") {
		t.Fatal("io.readText should not be marked as a pure builtin")
	}
}

func TestValidateXML(t *testing.T) {
	if !native.ValidateXML(`<root><child /></root>`) {
		t.Fatal("valid XML was rejected")
	}
	if native.ValidateXML(`<root><child></root>`) {
		t.Fatal("invalid XML was accepted")
	}
	if native.ValidateXML(`<a></a><b></b>`) {
		t.Fatal("multiple XML roots were accepted")
	}
}

func TestParseErrorValue(t *testing.T) {
	parseErr := native.NewParseError("bad data", "{\n  bad", 5)
	value := native.ParseErrorValue(parseErr)
	dict := value.(runtime.Dict)
	if dict.Entries["string:\"message\""].Value.Inspect() != "bad data" {
		t.Fatalf("message: got %q", dict.Entries["string:\"message\""].Value.Inspect())
	}
	if dict.Entries["string:\"line\""].Value.Inspect() != "2" {
		t.Fatalf("line: got %q", dict.Entries["string:\"line\""].Value.Inspect())
	}
}
