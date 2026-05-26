package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"geblang/internal/bundle"
	"geblang/internal/bytecode"
	"geblang/internal/check"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/parser"
)

func runBuild(args []string) {
	var entry, outPath, pkgDir string
	pkgDir = "."

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--entry":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "geblang build: --entry requires a value")
				os.Exit(2)
			}
			i++
			entry = args[i]
		case "--out":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "geblang build: --out requires a value")
				os.Exit(2)
			}
			i++
			outPath = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "geblang build: unknown flag %s\n", args[i])
				os.Exit(2)
			}
			pkgDir = args[i]
		}
	}

	if entry == "" {
		fmt.Fprintln(os.Stderr, "geblang build: --entry is required")
		fmt.Fprintln(os.Stderr, "usage: geblang build --entry module.name --out <path> [<package-dir>]")
		os.Exit(2)
	}
	if outPath == "" {
		fmt.Fprintln(os.Stderr, "geblang build: --out is required")
		fmt.Fprintln(os.Stderr, "usage: geblang build --entry module.name --out <path> [<package-dir>]")
		os.Exit(2)
	}

	absPkgDir, err := filepath.Abs(pkgDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "geblang build: %v\n", err)
		os.Exit(1)
	}

	resolver := modules.NewResolver([]string{absPkgDir})

	entryPath, err := resolver.Resolve(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "geblang build: cannot resolve entry module %q: %v\n", entry, err)
		os.Exit(1)
	}

	allModules, err := bundle.WalkImports(entry, entryPath, resolver, check.IsNativeImport)
	if err != nil {
		fmt.Fprintf(os.Stderr, "geblang build: %v\n", err)
		os.Exit(1)
	}

	stdlibDirs := modules.DefaultStdlibPaths()

	files := map[string][]byte{}
	var records []bundle.ModuleRecord

	for canonical, absPath := range allModules {
		src, err := os.ReadFile(absPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "geblang build: read %s: %v\n", absPath, err)
			os.Exit(1)
		}

		isStdlib := false
		for _, sd := range stdlibDirs {
			if strings.HasPrefix(absPath, sd+string(os.PathSeparator)) {
				isStdlib = true
				break
			}
		}

		prefix := "src"
		if isStdlib {
			prefix = "stdlib"
		}

		relPath := strings.ReplaceAll(canonical, ".", "/") + ".gb"
		zipSrcPath := prefix + "/" + relPath
		files[zipSrcPath] = src

		p := parser.New(lexer.New(string(src)))
		prog := p.ParseProgram()
		if len(p.Errors()) == 0 {
			chunk, err := bytecode.Compile(prog, src, version)
			if err == nil {
				encoded, err := bytecode.Encode(chunk)
				if err == nil {
					files[prefix+"/"+strings.ReplaceAll(canonical, ".", "/")+".gbc"] = encoded
				} else {
					fmt.Fprintf(os.Stderr, "geblang build: warn: encode %s: %v\n", canonical, err)
				}
			} else {
				fmt.Fprintf(os.Stderr, "geblang build: warn: compile %s: %v\n", canonical, err)
			}
		}

		records = append(records, bundle.ModuleRecord{
			Canonical:  canonical,
			SourcePath: zipSrcPath,
			SourceHash: bundle.SourceHash(src),
			IsStdlib:   isStdlib,
		})
	}

	manifest := bundle.Manifest{
		Version: version,
		Entry:   entry,
		Modules: records,
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "geblang build: get executable: %v\n", err)
		os.Exit(1)
	}
	exeData, err := os.ReadFile(exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "geblang build: read executable: %v\n", err)
		os.Exit(1)
	}

	absOut, err := filepath.Abs(outPath)
	if err != nil {
		absOut = outPath
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "geblang build: create output dir: %v\n", err)
		os.Exit(1)
	}

	outFile, err := os.Create(absOut)
	if err != nil {
		fmt.Fprintf(os.Stderr, "geblang build: create output: %v\n", err)
		os.Exit(1)
	}

	if _, err := outFile.Write(exeData); err != nil {
		outFile.Close()
		fmt.Fprintf(os.Stderr, "geblang build: write executable: %v\n", err)
		os.Exit(1)
	}

	if err := bundle.Write(outFile, manifest, files); err != nil {
		outFile.Close()
		fmt.Fprintf(os.Stderr, "geblang build: write bundle: %v\n", err)
		os.Exit(1)
	}

	if err := outFile.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "geblang build: close output: %v\n", err)
		os.Exit(1)
	}

	if err := os.Chmod(absOut, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "geblang build: chmod: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "built %s\n", absOut)
}

func runBundled(b *bundle.Bundle) int {
	hash := b.Hash()
	tempDir := filepath.Join(os.TempDir(), "geblang-"+hash)

	if err := b.ExtractTo(tempDir, version, bytecodeCacheDir()); err != nil {
		fmt.Fprintf(os.Stderr, "geblang: bundle extract: %v\n", err)
		return 1
	}

	stdlibDir := filepath.Join(tempDir, "stdlib")
	if info, err := os.Stat(stdlibDir); err == nil && info.IsDir() {
		if err := os.Setenv("GEBLANG_STDLIB", stdlibDir); err != nil {
			fmt.Fprintf(os.Stderr, "geblang: set GEBLANG_STDLIB: %v\n", err)
			return 1
		}
	}

	return runBundledEntry(b.Manifest.Entry, os.Args[1:], filepath.Join(tempDir, "src"))
}

func runBundledEntry(entry string, args []string, srcDir string) int {
	source := []byte(fmt.Sprintf(`import sys;
import %s as __geb_module;

let __geb_result = __geb_module.main(sys.args());
if (__geb_result != null) {
    sys.exit(__geb_result as int);
}
`, entry))

	program, err := parseAndAnalyze(string(source))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	sourcePath := filepath.Join(srcDir, "__geblang_bundle__.gb")
	exitCode, err := runScript(sourcePath, args, source, program, executionAuto, os.Stdout, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return exitCode
}
