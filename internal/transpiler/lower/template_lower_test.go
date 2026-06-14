package lower_test

import (
	"strings"
	"testing"
)

// template.renderString bridges over html/template; Template/Engine yield opaque
// handles whose methods route to their Go methods.
func TestTemplateModuleLower(t *testing.T) {
	src := `import io;
import template;
io.println(template.renderString("Hi {{.n}}", {"n": "x"}));
let t = template.Template("Hi {{.n}}", "g");
io.println(t.name());
io.println(t.render({"n": "y"}));
let e = template.Engine("views");
io.println(e.dir());
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	for _, want := range []string{
		"transpilert.TemplateRenderString(",
		"transpilert.NewTemplate(",
		"t.Name_()",
		"t.Render(",
		"transpilert.NewTemplateEngine(",
		"e.Dir_()",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output:\n%s", want, got)
		}
	}
}

// An unknown Template method diagnoses cleanly rather than miscompiling.
func TestTemplateUnknownMethodDiagnoses(t *testing.T) {
	src := `import template;
let t = template.Template("x");
t.bogus();
`
	l := lowerSource(t, src)
	if len(l.Errors()) == 0 {
		t.Fatal("expected unknown template.Template method to diagnose, got none")
	}
}

// The unicode module is backed by golang.org/x/text (non-stdlib), so it has no
// transpiler bridge: every call must diagnose, never miscompile.
func TestUnicodeModuleDiagnoses(t *testing.T) {
	src := `import io;
import unicode;
io.println(unicode.normalize("é", "NFC"));
`
	l := lowerSource(t, src)
	if len(l.Errors()) == 0 {
		t.Fatal("expected unicode.normalize to diagnose (no zero-dep bridge), got none")
	}
}

// The markdown module is backed by goldmark (non-stdlib): no bridge, diagnose.
func TestMarkdownModuleDiagnoses(t *testing.T) {
	src := `import io;
import markdown;
io.println(markdown.renderHtml("# hi"));
`
	l := lowerSource(t, src)
	if len(l.Errors()) == 0 {
		t.Fatal("expected markdown.renderHtml to diagnose (no zero-dep bridge), got none")
	}
}
