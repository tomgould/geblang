package formatter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"geblang/internal/formatter"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// astFingerprint returns the program's String() (parenthesized, position-free) and whether src parsed cleanly.
func astFingerprint(src string) (string, bool) {
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if len(p.Errors()) != 0 {
		return strings.Join(p.Errors(), "; "), false
	}
	return prog.String(), true
}

// TestFormatPreservesAST asserts that formatting every valid .gb file leaves the AST unchanged and still parsing.
func TestFormatPreservesAST(t *testing.T) {
	roots := []string{"../../tests", "../../examples", "../../gebweb/src", "../../gebweb/tests", "../../stdlib"}
	var files []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".gb") {
				return nil
			}
			files = append(files, path)
			return nil
		})
	}
	if len(files) == 0 {
		t.Skip("no corpus files found")
	}

	var refused, valid int
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		before, ok := astFingerprint(string(src))
		if !ok {
			continue
		}
		valid++
		out, err := formatter.Format(src)
		if err != nil {
			refused++ // safe refusal (the self-check caught a printer limitation); never corruption
			continue
		}
		after, ok := astFingerprint(string(out))
		if !ok {
			t.Errorf("SAFETY VIOLATION (output unparseable): %s", path)
			continue
		}
		if before != after {
			t.Errorf("SAFETY VIOLATION (AST changed): %s\n  before: %s\n  after:  %s", path, firstDiff(before, after, 160), firstDiff(after, before, 160))
			continue
		}
		if out2, err := formatter.Format(out); err != nil || string(out2) != string(out) {
			t.Errorf("NOT IDEMPOTENT: %s (a second format pass changed the output)", path)
		}
	}
	t.Logf("corpus: %d valid files, %d formatted cleanly, %d safely refused (printer limitations to fix)", valid, valid-refused, refused)
}

// firstDiff returns a window of a around the first byte where a and b differ.
func firstDiff(a, b string, width int) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	start := i - width/4
	if start < 0 {
		start = 0
	}
	end := start + width
	if end > len(a) {
		end = len(a)
	}
	return a[start:end]
}
