package transpilert

import "testing"

func TestURLValueAccessors(t *testing.T) {
	u := NewURL("https://example.com:8443/a/b?q=hello#frag")
	if u.Scheme() != "https" {
		t.Errorf("scheme: %q", u.Scheme())
	}
	if u.Host() != "example.com" {
		t.Errorf("host: %q", u.Host())
	}
	if u.Port() != "8443" {
		t.Errorf("port: %q", u.Port())
	}
	if u.Path() != "/a/b" {
		t.Errorf("path: %q", u.Path())
	}
	if u.Fragment() != "frag" {
		t.Errorf("fragment: %q", u.Fragment())
	}
	if q, _ := u.Query().Get("q"); q != "hello" {
		t.Errorf("query q: %v", q)
	}
}

func TestURLValueWithChain(t *testing.T) {
	u := NewURL("https://example.com/p?a=1#f")
	v := u.WithScheme("http").WithHost("other.test").WithPath("/x").WithFragment("top")
	if got := v.ToString(); got != "http://other.test/x?a=1#top" {
		t.Errorf("with chain: %q", got)
	}
}

func TestURLValueWithQueryStringAndDict(t *testing.T) {
	u := NewURL("https://e.com/p")
	if got := u.WithQuery("a=1&b=2").ToString(); got != "https://e.com/p?a=1&b=2" {
		t.Errorf("withQuery string: %q", got)
	}
	d := NewOrderedDict[string, string]()
	d.Set("k", "v")
	if got := u.WithQuery(d).ToString(); got != "https://e.com/p?k=v" {
		t.Errorf("withQuery dict: %q", got)
	}
}

func TestURLValueResolveNormalize(t *testing.T) {
	r := NewURL("https://e.com/base/page.html").Resolve("../other/thing")
	if got := r.ToString(); got != "https://e.com/other/thing" {
		t.Errorf("resolve: %q", got)
	}
	n := NewURL("https://e.com/a/./b/../c?x=1").Normalize()
	if got := n.ToString(); got != "https://e.com/a/c?x=1" {
		t.Errorf("normalize: %q", got)
	}
}

func TestURLValueToDictSortedKeys(t *testing.T) {
	d := NewURL("https://e.com:9000/p?q=1#f").ToDict()
	want := []string{"fragment", "host", "path", "port", "query", "scheme"}
	keys := d.Keys()
	if len(keys) != len(want) {
		t.Fatalf("keys: %v", keys)
	}
	for i, k := range want {
		if keys[i] != k {
			t.Fatalf("key order: got %v want %v", keys, want)
		}
	}
}

func TestNewURLFromDict(t *testing.T) {
	d := NewOrderedDict[string, string]()
	d.Set("scheme", "https")
	d.Set("host", "h.test")
	d.Set("port", "9000")
	d.Set("path", "/p")
	if got := NewURL(d).ToString(); got != "https://h.test:9000/p" {
		t.Errorf("from dict: %q", got)
	}
}

func TestURLStringify(t *testing.T) {
	d := NewOrderedDict[string, string]()
	d.Set("scheme", "https")
	d.Set("host", "h.test")
	d.Set("path", "/p")
	if got := URLStringify(d); got != "https://h.test/p" {
		t.Errorf("stringify: %q", got)
	}
}
