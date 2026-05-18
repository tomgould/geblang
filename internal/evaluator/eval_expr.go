package evaluator

import (
	"fmt"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/runtime"
)

// EvalExpression parses and evaluates a single expression string in the given
// environment. Used by the debug adapter for watch expressions, hover, and
// conditional breakpoints.
func (e *Evaluator) EvalExpression(src string, env *runtime.Environment) (runtime.Value, error) {
	// The parser requires statements to end with a semicolon. Callers may omit
	// it (e.g. when the user types a value in the VS Code variable editor), so
	// add one if the input doesn't already end with one.
	if len(src) > 0 && src[len(src)-1] != ';' {
		src = src + ";"
	}
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) > 0 {
		return nil, fmt.Errorf("%s", errs[0])
	}
	if len(prog.Statements) == 0 {
		return runtime.Null{}, nil
	}
	exprStmt, ok := prog.Statements[0].(*ast.ExpressionStatement)
	if !ok {
		return nil, fmt.Errorf("expected expression")
	}
	val, err := e.evalExpression(exprStmt.Expression, env)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return runtime.Null{}, nil
	}
	return val, nil
}
