package bytecode

import (
	"fmt"
	"testing"
)

func TestIsParityErrorTypedNotSubstring(t *testing.T) {
	// A genuine parity error is classified.
	if !IsParityError(parityErrorf("bytecode compiler does not support X yet")) {
		t.Fatal("parityErrorf should be a parity error")
	}
	// Wrapped in locatedError, still classified via errors.As.
	if !IsParityError(locatedError{line: 1, column: 1, err: parityErrorf("does not support Y")}) {
		t.Fatal("located parity error should be classified")
	}
	// A genuine static error that merely contains parity words is NOT misclassified.
	for _, msg := range []string{
		"class Foo has no exported type Bar",
		"async function cannot be used here",
		"generator value is not callable",
	} {
		if IsParityError(fmt.Errorf("%s", msg)) {
			t.Fatalf("non-parity error misclassified as parity: %q", msg)
		}
	}
	if IsParityError(nil) {
		t.Fatal("nil is not a parity error")
	}
}
