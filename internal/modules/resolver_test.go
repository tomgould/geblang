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

func TestResolverResolvesAbsolutePathDependency(t *testing.T) {
	depRoot := t.TempDir()
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

	root := t.TempDir()
	t.Setenv("GEBLANG_DEP_DIR", depRoot)
	for label, depPath := range map[string]string{
		"absolute": depRoot,
		"env":      "$GEBLANG_DEP_DIR",
	} {
		manifest := []byte("name: app\ndependencies:\n  dep:\n    path: " + depPath + "\n")
		if err := os.WriteFile(filepath.Join(root, "geblang.yaml"), manifest, 0o644); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		resolver := modules.NewResolver([]string{root})
		resolved, err := resolver.Resolve("dep.util")
		if err != nil {
			t.Fatalf("%s: resolve: %v", label, err)
		}
		if resolved != filepath.Clean(modulePath) {
			t.Fatalf("%s: got %q, want %q", label, resolved, filepath.Clean(modulePath))
		}
	}
}

func TestResolverReservedNamesResolveToStdlib(t *testing.T) {
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
	// `pkg` exists on the stdlib path, so it is a reserved built-in name: a user
	// or GEBLANG_PATH file may not shadow it; the stdlib copy always wins.
	stdlibModule := filepath.Join(stdlibRoot, "pkg", "tool.gb")
	for path, source := range map[string]string{
		filepath.Join(appRoot, "pkg", "tool.gb"): "module pkg.tool; # app",
		stdlibModule:                             "module pkg.tool; # stdlib",
		filepath.Join(envRoot, "pkg", "tool.gb"): "module pkg.tool; # env",
	} {
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	t.Setenv("GEBLANG_PATH", envRoot)

	// Reserved name: stdlib wins over the app's own ModulePaths.
	resolver := modules.NewResolver([]string{appRoot})
	resolver.StdlibPaths = []string{stdlibRoot}
	resolved, err := resolver.Resolve("pkg.tool")
	if err != nil {
		t.Fatalf("resolve reserved module: %v", err)
	}
	if resolved != filepath.Clean(stdlibModule) {
		t.Fatalf("reserved name should resolve to stdlib: got %q, want %q", resolved, filepath.Clean(stdlibModule))
	}

	// `import geblang.pkg.tool` resolves the same built-in explicitly.
	resolved, err = resolver.Resolve("geblang.pkg.tool")
	if err != nil {
		t.Fatalf("resolve geblang-prefixed module: %v", err)
	}
	if resolved != filepath.Clean(stdlibModule) {
		t.Fatalf("geblang.pkg.tool should resolve to stdlib: got %q", resolved)
	}

	// A non-reserved user module still follows ModulePaths before GEBLANG_PATH.
	appWidget := filepath.Join(appRoot, "widget.gb")
	if err := os.WriteFile(appWidget, []byte("module widget; # app"), 0o644); err != nil {
		t.Fatalf("write widget: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envRoot, "widget.gb"), []byte("module widget; # env"), 0o644); err != nil {
		t.Fatalf("write env widget: %v", err)
	}
	resolved, err = resolver.Resolve("widget")
	if err != nil {
		t.Fatalf("resolve user module: %v", err)
	}
	if resolved != filepath.Clean(appWidget) {
		t.Fatalf("non-reserved name should resolve from ModulePaths: got %q, want %q", resolved, filepath.Clean(appWidget))
	}
}
