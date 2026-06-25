// Package doctest parses the `gb` fenced code blocks in the user docs so a broken example fails the build.
package doctest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// Failure is a documentation code block that failed to parse.
type Failure struct {
	File string
	Line int
	Errs []string
}

func (f Failure) String() string {
	return fmt.Sprintf("%s:%d: %s", f.File, f.Line, strings.Join(f.Errs, "; "))
}

type block struct {
	code string
	line int
}

// CheckDocs parses every testable `gb` block under root and returns the failures.
func CheckDocs(root string) ([]Failure, error) {
	var failures []Failure
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".md") || filepath.Base(path) == "AGENTS.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, b := range extractBlocks(string(data)) {
			if !testable(b.code) {
				continue
			}
			if errs := parseErrors(b.code); len(errs) > 0 {
				failures = append(failures, Failure{File: path, Line: b.line, Errs: errs})
			}
		}
		return nil
	})
	return failures, err
}

func parseErrors(code string) (errs []string) {
	defer func() {
		if r := recover(); r != nil {
			errs = []string{fmt.Sprintf("panic: %v", r)}
		}
	}()
	p := parser.New(lexer.New(code))
	p.ParseProgram()
	return p.Errors()
}

func extractBlocks(src string) []block {
	var blocks []block
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t != "```gb" && t != "```geblang" {
			continue
		}
		skip := precededBySkip(lines, i)
		start := i + 2
		i++
		var body []string
		for i < len(lines) && strings.TrimSpace(lines[i]) != "```" {
			body = append(body, lines[i])
			i++
		}
		if !skip {
			blocks = append(blocks, block{code: strings.Join(body, "\n"), line: start})
		}
	}
	return blocks
}

// precededBySkip reports whether the nearest non-blank line above a fence carries a doctest:skip marker.
func precededBySkip(lines []string, fence int) bool {
	for j := fence - 1; j >= 0; j-- {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		return strings.Contains(lines[j], "doctest:skip")
	}
	return false
}

var ellipsisPlaceholder = regexp.MustCompile(`\.\.\.([^a-zA-Z_]|$)`)

// testable reports whether a block has a statement and is not a fragment.
func testable(code string) bool {
	if !strings.Contains(code, ";") || strings.Contains(code, "doctest:skip") {
		return false
	}
	for _, raw := range strings.Split(code, "\n") {
		if ellipsisPlaceholder.MatchString(strings.TrimSpace(raw)) {
			return false
		}
	}
	return !opensMidConstruct(code)
}

func opensMidConstruct(code string) bool {
	for _, raw := range strings.Split(code, "\n") {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "/*") {
			continue
		}
		switch t[0] {
		case '}', ')', ']', '.', '?', '|':
			return true
		}
		return strings.HasPrefix(t, "case ") || strings.HasPrefix(t, "default") ||
			strings.HasPrefix(t, "catch") || strings.HasPrefix(t, "else")
	}
	return false
}
