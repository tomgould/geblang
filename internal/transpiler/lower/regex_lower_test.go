package lower_test

import (
	"strings"
	"testing"
)

// String regex methods lower to the transpilert RE2 helpers (receiver is text).
func TestStringRegexMethodsLower(t *testing.T) {
	src := `import io;
let s = "a1b2";
io.println(s.matchesRegex("[0-9]"));
io.println(s.splitRegex("[0-9]"));
io.println(s.replaceRegex("[0-9]", "#"));
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	for _, want := range []string{
		"transpilert.StringMatchesRegex(s, \"[0-9]\")",
		"transpilert.StringSplitRegex(s, \"[0-9]\")",
		"transpilert.StringReplaceRegex(s, \"[0-9]\", \"#\")",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output:\n%s", want, got)
		}
	}
}

// re.replace bridges to the free function; re.compile yields a RePattern whose
// chained methods route to its Go methods.
func TestReModuleLower(t *testing.T) {
	src := `import io;
import re;
io.println(re.replace("\\d", "#", "a1"));
let p = re.compile("[0-9]+");
io.println(p.test("a1"));
io.println(p.find("a1"));
io.println(p.findAll("a1b2"));
io.println(p.split("a1b2"));
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	for _, want := range []string{
		"transpilert.ReReplace(",
		"transpilert.ReCompile(\"[0-9]+\")",
		"p.Test(\"a1\")",
		"p.Find(\"a1\")",
		"p.FindAll(\"a1b2\")",
		"p.Split(\"a1b2\")",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output:\n%s", want, got)
		}
	}
}

// pcre has no zero-dep bridge (regexp2 is non-stdlib); it must diagnose cleanly.
func TestPcreDiagnoses(t *testing.T) {
	src := `import io;
import pcre;
let p = pcre.compile("\\d+");
io.println(p.test("a1"));
`
	l := lowerSource(t, src)
	errs := l.Errors()
	if len(errs) == 0 {
		t.Fatal("expected pcre to diagnose, got none")
	}
	if !strings.Contains(string(l.Module.Render()), "transpilert.PcreCompile") &&
		strings.Contains(string(l.Module.Render()), "PcreCompile") {
		t.Fatal("pcre must not emit a runtime bridge")
	}
}
