// Package formatter provides canonical source formatting for Geblang programs; it preserves comments and refuses (never corrupts) any output that would not re-parse to the same AST.
package formatter

import (
	"fmt"
	"reflect"
	"strings"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// Format parses src, formats it canonically, and returns the result.
// Returns a parse error if src is not valid Geblang.
func Format(src []byte) (result []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, fmt.Errorf("formatter bug: panic while formatting (%v); source left unchanged", r)
		}
	}()
	p := parser.New(lexer.New(string(src)))
	program := p.ParseProgram()
	if errs := p.Errors(); len(errs) > 0 {
		return nil, fmt.Errorf("parse error: %s", errs[0])
	}
	f := &fmtr{comments: p.Comments()}
	f.program(program)
	out := strings.TrimRight(f.buf.String(), "\n") + "\n"
	if err := verifyRoundTrip(program, out); err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// verifyRoundTrip refuses any output that does not re-parse to the same AST, so fmt can never silently change a program's meaning or break its syntax.
func verifyRoundTrip(input *ast.Program, out string) error {
	p := parser.New(lexer.New(out))
	reparsed := p.ParseProgram()
	if errs := p.Errors(); len(errs) > 0 {
		return fmt.Errorf("formatter bug: output no longer parses (%s); source left unchanged", errs[0])
	}
	if input.String() != reparsed.String() {
		return fmt.Errorf("formatter bug: formatting would change the program's meaning; source left unchanged")
	}
	return nil
}

type fmtr struct {
	buf             strings.Builder
	depth           int
	prevWasTopLevel bool
	comments        []lexer.Comment
	ci              int
}

// startLine returns a statement's first source line via its Token field, or a large sentinel for nodes without one.
func startLine(s ast.Statement) int {
	v := reflect.Indirect(reflect.ValueOf(s))
	if v.Kind() == reflect.Struct {
		if tok := v.FieldByName("Token"); tok.IsValid() && tok.Kind() == reflect.Struct {
			if line := tok.FieldByName("Line"); line.IsValid() && line.CanInt() {
				return int(line.Int())
			}
		}
	}
	return 1 << 30
}

// endLine returns the largest source line in a statement's subtree (its last token), so blank-line gaps after multi-line statements are measured correctly.
func endLine(s ast.Statement) int {
	max := 0
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Ptr, reflect.Interface:
			walk(v.Elem())
		case reflect.Struct:
			if v.Type().Name() == "Token" {
				if line := v.FieldByName("Line"); line.IsValid() && line.CanInt() {
					if n := int(line.Int()); n > max {
						max = n
					}
				}
				return
			}
			for i := 0; i < v.NumField(); i++ {
				if fv := v.Field(i); fv.CanInterface() {
					walk(fv)
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(v.MapIndex(k))
			}
		}
	}
	walk(reflect.ValueOf(s))
	return max
}

// maybeBlank emits one blank line when the source had a gap (>1 line) between the previous statement's end and the next statement or its leading comment.
func (f *fmtr) maybeBlank(prevEnd int, s ast.Statement) {
	if prevEnd <= 0 {
		return
	}
	next := startLine(s)
	if f.ci < len(f.comments) && f.comments[f.ci].Line < next {
		next = f.comments[f.ci].Line
	}
	if next-prevEnd > 1 {
		f.nl()
	}
}

// flushComments emits every pending comment whose source line precedes beforeLine.
func (f *fmtr) flushComments(beforeLine int) {
	for f.ci < len(f.comments) && f.comments[f.ci].Line < beforeLine {
		f.writeln(f.pad() + renderComment(f.comments[f.ci]))
		f.ci++
	}
}

func renderComment(c lexer.Comment) string {
	switch c.Kind {
	case "doc-line":
		if c.Text == "" {
			return "##"
		}
		return "## " + c.Text
	case "block":
		if strings.Contains(c.Text, "\n") {
			return "/*" + c.Text + "*/"
		}
		return "/* " + strings.TrimSpace(c.Text) + " */"
	case "doc-block":
		if strings.Contains(c.Text, "\n") {
			return "/**" + c.Text + "*/"
		}
		return "/** " + strings.TrimSpace(c.Text) + " */"
	default:
		if c.Text == "" {
			return "#"
		}
		return "# " + c.Text
	}
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
			f.flushComments(startLine(stmts[i]))
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
		f.flushComments(startLine(stmts[i]))
		f.stmt(stmts[i])
		i++
	}
	f.flushComments(1 << 30)
}

// ---- statements ----

func (f *fmtr) stmt(s ast.Statement) {
	switch s := s.(type) {
	case *ast.ModuleStatement:
		f.writeln("module " + strings.Join(s.Path, ".") + ";")
	case *ast.ImportStatement:
		line := "import " + strings.Join(s.Path, ".")
		if s.Alias != nil {
			line += " as " + s.Alias.Value
		}
		f.writeln(line + ";")
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
		} else if lit, ok := s.Value.(*ast.ListLiteral); ok && lit.Bare {
			f.writeln(f.pad() + "return " + f.bareElements(lit) + ";")
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
	case *ast.WithStatement:
		head := "with ("
		if s.Name != nil {
			head += s.Name.Value + " = "
		}
		head += f.expr(s.Value) + ") "
		f.write(f.pad() + head)
		f.block(s.Body)
	case *ast.SelectStatement:
		f.writeln(f.pad() + "select {")
		f.depth++
		for _, c := range s.Cases {
			var sc string
			if c.Kind == "send" {
				sc = f.expr(c.Channel) + ".send(" + f.expr(c.Value) + ")"
			} else {
				if c.Binding != "" {
					sc = "let " + c.Binding + " = "
				}
				sc += f.expr(c.Channel) + ".recv()"
			}
			f.write(f.pad() + "case " + sc + ": ")
			f.block(c.Body)
		}
		if s.Default != nil {
			f.write(f.pad() + "default: ")
			f.block(s.Default)
		}
		f.depth--
		f.writeln(f.pad() + "}")
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
	case *ast.InterfaceStatement:
		f.fmtInterface(s)
	case *ast.EnumStatement:
		f.fmtEnum(s)
	default:
		f.stmtNoPrefix(s)
	}
}

func (f *fmtr) stmtNoPrefix(s ast.Statement) {
	switch s := s.(type) {
	case *ast.DeclarationStatement:
		line := strings.Join(declPrefix(s), " ")
		if s.Value != nil {
			line += " = " + f.expr(s.Value)
		}
		f.writeln(line + ";")
	default:
		f.writeln(s.String() + ";")
	}
}

// ---- declaration ----

// declPrefix renders the qualifier + optional type + name of a declaration, preserving const/static (Kind) which a typed-only render would drop.
func declPrefix(s *ast.DeclarationStatement) []string {
	var parts []string
	if s.Kind != "" {
		parts = append(parts, s.Kind)
	}
	if s.Type != nil {
		parts = append(parts, s.Type.String())
	}
	if len(parts) == 0 {
		parts = append(parts, "let")
	}
	parts = append(parts, s.Name.Value)
	return parts
}

func (f *fmtr) fmtDecorator(dec ast.Decorator) string {
	if len(dec.Arguments) > 0 {
		return "@" + dec.Name.Value + "(" + f.callArgs(dec.Arguments) + ")"
	}
	return "@" + dec.Name.Value
}

func (f *fmtr) typeArgs(args []*ast.TypeRef) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.String()
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

func (f *fmtr) fmtDeclaration(s *ast.DeclarationStatement) {
	line := f.pad() + strings.Join(declPrefix(s), " ")
	if s.Value != nil {
		line += " = " + f.expr(s.Value)
	}
	f.writeln(line + ";")
}

func (f *fmtr) fmtDestructuring(s *ast.DestructuringStatement) {
	var lhs string
	if s.IsList && s.Bare {
		parts := make([]string, 0, len(s.Names))
		for _, n := range s.Names {
			parts = append(parts, n.Value)
		}
		lhs = strings.Join(parts, ", ")
	} else if s.IsList {
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
	rhs := f.expr(s.Value)
	if lit, ok := s.Value.(*ast.ListLiteral); ok && lit.Bare {
		rhs = f.bareElements(lit)
	}
	f.writeln(f.pad() + kw + lhs + " = " + rhs + ";")
}

// bareElements renders list elements comma-separated, without brackets.
func (f *fmtr) bareElements(lit *ast.ListLiteral) string {
	parts := make([]string, 0, len(lit.Elements))
	for _, el := range lit.Elements {
		parts = append(parts, f.expr(el))
	}
	return strings.Join(parts, ", ")
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
	prevEnd := 0
	for _, s := range block.Statements {
		f.maybeBlank(prevEnd, s)
		f.flushComments(startLine(s))
		f.stmt(s)
		prevEnd = endLine(s)
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
	for i, dec := range s.Decorators {
		if i > 0 {
			f.write(f.pad())
		}
		f.writeln(f.fmtDecorator(dec))
	}
	if len(s.Decorators) > 0 {
		f.write(f.pad())
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
	for i, dec := range s.Decorators {
		if i > 0 {
			f.write(f.pad())
		}
		f.writeln(f.fmtDecorator(dec))
	}
	if len(s.Decorators) > 0 {
		f.write(f.pad())
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
	prevEnd := 0
	for _, m := range s.Members {
		f.maybeBlank(prevEnd, m)
		f.flushComments(startLine(m))
		f.stmt(m)
		prevEnd = endLine(m)
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
	header := "enum " + s.Name.Value
	if len(s.Implements) > 0 {
		parts := make([]string, 0, len(s.Implements))
		for _, iface := range s.Implements {
			parts = append(parts, iface.String())
		}
		header += " implements " + strings.Join(parts, ", ")
	}
	f.writeln(f.pad() + header + " {")
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
		} else if len(s.Methods) > 0 {
			line += ";"
		}
		f.writeln(line)
	}
	for _, m := range s.Methods {
		f.fmtFunction(m)
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
	head := "default"
	if !c.Default {
		head = "case " + f.matchCasePattern(c)
	}
	if c.Body == nil { // arrow form: `case pat => value;`
		f.writeln(f.pad() + head + " => " + f.expr(c.Value) + ";")
		return
	}
	f.write(f.pad() + head + ": ")
	f.block(c.Body)
}

func (f *fmtr) matchCasePattern(c ast.MatchCase) string {
	var pat string
	switch {
	case c.EnumVariant != nil:
		pat = f.enumVariantPattern(c.EnumVariant)
	case c.ListPattern != nil:
		pat = f.listPattern(c.ListPattern)
	case c.Type != nil:
		pat = c.Type.String()
		if c.Name != nil {
			pat += " " + c.Name.Value
		}
	default:
		pat = f.matchPattern(c.Pattern)
	}
	for _, alt := range c.Alternates {
		pat += " | " + f.expr(alt)
	}
	if c.Guard != nil {
		pat += " if (" + f.expr(c.Guard) + ")"
	}
	return pat
}

func (f *fmtr) enumVariantPattern(p *ast.EnumVariantPattern) string {
	s := p.Enum.Value + "." + p.Variant.Value
	if len(p.Params) > 0 {
		parts := make([]string, len(p.Params))
		for i, pp := range p.Params {
			seg := ""
			if pp.Type != nil {
				seg = pp.Type.String() + " "
			}
			if pp.Name != nil {
				seg += pp.Name.Value
			}
			parts[i] = seg
		}
		s += "(" + strings.Join(parts, ", ") + ")"
	}
	return s
}

func (f *fmtr) listPattern(p *ast.ListPatternMatch) string {
	parts := make([]string, len(p.Bindings))
	for i, b := range p.Bindings {
		if b.Literal != nil {
			parts[i] = f.expr(b.Literal)
			continue
		}
		seg := ""
		if b.Type != nil {
			seg = b.Type.String() + " "
		}
		if b.Name != nil {
			seg += b.Name.Value
		}
		parts[i] = seg
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func (f *fmtr) matchPattern(p ast.Expression) string {
	if p == nil {
		return "_"
	}
	return f.expr(p)
}

// ---- expressions ----

// Precedence ladder mirroring internal/parser so expr() can parenthesize exactly where re-association would otherwise change meaning.
const (
	fpLowest = iota
	fpAssign
	fpPipe
	fpTernary
	fpNullCoalesce
	fpLogicalOr
	fpLogicalAnd
	fpBitOr
	fpBitXor
	fpBitAnd
	fpEquality
	fpCompare
	fpShift
	fpSum
	fpProduct
	fpPower
	fpPrefix
	fpPostfix
	fpCall
)

var fmtInfixPrec = map[string]int{
	"??": fpNullCoalesce, "|>": fpPipe,
	"||": fpLogicalOr, "xor": fpLogicalOr, "&&": fpLogicalAnd,
	"|": fpBitOr, "^": fpBitXor, "&": fpBitAnd,
	"==": fpEquality, "!=": fpEquality, "is": fpEquality, "is not": fpEquality, "instanceof": fpEquality,
	"in": fpCompare, "<": fpCompare, "<=": fpCompare, ">": fpCompare, ">=": fpCompare,
	"<<": fpShift, ">>": fpShift,
	"+": fpSum, "-": fpSum,
	"*": fpProduct, "/": fpProduct, "//": fpProduct, "%": fpProduct,
	"**": fpPower,
}

func fmtPrec(e ast.Expression) int {
	switch x := e.(type) {
	case *ast.InfixExpression:
		if p, ok := fmtInfixPrec[x.Operator]; ok {
			return p
		}
		return fpLowest + 1 // unknown operator: parenthesize defensively
	case *ast.PrefixExpression, *ast.AwaitExpression:
		return fpPrefix
	case *ast.PostfixExpression:
		return fpPostfix
	case *ast.CastExpression, *ast.RangeExpression:
		return fpCompare
	case *ast.TernaryExpression:
		return fpTernary
	case *ast.AssignmentExpression:
		return fpAssign
	case *ast.SpreadExpression:
		return fpLowest
	default:
		return fpCall
	}
}

// fmtIsPrimary reports whether e can sit unparenthesized to the left of a postfix tail (call/index/selector).
func fmtIsPrimary(e ast.Expression) bool {
	switch e.(type) {
	case *ast.InfixExpression, *ast.PrefixExpression, *ast.PostfixExpression,
		*ast.CastExpression, *ast.TernaryExpression, *ast.AssignmentExpression,
		*ast.RangeExpression, *ast.AwaitExpression, *ast.SpreadExpression:
		return false
	}
	return true
}

func parenIf(cond bool, s string) string {
	if cond {
		return "(" + s + ")"
	}
	return s
}

// nodeLine returns an expression's source line via its Token field, or 0.
func nodeLine(e ast.Expression) int {
	v := reflect.Indirect(reflect.ValueOf(e))
	if v.Kind() == reflect.Struct {
		if tok := v.FieldByName("Token"); tok.IsValid() && tok.Kind() == reflect.Struct {
			if line := tok.FieldByName("Line"); line.IsValid() && line.CanInt() {
				return int(line.Int())
			}
		}
	}
	return 0
}

// flattenInfix collects the operands of a left-leaning chain of one operator.
func flattenInfix(e *ast.InfixExpression, op string) []ast.Expression {
	var operands []ast.Expression
	var walk func(n ast.Expression)
	walk = func(n ast.Expression) {
		if inf, ok := n.(*ast.InfixExpression); ok && inf.Operator == op {
			walk(inf.Left)
			operands = append(operands, inf.Right)
			return
		}
		operands = append(operands, n)
	}
	walk(e)
	return operands
}

// shouldBreakInfix reports whether a +/&&/|| chain of 3+ operands spanned multiple source lines (an intentional author line break to preserve).
func (f *fmtr) shouldBreakInfix(e *ast.InfixExpression) bool {
	op := e.Operator
	if op != "+" && op != "&&" && op != "||" {
		return false
	}
	operands := flattenInfix(e, op)
	if len(operands) < 3 {
		return false
	}
	minL, maxL := 1<<30, 0
	for _, o := range operands {
		l := nodeLine(o)
		if l == 0 {
			continue
		}
		if l < minL {
			minL = l
		}
		if l > maxL {
			maxL = l
		}
	}
	return maxL > minL
}

// multilineInfix renders a chain with each continuation operand on its own line, operator-first, indented one level past the statement.
func (f *fmtr) multilineInfix(e *ast.InfixExpression) string {
	op := e.Operator
	operands := flattenInfix(e, op)
	self := fmtPrec(e)
	indent := strings.Repeat("    ", f.depth+1)
	var b strings.Builder
	for i, o := range operands {
		wrap := fmtPrec(o) <= self
		if i == 0 {
			wrap = fmtPrec(o) < self
		}
		s := parenIf(wrap, f.expr(o))
		if i == 0 {
			b.WriteString(s)
		} else {
			b.WriteString("\n" + indent + op + " " + s)
		}
	}
	return b.String()
}

// sliceIndex renders a range used as an index in Python slice syntax: start:end[:step].
func (f *fmtr) sliceIndex(r *ast.RangeExpression) string {
	s := ""
	if r.Start != nil {
		s += f.expr(r.Start)
	}
	s += ":"
	if r.End != nil {
		s += f.expr(r.End)
	}
	if r.Step != nil {
		s += ":" + f.expr(r.Step)
	}
	return s
}

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
		sep := ""
		if n := len(e.Operator); n > 0 && e.Operator[n-1] >= 'a' && e.Operator[n-1] <= 'z' {
			sep = " "
		}
		return e.Operator + sep + parenIf(fmtPrec(e.Right) < fpPrefix, f.expr(e.Right))
	case *ast.PostfixExpression:
		return parenIf(fmtPrec(e.Left) < fpPostfix, f.expr(e.Left)) + e.Operator
	case *ast.InfixExpression:
		if f.shouldBreakInfix(e) {
			return f.multilineInfix(e)
		}
		self := fmtPrec(e)
		leftWrap := fmtPrec(e.Left) < self
		rightWrap := fmtPrec(e.Right) <= self
		if e.Operator == "**" { // right-associative
			leftWrap = fmtPrec(e.Left) <= self
			rightWrap = fmtPrec(e.Right) < self
		}
		if _, isCast := e.Left.(*ast.CastExpression); isCast && (e.Operator == "|" || e.Operator == "&") {
			leftWrap = true // a cast's type greedily absorbs a following | / & as a union/intersection type
		}
		return parenIf(leftWrap, f.expr(e.Left)) + " " + e.Operator + " " + parenIf(rightWrap, f.expr(e.Right))
	case *ast.AssignmentExpression:
		return f.expr(e.Left) + " = " + f.expr(e.Value)
	case *ast.SelectorExpression:
		dot := "."
		if e.Optional {
			dot = "?."
		}
		return parenIf(!fmtIsPrimary(e.Object), f.expr(e.Object)) + dot + e.Name.Value
	case *ast.CallExpression:
		return parenIf(!fmtIsPrimary(e.Callee), f.expr(e.Callee)) + f.typeArgs(e.TypeArguments) + "(" + f.callArgs(e.Arguments) + ")"
	case *ast.IndexExpression:
		idx := f.expr(e.Index)
		if rng, ok := e.Index.(*ast.RangeExpression); ok {
			idx = f.sliceIndex(rng) // a range as an index is Python-style slice syntax
		}
		return parenIf(!fmtIsPrimary(e.Left), f.expr(e.Left)) + "[" + idx + "]"
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
		return parenIf(fmtPrec(e.Value) < fpCompare, f.expr(e.Value)) + " as " + e.Type.String()
	case *ast.TernaryExpression:
		cond := parenIf(fmtPrec(e.Condition) <= fpTernary, f.expr(e.Condition))
		then := parenIf(fmtPrec(e.ThenExpr) < fpTernary, f.expr(e.ThenExpr))
		els := parenIf(fmtPrec(e.ElseExpr) < fpTernary, f.expr(e.ElseExpr))
		return cond + " ? " + then + " : " + els
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
		switch part := p.(type) {
		case *ast.StringLiteral:
			sb.WriteString(part.Raw)
		case *ast.FormattedInterpolation:
			sb.WriteString("${")
			sb.WriteString(f.expr(part.Value))
			sb.WriteString(":")
			sb.WriteString(part.Spec)
			sb.WriteString("}")
		default:
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
	inner := &fmtr{depth: f.depth + 1, comments: f.comments, ci: f.ci}
	prevEnd := 0
	for _, s := range block.Statements {
		inner.maybeBlank(prevEnd, s)
		inner.flushComments(startLine(s))
		inner.stmt(s)
		prevEnd = endLine(s)
	}
	f.ci = inner.ci
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
		parts = append(parts, f.param(p))
	}
	return strings.Join(parts, ", ")
}

// param renders a parameter, using f.expr for the default so dict/list/closure defaults are not emitted as String() placeholders.
func (f *fmtr) param(p ast.Parameter) string {
	prefix := ""
	for _, d := range p.Decorators {
		prefix += f.fmtDecorator(d) + " "
	}
	var seg []string
	if p.Const {
		seg = append(seg, "const")
	}
	if p.Type != nil {
		seg = append(seg, p.Type.String())
	}
	if p.Variadic {
		seg = append(seg, "...")
	}
	if p.Name != nil {
		seg = append(seg, p.Name.Value)
	}
	out := prefix + strings.Join(seg, " ")
	if p.Default != nil {
		out += " = " + f.expr(p.Default)
	}
	return out
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
