package main

import (
	"fmt"
	"io"
	"os"

	"geblang/internal/formatter"
)

func runFmt(args []string) {
	// --stdin reads source from stdin and writes the formatted result to
	// stdout, leaving no file on disk. The VS Code extension uses this
	// path for Format Document and format-on-save.
	for i, arg := range args {
		if arg != "--stdin" && arg != "-" {
			continue
		}
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "fmt: --stdin does not take additional arguments")
			os.Exit(2)
		}
		_ = i
		src, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fmt:", err)
			os.Exit(1)
		}
		out, err := formatter.Format(src)
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

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "fmt: no files specified")
		fmt.Fprintln(os.Stderr, "usage: geblang fmt <file.gb> [...] | geblang fmt --stdin")
		os.Exit(2)
	}

	exitCode := 0
	for _, path := range args {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fmt:", err)
			exitCode = 1
			continue
		}
		out, err := formatter.Format(src)
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
