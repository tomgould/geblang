package main

import (
	"fmt"
	"io"
	"os"

	"geblang/internal/formatter"
)

func runFmt(args []string) {
	// --clean = minimal form, --strip-comments = drop comments, --stdin = read stdin / write stdout (used by the VS Code extension).
	var opts formatter.Options
	stdin := false
	var paths []string
	for _, arg := range args {
		switch arg {
		case "--clean":
			opts.Clean = true
		case "--strip-comments":
			opts.StripComments = true
		case "--stdin", "-":
			stdin = true
		default:
			paths = append(paths, arg)
		}
	}

	if stdin {
		if len(paths) != 0 {
			fmt.Fprintln(os.Stderr, "fmt: --stdin does not take file arguments")
			os.Exit(2)
		}
		src, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fmt:", err)
			os.Exit(1)
		}
		out, err := formatter.FormatWithOptions(src, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fmt:", err)
			os.Exit(1)
		}
		if _, err := os.Stdout.Write(out); err != nil {
			fmt.Fprintln(os.Stderr, "fmt:", err)
			os.Exit(1)
		}
		return
	}

	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "fmt: no files specified")
		fmt.Fprintln(os.Stderr, "usage: geblang fmt [--clean] [--strip-comments] <file.gb> [...] | geblang fmt [...] --stdin")
		os.Exit(2)
	}

	exitCode := 0
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fmt:", err)
			exitCode = 1
			continue
		}
		out, err := formatter.FormatWithOptions(src, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fmt: %s: %v\n", path, err)
			exitCode = 1
			continue
		}
		if err := os.WriteFile(path, out, 0644); err != nil {
			fmt.Fprintln(os.Stderr, "fmt:", err)
			exitCode = 1
		}
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
