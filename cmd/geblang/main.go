package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"geblang/internal/ast"
	"geblang/internal/bundle"
	"geblang/internal/bytecode"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/parser"
	"geblang/internal/runtime"
	"geblang/internal/semantic"
)

const version = "1.0.1"
const bannerString = "Geblang Version %s, ©2026 David Gebler.\n==========================================\n"

type executionMode int

const (
	executionAuto executionMode = iota
	executionEvaluatorOnly
	executionVMStrict
)

func main() {
	if b, err := bundle.OpenFromExecutable(); err == nil && b != nil {
		os.Exit(runBundled(b))
	}

	if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Fprintf(os.Stdout, bannerString, version)
		topic := ""
		if len(os.Args) > 2 {
			topic = os.Args[2]
		}
		if !printHelp(os.Stdout, topic) {
			fmt.Fprintf(os.Stderr, "unknown help topic %s\n", topic)
			os.Exit(2)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Fprintf(os.Stdout, "geblang %s\n", version)
		return
	}
	if len(os.Args) > 2 && isHelpArg(os.Args[2]) {
		topic := os.Args[1]
		if topic == "--module" || topic == "-m" {
			topic = "module"
		}
		if printHelp(os.Stdout, topic) {
			return
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "test" {
		runTests(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "cache" {
		runCache(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "check" {
		runCheck(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "init" {
		runInit(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		runDoctor(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "doc" {
		runDoc(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "install" {
		runInstall(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "fmt" {
		runFmt(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "dap" {
		runDap(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "lsp" {
		runLsp(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "build" {
		runBuild(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "-m" || os.Args[1] == "--module") {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: geblang -m <module> [args...]")
			os.Exit(2)
		}
		exitCode, err := runModule(os.Args[2], os.Args[3:], executionAuto, false, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return
	}
	if len(os.Args) == 1 {
		os.Exit(runREPL(os.Stdin, os.Stdout, os.Stderr, replConfig{}))
	}
	if len(os.Args) > 1 && os.Args[1] == "repl" {
		mode := executionEvaluatorOnly
		traceExec := false
		args := os.Args[2:]
		for len(args) > 0 {
			switch args[0] {
			case "--disable-vm":
				mode = executionEvaluatorOnly
				args = args[1:]
			case "--vm-strict", "--vm":
				mode = executionVMStrict
				args = args[1:]
			case "--trace-exec":
				traceExec = true
				args = args[1:]
			default:
				fmt.Fprintf(os.Stderr, "unknown repl option %s\n", args[0])
				os.Exit(2)
			}
		}
		os.Exit(runREPL(os.Stdin, os.Stdout, os.Stderr, replConfig{mode: mode, traceExec: traceExec}))
	}

	mode := executionAuto
	traceExec := false
	moduleName := ""
	args := os.Args[1:]
	for len(args) > 0 {
		switch args[0] {
		case "--disable-vm":
			mode = executionEvaluatorOnly
			args = args[1:]
		case "--vm-strict", "--vm":
			mode = executionVMStrict
			args = args[1:]
		case "--trace-exec":
			traceExec = true
			args = args[1:]
		case "-m", "--module":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: geblang -m <module> [args...]")
				os.Exit(2)
			}
			moduleName = args[1]
			args = args[2:]
			goto doneFlags
		default:
			goto doneFlags
		}
	}
doneFlags:

	if moduleName != "" {
		exitCode, err := runModule(moduleName, args, mode, traceExec, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return
	}

	if len(args) < 1 {
		printUsage(os.Stderr)
		os.Exit(2)
	}

	source, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", args[0], err)
		os.Exit(1)
	}

	p := parser.New(lexer.New(string(source)))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		for _, msg := range p.Errors() {
			fmt.Fprintln(os.Stderr, msg)
		}
		os.Exit(1)
	}
	if diagnostics := semantic.New().Analyze(program); len(diagnostics) > 0 {
		hasError := false
		for _, diagnostic := range diagnostics {
			prefix := "error: "
			if diagnostic.Severity == semantic.SeverityWarning {
				prefix = "warning: "
			} else {
				hasError = true
			}
			fmt.Fprintf(os.Stderr, "%s%s\n", prefix, diagnostic.Message)
		}
		// Errors abort before any execution. Warnings still print but
		// don't block - the program runs normally.
		if hasError {
			os.Exit(1)
		}
	}

	exitCode, err := runScript(args[0], args[1:], source, program, mode, os.Stdout, traceWriter(traceExec, os.Stderr))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Usage: geblang <command> [arguments...]")
	fmt.Fprintln(writer, "       geblang <script.gb> [args...]    run a script directly (shorthand)")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Running code:")
	fmt.Fprintln(writer, "  geblang <script.gb> [args...]      run a script with the bytecode VM (fallback to evaluator on unsupported constructs)")
	fmt.Fprintln(writer, "  geblang --disable-vm <script.gb>   run a script with the tree-walking evaluator")
	fmt.Fprintln(writer, "  geblang --vm-strict <script.gb>    run a script with the VM only, failing instead of falling back")
	fmt.Fprintln(writer, "  geblang --trace-exec <script.gb>   print which engine handled the script")
	fmt.Fprintln(writer, "  geblang -m <module> [args...]      run the named module's exported main()")
	fmt.Fprintln(writer, "  geblang repl                       start the interactive REPL")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Project workflow:")
	fmt.Fprintln(writer, "  geblang init [--name n] [--source d]  scaffold a geblang.yaml manifest in the current directory")
	fmt.Fprintln(writer, "  geblang install [git-url[@ver]]    install a package or all manifest dependencies")
	fmt.Fprintln(writer, "  geblang build --entry m --out p    bundle the project into a single self-contained binary")
	fmt.Fprintln(writer, "  geblang fmt <file.gb> [...]        format files in place (use --stdin for piped input)")
	fmt.Fprintln(writer, "  geblang check [--strict] <path>    parse + lint without executing")
	fmt.Fprintln(writer, "  geblang test [--tag n] [-v] <p>    discover and run *_test.gb files")
	fmt.Fprintln(writer, "  geblang doc [--format m|json] <p>  extract API documentation from doc comments")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "IDE / tooling integration:")
	fmt.Fprintln(writer, "  geblang lsp                        start the Language Server Protocol server (stdio)")
	fmt.Fprintln(writer, "  geblang dap                        start the Debug Adapter Protocol server (stdio)")
	fmt.Fprintln(writer, "  geblang doctor [--json]            inspect the local install for common setup issues")
	fmt.Fprintln(writer, "  geblang cache clean                purge the bytecode cache")
	fmt.Fprintln(writer, "  geblang cache stats [--json]       report bytecode cache size and entries")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Help:")
	fmt.Fprintln(writer, "  geblang help [topic]               show detailed help for a command (topic == repl, run, build, ...)")
	fmt.Fprintln(writer, "  geblang --version                  print the Geblang version and exit")
}

func isHelpArg(arg string) bool {
	return arg == "--help" || arg == "-help" || arg == "-h" || arg == "help"
}

func printHelp(writer io.Writer, topic string) bool {
	switch strings.ToLower(topic) {
	case "":
		printUsage(writer)
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Run geblang with no arguments to start the REPL.")
		fmt.Fprintln(writer, "Use `geblang help <topic>` or `geblang <command> --help` for command details.")
		fmt.Fprintln(writer, "Topics: repl, run, module, build, install, fmt, lsp, dap, test, check, init, doctor, doc, cache")
		return true
	case "repl":
		fmt.Fprintln(writer, "usage: geblang repl [--disable-vm|--vm-strict|--trace-exec]")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Starts an interactive Geblang session. REPL commands include:")
		fmt.Fprintln(writer, "  :help, :quit, :reset, :load <file>, :vars, :imports, :stdlib, :modules, :members <module>, :mode, :history")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang repl")
		fmt.Fprintln(writer, "  geblang repl --vm-strict")
		return true
	case "run":
		fmt.Fprintln(writer, "usage: geblang [--disable-vm|--vm-strict|--trace-exec] <script.gb> [args...]")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Runs a script. VM execution is attempted by default and falls back to the evaluator when needed.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang app.gb")
		fmt.Fprintln(writer, "  geblang --vm-strict app.gb --port 8080")
		fmt.Fprintln(writer, "  geblang --trace-exec app.gb")
		return true
	case "build":
		fmt.Fprintln(writer, "usage: geblang build --entry module.name --out <path> [<package-dir>]")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Produces a self-contained executable from a Geblang package.")
		fmt.Fprintln(writer, "The output binary bundles reachable source files, precompiled bytecode,")
		fmt.Fprintln(writer, "source stdlib modules, and imported vendored package dependencies.")
		fmt.Fprintln(writer, "Running the output binary requires no separate geblang installation.")
		fmt.Fprintln(writer, "--entry  canonical module name whose main() is the entry point (required)")
		fmt.Fprintln(writer, "--out    output file path (required)")
		fmt.Fprintln(writer, "<package-dir>  package root directory containing geblang.yaml (default: .)")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang build --entry app.main --out build/app")
		fmt.Fprintln(writer, "  geblang build --entry app.main --out build/app ./packages/app")
		return true
	case "install":
		fmt.Fprintln(writer, "usage: geblang install")
		fmt.Fprintln(writer, "       geblang install <git-url>[@version] [<name>]")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Without arguments, fetches all git dependencies declared in geblang.yaml")
		fmt.Fprintln(writer, "into vendor/ and pins their commit SHAs in geblang.lock.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "With a git URL, adds the dependency to geblang.yaml and fetches it.")
		fmt.Fprintln(writer, "The package name is derived from the URL's last path segment unless")
		fmt.Fprintln(writer, "an explicit <name> is provided.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "geblang.lock should be committed to source control for reproducible builds.")
		fmt.Fprintln(writer, "vendor/ may be gitignored and regenerated with geblang install.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang install")
		fmt.Fprintln(writer, "  geblang install https://github.com/acme/tools.git")
		fmt.Fprintln(writer, "  geblang install https://github.com/acme/tools.git@v1.2.0 acme.tools")
		return true
	case "fmt":
		fmt.Fprintln(writer, "usage: geblang fmt <file.gb> [...]")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Formats Geblang source files in place using canonical style:")
		fmt.Fprintln(writer, "  4-space indentation, braces on the same line, blank lines between top-level declarations.")
		fmt.Fprintln(writer, "Files with syntax errors are reported and left unchanged.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang fmt src/main.gb")
		fmt.Fprintln(writer, "  geblang fmt src/*.gb")
		return true
	case "lsp":
		fmt.Fprintln(writer, "usage: geblang lsp")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Starts a Language Server Protocol (LSP) server on stdin/stdout.")
		fmt.Fprintln(writer, "The server publishes parse and semantic diagnostics on open/change.")
		fmt.Fprintln(writer, "Intended to be launched by an IDE extension (e.g. vscode-geblang).")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang lsp")
		return true
	case "dap":
		fmt.Fprintln(writer, "usage: geblang dap")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Starts a Debug Adapter Protocol (DAP) server on stdin/stdout.")
		fmt.Fprintln(writer, "Supports breakpoints, step over/into/out, and variable inspection.")
		fmt.Fprintln(writer, "Intended to be launched by an IDE debug configuration (e.g. vscode-geblang).")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang dap")
		return true
	case "module", "-m":
		fmt.Fprintln(writer, "usage: geblang -m <module> [args...]")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Resolves a module and invokes its exported main(args) function without a wrapper script.")
		fmt.Fprintln(writer, "VM execution is attempted by default and falls back to the evaluator when needed.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang -m http.server 8080")
		fmt.Fprintln(writer, "  geblang --vm-strict -m app.main --verbose")
		return true
	case "test":
		fmt.Fprintln(writer, "usage: geblang test [--tag name] [--verbose|-v] <file-or-dir>")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Runs Geblang test files. Directory paths discover *_test.gb files recursively.")
		fmt.Fprintln(writer, "--tag name runs only tests decorated with the given tag. Repeat to include multiple tags.")
		fmt.Fprintln(writer, "--verbose / -v prints each test class and method with PASS/FAIL status (testdox-style).")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang test tests/user_test.gb")
		fmt.Fprintln(writer, "  geblang test tests/")
		fmt.Fprintln(writer, "  geblang test --tag integration tests/")
		fmt.Fprintln(writer, "  geblang test --verbose tests/")
		return true
	case "check":
		fmt.Fprintln(writer, "usage: geblang check [--json] [--no-lint] [--strict] <file-or-dir>")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Parses, semantically checks, and lints Geblang files without executing them.")
		fmt.Fprintln(writer, "--json writes a structured report for CI and editor tooling.")
		fmt.Fprintln(writer, "--no-lint disables lint diagnostics. --strict exits non-zero on warnings.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang check src/")
		fmt.Fprintln(writer, "  geblang check --strict docs/examples/")
		fmt.Fprintln(writer, "  geblang check --json src/main.gb")
		return true
	case "init":
		fmt.Fprintln(writer, "usage: geblang init [--name name] [--source dir] [--force]")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Creates a minimal geblang.yaml package manifest in the current directory.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang init --name acme.api")
		fmt.Fprintln(writer, "  geblang init --name acme.api --source src")
		return true
	case "doctor":
		fmt.Fprintln(writer, "usage: geblang doctor [--json]")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Reports local Geblang tooling, manifest, and bytecode cache state.")
		fmt.Fprintln(writer, "--json writes a structured report for CI and editor tooling.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang doctor")
		fmt.Fprintln(writer, "  geblang doctor --json")
		return true
	case "doc":
		fmt.Fprintln(writer, "usage: geblang doc [--format markdown|json] [--out file] <file-or-dir>")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Generates source API documentation from Geblang declarations and docblocks.")
		fmt.Fprintln(writer, "When a file uses export, only exported declarations are included.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang doc src/")
		fmt.Fprintln(writer, "  geblang doc --format json src/")
		fmt.Fprintln(writer, "  geblang doc --out build/api.md src/")
		return true
	case "cache":
		fmt.Fprintln(writer, "usage: geblang cache clean")
		fmt.Fprintln(writer, "       geblang cache stats [--json]")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Inspects or removes cached bytecode chunks under .geblang-cache/.")
		fmt.Fprintln(writer, "--json writes structured cache stats.")
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Examples:")
		fmt.Fprintln(writer, "  geblang cache stats")
		fmt.Fprintln(writer, "  geblang cache stats --json")
		fmt.Fprintln(writer, "  geblang cache clean")
		return true
	default:
		return false
	}
}

func runDoctor(args []string) {
	config, err := parseDoctorArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: geblang doctor [--json]")
		os.Exit(2)
	}
	report, err := collectDoctorReport(exec.LookPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if config.JSON {
		if err := writeDoctorJSON(os.Stdout, report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	writeDoctorReport(os.Stdout, report)
}

type doctorConfig struct {
	JSON bool
}

type doctorReport struct {
	GeblangVersion   string              `json:"geblangVersion"`
	WorkingDirectory string              `json:"workingDirectory"`
	GoPath           string              `json:"goPath,omitempty"`
	GoFound          bool                `json:"goFound"`
	Manifest         *doctorManifest     `json:"manifest,omitempty"`
	ManifestError    string              `json:"manifestError,omitempty"`
	Cache            doctorCacheSnapshot `json:"cache"`
	CacheError       string              `json:"cacheError,omitempty"`
}

type doctorManifest struct {
	Path    string `json:"path"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`
}

type doctorCacheSnapshot struct {
	Root  string `json:"root"`
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
}

func parseDoctorArgs(args []string) (doctorConfig, error) {
	config := doctorConfig{}
	for _, arg := range args {
		switch arg {
		case "--json":
			config.JSON = true
		default:
			return config, fmt.Errorf("unknown doctor option %s", arg)
		}
	}
	return config, nil
}

func collectDoctorReport(lookPath func(string) (string, error)) (doctorReport, error) {
	wd, err := os.Getwd()
	if err != nil {
		return doctorReport{}, err
	}
	report := doctorReport{GeblangVersion: version, WorkingDirectory: wd}
	if path, err := lookPath("go"); err == nil {
		report.GoFound = true
		report.GoPath = path
	}
	resolver := modules.NewResolver([]string{wd})
	manifest, err := resolver.FindManifest(wd)
	if err != nil {
		report.ManifestError = err.Error()
	} else if manifest != nil {
		report.Manifest = &doctorManifest{
			Path:    manifest.Path,
			Name:    manifest.Name,
			Version: manifest.Version,
			Source:  manifest.Source,
		}
	}
	stats, err := bytecodeCacheStats()
	if err != nil {
		report.Cache = doctorCacheSnapshot{Root: bytecodeCacheRoot()}
		report.CacheError = err.Error()
	} else {
		report.Cache = doctorCacheSnapshot{Root: stats.Root, Files: stats.Files, Bytes: stats.Bytes}
	}
	return report, nil
}

func writeDoctorReport(writer io.Writer, report doctorReport) {
	fmt.Fprintf(writer, "geblang: %s\n", report.GeblangVersion)
	fmt.Fprintf(writer, "working directory: %s\n", report.WorkingDirectory)
	if report.GoFound {
		fmt.Fprintf(writer, "go: %s\n", report.GoPath)
	} else {
		fmt.Fprintln(writer, "go: not found")
	}
	if report.ManifestError != "" {
		fmt.Fprintf(writer, "manifest: error: %v\n", report.ManifestError)
	} else if report.Manifest == nil {
		fmt.Fprintln(writer, "manifest: not found")
	} else {
		fmt.Fprintf(writer, "manifest: %s\n", report.Manifest.Path)
		fmt.Fprintf(writer, "package: %s %s\n", valueOrDash(report.Manifest.Name), valueOrDash(report.Manifest.Version))
		fmt.Fprintf(writer, "source: %s\n", valueOrDash(report.Manifest.Source))
	}
	if report.CacheError != "" {
		fmt.Fprintf(writer, "cache: error: %v\n", report.CacheError)
	} else {
		fmt.Fprintf(writer, "cache: %s files=%d bytes=%d\n", report.Cache.Root, report.Cache.Files, report.Cache.Bytes)
	}
}

func writeDoctorJSON(writer io.Writer, report doctorReport) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

type initConfig struct {
	name   string
	source string
	force  bool
}

func runInit(args []string) {
	config, err := parseInitArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: geblang init [--name name] [--source dir] [--force]")
		os.Exit(2)
	}
	path, err := initPackage(config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("created %s\n", path)
}

func parseInitArgs(args []string) (initConfig, error) {
	config := initConfig{source: "src"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 >= len(args) {
				return config, fmt.Errorf("geblang init --name expects a value")
			}
			config.name = args[i+1]
			i++
		case "--source":
			if i+1 >= len(args) {
				return config, fmt.Errorf("geblang init --source expects a directory")
			}
			config.source = args[i+1]
			i++
		case "--force":
			config.force = true
		default:
			return config, fmt.Errorf("unknown init option %s", args[i])
		}
	}
	return config, nil
}

func initPackage(config initConfig) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if config.name == "" {
		config.name = packageNameFromDir(wd)
	}
	if config.name == "" {
		return "", fmt.Errorf("package name is required")
	}
	if config.source == "" {
		config.source = "src"
	}
	manifestPath := filepath.Join(wd, "geblang.yaml")
	if _, err := os.Stat(manifestPath); err == nil && !config.force {
		return "", fmt.Errorf("%s already exists; use --force to overwrite", manifestPath)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(wd, config.source), 0o755); err != nil {
		return "", err
	}
	manifest := fmt.Sprintf("name: %s\nversion: 0.1.0\nsource: %s\npaths: []\ndependencies: {}\n", config.name, config.source)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		return "", err
	}
	return manifestPath, nil
}

func packageNameFromDir(path string) string {
	name := strings.ToLower(filepath.Base(path))
	var b strings.Builder
	lastWasDot := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastWasDot = false
			continue
		}
		if !lastWasDot && b.Len() > 0 {
			b.WriteByte('.')
			lastWasDot = true
		}
	}
	return strings.Trim(b.String(), ".")
}

func runCheck(args []string) {
	config, err := parseCheckArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: geblang check [--json] [--no-lint] [--strict] <file-or-dir>")
		os.Exit(2)
	}
	result, err := checkGeblangPath(config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if config.JSON {
		if err := writeCheckJSON(os.Stdout, result); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	} else {
		for _, diagnostic := range result.Diagnostics {
			fmt.Fprintln(os.Stderr, diagnostic.String())
		}
		if len(result.Diagnostics) == 0 {
			fmt.Printf("checked %d file(s)\n", result.Checked)
		}
	}
	if result.HasErrors() || (config.Strict && result.HasWarnings()) {
		os.Exit(1)
	}
}

type checkConfig struct {
	Path   string
	JSON   bool
	Lint   bool
	Strict bool
}

type checkResult struct {
	Checked     int               `json:"checked"`
	Diagnostics []checkDiagnostic `json:"diagnostics"`
}

type checkDiagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity"`
	Rule     string `json:"rule,omitempty"`
	Message  string `json:"message"`
}

func (d checkDiagnostic) String() string {
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

func (r checkResult) HasErrors() bool {
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

func (r checkResult) HasWarnings() bool {
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Severity == "warning" {
			return true
		}
	}
	return false
}

func parseCheckArgs(args []string) (checkConfig, error) {
	config := checkConfig{Lint: true}
	paths := []string{}
	for _, arg := range args {
		switch arg {
		case "--json":
			config.JSON = true
		case "--no-lint":
			config.Lint = false
		case "--strict":
			config.Strict = true
		default:
			paths = append(paths, arg)
		}
	}
	if len(paths) != 1 {
		return config, fmt.Errorf("geblang check expects one file or directory")
	}
	config.Path = paths[0]
	return config, nil
}

func writeCheckJSON(writer io.Writer, result checkResult) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func checkGeblangPath(config checkConfig) (checkResult, error) {
	files, err := discoverGeblangFiles(config.Path)
	if err != nil {
		return checkResult{}, err
	}
	if len(files) == 0 {
		return checkResult{}, fmt.Errorf("no Geblang files found")
	}
	result := checkResult{Checked: len(files), Diagnostics: []checkDiagnostic{}}
	programs := map[string]*ast.Program{}
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, checkDiagnostic{File: file, Severity: "error", Rule: "read", Message: err.Error()})
			continue
		}
		program, diagnostics := checkGeblangSource(file, string(source), config)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		if program != nil {
			programs[file] = program
		}
	}
	result.Diagnostics = append(result.Diagnostics, checkModuleDeclarations(config, programs)...)
	return result, nil
}

func checkGeblangSource(file, source string, config checkConfig) (*ast.Program, []checkDiagnostic) {
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		diagnostics := make([]checkDiagnostic, 0, len(p.Errors()))
		for _, msg := range p.Errors() {
			diagnostics = append(diagnostics, checkDiagnostic{File: file, Severity: "error", Rule: "parse", Message: msg})
		}
		return nil, diagnostics
	}
	diagnostics := []checkDiagnostic{}
	for _, semanticDiagnostic := range semantic.New().Analyze(program) {
		severity := "error"
		if semanticDiagnostic.Severity == semantic.SeverityWarning {
			severity = "warning"
		}
		diagnostics = append(diagnostics, checkDiagnostic{File: file, Severity: severity, Rule: "semantic", Message: semanticDiagnostic.Message, Line: semanticDiagnostic.Line, Column: semanticDiagnostic.Column})
	}
	if _, compileErr := bytecode.Compile(program, []byte(source), file); compileErr != nil && !isBytecodeParityError(compileErr) {
		diagnostics = append(diagnostics, checkDiagnostic{File: file, Severity: "error", Rule: "type", Message: compileErr.Error()})
	}
	diagnostics = append(diagnostics, checkImports(config, file, program)...)
	if config.Lint {
		diagnostics = append(diagnostics, lintProgram(file, program)...)
	}
	return program, diagnostics
}

// isBytecodeParityError is the cmd/-package alias for
// bytecode.IsParityError. Kept as a thin wrapper so existing callers
// in this file don't need touching while the helper lives in the
// bytecode package, where it can also be reused by the DAP server's
// pre-flight check.
func isBytecodeParityError(err error) bool {
	return bytecode.IsParityError(err)
}

func checkImports(config checkConfig, file string, program *ast.Program) []checkDiagnostic {
	resolver := checkResolver(config, file)
	diagnostics := []checkDiagnostic{}
	for _, stmt := range program.Statements {
		imp, ok := stmt.(*ast.ImportStatement)
		if !ok {
			continue
		}
		canonical := strings.Join(imp.Path, ".")
		if canonical == "" || isNativeImport(canonical) {
			continue
		}
		if _, err := resolver.Resolve(canonical); err != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				File:     file,
				Line:     imp.Token.Line,
				Column:   imp.Token.Column,
				Severity: "error",
				Rule:     "import",
				Message:  fmt.Sprintf("cannot resolve import %s", canonical),
			})
		}
	}
	return diagnostics
}

type moduleDeclaration struct {
	name   string
	file   string
	line   int
	column int
}

func checkModuleDeclarations(config checkConfig, programs map[string]*ast.Program) []checkDiagnostic {
	diagnostics := []checkDiagnostic{}
	declared := map[string][]moduleDeclaration{}
	files := make([]string, 0, len(programs))
	for file := range programs {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		program := programs[file]
		modules := []moduleDeclaration{}
		for index, stmt := range program.Statements {
			moduleStmt, ok := stmt.(*ast.ModuleStatement)
			if !ok {
				continue
			}
			name := strings.Join(moduleStmt.Path, ".")
			decl := moduleDeclaration{name: name, file: file, line: moduleStmt.Token.Line, column: moduleStmt.Token.Column}
			modules = append(modules, decl)
			if index != 0 {
				diagnostics = append(diagnostics, checkDiagnostic{
					File:     file,
					Line:     moduleStmt.Token.Line,
					Column:   moduleStmt.Token.Column,
					Severity: "error",
					Rule:     "module",
					Message:  "module declaration must be the first statement",
				})
			}
		}
		if len(modules) > 1 {
			for _, decl := range modules[1:] {
				diagnostics = append(diagnostics, checkDiagnostic{
					File:     decl.file,
					Line:     decl.line,
					Column:   decl.column,
					Severity: "error",
					Rule:     "module",
					Message:  "file contains more than one module declaration",
				})
			}
		}
		if len(modules) == 0 {
			continue
		}
		decl := modules[0]
		declared[decl.name] = append(declared[decl.name], decl)
		resolved, err := checkResolver(config, file).Resolve(decl.name)
		if err != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				File:     decl.file,
				Line:     decl.line,
				Column:   decl.column,
				Severity: "error",
				Rule:     "module",
				Message:  fmt.Sprintf("cannot resolve declared module %s", decl.name),
			})
			continue
		}
		if !sameFilePath(resolved, file) {
			diagnostics = append(diagnostics, checkDiagnostic{
				File:     decl.file,
				Line:     decl.line,
				Column:   decl.column,
				Severity: "error",
				Rule:     "module",
				Message:  fmt.Sprintf("declared module %s resolves to %s", decl.name, resolved),
			})
		}
	}
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		decls := declared[name]
		if len(decls) < 2 {
			continue
		}
		for _, decl := range decls {
			diagnostics = append(diagnostics, checkDiagnostic{
				File:     decl.file,
				Line:     decl.line,
				Column:   decl.column,
				Severity: "error",
				Rule:     "duplicate-module",
				Message:  fmt.Sprintf("module %s is declared in multiple checked files", name),
			})
		}
	}
	return diagnostics
}

func checkResolver(config checkConfig, file string) *modules.Resolver {
	paths := []string{}
	if config.Path != "" {
		if info, err := os.Stat(config.Path); err == nil && info.IsDir() {
			paths = append(paths, config.Path)
		} else {
			paths = append(paths, filepath.Dir(config.Path))
		}
	}
	paths = append(paths, filepath.Dir(file))
	return modules.NewResolver(paths)
}

func sameFilePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil && rightErr == nil {
		return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func isNativeImport(canonical string) bool {
	_, ok := nativeImportModules[canonical]
	return ok
}

var nativeImportModules = map[string]struct{}{
	"args":        {},
	"async":       {},
	"bytes":       {},
	"cli":         {},
	"collections": {},
	"compress":    {},
	"crypt":       {},
	"csv":         {},
	"datetime":    {},
	"db":          {},
	"dotenv":      {},
	"encoding":    {},
	"errors":      {},
	"ext":         {},
	"freeze":      {},
	"http":        {},
	"io":          {},
	"json":        {},
	"log":         {},
	"markdown":    {},
	"math":        {},
	"metrics":     {},
	"net":         {},
	"path":        {},
	"process":     {},
	"profile":     {},
	"random":      {},
	"re":          {},
	"reflect":     {},
	"schema":      {},
	"secrets":     {},
	"serde":       {},
	"smtp":        {},
	"sys":         {},
	"template":    {},
	"test":        {},
	"time":        {},
	"toml":        {},
	"trace":       {},
	"url":         {},
	"uuid":        {},
	"web":         {},
	"websocket":   {},
	"watch":       {},
	"xml":         {},
	"yaml":        {},
}

func discoverGeblangFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	files := []string{}
	err = filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".geblang-cache" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".gb") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

type lintImport struct {
	name   string
	token  ast.ImportStatement
	used   bool
	module string
}

func lintProgram(file string, program *ast.Program) []checkDiagnostic {
	diagnostics := []checkDiagnostic{}
	imports := map[string]*lintImport{}
	for _, stmt := range program.Statements {
		if imp, ok := stmt.(*ast.ImportStatement); ok {
			name := imp.ModuleName()
			imports[strings.ToLower(name)] = &lintImport{name: name, token: *imp, module: strings.Join(imp.Path, ".")}
		}
	}
	for _, stmt := range program.Statements {
		if _, ok := stmt.(*ast.ImportStatement); ok {
			continue
		}
		lintMarkStatementIdentifiers(stmt, imports)
	}
	for _, imp := range imports {
		if !imp.used {
			diagnostics = append(diagnostics, checkDiagnostic{
				File:     file,
				Line:     imp.token.Token.Line,
				Column:   imp.token.Token.Column,
				Severity: "warning",
				Rule:     "unused-import",
				Message:  fmt.Sprintf("import %s is not used", imp.module),
			})
		}
	}
	for _, stmt := range program.Statements {
		diagnostics = append(diagnostics, lintUnreachableStatement(file, stmt)...)
	}
	return diagnostics
}

func lintMarkStatementIdentifiers(stmt ast.Statement, imports map[string]*lintImport) {
	switch s := stmt.(type) {
	case *ast.ExportStatement:
		lintMarkStatementIdentifiers(s.Statement, imports)
	case *ast.DeclarationStatement:
		lintMarkTypeRef(s.Type, imports)
		lintMarkExpressionIdentifiers(s.Value, imports)
	case *ast.DestructuringStatement:
		lintMarkExpressionIdentifiers(s.Value, imports)
	case *ast.ExpressionStatement:
		lintMarkExpressionIdentifiers(s.Expression, imports)
	case *ast.ReturnStatement:
		lintMarkExpressionIdentifiers(s.Value, imports)
	case *ast.YieldStatement:
		lintMarkExpressionIdentifiers(s.Value, imports)
	case *ast.SimpleStatement:
		lintMarkExpressionIdentifiers(s.Value, imports)
	case *ast.IfStatement:
		lintMarkExpressionIdentifiers(s.Condition, imports)
		lintMarkBlockIdentifiers(s.Consequence, imports)
		for _, clause := range s.ElseIfs {
			lintMarkExpressionIdentifiers(clause.Condition, imports)
			lintMarkBlockIdentifiers(clause.Body, imports)
		}
		lintMarkBlockIdentifiers(s.Alternative, imports)
	case *ast.WhileStatement:
		lintMarkExpressionIdentifiers(s.Condition, imports)
		lintMarkBlockIdentifiers(s.Body, imports)
	case *ast.ForStatement:
		lintMarkStatementIdentifiers(s.Init, imports)
		lintMarkExpressionIdentifiers(s.Condition, imports)
		lintMarkStatementIdentifiers(s.Update, imports)
		lintMarkExpressionIdentifiers(s.Iterable, imports)
		lintMarkExpressionIdentifiers(s.Step, imports)
		lintMarkBlockIdentifiers(s.Body, imports)
	case *ast.FunctionStatement:
		lintMarkDecorators(s.Decorators, imports)
		for _, param := range s.Parameters {
			lintMarkTypeRef(param.Type, imports)
			lintMarkExpressionIdentifiers(param.Default, imports)
		}
		lintMarkTypeRef(s.ReturnType, imports)
		lintMarkBlockIdentifiers(s.Body, imports)
	case *ast.ClassStatement:
		lintMarkDecorators(s.Decorators, imports)
		lintMarkTypeRef(s.Extends, imports)
		for _, typ := range s.Implements {
			lintMarkTypeRef(typ, imports)
		}
		for _, member := range s.Members {
			lintMarkStatementIdentifiers(member, imports)
		}
	case *ast.InterfaceStatement:
		for _, typ := range s.Parents {
			lintMarkTypeRef(typ, imports)
		}
		for _, method := range s.Methods {
			for _, param := range method.Parameters {
				lintMarkTypeRef(param.Type, imports)
				lintMarkExpressionIdentifiers(param.Default, imports)
			}
			lintMarkTypeRef(method.ReturnType, imports)
		}
	case *ast.TryStatement:
		lintMarkBlockIdentifiers(s.Body, imports)
		for _, clause := range s.Catches {
			lintMarkTypeRef(clause.Type, imports)
			lintMarkBlockIdentifiers(clause.Body, imports)
		}
		lintMarkBlockIdentifiers(s.Finally, imports)
	case *ast.EnumStatement:
	case *ast.MatchStatement:
		lintMarkExpressionIdentifiers(s.Expr, imports)
		for _, clause := range s.Cases {
			lintMarkExpressionIdentifiers(clause.Pattern, imports)
			lintMarkExpressionIdentifiers(clause.Guard, imports)
			lintMarkExpressionIdentifiers(clause.Value, imports)
			lintMarkBlockIdentifiers(clause.Body, imports)
		}
	case *ast.InitStatement:
		lintMarkBlockIdentifiers(s.Body, imports)
	}
}

func lintMarkBlockIdentifiers(block *ast.BlockStatement, imports map[string]*lintImport) {
	if block == nil {
		return
	}
	for _, stmt := range block.Statements {
		lintMarkStatementIdentifiers(stmt, imports)
	}
}

func lintMarkDecorators(decorators []ast.Decorator, imports map[string]*lintImport) {
	for _, decorator := range decorators {
		if decorator.Name != nil {
			if imp, ok := imports[strings.ToLower(decorator.Name.Value)]; ok {
				imp.used = true
			}
		}
		for _, arg := range decorator.Arguments {
			lintMarkExpressionIdentifiers(arg.Value, imports)
		}
	}
}

func lintMarkTypeRef(typ *ast.TypeRef, imports map[string]*lintImport) {
	if typ == nil {
		return
	}
	name := typ.Name
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		name = name[:dot]
	}
	if imp, ok := imports[strings.ToLower(name)]; ok {
		imp.used = true
	}
	for _, arg := range typ.Arguments {
		lintMarkTypeRef(arg, imports)
	}
	lintMarkTypeRef(typ.Left, imports)
	lintMarkTypeRef(typ.Right, imports)
}

func lintMarkExpressionIdentifiers(expr ast.Expression, imports map[string]*lintImport) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.Identifier:
		if imp, ok := imports[strings.ToLower(e.Value)]; ok {
			imp.used = true
		}
	case *ast.SpreadExpression:
		lintMarkExpressionIdentifiers(e.Value, imports)
	case *ast.InterpolatedString:
		for _, part := range e.Parts {
			lintMarkExpressionIdentifiers(part, imports)
		}
	case *ast.PrefixExpression:
		lintMarkExpressionIdentifiers(e.Right, imports)
	case *ast.PostfixExpression:
		lintMarkExpressionIdentifiers(e.Left, imports)
	case *ast.InfixExpression:
		lintMarkExpressionIdentifiers(e.Left, imports)
		lintMarkExpressionIdentifiers(e.Right, imports)
	case *ast.AssignmentExpression:
		lintMarkExpressionIdentifiers(e.Left, imports)
		lintMarkExpressionIdentifiers(e.Value, imports)
	case *ast.SelectorExpression:
		lintMarkExpressionIdentifiers(e.Object, imports)
	case *ast.CallExpression:
		lintMarkExpressionIdentifiers(e.Callee, imports)
		for _, arg := range e.Arguments {
			lintMarkExpressionIdentifiers(arg.Value, imports)
		}
	case *ast.IndexExpression:
		lintMarkExpressionIdentifiers(e.Left, imports)
		lintMarkExpressionIdentifiers(e.Index, imports)
	case *ast.ListLiteral:
		for _, element := range e.Elements {
			lintMarkExpressionIdentifiers(element, imports)
		}
	case *ast.DictLiteral:
		for _, pair := range e.Entries {
			lintMarkExpressionIdentifiers(pair.Key, imports)
			lintMarkExpressionIdentifiers(pair.Value, imports)
		}
	case *ast.SetLiteral:
		for _, element := range e.Elements {
			lintMarkExpressionIdentifiers(element, imports)
		}
	case *ast.RangeExpression:
		lintMarkExpressionIdentifiers(e.Start, imports)
		lintMarkExpressionIdentifiers(e.End, imports)
		lintMarkExpressionIdentifiers(e.Step, imports)
	case *ast.FunctionLiteral:
		for _, param := range e.Parameters {
			lintMarkTypeRef(param.Type, imports)
			lintMarkExpressionIdentifiers(param.Default, imports)
		}
		lintMarkTypeRef(e.ReturnType, imports)
		lintMarkBlockIdentifiers(e.Body, imports)
	case *ast.MatchExpression:
		lintMarkExpressionIdentifiers(e.Expr, imports)
		for _, clause := range e.Cases {
			lintMarkExpressionIdentifiers(clause.Pattern, imports)
			lintMarkExpressionIdentifiers(clause.Guard, imports)
			lintMarkExpressionIdentifiers(clause.Value, imports)
			lintMarkBlockIdentifiers(clause.Body, imports)
		}
	case *ast.AwaitExpression:
		lintMarkExpressionIdentifiers(e.Value, imports)
	case *ast.CastExpression:
		lintMarkExpressionIdentifiers(e.Value, imports)
	case *ast.TernaryExpression:
		lintMarkExpressionIdentifiers(e.Condition, imports)
		lintMarkExpressionIdentifiers(e.ThenExpr, imports)
		lintMarkExpressionIdentifiers(e.ElseExpr, imports)
	}
}

func lintUnreachableStatement(file string, stmt ast.Statement) []checkDiagnostic {
	switch s := stmt.(type) {
	case *ast.ExportStatement:
		return lintUnreachableStatement(file, s.Statement)
	case *ast.InitStatement:
		return lintUnreachableBlock(file, s.Body)
	case *ast.FunctionStatement:
		return lintUnreachableBlock(file, s.Body)
	case *ast.ClassStatement:
		diagnostics := []checkDiagnostic{}
		for _, member := range s.Members {
			diagnostics = append(diagnostics, lintUnreachableStatement(file, member)...)
		}
		return diagnostics
	case *ast.IfStatement:
		diagnostics := lintUnreachableBlock(file, s.Consequence)
		for _, clause := range s.ElseIfs {
			diagnostics = append(diagnostics, lintUnreachableBlock(file, clause.Body)...)
		}
		diagnostics = append(diagnostics, lintUnreachableBlock(file, s.Alternative)...)
		return diagnostics
	case *ast.WhileStatement:
		return lintUnreachableBlock(file, s.Body)
	case *ast.ForStatement:
		return lintUnreachableBlock(file, s.Body)
	case *ast.TryStatement:
		diagnostics := lintUnreachableBlock(file, s.Body)
		for _, clause := range s.Catches {
			diagnostics = append(diagnostics, lintUnreachableBlock(file, clause.Body)...)
		}
		diagnostics = append(diagnostics, lintUnreachableBlock(file, s.Finally)...)
		return diagnostics
	case *ast.MatchStatement:
		diagnostics := []checkDiagnostic{}
		for _, clause := range s.Cases {
			diagnostics = append(diagnostics, lintUnreachableBlock(file, clause.Body)...)
		}
		return diagnostics
	default:
		return nil
	}
}

func lintUnreachableBlock(file string, block *ast.BlockStatement) []checkDiagnostic {
	if block == nil {
		return nil
	}
	diagnostics := []checkDiagnostic{}
	terminated := false
	for _, stmt := range block.Statements {
		if terminated {
			line, column := statementPosition(stmt)
			diagnostics = append(diagnostics, checkDiagnostic{
				File:     file,
				Line:     line,
				Column:   column,
				Severity: "warning",
				Rule:     "unreachable",
				Message:  "statement is unreachable",
			})
			continue
		}
		diagnostics = append(diagnostics, lintUnreachableStatement(file, stmt)...)
		if statementTerminates(stmt) {
			terminated = true
		}
	}
	return diagnostics
}

func statementTerminates(stmt ast.Statement) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStatement:
		return true
	case *ast.SimpleStatement:
		return s.Kind == "break" || s.Kind == "continue" || s.Kind == "throw"
	case *ast.ExportStatement:
		return statementTerminates(s.Statement)
	default:
		return false
	}
}

func statementPosition(stmt ast.Statement) (int, int) {
	switch s := stmt.(type) {
	case *ast.ExportStatement:
		return s.Token.Line, s.Token.Column
	case *ast.ImportStatement:
		return s.Token.Line, s.Token.Column
	case *ast.DeclarationStatement:
		return s.Token.Line, s.Token.Column
	case *ast.DestructuringStatement:
		return s.Token.Line, s.Token.Column
	case *ast.ExpressionStatement:
		return s.Token.Line, s.Token.Column
	case *ast.ReturnStatement:
		return s.Token.Line, s.Token.Column
	case *ast.YieldStatement:
		return s.Token.Line, s.Token.Column
	case *ast.SimpleStatement:
		return s.Token.Line, s.Token.Column
	case *ast.IfStatement:
		return s.Token.Line, s.Token.Column
	case *ast.WhileStatement:
		return s.Token.Line, s.Token.Column
	case *ast.ForStatement:
		return s.Token.Line, s.Token.Column
	case *ast.FunctionStatement:
		return s.Token.Line, s.Token.Column
	case *ast.ClassStatement:
		return s.Token.Line, s.Token.Column
	case *ast.TryStatement:
		return s.Token.Line, s.Token.Column
	case *ast.MatchStatement:
		return s.Token.Line, s.Token.Column
	case *ast.InitStatement:
		return s.Token.Line, s.Token.Column
	default:
		return 0, 0
	}
}

func runScript(sourcePath string, scriptArgs []string, source []byte, program *ast.Program, mode executionMode, stdout io.Writer, trace io.Writer) (int, error) {
	if mode != executionEvaluatorOnly {
		chunk, err := loadOrCompileBytecode(sourcePath, source, program)
		if err == nil {
			traceExecution(trace, "vm", "")
			loader := newBytecodeModuleLoader(stdout, []string{filepath.Dir(sourcePath)})
			stateful := evaluator.NewWithArgsAndModulePaths(stdout, scriptArgs, []string{filepath.Dir(sourcePath)})
			defer stateful.Cleanup()
			loader.stateful = stateful
			vm := bytecode.NewVMWithModuleLoader(chunk, stdout, loader)
			defer vm.Cleanup()
			vm.SetModulePaths([]string{filepath.Dir(sourcePath)})
			vm.SetStatefulNativeCaller(stateful)
			stateful.SetMethodDispatcher(vm)
			if err := vm.Run(); err != nil {
				var exitErr bytecode.ExitError
				if errors.As(err, &exitErr) {
					return exitErr.Code, nil
				}
				return 1, err
			}
			return 0, nil
		}
		if mode == executionVMStrict {
			return 1, err
		}
		// Bytecode-compile errors that are NOT parity gaps are real
		// static-analysis errors (type mismatches, no-matching-overload,
		// undeclared identifiers). Abort instead of falling back to the
		// evaluator: running half the script before crashing on a
		// known-bad call is the worst possible failure mode.
		if !isBytecodeParityError(err) {
			return 1, err
		}
		traceExecution(trace, "evaluator", err.Error())
	} else {
		traceExecution(trace, "evaluator", "--disable-vm")
	}
	return runEvaluator(sourcePath, scriptArgs, program, stdout)
}

func runModule(moduleName string, moduleArgs []string, mode executionMode, traceExec bool, stdout io.Writer, stderr io.Writer) (int, error) {
	if moduleName == "" {
		return 2, fmt.Errorf("module name is required")
	}
	alias := "__geb_module"
	source := []byte(fmt.Sprintf(`import sys;
import %s as %s;

let __geb_result = %s.main(sys.args());
if (__geb_result != null) {
    sys.exit(__geb_result as int);
}
`, moduleName, alias, alias))
	program, err := parseAndAnalyze(string(source))
	if err != nil {
		return 1, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return 1, err
	}
	sourcePath := filepath.Join(wd, "__geblang_module__.gb")
	return runScript(sourcePath, moduleArgs, source, program, mode, stdout, traceWriter(traceExec, stderr))
}

func traceWriter(enabled bool, writer io.Writer) io.Writer {
	if !enabled {
		return nil
	}
	return writer
}

func traceExecution(writer io.Writer, engine string, reason string) {
	if writer == nil {
		return
	}
	if reason == "" {
		fmt.Fprintf(writer, "geblang: execution=%s\n", engine)
		return
	}
	fmt.Fprintf(writer, "geblang: execution=%s reason=%s\n", engine, reason)
}

func runEvaluator(sourcePath string, scriptArgs []string, program *ast.Program, stdout io.Writer) (int, error) {
	e := evaluator.NewWithArgsAndModulePaths(stdout, scriptArgs, []string{filepath.Dir(sourcePath)})
	defer e.Cleanup()
	result, err := e.Eval(program)
	if err != nil {
		return 1, err
	}
	if result.Exited {
		return result.ExitCode, nil
	}
	return 0, nil
}

func runCache(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: geblang cache clean")
		fmt.Fprintln(os.Stderr, "       geblang cache stats [--json]")
		os.Exit(2)
	}
	switch args[0] {
	case "clean":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "usage: geblang cache clean")
			os.Exit(2)
		}
		if err := cleanBytecodeCache(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("bytecode cache cleaned")
	case "stats":
		config, err := parseCacheStatsArgs(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprintln(os.Stderr, "usage: geblang cache stats [--json]")
			os.Exit(2)
		}
		stats, err := bytecodeCacheStats()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if config.JSON {
			if err := writeCacheStatsJSON(os.Stdout, stats); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		fmt.Printf("cache root: %s\n", stats.Root)
		fmt.Printf("files: %d\n", stats.Files)
		fmt.Printf("bytes: %d\n", stats.Bytes)
	default:
		fmt.Fprintln(os.Stderr, "usage: geblang cache clean")
		fmt.Fprintln(os.Stderr, "       geblang cache stats [--json]")
		os.Exit(2)
	}
}

func cleanBytecodeCache() error {
	return os.RemoveAll(bytecodeCacheRoot())
}

type cacheStats struct {
	Root  string `json:"root"`
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
}

type cacheStatsConfig struct {
	JSON bool
}

func parseCacheStatsArgs(args []string) (cacheStatsConfig, error) {
	config := cacheStatsConfig{}
	for _, arg := range args {
		switch arg {
		case "--json":
			config.JSON = true
		default:
			return config, fmt.Errorf("unknown cache stats option %s", arg)
		}
	}
	return config, nil
}

func writeCacheStatsJSON(writer io.Writer, stats cacheStats) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(stats)
}

func bytecodeCacheStats() (cacheStats, error) {
	root := bytecodeCacheRoot()
	stats := cacheStats{Root: root}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return stats, nil
	} else if err != nil {
		return stats, err
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stats.Files++
		stats.Bytes += info.Size()
		return nil
	})
	return stats, err
}

func runTests(args []string) {
	tags := []string{}
	paths := []string{}
	verbose := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tag":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "geblang test --tag expects a name")
				os.Exit(2)
			}
			tags = append(tags, args[i+1])
			i++
		case "--verbose", "-v":
			verbose = true
		default:
			paths = append(paths, args[i])
		}
	}
	if len(paths) != 1 {
		fmt.Fprintln(os.Stderr, "usage: geblang test [--tag name] [--verbose] <file-or-dir>")
		os.Exit(2)
	}
	files, err := discoverTestFiles(paths[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no test files found")
		os.Exit(1)
	}
	total := int64(0)
	failed := int64(0)
	for _, file := range files {
		fileTotal, fileFailed, err := runTestFile(file, tags, verbose)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", file, err)
			os.Exit(1)
		}
		total += fileTotal
		failed += fileFailed
	}
	fmt.Printf("tests: total=%d failed=%d passed=%d\n", total, failed, total-failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func discoverTestFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	files := []string{}
	err = filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), "_test.gb") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func runTestFile(path string, tags []string, verbose bool) (int64, int64, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	program, err := parseAndAnalyze(string(source))
	if err != nil {
		return 0, 0, err
	}
	classes := testClasses(program)
	if len(classes) == 0 {
		return 0, 0, nil
	}
	runner := buildTestRunner(classes, tags, verbose)
	program, err = parseAndAnalyze(string(source) + "\n" + runner)
	if err != nil {
		return 0, 0, err
	}
	var out strings.Builder
	result, err := evaluator.New(&out).Eval(program)
	if err != nil {
		return 0, 0, err
	}
	if result.Exited && result.ExitCode != 0 {
		return 0, 0, fmt.Errorf("test runner exited with %d", result.ExitCode)
	}
	printTestOutput(out.String())
	return parseTestSummary(out.String())
}

func parseAndAnalyze(source string) (*ast.Program, error) {
	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(p.Errors(), "\n"))
	}
	if diagnostics := semantic.New().Analyze(program); len(diagnostics) > 0 {
		messages := make([]string, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			messages = append(messages, diagnostic.Message)
		}
		return nil, fmt.Errorf("%s", strings.Join(messages, "\n"))
	}
	return program, nil
}

func testClasses(program *ast.Program) []string {
	classes := []string{}
	for _, stmt := range program.Statements {
		class, ok := stmt.(*ast.ClassStatement)
		if !ok || class.Extends == nil {
			continue
		}
		// The runner targets classes that derive (directly) from the
		// Test framework class. Source typically writes `extends test.Test`
		// after `import test`, which the parser stores as the fully-
		// qualified name "test.Test"; accept the bare form too for
		// callers who alias the import.
		switch class.Extends.Name {
		case "Test", "test.Test":
			classes = append(classes, class.Name.Value)
		}
	}
	return classes
}

func buildTestRunner(classes []string, tags []string, verbose bool) string {
	var b strings.Builder
	b.WriteString("import io;\nimport sys;\nimport test;\n")
	b.WriteString("let __geb_total = 0;\nlet __geb_failed = 0;\n")
	tagList := "[]"
	if len(tags) > 0 {
		quoted := make([]string, 0, len(tags))
		for _, tag := range tags {
			quoted = append(quoted, strconvQuote(tag))
		}
		tagList = "[" + strings.Join(quoted, ", ") + "]"
	}
	for i, className := range classes {
		resultName := fmt.Sprintf("__geb_result_%d", i)
		fmt.Fprintf(&b, "let %s = test.run(%s, {\"tags\": %s});\n", resultName, className, tagList)
		fmt.Fprintf(&b, "__geb_total = __geb_total + %s[\"total\"];\n", resultName)
		fmt.Fprintf(&b, "__geb_failed = __geb_failed + %s[\"failed\"];\n", resultName)
		if verbose {
			// Emit per-test pass/fail lines in testdox style. The class
			// name comes first, then each method on its own line. Names
			// are kept verbatim so they read like the source.
			fmt.Fprintf(&b, "io.println(%q);\n", className)
			fmt.Fprintf(&b, "for (__geb_case in %s[\"tests\"]) {\n", resultName)
			b.WriteString("  if (__geb_case[\"passed\"]) {\n")
			b.WriteString("    io.println(\"  PASS \" + __geb_case[\"name\"]);\n")
			b.WriteString("  } else {\n")
			b.WriteString("    io.println(\"  FAIL \" + __geb_case[\"name\"] + \": \" + __geb_case[\"message\"]);\n")
			b.WriteString("  }\n")
			b.WriteString("}\n")
		} else {
			fmt.Fprintf(&b, "for (__geb_failure in %s[\"failures\"]) { io.println(\"FAIL %s: \" + __geb_failure); }\n", resultName, className)
		}
	}
	b.WriteString("io.println(\"__GEB_TEST_SUMMARY__ \" + (__geb_total as string) + \" \" + (__geb_failed as string));\n")
	return b.String()
}

func strconvQuote(value string) string {
	return fmt.Sprintf("%q", value)
}

func parseTestSummary(output string) (int64, int64, error) {
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "__GEB_TEST_SUMMARY__ ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			return 0, 0, fmt.Errorf("invalid test summary")
		}
		var total, failed int64
		if _, err := fmt.Sscan(parts[1], &total); err != nil {
			return 0, 0, err
		}
		if _, err := fmt.Sscan(parts[2], &failed); err != nil {
			return 0, 0, err
		}
		return total, failed, nil
	}
	return 0, 0, fmt.Errorf("missing test summary")
}

func printTestOutput(output string) {
	for _, line := range strings.SplitAfter(output, "\n") {
		if strings.HasPrefix(strings.TrimSuffix(line, "\n"), "__GEB_TEST_SUMMARY__ ") {
			continue
		}
		fmt.Print(line)
	}
}

func loadOrCompileBytecode(sourcePath string, source []byte, astProgram *ast.Program) (bytecode.Chunk, error) {
	cacheDir := bytecodeCacheDir()
	cachePath := bytecode.CachePath(cacheDir, sourcePath, source, version)
	if data, err := os.ReadFile(cachePath); err == nil {
		chunk, err := bytecode.Decode(data)
		if err == nil && chunk.Compiler == version && chunk.SourceHash == bytecode.SourceHash(source) {
			return chunk, nil
		}
	}
	chunk, err := bytecode.Compile(astProgram, source, version)
	if err != nil {
		return bytecode.Chunk{}, err
	}
	encoded, err := bytecode.Encode(chunk)
	if err != nil {
		return bytecode.Chunk{}, err
	}
	if err := os.MkdirAll(cacheDir, 0o755); err == nil {
		_ = os.WriteFile(cachePath, encoded, 0o644)
	}
	return chunk, nil
}

type bytecodeModuleLoader struct {
	stdout      io.Writer
	modulePaths []string
	stateful    bytecode.StatefulNativeCaller
	modules     map[string]*runtime.Module
	chunks      map[string]bytecode.Chunk
	globals     map[string][]runtime.Value
	decorators  map[string]bytecode.FunctionDecoratorState
	loading     map[string]bool
}

func newBytecodeModuleLoader(stdout io.Writer, modulePaths []string) *bytecodeModuleLoader {
	return &bytecodeModuleLoader{
		stdout:      stdout,
		modulePaths: append([]string(nil), modulePaths...),
		modules:     map[string]*runtime.Module{},
		chunks:      map[string]bytecode.Chunk{},
		globals:     map[string][]runtime.Value{},
		decorators:  map[string]bytecode.FunctionDecoratorState{},
		loading:     map[string]bool{},
	}
}

func (l *bytecodeModuleLoader) LoadModule(canonical string, alias string) (*runtime.Module, error) {
	if module, ok := l.modules[canonical]; ok {
		return module, nil
	}
	path, err := modules.NewResolver(l.modulePaths).Resolve(canonical)
	if err != nil {
		return nil, err
	}
	if l.loading[path] {
		return nil, fmt.Errorf("circular import detected for %s", canonical)
	}
	l.loading[path] = true
	defer delete(l.loading, path)

	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read module %s: %w", canonical, err)
	}
	p := parser.New(lexer.New(string(source)))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("parse module %s: %s", canonical, strings.Join(p.Errors(), "\n"))
	}
	if diagnostics := semantic.New().Analyze(program); len(diagnostics) > 0 {
		errorMessages := make([]string, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == semantic.SeverityWarning {
				fmt.Fprintf(l.stdout, "warning: module %s: %s\n", canonical, diagnostic.Message)
				continue
			}
			errorMessages = append(errorMessages, diagnostic.Message)
		}
		if len(errorMessages) > 0 {
			return nil, fmt.Errorf("analyze module %s: %s", canonical, strings.Join(errorMessages, "\n"))
		}
	}

	chunk, err := loadOrCompileBytecode(path, source, program)
	if err != nil {
		return nil, fmt.Errorf("compile module %s: %w", canonical, err)
	}
	previousPaths := l.modulePaths
	l.modulePaths = append([]string{filepath.Dir(path)}, l.modulePaths...)
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(canonical)
	vm.SetModulePaths(l.modulePaths)
	vm.SetStatefulNativeCaller(l.stateful)
	err = vm.Run()
	l.modulePaths = previousPaths
	if err != nil {
		return nil, fmt.Errorf("evaluate module %s: %w", canonical, err)
	}
	exports, err := vm.Exports()
	if err != nil {
		return nil, fmt.Errorf("export module %s: %w", canonical, err)
	}
	module := &runtime.Module{Name: alias, Exports: exports}
	for name, value := range module.Exports {
		if function, ok := value.(runtime.BytecodeFunction); ok {
			function.Module = canonical
			module.Exports[name] = function
		}
		if class, ok := value.(runtime.BytecodeClass); ok {
			class.Module = canonical
			module.Exports[name] = class
		}
	}
	l.modules[canonical] = module
	l.chunks[canonical] = chunk
	l.globals[canonical] = vm.GlobalsSnapshot()
	l.decorators[canonical] = vm.FunctionDecoratorState()
	return module, nil
}

func (l *bytecodeModuleLoader) CallModuleFunction(function runtime.BytecodeFunction, args []runtime.Value) (runtime.Value, error) {
	chunk, ok := l.chunks[function.Module]
	if !ok {
		return nil, fmt.Errorf("module %s is not loaded", function.Module)
	}
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(function.Module)
	vm.SetModulePaths(l.modulePaths)
	vm.SetStatefulNativeCaller(l.stateful)
	vm.RestoreGlobals(l.globals[function.Module])
	vm.RestoreFunctionDecoratorState(l.decorators[function.Module])
	return vm.CallFunction(function.Index, args)
}

func (l *bytecodeModuleLoader) CallModuleClosure(closure runtime.BytecodeClosure, args []runtime.Value) (runtime.Value, error) {
	chunk, ok := l.chunks[closure.Module]
	if !ok {
		return nil, fmt.Errorf("module %s is not loaded", closure.Module)
	}
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(closure.Module)
	vm.SetModulePaths(l.modulePaths)
	vm.SetStatefulNativeCaller(l.stateful)
	vm.RestoreGlobals(l.globals[closure.Module])
	vm.RestoreFunctionDecoratorState(l.decorators[closure.Module])
	return vm.CallClosure(closure, args)
}

func (l *bytecodeModuleLoader) ConstructModuleClass(class runtime.BytecodeClass, args []runtime.Value) (runtime.Value, error) {
	chunk, ok := l.chunks[class.Module]
	if !ok {
		return nil, fmt.Errorf("module %s is not loaded", class.Module)
	}
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(class.Module)
	vm.SetModulePaths(l.modulePaths)
	vm.SetStatefulNativeCaller(l.stateful)
	vm.RestoreGlobals(l.globals[class.Module])
	vm.RestoreFunctionDecoratorState(l.decorators[class.Module])
	return vm.ConstructClass(class.Index, args)
}

func (l *bytecodeModuleLoader) CallModuleStaticMethod(class runtime.BytecodeClass, methodName string, args []runtime.Value) (runtime.Value, error) {
	chunk, ok := l.chunks[class.Module]
	if !ok {
		return nil, fmt.Errorf("module %s is not loaded", class.Module)
	}
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(class.Module)
	vm.SetModulePaths(l.modulePaths)
	vm.SetStatefulNativeCaller(l.stateful)
	vm.RestoreGlobals(l.globals[class.Module])
	vm.RestoreFunctionDecoratorState(l.decorators[class.Module])
	return vm.CallStaticMethod(class.Index, methodName, args)
}

func (l *bytecodeModuleLoader) CallModuleMethod(module string, className string, methodName string, instance *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	chunk, ok := l.chunks[module]
	if !ok {
		return nil, fmt.Errorf("module %s is not loaded", module)
	}
	vm := bytecode.NewVMWithModuleLoader(chunk, l.stdout, l)
	vm.SetModuleName(module)
	vm.SetModulePaths(l.modulePaths)
	vm.SetStatefulNativeCaller(l.stateful)
	vm.RestoreGlobals(l.globals[module])
	vm.RestoreFunctionDecoratorState(l.decorators[module])
	return vm.CallMethod(instance, methodName, args)
}

func bytecodeCacheRoot() string {
	return ".geblang-cache"
}

func bytecodeCacheDir() string {
	return filepath.Join(bytecodeCacheRoot(), version)
}
