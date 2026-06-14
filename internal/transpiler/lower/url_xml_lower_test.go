package lower_test

import (
	"strings"
	"testing"
)

// url.URL yields an opaque URLValue whose accessors, with* chain, and toDict
// route to its Go methods; the free functions bridge over net/url.
func TestURLObjectLower(t *testing.T) {
	src := `import io;
import url;
let u = url.URL("https://e.com:8443/p?q=1#f");
io.println(u.scheme());
io.println(u.toString());
let v = u.withHost("x").withPath("/y").toString();
io.println(v);
let d = u.toDict();
io.println(d.get("scheme"));
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	for _, want := range []string{
		"transpilert.NewURL(\"https://e.com:8443/p?q=1#f\")",
		"u.Scheme()",
		"u.ToString()",
		"u.WithHost(\"x\").WithPath(\"/y\").ToString()",
		"u.ToDict()",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output:\n%s", want, got)
		}
	}
}

// An unknown url.URL method diagnoses cleanly rather than miscompiling.
func TestURLObjectUnknownMethodDiagnoses(t *testing.T) {
	src := `import url;
let u = url.URL("https://e.com");
u.bogus();
`
	l := lowerSource(t, src)
	if len(l.Errors()) == 0 {
		t.Fatal("expected unknown url.URL method to diagnose, got none")
	}
}

// xml.parse/stringify/validate bridge over encoding/xml.
func TestXMLModuleLower(t *testing.T) {
	src := `import io;
import xml;
io.println(xml.validate("<a></a>"));
let root = {"name": "a", "attributes": {}, "children": [], "text": "x"};
io.println(xml.stringify(root));
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	for _, want := range []string{
		"transpilert.XMLValidate(",
		"transpilert.XMLStringify(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output:\n%s", want, got)
		}
	}
}

// A non-HOF method on an any-typed value (here xml.parse's result) routes
// through the runtime dispatcher; it must not diagnose.
func TestXMLParseResultMethodRoutesToCallMethod(t *testing.T) {
	src := `import io;
import xml;
let root = xml.parse("<a></a>");
io.println(root.get("name"));
`
	mod, l := runLowerer(t, src)
	if len(l.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", l.Errors())
	}
	if !strings.Contains(mod.MainBody().String(), `transpilert.CallMethod(`) {
		t.Fatalf("expected CallMethod dispatch, got:\n%s", mod.MainBody().String())
	}
}

// Index-into-any (e.g. json.parse(...)["k"]) lowers to transpilert.Index, and a
// trailing cast lowers to the matching As* helper, so dynamic navigation works.
func TestIndexIntoAnyLowersToRuntimeHelper(t *testing.T) {
	src := `import io;
import json;
let data = json.parse("{}");
io.println(data["a"]["b"]);
string s = data["name"] as string;
io.println(s);
int n = data["qty"] as int;
io.println(n);
let xs = data["tags"] as list<any>;
io.println(xs.length());
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	for _, want := range []string{
		`transpilert.Index(transpilert.Index(`,
		`transpilert.AsString(`,
		`transpilert.AsIntFast(`,
		`transpilert.AsList(`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output:\n%s", want, got)
		}
	}
}

// Index-ASSIGN into an any-typed receiver lowers to transpilert.IndexSet (the
// read lhs is not a Go lvalue), and a nested write composes IndexSet over Index.
func TestIndexAssignIntoAnyLowersToIndexSet(t *testing.T) {
	src := `import json;
let data = json.parse("{}");
data["count"] = 5;
data["items"][0] = 99;
data["obj"]["k"] = true;
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	for _, want := range []string{
		`transpilert.IndexSet(`,
		`transpilert.IndexSet(transpilert.Index(`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output:\n%s", want, got)
		}
	}
}
