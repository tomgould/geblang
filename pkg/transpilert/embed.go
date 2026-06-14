package transpilert

import "embed"

// RuntimeSources is this package's own Go source, embedded so `geblang build
// --native` can vendor a self-contained, offline copy into transpiled output.
// transpilert is pure Go stdlib, so this is the only source the build vendors.
// Test files are present in the FS and skipped by the consumer when writing.
//
//go:embed *.go
var RuntimeSources embed.FS
