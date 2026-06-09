package native

import (
	"fmt"
	nethttp "net/http"
	"strings"
	"time"
)

// Canonical method-name sets for the datetime value types. The dispatch
// switches and the LSP catalog are both guarded against these.
var (
	DateTimeInstantMethods = []string{
		"unix", "toUnix", "toUnixMillis", "toUnixNanos",
		"format", "formatRFC3339", "formatHTTP", "toUtc", "toString",
		"toLocal", "inZone", "parts",
		"add", "sub", "addSeconds", "addDays", "addMonths", "addYears",
		"diff", "isBefore", "isAfter", "equals",
		"year", "month", "day", "hour", "minute", "second",
		"weekday", "dayOfYear", "isWeekend", "copy",
	}
	DateTimeDurationMethods = []string{
		"seconds", "inSeconds", "inMillis", "inNanos",
		"abs", "negate", "add", "sub", "toDict", "toString",
	}
	DateTimeZoneMethods = []string{"name", "toString", "offset", "offsetAt"}
)

var datetimeNamedLayouts = map[string]string{
	"iso":      time.RFC3339,
	"rfc3339":  time.RFC3339,
	"date":     "2006-01-02",
	"time":     "15:04:05",
	"datetime": "2006-01-02 15:04:05",
	"http":     nethttp.TimeFormat,
}

var strftimeToGo = map[byte]string{
	'Y': "2006", 'y': "06",
	'm': "01", 'd': "02",
	'H': "15", 'I': "03",
	'M': "04", 'S': "05",
	'p': "PM", 'A': "Monday", 'a': "Mon",
	'B': "January", 'b': "Jan",
	'j': "002", 'z': "-0700", 'Z': "MST",
}

// ResolveDateLayout maps a user layout to a Go reference-time layout. A layout
// containing '%' is strftime; a recognised name is a preset; anything else is
// a raw Go layout (back-compat).
func ResolveDateLayout(layout string) (string, error) {
	if strings.Contains(layout, "%") {
		return strftimeToGoLayout(layout)
	}
	if preset, ok := datetimeNamedLayouts[strings.ToLower(layout)]; ok {
		return preset, nil
	}
	return layout, nil
}

func strftimeToGoLayout(layout string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(layout); i++ {
		if layout[i] != '%' {
			b.WriteByte(layout[i])
			continue
		}
		i++
		if i >= len(layout) {
			return "", fmt.Errorf("trailing %% in date format")
		}
		if layout[i] == '%' {
			b.WriteByte('%')
			continue
		}
		repl, ok := strftimeToGo[layout[i]]
		if !ok {
			return "", fmt.Errorf("unknown date format token %%%c", layout[i])
		}
		b.WriteString(repl)
	}
	return b.String(), nil
}

// isoWeekday returns 1 (Monday) through 7 (Sunday).
func isoWeekday(t time.Time) int {
	if wd := int(t.Weekday()); wd != 0 {
		return wd
	}
	return 7
}
