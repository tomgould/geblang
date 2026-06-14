package types_test

import (
	"testing"

	"geblang/internal/ast"
	"geblang/internal/transpiler/types"
)

func TestFromASTPrimitives(t *testing.T) {
	cases := map[string]types.Kind{
		"int":     types.KindInt,
		"float":   types.KindFloat,
		"decimal": types.KindDecimal,
		"string":  types.KindString,
		"bool":    types.KindBool,
		"bytes":   types.KindBytes,
		"null":    types.KindNull,
		"any":     types.KindAny,
	}
	for name, want := range cases {
		got := types.FromAST(&ast.TypeRef{Name: name})
		if got.Kind != want {
			t.Errorf("FromAST(%q): got Kind %v, want %v", name, got.Kind, want)
		}
	}
}

func TestVoidMapsToEmptyGoType(t *testing.T) {
	got := types.FromAST(&ast.TypeRef{Name: "void"})
	if got.Kind != types.KindVoid {
		t.Fatalf("FromAST(void): got Kind %v, want KindVoid", got.Kind)
	}
	if src := types.ToGo(got, types.IntModeFast).Source; src != "" {
		t.Errorf("ToGo(void) = %q, want empty (no Go return type)", src)
	}
}

func TestFromASTUserClassFallsBackToClassKind(t *testing.T) {
	got := types.FromAST(&ast.TypeRef{Name: "MyUser"})
	if got.Kind != types.KindClass {
		t.Errorf("got Kind %v, want KindClass", got.Kind)
	}
	if got.Name != "MyUser" {
		t.Errorf("got Name %q, want %q", got.Name, "MyUser")
	}
}

func TestFromASTListWithTypeArgument(t *testing.T) {
	in := &ast.TypeRef{
		Name:      "list",
		Arguments: []*ast.TypeRef{{Name: "int"}},
	}
	got := types.FromAST(in)
	if got.Kind != types.KindList {
		t.Fatalf("got Kind %v, want KindList", got.Kind)
	}
	if got.Elem == nil || got.Elem.Kind != types.KindInt {
		t.Fatalf("got Elem %+v, want KindInt", got.Elem)
	}
}

func TestFromASTDictWithTypeArguments(t *testing.T) {
	in := &ast.TypeRef{
		Name:      "dict",
		Arguments: []*ast.TypeRef{{Name: "string"}, {Name: "int"}},
	}
	got := types.FromAST(in)
	if got.Kind != types.KindDict {
		t.Fatalf("got Kind %v, want KindDict", got.Kind)
	}
	if got.Key.Kind != types.KindString || got.Value.Kind != types.KindInt {
		t.Errorf("got Key=%v Value=%v, want KindString/KindInt", got.Key.Kind, got.Value.Kind)
	}
}

func TestFromASTNilReturnsUnknown(t *testing.T) {
	got := types.FromAST(nil)
	if got.Kind != types.KindUnknown {
		t.Errorf("got Kind %v, want KindUnknown", got.Kind)
	}
}

func TestFromASTUnionFallsBackToAny(t *testing.T) {
	in := &ast.TypeRef{
		Operator: "|",
		Left:     &ast.TypeRef{Name: "int"},
		Right:    &ast.TypeRef{Name: "string"},
	}
	got := types.FromAST(in)
	if got.Kind != types.KindAny {
		t.Errorf("got Kind %v, want KindAny", got.Kind)
	}
}

func TestToGoIntModes(t *testing.T) {
	intTy := &types.Type{Kind: types.KindInt}
	cases := map[types.IntMode]string{
		types.IntModeFast:   "int64",
		types.IntModeBigInt: "transpilert.Int",
	}
	for mode, want := range cases {
		got := types.ToGo(intTy, mode)
		if got.Source != want {
			t.Errorf("ToGo(int, %v): got %q, want %q", mode, got.Source, want)
		}
	}
}

func TestToGoIntBigIntImportsTranspilert(t *testing.T) {
	got := types.ToGo(&types.Type{Kind: types.KindInt}, types.IntModeBigInt)
	if got.ImportPath != types.OrderedDictImport {
		t.Errorf("got import %q, want %q", got.ImportPath, types.OrderedDictImport)
	}
}

func TestToGoListWrapsElem(t *testing.T) {
	listTy := &types.Type{Kind: types.KindList, Elem: &types.Type{Kind: types.KindString}}
	got := types.ToGo(listTy, types.IntModeFast)
	if got.Source != "[]string" {
		t.Errorf("got %q, want %q", got.Source, "[]string")
	}
}

func TestToGoDictWrapsKeyValue(t *testing.T) {
	dictTy := &types.Type{
		Kind:  types.KindDict,
		Key:   &types.Type{Kind: types.KindString},
		Value: &types.Type{Kind: types.KindInt},
	}
	got := types.ToGo(dictTy, types.IntModeFast)
	if got.Source != "*transpilert.OrderedDict[string, int64]" {
		t.Errorf("got %q, want %q", got.Source, "*transpilert.OrderedDict[string, int64]")
	}
	if len(got.AllImports()) == 0 || got.AllImports()[0] != types.OrderedDictImport {
		t.Errorf("dict type must import %q, got %v", types.OrderedDictImport, got.AllImports())
	}
}

func TestToGoClassUsesPointer(t *testing.T) {
	got := types.ToGo(&types.Type{Kind: types.KindClass, Name: "User"}, types.IntModeFast)
	if got.Source != "*User" {
		t.Errorf("got %q, want %q", got.Source, "*User")
	}
}

func TestToGoGeneratorUsesIterSeq(t *testing.T) {
	got := types.ToGo(&types.Type{Kind: types.KindGenerator, Elem: &types.Type{Kind: types.KindInt}}, types.IntModeFast)
	if got.Source != "iter.Seq[int64]" {
		t.Errorf("got %q, want %q", got.Source, "iter.Seq[int64]")
	}
	if got.ImportPath != "iter" {
		t.Errorf("got import %q, want %q", got.ImportPath, "iter")
	}
}

func TestToGoNilReturnsAny(t *testing.T) {
	got := types.ToGo(nil, types.IntModeFast)
	if got.Source != "any" {
		t.Errorf("got %q, want %q", got.Source, "any")
	}
}

func TestToGoTaskWrapsInlineTask(t *testing.T) {
	got := types.ToGo(&types.Type{Kind: types.KindTask, Elem: &types.Type{Kind: types.KindString}}, types.IntModeFast)
	if got.Source != "*gbTask[string]" {
		t.Errorf("got %q, want %q", got.Source, "*gbTask[string]")
	}
}
