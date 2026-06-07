package native

import (
	"testing"

	"geblang/internal/runtime"
)

func callImage(t *testing.T, r *Registry, name string, args ...runtime.Value) runtime.Value {
	t.Helper()
	v, err := r.Call("imagenative", name, args)
	if err != nil {
		t.Fatalf("image.%s: %v", name, err)
	}
	return v
}

func TestImageBlankResizeEncodeRoundTrip(t *testing.T) {
	r := NewBuiltinRegistry()

	h := callImage(t, r, "blank", runtime.SmallInt{Value: 8}, runtime.SmallInt{Value: 4})
	if w := callImage(t, r, "width", h); !valueIsInt(w, 8) {
		t.Fatalf("width = %v, want 8", w.Inspect())
	}
	if hgt := callImage(t, r, "height", h); !valueIsInt(hgt, 4) {
		t.Fatalf("height = %v, want 4", hgt.Inspect())
	}

	resized := callImage(t, r, "resize", h, runtime.SmallInt{Value: 16}, runtime.SmallInt{Value: 16})
	if w := callImage(t, r, "width", resized); !valueIsInt(w, 16) {
		t.Fatalf("resized width = %v, want 16", w.Inspect())
	}

	png := callImage(t, r, "encode", resized, runtime.String{Value: "png"})
	b, ok := png.(runtime.Bytes)
	if !ok || len(b.Value) == 0 {
		t.Fatalf("encode did not return non-empty bytes")
	}

	decoded := callImage(t, r, "decode", png)
	if w := callImage(t, r, "width", decoded); !valueIsInt(w, 16) {
		t.Fatalf("decoded width = %v, want 16", w.Inspect())
	}
}

func valueIsInt(v runtime.Value, n int64) bool {
	got, ok := AsInt64(v)
	return ok && got == n
}
