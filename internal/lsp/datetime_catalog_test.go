package lsp

import (
	"sort"
	"testing"

	"geblang/internal/native"
)

// Guards that the datetime value-type method docs in the catalog match the
// canonical method sets the engine dispatch is also guarded against. Closes
// the gap that let the catalog advertise Instant/Duration/Zone methods the
// runtime never implemented.
func TestCatalogDateTimeMethodsMatchEngine(t *testing.T) {
	dt, ok := stdlibCatalog["datetime"]
	if !ok {
		t.Fatal("datetime module missing from catalog")
	}
	cases := map[string][]string{
		"Instant":  native.DateTimeInstantMethods,
		"Duration": native.DateTimeDurationMethods,
		"Zone":     native.DateTimeZoneMethods,
	}
	for class, want := range cases {
		got := make([]string, 0, len(dt.ClassMethods[class]))
		for name := range dt.ClassMethods[class] {
			got = append(got, name)
		}
		if !sameSet(got, want) {
			sort.Strings(got)
			sorted := append([]string(nil), want...)
			sort.Strings(sorted)
			t.Errorf("datetime.%s catalog methods %v do not match engine set %v", class, got, sorted)
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			return false
		}
	}
	return true
}
