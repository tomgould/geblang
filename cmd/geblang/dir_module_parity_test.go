package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDirModuleParity covers dir(<module>) across backends. dir() is primarily a
// REPL/dev tool that runs on the EVALUATOR, so the priority is that the
// evaluator list be COMPREHENSIVE: the union of a module's native functions and
// its source `.gb` exports (whichever resolve), since dual-name modules make
// both accessible via `module.<member>`. The VM keeps its best-effort behavior;
// exact eval/VM parity is required ONLY for the pure-native modules (sys, math),
// where both backends read the same native-symbols set. For every module both
// backends must produce a non-empty list (no crash).
func TestDirModuleParity(t *testing.T) {
	bin := buildCMBinary(t)
	cases := []struct {
		label         string
		imp           string
		name          string
		evalMustHave  []string // members the comprehensive evaluator dir must list
		byteIdentical bool     // eval/VM dir must match exactly (pure-native only)
	}{
		{"sys", "import sys;", "sys", nil, true},
		{"math", "import math;", "math", []string{"sqrt"}, true},
		// async.sync is dual-name: both the source classes and the native
		// primitives are accessible, so the comprehensive dir lists both.
		{"sync", "import async.sync as sync;", "sync",
			[]string{"Mutex", "mutexNew"}, false},
		// datetime is dual-name: the DateTime class export plus native functions.
		{"datetime", "import datetime;", "datetime",
			[]string{"DateTime", "addDays"}, false},
		// strings is source-backed: dir must list its source exports.
		{"strings", "import strings as s;", "s",
			[]string{"StringBuilder"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			dir := t.TempDir()
			mainPath := filepath.Join(dir, "main.gb")
			src := tc.imp + "\n" +
				"import io;\n" +
				"let names = dir(" + tc.name + ");\n" +
				"io.println(names.join(\",\"));\n"
			os.WriteFile(mainPath, []byte(src), 0644)
			vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
			if vmErr != nil {
				t.Fatalf("%s VM failed: %v\n%s", tc.label, vmErr, vm)
			}
			if evalErr != nil {
				t.Fatalf("%s evaluator failed: %v\n%s", tc.label, evalErr, eval)
			}
			if strings.TrimSpace(vm) == "" {
				t.Fatalf("%s: dir(%s) produced empty list on VM: %q", tc.label, tc.name, vm)
			}
			if strings.TrimSpace(eval) == "" {
				t.Fatalf("%s: dir(%s) produced empty list on evaluator: %q", tc.label, tc.name, eval)
			}
			has := map[string]bool{}
			for _, m := range strings.Split(strings.TrimSpace(eval), ",") {
				has[m] = true
			}
			for _, want := range tc.evalMustHave {
				if !has[want] {
					t.Errorf("%s: evaluator dir(%s) missing accessible member %q; got %q", tc.label, tc.name, want, eval)
				}
			}
			if tc.byteIdentical && vm != eval {
				t.Fatalf("%s: pure-native dir(%s) must match across backends:\nVM:   %q\neval: %q", tc.label, tc.name, vm, eval)
			}
		})
	}
}
