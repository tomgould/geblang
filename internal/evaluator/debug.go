package evaluator

import (
	"reflect"

	"geblang/internal/ast"
	"geblang/internal/runtime"
)

// DebugLocation is a source position reported to the debug hook.
type DebugLocation struct {
	Path string
	Line int
}

// DebugFrame is one entry in the call stack exposed to the debugger.
type DebugFrame struct {
	Name string
	Path string
	Line int
}

// DebugVariable is a name/value pair snapshotted at a pause point.
type DebugVariable struct {
	Name  string
	Value string
	Type  string
}

// DebugPause is the full state sent to the debugger when execution pauses.
type DebugPause struct {
	Loc       DebugLocation
	Frames    []DebugFrame
	Vars      []DebugVariable  // variables for the innermost (current) frame
	FrameVars [][]DebugVariable // per-frame variables: FrameVars[i] corresponds to Frames[i]
	Env       *runtime.Environment // current environment (for evaluate/setVariable)
	Reason    string // "breakpoint", "step", "pause", "exception"
}

// DebugHookFunc is called before each statement when a debug hook is installed.
// It may block; execution resumes when it returns.
type DebugHookFunc func(pause DebugPause)

// SetDebugHook installs a debug hook. The hook is called before each statement
// in any block. It may block to implement pause/resume semantics.
func (e *Evaluator) SetDebugHook(hook DebugHookFunc) {
	e.debugHook = hook
}

// SetDebugSourcePath sets the file path reported in debug locations.
func (e *Evaluator) SetDebugSourcePath(path string) {
	e.debugSourcePath = path
}

// callDebugHook is called from evalStatements for each statement.
func (e *Evaluator) callDebugHook(stmt ast.Statement, env *runtime.Environment, reason string) {
	if e.debugHook == nil {
		return
	}
	line := statementLine(stmt)
	if line == 0 {
		return
	}
	path := e.debugSourcePath
	frames, frameVars := e.snapshotFrames(path, line, env)
	pause := DebugPause{
		Loc:       DebugLocation{Path: path, Line: line},
		Frames:    frames,
		Vars:      snapshotEnv(env),
		FrameVars: frameVars,
		Env:       env,
		Reason:    reason,
	}
	e.debugHook(pause)
}

// snapshotFrames builds a call frame list and per-frame variable snapshots.
func (e *Evaluator) snapshotFrames(currentPath string, currentLine int, currentEnv *runtime.Environment) ([]DebugFrame, [][]DebugVariable) {
	frames := make([]DebugFrame, 0, len(e.callStack)+1)
	frameVars := make([][]DebugVariable, 0, len(e.callStack)+1)

	// Current position as the innermost frame
	frames = append(frames, DebugFrame{Name: "<top level>", Path: currentPath, Line: currentLine})
	frameVars = append(frameVars, snapshotEnv(currentEnv))

	for i := len(e.callStack) - 1; i >= 0; i-- {
		f := e.callStack[i]
		name := f.name
		if name == "" {
			name = "<anonymous>"
		}
		frames = append(frames, DebugFrame{Name: name, Path: currentPath, Line: f.line})
		frameVars = append(frameVars, snapshotEnv(f.env))
	}
	return frames, frameVars
}

// snapshotEnv captures all variables in the current (innermost) scope.
func snapshotEnv(env *runtime.Environment) []DebugVariable {
	if env == nil {
		return nil
	}
	names := env.VisibleNames()
	vars := make([]DebugVariable, 0, len(names))
	for _, name := range names {
		val, ok := env.Get(name)
		if !ok {
			continue
		}
		vars = append(vars, DebugVariable{
			Name:  name,
			Value: val.Inspect(),
			Type:  val.TypeName(),
		})
	}
	return vars
}

// statementLine extracts the source line from any AST statement via reflection.
// All concrete statement types have Token token.Token as their first field.
func statementLine(stmt ast.Statement) int {
	v := reflect.ValueOf(stmt)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	tok := v.FieldByName("Token")
	if !tok.IsValid() {
		return 0
	}
	line := tok.FieldByName("Line")
	if !line.IsValid() {
		return 0
	}
	return int(line.Int())
}
