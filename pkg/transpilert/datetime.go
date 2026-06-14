package transpilert

import (
	"fmt"
	"strings"
	"time"
)

// httpTimeFormat is net/http.TimeFormat inlined so transpilert (and vendored
// binaries) avoid pulling the whole net/http tree for one constant.
const httpTimeFormat = "Mon, 02 Jan 2006 15:04:05 GMT"

// Typed adapters for the Geblang datetime module's scalar (Unix-seconds)
// function surface over Go's time package. Instants are int64 Unix seconds and
// formatting/parsing match the interpreter (strftime tokens, RFC3339 default).
// The OO Instant/Duration/Zone surface and timezone-DB functions are not
// bridged here (they need runtime class values).

var strftimeToGo = map[byte]string{
	'Y': "2006", 'y': "06",
	'm': "01", 'd': "02",
	'H': "15", 'I': "03",
	'M': "04", 'S': "05",
	'p': "PM", 'A': "Monday", 'a': "Mon",
	'B': "January", 'b': "Jan",
	'j': "002", 'z': "-0700", 'Z': "MST",
}

var datetimeNamedLayouts = map[string]string{
	"iso":      time.RFC3339,
	"rfc3339":  time.RFC3339,
	"date":     "2006-01-02",
	"time":     "15:04:05",
	"datetime": "2006-01-02 15:04:05",
	"http":     httpTimeFormat,
}

func resolveDateLayout(layout string) string {
	if strings.Contains(layout, "%") {
		var b strings.Builder
		for i := 0; i < len(layout); i++ {
			if layout[i] != '%' {
				b.WriteByte(layout[i])
				continue
			}
			i++
			if i >= len(layout) {
				panic(dtErr("trailing %% in date format"))
			}
			if layout[i] == '%' {
				b.WriteByte('%')
				continue
			}
			repl, ok := strftimeToGo[layout[i]]
			if !ok {
				panic(dtErr(fmt.Sprintf("unknown date format token %%%c", layout[i])))
			}
			b.WriteString(repl)
		}
		return b.String()
	}
	if preset, ok := datetimeNamedLayouts[strings.ToLower(layout)]; ok {
		return preset
	}
	return layout
}

func dtErr(msg string) *Error {
	return &Error{Class: "RuntimeError", Message: msg, Parents: []string{"Error"}}
}

// DatetimeNowUnix returns the current time as Unix seconds.
func DatetimeNowUnix() int64 { return time.Now().Unix() }

// DatetimeUnix renders Unix seconds as an RFC3339 UTC string.
func DatetimeUnix(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

// DatetimeParse parses RFC3339 text into Unix seconds.
func DatetimeParse(text string) int64 {
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		panic(dtErr(err.Error()))
	}
	return parsed.Unix()
}

// DatetimeParseLayout parses text with a strftime/named/Go layout into Unix seconds.
func DatetimeParseLayout(text, layout string) int64 {
	parsed, err := time.Parse(resolveDateLayout(layout), text)
	if err != nil {
		panic(dtErr(err.Error()))
	}
	return parsed.Unix()
}

// DatetimeParseRFC3339 parses an RFC3339 string into Unix seconds.
func DatetimeParseRFC3339(text string) int64 {
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		panic(dtErr("datetime.parseRFC3339: " + err.Error()))
	}
	return parsed.Unix()
}

// DatetimeFormat formats Unix seconds with a strftime/named/Go layout (UTC).
func DatetimeFormat(seconds int64, layout string) string {
	return time.Unix(seconds, 0).UTC().Format(resolveDateLayout(layout))
}

// DatetimeFormatRFC3339 / Date / Time / HTTP render the fixed presets (UTC).
func DatetimeFormatRFC3339(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}
func DatetimeFormatDate(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format("2006-01-02")
}
func DatetimeFormatTime(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format("15:04:05")
}
func DatetimeFormatHTTP(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format(httpTimeFormat)
}
func DatetimeToUtc(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

// DatetimeAddSeconds / Days / Months / Years return adjusted Unix seconds.
func DatetimeAddSeconds(seconds, delta int64) int64 { return seconds + delta }
func DatetimeAddDays(seconds, days int64) int64 {
	return time.Unix(seconds, 0).UTC().AddDate(0, 0, int(days)).Unix()
}
func DatetimeAddMonths(seconds, months int64) int64 {
	return time.Unix(seconds, 0).UTC().AddDate(0, int(months), 0).Unix()
}
func DatetimeAddYears(seconds, years int64) int64 {
	return time.Unix(seconds, 0).UTC().AddDate(int(years), 0, 0).Unix()
}

// DatetimeMake builds Unix seconds from calendar components (UTC); hour/minute/
// second default to zero.
func DatetimeMake(parts ...int64) int64 {
	if len(parts) < 3 || len(parts) > 6 {
		panic(dtErr("datetime.make expects 3 to 6 arguments (year, month, day[, hour, minute, second])"))
	}
	p := make([]int64, 6)
	copy(p, parts)
	t := time.Date(int(p[0]), time.Month(p[1]), int(p[2]), int(p[3]), int(p[4]), int(p[5]), 0, time.UTC)
	return t.Unix()
}

// DatetimeDiff returns the absolute span between two instants broken into
// days/hours/minutes/seconds, in that key order (matching the interpreter).
func DatetimeDiff(start, end int64) *OrderedDict[string, int64] {
	delta := end - start
	if delta < 0 {
		delta = -delta
	}
	d := NewOrderedDict[string, int64]()
	d.Set("days", delta/86400)
	delta %= 86400
	d.Set("hours", delta/3600)
	delta %= 3600
	d.Set("minutes", delta/60)
	d.Set("seconds", delta%60)
	return d
}

// DatetimeWeekdayName maps 0..6 (Sunday-based) to a weekday name.
func DatetimeWeekdayName(n int64) string {
	names := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	idx := int(n) % 7
	if idx < 0 {
		idx += 7
	}
	return names[idx]
}

// DatetimeMonthName maps 1..12 to a month name.
func DatetimeMonthName(n int64) string {
	if n < 1 || n > 12 {
		panic(dtErr("datetime.monthName month must be between 1 and 12"))
	}
	return time.Month(n).String()
}

// DatetimeSleep blocks for ms milliseconds.
func DatetimeSleep(ms int64) any {
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return nil
}
