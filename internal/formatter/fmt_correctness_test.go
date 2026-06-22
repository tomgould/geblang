package formatter_test

import (
	"strings"
	"testing"

	"geblang/internal/formatter"
)

// assertFormat formats input, checks it equals want, and checks idempotency.
func assertFormat(t *testing.T, input, want string) {
	t.Helper()
	out, err := formatter.Format([]byte(input))
	if err != nil {
		t.Fatalf("Format(%q) errored: %v", input, err)
	}
	got := strings.TrimRight(string(out), "\n")
	if got != want {
		t.Errorf("Format(%q)\n  got:  %q\n  want: %q", input, got, want)
	}
	out2, err := formatter.Format(out)
	if err != nil {
		t.Fatalf("Format(Format(%q)) errored: %v", input, err)
	}
	if string(out2) != string(out) {
		t.Errorf("not idempotent for %q:\n  pass1: %q\n  pass2: %q", input, string(out), string(out2))
	}
}

func TestFormatPrecedenceParens(t *testing.T) {
	cases := []struct{ in, want string }{
		{"let x = (a || b) && c;", "let x = (a || b) && c;"},
		{"let y = a && b || c;", "let y = a && b || c;"},
		{"let z = !(a && b);", "let z = !(a && b);"},
		{"let t = (j as dict<string, any>)[\"k\"];", "let t = (j as dict<string, any>)[\"k\"];"},
		{"let v = (a ?? []) as list<any>;", "let v = (a ?? []) as list<any>;"},
		{"let p = a ** b ** c;", "let p = a ** b ** c;"},
		{"let q = (a ** b) ** c;", "let q = (a ** b) ** c;"},
		{"let s = a.b.c.d;", "let s = a.b.c.d;"},
		{"let m = (x as Foo).bar;", "let m = (x as Foo).bar;"},
		{"let n = (a - b) - c;", "let n = (a - b) - c;"},
		{"let o = a - (b - c);", "let o = a - (b - c);"},
	}
	for _, c := range cases {
		assertFormat(t, c.in, c.want)
	}
}

// assertFormatHas formats input, asserts each substring is present, and checks idempotency.
func assertFormatHas(t *testing.T, input string, must ...string) {
	t.Helper()
	out, err := formatter.Format([]byte(input))
	if err != nil {
		t.Fatalf("Format(%q) errored: %v", input, err)
	}
	got := string(out)
	for _, m := range must {
		if !strings.Contains(got, m) {
			t.Errorf("Format(%q) missing %q\n  got: %s", input, m, got)
		}
	}
	out2, err := formatter.Format(out)
	if err != nil || string(out2) != got {
		t.Errorf("not idempotent for %q", input)
	}
}

func TestFormatStructural(t *testing.T) {
	// Single-statement, single-line round-trips.
	exact := []struct{ in, want string }{
		{"import async.scope as ascope;", "import async.scope as ascope;"},
		{"let b = Box<string>(42);", "let b = Box<string>(42);"},
		{"const x = 1;", "const x = 1;"},
		{"const int y = 2;", "const int y = 2;"},
		{"let int z = 3;", "let int z = 3;"},
		{"let a = xs[::-1];", "let a = xs[::-1];"},
		{"let c = xs[1:3];", "let c = xs[1:3];"},
		{"let d = xs[4:0:-1];", "let d = xs[4:0:-1];"},
		{"let e = (x as int) | (y as int) << 8;", "let e = (x as int) | (y as int) << 8;"},
	}
	for _, c := range exact {
		assertFormat(t, c.in, c.want)
	}
}

func TestFormatDecorators(t *testing.T) {
	assertFormatHas(t, "class C {\n@Cache(ttl: 60)\nfunc f(): int { return 1; }\n}", "@Cache(ttl: 60)")
	assertFormatHas(t, "@Controller\nclass C { }", "@Controller")
	// a bare decorator must not gain empty parens
	out, _ := formatter.Format([]byte("@Controller\nclass C { }"))
	if strings.Contains(string(out), "@Controller(") {
		t.Errorf("bare decorator gained parens: %s", out)
	}
	assertFormatHas(t, "class C {\n@HealthCheck(name: \"memory\")\nfunc h(): void { }\n}", "@HealthCheck(name: \"memory\")")
}

func TestFormatMatch(t *testing.T) {
	prog := `func classify(int n): string {
    return match (n) {
        case 0 => "zero";
        case 1 | 2 | 3 => "small";
        case int x if (x < 0) => "neg";
        default => "big";
    };
}`
	assertFormatHas(t, prog, `case 0 => "zero";`, `case 1 | 2 | 3 => "small";`, `if (x < 0)`, "default => ")
}

func TestFormatParamDefaults(t *testing.T) {
	assertFormatHas(t, "func f(dict<string, any> o = {}): void { }", "= {}")
	assertFormatHas(t, "func g(list<int> xs = []): void { }", "= []")
}

func TestFormatPreservesComments(t *testing.T) {
	assertFormatHas(t, "/** a doc block */\nfunc f(): void { }", "/** a doc block */")
	assertFormatHas(t, "# a line comment\nlet x = 1;", "# a line comment")
	assertFormatHas(t, "/* a block */\nlet y = 2;", "/* a block */")
	assertFormatHas(t, "## a doc line\nlet z = 3;", "## a doc line")
	// inside a class + method
	assertFormatHas(t, "class C {\n/* field note */\nint x;\nfunc m(): void {\n# inner\nthis.x = 1;\n}\n}", "/* field note */", "# inner")
	// a doc comment must appear exactly once (not double-emitted from the AST Doc field)
	out, _ := formatter.Format([]byte("/** once */\nfunc f(): void { }"))
	if n := strings.Count(string(out), "/** once */"); n != 1 {
		t.Errorf("doc comment emitted %d times, want 1:\n%s", n, out)
	}
}

func TestFormatLayout(t *testing.T) {
	// blank line inside a class body is preserved
	out, _ := formatter.Format([]byte("class C {\nint a;\n\nint b;\n}"))
	if !strings.Contains(string(out), "int a;\n\n    int b;") {
		t.Errorf("blank line inside class not preserved:\n%s", out)
	}
	// blank line inside a function body is preserved
	fb, _ := formatter.Format([]byte("func f(): void {\nlet a = 1;\n\nlet b = 2;\n}"))
	if !strings.Contains(string(fb), "let a = 1;\n\n    let b = 2;") {
		t.Errorf("blank line inside function not preserved:\n%s", fb)
	}
	// a multi-line +/&&/|| chain stays multi-line and is idempotent
	mi, _ := formatter.Format([]byte("func g(): string {\nreturn \"a\"\n+ \"b\"\n+ \"c\";\n}"))
	if !strings.Contains(string(mi), "\"a\"\n") || !strings.Contains(string(mi), "+ \"b\"") {
		t.Errorf("multi-line infix collapsed:\n%s", mi)
	}
	mi2, _ := formatter.Format(mi)
	if string(mi2) != string(mi) {
		t.Errorf("multi-line infix not idempotent:\n%s\n---\n%s", mi, mi2)
	}
	// a single-line chain stays inline
	si, _ := formatter.Format([]byte("let x = \"a\" + \"b\" + \"c\";"))
	if strings.Count(string(si), "\n") != 1 {
		t.Errorf("single-line concat got broken: %q", si)
	}
}

func TestFormatPreservesExplicitParens(t *testing.T) {
	// Redundant grouping parentheses the author wrote are kept, not stripped.
	cases := []struct{ in, want string }{
		{"let c = (((a || b) && k) || (d && e && f));", "let c = (((a || b) && k) || (d && e && f));"},
		{"let g = ((lat as float) * GRID) as int;", "let g = ((lat as float) * GRID) as int;"},
		{"let h = (x[\"exp\"] as int) > now;", "let h = (x[\"exp\"] as int) > now;"},
		{"let i = (a + b) * c;", "let i = (a + b) * c;"},
		// but no parens are invented where none were written:
		{"let m = a || b && c;", "let m = a || b && c;"},
		{"let n = a + b * c;", "let n = a + b * c;"},
	}
	for _, c := range cases {
		assertFormat(t, c.in, c.want)
	}
}

func TestFormatMethodChain(t *testing.T) {
	// a method chain the author split across lines stays multi-line + idempotent
	in := "func f(): void {\nlet r = http.request(u)\n.withMethod(\"POST\")\n.withBody(b)\n.send();\n}"
	out, _ := formatter.Format([]byte(in))
	if !strings.Contains(string(out), "http.request(u)\n") || !strings.Contains(string(out), ".withMethod(\"POST\")") {
		t.Errorf("method chain flattened:\n%s", out)
	}
	out2, _ := formatter.Format(out)
	if string(out2) != string(out) {
		t.Errorf("method chain not idempotent")
	}
	// a single-line chain stays inline
	si, _ := formatter.Format([]byte("let x = a.b().c().d();"))
	if strings.Count(string(si), "\n") != 1 {
		t.Errorf("single-line chain broken: %q", si)
	}
}

func TestFormatTrailingComments(t *testing.T) {
	// a trailing same-line comment stays on the statement's line
	assertFormatHas(t, "let x = 1; /* note */", "let x = 1; /* note */")
	// a trailing comment inside a block stays inside the block (not pushed out)
	out, _ := formatter.Format([]byte("func f(): void {\nif (a) {\nreturn; /* done */\n}\nreturn;\n}"))
	if !strings.Contains(string(out), "return; /* done */") {
		t.Errorf("trailing comment not kept inline / moved out of block:\n%s", out)
	}
}

func TestFormatCleanMode(t *testing.T) {
	clean := func(in string) string {
		out, err := formatter.FormatWithOptions([]byte(in), formatter.Options{Clean: true})
		if err != nil {
			t.Fatalf("clean %q errored: %v", in, err)
		}
		// idempotent in clean mode too
		out2, _ := formatter.FormatWithOptions(out, formatter.Options{Clean: true})
		if string(out2) != string(out) {
			t.Errorf("clean not idempotent for %q", in)
		}
		return strings.TrimRight(string(out), "\n")
	}
	// redundant grouping parens stripped, precedence-required ones kept
	if got := clean("let c = (((a || b) && k) || (d && e && f));"); got != "let c = (a || b) && k || d && e && f;" {
		t.Errorf("clean parens: got %q", got)
	}
	if got := clean("let x = (a || b) && c;"); got != "let x = (a || b) && c;" {
		t.Errorf("clean dropped a needed paren: got %q", got)
	}
	// multi-line method chain flattened to one line
	if got := clean("func f(): void {\nlet r = a.b()\n.c()\n.d();\n}"); !strings.Contains(got, "let r = a.b().c().d();") {
		t.Errorf("clean did not flatten chain:\n%s", got)
	}
	// multi-line concat flattened to one line
	if got := clean("func g(): string {\nreturn \"a\"\n+ \"b\"\n+ \"c\";\n}"); !strings.Contains(got, "return \"a\" + \"b\" + \"c\";") {
		t.Errorf("clean did not flatten concat:\n%s", got)
	}
	// blank lines are KEPT in clean mode (structure, not redundant syntax)
	if got := clean("class C {\nint a;\n\nint b;\n}"); !strings.Contains(got, "int a;\n\n    int b;") {
		t.Errorf("clean dropped blank lines:\n%s", got)
	}
}

func TestFormatStripComments(t *testing.T) {
	out, err := formatter.FormatWithOptions(
		[]byte("/* a */\nlet x = 1; /* trailing */\n## doc\nlet y = 2;"),
		formatter.Options{StripComments: true})
	if err != nil {
		t.Fatalf("strip-comments errored: %v", err)
	}
	got := string(out)
	for _, c := range []string{"/* a */", "trailing", "## doc"} {
		if strings.Contains(got, c) {
			t.Errorf("strip-comments left %q:\n%s", c, got)
		}
	}
	if !strings.Contains(got, "let x = 1;") || !strings.Contains(got, "let y = 2;") {
		t.Errorf("strip-comments dropped code:\n%s", got)
	}
}

func TestFormatCollectionLayout(t *testing.T) {
	// an author-multiline list stays multi-line (one element per line, trailing comma)
	out, _ := formatter.Format([]byte("let x = [\n1,\n2,\n3\n];"))
	if !strings.Contains(string(out), "[\n    1,\n    2,\n    3,\n]") {
		t.Errorf("author-multiline list not preserved:\n%s", out)
	}
	// a short single-line list stays flat
	if flat, _ := formatter.Format([]byte("let y = [1, 2, 3];")); strings.TrimRight(string(flat), "\n") != "let y = [1, 2, 3];" {
		t.Errorf("short list got broken: %q", flat)
	}
	// a single collection argument hugs the call parens
	if hug, _ := formatter.Format([]byte("foo({\n\"a\": 1,\n\"b\": 2\n});")); !strings.Contains(string(hug), "foo({\n    \"a\": 1,") {
		t.Errorf("collection arg did not hug:\n%s", hug)
	}
	// a long flat call (over the 100-col width) wraps onto one arg per line
	if long, _ := formatter.Format([]byte("let r = call(argumentNumberOne, argumentNumberTwo, argumentNumberThree, argumentNumberFour, argumentNumberFive, argumentNumberSix);")); !strings.Contains(string(long), "call(\n    argumentNumberOne,") {
		t.Errorf("long call did not wrap:\n%s", long)
	}
	// clean mode flattens collections onto one line
	if cln, _ := formatter.FormatWithOptions([]byte("let z = [\n1,\n2,\n3\n];"), formatter.Options{Clean: true}); strings.TrimRight(string(cln), "\n") != "let z = [1, 2, 3];" {
		t.Errorf("clean did not flatten list: %q", cln)
	}
}
