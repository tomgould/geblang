package lsp

import (
	"sort"
	"strings"
	"unicode"
)

const (
	completionKindModule   = 9
	completionKindFunction = 3
	completionKindClass    = 7
	completionKindKeyword  = 14
)

func (s *server) completions(params CompletionParams) []CompletionItem {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return []CompletionItem{}
	}
	prefix := sourcePrefix(source, params.Position)
	if mod, ok := moduleMemberContext(prefix); ok {
		return moduleCompletionItems(mod)
	}
	// Try primitive-type method completion: `someDecimal.<TAB>` -> the
	// methods registered for `decimal`. Type is inferred from the most
	// recent `<primitiveType> <name>` declaration in the file (a
	// lightweight lexical scan; explicit type annotations only).
	if typ, ok := primitiveMemberContext(prefix, source); ok {
		return primitiveMethodCompletionItems(typ)
	}
	if importPrefix, ok := importContext(prefix); ok {
		return moduleNameCompletionItems(importPrefix)
	}
	idPrefix := identifierPrefix(prefix)
	items := topLevelCompletionItems(idPrefix)
	items = append(items, userSymbolCompletionItems(source, idPrefix)...)
	return items
}

func (s *server) signatureHelp(params SignatureHelpParams) SignatureHelp {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return SignatureHelp{}
	}
	prefix := sourcePrefix(source, params.Position)
	module, name, argPrefix, ok := callContext(prefix)
	if !ok {
		return SignatureHelp{}
	}
	fn, ok := lookupFunction(module, name)
	if !ok {
		return SignatureHelp{}
	}
	return SignatureHelp{
		Signatures: []SignatureInformation{{
			Label:         fn.signature(),
			Documentation: fn.doc,
			Parameters:    parameterInformation(fn.params),
		}},
		ActiveSignature: 0,
		ActiveParameter: activeParameter(argPrefix, len(fn.params)),
	}
}

func (s *server) document(uri string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.docs[uri]
	return source, ok
}

func sourcePrefix(source string, pos Position) string {
	lines := strings.Split(source, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return ""
	}
	line := lines[pos.Line]
	if pos.Character < 0 {
		pos.Character = 0
	}
	if pos.Character > len(line) {
		pos.Character = len(line)
	}
	return line[:pos.Character]
}

func moduleMemberContext(prefix string) (string, bool) {
	trimmed := strings.TrimRightFunc(prefix, isIdentRune)
	if !strings.HasSuffix(trimmed, ".") {
		return "", false
	}
	beforeDot := strings.TrimSpace(strings.TrimSuffix(trimmed, "."))
	name := trailingIdentifier(beforeDot)
	if name == "" {
		return "", false
	}
	if _, ok := stdlibCatalog[name]; ok {
		return name, true
	}
	return "", false
}

func importContext(prefix string) (string, bool) {
	trimmed := strings.TrimSpace(prefix)
	if trimmed == "import" {
		return "", true
	}
	if strings.HasPrefix(trimmed, "import ") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "import ")), true
	}
	return "", false
}

func callContext(prefix string) (module, name, args string, ok bool) {
	open := strings.LastIndex(prefix, "(")
	if open < 0 {
		return "", "", "", false
	}
	callee := strings.TrimSpace(prefix[:open])
	args = prefix[open+1:]
	parts := strings.Split(callee, ".")
	if len(parts) == 1 {
		name = trailingIdentifier(parts[0])
		if name == "" {
			return "", "", "", false
		}
		return "", name, args, true
	}
	name = trailingIdentifier(parts[len(parts)-1])
	module = trailingIdentifier(parts[len(parts)-2])
	if module == "" || name == "" {
		return "", "", "", false
	}
	return module, name, args, true
}

func identifierPrefix(prefix string) string {
	return trailingIdentifier(prefix)
}

func trailingIdentifier(text string) string {
	i := len(text)
	for i > 0 {
		r, size := runeBefore(text[:i])
		if !isIdentRune(r) {
			break
		}
		i -= size
	}
	return text[i:]
}

func runeBefore(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xc0 != 0x80 {
			r := []rune(s[i:])
			if len(r) == 0 {
				return rune(s[i]), 1
			}
			return r[0], len(s) - i
		}
	}
	return 0, 0
}

func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func moduleNameCompletionItems(prefix string) []CompletionItem {
	names := moduleNames()
	items := make([]CompletionItem, 0, len(names))
	for _, name := range names {
		if prefix == "" || strings.HasPrefix(name, prefix) {
			items = append(items, CompletionItem{Label: name, Kind: completionKindModule, Detail: "stdlib module"})
		}
	}
	return items
}

func moduleCompletionItems(module string) []CompletionItem {
	mod, ok := stdlibCatalog[module]
	if !ok {
		return []CompletionItem{}
	}
	names := make([]string, 0, len(mod.functions)+len(mod.classes))
	for name := range mod.functions {
		names = append(names, name)
	}
	for name := range mod.classes {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]CompletionItem, 0, len(names))
	for _, name := range names {
		if fn, ok := mod.functions[name]; ok {
			items = append(items, CompletionItem{
				Label:         name,
				Kind:          completionKindFunction,
				Detail:        fn.signature(),
				Documentation: fn.doc,
			})
			continue
		}
		items = append(items, CompletionItem{
			Label:  name,
			Kind:   completionKindClass,
			Detail: mod.classes[name],
		})
	}
	return items
}

// primitiveMemberContext reports whether the cursor is on a selector
// `name.<cursor>` where `name` is a variable of a known primitive
// type (string, int, float, decimal, bool, bytes, list, dict, set,
// range). On a hit it returns the primitive-type keyword so the
// caller can look up its method table. Misses fall through to the
// next completion path.
//
// Type inference is intentionally simple: a lexical scan over the
// document collects the most recent `<primitive> <name>` declaration
// before the cursor. Inferred `let x = ...` and complex type
// expressions (`?string`, `list<int>`) are out of scope - autocomplete
// is best-effort.
func primitiveMemberContext(prefix, source string) (string, bool) {
	trimmed := strings.TrimRightFunc(prefix, isIdentRune)
	if !strings.HasSuffix(trimmed, ".") {
		return "", false
	}
	beforeDot := strings.TrimSpace(strings.TrimSuffix(trimmed, "."))
	name := trailingIdentifier(beforeDot)
	if name == "" {
		return "", false
	}
	types := fileVarTypes(source)
	typ, ok := types[name]
	if !ok {
		return "", false
	}
	if _, isPrimitive := primitiveTypeNames[typ]; !isPrimitive {
		return "", false
	}
	return typ, true
}

// primitiveMethodCompletionItems returns the LSP CompletionItems for
// every method registered on the given primitive type. Sorted for
// determinism so the editor always shows the same order.
func primitiveMethodCompletionItems(typ string) []CompletionItem {
	methods, ok := primitiveMethods[typ]
	if !ok {
		return []CompletionItem{}
	}
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]CompletionItem, 0, len(names))
	for _, name := range names {
		method := methods[name]
		items = append(items, CompletionItem{
			Label:         name,
			Kind:          completionKindFunction,
			Detail:        method.signature(),
			Documentation: method.doc,
		})
	}
	return items
}

// fileVarTypes walks the source for `<primitiveType> <name>`-shape
// declarations and records the declared type per variable name. The
// scan is line-oriented and rough but correct for the common case:
// the user wrote `decimal d = 3.14;` and later types `d.<TAB>`.
// Re-declarations overwrite earlier ones (last wins, mirroring lexical
// scoping in the broad sense).
func fileVarTypes(source string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(source, "\n")
	for _, line := range lines {
		// Strip line comments.
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Accept a leading `let ` / `const ` / `static ` prefix so
		// `let decimal d = ...` is recognised. Strip it for the type
		// match below.
		for _, prefix := range []string{"let ", "const ", "static "} {
			if strings.HasPrefix(line, prefix) {
				line = strings.TrimPrefix(line, prefix)
				line = strings.TrimSpace(line)
			}
		}
		// Look for `TYPE NAME` where TYPE is a primitive keyword and
		// NAME is a simple identifier. The line must continue with
		// `=` or `;` (declaration) - we don't want to misread function
		// parameter lists or expressions.
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		typ := parts[0]
		if _, ok := primitiveTypeNames[typ]; !ok {
			continue
		}
		name := parts[1]
		// Trim the trailing `;` or `,` or `=...` if present.
		for _, sep := range []string{"=", ";", ","} {
			if idx := strings.Index(name, sep); idx >= 0 {
				name = name[:idx]
			}
		}
		name = strings.TrimSpace(name)
		if name == "" || !isIdentName(name) {
			continue
		}
		out[name] = typ
	}
	return out
}

// isIdentName reports whether s is a plain identifier (letters,
// digits, underscores, not starting with a digit).
func isIdentName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && (r >= '0' && r <= '9') {
			return false
		}
		if !isIdentRune(r) {
			return false
		}
	}
	return true
}

func userSymbolCompletionItems(source, prefix string) []CompletionItem {
	syms := extractSymbols(source)
	items := make([]CompletionItem, 0, len(syms))
	seen := map[string]bool{}
	for _, sym := range syms {
		if prefix != "" && !strings.HasPrefix(sym.name, prefix) {
			continue
		}
		if seen[sym.name] {
			continue
		}
		seen[sym.name] = true
		kind := completionKindFunction
		switch sym.kind {
		case symbolKindClass:
			kind = completionKindClass
		case symbolKindVariable, symbolKindConstant:
			kind = 6 // variable kind in LSP CompletionItemKind
		}
		items = append(items, CompletionItem{
			Label:  sym.name,
			Kind:   kind,
			Detail: sym.detail,
		})
	}
	return items
}

func topLevelCompletionItems(prefix string) []CompletionItem {
	items := moduleNameCompletionItems(prefix)
	for _, kw := range []string{"import", "export", "module", "init", "func", "class", "interface", "enum", "let", "var", "const", "type", "if", "else", "for", "in", "while", "with", "del", "return", "async", "await", "yield", "try", "catch", "throw"} {
		if prefix == "" || strings.HasPrefix(kw, prefix) {
			items = append(items, CompletionItem{Label: kw, Kind: completionKindKeyword, Detail: "keyword"})
		}
	}
	if strings.HasPrefix(prefix, "_") || prefix == "" {
		for _, m := range magicMethods {
			if prefix == "" || strings.HasPrefix(m.name, prefix) {
				items = append(items, CompletionItem{Label: m.name, Kind: completionKindFunction, Detail: m.detail})
			}
		}
	}
	return items
}

// magicMethods enumerates the dunder methods recognised by the
// runtime. Surfaced as completions when the cursor sits on a name
// starting with `_` so editors can scaffold the right signature.
// Names match the recognised forms in internal/bytecode/vm.go and
// internal/evaluator/evaluator.go: most operators use a single
// trailing underscore segment (`__eq`, `__add`, ...). The four
// hooks `__enter__` / `__exit__` / `__serialize__` / `__deserialize__`
// keep the trailing `__` pair for historical reasons.
var magicMethods = []struct {
	name   string
	detail string
}{
	// Dynamic dispatch.
	{"__invoke", "callable-object dispatch (instance(...) and func-typed)"},
	{"__call", "fallback for unknown instance method names"},
	{"__callStatic", "fallback for unknown static method names"},
	{"__get", "fallback for unknown field reads"},
	{"__set", "fallback for unknown field writes"},
	{"__getStatic", "fallback for unknown static-value reads"},
	{"__setStatic", "fallback for unknown static-value writes"},
	{"__parentMsg", "explicit fallthrough to the parent class chain"},
	// Iteration protocol (1.0.6).
	{"__iter", "iterator-protocol: returns the iterator for `for in`"},
	{"__done", "iterator-protocol: true when iteration finished"},
	{"__next", "iterator-protocol: next value (called when not done)"},
	// Context managers.
	{"__enter__", "with-block entry hook (return value bound to `with (n = ...)`)"},
	{"__exit__", "with-block exit hook (always runs on any block exit)"},
	// Serialisation.
	{"__serialize__", "json/yaml/toml.stringify override (returns plain value)"},
	{"__deserialize__", "json/yaml/toml.parse factory (static method)"},
	// Type coercion.
	{"__string", "implicit-string conversion (str / interpolation / `+`)"},
	{"__int", "implicit-int conversion (cast-as / arithmetic)"},
	{"__float", "implicit-float conversion"},
	{"__decimal", "implicit-decimal conversion"},
	{"__bool", "implicit-bool conversion (truthiness / `if`)"},
	{"__bytes", "implicit-bytes conversion"},
	// Comparison.
	{"__eq", "equality operator (==)"},
	{"__lt", "less-than operator (<)"},
	{"__lte", "less-than-or-equal operator (<=)"},
	{"__gt", "greater-than operator (>)"},
	{"__gte", "greater-than-or-equal operator (>=)"},
	// Arithmetic operators.
	{"__add", "addition operator (+)"},
	{"__sub", "subtraction operator (-)"},
	{"__mul", "multiplication operator (*)"},
	{"__div", "division operator (/)"},
	{"__intdiv", "integer-division operator (//)"},
	{"__mod", "modulo operator (%)"},
	{"__pow", "exponentiation operator (**)"},
	{"__neg", "unary negation (-)"},
	{"__not", "logical-not operator (!)"},
	// Bitwise operators.
	{"__bitand", "bitwise-AND operator (&)"},
	{"__bitor", "bitwise-OR operator (|)"},
	{"__bitxor", "bitwise-XOR operator (^)"},
	{"__bitnot", "bitwise-NOT operator (~)"},
	{"__lshift", "left-shift operator (<<)"},
	{"__rshift", "right-shift operator (>>)"},
}

func lookupFunction(module, name string) (functionDoc, bool) {
	if module != "" {
		mod, ok := stdlibCatalog[module]
		if !ok {
			return functionDoc{}, false
		}
		fn, ok := mod.functions[name]
		return fn, ok
	}
	for _, mod := range stdlibCatalog {
		if fn, ok := mod.functions[name]; ok {
			return fn, true
		}
	}
	return functionDoc{}, false
}

func parameterInformation(params []string) []ParameterInformation {
	out := make([]ParameterInformation, len(params))
	for i, p := range params {
		out[i] = ParameterInformation{Label: p}
	}
	return out
}

func activeParameter(args string, max int) int {
	if max == 0 {
		return 0
	}
	depth := 0
	active := 0
	inString := rune(0)
	escaped := false
	for _, r := range args {
		if inString != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == inString {
				inString = 0
			}
			continue
		}
		switch r {
		case '"', '\'', '`':
			inString = r
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				active++
			}
		}
	}
	if active >= max {
		return max - 1
	}
	return active
}
