package bytecode_test

import (
	"fmt"
	"strings"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// compileOnVM compiles src on the bytecode backend without running it.
// Module-recognition is a compile-time decision, and importing a module
// (db, sockets, http) must not actually execute in a guard.
func compileOnVM(src string) error {
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return fmt.Errorf("parse: %s", strings.Join(p.Errors(), "; "))
	}
	_, err := bytecode.Compile(program, []byte(src), "guard")
	return err
}

// isUnsupportedModuleErr reports a VM capability gap for a module import
// (the "does not support builtin module X yet" / parity class), as
// opposed to a genuine source/type error.
func isUnsupportedModuleErr(err error) bool {
	if err == nil {
		return false
	}
	return bytecode.IsParityError(err) ||
		strings.Contains(err.Error(), "does not support builtin module")
}

// TestNativeModulesRecognizedByVM guards the module half of the static-
// analysis contract: every module in NativeModuleSymbols (the canonical
// surface the evaluator, dir, and geblang check trust) must be
// recognised by the bytecode compiler. Otherwise an import valid on the
// evaluator and accepted by the analyzer would be rejected by the VM - a
// backend/tooling divergence of the kind R2's guards close for builtins
// and primitive methods.
func TestNativeModulesRecognizedByVM(t *testing.T) {
	for module := range evaluator.NativeModuleSymbols() {
		src := "import " + module + ";\n"
		if err := compileOnVM(src); isUnsupportedModuleErr(err) {
			t.Errorf("VM compiler does not recognise canonical native module %q: %v", module, err)
		}
	}
}
