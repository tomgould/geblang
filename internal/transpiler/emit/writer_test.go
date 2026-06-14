package emit_test

import (
	"testing"

	"geblang/internal/transpiler/emit"
)

func TestWriterEmitsRawText(t *testing.T) {
	w := emit.NewWriter()
	w.WriteString("hello")
	if got, want := w.String(), "hello"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWriterIndentsAtStartOfLine(t *testing.T) {
	w := emit.NewWriter()
	w.Indent()
	w.WriteString("foo")
	if got, want := w.String(), "\tfoo"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWriterIndentsEachNewLine(t *testing.T) {
	w := emit.NewWriter()
	w.Indent()
	w.WriteString("a\nb")
	if got, want := w.String(), "\ta\n\tb"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWriterDoesNotIndentMidLine(t *testing.T) {
	w := emit.NewWriter()
	w.WriteString("foo")
	w.Indent()
	w.WriteString("bar")
	if got, want := w.String(), "foobar"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWriterDedentDoesNotUnderflow(t *testing.T) {
	w := emit.NewWriter()
	w.Dedent()
	w.Dedent()
	w.WriteString("x")
	if got, want := w.String(), "x"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := w.IndentLevel(); got != 0 {
		t.Fatalf("indent level after dedent underflow: got %d, want 0", got)
	}
}

func TestWriterWriteLineAppendsNewline(t *testing.T) {
	w := emit.NewWriter()
	w.WriteLine("a")
	w.WriteLine("b")
	if got, want := w.String(), "a\nb\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWriterNestedIndentation(t *testing.T) {
	w := emit.NewWriter()
	w.WriteLine("func main() {")
	w.Indent()
	w.WriteLine("if cond {")
	w.Indent()
	w.WriteLine("x := 1")
	w.Dedent()
	w.WriteLine("}")
	w.Dedent()
	w.WriteLine("}")

	want := "func main() {\n\tif cond {\n\t\tx := 1\n\t}\n}\n"
	if got := w.String(); got != want {
		t.Fatalf("nested indentation mismatch\n  got: %q\n want: %q", got, want)
	}
}

func TestWriterResetClearsState(t *testing.T) {
	w := emit.NewWriter()
	w.Indent()
	w.WriteString("a")
	w.Reset()
	w.WriteString("b")
	if got, want := w.String(), "b"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := w.IndentLevel(); got != 0 {
		t.Fatalf("indent level after reset: got %d, want 0", got)
	}
}

func TestWriterEmptyStringIsNoOp(t *testing.T) {
	w := emit.NewWriter()
	w.WriteString("")
	if got := w.Len(); got != 0 {
		t.Fatalf("expected empty buffer after empty WriteString, got len %d", got)
	}
}

func TestWriterTrailingNewlineTriggersIndentOnNext(t *testing.T) {
	w := emit.NewWriter()
	w.WriteString("a\n")
	w.Indent()
	w.WriteString("b")
	if got, want := w.String(), "a\n\tb"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
