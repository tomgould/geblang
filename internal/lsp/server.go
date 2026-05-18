// Package lsp implements a Language Server Protocol server for Geblang.
// Phase 1: diagnostics only (parse + semantic errors pushed on open/change).
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"geblang/internal/formatter"
	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/semantic"
)

// diagnosticDebounce is how long the server waits after the last document
// change before running diagnostics. Fast typists send many didChange
// notifications per second; debouncing avoids spending CPU on each
// keystroke and (more importantly) avoids painting squiggles that are
// only correct for a buffer state the user has already moved past.
const diagnosticDebounce = 200 * time.Millisecond

// ServeTCP listens on the given TCP port on all interfaces, writes "IP:PORT\n"
// to portOut, accepts one connection, then serves LSP over it. Pass port 0 to
// bind to a random available port. Listening on all interfaces and advertising
// the non-loopback IP lets Windows-side callers reach the server when it runs
// inside WSL2 (WSL2 loopback != Windows loopback).
func ServeTCP(portOut io.Writer, port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	fmt.Fprintf(portOut, "%s:%d\n", tcpAdvertiseIP(), port)
	conn, err := ln.Accept()
	ln.Close()
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}
	defer conn.Close()
	return Serve(conn, conn)
}

// tcpAdvertiseIP returns the first non-loopback IPv4 address on an up
// interface, which is the address reachable from a Windows host when the
// process is running inside WSL2. Falls back to 127.0.0.1.
func tcpAdvertiseIP() string {
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}

// Serve runs the LSP protocol loop on r/w until the session ends.
func Serve(r io.Reader, w io.Writer) error {
	s := &server{
		r:          bufio.NewReader(r),
		w:          w,
		seq:        0,
		docs:       map[string]string{},
		diagTimers: map[string]*time.Timer{},
	}
	return s.run()
}

type server struct {
	r          *bufio.Reader
	w          io.Writer
	mu         sync.Mutex
	seq        int
	docs       map[string]string      // uri → source text
	diagTimers map[string]*time.Timer // uri → pending debounced diagnostic timer
}

// ---- protocol framing ----

type rawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   any             `json:"error,omitempty"`
}

func (s *server) readMessage() (*rawMessage, error) {
	contentLength := -1
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			v := strings.TrimPrefix(line, "Content-Length: ")
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %w", err)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(s.r, body); err != nil {
		return nil, err
	}
	var msg rawMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (s *server) write(obj any) {
	data, _ := json.Marshal(obj)
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, "Content-Length: %d\r\n\r\n", len(data))
	s.w.Write(data)
}

func (s *server) respond(id json.RawMessage, result any) {
	s.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"result":  result,
	})
}

func (s *server) notify(method string, params any) {
	s.write(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

// ---- main loop ----

func (s *server) run() error {
	for {
		msg, err := s.readMessage()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		s.handle(msg)
	}
}

func (s *server) handle(msg *rawMessage) {
	switch msg.Method {
	case "initialize":
		s.respond(msg.ID, map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync": 1, // full sync
				"completionProvider": map[string]any{
					"triggerCharacters": []string{"."},
					"resolveProvider":   false,
				},
				"signatureHelpProvider": map[string]any{
					"triggerCharacters":   []string{"(", ","},
					"retriggerCharacters": []string{","},
				},
				"hoverProvider":              true,
				"documentSymbolProvider":     true,
				"definitionProvider":         true,
				"documentFormattingProvider": true,
			},
			"serverInfo": map[string]any{
				"name":    "geblang-lsp",
				"version": "1.0.0",
			},
		})

	case "initialized":
		// Client notification; no response needed.

	case "shutdown":
		s.respond(msg.ID, nil)

	case "exit":
		return

	case "textDocument/didOpen":
		var params DidOpenParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
		s.mu.Lock()
		s.docs[params.TextDocument.URI] = params.TextDocument.Text
		s.mu.Unlock()
		s.scheduleDiagnostics(params.TextDocument.URI, params.TextDocument.Text, params.TextDocument.Version)

	case "textDocument/didChange":
		var params DidChangeParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
		if len(params.ContentChanges) == 0 {
			return
		}
		text := params.ContentChanges[len(params.ContentChanges)-1].Text
		s.mu.Lock()
		s.docs[params.TextDocument.URI] = text
		s.mu.Unlock()
		s.scheduleDiagnostics(params.TextDocument.URI, text, params.TextDocument.Version)

	case "textDocument/didClose":
		var params DidCloseParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
		// Cancel any pending debounced diagnostic for this URI so it
		// doesn't fire after the file has been closed.
		s.mu.Lock()
		if t := s.diagTimers[params.TextDocument.URI]; t != nil {
			t.Stop()
			delete(s.diagTimers, params.TextDocument.URI)
		}
		s.mu.Unlock()
		// Clear diagnostics for the closed file *before* releasing the
		// stored buffer, so callers see the squiggles disappear at the
		// same moment we forget about the document.
		s.notify("textDocument/publishDiagnostics", map[string]any{
			"uri":         params.TextDocument.URI,
			"diagnostics": []any{},
		})
		s.mu.Lock()
		delete(s.docs, params.TextDocument.URI)
		s.mu.Unlock()

	case "textDocument/completion":
		var params CompletionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			s.respond(msg.ID, []CompletionItem{})
			return
		}
		s.respond(msg.ID, s.completions(params))

	case "textDocument/signatureHelp":
		var params SignatureHelpParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			s.respond(msg.ID, SignatureHelp{})
			return
		}
		s.respond(msg.ID, s.signatureHelp(params))

	case "textDocument/hover":
		var params TextDocumentPositionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			s.respond(msg.ID, nil)
			return
		}
		s.respond(msg.ID, s.hover(params))

	case "textDocument/documentSymbol":
		var params struct {
			TextDocument TextDocumentIdentifier `json:"textDocument"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			s.respond(msg.ID, []DocumentSymbol{})
			return
		}
		source, ok := s.document(params.TextDocument.URI)
		if !ok {
			s.respond(msg.ID, []DocumentSymbol{})
			return
		}
		s.respond(msg.ID, documentSymbols(source))

	case "textDocument/definition":
		var params TextDocumentPositionParams
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			s.respond(msg.ID, nil)
			return
		}
		s.respond(msg.ID, s.definition(params))

	case "textDocument/formatting":
		var params struct {
			TextDocument TextDocumentIdentifier `json:"textDocument"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			s.respond(msg.ID, nil)
			return
		}
		s.respond(msg.ID, s.formatting(params.TextDocument.URI))
	}
}

// ---- hover ----

type HoverResult struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type MarkupContent struct {
	Kind  string `json:"kind"` // "markdown" or "plaintext"
	Value string `json:"value"`
}

func (s *server) hover(params TextDocumentPositionParams) any {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return nil
	}
	content := hoverContent(source, params.Position.Line, params.Position.Character)
	if content == "" {
		return nil
	}
	return HoverResult{Contents: MarkupContent{Kind: "markdown", Value: content}}
}

// ---- definition ----

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

func (s *server) definition(params TextDocumentPositionParams) any {
	source, ok := s.document(params.TextDocument.URI)
	if !ok {
		return nil
	}
	word := wordAtPosition(source, params.Position.Line, params.Position.Character)
	if word == "" {
		return nil
	}
	defLine := findDefinition(source, word)
	if defLine < 0 {
		return nil
	}
	lines := strings.Split(source, "\n")
	col := 0
	if defLine < len(lines) {
		col = strings.Index(lines[defLine], word)
		if col < 0 {
			col = 0
		}
	}
	r := Range{
		Start: Position{Line: defLine, Character: col},
		End:   Position{Line: defLine, Character: col + len(word)},
	}
	return Location{URI: params.TextDocument.URI, Range: r}
}

// ---- formatting ----

// TextEdit is an LSP text edit.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

func (s *server) formatting(uri string) any {
	source, ok := s.document(uri)
	if !ok {
		return nil
	}
	formatted, err := formatter.Format([]byte(source))
	if err != nil || string(formatted) == source {
		return []TextEdit{}
	}
	lines := strings.Split(source, "\n")
	lastLine := len(lines) - 1
	lastChar := len(lines[lastLine])
	wholeDoc := Range{
		Start: Position{Line: 0, Character: 0},
		End:   Position{Line: lastLine, Character: lastChar},
	}
	return []TextEdit{{Range: wholeDoc, NewText: string(formatted)}}
}

// ---- diagnostics ----

// scheduleDiagnostics queues a debounced call to publishDiagnostics for
// the given URI. If a previous call is still pending, it is replaced -
// only the latest content (and matching version) will be analyzed once
// the typing pause is long enough. This both saves CPU on flurries and
// prevents stale squiggles arriving from analyses that started against
// content the editor has already moved past.
func (s *server) scheduleDiagnostics(uri, source string, version int) {
	s.mu.Lock()
	if t := s.diagTimers[uri]; t != nil {
		t.Stop()
	}
	s.diagTimers[uri] = time.AfterFunc(diagnosticDebounce, func() {
		s.mu.Lock()
		delete(s.diagTimers, uri)
		s.mu.Unlock()
		s.publishDiagnostics(uri, source, version)
	})
	s.mu.Unlock()
}

func (s *server) publishDiagnostics(uri, source string, version int) {
	diags := s.analyze(source)
	// LSP §3.17 PublishDiagnosticsParams.diagnostics is an array - it
	// must never be JSON `null`. analyze() can return a nil slice when
	// nothing was found, and encoding/json marshals nil slices to
	// `null`, which VS Code interprets as "no update" rather than
	// "clear the existing squiggles". That kept stale diagnostics
	// visible until the next edit produced fresh ones (and they were
	// often for the wrong buffer state). Always send an empty array.
	if diags == nil {
		diags = []Diagnostic{}
	}
	payload := map[string]any{
		"uri":         uri,
		"diagnostics": diags,
	}
	// Per LSP §3.17, the version on PublishDiagnostics lets the client
	// discard results that no longer match its buffer state. Always
	// include it so VS Code can drop stale notifications produced by an
	// analysis that started against earlier content.
	if version > 0 {
		payload["version"] = version
	}
	s.notify("textDocument/publishDiagnostics", payload)
}

// Diagnostic is an LSP diagnostic object.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"` // 1=error, 2=warning
	Source   string `json:"source"`
	Message  string `json:"message"`
}

// Range is an LSP range (0-based lines and characters).
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Position is an LSP position.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

func (s *server) analyze(source string) []Diagnostic {
	var diags []Diagnostic

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()

	for _, msg := range p.Errors() {
		// Parser errors are formatted as "line:col: message"
		line, col, text := parseErrorMsg(msg)
		diags = append(diags, Diagnostic{
			Range:    lineColRange(line, col),
			Severity: 1,
			Source:   "geblang",
			Message:  text,
		})
	}

	// Run the semantic analyzer regardless of parse errors. Even with
	// partial parses, the analyzer often finds additional issues the
	// user benefits from seeing in the same pass (a typo plus a wrong
	// type, say). Position is taken from the diagnostic itself; the
	// lineColRange helper falls back to (1, 1) for zero values.
	for _, sd := range semantic.New().Analyze(program) {
		// Map semantic.Severity to LSP severity codes (1=error,
		// 2=warning). Errors are the default; warnings still surface
		// in the VS Code Problems panel but don't block geblang run.
		severity := 1
		if sd.Severity == semantic.SeverityWarning {
			severity = 2
		}
		diags = append(diags, Diagnostic{
			Range:    lineColRange(sd.Line, sd.Column),
			Severity: severity,
			Source:   "geblang",
			Message:  sd.Message,
		})
	}
	return diags
}

// parseErrorMsg splits a parser error string "line:col: message" into parts.
func parseErrorMsg(msg string) (line, col int, text string) {
	// Format: "line:col: message"
	parts := strings.SplitN(msg, ": ", 2)
	if len(parts) == 2 {
		pos := strings.SplitN(parts[0], ":", 2)
		if len(pos) == 2 {
			line, _ = strconv.Atoi(pos[0])
			col, _ = strconv.Atoi(pos[1])
			text = parts[1]
			return
		}
	}
	return 1, 1, msg
}

// lineColRange converts 1-based line/col to an LSP Range (0-based).
func lineColRange(line, col int) Range {
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	l := line - 1
	c := col - 1
	return Range{
		Start: Position{Line: l, Character: c},
		End:   Position{Line: l, Character: c + 1},
	}
}

// ---- LSP parameter types ----

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type DidOpenParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type VersionedTextDocument struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

type DidChangeParams struct {
	TextDocument   VersionedTextDocument            `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type DidCloseParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type CompletionParams struct {
	TextDocumentPositionParams
}

type SignatureHelpParams struct {
	TextDocumentPositionParams
}

type CompletionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
	InsertText    string `json:"insertText,omitempty"`
}

type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature"`
	ActiveParameter int                    `json:"activeParameter"`
}

type SignatureInformation struct {
	Label         string                 `json:"label"`
	Documentation string                 `json:"documentation,omitempty"`
	Parameters    []ParameterInformation `json:"parameters,omitempty"`
}

type ParameterInformation struct {
	Label string `json:"label"`
}
