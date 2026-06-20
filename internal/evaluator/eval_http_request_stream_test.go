package evaluator_test

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"testing"

	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// A truncated response (claims more than it sends, then drops) must surface from read() as a catchable IOError, not a clean end.
func TestRequestStreamSurfacesMidStreamError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		br := bufio.NewReader(conn)
		for {
			line, err := br.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
		}
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\nline-one\npartial"))
		conn.Close()
	}()

	prog := `import http;
import io;
let stream = http.requestStream({"method": "GET", "url": "http://` + ln.Addr().String() + `/"});
try {
    let line = stream.read();
    while (line != null) {
        line = stream.read();
    }
    io.println("clean");
} catch (IOError e) {
    io.println("threw");
}`
	p := parser.New(lexer.New(prog))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse: %v", p.Errors())
	}
	var out bytes.Buffer
	if _, err := evaluator.New(&out).Eval(program); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !strings.Contains(out.String(), "threw") {
		t.Fatalf("expected mid-stream read error to surface as IOError, got %q", out.String())
	}
}
