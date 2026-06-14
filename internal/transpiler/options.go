package transpiler

import "geblang/internal/transpiler/types"

type Options struct {
	PackageName string
	EntryModule string
	Strict      bool
	IntMode     types.IntMode
	// EntryMainWantsArgs/EntryMainReturnsInt describe the entry module's
	// exported main so the generated Go main() calls it with os.Args[1:] and
	// uses its int return as the process exit code.
	EntryMainWantsArgs  bool
	EntryMainReturnsInt bool
}
