package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"geblang/internal/sourcedoc"
)

type docConfig struct {
	format string
	out    string
	path   string
}

func runDoc(args []string) {
	config, err := parseDocArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: geblang doc [--format markdown|json] [--out file] <file-or-dir>")
		os.Exit(2)
	}
	report, err := sourcedoc.Collect(config.path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var out bytes.Buffer
	switch config.format {
	case "markdown":
		sourcedoc.WriteMarkdown(&out, report)
	case "json":
		if err := sourcedoc.WriteJSON(&out, report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown doc format %s\n", config.format)
		os.Exit(2)
	}
	if config.out != "" {
		if err := os.WriteFile(config.out, out.Bytes(), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", config.out, err)
			os.Exit(1)
		}
		return
	}
	fmt.Print(out.String())
}

func parseDocArgs(args []string) (docConfig, error) {
	config := docConfig{format: "markdown"}
	for len(args) > 0 {
		switch args[0] {
		case "--format":
			if len(args) < 2 {
				return config, fmt.Errorf("--format requires a value")
			}
			config.format = args[1]
			args = args[2:]
		case "--json":
			config.format = "json"
			args = args[1:]
		case "--out":
			if len(args) < 2 {
				return config, fmt.Errorf("--out requires a value")
			}
			config.out = args[1]
			args = args[2:]
		default:
			if strings.HasPrefix(args[0], "-") {
				return config, fmt.Errorf("unknown doc option %s", args[0])
			}
			if config.path != "" {
				return config, fmt.Errorf("doc accepts one file or directory")
			}
			config.path = args[0]
			args = args[1:]
		}
	}
	if config.path == "" {
		return config, fmt.Errorf("doc requires a file or directory")
	}
	if config.format != "markdown" && config.format != "json" {
		return config, fmt.Errorf("unknown doc format %s", config.format)
	}
	return config, nil
}
