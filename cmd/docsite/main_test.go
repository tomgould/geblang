package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAPIPagesBuildsVirtualPages(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "api.gb")
	if err := os.WriteFile(source, []byte(`module app.api;

## Handles an API status check.
export @route("GET", "/status")
func status(): dict<string, any> {
    return {"ok": true};
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	pages, err := loadAPIPages([]string{source})
	if err != nil {
		t.Fatalf("load api pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages: %#v", pages)
	}
	page := pages[0]
	if !strings.HasPrefix(page.Output, "api/") || !strings.HasSuffix(page.Output, ".html") {
		t.Fatalf("output: %q", page.Output)
	}
	for _, want := range []string{
		"API: api",
		"app.api",
		"Function",
		"status",
		"Handles an API status check.",
		"route(&quot;GET&quot;, &quot;/status&quot;)",
	} {
		if !strings.Contains(page.HTML, want) {
			t.Fatalf("html missing %q: %q", want, page.HTML)
		}
	}
}

func TestAPISlug(t *testing.T) {
	tests := map[string]string{
		"stdlib":               "stdlib",
		"examples/api_docs.gb": "examples-api-docs-gb",
		"./":                   "source",
	}
	for input, want := range tests {
		if got := apiSlug(input); got != want {
			t.Fatalf("apiSlug(%q): got %q, want %q", input, got, want)
		}
	}
}

func TestMarkdownToHTMLSupportsGitHubEmojiAndStyledQuotes(t *testing.T) {
	html := markdownToHTML([]byte("> :bulb: Use `:bulb:` in docs.\n\n`:warning:`\n"))
	if !strings.Contains(html, "&#x1f4a1;") {
		t.Fatalf("expected :bulb: to render as emoji: %q", html)
	}
	if strings.Contains(html, "<code>&#x26a0;&#xfe0f;</code>") {
		t.Fatalf("emoji inside code should not be substituted: %q", html)
	}
	if !strings.Contains(html, "<blockquote>") {
		t.Fatalf("expected blockquote HTML: %q", html)
	}
}

func TestLayoutIncludesGeblangSyntaxHighlighter(t *testing.T) {
	html := layout([]page{{Output: "index.html", Title: "Home", HTML: `<pre><code class="language-gb">let x = 1;</code></pre>`}}, page{Output: "index.html", Title: "Home", HTML: `<pre><code class="language-gb">let x = 1;</code></pre>`}, nil, nil)
	for _, want := range []string{
		"highlightGeblang",
		"pre code.language-gb",
		"hl-keyword",
		"hl-string",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("layout missing %q: %q", want, html)
		}
	}
}

func TestLoadExamplePagesBuildsIndexGroupAndFilePages(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "README.md"), []byte("# App Example\n\nA multi-file example.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `/**
 * @title App Main
 * @summary Runs the application.
 * @category Applications
 * @description Shows how a multi-file example can be documented.
 */
import io;

io.println("app");
`
	if err := os.WriteFile(filepath.Join(appDir, "main.gb"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "service.gb"), []byte(`/**
 * @title App Service
 * @summary Provides app services.
 * @category Applications
 */
export func name(): string { return "app"; }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pages, err := loadExamplePages(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 4 {
		t.Fatalf("expected index, group and two file pages, got %#v", pages)
	}
	if pages[0].Title != "Examples" || pages[0].Output != "examples/index.html" {
		t.Fatalf("unexpected index page %#v", pages[0])
	}
	if pages[1].Output != "examples/app/index.html" {
		t.Fatalf("unexpected group output %q", pages[1].Output)
	}
	filePage := pages[2]
	if filePage.Title != "App Main" {
		t.Fatalf("file title = %q", filePage.Title)
	}
	for _, want := range []string{
		"Runs the application.",
		"Shows how a multi-file example can be documented.",
		filepath.ToSlash(filepath.Join(dir, "app", "main.gb")),
		`<pre><code class="language-gb">import io;`,
	} {
		if !strings.Contains(filePage.HTML, want) {
			t.Fatalf("example html missing %q: %q", want, filePage.HTML)
		}
	}
	if strings.Contains(filePage.HTML, "@title") {
		t.Fatalf("example code should omit metadata docblock: %q", filePage.HTML)
	}
}
