package bytecode

import (
	"encoding/hex"
	"path/filepath"
	"strconv"
)

func CachePath(cacheDir string, sourcePath string, source []byte, compiler string) string {
	return cachePathForVersion(cacheDir, sourcePath, source, compiler, Version)
}

func cachePathForVersion(cacheDir string, sourcePath string, source []byte, compiler string, chunkVersion uint16) string {
	key := []byte(compiler + "\x00" + strconv.FormatUint(uint64(chunkVersion), 10) + "\x00" + sourcePath + "\x00")
	hash := SourceHash(append(key, source...))
	return filepath.Join(cacheDir, hex.EncodeToString(hash[:])+".gbc")
}
