package formatter_test

import (
	"strings"
	"testing"

	"geblang/internal/formatter"
)

func TestFormatPartialRoundTrip(t *testing.T) {
	cases := []string{
		"let f = add(_, 10);\n",
		"let g = wrap(_, \"-\", _);\n",
		"let h = open(mode: _);\n",
	}
	for _, src := range cases {
		out, err := formatter.Format([]byte(src))
		if err != nil {
			t.Fatalf("Format(%q) error: %v", src, err)
		}
		if string(out) != src {
			t.Fatalf("round-trip mismatch:\n in:  %q\n out: %q", src, string(out))
		}
	}
}

func TestFormatPartialWidthBreak(t *testing.T) {
	// A partial call wide enough to trigger a width break must still round-trip.
	src := "let result = someVeryLongFunctionName(firstArgument, _, thirdArgument, _, fifthArgument);\n"
	out, err := formatter.Format([]byte(src))
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	// idempotency implies AST-equality (the formatter's round-trip guard fires on first pass)
	out2, err := formatter.Format(out)
	if err != nil {
		t.Fatalf("re-format error: %v", err)
	}
	if string(out) != string(out2) {
		t.Fatalf("not idempotent:\nfirst:\n%s\nsecond:\n%s", out, out2)
	}
	// Confirm _ holes survive in the output.
	s := string(out)
	if !strings.Contains(s, "_") {
		t.Fatalf("hole _ missing from output:\n%s", s)
	}
}
