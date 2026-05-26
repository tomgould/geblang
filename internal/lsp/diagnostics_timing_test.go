package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"geblang/internal/check"
)

// notifyCapture is a sync-wrapped io.Writer that captures the bytes
// the LSP server writes during a test. Tests inspect the captured
// payload to confirm the version field and debounce behaviour.
type notifyCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *notifyCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *notifyCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// publishedDiagnostics returns one entry per textDocument/publishDiagnostics
// notification the server emitted, parsed back into a map.
func (c *notifyCapture) publishedDiagnostics(t *testing.T) []map[string]any {
	t.Helper()
	raw := c.String()
	var out []map[string]any
	// Each LSP message is `Content-Length: N\r\n\r\n{json}`.  Strip the
	// frame headers and parse each JSON body.
	for {
		idx := strings.Index(raw, "\r\n\r\n")
		if idx < 0 {
			break
		}
		header := raw[:idx]
		raw = raw[idx+4:]
		var n int
		for _, line := range strings.Split(header, "\r\n") {
			if strings.HasPrefix(line, "Content-Length: ") {
				_, err := fmtAtoi(strings.TrimPrefix(line, "Content-Length: "), &n)
				if err != nil {
					t.Fatalf("invalid Content-Length: %v", err)
				}
			}
		}
		if n <= 0 || n > len(raw) {
			break
		}
		body := raw[:n]
		raw = raw[n:]
		var msg map[string]any
		if err := json.Unmarshal([]byte(body), &msg); err != nil {
			t.Fatalf("parse notification: %v: %q", err, body)
		}
		if method, _ := msg["method"].(string); method != "textDocument/publishDiagnostics" {
			continue
		}
		params, _ := msg["params"].(map[string]any)
		out = append(out, params)
	}
	return out
}

// fmtAtoi parses a positive integer; tiny helper to avoid pulling in
// strconv at the test-helper level (the production code already uses
// strconv, but importing it here would shadow no production-only
// import we care about - this is purely stylistic).
func fmtAtoi(s string, out *int) (int, error) {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0, &numErr{s}
		}
		n = n*10 + int(r-'0')
	}
	*out = n
	return n, nil
}

type numErr struct{ s string }

func (e *numErr) Error() string { return "invalid number: " + e.s }

// newTestServer builds a server with a notification capture writer
// attached. The caller doesn't drive the `run()` loop - tests call
// `handle()` directly with constructed messages.
func newTestServer() (*server, *notifyCapture) {
	cap := &notifyCapture{}
	s := &server{
		w:           cap,
		docs:        map[string]string{},
		diagTimers:  map[string]*time.Timer{},
		moduleCache: check.NewModuleCache(),
		workspace:   newWorkspaceIndex(),
	}
	return s, cap
}

func didChangeMessage(t *testing.T, uri string, version int, text string) *rawMessage {
	t.Helper()
	params := DidChangeParams{
		TextDocument:   VersionedTextDocument{URI: uri, Version: version},
		ContentChanges: []TextDocumentContentChangeEvent{{Text: text}},
	}
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &rawMessage{Method: "textDocument/didChange", Params: body}
}

// TestPublishDiagnosticsCarriesVersion verifies the server stamps the
// document version onto every publishDiagnostics payload so VS Code
// can discard stale results.
func TestPublishDiagnosticsCarriesVersion(t *testing.T) {
	s, cap := newTestServer()
	s.handle(didChangeMessage(t, "file:///main.gb", 7, "let x = 1;\n"))

	// Wait for the debounce timer to fire.
	time.Sleep(diagnosticDebounce + 100*time.Millisecond)

	notifications := cap.publishedDiagnostics(t)
	if len(notifications) != 1 {
		t.Fatalf("expected 1 publishDiagnostics notification, got %d", len(notifications))
	}
	v, ok := notifications[0]["version"]
	if !ok {
		t.Fatalf("publishDiagnostics payload missing version field: %#v", notifications[0])
	}
	if got, want := v.(float64), float64(7); got != want {
		t.Fatalf("version: got %v, want %v", got, want)
	}
}

// TestDidChangeDebounceCoalesces verifies that a flurry of didChange
// notifications for the same URI produces exactly one
// publishDiagnostics notification once the debounce window has
// expired - and that the notification's version matches the latest
// change (not the first or some intermediate value).
func TestDidChangeDebounceCoalesces(t *testing.T) {
	s, cap := newTestServer()

	// Three rapid changes for the same URI, well inside the debounce
	// window.
	s.handle(didChangeMessage(t, "file:///main.gb", 1, "let x = 1\n"))
	s.handle(didChangeMessage(t, "file:///main.gb", 2, "let x = 1;\n"))
	s.handle(didChangeMessage(t, "file:///main.gb", 3, "let x = 12;\n"))

	// During the debounce window, no notification should have fired yet.
	time.Sleep(diagnosticDebounce / 2)
	if early := cap.publishedDiagnostics(t); len(early) != 0 {
		t.Fatalf("expected no notifications during debounce window, got %d", len(early))
	}

	// After the window, exactly one notification - and it carries the
	// version of the most recent change.
	time.Sleep(diagnosticDebounce + 100*time.Millisecond)
	notifications := cap.publishedDiagnostics(t)
	if len(notifications) != 1 {
		t.Fatalf("expected 1 coalesced publishDiagnostics notification, got %d", len(notifications))
	}
	if got, want := notifications[0]["version"].(float64), float64(3); got != want {
		t.Fatalf("version: got %v (intermediate), want %v (latest)", got, want)
	}
}

// TestPublishDiagnosticsEmptySendsArrayNotNull is the regression
// test for the stale-squiggle-until-next-edit symptom. When a file
// becomes clean (e.g. the user fixes the last error), the server
// must publish `"diagnostics": []`, not `"diagnostics": null`. The
// JSON null form (Go's zero-value encoding for a nil slice) is what
// VS Code reads as "no update" - keeping the old squiggle on screen
// until the next edit happens to produce non-empty diagnostics.
func TestPublishDiagnosticsEmptySendsArrayNotNull(t *testing.T) {
	s, cap := newTestServer()
	// A trivially valid file - analyze() will return nothing.
	s.handle(didChangeMessage(t, "file:///main.gb", 1, "import io;\nio.println(1);\n"))
	time.Sleep(diagnosticDebounce + 100*time.Millisecond)

	// The raw bytes must contain the JSON token `[]`, not `null`.
	if got := cap.String(); !strings.Contains(got, `"diagnostics":[]`) {
		t.Fatalf("expected diagnostics to be an empty JSON array, got: %q", got)
	}
	if strings.Contains(cap.String(), `"diagnostics":null`) {
		t.Fatalf("diagnostics was JSON null, which VS Code reads as no-update - stale squiggles persist")
	}
}

// TestDidCloseCancelsPendingDiagnostics verifies that closing a file
// during its debounce window prevents the stale diagnostic from
// firing - the only publishDiagnostics emitted should be the
// clear-on-close empty one.
func TestDidCloseCancelsPendingDiagnostics(t *testing.T) {
	s, cap := newTestServer()
	s.handle(didChangeMessage(t, "file:///main.gb", 1, "let x = oops\n"))

	// Close before the debounce fires.
	closeParams := DidCloseParams{TextDocument: TextDocumentIdentifier{URI: "file:///main.gb"}}
	closeBody, _ := json.Marshal(closeParams)
	s.handle(&rawMessage{Method: "textDocument/didClose", Params: closeBody})

	// Wait past the debounce window to confirm no late notification arrives.
	time.Sleep(diagnosticDebounce + 100*time.Millisecond)

	notifications := cap.publishedDiagnostics(t)
	// One notification only: the empty list sent by didClose.
	if len(notifications) != 1 {
		t.Fatalf("expected 1 publishDiagnostics from close, got %d", len(notifications))
	}
	diags, _ := notifications[0]["diagnostics"].([]any)
	if len(diags) != 0 {
		t.Fatalf("expected empty diagnostics from close, got %#v", diags)
	}
}
