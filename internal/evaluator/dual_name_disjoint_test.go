package evaluator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/native"
	"geblang/internal/parser"
)

// TestDualNameModuleSurfacesDisjoint enforces the documented dual-name
// contract (07-modules-packages.md): a stdlib .gb module sharing its
// canonical name with a native module must not export a member that
// also exists on the native surface - an overlapping name resolves
// native-first on the evaluator and stdlib-first on the VM (known
// divergence 12), so disjointness is what keeps the divergence latent.
func TestDualNameModuleSurfacesDisjoint(t *testing.T) {
	stdlibDir := filepath.Join("..", "..", "stdlib")
	if _, err := os.Stat(stdlibDir); err != nil {
		t.Skipf("stdlib dir not found: %v", err)
	}
	nativeSurfaces := NativeModuleSymbols()

	var checked int
	err := filepath.Walk(stdlibDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".gb") {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		p := parser.New(lexer.New(string(source)))
		program := p.ParseProgram()
		if len(p.Errors()) != 0 {
			t.Errorf("%s: parse errors: %v", path, p.Errors())
			return nil
		}
		moduleName := declaredModuleName(program)
		if moduleName == "" {
			return nil
		}
		if !native.IsNativeModule(moduleName) {
			return nil
		}
		nativeSet := nativeSurfaces[moduleName]
		if len(nativeSet) == 0 {
			return nil
		}
		checked++
		for _, name := range exportedMemberNames(program) {
			if _, clash := nativeSet[name]; clash {
				t.Errorf("%s: export %q collides with the native %s.%s (dual-name surfaces must be disjoint; rename or use the *native split)",
					path, name, moduleName, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no dual-name stdlib modules found; walker or naming broke")
	}
}

func declaredModuleName(program *ast.Program) string {
	for _, stmt := range program.Statements {
		if m, ok := stmt.(*ast.ModuleStatement); ok {
			return strings.Join(m.Path, ".")
		}
	}
	return ""
}

func exportedMemberNames(program *ast.Program) []string {
	var names []string
	for _, stmt := range program.Statements {
		export, ok := stmt.(*ast.ExportStatement)
		if !ok {
			continue
		}
		switch inner := export.Statement.(type) {
		case *ast.FunctionStatement:
			if inner.Name != nil {
				names = append(names, inner.Name.Value)
			}
		case *ast.ClassStatement:
			if inner.Name != nil {
				names = append(names, inner.Name.Value)
			}
		case *ast.DeclarationStatement:
			if inner.Name != nil {
				names = append(names, inner.Name.Value)
			}
		}
	}
	return names
}
