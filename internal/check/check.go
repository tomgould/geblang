// Package check is the shared static-analysis pipeline used by both
// `geblang check` (CLI) and the LSP server. It runs parse, semantic
// analysis, import resolution, bytecode compilation (for the type
// checks the semantic pass misses), and optional lint rules over a
// single Geblang source file.
package check

import (
	"fmt"
	"path/filepath"
	"strings"

	"geblang/internal/ast"
	"geblang/internal/bytecode"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/native"
	"geblang/internal/parser"
	"geblang/internal/semantic"
)

// Severity classifies a diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is a single check finding.
type Diagnostic struct {
	File     string   `json:"file"`
	Line     int      `json:"line,omitempty"`
	Column   int      `json:"column,omitempty"`
	Severity Severity `json:"severity"`
	Rule     string   `json:"rule,omitempty"`
	Message  string   `json:"message"`
}

// String renders the diagnostic in the geblang-check CLI's traditional
// `file:line:col: severity[rule]: message` format.
func (d Diagnostic) String() string {
	prefix := d.File
	if d.Line > 0 {
		prefix = fmt.Sprintf("%s:%d:%d", prefix, d.Line, d.Column)
	}
	if prefix == "" {
		return fmt.Sprintf("%s: %s", d.Severity, d.Message)
	}
	if d.Rule != "" {
		return fmt.Sprintf("%s: %s[%s]: %s", prefix, d.Severity, d.Rule, d.Message)
	}
	return fmt.Sprintf("%s: %s: %s", prefix, d.Severity, d.Message)
}

// Options controls which checks run.
type Options struct {
	// Lint runs the optional lint rules (unused-import, unreachable).
	Lint bool
	// Resolver, when non-nil, is the module resolver used for import
	// checks. When nil, callers should leave imports unchecked or build
	// a default resolver via NewDefaultResolver.
	Resolver *modules.Resolver
	// CrossModule turns on cross-module symbol checks (foo.bar where
	// bar is not exported by foo). Requires Resolver.
	CrossModule bool
	// NativeSymbols, when non-nil, lists the exported function names
	// for each native module name. Used by cross-module checks. The
	// LSP populates this from internal/lsp/catalog.go.
	NativeSymbols map[string]map[string]struct{}
	// ModuleCache, when non-nil, caches parsed module sources keyed by
	// absolute path so successive cross-module checks don't re-parse.
	ModuleCache *ModuleCache
}

// NewDefaultResolver returns a resolver rooted at the file's directory.
// Mirrors the per-file behaviour of the legacy `checkResolver` helper.
func NewDefaultResolver(file string) *modules.Resolver {
	return modules.NewResolver([]string{filepath.Dir(file)})
}

// Source runs the configured checks against a single source file.
// Returns the parsed program (or nil on parse failure) and the
// accumulated diagnostics.
func Source(file, source string, opts Options) (*ast.Program, []Diagnostic) {
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		diags := make([]Diagnostic, 0, len(p.Errors()))
		for _, msg := range p.Errors() {
			line, col, text := parseParserError(msg)
			diags = append(diags, Diagnostic{
				File:     file,
				Line:     line,
				Column:   col,
				Severity: SeverityError,
				Rule:     "parse",
				Message:  text,
			})
		}
		return nil, diags
	}
	diags := []Diagnostic{}
	analyzer := semantic.New()
	if opts.CrossModule {
		analyzer.EnableMethodChecks()
		if opts.Resolver != nil {
			cache := opts.ModuleCache
			if cache == nil {
				cache = NewModuleCache()
			}
			graph := buildClassGraph(program, opts, cache)
			analyzer.SetClassSurfaceResolver(graph.surface)
		}
	}
	for _, sd := range analyzer.Analyze(program) {
		severity := SeverityError
		if sd.Severity == semantic.SeverityWarning {
			severity = SeverityWarning
		}
		diags = append(diags, Diagnostic{
			File:     file,
			Line:     sd.Line,
			Column:   sd.Column,
			Severity: severity,
			Rule:     "semantic",
			Message:  sd.Message,
		})
	}
	if _, compileErr := bytecode.Compile(program, []byte(source), file); compileErr != nil {
		if d, ok := compileDiagnostic(file, compileErr); ok {
			diags = append(diags, d)
		}
	}
	if opts.Resolver != nil {
		diags = append(diags, checkImports(file, program, opts)...)
		if opts.CrossModule {
			diags = append(diags, checkCrossModuleSymbols(file, program, opts)...)
		}
	}
	if opts.Lint {
		diags = append(diags, lintProgram(file, program)...)
	}
	return program, diags
}

// compileDiagnostic maps a bytecode compile error onto the static-
// analysis contract. A VM capability gap (IsParityError) is valid code
// the tree-walking evaluator runs but the bytecode VM cannot build yet,
// so it surfaces as a vm-unsupported warning rather than vanishing;
// every other compile error is a genuine static error both backends
// must reject. Returns false only for a nil error.
func compileDiagnostic(file string, compileErr error) (Diagnostic, bool) {
	if compileErr == nil {
		return Diagnostic{}, false
	}
	if bytecode.IsParityError(compileErr) {
		return Diagnostic{
			File:     file,
			Severity: SeverityWarning,
			Rule:     "vm-unsupported",
			Message:  compileErr.Error(),
		}, true
	}
	return Diagnostic{
		File:     file,
		Severity: SeverityError,
		Rule:     "type",
		Message:  compileErr.Error(),
	}, true
}

// parseParserError splits "line:col: message" into its parts. Parser
// errors that don't match this shape fall back to (0, 0, msg).
func parseParserError(msg string) (line, col int, text string) {
	parts := strings.SplitN(msg, ": ", 2)
	if len(parts) != 2 {
		return 0, 0, msg
	}
	pos := strings.SplitN(parts[0], ":", 2)
	if len(pos) != 2 {
		return 0, 0, msg
	}
	if _, err := fmt.Sscanf(pos[0], "%d", &line); err != nil {
		return 0, 0, msg
	}
	if _, err := fmt.Sscanf(pos[1], "%d", &col); err != nil {
		return 0, 0, msg
	}
	return line, col, parts[1]
}

func checkImports(file string, program *ast.Program, opts Options) []Diagnostic {
	resolver := opts.Resolver
	isNative := func(canonical string) bool {
		if IsNativeImport(canonical) {
			return true
		}
		// Engine-native modules (e.g. secureRandom, the ffi/ssh/proc
		// bridges) are not all listed in NativeModuleNames; trust the
		// engine-derived symbol set when present.
		_, ok := opts.NativeSymbols[canonical]
		return ok
	}
	diags := []Diagnostic{}
	for _, stmt := range program.Statements {
		switch imp := stmt.(type) {
		case *ast.ImportStatement:
			canonical := strings.Join(imp.Path, ".")
			if canonical == "" || isNative(canonical) {
				continue
			}
			if _, err := resolver.Resolve(canonical); err != nil {
				diags = append(diags, Diagnostic{
					File:     file,
					Line:     imp.Token.Line,
					Column:   imp.Token.Column,
					Severity: SeverityError,
					Rule:     "import",
					Message:  fmt.Sprintf("cannot resolve import %s", canonical),
				})
			}
		case *ast.FromImportStatement:
			canonical := strings.Join(imp.Path, ".")
			if canonical == "" || isNative(canonical) {
				continue
			}
			if _, err := resolver.Resolve(canonical); err != nil {
				diags = append(diags, Diagnostic{
					File:     file,
					Line:     imp.Token.Line,
					Column:   imp.Token.Column,
					Severity: SeverityError,
					Rule:     "import",
					Message:  fmt.Sprintf("cannot resolve import %s", canonical),
				})
			}
		}
	}
	return diags
}

// IsNativeImport reports whether the canonical module name is shipped
// as a native (Go-implemented) module. Thin pass-through to
// internal/native.IsNativeModule so the list has a single source of
// truth.
func IsNativeImport(canonical string) bool {
	return native.IsNativeModule(canonical)
}

// NativeImportModules returns the set of canonical names of native
// modules. Exposed so the LSP code-action quick-fix can rank candidates.
func NativeImportModules() []string {
	return native.NativeModuleList()
}

// SameFilePath reports whether two paths resolve to the same file on
// disk after absolute-path normalisation.
func SameFilePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil && rightErr == nil {
		return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
