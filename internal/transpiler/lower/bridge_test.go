package lower_test

import (
	"testing"

	"geblang/internal/transpiler/lower"
)

func TestNativeBridgeRegistersDefaults(t *testing.T) {
	b := lower.NewNativeBridge()
	// io.print / io.println use a custom Emit so nullable values render the
	// Geblang way ("null") instead of Go's pointer/<nil> default.
	for _, c := range []struct{ mod, fn string }{
		{"io", "print"},
		{"io", "println"},
	} {
		got, ok := b.Lookup(c.mod, c.fn)
		if !ok {
			t.Errorf("Lookup(%s, %s): not found", c.mod, c.fn)
			continue
		}
		if got.Emit == nil {
			t.Errorf("Lookup(%s, %s): expected custom Emit closure", c.mod, c.fn)
		}
		if len(got.Imports) == 0 {
			t.Errorf("Lookup(%s, %s): expected non-empty Imports", c.mod, c.fn)
		}
	}
}

func TestNativeBridgeSysExitUsesCustomEmit(t *testing.T) {
	b := lower.NewNativeBridge()
	got, ok := b.Lookup("sys", "exit")
	if !ok {
		t.Fatalf("sys.exit not registered")
	}
	if got.Emit == nil {
		t.Errorf("sys.exit: expected custom Emit closure, got nil")
	}
	if len(got.Imports) == 0 || got.Imports[0] != "os" {
		t.Errorf("sys.exit imports: got %v, want [os]", got.Imports)
	}
}

func TestNativeBridgeLookupUnknownReturnsFalse(t *testing.T) {
	b := lower.NewNativeBridge()
	if _, ok := b.Lookup("unknown", "x"); ok {
		t.Errorf("expected miss")
	}
}

func TestNativeBridgeIsKnownModule(t *testing.T) {
	b := lower.NewNativeBridge()
	for _, m := range []string{"io", "sys"} {
		if !b.IsKnownModule(m) {
			t.Errorf("IsKnownModule(%q) = false, want true", m)
		}
	}
	if b.IsKnownModule("nope") {
		t.Errorf("IsKnownModule(nope) = true, want false")
	}
}

func TestNativeBridgeRegisterAddsCustom(t *testing.T) {
	b := lower.NewNativeBridge()
	b.Register("custom", "f", lower.BridgeEntry{GoFunc: "pkg.F", Imports: []string{"pkg"}})

	got, ok := b.Lookup("custom", "f")
	if !ok || got.GoFunc != "pkg.F" {
		t.Errorf("Lookup after Register: got %+v ok=%v, want pkg.F", got, ok)
	}
	if !b.IsKnownModule("custom") {
		t.Errorf("IsKnownModule(custom) should be true after Register")
	}
}
