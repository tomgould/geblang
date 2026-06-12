package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildDockerfileGeneration covers `geblang build --docker`: the
// Dockerfile lands beside the binary, EXPOSE is port-gated, and an
// existing Dockerfile is preserved unless --force.
func TestBuildDockerfileGeneration(t *testing.T) {
	bin := buildGeblangBinary(t, false)
	dir := t.TempDir()
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("geblang.yaml", "name: dockapp\nversion: 0.1.0\n")
	write("app.gb", `module app;
import io;

export func main(list<string> args): int {
    io.println("hi");
    return 0;
}
`)
	out := filepath.Join(dir, "out", "dockapp")
	run := func(args ...string) string {
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		outBytes, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run %v: %v\n%s", args, err, outBytes)
		}
		return string(outBytes)
	}

	run("build", "--entry", "app", "--out", out, "--docker", "--docker-port", "8085", ".")
	dockerfile := filepath.Join(dir, "out", "Dockerfile")
	content, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	df := string(content)
	for _, want := range []string{
		"FROM gcr.io/distroless/base-debian12",
		"COPY dockapp /app",
		"COPY dockapp.NOTICES.txt /app.NOTICES.txt",
		"EXPOSE 8085",
		"ENTRYPOINT [\"/app\"]",
	} {
		if !strings.Contains(df, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, df)
		}
	}

	// Preserve without --force.
	if err := os.WriteFile(dockerfile, []byte("# custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := run("build", "--entry", "app", "--out", out, "--docker", ".")
	if !strings.Contains(got, "left unchanged") {
		t.Fatalf("expected preserve notice, got:\n%s", got)
	}
	content, _ = os.ReadFile(dockerfile)
	if string(content) != "# custom\n" {
		t.Fatalf("custom Dockerfile was overwritten:\n%s", content)
	}

	// --force regenerates; no port means no EXPOSE.
	run("build", "--entry", "app", "--out", out, "--docker", "--force", ".")
	content, _ = os.ReadFile(dockerfile)
	if strings.Contains(string(content), "EXPOSE") {
		t.Fatalf("EXPOSE present without --docker-port:\n%s", content)
	}
	if !strings.Contains(string(content), "ENTRYPOINT [\"/app\"]") {
		t.Fatalf("regenerated Dockerfile malformed:\n%s", content)
	}
}
