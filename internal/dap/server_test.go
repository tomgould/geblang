package dap

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"geblang/internal/evaluator"
)

// TestRunScriptAbortsOnStaticError verifies the DAP launch pre-flight
// (matching what `geblang run` does at the CLI). The example file
// has overloads for `describe(string)` and `describe(int)` but the
// caller passes a `float`. The bytecode compiler catches the
// no-matching-overload at compile time; the DAP server should abort
// the launch and emit the diagnostic to the debug console rather than
// run the script partway and crash at runtime.
func TestRunScriptAbortsOnStaticError(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "bad_overload.gb")
	if err := os.WriteFile(scriptPath, []byte(`import io;
func describe(string v): string { return "s:" + v; }
func describe(int v): string { return "i:" + (v as string); }
io.println("would run if pre-flight skipped");
io.println(describe(1.0f));
`), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	var out bytes.Buffer
	s := &Server{
		w:            &out,
		breakpoints:  map[string]map[int]breakpointInfo{},
		sourcePaths:  map[string]string{},
		terminatedCh: make(chan error, 1),
		scriptPath:   scriptPath,
	}
	s.runScript()

	err := <-s.terminatedCh
	if err == nil {
		t.Fatal("expected terminate-with-error from runScript")
	}
	if !strings.Contains(err.Error(), "no matching overload for describe") {
		t.Fatalf("error: got %v", err)
	}
	if !strings.Contains(out.String(), "no matching overload for describe") {
		t.Fatalf("debug-console output should include the diagnostic, got %q", out.String())
	}
	if strings.Contains(out.String(), "would run if pre-flight skipped") {
		t.Fatalf("script body must not execute; got %q", out.String())
	}
}

// TestRunScriptAbortsOnSemanticError verifies the semantic-side branch
// of the pre-flight: a module file with a free-standing top-level
// statement should also abort before any execution.
func TestRunScriptAbortsOnSemanticError(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "bad_module.gb")
	if err := os.WriteFile(scriptPath, []byte(`module bad;
import io;
io.println("free-standing at module top level");
`), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	var out bytes.Buffer
	s := &Server{
		w:            &out,
		breakpoints:  map[string]map[int]breakpointInfo{},
		sourcePaths:  map[string]string{},
		terminatedCh: make(chan error, 1),
		scriptPath:   scriptPath,
	}
	s.runScript()

	err := <-s.terminatedCh
	if err == nil {
		t.Fatal("expected terminate-with-error from runScript")
	}
	if !strings.Contains(err.Error(), "static analysis failed") {
		t.Fatalf("error: got %v", err)
	}
	if !strings.Contains(out.String(), "free-standing top-level") {
		t.Fatalf("debug-console output should include the diagnostic, got %q", out.String())
	}
	if strings.Contains(out.String(), "free-standing at module top level") {
		t.Fatalf("script body must not execute; got %q", out.String())
	}
}

func TestNormalizePathUsesCwdForRelativeProgram(t *testing.T) {
	cwd := filepath.Join("tmp", "project")
	got, err := normalizePath("src/main.gb", "", cwd)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(filepath.Join(cwd, "src", "main.gb"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("normalizePath = %q, want %q", got, want)
	}
}

func TestNormalizePathHandlesFileURI(t *testing.T) {
	got, err := normalizePath("file:///tmp/app/main.gb", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(filepath.Join(string(filepath.Separator), "tmp", "app", "main.gb"))
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("normalizePath = %q, want %q", got, want)
	}
}

func TestNormalizePathHandlesWSLLocalhostUNC(t *testing.T) {
	got, err := normalizePath(`\\wsl.localhost\Ubuntu\home\daveg\projects\geblang\examples\functions.gb`, "", "/home/daveg/projects/geblang/examples")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(string(filepath.Separator), "home", "daveg", "projects", "geblang", "examples", "functions.gb")
	if got != filepath.Clean(want) {
		t.Fatalf("normalizePath = %q, want %q", got, want)
	}
}

func TestNormalizePathHandlesWSLDollarUNC(t *testing.T) {
	got, err := normalizePath(`\\wsl$\Ubuntu\home\daveg\projects\geblang\examples\functions.gb`, "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(string(filepath.Separator), "home", "daveg", "projects", "geblang", "examples", "functions.gb")
	if got != filepath.Clean(want) {
		t.Fatalf("normalizePath = %q, want %q", got, want)
	}
}

func TestWindowsDriveToWSLPath(t *testing.T) {
	got, ok := windowsDriveToWSLPath(`C:\Users\dave\project\main.gb`)
	if !ok {
		t.Fatal("expected Windows drive path to be recognized")
	}
	want := "/mnt/c/Users/dave/project/main.gb"
	if got != want {
		t.Fatalf("windowsDriveToWSLPath = %q, want %q", got, want)
	}
}

func TestStackTraceReturnsClientSourcePath(t *testing.T) {
	var out bytes.Buffer
	s := &Server{
		w:           &out,
		sourcePaths: map[string]string{},
		lastPause: &evaluator.DebugPause{Loc: evaluator.DebugLocation{
			Path: "/home/daveg/projects/geblang/examples/functions.gb",
			Line: 10,
		}, Frames: []evaluator.DebugFrame{{
			Name: "<top level>",
			Path: "/home/daveg/projects/geblang/examples/functions.gb",
			Line: 10,
		}}, Vars: []evaluator.DebugVariable{
			{Name: "answer", Value: "42", Type: "int"},
		}},
	}
	clientPath := `\\wsl.localhost\Ubuntu\home\daveg\projects\geblang\examples\functions.gb`
	s.recordClientSourcePathLocked("/home/daveg/projects/geblang/examples/functions.gb", clientPath, "")

	if err := s.handleRequest(&Message{Seq: 1, Command: "stackTrace"}); err != nil {
		t.Fatal(err)
	}
	response := readDAPResponse(t, out.String())
	body := response["body"].(map[string]any)
	frames := body["stackFrames"].([]any)
	frame := frames[0].(map[string]any)
	source := frame["source"].(map[string]any)
	if source["path"] != clientPath {
		t.Fatalf("source path = %q, want %q", source["path"], clientPath)
	}
	if source["name"] != "functions.gb" {
		t.Fatalf("source name = %q, want functions.gb", source["name"])
	}
	if frame["id"].(float64) != 1 {
		t.Fatalf("frame id = %v, want 1", frame["id"])
	}
}

func TestScopesAndVariablesReturnPausedLocals(t *testing.T) {
	var out bytes.Buffer
	s := &Server{
		w: &out,
		lastPause: &evaluator.DebugPause{
			Frames: []evaluator.DebugFrame{{Name: "<top level>", Path: "/tmp/main.gb", Line: 1}},
			Vars:   []evaluator.DebugVariable{{Name: "name", Value: "Geblang", Type: "string"}},
		},
	}
	if err := s.handleRequest(&Message{Seq: 1, Command: "stackTrace"}); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := s.handleRequest(&Message{Seq: 2, Command: "scopes", Arguments: map[string]any{"frameId": 1}}); err != nil {
		t.Fatal(err)
	}
	scopeResponse := readDAPResponse(t, out.String())
	scopes := scopeResponse["body"].(map[string]any)["scopes"].([]any)
	scope := scopes[0].(map[string]any)
	if scope["variablesReference"].(float64) != 1 {
		t.Fatalf("variablesReference = %v, want 1", scope["variablesReference"])
	}

	out.Reset()
	if err := s.handleRequest(&Message{Seq: 3, Command: "variables", Arguments: map[string]any{"variablesReference": 1}}); err != nil {
		t.Fatal(err)
	}
	varResponse := readDAPResponse(t, out.String())
	variables := varResponse["body"].(map[string]any)["variables"].([]any)
	if len(variables) != 1 {
		t.Fatalf("expected one variable, got %#v", variables)
	}
	variable := variables[0].(map[string]any)
	if variable["name"] != "name" || variable["value"] != "Geblang" || variable["type"] != "string" {
		t.Fatalf("unexpected variable %#v", variable)
	}
}

func readDAPResponse(t *testing.T, raw string) map[string]any {
	t.Helper()
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatalf("invalid DAP response %q", raw)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(parts[1]), &response); err != nil {
		t.Fatal(err)
	}
	return response
}
