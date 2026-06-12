package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestREPLUncaughtCanonicalFormat locks the REPL's uncaught-error
// output to the canonical classed header + trace.
func TestREPLUncaughtCanonicalFormat(t *testing.T) {
	in := strings.NewReader("throw RuntimeError(\"repl boom\");\n" +
		"func f(int x): int { throw RuntimeError(\"deep\"); }\n" +
		"f(3);\n")
	var out, errOut bytes.Buffer
	if code := runREPL(in, &out, &errOut, replConfig{}); code != 0 {
		t.Fatalf("repl exit code %d (stderr %q)", code, errOut.String())
	}
	got := errOut.String()
	for _, want := range []string{
		"uncaught RuntimeError: repl boom\n  at <top level> (line 1)\n",
		"uncaught RuntimeError: deep\n  at f (line 1)\n  at <top level> (line 1)\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repl stderr missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Error: uncaught") {
		t.Fatalf("redundant Error: prefix on uncaught output:\n%s", got)
	}
}

// TestREPLWarningsDoNotBlockEvaluation locks the contract that analyzer
// warnings print but the snippet still runs (and throws canonically).
func TestREPLWarningsDoNotBlockEvaluation(t *testing.T) {
	in := strings.NewReader("3 // 0;\n:quit\n")
	var out, errOut bytes.Buffer
	if code := runREPL(in, &out, &errOut, replConfig{}); code != 0 {
		t.Fatalf("repl exit code %d (stderr %q)", code, errOut.String())
	}
	got := errOut.String()
	for _, want := range []string{
		"warning: \"//\" by literal zero always throws at runtime\n",
		"uncaught RuntimeError: integer division by zero\n  at <top level>\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repl stderr missing %q:\n%s", want, got)
		}
	}
}
