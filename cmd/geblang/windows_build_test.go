package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestWindowsCrossCompiles guards that the whole module still builds for Windows.
func TestWindowsCrossCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cross-compile guard in -short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("native build already covers this platform")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("module does not build for windows/amd64:\n%s", out)
	}
}
