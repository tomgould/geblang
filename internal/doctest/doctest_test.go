package doctest

import (
	"os"
	"testing"
)

func TestDocExamplesParse(t *testing.T) {
	if os.Getenv("GEBLANG_DOCTEST") == "" {
		t.Skip("set GEBLANG_DOCTEST=1 (make doc-test) to parse the user-doc examples")
	}
	failures, err := CheckDocs("../../docs/user")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range failures {
		t.Errorf("doc example failed to parse: %s", f)
	}
	if n := len(failures); n > 0 {
		t.Fatalf("%d doc example(s) failed to parse", n)
	}
}
