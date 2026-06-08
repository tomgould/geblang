package main

// go test ./cmd/geblang -bench BenchmarkCrossModule -run '^$' -benchtime=3x

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeCMBenchFixture writes 5 modules with classes + functions and a main
// that imports and uses all of them. Returns the directory and main.gb path.
func writeCMBenchFixture(tb testing.TB) (dir string, mainPath string) {
	tb.Helper()
	dir = tb.TempDir()
	for i := 0; i < 5; i++ {
		src := fmt.Sprintf(
			"module mod%d;\nexport class Item%d {}\nexport func make%d(): int { return %d; }\n",
			i, i, i, i,
		)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("mod%d.gb", i)), []byte(src), 0644); err != nil {
			tb.Fatal(err)
		}
	}
	var body string
	for i := 0; i < 5; i++ {
		body += fmt.Sprintf("import mod%d;\n", i)
	}
	body += "import io;\n"
	for i := 0; i < 5; i++ {
		body += fmt.Sprintf("func use%d(mod%d.Item%d x): int { return mod%d.make%d(); }\n", i, i, i, i, i)
	}
	body += "io.println(\"ready\");\n"
	mainPath = filepath.Join(dir, "main.gb")
	if err := os.WriteFile(mainPath, []byte(body), 0644); err != nil {
		tb.Fatal(err)
	}
	return dir, mainPath
}

// BenchmarkCrossModuleColdStart times a full cold run: cache absent so the
// binary must parse, analyze (cross-module), compile, write .gbc, and execute.
func BenchmarkCrossModuleColdStart(b *testing.B) {
	bin := buildCMBinary(b)
	dir, _ := writeCMBenchFixture(b)
	cacheDir := filepath.Join(dir, ".geblang-cache")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		os.RemoveAll(cacheDir)
		cmd := exec.Command(bin, "main.gb")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("cold run failed: %v\n%s", err, out)
		}
	}
}

// BenchmarkCrossModuleWarmCache times a warm run: .gbc already present so the
// binary loads the cached chunk and skips parse+analyze+compile.
func BenchmarkCrossModuleWarmCache(b *testing.B) {
	bin := buildCMBinary(b)
	dir, _ := writeCMBenchFixture(b)

	// Prime the cache before timing.
	prime := exec.Command(bin, "main.gb")
	prime.Dir = dir
	if out, err := prime.CombinedOutput(); err != nil {
		b.Fatalf("cache prime failed: %v\n%s", err, out)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := exec.Command(bin, "main.gb")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("warm run failed: %v\n%s", err, out)
		}
	}
}
