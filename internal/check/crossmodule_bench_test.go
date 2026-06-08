package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/parser"
	"geblang/internal/semantic"
)

// setupCrossModuleProgram writes n importable modules (each exporting a class
// and a function) plus a main program that imports all of them and references
// a member of each (a qualified-type annotation + a cross-module call). This
// is the work the cross-module checks must resolve. Returns the main file
// path, its source, and a resolver rooted at the module dir.
func setupCrossModuleProgram(b *testing.B, n int) (string, string, *modules.Resolver) {
	dir := b.TempDir()
	var imports, uses strings.Builder
	for i := 0; i < n; i++ {
		src := fmt.Sprintf("module mod%d;\nexport class C%d {}\nexport func f%d(): int { return %d; }\n", i, i, i, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("mod%d.gb", i)), []byte(src), 0644); err != nil {
			b.Fatal(err)
		}
		imports.WriteString(fmt.Sprintf("import mod%d;\n", i))
		uses.WriteString(fmt.Sprintf("func use%d(mod%d.C%d x): int { return mod%d.f%d(); }\n", i, i, i, i, i))
	}
	main := imports.String() + uses.String() + "let done = 1;\n"
	mainPath := filepath.Join(dir, "main.gb")
	if err := os.WriteFile(mainPath, []byte(main), 0644); err != nil {
		b.Fatal(err)
	}
	return mainPath, main, modules.NewResolver([]string{dir})
}

// benchAnalyze measures one compile-path analysis of an n-import program.
// crossModule=false is the pre-Phase-1 baseline (single-file analyzer, no
// resolver); crossModule=true is the new path (fresh ModuleCache each call,
// matching production, so the imported modules are re-parsed per compile).
// The delta between the two at the same n is the cross-module overhead.
func benchAnalyze(b *testing.B, n int, crossModule bool) {
	mainPath, mainSrc, resolver := setupCrossModuleProgram(b, n)
	native := evaluator.NativeModuleSymbols()
	program := parser.New(lexer.New(mainSrc)).ParseProgram()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if crossModule {
			CrossModuleAnalysis(mainPath, program, Options{
				Resolver:      resolver,
				CrossModule:   true,
				NativeSymbols: native,
				ModuleCache:   NewModuleCache(),
			})
		} else {
			semantic.New().Analyze(program)
		}
	}
}

func BenchmarkAnalyzeSingleFile(b *testing.B) {
	for _, n := range []int{0, 1, 8, 32} {
		b.Run(fmt.Sprintf("imports=%d", n), func(b *testing.B) { benchAnalyze(b, n, false) })
	}
}

func BenchmarkAnalyzeCrossModule(b *testing.B) {
	for _, n := range []int{0, 1, 8, 32} {
		b.Run(fmt.Sprintf("imports=%d", n), func(b *testing.B) { benchAnalyze(b, n, true) })
	}
}

// setupImportsOnly writes n modules and a main that imports them all but
// references no cross-module member. Isolates the cost of merely having
// imports (graph building) from the cost of resolving referenced members.
func setupImportsOnly(b *testing.B, n int) (string, string, *modules.Resolver) {
	dir := b.TempDir()
	var imports strings.Builder
	for i := 0; i < n; i++ {
		src := fmt.Sprintf("module mod%d;\nexport class C%d {}\nexport func f%d(): int { return %d; }\n", i, i, i, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("mod%d.gb", i)), []byte(src), 0644); err != nil {
			b.Fatal(err)
		}
		imports.WriteString(fmt.Sprintf("import mod%d;\n", i))
	}
	main := imports.String() + "let done = 1;\n"
	mainPath := filepath.Join(dir, "main.gb")
	if err := os.WriteFile(mainPath, []byte(main), 0644); err != nil {
		b.Fatal(err)
	}
	return mainPath, main, modules.NewResolver([]string{dir})
}

func BenchmarkAnalyzeCrossModuleImportsOnly(b *testing.B) {
	native := evaluator.NativeModuleSymbols()
	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("imports=%d", n), func(b *testing.B) {
			mainPath, mainSrc, resolver := setupImportsOnly(b, n)
			program := parser.New(lexer.New(mainSrc)).ParseProgram()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				CrossModuleAnalysis(mainPath, program, Options{
					Resolver: resolver, CrossModule: true, NativeSymbols: native, ModuleCache: NewModuleCache(),
				})
			}
		})
	}
}

// BenchmarkAnalyzeCrossModuleWarmCache reuses one ModuleCache across calls,
// modeling a shared cache where imported module sources are parsed once.
// The delta vs the fresh-cache benchmark is the re-parsing cost a shared
// cache would eliminate.
func BenchmarkAnalyzeCrossModuleWarmCache(b *testing.B) {
	native := evaluator.NativeModuleSymbols()
	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("imports=%d", n), func(b *testing.B) {
			mainPath, mainSrc, resolver := setupCrossModuleProgram(b, n)
			program := parser.New(lexer.New(mainSrc)).ParseProgram()
			cache := NewModuleCache()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				CrossModuleAnalysis(mainPath, program, Options{
					Resolver: resolver, CrossModule: true, NativeSymbols: native, ModuleCache: cache,
				})
			}
		})
	}
}
