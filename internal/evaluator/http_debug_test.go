package evaluator

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gruntime "geblang/internal/runtime"
)

// throwingHandler is a native handler whose throw carries structured frames,
// mirroring what a Geblang handler produces on both backends.
func throwingHandler() gruntime.Function {
	return gruntime.Function{Native: func(this *gruntime.Instance, args []gruntime.Value) (gruntime.Value, error) {
		return nil, thrownError{value: gruntime.Error{
			Class:   "ValueError",
			Message: "handler boom",
			TraceFrames: []gruntime.StackFrame{
				{Name: "explode", CallLine: 0},
			},
			ErrorLine:    5,
			TopLevelLine: 9,
		}}
	}}
}

func serveOnce(t *testing.T, debug bool) (int, string, string) {
	t.Helper()
	e := New(io.Discard)
	var errBuf bytes.Buffer
	e.stderr = &errBuf
	server := httptest.NewServer(e.httpHandler(throwingHandler(), nil, nil, 0, debug))
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body), errBuf.String()
}

func TestServeHandlerErrorProductionMode(t *testing.T) {
	status, body, logged := serveOnce(t, false)
	if status != http.StatusInternalServerError {
		t.Fatalf("status %d", status)
	}
	if body != "Internal Server Error\n" {
		t.Fatalf("production body must be generic, got %q", body)
	}
	if logged != "http.serve: handler error: ValueError: handler boom\n" {
		t.Fatalf("production log must be one line, got %q", logged)
	}
}

func TestServeHandlerErrorDebugOptsMode(t *testing.T) {
	status, body, logged := serveOnce(t, true)
	if status != http.StatusInternalServerError {
		t.Fatalf("status %d", status)
	}
	want := "uncaught ValueError: handler boom\n  at explode (line 5)\n  at <top level> (line 9)"
	if !strings.Contains(body, want) {
		t.Fatalf("debug body missing trace:\n%s", body)
	}
	if !strings.Contains(logged, want) {
		t.Fatalf("debug log missing trace:\n%s", logged)
	}
}

func TestServeDebugEnabledEnvSwitch(t *testing.T) {
	t.Setenv("GEBLANG_DEBUG", "1")
	if !serveDebugEnabled(false) {
		t.Fatal("GEBLANG_DEBUG=1 must enable debug")
	}
	t.Setenv("GEBLANG_DEBUG", "0")
	if serveDebugEnabled(false) {
		t.Fatal("GEBLANG_DEBUG=0 must not enable debug")
	}
	t.Setenv("GEBLANG_DEBUG", "")
	if serveDebugEnabled(false) {
		t.Fatal("empty GEBLANG_DEBUG must not enable debug")
	}
	if !serveDebugEnabled(true) {
		t.Fatal("opts debug must enable regardless of env")
	}
}

func TestParseDebugFlag(t *testing.T) {
	if got, err := parseDebugFlag(gruntime.Null{}, "http.serve"); err != nil || got {
		t.Fatalf("non-dict opts: got %v err %v", got, err)
	}
	entries := map[string]gruntime.DictEntry{}
	putDict(entries, "debug", gruntime.Bool{Value: true})
	if got, err := parseDebugFlag(gruntime.Dict{Entries: entries}, "http.serve"); err != nil || !got {
		t.Fatalf("debug true: got %v err %v", got, err)
	}
	bad := map[string]gruntime.DictEntry{}
	putDict(bad, "debug", gruntime.String{Value: "yes"})
	if _, err := parseDebugFlag(gruntime.Dict{Entries: bad}, "http.serve"); err == nil {
		t.Fatal("non-bool debug must error")
	}
}
