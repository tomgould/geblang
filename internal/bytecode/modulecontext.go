package bytecode

// ModuleContext is the dispatch loop's view of a VM's module (chunk + class index); a VM never switches it (it always aliases vm.chunk), so it is a stable indirection, not a live module-switching handle.
type ModuleContext struct {
	Chunk      Chunk
	classIndex map[string]int
}
