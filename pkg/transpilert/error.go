package transpilert

import (
	"fmt"
	"strings"
)

// Frame is one trace frame, innermost first.
type Frame struct {
	Name     string
	CallLine int
	Repeat   int
}

// Error is a thrown Geblang error value. A transpiled `throw` panics with an
// *Error; top-level recovery renders it via Render for output identical to
// the interpreter's uncaught format.
type Error struct {
	Class   string
	Message string
	Parents []string
	ErrLine int
	TopLine int
	Frames  []Frame
}

// NewError builds an error value with a class and message.
func NewError(class, message string) *Error {
	return &Error{Class: class, Message: message}
}

func (e *Error) Error() string { return e.Render() }

// Render produces the canonical uncaught error text both backends emit. The
// format is inlined (not delegated to the engine) so transpilert stays pure
// Go stdlib for vendoring; it matches internal/runtime.UncaughtError.Render
// byte for byte. Transpiled errors carry no frames in Tier-1, so the common
// case is just the header plus the top-level line.
func (e *Error) Render() string {
	var sb strings.Builder
	sb.WriteString("uncaught ")
	sb.WriteString(e.Class)
	if e.Message != "" {
		sb.WriteString(": ")
		sb.WriteString(e.Message)
	}
	frames := collapseFrames(e.Frames)
	if len(frames) > 0 {
		first := frames[0]
		writeFrameLine(&sb, first.Name, e.ErrLine)
		if r := normalizeRepeat(first.Repeat); r > 1 {
			writeFrameRepeat(&sb, first.Name, first.CallLine, r)
		}
		for _, f := range frames[1:] {
			if r := normalizeRepeat(f.Repeat); r > 1 {
				writeFrameRepeat(&sb, f.Name, f.CallLine, r)
			} else {
				writeFrameLine(&sb, f.Name, f.CallLine)
			}
		}
	}
	writeFrameLine(&sb, "<top level>", e.TopLine)
	return sb.String()
}

func writeFrameLine(sb *strings.Builder, name string, line int) {
	if name == "" {
		name = "<closure>"
	}
	if line > 0 {
		fmt.Fprintf(sb, "\n  at %s (line %d)", name, line)
	} else {
		fmt.Fprintf(sb, "\n  at %s", name)
	}
}

func writeFrameRepeat(sb *strings.Builder, name string, line, repeat int) {
	if name == "" {
		name = "<closure>"
	}
	if line > 0 {
		fmt.Fprintf(sb, "\n  at %s (line %d) [x%d]", name, line, repeat)
	} else {
		fmt.Fprintf(sb, "\n  at %s [x%d]", name, repeat)
	}
}

func normalizeRepeat(r int) int {
	if r < 1 {
		return 1
	}
	return r
}

func collapseFrames(frames []Frame) []Frame {
	out := make([]Frame, 0, len(frames))
	for _, f := range frames {
		r := normalizeRepeat(f.Repeat)
		if n := len(out); n > 0 && out[n-1].Name == f.Name && out[n-1].CallLine == f.CallLine {
			out[n-1].Repeat += r
			continue
		}
		f.Repeat = r
		out = append(out, f)
	}
	return out
}

// IsClass reports whether e is of class or has it in its parent chain,
// matching Geblang's catch-by-class hierarchy.
func (e *Error) IsClass(class string) bool {
	if e.Class == class {
		return true
	}
	for _, p := range e.Parents {
		if p == class {
			return true
		}
	}
	return false
}
