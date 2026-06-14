package emit

import (
	"strings"
	"unicode"
)

var goReserved = map[string]struct{}{}

func init() {
	words := []string{
		"break", "case", "chan", "const", "continue", "default", "defer",
		"else", "fallthrough", "for", "func", "go", "goto", "if", "import",
		"interface", "map", "package", "range", "return", "select", "struct",
		"switch", "type", "var",
		"any", "bool", "byte", "comparable", "complex64", "complex128", "error",
		"float32", "float64", "int", "int8", "int16", "int32", "int64",
		"rune", "string", "uint", "uint8", "uint16", "uint32", "uint64",
		"uintptr", "true", "false", "iota", "nil",
		"append", "cap", "clear", "close", "complex", "copy", "delete", "imag",
		"len", "make", "max", "min", "new", "panic", "print", "println", "real",
		"recover",
	}
	for _, w := range words {
		goReserved[w] = struct{}{}
	}
}

func IsGoReserved(s string) bool {
	_, ok := goReserved[s]
	return ok
}

func MangleIdent(name string) string {
	if name == "" {
		return "_blank"
	}
	var b strings.Builder
	for i, r := range name {
		switch {
		case unicode.IsLetter(r) || r == '_':
			b.WriteRune(r)
		case unicode.IsDigit(r):
			if i == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if IsGoReserved(out) {
		out += "_"
	}
	return out
}

func PackagePathFromModule(canonical string) string {
	canonical = strings.TrimSpace(canonical)
	if canonical == "" {
		return ""
	}
	parts := strings.Split(canonical, ".")
	for i, p := range parts {
		parts[i] = MangleIdent(p)
	}
	return strings.Join(parts, "/")
}

func PackageNameFromModule(canonical string) string {
	canonical = strings.TrimSpace(canonical)
	if canonical == "" {
		return "main"
	}
	if i := strings.LastIndexByte(canonical, '.'); i >= 0 {
		return MangleIdent(canonical[i+1:])
	}
	return MangleIdent(canonical)
}
