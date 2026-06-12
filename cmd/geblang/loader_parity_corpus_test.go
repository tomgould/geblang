package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// loaderParityCase is a two-module program run through the REAL module
// loader on both backends. stdout, stderr, and exit code must match
// byte-for-byte: in-process parity tests run single-chunk and have
// repeatedly missed loader-hop divergences (cross-module dispatch
// cluster V1-V3, the 1.19.0 frame loss).
type loaderParityCase struct {
	name   string
	helper string
	main   string
}

var loaderParityCorpus = []loaderParityCase{
	{
		name: "FunctionCall",
		helper: `module helper;

export func double(int x): int {
    return x * 2;
}
`,
		main: `import io;
import helper;

io.println(helper.double(21));
`,
	},
	{
		name: "MethodDispatch",
		helper: `module helper;

export class Counter {
    int n;
    func Counter(int n) { this.n = n; }
    func bump(int by): int { this.n = this.n + by; return this.n; }
}
`,
		main: `import io;
import helper;

let c = helper.Counter(10);
io.println(c.bump(5));
io.println(c.bump(7));
`,
	},
	{
		name: "InheritedMethodAcrossBoundary",
		helper: `module helper;

export class Base {
    func describe(): string { return "base"; }
    func boom(): int { throw ValueError("inherited cross boom"); }
}
`,
		main: `import io;
import helper;

class Sub extends helper.Base {
}

let s = Sub();
io.println(s.describe());
io.println(s.boom());
`,
	},
	{
		name: "InstanceofAcrossBoundary",
		helper: `module helper;

export class Animal {
    func Animal() {}
}

export class Dog extends Animal {
    func Dog() { parent(); }
}
`,
		main: `import io;
import helper;

let d = helper.Dog();
io.println(d instanceof helper.Dog);
io.println(d instanceof helper.Animal);
`,
	},
	{
		name: "InterfaceDefaultAcrossBoundary",
		helper: `module helper;

export interface Greeter {
    func name(): string;
    func greet(): string {
        return "hello " + this.name();
    }
}
`,
		main: `import io;
import helper;

class Person implements helper.Greeter {
    func name(): string { return "ada"; }
}

io.println(Person().greet());
`,
	},
	{
		name: "StaticMethodAcrossBoundary",
		helper: `module helper;

export class Maths {
    static func triple(int x): int { return x * 3; }
}
`,
		main: `import io;
import helper;

io.println(helper.Maths.triple(14));
`,
	},
	{
		name: "FromImportFunction",
		helper: `module helper;

export func shout(string s): string {
    return s.upper();
}
`,
		main: `import io;
from helper import shout;

io.println(shout("quiet"));
`,
	},
	{
		name: "AliasedImport",
		helper: `module helper;

export func half(int x): int {
    return x // 2;
}
`,
		main: `import io;
import helper as h;

io.println(h.half(42));
`,
	},
	{
		name: "ListSpreadIntoCrossModuleFunction",
		helper: `module helper;

export func sum3(int a, int b, int c): int {
    return a + b + c;
}
`,
		main: `import io;
import helper;

let xs = [1, 2, 3];
io.println(helper.sum3(...xs));
`,
	},
	{
		name: "UncaughtThrowAcrossBoundary",
		helper: `module helper;

export func explode(int x): int {
    throw ValueError("loader boom");
}
`,
		main: `import io;
import helper;

func relay(int x): int {
    return helper.explode(x);
}

io.println(relay(2));
`,
	},
	{
		name: "CaughtCrossModuleThrowClassAndTrace",
		helper: `module helper;

export func explode(): int {
    throw ValueError("caught cross boom");
}
`,
		main: `import io;
import helper;

try {
    helper.explode();
} catch (ValueError e) {
    io.println(e.class);
    io.println(e.message);
    io.println(e.stackTrace().first().function());
}
`,
	},
	{
		name: "DictSpreadIntoCrossModuleMethod",
		helper: `module helper;

export class Box {
    func Box() {}
    func describe(int width, int height, string label): string {
        return "${label}:${width}x${height}";
    }
}
`,
		main: `import io;
import helper;

let b = helper.Box();
io.println(b.describe(...{"label": "crate", "height": 4, "width": 9}));
`,
	},
	{
		name: "DestructorFailureAcrossBoundary",
		helper: `module helper;

export class Res {
    func Res() {}
    func ~Res() {
        throw ValueError("dtor cross boom");
    }
}
`,
		main: `import io;
import helper;

func use() {
    let r = helper.Res();
    io.println("used");
}

use();
io.println("end");
`,
	},
	{
		name: "GeneratorConsumedAcrossBoundary",
		helper: `module helper;

export func counts(int n): generator<int> {
    for (i in 0..<n) {
        yield i * 10;
    }
}
`,
		main: `import io;
import helper;

for (v in helper.counts(3)) {
    io.println(v);
}
`,
	},
}

// TestLoaderParityCorpus runs each two-module case through the real CLI
// on both backends and requires byte-identical stdout, stderr, and exit
// codes.
func TestLoaderParityCorpus(t *testing.T) {
	bin := buildGeblangBinary(t, false)
	for _, tc := range loaderParityCorpus {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "helper.gb"), []byte(tc.helper), 0o644); err != nil {
				t.Fatal(err)
			}
			mainPath := filepath.Join(dir, "main.gb")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatal(err)
			}
			run := func(flag string) (string, string, int) {
				cmd := exec.Command(bin, flag, mainPath)
				var stdout, stderr strings.Builder
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				err := cmd.Run()
				code := 0
				if exitErr, ok := err.(*exec.ExitError); ok {
					code = exitErr.ExitCode()
				} else if err != nil {
					t.Fatalf("run %s: %v", flag, err)
				}
				return stdout.String(), stderr.String(), code
			}
			vmOut, vmErr, vmCode := run("--vm-strict")
			evOut, evErr, evCode := run("--disable-vm")
			if vmCode != evCode {
				t.Fatalf("exit code divergence: vm %d eval %d\nvm stderr: %s\neval stderr: %s", vmCode, evCode, vmErr, evErr)
			}
			if vmOut != evOut {
				t.Fatalf("stdout divergence:\n--- vm ---\n%s--- eval ---\n%s", vmOut, evOut)
			}
			if vmErr != evErr {
				t.Fatalf("stderr divergence:\n--- vm ---\n%s--- eval ---\n%s", vmErr, evErr)
			}
		})
	}
}
