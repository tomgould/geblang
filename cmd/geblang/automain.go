package main

import (
	"strings"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// appendMainInvocation wraps a call to an exported top-level `main` in an
// init block so a module-style entry runs directly as well as building.
func appendMainInvocation(program *ast.Program) bool {
	main := exportedMain(program)
	if main == nil {
		return false
	}

	callArgs := "sys.args()"
	if len(main.Parameters) == 0 {
		callArgs = ""
	}
	var body strings.Builder
	if returnsValue(main) {
		body.WriteString("let __geb_main_result = main(" + callArgs + ");\n")
		body.WriteString("if (__geb_main_result != null) { sys.exit(__geb_main_result as int); }\n")
	} else {
		body.WriteString("main(" + callArgs + ");\n")
	}

	bodyStmts := parseStatements(body.String())
	if bodyStmts == nil {
		return false
	}

	// sys must be imported before the init runs; the evaluator resolves imports in
	// source order, so insert at the front (after a leading `module` if present).
	if !importsSys(program) {
		imp := parseStatements("import sys;\n")
		at := 0
		if len(program.Statements) > 0 {
			if _, ok := program.Statements[0].(*ast.ModuleStatement); ok {
				at = 1
			}
		}
		program.Statements = append(program.Statements[:at], append(imp, program.Statements[at:]...)...)
	}

	if init := existingInit(program); init != nil {
		init.Body.Statements = append(init.Body.Statements, bodyStmts...)
	} else {
		initStmts := parseStatements("init {\n" + body.String() + "}\n")
		if initStmts == nil {
			return false
		}
		program.Statements = append(program.Statements, initStmts...)
	}
	return true
}

func parseStatements(src string) []ast.Statement {
	p := parser.New(lexer.New(src))
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil
	}
	return prog.Statements
}

func exportedMain(program *ast.Program) *ast.FunctionStatement {
	for _, st := range program.Statements {
		export, ok := st.(*ast.ExportStatement)
		if !ok {
			continue
		}
		if fn, ok := export.Statement.(*ast.FunctionStatement); ok && fn.Name != nil && fn.Name.Value == "main" {
			return fn
		}
	}
	return nil
}

func existingInit(program *ast.Program) *ast.InitStatement {
	for _, st := range program.Statements {
		if init, ok := st.(*ast.InitStatement); ok {
			return init
		}
	}
	return nil
}

func importsSys(program *ast.Program) bool {
	for _, st := range program.Statements {
		if imp, ok := st.(*ast.ImportStatement); ok && !imp.ForceBuiltin && imp.Alias == nil && len(imp.Path) == 1 && imp.Path[0] == "sys" {
			return true
		}
	}
	return false
}

func returnsValue(fn *ast.FunctionStatement) bool {
	return fn.ReturnType != nil && strings.TrimSpace(fn.ReturnType.String()) != "void"
}
