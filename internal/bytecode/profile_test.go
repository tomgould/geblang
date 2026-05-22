package bytecode_test

import (
	"bytes"
	"os"
	"runtime/pprof"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// To run: go test -run TestProfileClassDispatch -tags profile_bytecode ./internal/bytecode/
// Profile lands at /tmp/class_dispatch.prof; analyse with `go tool pprof`.
func TestProfileClassDispatch(t *testing.T) {
	if os.Getenv("GEBLANG_PROFILE_CLASS_DISPATCH") == "" {
		t.Skip("set GEBLANG_PROFILE_CLASS_DISPATCH=1 to enable")
	}
	src := []byte(`import io;

class Counter {
    int value;

    func Counter(int start) {
        this.value = start;
    }

    func step(int delta): int {
        this.value = this.value + delta;
        return this.value;
    }

    func double(): int {
        this.value = this.value * 2;
        return this.value;
    }
}

int n = 50000;

let c = Counter(0);
int sum = 0;

for (int i = 0; i < n; i++) {
    if (i % 100 == 0) {
        c.value = i;
        sum = sum + c.double();
    } else {
        sum = sum + c.step(1);
    }
}

io.println(sum);
`)
	p := parser.New(lexer.New(string(src)))
	program := p.ParseProgram()
	chunk, err := bytecode.Compile(program, src, "bench")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	f, err := os.Create("/tmp/class_dispatch.prof")
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()
	for i := 0; i < 80; i++ {
		var out bytes.Buffer
		vm := bytecode.NewVM(chunk, &out)
		if err := vm.Run(); err != nil {
			t.Fatalf("vm: %v", err)
		}
	}
}

func TestProfileRecursiveFib(t *testing.T) {
	if os.Getenv("GEBLANG_PROFILE_FIB") == "" {
		t.Skip("set GEBLANG_PROFILE_FIB=1 to enable")
	}
	src := []byte(`import io;

func fib(int n): int {
    if (n < 2) {
        return n;
    }
    return fib(n - 1) + fib(n - 2);
}

io.println(fib(28));
`)
	p := parser.New(lexer.New(string(src)))
	program := p.ParseProgram()
	chunk, err := bytecode.Compile(program, src, "bench")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	f, err := os.Create("/tmp/recursive_fib.prof")
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()
	for i := 0; i < 30; i++ {
		var out bytes.Buffer
		vm := bytecode.NewVM(chunk, &out)
		if err := vm.Run(); err != nil {
			t.Fatalf("vm: %v", err)
		}
	}
}

func TestProfileJSONRoundtrip(t *testing.T) {
	if os.Getenv("GEBLANG_PROFILE_JSON_ROUNDTRIP") == "" {
		t.Skip("set GEBLANG_PROFILE_JSON_ROUNDTRIP=1 to enable")
	}
	src := []byte(`import io;
import json;

int n = 80;

let payload = {
    "name": "Geblang",
    "version": "1.1.0",
    "tags": ["script", "static", "decimals"],
    "metrics": {"users": 12345, "posts": 678910, "active": true},
    "items": [
        {"id": 1, "title": "alpha", "score": 95, "labels": ["x", "y"]},
        {"id": 2, "title": "beta",  "score": 80, "labels": ["x", "z"]},
        {"id": 3, "title": "gamma", "score": 75, "labels": ["z"]},
        {"id": 4, "title": "delta", "score": 60, "labels": ["y", "z"]}
    ]
};

let bulk = {"records": []};
for (int i = 0; i < 800; i++) {
    bulk["records"] = bulk["records"].push(payload);
}

let text = json.stringify(bulk);

int total = 0;
for (int i = 0; i < n; i++) {
    let parsed = json.parse(text);
    let again = json.stringify(parsed);
    total = total + again.length();
}

io.println(total);
`)
	p := parser.New(lexer.New(string(src)))
	program := p.ParseProgram()
	chunk, err := bytecode.Compile(program, src, "bench")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	f, err := os.Create("/tmp/json_roundtrip.prof")
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()
	var out bytes.Buffer
	vm := bytecode.NewVM(chunk, &out)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm: %v", err)
	}
}
