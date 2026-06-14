package transpiler_test

import (
	"strings"
	"testing"

	"geblang/internal/token"
	"geblang/internal/transpiler"
)

func TestErrorAtPopulatesFields(t *testing.T) {
	tok := token.Token{Line: 12, Column: 5}
	d := transpiler.ErrorAt("foo.gb", tok, "bad thing", "try X")

	if d.Severity != transpiler.SeverityError {
		t.Errorf("severity: got %v, want SeverityError", d.Severity)
	}
	if d.File != "foo.gb" {
		t.Errorf("file: got %q, want %q", d.File, "foo.gb")
	}
	if d.Line != 12 || d.Column != 5 {
		t.Errorf("pos: got %d:%d, want 12:5", d.Line, d.Column)
	}
	if d.Message != "bad thing" {
		t.Errorf("message: got %q, want %q", d.Message, "bad thing")
	}
	if d.Hint != "try X" {
		t.Errorf("hint: got %q, want %q", d.Hint, "try X")
	}
}

func TestWarningAtSetsSeverity(t *testing.T) {
	d := transpiler.WarningAt("a.gb", token.Token{Line: 1, Column: 1}, "soft", "")
	if d.Severity != transpiler.SeverityWarning {
		t.Errorf("got %v, want SeverityWarning", d.Severity)
	}
}

func TestDiagnosticStringIncludesPositionAndSeverity(t *testing.T) {
	d := transpiler.ErrorAt("foo.gb", token.Token{Line: 12, Column: 5}, "bad", "")
	got := d.String()
	for _, want := range []string{"foo.gb", "12", "5", "error", "bad"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() %q missing %q", got, want)
		}
	}
}

func TestDiagnosticStringIncludesHintWhenPresent(t *testing.T) {
	d := transpiler.ErrorAt("foo.gb", token.Token{Line: 1, Column: 1}, "bad", "try Y")
	got := d.String()
	if !strings.Contains(got, "try Y") {
		t.Errorf("expected hint in output, got %q", got)
	}
}

func TestDiagnosticStringOmitsHintWhenEmpty(t *testing.T) {
	d := transpiler.ErrorAt("foo.gb", token.Token{Line: 1, Column: 1}, "bad", "")
	got := d.String()
	if strings.Contains(got, "hint:") {
		t.Errorf("expected no hint in output, got %q", got)
	}
}
