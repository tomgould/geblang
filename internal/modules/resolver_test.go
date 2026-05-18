package modules_test

import (
	"os"
	"path/filepath"
	"testing"

	"geblang/internal/modules"
)

func TestResolverResolvesModulePathsAndPackageRoots(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(filepath.Join(src, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	modulePath := filepath.Join(src, "app", "math.gb")
	if err := os.WriteFile(modulePath, []byte("module app.math;"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`name: app
source: src
`)
	if err := os.WriteFile(filepath.Join(root, "geblang.yaml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := modules.NewResolver([]string{root})
	resolved, err := resolver.Resolve("app.math")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved != filepath.Clean(modulePath) {
		t.Fatalf("resolved: got %q, want %q", resolved, filepath.Clean(modulePath))
	}
}

func TestResolverResolvesLocalDependencyModules(t *testing.T) {
	root := t.TempDir()
	depRoot := filepath.Join(root, "dep")
	depSrc := filepath.Join(depRoot, "lib")
	if err := os.MkdirAll(depSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	modulePath := filepath.Join(depSrc, "util.gb")
	if err := os.WriteFile(modulePath, []byte("module dep.util;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depRoot, "geblang.yaml"), []byte("name: dep\nsource: lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`name: app
dependencies:
  dep:
    path: dep
`)
	if err := os.WriteFile(filepath.Join(root, "geblang.yaml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	resolver := modules.NewResolver([]string{root})
	resolved, err := resolver.Resolve("dep.util")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved != filepath.Clean(modulePath) {
		t.Fatalf("resolved: got %q, want %q", resolved, filepath.Clean(modulePath))
	}
}

func TestResolverSearchesStdlibBeforeGeblangPath(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	stdlibRoot := filepath.Join(root, "stdlib")
	envRoot := filepath.Join(root, "env")
	for _, dir := range []string{
		filepath.Join(appRoot, "pkg"),
		filepath.Join(stdlibRoot, "pkg"),
		filepath.Join(envRoot, "pkg"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	appModule := filepath.Join(appRoot, "pkg", "tool.gb")
	stdlibModule := filepath.Join(stdlibRoot, "pkg", "tool.gb")
	envModule := filepath.Join(envRoot, "pkg", "tool.gb")
	for path, source := range map[string]string{
		appModule:    "module pkg.tool; # app",
		stdlibModule: "module pkg.tool; # stdlib",
		envModule:    "module pkg.tool; # env",
	} {
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	t.Setenv("GEBLANG_PATH", envRoot)

	resolver := modules.NewResolver([]string{appRoot})
	resolver.StdlibPaths = []string{stdlibRoot}
	resolved, err := resolver.Resolve("pkg.tool")
	if err != nil {
		t.Fatalf("resolve app module: %v", err)
	}
	if resolved != filepath.Clean(appModule) {
		t.Fatalf("resolved app: got %q, want %q", resolved, filepath.Clean(appModule))
	}

	resolver = modules.NewResolver(nil)
	resolver.StdlibPaths = []string{stdlibRoot}
	resolved, err = resolver.Resolve("pkg.tool")
	if err != nil {
		t.Fatalf("resolve stdlib module: %v", err)
	}
	if resolved != filepath.Clean(stdlibModule) {
		t.Fatalf("resolved stdlib: got %q, want %q", resolved, filepath.Clean(stdlibModule))
	}

	resolver = modules.NewResolver(nil)
	resolver.DisableStdlib = true
	resolved, err = resolver.Resolve("pkg.tool")
	if err != nil {
		t.Fatalf("resolve env module: %v", err)
	}
	if resolved != filepath.Clean(envModule) {
		t.Fatalf("resolved env: got %q, want %q", resolved, filepath.Clean(envModule))
	}
}
