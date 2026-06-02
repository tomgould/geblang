package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestBashCompletionScriptListsCommands(t *testing.T) {
	script := bashCompletionScript()
	for _, cmd := range topLevelCommands {
		if !strings.Contains(script, cmd) {
			t.Errorf("completion script missing command %q", cmd)
		}
	}
	if !strings.Contains(script, "complete -o filenames -F _geblang_complete geblang") {
		t.Errorf("completion script missing the complete registration: %q", script)
	}
}

// TestTopLevelCommandsAreDocumented is the drift guard: every command
// in the canonical completion list must appear as a word in the
// top-level help, so completion never offers a command help does not
// document. Word-boundary matching tolerates presentation differences
// (run is shown as the script shorthand, version as --version) while
// still rejecting an absent command (and not matching `doc` inside
// `doctor`).
func TestTopLevelCommandsAreDocumented(t *testing.T) {
	var out bytes.Buffer
	if !printHelp(&out, "") {
		t.Fatal("top-level help should be known")
	}
	help := out.String()
	for _, cmd := range topLevelCommands {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(cmd) + `\b`).MatchString(help) {
			t.Errorf("command %q is in topLevelCommands but not documented in help", cmd)
		}
	}
}

func TestWritePagedPlainWhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	writePaged(&buf, "hello notices\n", true)
	if buf.String() != "hello notices\n" {
		t.Fatalf("no-pager output: got %q", buf.String())
	}
}
