package evaluator

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"geblang/internal/lexer"
	"geblang/internal/parser"
)

func runLeakProg(t *testing.T, src string) *Evaluator {
	t.Helper()
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse: %v", p.Errors())
	}
	var out bytes.Buffer
	e := New(&out)
	if _, err := e.Eval(prog); err != nil {
		t.Fatalf("eval: %v\n%s", err, out.String())
	}
	return e
}

// A response stream read to EOF must drop its handle, not retain it for the program's life.
func TestResponseStreamHandleReleasedOnEOF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("line1\nline2\nline3\n"))
	}))
	defer srv.Close()

	prog := `import http;
let s = http.requestStream({"method": "GET", "url": "` + srv.URL + `"});
let line = s.read();
while (line != null) { line = s.read(); }
`
	e := runLeakProg(t, prog)
	e.httpResponseStreamMu.Lock()
	n := len(e.httpResponseStreams)
	e.httpResponseStreamMu.Unlock()
	if n != 0 {
		t.Fatalf("response stream handle leaked: %d entries remain", n)
	}
}

// An explicit close() also drops the handle.
func TestResponseStreamHandleReleasedOnClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("a\nb\n"))
	}))
	defer srv.Close()

	prog := `import http;
let s = http.requestStream({"method": "GET", "url": "` + srv.URL + `"});
s.read();
s.close();
`
	e := runLeakProg(t, prog)
	e.httpResponseStreamMu.Lock()
	n := len(e.httpResponseStreams)
	e.httpResponseStreamMu.Unlock()
	if n != 0 {
		t.Fatalf("response stream handle leaked after close: %d entries remain", n)
	}
}

// A fetch stream fully consumed via next() must drop its handle.
func TestFetchStreamHandleReleasedAfterConsumption(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	prog := `import http;
let fs = http.fetchStream(["` + srv.URL + `", "` + srv.URL + `"]);
let r = fs.next();
while (r != null) { r = fs.next(); }
`
	e := runLeakProg(t, prog)
	e.httpFetchStreamMu.Lock()
	n := len(e.httpFetchStreams)
	e.httpFetchStreamMu.Unlock()
	if n != 0 {
		t.Fatalf("fetch stream handle leaked: %d entries remain", n)
	}
}
