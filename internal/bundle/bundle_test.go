package bundle

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func bundleFrom(t *testing.T, files map[string][]byte) *Bundle {
	t.Helper()
	return bundleFromManifest(t, Manifest{Version: "test", Entry: "main"}, files)
}

func bundleFromManifest(t *testing.T, manifest Manifest, files map[string][]byte) *Bundle {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, manifest, files); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw := buf.Bytes()
	zipSize := int64(binary.LittleEndian.Uint64(raw[len(raw)-TrailerSize : len(raw)-TrailerSize+8]))
	zipData := raw[int64(len(raw))-int64(TrailerSize)-zipSize : int64(len(raw))-int64(TrailerSize)]
	b, err := parseZip(zipData)
	if err != nil {
		t.Fatalf("parseZip: %v", err)
	}
	return b
}

func TestPermissionsRoundTrip(t *testing.T) {
	files := map[string][]byte{"src/main.gb": []byte("func main() {}")}
	want := &Permissions{FFI: []string{"/usr/lib/a.so", "/opt/*.so"}, Onnx: true, ProcessControl: true}

	b := bundleFromManifest(t, Manifest{Version: "test", Entry: "main", Permissions: want}, files)
	if !reflect.DeepEqual(b.Manifest.Permissions, want) {
		t.Fatalf("round-trip: got %+v, want %+v", b.Manifest.Permissions, want)
	}

	plain := bundleFromManifest(t, Manifest{Version: "test", Entry: "main"}, files)
	if plain.Manifest.Permissions != nil {
		t.Fatalf("a manifest with no permissions must decode as nil, got %+v", plain.Manifest.Permissions)
	}
}

// TestExtractAtomicPublish: ExtractTo publishes a complete dir, leaves no temp dirs, and skips on a second call.
func TestExtractAtomicPublish(t *testing.T) {
	b := bundleFrom(t, map[string][]byte{
		"src/main.gb": []byte("func main() {}"),
		"stdlib/a.gb": []byte("module a;"),
	})
	parent := t.TempDir()
	dir := filepath.Join(parent, "extract")
	if err := b.ExtractTo(dir, "test", ""); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stdlib", "a.gb")); err != nil {
		t.Fatalf("extracted file missing: %v", err)
	}
	entries, _ := os.ReadDir(parent)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".geblang-extract-") {
			t.Errorf("leftover temp extract dir: %s", e.Name())
		}
	}
	if err := b.ExtractTo(dir, "test", ""); err != nil {
		t.Fatalf("second extract (idempotent skip): %v", err)
	}
}

// TestResourceRoundTrip embeds a non-.gb resource at a project-relative path and
// confirms ExtractTo writes it back at the same relative path under the extract
// dir, so a bundled program reads it via sys.bundleDir()+"/templates/page.html".
func TestResourceRoundTrip(t *testing.T) {
	want := []byte("<h1>{{ title }}</h1>")
	files := map[string][]byte{
		"src/main.gb":         []byte("func main() {}"),
		"templates/page.html": want,
	}
	manifest := Manifest{Version: "test", Entry: "main"}

	var buf bytes.Buffer
	if err := Write(&buf, manifest, files); err != nil {
		t.Fatalf("write: %v", err)
	}

	raw := buf.Bytes()
	zipSize := int64(binary.LittleEndian.Uint64(raw[len(raw)-TrailerSize : len(raw)-TrailerSize+8]))
	zipData := raw[int64(len(raw))-int64(TrailerSize)-zipSize : int64(len(raw))-int64(TrailerSize)]

	b, err := parseZip(zipData)
	if err != nil {
		t.Fatalf("parseZip: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "extract")
	if err := b.ExtractTo(dir, "test", ""); err != nil {
		t.Fatalf("extract: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "templates", "page.html"))
	if err != nil {
		t.Fatalf("read embedded resource: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("resource content mismatch: got %q, want %q", got, want)
	}
}
