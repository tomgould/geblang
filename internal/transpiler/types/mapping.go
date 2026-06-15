package types

import "geblang/internal/ast"

type Kind int

const (
	KindUnknown Kind = iota
	KindAny
	KindNull
	KindBool
	KindInt
	KindFloat
	KindDecimal
	KindString
	KindBytes
	KindList
	KindDict
	KindSet
	KindFunc
	KindClass
	KindInterface
	KindEnum
	KindGenerator
	KindTask
	KindTypeParam
	// KindVoid is a function/method `: void` return annotation; it has no Go
	// return type (the function returns nothing).
	KindVoid
)

type Type struct {
	Kind     Kind
	Name     string
	Nullable bool
	Elem     *Type
	Key      *Type
	Value    *Type
	Params   []*Type
	Result   *Type
	// EnumScalar marks an untagged enum (Go int-based), whose nullable form
	// needs a pointer wrapper; tagged enums are Go interfaces (already nil-able).
	EnumScalar bool
}

func Any() *Type     { return &Type{Kind: KindAny} }
func Unknown() *Type { return &Type{Kind: KindUnknown} }

// RePatternName is the reserved class name for re.compile's opaque Pattern. The
// dot cannot collide with a user class (emit.MangleIdent never produces one).
const RePatternName = "re.Pattern"

// URLValueName is the reserved class name for url.URL's opaque value handle.
const URLValueName = "url.URL"

// Reserved class names for the opaque datetime handles. The dots cannot collide
// with a user class (emit.MangleIdent never produces one).
const (
	DateTimeInstantName  = "datetime.Instant"
	DateTimeDurationName = "datetime.Duration"
	DateTimeZoneName     = "datetime.Zone"
)

// Reserved class names for the opaque template handles.
const (
	TemplateValueName  = "template.Template"
	TemplateEngineName = "template.Engine"
)

func FromAST(t *ast.TypeRef) *Type {
	if t == nil {
		return Unknown()
	}
	if t.Operator != "" {
		return Any()
	}
	out := &Type{Nullable: t.Nullable}
	switch t.Name {
	case "int":
		out.Kind = KindInt
	case "float":
		out.Kind = KindFloat
	case "decimal":
		out.Kind = KindDecimal
	case "string":
		out.Kind = KindString
	case "bool":
		out.Kind = KindBool
	case "bytes":
		out.Kind = KindBytes
	case "null":
		out.Kind = KindNull
	case "void":
		out.Kind = KindVoid
	case "any":
		out.Kind = KindAny
	case "list":
		out.Kind = KindList
		if len(t.Arguments) > 0 {
			out.Elem = FromAST(t.Arguments[0])
		} else {
			out.Elem = Any()
		}
	case "dict":
		out.Kind = KindDict
		if len(t.Arguments) >= 2 {
			out.Key = FromAST(t.Arguments[0])
			out.Value = FromAST(t.Arguments[1])
		} else {
			out.Key = &Type{Kind: KindString}
			out.Value = Any()
		}
	case "set":
		out.Kind = KindSet
		if len(t.Arguments) > 0 {
			out.Elem = FromAST(t.Arguments[0])
		} else {
			out.Elem = Any()
		}
	case "generator", "iterable":
		out.Kind = KindGenerator
		if len(t.Arguments) > 0 {
			out.Elem = FromAST(t.Arguments[0])
		} else {
			out.Elem = Any()
		}
	case "Task":
		out.Kind = KindTask
		if len(t.Arguments) > 0 {
			out.Elem = FromAST(t.Arguments[0])
		} else {
			out.Elem = Any()
		}
	case "":
		out.Kind = KindUnknown
	default:
		out.Kind = KindClass
		out.Name = t.Name
		for _, arg := range t.Arguments {
			out.Params = append(out.Params, FromAST(arg))
		}
	}
	return out
}

type GoType struct {
	Source     string
	ImportPath string
	// Imports holds extra paths for composite types one ImportPath cannot express.
	Imports []string
}

// OrderedDictImport is the public runtime package that backs Geblang dicts.
const OrderedDictImport = "geblang/pkg/transpilert"

// AllImports returns every import path the type depends on.
func (g GoType) AllImports() []string {
	out := g.Imports
	if g.ImportPath != "" {
		out = append([]string{g.ImportPath}, out...)
	}
	return out
}

func ToGo(t *Type, intMode IntMode) GoType {
	if t == nil {
		return GoType{Source: "any"}
	}
	if t.Nullable && nullablePtrKind(t) {
		// Nullable value-types become pointers so nil represents null;
		// reference-types (list/dict/set/class/tagged enum) are already nil-able.
		inner := *t
		inner.Nullable = false
		g := ToGo(&inner, intMode)
		g.Source = "*" + g.Source
		return g
	}
	switch t.Kind {
	case KindBool:
		return GoType{Source: "bool"}
	case KindString:
		return GoType{Source: "string"}
	case KindFloat:
		return GoType{Source: "float64"}
	case KindBytes:
		return GoType{Source: "[]byte"}
	case KindInt:
		if intMode == IntModeBigInt {
			return GoType{Source: "transpilert.Int", ImportPath: OrderedDictImport}
		}
		return GoType{Source: "int64"}
	case KindDecimal:
		return GoType{Source: "*big.Rat", ImportPath: "math/big"}
	case KindNull:
		return GoType{Source: "any"}
	case KindVoid:
		return GoType{Source: ""}
	case KindList:
		elem := ToGo(t.Elem, intMode)
		return GoType{Source: "[]" + elem.Source, ImportPath: elem.ImportPath}
	case KindDict:
		k := ToGo(t.Key, intMode)
		v := ToGo(t.Value, intMode)
		imports := []string{OrderedDictImport}
		imports = append(imports, k.AllImports()...)
		imports = append(imports, v.AllImports()...)
		return GoType{
			Source:  "*transpilert.OrderedDict[" + k.Source + ", " + v.Source + "]",
			Imports: imports,
		}
	case KindSet:
		elem := ToGo(t.Elem, intMode)
		return GoType{Source: "map[" + elem.Source + "]struct{}", ImportPath: elem.ImportPath}
	case KindClass:
		if t.Name == RePatternName {
			return GoType{Source: "*transpilert.RePattern", ImportPath: OrderedDictImport}
		}
		if t.Name == URLValueName {
			return GoType{Source: "*transpilert.URLValue", ImportPath: OrderedDictImport}
		}
		if t.Name == DateTimeInstantName {
			return GoType{Source: "transpilert.DateTimeInstant", ImportPath: OrderedDictImport}
		}
		if t.Name == DateTimeDurationName {
			return GoType{Source: "transpilert.DateTimeDuration", ImportPath: OrderedDictImport}
		}
		if t.Name == DateTimeZoneName {
			return GoType{Source: "transpilert.DateTimeZone", ImportPath: OrderedDictImport}
		}
		if t.Name == TemplateValueName {
			return GoType{Source: "transpilert.TemplateValue", ImportPath: OrderedDictImport}
		}
		if t.Name == TemplateEngineName {
			return GoType{Source: "transpilert.TemplateEngine", ImportPath: OrderedDictImport}
		}
		src := "*" + t.Name
		if len(t.Params) > 0 {
			src += renderTypeArgs(t.Params, intMode)
		}
		return GoType{Source: src}
	case KindInterface:
		src := t.Name
		if len(t.Params) > 0 {
			src += renderTypeArgs(t.Params, intMode)
		}
		return GoType{Source: src}
	case KindEnum, KindTypeParam:
		return GoType{Source: t.Name}
	case KindFunc:
		return GoType{Source: "any"}
	case KindGenerator:
		elem := ToGo(t.Elem, intMode)
		return GoType{Source: "iter.Seq[" + elem.Source + "]", ImportPath: "iter"}
	case KindTask:
		elem := ToGo(t.Elem, intMode)
		return GoType{Source: "*gbTask[" + elem.Source + "]"}
	}
	return GoType{Source: "any"}
}

// isNullableValueKind reports kinds whose Go representation is a non-nilable
// value, so nullability needs a pointer wrapper. decimal (*big.Rat), bytes
// ([]byte), and reference kinds are already nil-able and pass through.
func isNullableValueKind(k Kind) bool {
	switch k {
	case KindInt, KindFloat, KindBool, KindString:
		return true
	}
	return false
}

// nullablePtrKind reports whether a nullable t needs a Go pointer wrapper:
// the scalar value-types plus untagged (int-based) enums.
func nullablePtrKind(t *Type) bool {
	return isNullableValueKind(t.Kind) || (t.Kind == KindEnum && t.EnumScalar)
}

func renderTypeArgs(args []*Type, intMode IntMode) string {
	out := "["
	for i, a := range args {
		if i > 0 {
			out += ", "
		}
		out += ToGo(a, intMode).Source
	}
	return out + "]"
}
