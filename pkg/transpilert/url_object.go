package transpilert

import (
	"net/url"
	pathlib "path"
	"sort"
	"strings"
)

// URLValue mirrors the interpreter's runtime.URLValue: it stores the raw URL
// string and re-parses on each method call (net/url, stdlib) so --native
// matches byte-for-byte. It backs url.URL(string|dict) and its chained methods.
type URLValue struct{ raw string }

// NewURL backs url.URL(arg): arg is a string (parsed) or a parts dict (built).
func NewURL(arg any) *URLValue {
	if s, ok := arg.(string); ok {
		parsed, err := url.Parse(s)
		if err != nil {
			panic(NewError("RuntimeError", "url.URL: "+err.Error()))
		}
		return &URLValue{raw: parsed.String()}
	}
	if _, _, ok := urlStringKeyDict(arg); ok {
		return &URLValue{raw: urlBuildString(arg)}
	}
	panic(NewError("RuntimeError", "url.URL expects string or dict"))
}

// URLStringify backs url.stringify(dict): builds a URL string from a parts dict.
func URLStringify(parts any) string { return urlBuildString(parts) }

func (u *URLValue) parse(method string) *url.URL {
	parsed, err := url.Parse(u.raw)
	if err != nil {
		panic(NewError("RuntimeError", "url.URL."+method+": "+err.Error()))
	}
	return parsed
}

func (u *URLValue) ToString() string { return u.parse("toString").String() }

func (u *URLValue) ToDict() *OrderedDict[string, any] { return urlPartsDict(u.parse("toDict")) }

func (u *URLValue) Scheme() string { return u.parse("scheme").Scheme }

func (u *URLValue) Host() string { return u.parse("host").Hostname() }

func (u *URLValue) Port() string { return u.parse("port").Port() }

func (u *URLValue) Path() string { return u.parse("path").Path }

func (u *URLValue) Query() *OrderedDict[string, any] { return urlQueryDict(u.parse("query").Query()) }

func (u *URLValue) Fragment() string { return u.parse("fragment").Fragment }

func (u *URLValue) WithScheme(s string) *URLValue {
	p := u.parse("withScheme")
	p.Scheme = s
	return &URLValue{raw: p.String()}
}

func (u *URLValue) WithHost(s string) *URLValue {
	p := u.parse("withHost")
	p.Host = s
	return &URLValue{raw: p.String()}
}

func (u *URLValue) WithPath(s string) *URLValue {
	p := u.parse("withPath")
	p.Path = s
	return &URLValue{raw: p.String()}
}

func (u *URLValue) WithQuery(arg any) *URLValue {
	p := u.parse("withQuery")
	switch v := arg.(type) {
	case string:
		p.RawQuery = strings.TrimPrefix(v, "?")
	default:
		p.RawQuery = urlQueryValues(v).Encode()
	}
	return &URLValue{raw: p.String()}
}

func (u *URLValue) WithFragment(s string) *URLValue {
	p := u.parse("withFragment")
	p.Fragment = s
	return &URLValue{raw: p.String()}
}

func (u *URLValue) Resolve(arg any) *URLValue {
	p := u.parse("resolve")
	ref := urlFromArg(arg)
	return &URLValue{raw: p.ResolveReference(ref).String()}
}

func (u *URLValue) Normalize() *URLValue {
	p := u.parse("normalize")
	p.Path = pathlib.Clean(p.Path)
	if p.Path == "." {
		p.Path = ""
	}
	p.RawQuery = p.Query().Encode()
	return &URLValue{raw: p.String()}
}

func urlFromArg(arg any) *url.URL {
	var raw string
	switch v := arg.(type) {
	case *URLValue:
		raw = v.raw
	case string:
		raw = v
	default:
		panic(NewError("RuntimeError", "url.URL.resolve: value must be url.URL or string"))
	}
	ref, err := url.Parse(raw)
	if err != nil {
		panic(NewError("RuntimeError", "url.URL.resolve: "+err.Error()))
	}
	return ref
}

// urlPartsDict matches the interpreter's toDict: keys inserted sorted (the
// interpreter's nil-Order dict renders keys alphabetically).
func urlPartsDict(parsed *url.URL) *OrderedDict[string, any] {
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
	d := NewOrderedDict[string, any]()
	for _, k := range keys {
		d.Set(k, parts[k])
	}
	return d
}

// urlBuildString builds a URL string from a parts dict, mirroring the
// interpreter's buildURLString (scheme/host/port/path/query/fragment).
func urlBuildString(parts any) string {
	_, get, _ := urlStringKeyDict(parts)
	u := url.URL{
		Scheme:   urlDictString(parts, "scheme"),
		Path:     urlDictString(parts, "path"),
		Fragment: urlDictString(parts, "fragment"),
	}
	host := urlDictString(parts, "host")
	port := urlDictString(parts, "port")
	if host != "" && port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}
	if q, ok := get("query"); ok {
		u.RawQuery = urlQueryValues(q).Encode()
	}
	return u.String()
}

// urlStringKeyDict extracts (keys, getter) from either OrderedDict variant the
// transpiler emits for a string-keyed dict literal: homogeneous string values
// lower to [string]string, mixed values to [string]any.
func urlStringKeyDict(arg any) ([]string, func(string) (any, bool), bool) {
	switch d := arg.(type) {
	case *OrderedDict[string, any]:
		return d.Keys(), func(k string) (any, bool) { v, ok := d.Get(k); return v, ok }, true
	case *OrderedDict[string, string]:
		return d.Keys(), func(k string) (any, bool) { v, ok := d.Get(k); return v, ok }, true
	}
	return nil, nil, false
}

func urlDictString(parts any, key string) string {
	_, get, ok := urlStringKeyDict(parts)
	if !ok {
		return ""
	}
	if v, ok := get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func urlQueryValues(arg any) url.Values {
	keys, get, ok := urlStringKeyDict(arg)
	if !ok {
		panic(NewError("RuntimeError", "url.URL: query must be dict"))
	}
	query := url.Values{}
	for _, k := range keys {
		raw, _ := get(k)
		switch value := raw.(type) {
		case string:
			query.Add(k, value)
		case []any:
			for _, item := range value {
				s, ok := item.(string)
				if !ok {
					panic(NewError("RuntimeError", "url.URL: query list values must be strings"))
				}
				query.Add(k, s)
			}
		case []string:
			for _, item := range value {
				query.Add(k, item)
			}
		default:
			panic(NewError("RuntimeError", "url.URL: query values must be strings or lists of strings"))
		}
	}
	return query
}
