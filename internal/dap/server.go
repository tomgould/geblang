package dap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode"

	gevruntime "geblang/internal/runtime"

	"geblang/internal/bytecode"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/semantic"
)

// stepMode controls what the evaluator pauses on next.
type stepMode int

const (
	modeContinue stepMode = iota // pause only at breakpoints
	modeNext                     // step over: pause at same or outer depth
	modeStepIn                   // step into: pause at every statement
	modeStepOut                  // step out: pause when stack depth decreases
)

// breakpointInfo stores per-breakpoint metadata.
type breakpointInfo struct {
	condition string // empty = unconditional
}

// pausedCmd is a request sent to the hook goroutine while execution is paused.
type pausedCmd struct {
	kind  string // "evaluate", "setVariable"
	expr  string
	frame int
	name  string
	value string
}

// pausedCmdResult is the response from the hook goroutine.
type pausedCmdResult struct {
	result string
	typ    string
	err    string
}

// Server is a DAP debug adapter server.
type Server struct {
	r io.Reader
	w io.Writer

	mu  sync.Mutex
	seq int

	// breakpoints: absolute path → line → breakpoint info
	breakpoints map[string]map[int]breakpointInfo
	sourcePaths map[string]string // normalized runtime path → client-facing path

	// debug session state
	scriptPath  string
	cwd         string
	scriptArgs  []string
	stopOnEntry bool

	// channels connecting the main DAP loop and the evaluator goroutine
	pauseCh      chan evaluator.DebugPause
	resumeCh     chan stepMode
	terminatedCh chan error
	evalReqCh    chan pausedCmd
	evalRepCh    chan pausedCmdResult

	// last pause snapshot (protected by mu)
	lastPause     *evaluator.DebugPause
	frameVars     map[int][]Variable
	lastException string
	exitCode      int
	terminated    bool
	paused        bool

	// current step mode and depth (written only by DAP loop, read by hook goroutine via closure)
	stepMode  stepMode
	stepDepth int // call stack depth when step was requested
}

// Serve runs the DAP protocol loop on r/w until the session ends.
// ServeTCP listens on the given TCP port on all interfaces, writes "IP:PORT\n"
// to portOut, accepts one connection, then serves DAP over it. Pass port 0 to
// bind to a random available port.
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

func Serve(r io.Reader, w io.Writer) error {
	s := &Server{
		r:            r,
		w:            w,
		breakpoints:  map[string]map[int]breakpointInfo{},
		sourcePaths:  map[string]string{},
		pauseCh:      make(chan evaluator.DebugPause),
		resumeCh:     make(chan stepMode, 1),
		terminatedCh: make(chan error, 1),
		evalReqCh:    make(chan pausedCmd),
		evalRepCh:    make(chan pausedCmdResult),
	}
	return s.run()
}

func (s *Server) run() error {
	requestCh := make(chan *Message, 4)

	go func() {
		br := bufio.NewReader(s.r)
		for {
			msg, err := readMessage(br)
			if err != nil {
				close(requestCh)
				return
			}
			requestCh <- msg
		}
	}()

	for {
		select {
		case req, ok := <-requestCh:
			if !ok {
				return nil
			}
			if err := s.handleRequest(req); err != nil {
				return err
			}
		case pause, ok := <-s.pauseCh:
			if !ok {
				// evaluator finished without sending terminated — shouldn't happen
				s.sendEvent("terminated", nil)
				return nil
			}
			s.mu.Lock()
			s.lastPause = &pause
			s.paused = true
			s.mu.Unlock()
			desc := ""
			if pause.Reason == "exception" {
				desc = s.lastException
			}
			s.sendEvent("stopped", StoppedEventBody{
				Reason:            pause.Reason,
				Description:       desc,
				ThreadID:          1,
				AllThreadsStopped: true,
				Text:              desc,
			})
		case err := <-s.terminatedCh:
			if err != nil {
				s.sendEvent("output", OutputEventBody{Category: "stderr", Output: err.Error() + "\n"})
			}
			s.mu.Lock()
			if err != nil && s.exitCode == 0 {
				s.exitCode = 1
			}
			exitCode := s.exitCode
			s.terminated = true
			s.paused = false
			s.mu.Unlock()
			s.sendEvent("exited", ExitedEventBody{ExitCode: exitCode})
			s.sendEvent("terminated", nil)
			return nil
		}
	}
}

func (s *Server) handleRequest(req *Message) error {
	switch req.Command {
	case "initialize":
		s.sendResponse(req, InitializeResponseBody{
			SupportsConfigurationDoneRequest:      true,
			SupportsSingleThreadExecutionRequests: true,
			SupportsEvaluateForHovers:             true,
			SupportsTerminateRequest:              true,
			SupportsSetVariable:                   true,
			SupportsConditionalBreakpoints:        true,
			SupportsExceptionInfoRequest:          true,
		})
		s.sendEvent("initialized", nil)

	case "setBreakpoints":
		var args SetBreakpointsArgs
		if err := remarshal(req.Arguments, &args); err != nil {
			s.sendErrorResponse(req, err.Error())
			return nil
		}
		path, err := normalizePath(args.Source.Path, args.Source.Name, s.cwd)
		if err != nil {
			s.sendErrorResponse(req, err.Error())
			return nil
		}
		s.mu.Lock()
		lines := map[int]breakpointInfo{}
		for _, bp := range args.Breakpoints {
			lines[bp.Line] = breakpointInfo{condition: bp.Condition}
		}
		s.breakpoints[path] = lines
		s.recordClientSourcePathLocked(path, args.Source.Path, args.Source.Name)
		s.mu.Unlock()
		result := make([]Breakpoint, len(args.Breakpoints))
		for i, bp := range args.Breakpoints {
			result[i] = Breakpoint{Verified: true, Line: bp.Line}
		}
		s.sendResponse(req, SetBreakpointsResponseBody{Breakpoints: result})

	case "configurationDone":
		s.sendResponse(req, nil)
		go s.runScript()

	case "launch":
		var args LaunchArgs
		if err := remarshal(req.Arguments, &args); err != nil {
			s.sendErrorResponse(req, err.Error())
			return nil
		}
		cwd, err := normalizeCwd(args.Cwd)
		if err != nil {
			s.sendErrorResponse(req, err.Error())
			return nil
		}
		s.cwd = cwd
		scriptPath, err := normalizePath(args.Program, "", cwd)
		if err != nil {
			s.sendErrorResponse(req, err.Error())
			return nil
		}
		s.scriptPath = scriptPath
		s.mu.Lock()
		s.recordClientSourcePathLocked(scriptPath, args.Program, "")
		s.mu.Unlock()
		s.scriptArgs = args.Args
		s.stopOnEntry = args.StopOnEntry
		s.sendResponse(req, nil)

	case "threads":
		s.sendResponse(req, ThreadsResponseBody{
			Threads: []Thread{{ID: 1, Name: "main"}},
		})

	case "stackTrace":
		s.mu.Lock()
		pause := s.lastPause
		s.mu.Unlock()
		if pause == nil {
			s.sendResponse(req, StackTraceResponseBody{})
			return nil
		}
		frames := make([]StackFrame, len(pause.Frames))
		frameVars := map[int][]Variable{}
		for i, f := range pause.Frames {
			id := i + 1
			name := f.Name
			sourcePath := s.clientSourcePath(f.Path)
			src := Source{Path: sourcePath, Name: sourceBase(sourcePath)}
			line := f.Line
			if i == 0 {
				line = pause.Loc.Line
			}
			frames[i] = StackFrame{ID: id, Name: name, Source: src, Line: line, Column: 1}
			// Use per-frame vars if available, fall back to innermost vars
			if i < len(pause.FrameVars) {
				frameVars[id] = dapVariables(pause.FrameVars[i])
			} else if i == 0 {
				frameVars[id] = dapVariables(pause.Vars)
			} else {
				frameVars[id] = []Variable{}
			}
		}
		s.mu.Lock()
		s.frameVars = frameVars
		s.mu.Unlock()
		s.sendResponse(req, StackTraceResponseBody{
			StackFrames: frames,
			TotalFrames: len(frames),
		})

	case "scopes":
		var args ScopesArgs
		if err := remarshal(req.Arguments, &args); err != nil {
			s.sendErrorResponse(req, err.Error())
			return nil
		}
		ref := args.FrameID
		if ref <= 0 {
			ref = 1
		}
		s.sendResponse(req, ScopesResponseBody{
			Scopes: []Scope{
				{Name: "Locals", VariablesReference: ref, Expensive: false},
			},
		})

	case "variables":
		var args VariablesArgs
		if err := remarshal(req.Arguments, &args); err != nil {
			s.sendErrorResponse(req, err.Error())
			return nil
		}
		s.mu.Lock()
		frameVars := s.frameVars
		pause := s.lastPause
		s.mu.Unlock()
		vars := []Variable{}
		if frameVars != nil && args.VariablesReference > 0 {
			if fv, ok := frameVars[args.VariablesReference]; ok {
				vars = fv
			}
		} else if pause != nil {
			vars = dapVariables(pause.Vars)
		}
		s.sendResponse(req, VariablesResponseBody{Variables: vars})

	case "setVariable":
		var args SetVariableArgs
		if err := remarshal(req.Arguments, &args); err != nil {
			s.sendErrorResponse(req, err.Error())
			return nil
		}
		s.mu.Lock()
		paused := s.paused
		s.mu.Unlock()
		if !paused {
			s.sendErrorResponse(req, "cannot set variable: not paused")
			return nil
		}
		s.evalReqCh <- pausedCmd{kind: "setVariable", name: args.Name, value: args.Value}
		rep := <-s.evalRepCh
		if rep.err != "" {
			s.sendErrorResponse(req, rep.err)
			return nil
		}
		s.sendResponse(req, SetVariableResponseBody{Value: rep.result, Type: rep.typ})

	case "evaluate":
		var args EvaluateArgs
		if err := remarshal(req.Arguments, &args); err != nil {
			s.sendErrorResponse(req, err.Error())
			return nil
		}
		s.mu.Lock()
		paused := s.paused
		s.mu.Unlock()
		if !paused {
			s.sendResponse(req, EvaluateResponseBody{Result: "(not paused)", VariablesReference: 0})
			return nil
		}
		s.evalReqCh <- pausedCmd{kind: "evaluate", expr: args.Expression, frame: args.FrameID}
		rep := <-s.evalRepCh
		if rep.err != "" {
			s.sendErrorResponse(req, rep.err)
			return nil
		}
		s.sendResponse(req, EvaluateResponseBody{Result: rep.result, Type: rep.typ, VariablesReference: 0})

	case "exceptionInfo":
		s.mu.Lock()
		exc := s.lastException
		s.mu.Unlock()
		if exc == "" {
			exc = "unknown exception"
		}
		s.sendResponse(req, ExceptionInfoResponseBody{
			ExceptionID: "exception",
			Description: exc,
			BreakMode:   "always",
		})

	case "continue":
		s.mu.Lock()
		s.stepMode = modeContinue
		s.paused = false
		s.mu.Unlock()
		s.sendResponse(req, ContinueResponseBody{AllThreadsContinued: true})
		s.resumeCh <- modeContinue

	case "next":
		s.mu.Lock()
		depth := 0
		if s.lastPause != nil {
			depth = len(s.lastPause.Frames) - 1
		}
		s.stepMode = modeNext
		s.stepDepth = depth
		s.paused = false
		s.mu.Unlock()
		s.sendResponse(req, nil)
		s.resumeCh <- modeNext

	case "stepIn":
		s.mu.Lock()
		s.stepMode = modeStepIn
		s.paused = false
		s.mu.Unlock()
		s.sendResponse(req, nil)
		s.resumeCh <- modeStepIn

	case "stepOut":
		s.mu.Lock()
		depth := 0
		if s.lastPause != nil {
			depth = len(s.lastPause.Frames) - 1
		}
		s.stepMode = modeStepOut
		s.stepDepth = depth
		s.paused = false
		s.mu.Unlock()
		s.sendResponse(req, nil)
		s.resumeCh <- modeStepOut

	case "pause":
		s.mu.Lock()
		s.stepMode = modeStepIn
		s.mu.Unlock()
		s.sendResponse(req, nil)

	case "disconnect":
		s.sendResponse(req, nil)
		select {
		case s.resumeCh <- modeContinue:
		default:
		}
		return io.EOF

	case "terminate":
		s.sendResponse(req, nil)
		select {
		case s.resumeCh <- modeContinue:
		default:
		}
		return io.EOF

	default:
		s.sendErrorResponse(req, fmt.Sprintf("unknown command: %s", req.Command))
	}
	return nil
}

// runScript starts the evaluator goroutine. Called after configurationDone.
func (s *Server) runScript() {
	src, err := os.ReadFile(s.scriptPath)
	if err != nil {
		s.terminatedCh <- fmt.Errorf("cannot read %s: %w", s.scriptPath, err)
		return
	}

	p := parser.New(lexer.New(string(src)))
	program := p.ParseProgram()
	if errs := p.Errors(); len(errs) > 0 {
		for _, e := range errs {
			s.sendEvent("output", OutputEventBody{Category: "stderr", Output: e + "\n"})
		}
		s.terminatedCh <- fmt.Errorf("parse errors in %s", s.scriptPath)
		return
	}

	// Pre-flight: run the same static checks `geblang run` does. Without
	// this, real errors like "no matching overload" (which the bytecode
	// compiler catches but the evaluator doesn't) execute partway and
	// crash at runtime - a confusing UX for users hitting Run Without
	// Debugging in VS Code (the DAP launch goes through here).
	hasSemanticError := false
	for _, diag := range semantic.New().Analyze(program) {
		prefix := "error: "
		if diag.Severity == semantic.SeverityWarning {
			prefix = "warning: "
		} else {
			hasSemanticError = true
		}
		msg := prefix
		if diag.Line > 0 {
			msg += fmt.Sprintf("line %d:%d: ", diag.Line, diag.Column)
		}
		msg += diag.Message + "\n"
		s.sendEvent("output", OutputEventBody{Category: "stderr", Output: msg})
	}
	if hasSemanticError {
		s.terminatedCh <- fmt.Errorf("static analysis failed for %s", s.scriptPath)
		return
	}
	// Try bytecode compilation as the secondary static-check pass.
	// Non-parity errors (no-matching-overload, type mismatch, undeclared
	// identifier) abort the launch. Parity gaps - constructs the
	// bytecode compiler doesn't support yet - are not real errors; the
	// evaluator handles them and the debug session can proceed.
	if _, compileErr := bytecode.Compile(program, src, s.scriptPath); compileErr != nil && !bytecode.IsParityError(compileErr) {
		s.sendEvent("output", OutputEventBody{Category: "stderr", Output: compileErr.Error() + "\n"})
		s.terminatedCh <- compileErr
		return
	}

	modulePaths := []string{}
	if s.cwd != "" {
		modulePaths = append(modulePaths, s.cwd)
	}
	ev := evaluator.NewWithArgsAndModulePaths(s.outputWriter(), s.scriptArgs, modulePaths)
	ev.SetDebugSourcePath(s.scriptPath)

	// current step state (read/written only from the hook, which runs in this goroutine)
	currentMode := modeContinue
	currentDepth := 0
	if s.stopOnEntry {
		currentMode = modeStepIn
	}

	ev.SetDebugHook(func(pause evaluator.DebugPause) {
		depth := len(pause.Frames) - 1 // 0 = top level

		// Check if we should pause at this location
		shouldPause, bpInfo := s.shouldPauseAt(pause.Loc, currentMode, currentDepth, depth)
		if !shouldPause {
			return
		}

		// Conditional breakpoint: evaluate condition before pausing
		if bpInfo != nil && bpInfo.condition != "" && pause.Env != nil {
			val, err := ev.EvalExpression(bpInfo.condition, pause.Env)
			if err != nil || !isTruthy(val) {
				return
			}
		}

		reason := pause.Reason
		if reason == "step" {
			s.mu.Lock()
			bps := s.breakpoints[pause.Loc.Path]
			s.mu.Unlock()
			if _, atBp := bps[pause.Loc.Line]; atBp {
				reason = "breakpoint"
			}
		}
		pause.Reason = reason

		// Send pause to DAP loop
		s.pauseCh <- pause

		// While paused: service eval/setVariable requests until resumed
		for {
			select {
			case mode := <-s.resumeCh:
				currentMode = mode
				currentDepth = depth
				return
			case cmd := <-s.evalReqCh:
				s.evalRepCh <- s.handlePausedCmd(cmd, pause, ev)
			}
		}
	})

	result, err := ev.Eval(program)
	if err != nil {
		s.mu.Lock()
		s.lastException = err.Error()
		s.mu.Unlock()
		s.terminatedCh <- err
		return
	}
	if result.ExitCode != 0 {
		s.sendEvent("output", OutputEventBody{
			Category: "stderr",
			Output:   fmt.Sprintf("process exited with code %d\n", result.ExitCode),
		})
	}
	s.mu.Lock()
	s.exitCode = result.ExitCode
	s.mu.Unlock()
	s.terminatedCh <- nil
}

// handlePausedCmd executes an evaluate or setVariable command while paused.
func (s *Server) handlePausedCmd(cmd pausedCmd, pause evaluator.DebugPause, ev *evaluator.Evaluator) pausedCmdResult {
	env := pause.Env
	// Use per-frame env if frameId > 1 and we have frame vars
	if cmd.frame > 1 && cmd.frame-1 < len(pause.FrameVars) {
		// We don't store per-frame envs directly; use the current env as fallback
		// (outer frames would need their own env refs, which pause.Env doesn't provide for non-current frames)
	}
	if env == nil {
		return pausedCmdResult{err: "no environment available"}
	}

	switch cmd.kind {
	case "evaluate":
		val, err := ev.EvalExpression(cmd.expr, env)
		if err != nil {
			return pausedCmdResult{err: err.Error()}
		}
		return pausedCmdResult{result: val.Inspect(), typ: val.TypeName()}

	case "setVariable":
		// Parse the new value as an expression
		val, err := ev.EvalExpression(cmd.value, env)
		if err != nil {
			return pausedCmdResult{err: fmt.Sprintf("invalid value: %s", err.Error())}
		}
		if err := env.Assign(cmd.name, val); err != nil {
			return pausedCmdResult{err: err.Error()}
		}
		// Refresh the variable snapshot in the pause
		pause.Vars = rebuildVars(env)
		s.mu.Lock()
		if s.lastPause != nil {
			s.lastPause.Vars = pause.Vars
			if len(s.frameVars) > 0 {
				if fv, ok := s.frameVars[1]; ok {
					// update frame 1 (innermost) vars
					_ = fv
					s.frameVars[1] = dapVariables(pause.Vars)
				}
			}
		}
		s.mu.Unlock()
		return pausedCmdResult{result: val.Inspect(), typ: val.TypeName()}
	}
	return pausedCmdResult{err: "unknown command"}
}

// rebuildVars re-snapshots variables from the environment after mutation.
func rebuildVars(env *gevruntime.Environment) []evaluator.DebugVariable {
	if env == nil {
		return nil
	}
	names := env.VisibleNames()
	vars := make([]evaluator.DebugVariable, 0, len(names))
	for _, name := range names {
		val, ok := env.Get(name)
		if !ok {
			continue
		}
		vars = append(vars, evaluator.DebugVariable{
			Name:  name,
			Value: val.Inspect(),
			Type:  val.TypeName(),
		})
	}
	return vars
}

// isTruthy mirrors the evaluator's truthiness rules for conditional breakpoints.
func isTruthy(v gevruntime.Value) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case gevruntime.Bool:
		return val.Value
	case gevruntime.Null:
		return false
	case gevruntime.Int:
		return val.Value != nil && val.Value.Sign() != 0
	case gevruntime.Float:
		return val.Value != 0
	case gevruntime.String:
		return val.Value != ""
	}
	return true
}

// shouldPauseAt reports whether the hook should pause at the given location.
// Returns (shouldPause, *breakpointInfo) — bpInfo is non-nil if at a breakpoint.
func (s *Server) shouldPauseAt(loc DebugLocation, mode stepMode, stepDepth, currentDepth int) (bool, *breakpointInfo) {
	s.mu.Lock()
	bps := s.breakpoints[loc.Path]
	s.mu.Unlock()
	if info, ok := bps[loc.Line]; ok {
		return true, &info
	}

	switch mode {
	case modeContinue:
		return false, nil
	case modeStepIn:
		return true, nil
	case modeNext:
		return currentDepth <= stepDepth, nil
	case modeStepOut:
		return currentDepth < stepDepth, nil
	}
	return false, nil
}

// outputWriter returns an io.Writer that sends output to the DAP client.
func (s *Server) outputWriter() io.Writer {
	return &dacOutput{s: s}
}

type dacOutput struct{ s *Server }

func (o *dacOutput) Write(p []byte) (int, error) {
	o.s.sendEvent("output", OutputEventBody{Category: "stdout", Output: string(p)})
	return len(p), nil
}

func dapVariables(vars []evaluator.DebugVariable) []Variable {
	out := make([]Variable, 0, len(vars))
	for _, v := range vars {
		out = append(out, Variable{
			Name:               v.Name,
			Value:              v.Value,
			Type:               v.Type,
			VariablesReference: 0,
		})
	}
	return out
}

// ---- protocol framing ----

func readMessage(r *bufio.Reader) (*Message, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
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
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (s *Server) writeMessage(msg any) {
	data, _ := json.Marshal(msg)
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, "Content-Length: %d\r\n\r\n", len(data))
	s.w.Write(data)
}

func (s *Server) nextSeq() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return s.seq
}

func (s *Server) sendResponse(req *Message, body any) {
	s.writeMessage(map[string]any{
		"seq":         s.nextSeq(),
		"type":        "response",
		"request_seq": req.Seq,
		"command":     req.Command,
		"success":     true,
		"body":        body,
	})
}

func (s *Server) sendErrorResponse(req *Message, message string) {
	s.writeMessage(map[string]any{
		"seq":         s.nextSeq(),
		"type":        "response",
		"request_seq": req.Seq,
		"command":     req.Command,
		"success":     false,
		"message":     message,
	})
}

func (s *Server) sendEvent(event string, body any) {
	s.writeMessage(map[string]any{
		"seq":   s.nextSeq(),
		"type":  "event",
		"event": event,
		"body":  body,
	})
}

func (s *Server) recordClientSourcePathLocked(runtimePath, clientPath, fallbackName string) {
	if s.sourcePaths == nil {
		s.sourcePaths = map[string]string{}
	}
	displayPath := strings.TrimSpace(clientPath)
	if displayPath == "" {
		displayPath = strings.TrimSpace(fallbackName)
	}
	if displayPath == "" {
		displayPath = runtimePath
	}
	s.sourcePaths[runtimePath] = displayPath
}

func (s *Server) clientSourcePath(runtimePath string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sourcePath, ok := s.sourcePaths[runtimePath]; ok && sourcePath != "" {
		return sourcePath
	}
	return runtimePath
}

func sourceBase(path string) string {
	path = strings.TrimRight(path, `\/`)
	if path == "" {
		return ""
	}
	lastSlash := strings.LastIndex(path, "/")
	lastBackslash := strings.LastIndex(path, `\`)
	last := lastSlash
	if lastBackslash > last {
		last = lastBackslash
	}
	if last >= 0 && last+1 < len(path) {
		return path[last+1:]
	}
	return filepath.Base(path)
}

func remarshal(src, dst any) error {
	b, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func normalizeCwd(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", nil
	}
	return normalizePath(cwd, "", "")
}

func normalizePath(path, fallbackName, cwd string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(fallbackName)
	}
	if path == "" {
		return "", fmt.Errorf("missing source path")
	}
	if runtime.GOOS != "windows" {
		if linuxPath, ok := wslUNCToLinuxPath(path); ok {
			path = linuxPath
		} else if linuxPath, ok := windowsDriveToWSLPath(path); ok {
			path = linuxPath
		}
	}
	if strings.HasPrefix(path, "file://") {
		trimmed := strings.TrimPrefix(path, "file://")
		if strings.HasPrefix(trimmed, "/") && looksLikeWindowsDrive(trimmed[1:]) {
			trimmed = trimmed[1:]
		}
		path = trimmed
	}
	path = filepath.FromSlash(path)
	if cwd != "" && !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func looksLikeWindowsDrive(path string) bool {
	return len(path) >= 2 && unicode.IsLetter(rune(path[0])) && path[1] == ':'
}

func wslUNCToLinuxPath(path string) (string, bool) {
	normalized := strings.ReplaceAll(path, "/", "\\")
	for _, prefix := range []string{`\\wsl.localhost\`, `\\wsl$\`, `\\wsl\`} {
		if !strings.HasPrefix(strings.ToLower(normalized), strings.ToLower(prefix)) {
			continue
		}
		rest := normalized[len(prefix):]
		parts := strings.SplitN(rest, `\`, 2)
		if len(parts) != 2 || parts[1] == "" {
			return "", false
		}
		return "/" + strings.ReplaceAll(parts[1], `\`, "/"), true
	}
	return "", false
}

func windowsDriveToWSLPath(path string) (string, bool) {
	if !looksLikeWindowsDrive(path) {
		return "", false
	}
	drive := unicode.ToLower(rune(path[0]))
	rest := path[2:]
	rest = strings.TrimLeft(rest, `\/`)
	rest = strings.ReplaceAll(rest, `\`, "/")
	if rest == "" {
		return "/mnt/" + string(drive), true
	}
	return "/mnt/" + string(drive) + "/" + rest, true
}

// DebugLocation aliases the evaluator type for internal use.
type DebugLocation = evaluator.DebugLocation
