package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildNativeTestGeblang builds the geblang CLI under test, returning its path.
func buildNativeTestGeblang(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "geblang")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build geblang: %v\n%s", err, out)
	}
	return bin
}

// nativeBuildEnv forces an offline build so the test proves the vendored
// pipeline needs no network or module proxy.
func nativeBuildEnv() []string {
	return append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
}

// TestBuildNativeMatchesInterpreter transpiles a compute program (ints, list,
// dict, a class with a method, println) inside an exported main, builds it
// offline with --native, runs the binary, and asserts its stdout is
// byte-identical to the bundled VM build. This exercises the whole vendored
// offline pipeline and the shared exported-main entry convention.
func TestBuildNativeMatchesInterpreter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;

class Counter {
    int value;

    func Counter(int start) {
        this.value = start;
    }

    func bump(int amount): int {
        this.value = this.value + amount;
        return this.value;
    }
}

export func main(): int {
    let c = Counter(10);
    let nums = [1, 2, 3, 4];
    let total = 0;
    for (n in nums) {
        total = total + n;
    }
    io.println("total: " + (total as string));
    io.println("bumped: " + (c.bump(total) as string));
    let info = {"name": "geb", "n": 3};
    io.println(info);
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(dir, "bundle")
	bundleBuild := exec.Command(bin, "build", "--entry", "main", "--out", bundleOut, ".")
	bundleBuild.Dir = dir
	if combined, err := bundleBuild.CombinedOutput(); err != nil {
		t.Fatalf("bundle build failed: %v\n%s", err, combined)
	}
	wantOut, err := exec.Command(bundleOut).Output()
	if err != nil {
		t.Fatalf("run bundle binary: %v", err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %v\n%s", err, combined)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("native build produced no binary: %v", err)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir() // clean cwd: no source tree, no module
	gotOut, err := run.Output()
	if err != nil {
		t.Fatalf("run native binary: %v", err)
	}
	if string(gotOut) != string(wantOut) {
		t.Fatalf("native output != bundle\n--- want ---\n%q\n--- got ---\n%q", wantOut, gotOut)
	}
}

// TestBuildNativeRegex transpiles an exported main that uses a string regex
// method and a compiled re.Pattern, builds it offline with --native, and asserts
// its stdout is byte-identical to the bundled VM build (the RE2 bridge).
func TestBuildNativeRegex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;
import re;

export func main(): int {
    io.println("digits=" + ("abc123".matchesRegex("[0-9]+") as string));
    io.println("a1b2".replaceRegex("[0-9]", "#"));
    let p = re.compile("[0-9]+");
    io.println("test=" + (p.test("x42") as string));
    io.println("find=${p.find("foo7bar")}");
    io.println(p.findAll("a1b22c333"));
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(dir, "bundle")
	bundleBuild := exec.Command(bin, "build", "--entry", "main", "--out", bundleOut, ".")
	bundleBuild.Dir = dir
	if combined, err := bundleBuild.CombinedOutput(); err != nil {
		t.Fatalf("bundle build failed: %v\n%s", err, combined)
	}
	wantOut, err := exec.Command(bundleOut).Output()
	if err != nil {
		t.Fatalf("run bundle binary: %v", err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %v\n%s", err, combined)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir()
	gotOut, err := run.Output()
	if err != nil {
		t.Fatalf("run native binary: %v", err)
	}
	if string(gotOut) != string(wantOut) {
		t.Fatalf("native output != bundle\n--- want ---\n%q\n--- got ---\n%q", wantOut, gotOut)
	}
}

// TestBuildNativeBytesUrlCsv transpiles an exported main using a bytes-value
// method, url free functions, and csv parse + stringify, builds it offline with
// --native, and asserts its stdout is byte-identical to the bundled VM build.
func TestBuildNativeBytesUrlCsv(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;
import bytes;
import url;
import csv;

export func main(): int {
    let b = bytes.fromString("Geblang");
    io.println(b.toHex());
    io.println(b.slice(0, 3).toString());
    io.println(url.encode("a b&c=d"));
    let parts = url.parse("https://example.com:8443/p?q=1#frag");
    io.println(parts.get("host"));
    let rows = csv.parse("name,note\nwidget,\"a, b\"");
    io.println(rows.length());
    io.println(rows[1][1]);
    io.print(csv.stringify(rows));
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(dir, "bundle")
	bundleBuild := exec.Command(bin, "build", "--entry", "main", "--out", bundleOut, ".")
	bundleBuild.Dir = dir
	if combined, err := bundleBuild.CombinedOutput(); err != nil {
		t.Fatalf("bundle build failed: %v\n%s", err, combined)
	}
	wantOut, err := exec.Command(bundleOut).Output()
	if err != nil {
		t.Fatalf("run bundle binary: %v", err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %v\n%s", err, combined)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir()
	gotOut, err := run.Output()
	if err != nil {
		t.Fatalf("run native binary: %v", err)
	}
	if string(gotOut) != string(wantOut) {
		t.Fatalf("native output != bundle\n--- want ---\n%q\n--- got ---\n%q", wantOut, gotOut)
	}
}

// TestBuildNativeUrlObjectXml transpiles an exported main using a url.URL object
// with* chain plus xml validate/stringify, builds it offline with --native, and
// asserts its stdout is byte-identical to the bundled VM build.
func TestBuildNativeUrlObjectXml(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;
import url;
import xml;

export func main(): int {
    let u = url.URL("https://example.com:8443/a/b?q=1#frag");
    io.println(u.scheme());
    io.println(u.host());
    io.println(u.port());
    let v = u.withScheme("http").withHost("other.test").withPath("/x").toString();
    io.println(v);
    let n = url.URL("https://e.com/a/./b/../c?x=1").normalize().toString();
    io.println(n);
    io.println("valid=" + (xml.validate("<a><b>x</b></a>") as string));
    let el = {"name": "to", "attributes": {"k": "v"}, "children": [], "text": "Tove & co"};
    io.println(xml.stringify(el));
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(dir, "bundle")
	bundleBuild := exec.Command(bin, "build", "--entry", "main", "--out", bundleOut, ".")
	bundleBuild.Dir = dir
	if combined, err := bundleBuild.CombinedOutput(); err != nil {
		t.Fatalf("bundle build failed: %v\n%s", err, combined)
	}
	wantOut, err := exec.Command(bundleOut).Output()
	if err != nil {
		t.Fatalf("run bundle binary: %v", err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %v\n%s", err, combined)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir()
	gotOut, err := run.Output()
	if err != nil {
		t.Fatalf("run native binary: %v", err)
	}
	if string(gotOut) != string(wantOut) {
		t.Fatalf("native output != bundle\n--- want ---\n%q\n--- got ---\n%q", wantOut, gotOut)
	}
}

// TestBuildNativeAnyNavigation transpiles an exported main that json.parses a
// fixed string and navigates + casts the any-typed result (nested keys, array
// index, dict-miss, casts to string/int/list<any>), builds it offline with
// --native, and asserts byte-identical stdout against the bundled VM build.
func TestBuildNativeAnyNavigation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;
import json;

export func main(): int {
    let data = json.parse("{\"name\":\"widget\",\"qty\":12,\"price\":3.5,\"tags\":[\"a\",\"b\",\"c\"],\"meta\":{\"owner\":\"acme\",\"limits\":[10,20,30]}}");
    io.println(data["name"]);
    io.println(data["meta"]["owner"]);
    io.println(data["tags"][0]);
    io.println(data["meta"]["limits"][2]);
    io.println(data["missing"]);
    io.println((data["name"] as string));
    io.println((data["qty"] as int) + 100);
    io.println(data["price"] as float);
    io.println(data["price"] as int);
    let tags = data["tags"] as list<any>;
    io.println(tags.length());
    for (t in tags) {
        io.println(t as string);
    }
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(dir, "bundle")
	bundleBuild := exec.Command(bin, "build", "--entry", "main", "--out", bundleOut, ".")
	bundleBuild.Dir = dir
	if combined, err := bundleBuild.CombinedOutput(); err != nil {
		t.Fatalf("bundle build failed: %v\n%s", err, combined)
	}
	wantOut, err := exec.Command(bundleOut).Output()
	if err != nil {
		t.Fatalf("run bundle binary: %v", err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %v\n%s", err, combined)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir()
	gotOut, err := run.Output()
	if err != nil {
		t.Fatalf("run native binary: %v", err)
	}
	if string(gotOut) != string(wantOut) {
		t.Fatalf("native output != bundle\n--- want ---\n%q\n--- got ---\n%q", wantOut, gotOut)
	}
}

// TestBuildNativeDateTimeObject transpiles an exported main using the datetime
// OO handles (Instant chained add, diff -> Duration, format, parts/inZone dicts,
// Zone), builds it offline with --native, and asserts its stdout is
// byte-identical to the bundled VM build.
func TestBuildNativeDateTimeObject(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;
import datetime;

export func main(): int {
    let a = datetime.Instant(1700000000);
    io.println(a.toString());
    io.println(a.year());
    io.println(a.weekday());
    let b = a.addDays(10).addSeconds(3600);
    io.println(b.toString());
    let span = datetime.Instant(1700086400).diff(a);
    io.println(span.toString());
    io.println(span.inSeconds());
    io.println(a.format("%Y-%m-%d"));
    io.println(a.parts());
    io.println(a.inZone("UTC"));
    let z = datetime.Zone("UTC");
    io.println(z.name());
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(dir, "bundle")
	bundleBuild := exec.Command(bin, "build", "--entry", "main", "--out", bundleOut, ".")
	bundleBuild.Dir = dir
	if combined, err := bundleBuild.CombinedOutput(); err != nil {
		t.Fatalf("bundle build failed: %v\n%s", err, combined)
	}
	wantOut, err := exec.Command(bundleOut).Output()
	if err != nil {
		t.Fatalf("run bundle binary: %v", err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %v\n%s", err, combined)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir()
	gotOut, err := run.Output()
	if err != nil {
		t.Fatalf("run native binary: %v", err)
	}
	if string(gotOut) != string(wantOut) {
		t.Fatalf("native output != bundle\n--- want ---\n%q\n--- got ---\n%q", wantOut, gotOut)
	}
}

// TestBuildNativeTemplate transpiles an exported main using the template module
// (renderString with HTML auto-escaping, a Template handle), builds it offline
// with --native, and asserts byte-identical stdout against the bundled VM build.
func TestBuildNativeTemplate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;
import template;

export func main(): int {
    io.println(template.renderString("Hi {{.name}}!", {"name": "<b>x</b>"}));
    io.println(template.renderString("{{range .xs}}[{{.}}]{{end}}", {"xs": ["a", "b"]}));
    let t = template.Template("n={{.n}} on={{.on}}", "g");
    io.println(t.name());
    io.println(t.render({"n": 7, "on": true}));
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(dir, "bundle")
	bundleBuild := exec.Command(bin, "build", "--entry", "main", "--out", bundleOut, ".")
	bundleBuild.Dir = dir
	if combined, err := bundleBuild.CombinedOutput(); err != nil {
		t.Fatalf("bundle build failed: %v\n%s", err, combined)
	}
	wantOut, err := exec.Command(bundleOut).Output()
	if err != nil {
		t.Fatalf("run bundle binary: %v", err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %v\n%s", err, combined)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir()
	gotOut, err := run.Output()
	if err != nil {
		t.Fatalf("run native binary: %v", err)
	}
	if string(gotOut) != string(wantOut) {
		t.Fatalf("native output != bundle\n--- want ---\n%q\n--- got ---\n%q", wantOut, gotOut)
	}
}

// TestBuildNativeFailsLoudlyOnUnicode asserts the unicode module (backed by
// golang.org/x/text, non-stdlib) has no zero-dep bridge: --native must diagnose
// and exit non-zero, never miscompile to a wrong result.
func TestBuildNativeFailsLoudlyOnUnicode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;
import unicode;

export func main(): int {
    io.println(unicode.normalize("a", "NFC"));
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	combined, err := build.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for unicode native build, got success\n%s", combined)
	}
	if !strings.Contains(string(combined), "unicode") {
		t.Errorf("fail-loud output should name the unbridged call\noutput: %s", combined)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("unbridged build should produce no binary, but %s exists", out)
	}
}

// TestBuildNativeFailsLoudlyOnUnsupported builds a program using a construct
// the native compiler does not support (a set literal); --native must exit
// non-zero, name the unsupported construct, print the VM fallback hint, and
// leave no output binary behind.
func TestBuildNativeFailsLoudlyOnUnsupported(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;

export func main(): int {
    let s = {1, 2, 3};
    io.println(s.length());
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	combined, err := build.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success\n%s", combined)
	}

	msg := string(combined)
	for _, want := range []string{
		"set literals",
		"main.gb:4:13",
		"use 'geblang build' for the bundled VM binary",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("fail-loud output missing %q\noutput: %s", want, msg)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("unsupported build should produce no binary, but %s exists", out)
	}
}

// TestBuildNativeEnumMethods transpiles an exported main using an untagged enum
// with instance methods (one using `match (this)`, one calling a sibling) that
// implements an interface, dispatched both directly on a variant and through an
// interface-typed parameter, builds it offline with --native, and asserts
// byte-identical stdout against the bundled VM build.
func TestBuildNativeEnumMethods(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;

interface Describable {
    func describe(): string;
}

enum Status implements Describable {
    Active, Suspended, Closed;

    func describe(): string {
        return match (this) {
            case Status.Active => "active";
            case Status.Suspended => "suspended";
            default => "closed";
        };
    }

    func isTerminal(): bool {
        return match (this) {
            case Status.Closed => true;
            default => false;
        };
    }

    func loud(): string { return this.describe() + "!"; }
}

func report(Describable d): string { return d.describe(); }

export func main(): int {
    for (Status s in [Status.Active, Status.Suspended, Status.Closed]) {
        io.println(s.describe() + " terminal=" + (s.isTerminal() as string));
    }
    io.println(Status.Closed.loud());
    Describable d = Status.Suspended;
    io.println(report(d));
    io.println(report(Status.Active));
    io.println(Status.Active);
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(dir, "bundle")
	bundleBuild := exec.Command(bin, "build", "--entry", "main", "--out", bundleOut, ".")
	bundleBuild.Dir = dir
	if combined, err := bundleBuild.CombinedOutput(); err != nil {
		t.Fatalf("bundle build failed: %v\n%s", err, combined)
	}
	wantOut, err := exec.Command(bundleOut).Output()
	if err != nil {
		t.Fatalf("run bundle binary: %v", err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %v\n%s", err, combined)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir()
	gotOut, err := run.Output()
	if err != nil {
		t.Fatalf("run native binary: %v", err)
	}
	if string(gotOut) != string(wantOut) {
		t.Fatalf("native output != bundle\n--- want ---\n%q\n--- got ---\n%q", wantOut, gotOut)
	}
}

// TestBuildNativeAnyHof transpiles an exported main that json-parses an array
// and runs map/filter/reduce/find/any/all/flatMap with any-typed callbacks
// (casting inside the body, chained, and a method on the result), builds it
// offline with --native, and asserts byte-identical stdout to the bundled VM.
func TestBuildNativeAnyHof(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;
import json;

export func main(): int {
    let data = json.parse("{\"nums\":[1,2,3,4,5,6]}");
    io.println(data["nums"].map(func(any n): any { return (n as int) * 2; }));
    io.println(data["nums"].filter(func(any n): bool { return (n as int) % 2 == 0; }));
    io.println(data["nums"].reduce(func(any acc, any n): any { return (acc as int) + (n as int); }, 0));
    io.println(data["nums"].find(func(any n): bool { return (n as int) > 4; }));
    io.println(data["nums"].any(func(any n): bool { return (n as int) > 5; }));
    io.println(data["nums"].all(func(any n): bool { return (n as int) > 0; }));
    io.println(data["nums"].flatMap(func(any n): any { return [n, (n as int) * 10]; }));
    io.println(data["nums"].map(func(any n): any { return (n as int) + 1; }).filter(func(any n): bool { return (n as int) % 2 == 0; }));
    io.println(data["nums"].map(func(any n): any { return (n as int) * (n as int); }).length());
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(dir, "bundle")
	bundleBuild := exec.Command(bin, "build", "--entry", "main", "--out", bundleOut, ".")
	bundleBuild.Dir = dir
	if combined, err := bundleBuild.CombinedOutput(); err != nil {
		t.Fatalf("bundle build failed: %v\n%s", err, combined)
	}
	wantOut, err := exec.Command(bundleOut).Output()
	if err != nil {
		t.Fatalf("run bundle binary: %v", err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %v\n%s", err, combined)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir()
	gotOut, err := run.Output()
	if err != nil {
		t.Fatalf("run native binary: %v", err)
	}
	if string(gotOut) != string(wantOut) {
		t.Fatalf("native output != bundle\n--- want ---\n%q\n--- got ---\n%q", wantOut, gotOut)
	}
}

// writeEntryPkg writes a single-module package with the given entry source and
// returns the package dir. The module is named "app".
func writeEntryPkg(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "geblang.yaml"), []byte("name: app\nsource: src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "app.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func cmdExitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

// TestBuildRequiresExportedMain asserts the unified entry convention: an entry
// module without an exported main is a build error on BOTH the bundle and the
// --native path, exits non-zero, names the expected signature, and writes no
// output binary.
func TestBuildRequiresExportedMain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	src := `module app;
import io;
io.println("top level only, no exported main");
`
	for _, native := range []bool{false, true} {
		name := "bundle"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			dir := writeEntryPkg(t, src)
			out := filepath.Join(dir, "outbin", "app")
			var a []string
			if native {
				a = []string{"build", "--native", "--entry", "app", "--out", out, "."}
			} else {
				a = []string{"build", "--entry", "app", "--out", out, "."}
			}
			cmd := exec.Command(bin, a...)
			cmd.Dir = dir
			cmd.Env = nativeBuildEnv()
			combined, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected non-zero exit on missing main, got success\n%s", combined)
			}
			msg := string(combined)
			for _, want := range []string{
				`entry module "app" does not export a main function`,
				"export func main(list<string> args)",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("error output missing %q\noutput: %s", want, msg)
				}
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("missing-main build should produce no binary, but %s exists", out)
			}
		})
	}
}

// TestBuildEntryMainArgsAndExitCode proves the args-taking, int-returning entry
// main builds and runs on both the bundle and native paths, receives the
// process args, and surfaces its int return as the exit code.
func TestBuildEntryMainArgsAndExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	src := `module app;
import io;
export func main(list<string> args): int {
    io.println("argc=" + (args.length() as string));
    if (args.length() > 0) {
        io.println("arg0=" + args[0]);
    }
    return 7;
}
`
	for _, native := range []bool{false, true} {
		name := "bundle"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			dir := writeEntryPkg(t, src)
			out := filepath.Join(dir, "outbin", "app")
			var a []string
			if native {
				a = []string{"build", "--native", "--entry", "app", "--out", out, "."}
			} else {
				a = []string{"build", "--entry", "app", "--out", out, "."}
			}
			build := exec.Command(bin, a...)
			build.Dir = dir
			build.Env = nativeBuildEnv()
			if combined, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, combined)
			}
			run := exec.Command(out, "hello", "world")
			run.Dir = t.TempDir()
			combined, err := run.CombinedOutput()
			if code := cmdExitCode(err); code != 7 {
				t.Fatalf("exit code = %d, want 7\noutput: %s", code, combined)
			}
			got := string(combined)
			for _, want := range []string{"argc=2", "arg0=hello"} {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\noutput: %s", want, got)
				}
			}
		})
	}
}

// TestBuildEntryMainZeroArg proves a zero-parameter exported main builds and
// runs on both paths (the launcher calls main() with no args).
func TestBuildEntryMainZeroArg(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	src := `module app;
import io;
export func main() {
    io.println("zero-arg main ran");
}
`
	for _, native := range []bool{false, true} {
		name := "bundle"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			dir := writeEntryPkg(t, src)
			out := filepath.Join(dir, "outbin", "app")
			var a []string
			if native {
				a = []string{"build", "--native", "--entry", "app", "--out", out, "."}
			} else {
				a = []string{"build", "--entry", "app", "--out", out, "."}
			}
			build := exec.Command(bin, a...)
			build.Dir = dir
			build.Env = nativeBuildEnv()
			if combined, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, combined)
			}
			run := exec.Command(out)
			run.Dir = t.TempDir()
			combined, err := run.CombinedOutput()
			if err != nil {
				t.Fatalf("run failed: %v\n%s", err, combined)
			}
			if !strings.Contains(string(combined), "zero-arg main ran") {
				t.Errorf("output missing zero-arg main marker\noutput: %s", combined)
			}
		})
	}
}

// TestBuildNativeAnyMethods transpiles an exported main that json-parses an
// object and calls methods on the navigated any values (dict keys/length/get,
// list length/contains/join, string upper, a chained any-method), builds it
// offline with --native, and asserts byte-identical stdout to the bundled VM.
func TestBuildNativeAnyMethods(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping native build e2e in -short mode")
	}
	bin := buildNativeTestGeblang(t)

	dir := t.TempDir()
	src := `import io;
import json;

export func main(): int {
    let data = json.parse("{\"name\":\"widget\",\"qty\":12,\"tags\":[\"a\",\"b\",\"c\"]}");
    io.println(data.length());
    io.println(data.keys());
    io.println(data.get("name"));
    io.println(data.hasKey("qty"));
    io.println(data["tags"].length());
    io.println(data["tags"].contains("b"));
    io.println(data["tags"].join("-"));
    io.println(data["name"].upper());
    io.println(data["name"].upper().length());
    io.println(data.keys()[0]);
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleOut := filepath.Join(dir, "bundle")
	bundleBuild := exec.Command(bin, "build", "--entry", "main", "--out", bundleOut, ".")
	bundleBuild.Dir = dir
	if combined, err := bundleBuild.CombinedOutput(); err != nil {
		t.Fatalf("bundle build failed: %v\n%s", err, combined)
	}
	wantOut, err := exec.Command(bundleOut).Output()
	if err != nil {
		t.Fatalf("run bundle binary: %v", err)
	}

	out := filepath.Join(dir, "app")
	build := exec.Command(bin, "build", "--native", "--entry", "main", "--out", out, ".")
	build.Dir = dir
	build.Env = nativeBuildEnv()
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %v\n%s", err, combined)
	}

	run := exec.Command(out)
	run.Dir = t.TempDir()
	gotOut, err := run.Output()
	if err != nil {
		t.Fatalf("run native binary: %v", err)
	}
	if string(gotOut) != string(wantOut) {
		t.Fatalf("native output != bundle\n--- want ---\n%q\n--- got ---\n%q", wantOut, gotOut)
	}
}
