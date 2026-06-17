package modules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeGitURL(t *testing.T) {
	cases := map[string]string{
		"github.com/dwgebler/gebweb":      "https://github.com/dwgebler/gebweb",
		"github.com/acme/httplib.git":     "https://github.com/acme/httplib.git",
		"https://github.com/acme/httplib": "https://github.com/acme/httplib",
		"http://example.com/x":            "http://example.com/x",
		"ssh://git@github.com/acme/x.git": "ssh://git@github.com/acme/x.git",
		"git@github.com:acme/httplib.git": "git@github.com:acme/httplib.git",
		"git@github.com:acme/httplib":     "git@github.com:acme/httplib",
		"  github.com/acme/x  ":           "https://github.com/acme/x",
		"":                                "",
	}
	for in, want := range cases {
		if got := normalizeGitURL(in); got != want {
			t.Errorf("normalizeGitURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDependencyPath(t *testing.T) {
	root := "/pkg/root"
	abs := filepath.Join(t.TempDir(), "dep")

	if got := resolveDependencyPath(root, "../dep"); got != filepath.Clean("/pkg/dep") {
		t.Errorf("relative: got %q", got)
	}
	if got := resolveDependencyPath(root, abs); got != filepath.Clean(abs) {
		t.Errorf("absolute: got %q, want %q", got, abs)
	}

	t.Setenv("GEBLANG_DEP_DIR", abs)
	if got := resolveDependencyPath(root, "$GEBLANG_DEP_DIR"); got != filepath.Clean(abs) {
		t.Errorf("env: got %q, want %q", got, abs)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	if got := resolveDependencyPath(root, "~/pkgs/lib"); got != filepath.Join(home, "pkgs", "lib") {
		t.Errorf("home: got %q, want %q", got, filepath.Join(home, "pkgs", "lib"))
	}
}
