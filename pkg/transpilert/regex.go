package transpilert

import (
	"fmt"
	"regexp"
	"sort"
)

// Regex bridge over Go's stdlib regexp (RE2), the same engine the interpreter's
// re module and string regex methods use, so results are byte-identical. An
// invalid pattern panics *Error{Class:"RuntimeError"} so the uncaught render and
// exit code match the interpreter's native-error path.

func compileRegex(pattern, label string) *regexp.Regexp {
	re, err := regexp.Compile(pattern)
	if err != nil {
		panic(NewError("RuntimeError", fmt.Sprintf("%s: invalid pattern: %v", label, err)))
	}
	return re
}

// StringMatchesRegex backs the string.matchesRegex method (re.test).
func StringMatchesRegex(text, pattern string) bool {
	return compileRegex(pattern, "re.test").MatchString(text)
}

// StringSplitRegex backs the string.splitRegex method (re.split).
func StringSplitRegex(text, pattern string) []string {
	return compileRegex(pattern, "re.split").Split(text, -1)
}

// StringReplaceRegex backs the string.replaceRegex method (re.replace); the
// replacement honours Go's $1 / ${name} expansion, matching the interpreter.
func StringReplaceRegex(text, pattern, replacement string) string {
	return compileRegex(pattern, "re.replace").ReplaceAllString(text, replacement)
}

// ReReplace backs the re.replace free function: (pattern, replacement, text).
func ReReplace(pattern, replacement, text string) string {
	return compileRegex(pattern, "re.replace").ReplaceAllString(text, replacement)
}

// RePattern wraps a compiled RE2 regex; it backs re.compile(pattern) and its
// chained methods (test/find/findAll/match/matchAll/split/replace).
type RePattern struct{ re *regexp.Regexp }

// ReCompile backs re.compile(pattern).
func ReCompile(pattern string) *RePattern {
	return &RePattern{re: compileRegex(pattern, "re.compile")}
}

func (p *RePattern) Test(text string) bool { return p.re.MatchString(text) }

// Find returns the leftmost match, or nil (Geblang null) when there is none.
// A nullable string lowers to *string in --native, so nil represents null.
func (p *RePattern) Find(text string) *string {
	loc := p.re.FindStringIndex(text)
	if loc == nil {
		return nil
	}
	s := text[loc[0]:loc[1]]
	return &s
}

func (p *RePattern) FindAll(text string) []string {
	matches := p.re.FindAllString(text, -1)
	if matches == nil {
		return []string{}
	}
	return matches
}

func (p *RePattern) Split(text string) []string { return p.re.Split(text, -1) }

func (p *RePattern) Replace(replacement, text string) string {
	return p.re.ReplaceAllString(text, replacement)
}

// Match returns the match dict, or nil (Geblang null) when there is no match.
// Keys are inserted sorted (groups, named, text) so Show renders them in the
// same order as the interpreter, whose legacy-dict path sorts keys.
func (p *RePattern) Match(text string) *OrderedDict[string, any] {
	m := p.re.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	return p.matchDict(m)
}

func (p *RePattern) MatchAll(text string) []*OrderedDict[string, any] {
	all := p.re.FindAllStringSubmatch(text, -1)
	out := make([]*OrderedDict[string, any], 0, len(all))
	for _, m := range all {
		out = append(out, p.matchDict(m))
	}
	return out
}

func (p *RePattern) matchDict(m []string) *OrderedDict[string, any] {
	groups := make([]string, len(m))
	copy(groups, m)

	type namedPair struct{ name, value string }
	var named []namedPair
	for i, name := range p.re.SubexpNames() {
		if name == "" || i >= len(m) {
			continue
		}
		named = append(named, namedPair{name, m[i]})
	}
	sort.Slice(named, func(i, j int) bool { return named[i].name < named[j].name })
	namedDict := NewOrderedDict[string, string]()
	for _, np := range named {
		namedDict.Set(np.name, np.value)
	}

	d := NewOrderedDict[string, any]()
	d.Set("groups", groups)
	d.Set("named", namedDict)
	d.Set("text", m[0])
	return d
}
