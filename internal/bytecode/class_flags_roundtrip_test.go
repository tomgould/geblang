package bytecode

import "testing"

// TestClassFlagsRoundTrip guards that class-level immutability flags survive
// encode/decode (a cached .gbc must enforce @immutable identically to a fresh
// compile). Regression test for class.Immutable being dropped by the encoder.
func TestClassFlagsRoundTrip(t *testing.T) {
	c := Chunk{
		Compiler: "x",
		Classes: []ClassInfo{{
			Name:            "P",
			Immutable:       true,
			ImmutableFields: []string{"id", "createdAt"},
			DestructorIndex: -1,
		}},
	}
	enc, err := Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	got := dec.Classes[0]
	if !got.Immutable {
		t.Error("Immutable lost in encode/decode")
	}
	if len(got.ImmutableFields) != 2 || got.ImmutableFields[0] != "id" || got.ImmutableFields[1] != "createdAt" {
		t.Errorf("ImmutableFields lost/garbled: %v", got.ImmutableFields)
	}
}
