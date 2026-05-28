package main

import (
	"fmt"
	"os"

	"geblang/internal/ffi"
)

func runBind(args []string) {
	out := ""
	manifestPath := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out", "-o":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "geblang bind --out expects a file path")
				os.Exit(2)
			}
			out = args[i+1]
			i++
		case "--help", "-h":
			printBindUsage(os.Stdout)
			return
		default:
			if manifestPath != "" {
				fmt.Fprintln(os.Stderr, "geblang bind: only one manifest path accepted")
				os.Exit(2)
			}
			manifestPath = args[i]
		}
	}
	if manifestPath == "" {
		printBindUsage(os.Stderr)
		os.Exit(2)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "geblang bind: read manifest: %v\n", err)
		os.Exit(1)
	}
	manifest, err := ffi.ParseBindingManifest(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "geblang bind: %v\n", err)
		os.Exit(1)
	}
	source, err := ffi.GenerateBindings(manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "geblang bind: generate: %v\n", err)
		os.Exit(1)
	}
	if out == "" {
		fmt.Print(source)
		return
	}
	if err := os.WriteFile(out, []byte(source), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "geblang bind: write %s: %v\n", out, err)
		os.Exit(1)
	}
}

func printBindUsage(w *os.File) {
	fmt.Fprintln(w, "usage: geblang bind [--out file] <manifest.yaml>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Generates a Geblang module that wraps a C-ABI shared library according")
	fmt.Fprintln(w, "to the manifest. With no --out, prints the generated source to stdout.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Manifest schema:")
	fmt.Fprintln(w, "  module:   string         (required) Geblang module name for the output")
	fmt.Fprintln(w, "  library:  string         (required) path or library name for dlopen")
	fmt.Fprintln(w, "  doc:      string         optional module-level doc comment")
	fmt.Fprintln(w, "  constants: list          optional [{name, value, doc}]")
	fmt.Fprintln(w, "  structs:  dict<name, {fields: [{name, type}]}>")
	fmt.Fprintln(w, "  functions: list of {name, args: [type], returns: type, doc}")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Types: VOID, INT8..INT64, UINT8..UINT64, FLOAT, DOUBLE, PTR, CSTRING, BYTES.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  geblang bind bindings/sqlite.yaml --out src/sqlite.gb")
	fmt.Fprintln(w, "  geblang bind bindings/libm.yaml > src/libm.gb")
}
