// Package geblang holds the source stdlib embedded into the binary, used as the last-resort module-resolution fallback for self-contained binaries (a bare global install with no stdlib on disk).
package geblang

import "embed"

//go:embed stdlib
var StdlibFS embed.FS
