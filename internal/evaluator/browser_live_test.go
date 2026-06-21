package evaluator_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"geblang/internal/cdp"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/native"
	"geblang/internal/parser"
)

// Live: drive a real headless Chrome through the Geblang browser surface (launch -> page -> goto -> evaluate -> close). Skips without Chrome.
func TestBrowserLaunchNavigateLive(t *testing.T) {
	if cdp.FindChrome() == "" {
		t.Skip("no Chrome available (set GEBLANG_CHROME)")
	}
	native.SetBrowserEnabled(true)
	defer native.SetBrowserEnabled(false)

	prog := `import browser;
import io;
let b = browser.launch({"args": ["--no-sandbox"]});
let p = b.newPage();
p.goto("data:text/html,<title>tt</title><h1>live</h1>");
io.println("title=" + (p.title() as string));
io.println("h1=" + (p.evaluate("document.querySelector('h1').textContent") as string));
p.close();
b.close();
`
	pr := parser.New(lexer.New(prog))
	program := pr.ParseProgram()
	if len(pr.Errors()) != 0 {
		t.Fatalf("parse: %v", pr.Errors())
	}
	var out bytes.Buffer
	if _, err := evaluator.New(&out).Eval(program); err != nil {
		t.Fatalf("eval: %v\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "title=tt") || !strings.Contains(got, "h1=live") {
		t.Fatalf("unexpected output: %q", got)
	}
}

// Live: exercise the P2 interaction surface (waitFor / type / click / text / attribute) end to end.
func TestBrowserInteractionsLive(t *testing.T) {
	if cdp.FindChrome() == "" {
		t.Skip("no Chrome available (set GEBLANG_CHROME)")
	}
	native.SetBrowserEnabled(true)
	defer native.SetBrowserEnabled(false)

	prog := `import browser;
import io;
let b = browser.launch({"args": ["--no-sandbox"]});
let p = b.newPage();
p.goto("about:blank");
p.evaluate("""
document.body.innerHTML = "<input id='n'><button id='go'>go</button><div id='out'>empty</div>";
document.getElementById('go').addEventListener('click', function(){ document.getElementById('out').textContent = 'hi ' + document.getElementById('n').value; });
null
""");
p.waitFor("#n");
p.type("#n", "ada");
p.click("#go");
io.println("out=" + (p.text("#out") as string));
io.println("id=" + (p.attribute("#n", "id") as string));
b.close();
`
	pr := parser.New(lexer.New(prog))
	program := pr.ParseProgram()
	if len(pr.Errors()) != 0 {
		t.Fatalf("parse: %v", pr.Errors())
	}
	var out bytes.Buffer
	if _, err := evaluator.New(&out).Eval(program); err != nil {
		t.Fatalf("eval: %v\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "out=hi ada") || !strings.Contains(got, "id=n") {
		t.Fatalf("unexpected output: %q", got)
	}
}

// Live: cookies (read a Set-Cookie from a fixture) + multi-tab (pages lists open targets).
func TestBrowserCookiesAndTabsLive(t *testing.T) {
	if cdp.FindChrome() == "" {
		t.Skip("no Chrome available (set GEBLANG_CHROME)")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})
		_, _ = w.Write([]byte("<h1>ok</h1>"))
	}))
	defer srv.Close()

	native.SetBrowserEnabled(true)
	defer native.SetBrowserEnabled(false)

	prog := `import browser;
import io;
let b = browser.launch({"args": ["--no-sandbox"]});
let p = b.newPage();
p.goto("` + srv.URL + `/");
let cs = p.cookies();
if (cs.length() > 0) {
    io.println("cookie=" + ((cs[0] as dict<string, any>)["name"] as string) + "=" + ((cs[0] as dict<string, any>)["value"] as string));
}
b.newPage();
io.println("tabs>=2:" + ((b.pages().length() >= 2) as string));
b.close();
`
	pr := parser.New(lexer.New(prog))
	program := pr.ParseProgram()
	if len(pr.Errors()) != 0 {
		t.Fatalf("parse: %v", pr.Errors())
	}
	var out bytes.Buffer
	if _, err := evaluator.New(&out).Eval(program); err != nil {
		t.Fatalf("eval: %v\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "cookie=sid=abc") || !strings.Contains(got, "tabs>=2:true") {
		t.Fatalf("unexpected output: %q", got)
	}
}

// Live: request interception - a route handler fulfills the navigation with a mock response.
func TestBrowserInterceptionLive(t *testing.T) {
	if cdp.FindChrome() == "" {
		t.Skip("no Chrome available (set GEBLANG_CHROME)")
	}
	native.SetBrowserEnabled(true)
	defer native.SetBrowserEnabled(false)

	prog := `import browser;
import io;
let b = browser.launch({"args": ["--no-sandbox"]});
let p = b.newPage();
p.route("*", func(dict<string, any> req): dict<string, any> {
    return {"status": 200, "headers": {"Content-Type": "text/html"}, "body": "<h1>mocked " + (req["method"] as string) + "</h1>"};
});
p.goto("http://example.invalid/anything");
io.println("mocked:" + (p.content().contains("mocked GET") as string));
b.close();
`
	pr := parser.New(lexer.New(prog))
	program := pr.ParseProgram()
	if len(pr.Errors()) != 0 {
		t.Fatalf("parse: %v", pr.Errors())
	}
	var out bytes.Buffer
	if _, err := evaluator.New(&out).Eval(program); err != nil {
		t.Fatalf("eval: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "mocked:true") {
		t.Fatalf("interception did not fulfill: %q", out.String())
	}
}
