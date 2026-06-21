// Package cdp is a minimal hand-rolled Chrome DevTools Protocol client over gorilla/websocket: launch a headless Chrome, attach to page targets, and round-trip JSON commands. No external CDP library.
package cdp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const defaultCommandTimeout = 30 * time.Second

// Browser is a connection to a launched headless Chrome.
type Browser struct {
	cmd  *exec.Cmd
	conn *websocket.Conn
	udd  string

	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcResult
	closed  bool

	writeMu sync.Mutex

	eventMu  sync.Mutex
	handlers map[string]func(sessionID string, params json.RawMessage)
}

// Page is an attached page target; commands carry its session id.
type Page struct {
	browser   *Browser
	TargetID  string
	SessionID string
}

type rpcResult struct {
	result json.RawMessage
	err    *RPCError
}

// RPCError is a CDP-reported command failure.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("%s: %s", e.Message, e.Data)
	}
	return e.Message
}

// LaunchOptions configures Launch; an empty Executable triggers discovery.
type LaunchOptions struct {
	Executable string
	Headless   bool
	Args       []string
	Timeout    time.Duration
}

var wsURLRe = regexp.MustCompile(`ws://\S+`)

// Launch starts a headless Chrome and connects to its DevTools endpoint.
func Launch(opts LaunchOptions) (*Browser, error) {
	exe := opts.Executable
	if exe == "" {
		exe = FindChrome()
	}
	if exe == "" {
		return nil, fmt.Errorf("no Chrome/Chromium found; set the executable option or $GEBLANG_CHROME")
	}
	udd, err := os.MkdirTemp("", "geblang-chrome-")
	if err != nil {
		return nil, err
	}
	args := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=" + udd,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-gpu",
		"--disable-background-networking",
	}
	if opts.Headless {
		args = append(args, "--headless=new")
	}
	args = append(args, opts.Args...)
	args = append(args, "about:blank")

	cmd := exec.Command(exe, args...)
	setProcessGroup(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		os.RemoveAll(udd)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(udd)
		return nil, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	wsURL, err := readWSURL(stderr, timeout)
	if err != nil {
		killProcessGroup(cmd)
		_ = cmd.Wait()
		os.RemoveAll(udd)
		return nil, fmt.Errorf("waiting for Chrome devtools endpoint: %w", err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		killProcessGroup(cmd)
		_ = cmd.Wait()
		os.RemoveAll(udd)
		return nil, fmt.Errorf("connect to devtools: %w", err)
	}
	b := &Browser{cmd: cmd, conn: conn, udd: udd, pending: map[int]chan rpcResult{}, handlers: map[string]func(string, json.RawMessage){}}
	go b.readLoop()
	return b, nil
}

// readWSURL scans Chrome's stderr for the devtools URL, then keeps draining it so the pipe never blocks Chrome.
func readWSURL(stderr io.Reader, timeout time.Duration) (string, error) {
	found := make(chan string, 1)
	failed := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		sent := false
		for sc.Scan() {
			if !sent && strings.Contains(sc.Text(), "DevTools listening on") {
				if m := wsURLRe.FindString(sc.Text()); m != "" {
					found <- m
					sent = true
				}
			}
		}
		if !sent {
			failed <- fmt.Errorf("Chrome exited before announcing a devtools endpoint")
		}
	}()
	select {
	case u := <-found:
		return u, nil
	case err := <-failed:
		return "", err
	case <-time.After(timeout):
		return "", fmt.Errorf("timed out after %v", timeout)
	}
}

func (b *Browser) readLoop() {
	for {
		_, data, err := b.conn.ReadMessage()
		if err != nil {
			b.failAll(err)
			return
		}
		var msg struct {
			ID        int             `json:"id"`
			Result    json.RawMessage `json:"result"`
			Error     *RPCError       `json:"error"`
			Method    string          `json:"method"`
			Params    json.RawMessage `json:"params"`
			SessionID string          `json:"sessionId"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.ID != 0 {
			b.mu.Lock()
			ch := b.pending[msg.ID]
			delete(b.pending, msg.ID)
			b.mu.Unlock()
			if ch != nil {
				ch <- rpcResult{result: msg.Result, err: msg.Error}
			}
			continue
		}
		if msg.Method != "" {
			b.eventMu.Lock()
			h := b.handlers[msg.Method]
			b.eventMu.Unlock()
			if h != nil {
				h(msg.SessionID, msg.Params)
			}
		}
	}
}

func (b *Browser) failAll(err error) {
	b.mu.Lock()
	b.closed = true
	pending := b.pending
	b.pending = map[int]chan rpcResult{}
	b.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcResult{err: &RPCError{Message: "browser connection closed: " + err.Error()}}
	}
}

// Send issues a CDP command (sessionID empty = browser-level) and returns its result.
func (b *Browser) Send(sessionID, method string, params any) (json.RawMessage, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, fmt.Errorf("browser is closed")
	}
	b.nextID++
	id := b.nextID
	ch := make(chan rpcResult, 1)
	b.pending[id] = ch
	b.mu.Unlock()

	envelope := map[string]any{"id": id, "method": method}
	if params != nil {
		envelope["params"] = params
	}
	if sessionID != "" {
		envelope["sessionId"] = sessionID
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	b.writeMu.Lock()
	err = b.conn.WriteMessage(websocket.TextMessage, data)
	b.writeMu.Unlock()
	if err != nil {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return r.result, nil
	case <-time.After(defaultCommandTimeout):
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, fmt.Errorf("cdp %s timed out", method)
	}
}

// OnEvent registers a handler for a CDP event method (used for interception/page lifecycle).
func (b *Browser) OnEvent(method string, h func(sessionID string, params json.RawMessage)) {
	b.eventMu.Lock()
	b.handlers[method] = h
	b.eventMu.Unlock()
}

// NewPage creates and attaches to a fresh page target.
func (b *Browser) NewPage() (*Page, error) {
	res, err := b.Send("", "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return nil, err
	}
	var ct struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &ct); err != nil {
		return nil, err
	}
	return b.AttachPage(ct.TargetID)
}

// AttachPage attaches to an existing page target and enables its domains.
func (b *Browser) AttachPage(targetID string) (*Page, error) {
	res, err := b.Send("", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true})
	if err != nil {
		return nil, err
	}
	var at struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(res, &at); err != nil {
		return nil, err
	}
	p := &Page{browser: b, TargetID: targetID, SessionID: at.SessionID}
	for _, d := range []string{"Page.enable", "Runtime.enable", "DOM.enable", "Network.enable"} {
		_, _ = p.Send(d, nil)
	}
	return p, nil
}

// PageTargets returns the targetIds of all open page targets.
func (b *Browser) PageTargets() ([]string, error) {
	res, err := b.Send("", "Target.getTargets", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	var ids []string
	for _, t := range r.TargetInfos {
		if t.Type == "page" {
			ids = append(ids, t.TargetID)
		}
	}
	return ids, nil
}

// Version returns the "Browser" product string from Browser.getVersion.
func (b *Browser) Version() (string, error) {
	res, err := b.Send("", "Browser.getVersion", nil)
	if err != nil {
		return "", err
	}
	var v struct {
		Product string `json:"product"`
	}
	_ = json.Unmarshal(res, &v)
	return v.Product, nil
}

// Close shuts down the page session and removes its target.
func (p *Page) Close() error {
	_, err := p.browser.Send("", "Target.closeTarget", map[string]any{"targetId": p.TargetID})
	return err
}

// Send issues a CDP command on this page's session.
func (p *Page) Send(method string, params any) (json.RawMessage, error) {
	return p.browser.Send(p.SessionID, method, params)
}

// Browser returns the owning browser (for event registration on its connection).
func (p *Page) Browser() *Browser { return p.browser }

// Close terminates Chrome and cleans up its profile dir.
func (b *Browser) Close() error {
	b.mu.Lock()
	already := b.closed
	b.closed = true
	b.mu.Unlock()
	if !already {
		_, _ = b.Send("", "Browser.close", nil)
	}
	_ = b.conn.Close()
	killProcessGroup(b.cmd)
	_ = b.cmd.Wait()
	os.RemoveAll(b.udd)
	return nil
}

// FindChrome locates a browser binary: $GEBLANG_CHROME, then PATH names, then common install paths.
func FindChrome() string {
	if p := os.Getenv("GEBLANG_CHROME"); p != "" {
		return p
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, p := range []string{
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/opt/google/chrome/chrome",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}
