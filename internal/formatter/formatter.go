// Package formatter provides canonical source formatting for Geblang programs.
// Comments are not preserved in this implementation; the output is
// re-parseable to the same AST but without original comments.
package formatter

import (
	"fmt"
	"strings"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// Format parses src, formats it canonically, and returns the result.
// Returns a parse error if src is not valid Geblang.
func Format(src []byte) ([]byte, error) {
	p := parser.New(lexer.New(string(src)))
	program := p.ParseProgram()
	if errs := p.Errors(); len(errs) > 0 {
		return nil, fmt.Errorf("parse error: %s", errs[0])
	}
	f := &fmtr{}
	f.program(program)
	out := strings.TrimRight(f.buf.String(), "\n") + "\n"
	return []byte(out), nil
}

type fmtr struct {
	buf    strings.Builder
	depth  int
	prevWasTopLevel bool
}

func (f *fmtr) pad() string { return strings.Repeat("    ", f.depth) }

func (f *fmtr) write(s string)   { f.buf.WriteString(s) }
func (f *fmtr) writeln(s string) { f.buf.WriteString(s); f.buf.WriteByte('\n') }
func (f *fmtr) nl()              { f.buf.WriteByte('\n') }

// ---- top level ----

func (f *fmtr) program(prog *ast.Program) {
	// Module/import statements first (no blank line between them), then a blank
	// line before other declarations.
	stmts := prog.Statements
	i := 0
	// imports and module declarations
	for i < len(stmts) {
		switch stmts[i].(type) {
		case *ast.ModuleStatement, *ast.ImportStatement:
			f.stmt(stmts[i])
			i++
			continue
		}
		break
	}
	// remaining top-level statements with blank lines between them
	for i < len(stmts) {
		if i > 0 {
			f.nl()
		}
		f.stmt(stmts[i])
		i++
	}
}

// ---- statements ----

func (f *fmtr) stmt(s ast.Statement) {
	switch s := s.(type) {
	case *ast.ModuleStatement:
		f.writeln("module " + strings.Join(s.Path, ".") + ";")
	case *ast.ImportStatement:
		f.writeln("import " + strings.Join(s.Path, ".") + ";")
	case *ast.ExportStatement:
		f.write(f.pad() + "export ")
		f.stmtInner(s.Statement)
	case *ast.TypeAliasStatement:
		f.writeln(f.pad() + "type " + s.Name.Value + " = " + s.Type.String() + ";")
	case *ast.DeclarationStatement:
		f.fmtDeclaration(s)
	case *ast.DestructuringStatement:
		f.fmtDestructuring(s)
	case *ast.ExpressionStatement:
		f.writeln(f.pad() + f.expr(s.Expression) + ";")
	case *ast.ReturnStatement:
		if s.Value == nil {
			f.writeln(f.pad() + "return;")
		} else {
			f.writeln(f.pad() + "return " + f.expr(s.Value) + ";")
		}
	case *ast.YieldStatement:
		if s.Value == nil {
			f.writeln(f.pad() + "yield;")
		} else {
			f.writeln(f.pad() + "yield " + f.expr(s.Value) + ";")
		}
	case *ast.SimpleStatement:
		if s.Value == nil {
			f.writeln(f.pad() + s.Kind + ";")
		} else {
			f.writeln(f.pad() + s.Kind + " " + f.expr(s.Value) + ";")
		}
	case *ast.IfStatement:
		f.fmtIf(s)
	case *ast.WhileStatement:
		f.write(f.pad() + "while (" + f.expr(s.Condition) + ") ")
		f.block(s.Body)
	case *ast.ForStatement:
		f.fmtFor(s)
	case *ast.FunctionStatement:
		f.fmtFunction(s)
	case *ast.ClassStatement:
		f.fmtClass(s)
	case *ast.InterfaceStatement:
		f.fmtInterface(s)
	case *ast.EnumStatement:
		f.fmtEnum(s)
	case *ast.TryStatement:
		f.fmtTry(s)
	case *ast.MatchStatement:
		f.fmtMatch(s)
	case *ast.InitStatement:
		f.write(f.pad() + "init ")
		f.block(s.Body)
	default:
		// Fallback: use AST's own String() method
		f.writeln(f.pad() + s.String() + ";")
	}
}

// stmtInner writes a statement without the leading pad (used after "export ").
func (f *fmtr) stmtInner(s ast.Statement) {
	switch s := s.(type) {
	case *ast.FunctionStatement:
		f.fmtFunctionRaw(s)
	case *ast.ClassStatement:
		f.fmtClassRaw(s)
	default:
		f.stmtNoPrefix(s)
	}
}

func (f *fmtr) stmtNoPrefix(s ast.Statement) {
	switch s := s.(type) {
	case *ast.DeclarationStatement:
		var parts []string
		if s.Type != nil {
			parts = append(parts, s.Type.String())
		} else {
			parts = append(parts, "let")
		}
		parts = append(parts, s.Name.Value)
		line := strings.Join(parts, " ")
		if s.Value != nil {
			line += " = " + f.expr(s.Value)
		}
		f.writeln(line + ";")
	default:
		f.writeln(s.String() + ";")
	}
}

// ---- declaration ----

func (f *fmtr) fmtDeclaration(s *ast.DeclarationStatement) {
	var parts []string
	if s.Type != nil {
		parts = append(parts, s.Type.String())
	} else {
		parts = append(parts, "let")
	}
	parts = append(parts, s.Name.Value)
	line := f.pad() + strings.Join(parts, " ")
	if s.Value != nil {
		line += " = " + f.expr(s.Value)
	}
	f.writeln(line + ";")
}

func (f *fmtr) fmtDestructuring(s *ast.DestructuringStatement) {
	var lhs string
	if s.IsList {
		parts := make([]string, 0, len(s.Names))
		for _, n := range s.Names {
			parts = append(parts, n.Value)
		}
		lhs = "[" + strings.Join(parts, ", ") + "]"
	} else {
		parts := make([]string, 0, len(s.Names))
		for i, n := range s.Names {
			entry := n.Value
			if i < len(s.Keys) && s.Keys[i] != n.Value {
				entry = s.Keys[i] + ": " + n.Value
			}
			parts = append(parts, entry)
		}
		lhs = "{" + strings.Join(parts, ", ") + "}"
	}
	kw := "let "
	if !s.Define {
		kw = ""
	}
	f.writeln(f.pad() + kw + lhs + " = " + f.expr(s.Value) + ";")
}

// ---- if ----

func (f *fmtr) fmtIf(s *ast.IfStatement) {
	f.write(f.pad() + "if (" + f.expr(s.Condition) + ") ")
	f.blockInline(s.Consequence, func() {
		for _, ei := range s.ElseIfs {
			f.write(" else if (" + f.expr(ei.Condition) + ") ")
			f.blockInline(ei.Body, nil)
		}
		if s.Alternative != nil {
			f.write(" else ")
			f.block(s.Alternative)
		} else {
			f.nl()
		}
	})
}

// blockInline writes a block and calls after() before the trailing newline
// (used to chain else/else-if on the same line as the closing brace).
func (f *fmtr) blockInline(block *ast.BlockStatement, after func()) {
	if block == nil || len(block.Statements) == 0 {
		if after != nil {
			f.write("{}")
			after()
		} else {
			f.writeln("{}")
		}
		return
	}
	f.writeln("{")
	f.depth++
	for _, s := range block.Statements {
		f.stmt(s)
	}
	f.depth--
	f.write(f.pad() + "}")
	if after != nil {
		after()
	} else {
		f.nl()
	}
}

// block writes a block with its own trailing newline.
func (f *fmtr) block(block *ast.BlockStatement) {
	f.blockInline(block, nil)
}

// ---- for ----

func (f *fmtr) fmtFor(s *ast.ForStatement) {
	if s.VarName != nil || len(s.VarNames) > 0 {
		// for-in loop
		rangeStr := f.expr(s.Iterable)
		if s.Step != nil {
			rangeStr += " by " + f.expr(s.Step)
		}
		var vars string
		if len(s.VarNames) > 0 {
			parts := make([]string, 0, len(s.VarNames))
			for _, v := range s.VarNames {
				parts = append(parts, v.Value)
			}
			vars = strings.Join(parts, ", ")
		} else {
			if s.VarType != nil {
				vars = s.VarType.String() + " "
			}
			vars += s.VarName.Value
		}
		f.write(f.pad() + "for (" + vars + " in " + rangeStr + ") ")
		f.block(s.Body)
		return
	}
	// C-style for
	var initStr, condStr, updateStr string
	if s.Init != nil {
		// strip trailing newline/semicolon from statement rendering
		tmp := &fmtr{depth: 0}
		tmp.stmt(s.Init)
		initStr = strings.TrimRight(tmp.buf.String(), "\n;")
	}
	if s.Condition != nil {
		condStr = f.expr(s.Condition)
	}
	if s.Update != nil {
		tmp := &fmtr{depth: 0}
		tmp.stmt(s.Update)
		updateStr = strings.TrimRight(tmp.buf.String(), "\n;")
	}
	f.write(f.pad() + "for (" + initStr + "; " + condStr + "; " + updateStr + ") ")
	f.block(s.Body)
}

// ---- function ----

func (f *fmtr) fmtFunction(s *ast.FunctionStatement) {
	f.write(f.pad())
	f.fmtFunctionRaw(s)
}

func (f *fmtr) fmtFunctionRaw(s *ast.FunctionStatement) {
	for _, dec := range s.Decorators {
		f.writeln("@" + dec.Name.Value + f.callArgs(dec.Arguments))
	}
	prefix := ""
	if s.Static {
		prefix += "static "
	}
	if s.Async {
		prefix += "async "
	}
	prefix += "func " + s.Name.Value
	if len(s.Generics) > 0 {
		prefix += "<" + f.typeParams(s.Generics) + ">"
	}
	prefix += "(" + f.params(s.Parameters) + ")"
	if s.ReturnType != nil {
		prefix += ": " + s.ReturnType.String()
	}
	prefix += " "
	f.write(prefix)
	f.block(s.Body)
}

// ---- class ----

func (f *fmtr) fmtClass(s *ast.ClassStatement) {
	f.write(f.pad())
	f.fmtClassRaw(s)
}

func (f *fmtr) fmtClassRaw(s *ast.ClassStatement) {
	for _, dec := range s.Decorators {
		f.writeln("@" + dec.Name.Value + f.callArgs(dec.Arguments))
	}
	line := "class " + s.Name.Value
	if len(s.Generics) > 0 {
		line += "<" + f.typeParams(s.Generics) + ">"
	}
	if s.Extends != nil {
		line += " extends " + s.Extends.String()
	}
	if len(s.Implements) > 0 {
		parts := make([]string, 0, len(s.Implements))
		for _, iface := range s.Implements {
			parts = append(parts, iface.String())
		}
		line += " implements " + strings.Join(parts, ", ")
	}
	f.writeln(line + " {")
	f.depth++
	for _, m := range s.Members {
		f.stmt(m)
	}
	f.depth--
	f.writeln(f.pad() + "}")
}

// ---- interface ----

func (f *fmtr) fmtInterface(s *ast.InterfaceStatement) {
	line := f.pad() + "interface " + s.Name.Value
	if len(s.Generics) > 0 {
		line += "<" + f.typeParams(s.Generics) + ">"
	}
	if len(s.Parents) > 0 {
		parts := make([]string, 0, len(s.Parents))
		for _, p := range s.Parents {
			parts = append(parts, p.String())
		}
		line += " extends " + strings.Join(parts, ", ")
	}
	f.writeln(line + " {")
	f.depth++
	for _, m := range s.Methods {
		f.fmtSignature(m)
	}
	f.depth--
	f.writeln(f.pad() + "}")
}

func (f *fmtr) fmtSignature(sig *ast.FunctionSignature) {
	line := f.pad() + "func " + sig.Name.Value
	if len(sig.Generics) > 0 {
		line += "<" + f.typeParams(sig.Generics) + ">"
	}
	line += "(" + f.params(sig.Parameters) + ")"
	if sig.ReturnType != nil {
		line += ": " + sig.ReturnType.String()
	}
	f.writeln(line + ";")
}

// ---- enum ----

func (f *fmtr) fmtEnum(s *ast.EnumStatement) {
	f.writeln(f.pad() + "enum " + s.Name.Value + " {")
	f.depth++
	for i, v := range s.Variants {
		line := f.pad() + v.Name.Value
		if len(v.FieldTypes) > 0 {
			parts := make([]string, 0, len(v.FieldTypes))
			for _, ft := range v.FieldTypes {
				parts = append(parts, ft.String())
			}
			line += "(" + strings.Join(parts, ", ") + ")"
		}
		if i < len(s.Variants)-1 {
			line += ","
		}
		f.writeln(line)
	}
	f.depth--
	f.writeln(f.pad() + "}")
}

// ---- try ----

func (f *fmtr) fmtTry(s *ast.TryStatement) {
	f.write(f.pad() + "try ")
	f.blockInline(s.Body, func() {
		for _, catch := range s.Catches {
			clause := " catch"
			if catch.Type != nil || catch.Name != nil {
				clause += " ("
				if catch.Type != nil {
					clause += catch.Type.String()
				}
				if catch.Name != nil {
					if catch.Type != nil {
						clause += " "
					}
					clause += catch.Name.Value
				}
				clause += ")"
			}
			f.write(clause + " ")
			f.blockInline(catch.Body, nil)
		}
		if s.Finally != nil && len(s.Finally.Statements) > 0 {
			f.write(" finally ")
			f.block(s.Finally)
		} else {
			f.nl()
		}
	})
}

// ---- match ----

func (f *fmtr) fmtMatch(s *ast.MatchStatement) {
	f.writeln(f.pad() + "match (" + f.expr(s.Expr) + ") {")
	f.depth++
	for _, c := range s.Cases {
		f.fmtMatchCase(c)
	}
	f.depth--
	f.writeln(f.pad() + "}")
}

func (f *fmtr) fmtMatchCase(c ast.MatchCase) {
	var pat string
	if c.Type != nil {
		pat = c.Type.String()
		if c.Name != nil {
			pat += " " + c.Name.Value
		}
	} else {
		pat = f.matchPattern(c.Pattern)
	}
	for _, alt := range c.Alternates {
		pat += " | " + f.expr(alt)
	}
	if c.Guard != nil {
		pat += " if " + f.expr(c.Guard)
	}
	f.write(f.pad() + "case " + pat + " => ")
	if len(c.Body.Statements) == 1 {
		// Single-statement body: inline
		tmp := &fmtr{depth: 0}
		tmp.stmt(c.Body.Statements[0])
		f.writeln(strings.TrimRight(tmp.buf.String(), "\n"))
	} else {
		f.block(c.Body)
	}
}

func (f *fmtr) matchPattern(p ast.Expression) string {
	if p == nil {
		return "_"
	}
	return f.expr(p)
}

// ---- expressions ----

func (f *fmtr) expr(e ast.Expression) string {
	if e == nil {
		return ""
	}
	switch e := e.(type) {
	case *ast.Identifier:
		return e.Value
	case *ast.Literal:
		switch v := e.Value.(type) {
		case nil:
			return "null"
		case bool:
			if v {
				return "true"
			}
			return "false"
		default:
			return fmt.Sprintf("%v", v)
		}
	case *ast.IntegerLiteral:
		return e.Token.Literal
	case *ast.DecimalLiteral:
		return e.Token.Literal
	case *ast.FloatLiteral:
		return e.Token.Literal
	case *ast.StringLiteral:
		return f.fmtString(e.Raw, e.Quote, e.Triple)
	case *ast.InterpolatedString:
		return f.fmtInterpolated(e)
	case *ast.ListLiteral:
		parts := make([]string, 0, len(e.Elements))
		for _, el := range e.Elements {
			parts = append(parts, f.expr(el))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ast.DictLiteral:
		parts := make([]string, 0, len(e.Entries))
		for _, entry := range e.Entries {
			if entry.Spread {
				parts = append(parts, "..."+f.expr(entry.Value))
				continue
			}
			parts = append(parts, f.expr(entry.Key)+": "+f.expr(entry.Value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *ast.SetLiteral:
		parts := make([]string, 0, len(e.Elements))
		for _, el := range e.Elements {
			parts = append(parts, f.expr(el))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *ast.PipeExpression:
		return f.expr(e.Left) + " |> " + f.expr(e.Right)
	case *ast.ListComprehension:
		return "[" + f.expr(e.Body) + f.fmtComprehensionClauses(e.Clauses) + "]"
	case *ast.SetComprehension:
		return "{" + f.expr(e.Body) + f.fmtComprehensionClauses(e.Clauses) + "}"
	case *ast.DictComprehension:
		return "{" + f.expr(e.KeyBody) + ": " + f.expr(e.ValueBody) + f.fmtComprehensionClauses(e.Clauses) + "}"
	case *ast.PrefixExpression:
		return e.Operator + f.expr(e.Right)
	case *ast.PostfixExpression:
		return f.expr(e.Left) + e.Operator
	case *ast.InfixExpression:
		return f.expr(e.Left) + " " + e.Operator + " " + f.expr(e.Right)
	case *ast.AssignmentExpression:
		return f.expr(e.Left) + " = " + f.expr(e.Value)
	case *ast.SelectorExpression:
		dot := "."
		if e.Optional {
			dot = "?."
		}
		return f.expr(e.Object) + dot + e.Name.Value
	case *ast.CallExpression:
		return f.expr(e.Callee) + "(" + f.callArgs(e.Arguments) + ")"
	case *ast.IndexExpression:
		return f.expr(e.Left) + "[" + f.expr(e.Index) + "]"
	case *ast.SpreadExpression:
		return "..." + f.expr(e.Value)
	case *ast.RangeExpression:
		op := ".."
		if e.Exclusive {
			op = "..<"
		}
		s := f.expr(e.Start) + op + f.expr(e.End)
		if e.Step != nil {
			s += " by " + f.expr(e.Step)
		}
		return s
	case *ast.FunctionLiteral:
		prefix := "func"
		if e.Async {
			prefix = "async func"
		}
		sig := prefix + "(" + f.params(e.Parameters) + ")"
		if e.ReturnType != nil {
			sig += ": " + e.ReturnType.String()
		}
		// Inline small bodies; block format larger ones
		body := f.inlineBlock(e.Body)
		return sig + " " + body
	case *ast.AwaitExpression:
		return "await " + f.expr(e.Value)
	case *ast.CastExpression:
		return f.expr(e.Value) + " as " + e.Type.String()
	case *ast.TernaryExpression:
		return f.expr(e.Condition) + " ? " + f.expr(e.ThenExpr) + " : " + f.expr(e.ElseExpr)
	case *ast.MatchExpression:
		return f.fmtMatchExpr(e)
	default:
		return e.String()
	}
}

func (f *fmtr) fmtString(raw string, quote byte, triple bool) string {
	q := string(quote)
	if triple {
		return q + q + q + raw + q + q + q
	}
	return q + raw + q
}

func (f *fmtr) fmtInterpolated(e *ast.InterpolatedString) string {
	quote := e.Token.Quote
	if quote == 0 {
		quote = '"'
	}
	q := string(quote)
	var sb strings.Builder
	if e.Token.Triple {
		sb.WriteString(q + q + q)
	} else {
		sb.WriteString(q)
	}
	for _, p := range e.Parts {
		if sl, ok := p.(*ast.StringLiteral); ok {
			sb.WriteString(sl.Raw)
		} else {
			sb.WriteString("${")
			sb.WriteString(f.expr(p))
			sb.WriteString("}")
		}
	}
	if e.Token.Triple {
		sb.WriteString(q + q + q)
	} else {
		sb.WriteString(q)
	}
	return sb.String()
}

func (f *fmtr) inlineBlock(block *ast.BlockStatement) string {
	if block == nil || len(block.Statements) == 0 {
		return "{}"
	}
	if len(block.Statements) == 1 {
		tmp := &fmtr{depth: 0}
		tmp.stmt(block.Statements[0])
		inner := strings.TrimRight(tmp.buf.String(), "\n")
		return "{ " + inner + " }"
	}
	// Multi-statement: write as indented block (returns string representation)
	var b strings.Builder
	b.WriteString("{\n")
	inner := &fmtr{depth: f.depth + 1}
	for _, s := range block.Statements {
		inner.stmt(s)
	}
	b.WriteString(inner.buf.String())
	b.WriteString(strings.Repeat("    ", f.depth) + "}")
	return b.String()
}

func (f *fmtr) fmtMatchExpr(e *ast.MatchExpression) string {
	var b strings.Builder
	b.WriteString("match (" + f.expr(e.Expr) + ") {\n")
	inner := &fmtr{depth: f.depth + 1}
	for _, c := range e.Cases {
		inner.fmtMatchCase(c)
	}
	b.WriteString(inner.buf.String())
	b.WriteString(strings.Repeat("    ", f.depth) + "}")
	return b.String()
}

// ---- helpers ----

func (f *fmtr) callArgs(args []ast.CallArgument) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		s := ""
		if arg.Spread {
			s = "..." + f.expr(arg.Value)
		} else if arg.Name != nil {
			s = arg.Name.Value + ": " + f.expr(arg.Value)
		} else {
			s = f.expr(arg.Value)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func (f *fmtr) params(params []ast.Parameter) string {
	parts := make([]string, 0, len(params))
	for _, p := range params {
		parts = append(parts, p.String())
	}
	return strings.Join(parts, ", ")
}

func (f *fmtr) fmtComprehensionClauses(clauses []ast.ComprehensionClause) string {
	var sb strings.Builder
	for _, clause := range clauses {
		switch c := clause.(type) {
		case *ast.ComprehensionFor:
			sb.WriteString(" for ")
			if c.VarType != nil {
				sb.WriteString(c.VarType.String() + " ")
			}
			if len(c.VarNames) > 0 {
				names := make([]string, 0, len(c.VarNames))
				for _, n := range c.VarNames {
					names = append(names, n.Value)
				}
				sb.WriteString(strings.Join(names, ", "))
			} else if c.VarName != nil {
				sb.WriteString(c.VarName.Value)
			}
			sb.WriteString(" in " + f.expr(c.Iterable))
		case *ast.ComprehensionIf:
			sb.WriteString(" if " + f.expr(c.Filter))
		}
	}
	return sb.String()
}

func (f *fmtr) typeParams(tps []*ast.TypeParam) string {
	parts := make([]string, 0, len(tps))
	for _, tp := range tps {
		s := tp.Name.Value
		if tp.Constraint != nil {
			s += " implements " + tp.Constraint.String()
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}
