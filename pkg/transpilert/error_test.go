package transpilert

import "testing"

func TestErrorRenderMatchesInterpreterFormat(t *testing.T) {
	e := &Error{
		Class:   "ValueError",
		Message: "x too big: 7",
		ErrLine: 6,
		Frames: []Frame{
			{Name: "inner", CallLine: 6},
			{Name: "middle", CallLine: 12},
		},
		TopLine: 15,
	}
	want := "uncaught ValueError: x too big: 7" +
		"\n  at inner (line 6)" +
		"\n  at middle (line 12)" +
		"\n  at <top level> (line 15)"
	if got := e.Render(); got != want {
		t.Fatalf("Render mismatch:\ngot:  %q\nwant: %q", got, want)
	}
	if e.Error() != want {
		t.Fatal("Error() must equal Render()")
	}
}

func TestErrorRenderNoFrames(t *testing.T) {
	e := NewError("RuntimeError", "boom")
	want := "uncaught RuntimeError: boom\n  at <top level>"
	if got := e.Render(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestErrorRenderCollapsesRepeats(t *testing.T) {
	e := &Error{
		Class:   "Error",
		Message: "deep",
		ErrLine: 5,
		Frames: []Frame{
			{Name: "f", CallLine: 8},
			{Name: "f", CallLine: 8},
			{Name: "f", CallLine: 8},
			{Name: "g", CallLine: 20},
		},
		TopLine: 30,
	}
	want := "uncaught Error: deep" +
		"\n  at f (line 5)" +
		"\n  at f (line 8) [x3]" +
		"\n  at g (line 20)" +
		"\n  at <top level> (line 30)"
	if got := e.Render(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestErrorIsHierarchy(t *testing.T) {
	e := &Error{Class: "ValueError", Parents: []string{"RuntimeError", "Error"}}
	if !e.IsClass("ValueError") {
		t.Fatal("Is(own class) = false")
	}
	if !e.IsClass("RuntimeError") {
		t.Fatal("Is(parent) = false")
	}
	if e.IsClass("TypeError") {
		t.Fatal("Is(unrelated) = true")
	}
}
