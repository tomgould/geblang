package bytecode

import (
	"encoding/hex"
	"path/filepath"
)

func CachePath(cacheDir string, sourcePath string, source []byte, compiler string) string {
	hash := SourceHash(append([]byte(compiler+"\x00"+sourcePath+"\x00"), source...))
	return filepath.Join(cacheDir, hex.EncodeToString(hash[:])+".gbc")
}
