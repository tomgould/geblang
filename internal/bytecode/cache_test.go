package bytecode

import "testing"

func TestCachePathIncludesChunkVersion(t *testing.T) {
	source := []byte("import io;\nio.println(1);\n")
	a := cachePathForVersion("/tmp/cache", "/proj/main.gb", source, "1.5.0", 60)
	b := cachePathForVersion("/tmp/cache", "/proj/main.gb", source, "1.5.0", 61)
	if a == b {
		t.Fatalf("chunk-format Version should change the cache key")
	}
}
