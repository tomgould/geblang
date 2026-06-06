package bundle

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

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
