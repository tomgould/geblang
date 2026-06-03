package native

import "github.com/rivo/uniseg"

// Graphemes splits s into Unicode grapheme clusters (UAX #29) - the
// user-perceived characters, so combining marks and emoji ZWJ/flag sequences
// stay together (unlike codepoint- or rune-based splitting).
func Graphemes(s string) []string {
	out := []string{}
	rest := s
	state := -1
	for len(rest) > 0 {
		var cluster string
		cluster, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		out = append(out, cluster)
	}
	return out
}

// GraphemeCount returns the number of grapheme clusters in s.
func GraphemeCount(s string) int {
	return uniseg.GraphemeClusterCount(s)
}

// TruncateGraphemes returns the first n grapheme clusters of s (the whole
// string if it has fewer). n <= 0 yields the empty string.
func TruncateGraphemes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	rest := s
	state := -1
	count := 0
	end := 0
	for len(rest) > 0 && count < n {
		var cluster string
		cluster, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		end += len(cluster)
		count++
	}
	return s[:end]
}
