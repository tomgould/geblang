package emit

import (
	"bytes"
	"strings"
)

type Writer struct {
	buf    bytes.Buffer
	indent int
	atLine bool
}

func NewWriter() *Writer {
	return &Writer{atLine: true}
}

func (w *Writer) Indent() { w.indent++ }

func (w *Writer) Dedent() {
	if w.indent > 0 {
		w.indent--
	}
}

func (w *Writer) IndentLevel() int { return w.indent }

func (w *Writer) WriteString(s string) {
	if s == "" {
		return
	}
	for {
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			w.writeSegment(s)
			return
		}
		w.writeSegment(s[:nl])
		w.buf.WriteByte('\n')
		w.atLine = true
		s = s[nl+1:]
	}
}

func (w *Writer) writeSegment(s string) {
	if s == "" {
		return
	}
	if w.atLine {
		for i := 0; i < w.indent; i++ {
			w.buf.WriteByte('\t')
		}
		w.atLine = false
	}
	w.buf.WriteString(s)
}

func (w *Writer) WriteLine(s string) {
	w.WriteString(s)
	w.WriteString("\n")
}

func (w *Writer) Newline() {
	w.WriteString("\n")
}

func (w *Writer) Bytes() []byte { return w.buf.Bytes() }

func (w *Writer) String() string { return w.buf.String() }

func (w *Writer) Len() int { return w.buf.Len() }

func (w *Writer) Reset() {
	w.buf.Reset()
	w.indent = 0
	w.atLine = true
}
