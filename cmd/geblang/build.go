package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"geblang/internal/bundle"
	"geblang/internal/bytecode"
	"geblang/internal/check"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/notices"
	"geblang/internal/parser"
)

func runBuild(args []string) {
	var entry, outPath, pkgDir string
	pkgDir = "."
	var extraResources []resourceSpec

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
		case "--resource":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "geblang build: --resource requires a value")
				os.Exit(2)
			}
			i++
			extraResources = append(extraResources, parseResourceSpec(args[i]))
		case "--no-assert":
			bytecode.AssertionsDisabled = true
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
			chunk, err := bytecode.CompileWithOptions(prog, src, version, bytecode.CompileOptions{NativeSymbols: evaluator.CachedNativeModuleSymbols()})
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

	specs := append([]resourceSpec(nil), extraResources...)
	resourceRoot := absPkgDir
	appName := entry
	appVersion := ""
	if pkgManifest, err := resolver.FindManifest(absPkgDir); err == nil && pkgManifest != nil {
		resourceRoot = pkgManifest.Root
		for _, pattern := range pkgManifest.Resources {
			specs = append(specs, resourceSpec{src: pattern})
		}
		if pkgManifest.Name != "" {
			appName = pkgManifest.Name
		}
		appVersion = pkgManifest.Version
	}
	if len(specs) > 0 {
		resources, err := collectResources(resourceRoot, specs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "geblang build: collect resources: %v\n", err)
			os.Exit(1)
		}
		for zipPath, data := range resources {
			files[zipPath] = data
		}
	}

	manifest := bundle.Manifest{
		Version:    version,
		Entry:      entry,
		Name:       appName,
		AppVersion: appVersion,
		Modules:    records,
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

	// Sidecar NOTICES keeps distribution dirs licence-compliant even when
	// nobody runs the binary's --notices flag.
	noticesPath := absOut + ".NOTICES.txt"
	if err := os.WriteFile(noticesPath, []byte(notices.Text), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "geblang build: warn: write notices: %v\n", err)
	}

	fmt.Fprintf(os.Stdout, "built %s\n", absOut)
	fmt.Fprintf(os.Stdout, "wrote %s\n", noticesPath)
}

func runBundled(b *bundle.Bundle) int {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "--":
			args = args[1:]
		case "--help", "-h":
			printBundledHelp(b)
			return 0
		case "--version":
			printBundledVersion(b)
			return 0
		case "--notices", "--licences", "--licenses":
			fmt.Print(licenseText)
			return 0
		}
	}
	return runBundledWithArgs(b, args)
}

func bundledAppName(b *bundle.Bundle) string {
	if b.Manifest.Name != "" {
		return b.Manifest.Name
	}
	return b.Manifest.Entry
}

func printBundledVersion(b *bundle.Bundle) {
	name := bundledAppName(b)
	if b.Manifest.AppVersion != "" {
		fmt.Printf("%s %s (geblang %s)\n", name, b.Manifest.AppVersion, version)
		return
	}
	fmt.Printf("%s (geblang %s)\n", name, version)
}

func printBundledHelp(b *bundle.Bundle) {
	bin := filepath.Base(os.Args[0])
	printBundledVersion(b)
	fmt.Printf(`
usage: %s [args...]

Standard flags (recognised only as the first argument):
  --help, -h     show this help
  --version      show the application and runtime version
  --notices      print third-party licence notices
  --             pass everything after it to the application untouched

All other arguments are passed to the application.
`, bin)
}

func runBundledWithArgs(b *bundle.Bundle, args []string) int {
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

	if err := os.Setenv("GEBLANG_BUNDLE_DIR", tempDir); err != nil {
		fmt.Fprintf(os.Stderr, "geblang: set GEBLANG_BUNDLE_DIR: %v\n", err)
		return 1
	}

	return runBundledEntry(b.Manifest.Entry, args, filepath.Join(tempDir, "src"))
}

func runBundledEntry(entry string, args []string, srcDir string) int {
	source := []byte(fmt.Sprintf(`import sys;
import %s as __geb_module;

let __geb_result = __geb_module.main(sys.args());
if (__geb_result != null) {
    sys.exit(__geb_result as int);
}
`, entry))

	sourcePath := filepath.Join(srcDir, "__geblang_bundle__.gb")
	program, err := parseAndAnalyze(sourcePath, string(source))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	exitCode, err := runScript(sourcePath, args, source, program, executionAuto, nil, os.Stdout, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return exitCode
}

// resourceSpec is one bundle resource: a source path or glob (relative to the
// project root), with an optional bundle destination. When dest is empty the
// file keeps its project-relative path, so dev and bundled reads share a path.
// A non-empty dest remaps the source there (a dir's contents mirror under dest,
// a single file lands at dest), letting a build stage processed copies without
// disturbing the source tree.
type resourceSpec struct {
	src  string
	dest string
}

// parseResourceSpec parses a --resource value of the form "src" or "src=dest".
func parseResourceSpec(arg string) resourceSpec {
	if i := strings.Index(arg, "="); i >= 0 {
		return resourceSpec{src: arg[:i], dest: filepath.ToSlash(arg[i+1:])}
	}
	return resourceSpec{src: arg}
}

// collectResources resolves resource specs into bundle ZIP entries keyed by
// their bundle path. Directories embed recursively; otherwise the source is
// treated as a glob.
func collectResources(root string, specs []resourceSpec) (map[string][]byte, error) {
	out := map[string][]byte{}

	put := func(bundlePath, diskPath string) error {
		bundlePath = filepath.ToSlash(bundlePath)
		if bundlePath == ".." || strings.HasPrefix(bundlePath, "../") {
			return fmt.Errorf("resource %q maps outside the bundle", diskPath)
		}
		if bundlePath == "src" || strings.HasPrefix(bundlePath, "src/") || bundlePath == "stdlib" || strings.HasPrefix(bundlePath, "stdlib/") {
			return fmt.Errorf("resource path %q collides with a reserved bundle directory", bundlePath)
		}
		data, err := os.ReadFile(diskPath)
		if err != nil {
			return err
		}
		out[bundlePath] = data
		return nil
	}

	// bundlePathFor maps a matched disk path to its bundle path. base is the
	// directory the match is relative to (the spec's source dir, or root for a
	// bare file/glob); dest, when set, replaces that base prefix.
	bundlePathFor := func(diskPath, base, dest string) (string, error) {
		rel, err := filepath.Rel(base, diskPath)
		if err != nil {
			return "", err
		}
		rel = filepath.ToSlash(rel)
		if dest != "" {
			if rel == "." {
				return dest, nil
			}
			return dest + "/" + rel, nil
		}
		projRel, err := filepath.Rel(root, diskPath)
		if err != nil {
			return "", err
		}
		projRel = filepath.ToSlash(projRel)
		if projRel == ".." || strings.HasPrefix(projRel, "../") {
			return "", fmt.Errorf("resource %q is outside the project directory", diskPath)
		}
		return projRel, nil
	}

	addPath := func(diskPath, dest string) error {
		info, err := os.Stat(diskPath)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return filepath.WalkDir(diskPath, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				bundlePath, err := bundlePathFor(p, diskPath, dest)
				if err != nil {
					return err
				}
				return put(bundlePath, p)
			})
		}
		if dest != "" {
			return put(dest, diskPath)
		}
		bundlePath, err := bundlePathFor(diskPath, root, "")
		if err != nil {
			return err
		}
		return put(bundlePath, diskPath)
	}

	for _, spec := range specs {
		if spec.src == "" {
			continue
		}
		abs := spec.src
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, spec.src)
		}
		if _, err := os.Stat(abs); err == nil {
			if err := addPath(abs, spec.dest); err != nil {
				return nil, err
			}
			continue
		}
		matches, err := filepath.Glob(abs)
		if err != nil {
			return nil, fmt.Errorf("resource pattern %q: %w", spec.src, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("resource pattern %q matched no files", spec.src)
		}
		for _, m := range matches {
			dest := spec.dest
			if dest != "" {
				dest = dest + "/" + filepath.Base(m)
			}
			if err := addPath(m, dest); err != nil {
				return nil, err
			}
		}
	}

	return out, nil
}
