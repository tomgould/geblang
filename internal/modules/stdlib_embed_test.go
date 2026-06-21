package modules

import (
	"os"
	"path/filepath"
	"testing"

	rootembed "geblang"
	"geblang/internal/version"
)

func TestEmbeddedStdlibContainsSourceModules(t *testing.T) {
	for _, p := range []string{"stdlib/llm.gb", "stdlib/llm/openai.gb", "stdlib/rag.gb", "stdlib/geblang.yaml"} {
		if _, err := rootembed.StdlibFS.ReadFile(p); err != nil {
			t.Errorf("embedded stdlib missing %s: %v", p, err)
		}
	}
}

func TestEnsureEmbeddedStdlibExtracts(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	dir := ensureEmbeddedStdlib()
	if dir == "" {
		t.Fatal("ensureEmbeddedStdlib returned empty")
	}
	if base := filepath.Base(dir); base != "stdlib-"+version.Geblang {
		t.Errorf("cache dir not version-scoped: %s", base)
	}
	for _, rel := range []string{"llm.gb", filepath.Join("llm", "openai.gb"), "geblang.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("extracted stdlib missing %s: %v", rel, err)
		}
	}
	if again := ensureEmbeddedStdlib(); again != dir {
		t.Errorf("second call should reuse the cache: %s != %s", again, dir)
	}
}
