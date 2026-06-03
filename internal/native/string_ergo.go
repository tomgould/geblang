package native

import (
	"strings"
	"unicode"
)

// String ergonomics helpers shared by both backends so the eval and VM
// dispatches stay in lockstep.

// StringCapitalize upper-cases the first rune and lower-cases the rest.
func StringCapitalize(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	return string(unicode.ToUpper(r[0])) + strings.ToLower(string(r[1:]))
}

// StringTitle title-cases each whitespace-separated word (first rune upper,
// remaining runes lower); whitespace is preserved verbatim.
func StringTitle(s string) string {
	var b strings.Builder
	inWord := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			inWord = false
			b.WriteRune(r)
		case !inWord:
			b.WriteRune(unicode.ToUpper(r))
			inWord = true
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// StringLines splits on line boundaries (\n and \r\n). A trailing newline
// does not yield a final empty element; the empty string yields no lines.
func StringLines(s string) []string {
	if s == "" {
		return []string{}
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	parts := strings.Split(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func StringRemovePrefix(s, prefix string) string { return strings.TrimPrefix(s, prefix) }

func StringRemoveSuffix(s, suffix string) string { return strings.TrimSuffix(s, suffix) }

func StringIsBlank(s string) bool { return strings.TrimSpace(s) == "" }

func StringEqualsIgnoreCase(a, b string) bool { return strings.EqualFold(a, b) }

func StringContainsIgnoreCase(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
