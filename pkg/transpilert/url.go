package transpilert

import (
	"net/url"
	pathlib "path"
	"sort"
	"strings"
)

// URL helpers mirror internal/native url-module free functions over net/url
// (stdlib), so --native matches the interpreter byte-for-byte and stays
// zero-dep. parse builds insertion-sorted OrderedDicts because the interpreter
// constructs its result dicts without an explicit order (nil Order sorts keys).

func URLEncode(s string) string { return url.QueryEscape(s) }

func URLDecode(s string) string {
	decoded, err := url.QueryUnescape(s)
	if err != nil {
		panic(NewError("RuntimeError", "url.decode: "+err.Error()))
	}
	return decoded
}

// URLParse returns scheme/host/port/path/query/fragment as an OrderedDict whose
// keys are inserted in sorted order to match the interpreter's rendering.
func URLParse(raw string) *OrderedDict[string, any] {
	parsed, err := url.Parse(raw)
	if err != nil {
		panic(NewError("RuntimeError", "url.parse: "+err.Error()))
	}
	d := NewOrderedDict[string, any]()
	parts := map[string]any{
		"scheme":   parsed.Scheme,
		"host":     parsed.Hostname(),
		"port":     parsed.Port(),
		"path":     parsed.Path,
		"fragment": parsed.Fragment,
		"query":    urlQueryDict(parsed.Query()),
	}
	keys := make([]string, 0, len(parts))
	for k := range parts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d.Set(k, parts[k])
	}
	return d
}

// urlQueryDict renders url.Values as an OrderedDict; a single value stays a
// string, multiple values become a []any, keys inserted sorted.
func urlQueryDict(values url.Values) *OrderedDict[string, any] {
	d := NewOrderedDict[string, any]()
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vs := values[k]
		if len(vs) == 1 {
			d.Set(k, vs[0])
			continue
		}
		out := make([]any, len(vs))
		for i, v := range vs {
			out[i] = v
		}
		d.Set(k, out)
	}
	return d
}

// URLJoinPath joins base with path parts (path.Join), preserving a trailing
// slash when the last part has one, matching the interpreter.
func URLJoinPath(base string, rest ...string) string {
	u, err := url.Parse(base)
	if err != nil {
		panic(NewError("RuntimeError", "url.joinPath: "+err.Error()))
	}
	if len(rest) == 0 {
		return u.String()
	}
	parts := append([]string{u.Path}, rest...)
	u.Path = pathlib.Join(parts...)
	if strings.HasSuffix(parts[len(parts)-1], "/") && !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u.String()
}
