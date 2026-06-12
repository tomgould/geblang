package native

import (
	"testing"

	"geblang/internal/runtime"
)

func TestBuildErrorStackTrace(t *testing.T) {
	t.Run("frames-first basic expansion", func(t *testing.T) {
		err := runtime.Error{
			Class:     "ValueError",
			Message:   "m",
			ErrorLine: 6,
			TraceFrames: []runtime.StackFrame{
				{Name: "inner", CallLine: 6},
				{Name: "loop", CallLine: 8, Repeat: 3},
				{Name: "", CallLine: 12},
			},
			TopLevelLine: 20,
		}

		trace := buildErrorStackTrace(err)

		// Expected: inner(6), loop(8) x3, anonymous(12), top-level(20) = 6 frames
		if len(trace.Frames) != 6 {
			t.Fatalf("expected 6 frames, got %d", len(trace.Frames))
		}

		tests := []struct {
			idx  int
			fn   string
			line int64
		}{
			{0, "inner", 6},
			{1, "loop", 8},
			{2, "loop", 8},
			{3, "loop", 8},
			{4, "<closure>", 12},
			{5, "<top level>", 20},
		}

		for _, tc := range tests {
			frame := trace.Frames[tc.idx]
			if frame.Function != tc.fn {
				t.Errorf("frame %d: expected function %q, got %q", tc.idx, tc.fn, frame.Function)
			}
			if frame.Line != tc.line {
				t.Errorf("frame %d: expected line %d, got %d", tc.idx, tc.line, frame.Line)
			}
		}

		// Raw must be non-empty and match ResolvedStackTrace
		if trace.Raw == "" {
			t.Error("Raw should be non-empty")
		}
		if trace.Raw != err.ResolvedStackTrace() {
			t.Errorf("Raw mismatch: got %q, expected %q", trace.Raw, err.ResolvedStackTrace())
		}
	})

	t.Run("innermost frame ignores Repeat, subsequent frames capped at maxFrameExpansion", func(t *testing.T) {
		// Case: innermost with Repeat=500 should produce only 1 frame (ignoring Repeat).
		// Second frame with Repeat=500 should be capped at maxFrameExpansion (100).
		err := runtime.Error{
			Class:     "ValueError",
			Message:   "test",
			ErrorLine: 2,
			TraceFrames: []runtime.StackFrame{
				{Name: "innermost", CallLine: 2, Repeat: 500},
				{Name: "deep", CallLine: 3, Repeat: 500},
			},
			TopLevelLine: 9,
		}

		trace := buildErrorStackTrace(err)

		// Expected: innermost(2) x1 (ignores Repeat) + deep(3) x100 (capped) + top-level(9) = 102 frames
		if len(trace.Frames) != 102 {
			t.Fatalf("expected 102 frames, got %d", len(trace.Frames))
		}

		// First frame: innermost, line 2, count=1
		if trace.Frames[0].Function != "innermost" || trace.Frames[0].Line != 2 {
			t.Errorf("frame 0: expected (innermost, 2), got (%s, %d)", trace.Frames[0].Function, trace.Frames[0].Line)
		}

		// Frames 1-100: deep, line 3 (100 times)
		for i := 1; i <= 100; i++ {
			frame := trace.Frames[i]
			if frame.Function != "deep" || frame.Line != 3 {
				t.Errorf("frame %d: expected (deep, 3), got (%s, %d)", i, frame.Function, frame.Line)
			}
		}

		// Frame 101: top level, line 9
		if trace.Frames[101].Function != "<top level>" || trace.Frames[101].Line != 9 {
			t.Errorf("frame 101: expected (<top level>, 9), got (%s, %d)", trace.Frames[101].Function, trace.Frames[101].Line)
		}

		if trace.Raw == "" {
			t.Error("Raw should be non-empty")
		}
		if trace.Raw != err.ResolvedStackTrace() {
			t.Errorf("Raw mismatch: got %q, expected %q", trace.Raw, err.ResolvedStackTrace())
		}
	})
}
