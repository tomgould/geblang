package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A built binary carries capabilities baked at build time (geblang.yaml permissions block and/or build --allow-* flags), so a gated op runs with no launch flag. Gate state shows in the error class: closed throws PermissionError, open fails for another reason (no ORT / no library).
func TestBuiltBinaryCapabilities(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "geblang")
	if o, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build geblang: %v\n%s", err, o)
	}

	onnxApp := `module app;
import onnx;
import io;
export func main(): int {
    try { onnx.session("/no/such/model.onnx"); io.println("opened"); }
    catch (PermissionError e) { io.println("GATE-CLOSED"); }
    catch (Error e) { io.println("GATE-OPEN"); }
    return 0;
}
`
	ffiApp := `module app;
import ffi;
import io;
export func main(): int {
    try { ffi.dlopen("/no/such/lib.so"); io.println("opened"); }
    catch (PermissionError e) { io.println("GATE-CLOSED"); }
    catch (Error e) { io.println("GATE-OPEN"); }
    return 0;
}
`
	browserApp := `module app;
import browser;
import io;
export func main(): int {
    try { browser.launch({"executable": "/no/such/chrome"}); io.println("opened"); }
    catch (PermissionError e) { io.println("GATE-CLOSED"); }
    catch (Error e) { io.println("GATE-OPEN"); }
    return 0;
}
`
	buildAndRun := func(t *testing.T, yaml, app string, buildFlags ...string) string {
		t.Helper()
		dir := t.TempDir()
		writePermFile(t, filepath.Join(dir, "geblang.yaml"), yaml)
		writePermFile(t, filepath.Join(dir, "app.gb"), app)
		out := filepath.Join(dir, "app")
		args := append([]string{"build", "--entry", "app", "--out", out}, buildFlags...)
		args = append(args, dir)
		if o, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
			t.Fatalf("geblang build: %v\n%s", err, o)
		}
		o, _ := exec.Command(out).CombinedOutput()
		return string(o)
	}

	cases := []struct {
		name  string
		yaml  string
		app   string
		flags []string
		want  string
	}{
		{"onnx denied by default", "name: app\n", onnxApp, nil, "GATE-CLOSED"},
		{"onnx via build flag", "name: app\n", onnxApp, []string{"--allow-onnx"}, "GATE-OPEN"},
		{"onnx via manifest", "name: app\npermissions:\n  onnx: true\n", onnxApp, nil, "GATE-OPEN"},
		{"ffi denied by default", "name: app\n", ffiApp, nil, "GATE-CLOSED"},
		{"ffi via build flag", "name: app\n", ffiApp, []string{"--allow-ffi", "/no/such/*.so"}, "GATE-OPEN"},
		{"ffi via manifest", "name: app\npermissions:\n  ffi:\n    enabled: true\n    libraries:\n      - glob: /no/such/*.so\n", ffiApp, nil, "GATE-OPEN"},
		{"browser denied by default", "name: app\n", browserApp, nil, "GATE-CLOSED"},
		{"browser via build flag", "name: app\n", browserApp, []string{"--allow-browser"}, "GATE-OPEN"},
		{"browser via manifest", "name: app\npermissions:\n  browser: true\n", browserApp, nil, "GATE-OPEN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := buildAndRun(t, c.yaml, c.app, c.flags...); !strings.Contains(got, c.want) {
				t.Fatalf("want %s, got %q", c.want, got)
			}
		})
	}

	devScript := `import onnx;
import io;
try { onnx.session("/no/such/model.onnx"); io.println("opened"); }
catch (PermissionError e) { io.println("GATE-CLOSED"); }
catch (Error e) { io.println("GATE-OPEN"); }
`
	devRun := func(t *testing.T, yaml string) string {
		t.Helper()
		dir := t.TempDir()
		writePermFile(t, filepath.Join(dir, "geblang.yaml"), yaml)
		script := filepath.Join(dir, "run.gb")
		writePermFile(t, script, devScript)
		o, _ := exec.Command(bin, script).CombinedOutput()
		return string(o)
	}
	t.Run("dev-time onnx denied by default", func(t *testing.T) {
		if got := devRun(t, "name: app\n"); !strings.Contains(got, "GATE-CLOSED") {
			t.Fatalf("want GATE-CLOSED, got %q", got)
		}
	})
	t.Run("dev-time onnx via manifest", func(t *testing.T) {
		if got := devRun(t, "name: app\npermissions:\n  onnx: true\n"); !strings.Contains(got, "GATE-OPEN") {
			t.Fatalf("want GATE-OPEN, got %q", got)
		}
	})
}

func writePermFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
