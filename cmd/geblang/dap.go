package main

import (
	"fmt"
	"os"
	"strconv"

	"geblang/internal/dap"
)

func runDap(args []string) {
	tcp := false
	port := 0
	filtered := args[:0:0]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tcp":
			tcp = true
		case "--port":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dap: --port requires a port number")
				os.Exit(2)
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 || n > 65535 {
				fmt.Fprintf(os.Stderr, "dap: invalid port %q\n", args[i])
				os.Exit(2)
			}
			port = n
			tcp = true
		default:
			filtered = append(filtered, args[i])
		}
	}
	args = filtered
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(os.Stdout, "usage: geblang dap [--tcp] [--port N]")
		fmt.Fprintln(os.Stdout, "Starts a DAP debug adapter server on stdin/stdout.")
		fmt.Fprintln(os.Stdout, "With --tcp: listens on a TCP port and prints the address to stdout.")
		fmt.Fprintln(os.Stdout, "With --port N: binds to port N instead of a random port (implies --tcp).")
		fmt.Fprintln(os.Stdout, "Intended to be launched by an IDE (e.g. VS Code) debug configuration.")
		return
	}
	if tcp {
		if err := dap.ServeTCP(os.Stdout, port); err != nil {
			fmt.Fprintln(os.Stderr, "dap:", err)
			os.Exit(1)
		}
		return
	}
	if err := dap.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dap:", err)
		os.Exit(1)
	}
}
