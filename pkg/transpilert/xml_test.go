package transpilert

import "testing"

func TestXMLValidate(t *testing.T) {
	if !XMLValidate("<a><b>x</b></a>") {
		t.Error("expected valid")
	}
	if XMLValidate("<a><b></a>") {
		t.Error("expected invalid (mismatched)")
	}
	if XMLValidate("text outside") {
		t.Error("expected invalid (no root)")
	}
	if XMLValidate("<a/><b/>") {
		t.Error("expected invalid (two roots)")
	}
}

func TestXMLParse(t *testing.T) {
	v := XMLParse(`<note id="1" lang="en"><to>Tove</to><body>hi</body></note>`)
	root, ok := v.(*OrderedDict[string, any])
	if !ok {
		t.Fatalf("parse: not a dict: %T", v)
	}
	keys := root.Keys()
	want := []string{"attributes", "children", "name", "text"}
	for i, k := range want {
		if keys[i] != k {
			t.Fatalf("key order: got %v want %v", keys, want)
		}
	}
	if name, _ := root.Get("name"); name != "note" {
		t.Errorf("name: %v", name)
	}
	attrs, _ := root.Get("attributes")
	ad := attrs.(*OrderedDict[string, any])
	if id, _ := ad.Get("id"); id != "1" {
		t.Errorf("attr id: %v", id)
	}
	children, _ := root.Get("children")
	cl := children.([]any)
	if len(cl) != 2 {
		t.Fatalf("children: %d", len(cl))
	}
	first := cl[0].(*OrderedDict[string, any])
	if txt, _ := first.Get("text"); txt != "Tove" {
		t.Errorf("child text: %v", txt)
	}
}

func TestXMLStringifyRoundTrip(t *testing.T) {
	v := XMLParse(`<note id="1"><to>Tove &amp; co</to></note>`)
	if got := XMLStringify(v); got != `<note id="1"><to>Tove &amp; co</to></note>` {
		t.Errorf("round trip: %q", got)
	}
}

func TestXMLStringifyHandBuilt(t *testing.T) {
	to := NewOrderedDict[string, any]()
	to.Set("name", "to")
	to.Set("attributes", NewOrderedDict[string, any]())
	to.Set("children", []any{})
	to.Set("text", `a < b & "q"`)
	if got := XMLStringify(to); got != `<to>a &lt; b &amp; &#34;q&#34;</to>` {
		t.Errorf("hand-built: %q", got)
	}
}
