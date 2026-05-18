package main

import (
	"fmt"
	"net"
	"os"
)

func handleCall(fn string, args []any, kwargs map[string]any, slots [][]byte) (any, [][]byte, error) {
	switch fn {
	case "add":
		return args[0].(float64) + args[1].(float64), nil, nil
	case "greet":
		return "hello " + fmt.Sprint(kwargs["name"]), nil, nil
	case "echo":
		blob, err := bytesArg(args[0], slots)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"$type": "bytes", "slot": 0}, [][]byte{blob}, nil
	default:
		return nil, nil, fmt.Errorf("unknown function: %s", fn)
	}
}

func main() {
	host := getenv("EXT_HOST", "127.0.0.1")
	port := getenv("EXT_PORT", "9103")
	ln, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		panic(err)
	}
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err == nil {
			go serveExtension(conn, "go_example", []string{"add", "echo", "greet"}, handleCall)
		}
	}
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
