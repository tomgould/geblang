package native

import (
	"strings"
	"testing"

	"geblang/internal/runtime"
)

// Guards that every canonically-listed datetime value method is handled by
// its dispatch switch. A name in the set that no case handles returns
// "has no method", failing here before it can ship as a catalog phantom.
func TestDateTimeMethodsRecognized(t *testing.T) {
	check := func(kind string, names []string, call func(string) error) {
		for _, name := range names {
			if err := call(name); err != nil && strings.Contains(err.Error(), "has no method") {
				t.Errorf("datetime.%s dispatch does not handle %q", kind, name)
			}
		}
	}
	check("Instant", DateTimeInstantMethods, func(n string) error {
		_, err := DateTimeInstantMethod(runtime.DateTimeInstant{Unix: 1}, n, nil)
		return err
	})
	check("Duration", DateTimeDurationMethods, func(n string) error {
		_, err := DateTimeDurationMethod(runtime.DateTimeDuration{Seconds: 1}, n, nil)
		return err
	})
	check("Zone", DateTimeZoneMethods, func(n string) error {
		_, err := DateTimeZoneMethod(runtime.DateTimeZone{Name: "UTC"}, n, nil)
		return err
	})
}
