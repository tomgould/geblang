package native

// BareBuiltins is the canonical set of builtins invoked as bare calls
// (not module members): special forms the parser/compiler/evaluator
// recognise directly. Each has its own dispatch (distinct opcodes /
// handlers), so this list is not a dispatch table - it is the single
// source of truth for *which* names are bare builtins, used by the
// guard test that asserts every one is recognised on both backends
// (so a name handled on only one backend, as dump and dir once were,
// fails the build).
var BareBuiltins = []string{
	"assert",
	"dir",
	"dump",
	"parent",
	"range",
	"typeof",
	"zrange",
}
