package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRowsFor(t *testing.T) {
	cases := []struct {
		cells, width, want int
	}{
		{0, 80, 1},
		{1, 80, 1},
		{80, 80, 1},
		{81, 80, 2},
		{160, 80, 2},
		{161, 80, 3},
		{5, 0, 1},
	}
	for _, c := range cases {
		if got := rowsFor(c.cells, c.width); got != c.want {
			t.Errorf("rowsFor(%d, %d) = %d, want %d", c.cells, c.width, got, c.want)
		}
	}
}

func TestDisplayWidthStripsAnsi(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"geb> ", 5},
		{"\x1b[31mred\x1b[0m", 3},
		{"a\x1b[1;31mb\x1b[0mc", 3},
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestWriteRedrawSingleRowNoVerticalMotion(t *testing.T) {
	var buf bytes.Buffer
	row := writeRedraw(&buf, 80, "geb> ", []rune("hi"), 1, 0)
	if row != 0 {
		t.Fatalf("row offset: got %d want 0", row)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b[A") || strings.Contains(out, "[1A") {
		t.Fatalf("unexpected CUU in single-row redraw: %q", out)
	}
	if !strings.HasPrefix(out, "\r\x1b[J") {
		t.Fatalf("expected leading clear, got %q", out)
	}
	if !strings.Contains(out, "geb> hi") {
		t.Fatalf("expected prompt+buffer in output: %q", out)
	}
	if !strings.HasSuffix(out, "\x1b[6C") {
		t.Fatalf("expected cursor-forward to col 6 (prompt 5 + cursor 1), got tail %q", out)
	}
}

func TestWriteRedrawHomeOnWrappedLineMovesUp(t *testing.T) {
	width := 10
	prompt := "geb> "
	buf := strings.Repeat("x", 25)
	var out bytes.Buffer
	row := writeRedraw(&out, width, prompt, []rune(buf), len(buf), 0)
	if row == 0 {
		t.Fatalf("expected wrapped end-cursor row > 0, got %d", row)
	}
	out.Reset()
	row2 := writeRedraw(&out, width, prompt, []rune(buf), 0, row)
	if row2 != 0 {
		t.Fatalf("Home should leave cursor at row 0, got %d", row2)
	}
	emit := out.String()
	upSeq := "\x1b[" + itoa(row) + "A"
	if !strings.Contains(emit, upSeq) {
		t.Fatalf("expected CUU sequence %q to walk up from prev row %d, got %q", upSeq, row, emit)
	}
	if !strings.Contains(emit, "\x1b[5C") {
		t.Fatalf("expected CUF-by-5 to land after prompt, got %q", emit)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	if neg {
		return "-" + digits
	}
	return digits
}

func TestWriteRedrawLeftArrowAcrossRowBoundary(t *testing.T) {
	width := 10
	prompt := "geb> "
	buf := strings.Repeat("x", 12)
	var out bytes.Buffer
	row := writeRedraw(&out, width, prompt, []rune(buf), len(buf), 0)
	if row != 1 {
		t.Fatalf("expected end-cursor row 1 (5+12=17, row index 1), got %d", row)
	}
	out.Reset()
	row = writeRedraw(&out, width, prompt, []rune(buf), 4, row)
	if row != 0 {
		t.Fatalf("expected cursor row 0 after stepping back across the wrap (5+4=9, row 0), got %d", row)
	}
	emit := out.String()
	if !strings.Contains(emit, "\x1b[1A") {
		t.Fatalf("expected one CUU to walk back to row 0, got %q", emit)
	}
	if !strings.Contains(emit, "\x1b[9C") {
		t.Fatalf("expected CUF-by-9 to land in column 9, got %q", emit)
	}
}

func TestWriteRedrawClearsStaleRowsOnShrink(t *testing.T) {
	width := 10
	prompt := "geb> "
	long := strings.Repeat("x", 30)
	var out bytes.Buffer
	row := writeRedraw(&out, width, prompt, []rune(long), len(long), 0)
	out.Reset()
	short := "a"
	writeRedraw(&out, width, prompt, []rune(short), len(short), row)
	emit := out.String()
	if !strings.Contains(emit, "\x1b[J") {
		t.Fatalf("expected screen-clear when shrinking, got %q", emit)
	}
	if row > 0 {
		upSeq := "\x1b[" + itoa(row) + "A"
		if !strings.Contains(emit, upSeq) {
			t.Fatalf("expected %q to walk up from row %d before clear, got %q", upSeq, row, emit)
		}
	}
}
