package emit_test

import (
	"testing"

	"geblang/internal/transpiler/emit"
)

func TestMangleIdentPreservesSimpleNames(t *testing.T) {
	cases := map[string]string{
		"foo":     "foo",
		"Foo":     "Foo",
		"foo_bar": "foo_bar",
		"_x":      "_x",
		"x1":      "x1",
	}
	for in, want := range cases {
		if got := emit.MangleIdent(in); got != want {
			t.Errorf("MangleIdent(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestMangleIdentEscapesGoReservedWords(t *testing.T) {
	cases := map[string]string{
		"type":   "type_",
		"func":   "func_",
		"map":    "map_",
		"chan":   "chan_",
		"range":  "range_",
		"any":    "any_",
		"string": "string_",
		"int":    "int_",
		"error":  "error_",
		"new":    "new_",
		"len":    "len_",
		"panic":  "panic_",
	}
	for in, want := range cases {
		if got := emit.MangleIdent(in); got != want {
			t.Errorf("MangleIdent(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestMangleIdentHandlesLeadingDigit(t *testing.T) {
	if got, want := emit.MangleIdent("1foo"), "_1foo"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMangleIdentReplacesIllegalRunes(t *testing.T) {
	if got, want := emit.MangleIdent("a-b.c"), "a_b_c"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMangleIdentEmptyInput(t *testing.T) {
	if got, want := emit.MangleIdent(""), "_blank"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIsGoReserved(t *testing.T) {
	for _, w := range []string{"func", "type", "any", "int", "string", "error"} {
		if !emit.IsGoReserved(w) {
			t.Errorf("IsGoReserved(%q) = false, want true", w)
		}
	}
	for _, w := range []string{"foo", "Bar", "myFunc", "MyType"} {
		if emit.IsGoReserved(w) {
			t.Errorf("IsGoReserved(%q) = true, want false", w)
		}
	}
}

func TestPackagePathFromModule(t *testing.T) {
	cases := map[string]string{
		"app":             "app",
		"app.users":       "app/users",
		"app.users.auth":  "app/users/auth",
		"":                "",
		"a.b.c.d":         "a/b/c/d",
		"package.foo":     "package_/foo",
	}
	for in, want := range cases {
		if got := emit.PackagePathFromModule(in); got != want {
			t.Errorf("PackagePathFromModule(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestPackageNameFromModule(t *testing.T) {
	cases := map[string]string{
		"app":            "app",
		"app.users":      "users",
		"app.users.auth": "auth",
		"":               "main",
		"app.type":       "type_",
	}
	for in, want := range cases {
		if got := emit.PackageNameFromModule(in); got != want {
			t.Errorf("PackageNameFromModule(%q): got %q, want %q", in, got, want)
		}
	}
}
