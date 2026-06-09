package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dir of a datetime value type must surface its method names, byte-identical
// on both backends.
func TestDatetimeDirParity(t *testing.T) {
	bin := buildCMBinary(t)

	cases := []struct {
		label string
		expr  string
	}{
		{"Instant", "datetime.Instant(0)"},
		{"Duration", "datetime.Duration(60)"},
		{"Zone", `datetime.Zone("UTC")`},
	}

	results := map[string]string{}
	for _, c := range cases {
		dir := t.TempDir()
		mainPath := filepath.Join(dir, "main.gb")
		os.WriteFile(mainPath, []byte(
			"import datetime;\nimport io;\nio.println(dir("+c.expr+"));\n"), 0644)
		vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
		assertParitySuccess(t, c.label, vm, eval, vmErr, evalErr)
		results[c.label] = strings.TrimSpace(vm)
	}

	instant := results["Instant"]
	if instant == "[]" || instant == "" {
		t.Fatalf("Instant dir is empty: %q", instant)
	}
	for _, want := range []string{"year", "addDays", "copy"} {
		if !strings.Contains(instant, want) {
			t.Fatalf("Instant dir missing %q: %s", want, instant)
		}
	}
}
