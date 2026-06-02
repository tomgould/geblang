package token_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"geblang/internal/token"
)

// findGrammar walks up from the test directory to the repo root and
// returns the editor grammar path, or "" if absent (e.g. the dist
// checkout, which does not ship the VS Code extension).
func findGrammar() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, "vscode-geblang", "syntaxes", "geblang.tmLanguage.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

var grammarAltRe = regexp.MustCompile(`\\b\(([^)]*)\)\\b`)
var grammarWordRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

func collectGrammarPatterns(v interface{}, out *[][2]string) {
	switch x := v.(type) {
	case map[string]interface{}:
		name, hasName := x["name"].(string)
		match, hasMatch := x["match"].(string)
		if hasName && hasMatch {
			*out = append(*out, [2]string{name, match})
		}
		for _, val := range x {
			collectGrammarPatterns(val, out)
		}
	case []interface{}:
		for _, val := range x {
			collectGrammarPatterns(val, out)
		}
	}
}

// TestGrammarKeywordsMatchTokenKeywords keeps the editor's TextMate
// grammar in sync with the canonical keyword set (token.Keywords):
// every keyword the grammar highlights must be a real keyword, and
// every real keyword must be highlighted somewhere (keyword, language
// constant, or primitive-type group). Closes the last unguarded IDE
// surface.
func TestGrammarKeywordsMatchTokenKeywords(t *testing.T) {
	path := findGrammar()
	if path == "" {
		t.Skip("editor grammar not present (not the monorepo checkout)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read grammar: %v", err)
	}
	var doc interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse grammar JSON: %v", err)
	}
	var patterns [][2]string
	collectGrammarPatterns(doc, &patterns)

	keywords := map[string]bool{}
	for _, k := range token.Keywords() {
		keywords[k] = true
	}

	grammarKeywordWords := map[string]bool{} // keyword.* + constant.language
	highlighted := map[string]bool{}         // those plus primitive types

	for _, p := range patterns {
		name, match := p[0], p[1]
		isKeyword := strings.HasPrefix(name, "keyword.") || strings.HasPrefix(name, "constant.language")
		isType := strings.HasPrefix(name, "support.type.primitive")
		if !isKeyword && !isType {
			continue
		}
		m := grammarAltRe.FindStringSubmatch(match)
		if m == nil {
			continue // symbol operators etc. - not a \b(word|word)\b list
		}
		for _, w := range strings.Split(m[1], "|") {
			if !grammarWordRe.MatchString(w) {
				continue
			}
			if isKeyword {
				grammarKeywordWords[w] = true
			}
			highlighted[w] = true
		}
	}

	var phantom []string
	for w := range grammarKeywordWords {
		if !keywords[w] {
			phantom = append(phantom, w)
		}
	}
	if len(phantom) > 0 {
		sort.Strings(phantom)
		t.Errorf("grammar highlights words that are not keywords: %v (remove or reclassify)", phantom)
	}

	var missing []string
	for k := range keywords {
		if !highlighted[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("keywords not highlighted by the editor grammar: %v (add to the grammar)", missing)
	}
}
