package native

import (
	"regexp"
	"unicode/utf8"
)

// CompileSearchRegex compiles a pattern for the search* primitive methods (RE2, matching the `re` module).
func CompileSearchRegex(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}

// StringMatchRunePositions returns every rune index where sub occurs in s (overlapping); empty sub matches nothing.
func StringMatchRunePositions(s, sub string) []int {
	if sub == "" {
		return nil
	}
	runes := []rune(s)
	subRunes := []rune(sub)
	out := []int{}
	for i := 0; i+len(subRunes) <= len(runes); i++ {
		match := true
		for j := range subRunes {
			if runes[i+j] != subRunes[j] {
				match = false
				break
			}
		}
		if match {
			out = append(out, i)
		}
	}
	return out
}

// RegexMatchRunePositions returns the rune index of each regex match start in s.
func RegexMatchRunePositions(re *regexp.Regexp, s string) []int {
	locs := re.FindAllStringIndex(s, -1)
	out := make([]int, 0, len(locs))
	for _, loc := range locs {
		out = append(out, utf8.RuneCountInString(s[:loc[0]]))
	}
	return out
}
