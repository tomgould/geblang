package native

import (
	"reflect"
	"testing"
)

func TestParamName(t *testing.T) {
	cases := map[string]string{
		"number base":              "base",
		"int places = 0":           "places",
		"dict<string, any> opts":   "opts",
		"dict<string, any> o = {}": "o",
		"...any args":              "args",
		"string message = \"\"":    "message",
		"value":                    "value",
	}
	for in, want := range cases {
		if got := paramName(in); got != want {
			t.Errorf("paramName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNativeParamNames(t *testing.T) {
	if got, ok := NativeParamNames("math", "pow"); !ok || !reflect.DeepEqual(got, []string{"base", "exponent"}) {
		t.Errorf("math.pow -> %v ok=%v, want [base exponent]", got, ok)
	}
	if got, ok := NativeParamNames("http", "serve"); !ok || !reflect.DeepEqual(got, []string{"addr", "handler", "opts"}) {
		t.Errorf("http.serve -> %v ok=%v, want [addr handler opts]", got, ok)
	}
	if _, ok := NativeParamNames("math", "no_such_fn"); ok {
		t.Error("absent function should return ok=false")
	}
}
