package runtime

import "testing"

func TestRenderUncaughtBasic(t *testing.T) {
	u := &UncaughtError{
		Class:     "ValueError",
		Message:   "x too big: 7",
		ErrorLine: 6,
		Frames: []StackFrame{
			{Name: "inner", CallLine: 6},
			{Name: "middle", CallLine: 12},
		},
		TopLevelLine: 15,
	}
	want := "uncaught ValueError: x too big: 7" +
		"\n  at inner (line 6)" +
		"\n  at middle (line 12)" +
		"\n  at <top level> (line 15)"
	if got := u.Render(); got != want {
		t.Fatalf("Render mismatch:\ngot:  %q\nwant: %q", got, want)
	}
	if u.Error() != want {
		t.Fatalf("Error() must equal Render()")
	}
}

func TestRenderUncaughtNoFramesNoLines(t *testing.T) {
	u := &UncaughtError{Class: "RuntimeError", Message: "boom"}
	want := "uncaught RuntimeError: boom\n  at <top level>"
	if got := u.Render(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderUncaughtClosureFrame(t *testing.T) {
	u := &UncaughtError{
		Class: "Error", Message: "m", ErrorLine: 3,
		Frames: []StackFrame{{Name: "", CallLine: 3}},
	}
	want := "uncaught Error: m\n  at <closure> (line 3)\n  at <top level>"
	if got := u.Render(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderUncaughtRepeatCollapse(t *testing.T) {
	u := &UncaughtError{
		Class: "Error", Message: "deep", ErrorLine: 5,
		Frames: []StackFrame{
			{Name: "f", CallLine: 8, Repeat: 999},
			{Name: "g", CallLine: 20},
		},
		TopLevelLine: 30,
	}
	want := "uncaught Error: deep" +
		"\n  at f (line 5)" +
		"\n  at f (line 8) [x999]" +
		"\n  at g (line 20)" +
		"\n  at <top level> (line 30)"
	if got := u.Render(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCollapseFrames(t *testing.T) {
	frames := []StackFrame{
		{Name: "f", CallLine: 8},
		{Name: "f", CallLine: 8},
		{Name: "f", CallLine: 8},
		{Name: "g", CallLine: 20},
	}
	got := CollapseFrames(frames)
	want := []StackFrame{{Name: "f", CallLine: 8, Repeat: 3}, {Name: "g", CallLine: 20, Repeat: 1}}
	if len(got) != len(want) {
		t.Fatalf("got %d frames, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestRenderUncaughtEmptyMessage(t *testing.T) {
	u := &UncaughtError{Class: "FatalError"}
	if got := u.Render(); got != "uncaught FatalError\n  at <top level>" {
		t.Fatalf("got %q", got)
	}
}

func TestCollapseFramesEdges(t *testing.T) {
	if got := CollapseFrames(nil); len(got) != 0 {
		t.Fatalf("nil input: got %d frames", len(got))
	}
	one := CollapseFrames([]StackFrame{{Name: "f", CallLine: 2}})
	if len(one) != 1 || one[0].Repeat != 1 {
		t.Fatalf("single frame: got %+v", one)
	}
	distinct := CollapseFrames([]StackFrame{{Name: "f", CallLine: 2}, {Name: "g", CallLine: 3}})
	if len(distinct) != 2 || distinct[0].Repeat != 1 || distinct[1].Repeat != 1 {
		t.Fatalf("distinct frames: got %+v", distinct)
	}
	preset := CollapseFrames([]StackFrame{{Name: "f", CallLine: 2, Repeat: 3}, {Name: "f", CallLine: 2}})
	if len(preset) != 1 || preset[0].Repeat != 4 {
		t.Fatalf("preset repeat accumulation: got %+v", preset)
	}
}

func TestErrorResolvedStackTraceFromFrames(t *testing.T) {
	e := Error{
		Class: "ValueError", Message: "m",
		TraceFrames:  []StackFrame{{Name: "inner", CallLine: 6}},
		ErrorLine:    6,
		TopLevelLine: 15,
	}
	want := "  at inner (line 6)\n  at <top level> (line 15)"
	if got := e.ResolvedStackTrace(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestErrorResolvedStackTraceEagerStringWins(t *testing.T) {
	e := Error{Class: "E", StackTrace: "\n  at legacy (line 1)"}
	if got := e.ResolvedStackTrace(); got != "\n  at legacy (line 1)" {
		t.Fatalf("eager string must take precedence, got %q", got)
	}
}
