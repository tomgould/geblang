package lower_test

import (
	"strings"
	"testing"
)

// yaml/toml use non-stdlib backends (gopkg.in/yaml.v3, BurntSushi/toml); uuid is
// non-stdlib + non-reproducible. They must diagnose cleanly rather than emit an
// approximating bridge. (xml is now bridged over encoding/xml; see TestXMLModuleLower.)
func TestUnbridgedSerializersDiagnose(t *testing.T) {
	cases := []struct {
		name string
		src  string
		bad  string
	}{
		{"yaml", "import io;\nimport yaml;\nlet s = yaml.stringify({\"a\": 1});\nio.println(s);\n", "YAMLStringify"},
		{"toml", "import io;\nimport toml;\nlet s = toml.stringify({\"a\": 1});\nio.println(s);\n", "TOMLStringify"},
		{"uuid", "import io;\nimport uuid;\nlet s = uuid.v4();\nio.println(s);\n", "UUIDV4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := lowerSource(t, c.src)
			if len(l.Errors()) == 0 {
				t.Fatalf("expected %s to diagnose, got none", c.name)
			}
			if strings.Contains(string(l.Module.Render()), c.bad) {
				t.Fatalf("%s must not emit an approximating bridge (%s)", c.name, c.bad)
			}
		})
	}
}

// bytes module + bytes-value methods route to transpilert helpers, not raw Go.
func TestBytesBridgeLowers(t *testing.T) {
	src := `import io;
import bytes;
let b = bytes.fromString("hi");
io.println(b.toHex());
io.println(bytes.toBase64(b));
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	for _, want := range []string{"transpilert.BytesFromString", "transpilert.BytesToHex", "transpilert.BytesToBase64"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %s in output:\n%s", want, got)
		}
	}
}

func TestURLAndCSVBridgeLowers(t *testing.T) {
	src := `import io;
import url;
import csv;
io.println(url.encode("a b"));
let parts = url.parse("https://x/y?q=1");
io.println(parts.get("host"));
let rows = csv.parse("a,b\n1,2");
io.println(rows.length());
io.println(csv.stringify(rows));
`
	l := lowerSource(t, src)
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	got := string(l.Module.Render())
	for _, want := range []string{"transpilert.URLEncode", "transpilert.URLParse", "transpilert.CSVParse", "transpilert.CSVStringify"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %s in output:\n%s", want, got)
		}
	}
}
