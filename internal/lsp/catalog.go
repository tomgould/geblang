package lsp

import "geblang/internal/native"

// Aliases to the native-signature catalog, which moved to internal/native so the runtime can read native parameter names.
type (
	functionDoc = native.FunctionDoc
	moduleDoc   = native.ModuleDoc
)

var (
	fn                  = native.Fn
	globalBuiltins      = native.GlobalBuiltins
	testBaseMethods     = native.TestBaseMethods
	primitiveMethods    = native.CatalogPrimitiveMethods
	primitiveTypeNames  = native.PrimitiveTypeNames
	stdlibCatalog       = native.StdlibCatalog
	moduleNames         = native.CatalogModuleNames
	lookupClassMethods  = native.LookupClassMethods
	globalBuiltinDoc    = native.GlobalBuiltinDoc
	splitQualifiedClass = native.SplitQualifiedClass
)
