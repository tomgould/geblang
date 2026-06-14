package transpilert

import (
	"fmt"
	"sort"
	"time"
	_ "time/tzdata" // bundle the IANA zone DB so named zones match the interpreter
)

// The OO datetime surface mirrors internal/native: three opaque handles over
// Go's time package. Instant stores Unix seconds, Duration stores seconds, Zone
// stores an IANA name. Methods are byte-identical to the interpreter's
// DateTimeInstantMethod / DateTimeDurationMethod / DateTimeZoneMethod (UTC base,
// strftime layouts, ISO weekday for .weekday()). Dicts insert keys sorted so the
// nil-Order interpreter dicts render identically.

// DateTimeInstant is the handle for datetime.Instant.
type DateTimeInstant struct{ Unix int64 }

// DateTimeDuration is the handle for datetime.Duration.
type DateTimeDuration struct{ Seconds int64 }

// DateTimeZone is the handle for datetime.Zone.
type DateTimeZone struct{ Name string }

// NewDateTimeInstant backs datetime.Instant(...): 0 args (now), 1 arg
// (unix int / RFC3339 string / Instant copy), or 3-6 calendar ints (UTC).
func NewDateTimeInstant(args ...any) DateTimeInstant {
	switch len(args) {
	case 0:
		return DateTimeInstant{Unix: time.Now().Unix()}
	case 1:
		switch v := args[0].(type) {
		case DateTimeInstant:
			return v
		case int64:
			return DateTimeInstant{Unix: v}
		case int:
			return DateTimeInstant{Unix: int64(v)}
		case string:
			parsed, err := time.Parse(time.RFC3339, v)
			if err != nil {
				panic(dtErr("datetime.Instant: " + err.Error()))
			}
			return DateTimeInstant{Unix: parsed.Unix()}
		default:
			panic(dtErr("datetime.Instant expects int, string, or datetime.Instant"))
		}
	case 3, 4, 5, 6:
		ints := make([]int64, 6)
		for i, a := range args {
			ints[i] = dtAsInt64(a, "datetime.Instant calendar arguments must be int")
		}
		t := time.Date(int(ints[0]), time.Month(ints[1]), int(ints[2]), int(ints[3]), int(ints[4]), int(ints[5]), 0, time.UTC)
		return DateTimeInstant{Unix: t.Unix()}
	default:
		panic(dtErr("datetime.Instant expects 0 args (now), 1 (unix/string/Instant), or 3-6 (year, month, day[, hour, minute, second])"))
	}
}

// NewDateTimeInstantNow backs datetime.nowInstant().
func NewDateTimeInstantNow() DateTimeInstant { return DateTimeInstant{Unix: time.Now().Unix()} }

// NewDateTimeDuration backs datetime.Duration(seconds).
func NewDateTimeDuration(seconds int64) DateTimeDuration { return DateTimeDuration{Seconds: seconds} }

// NewDateTimeZone backs datetime.Zone(name); an unknown zone panics.
func NewDateTimeZone(name string) DateTimeZone {
	if _, err := time.LoadLocation(name); err != nil {
		panic(dtErr("datetime.Zone: " + err.Error()))
	}
	return DateTimeZone{Name: name}
}

func dtAsInt64(v any, msg string) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		panic(dtErr(msg))
	}
}

// --- Instant methods ---

func (i DateTimeInstant) Copy() DateTimeInstant { return i }

func (i DateTimeInstant) Unix_() int64 { return i.Unix }

func (i DateTimeInstant) ToUnixMillis() int64 { return i.Unix * 1000 }

func (i DateTimeInstant) ToUnixNanos() int64 { return i.Unix * 1_000_000_000 }

func (i DateTimeInstant) ToString() string {
	return time.Unix(i.Unix, 0).UTC().Format(time.RFC3339)
}

func (i DateTimeInstant) FormatHTTP() string {
	return time.Unix(i.Unix, 0).UTC().Format(httpTimeFormat)
}

func (i DateTimeInstant) Format(layout string) string {
	return time.Unix(i.Unix, 0).UTC().Format(resolveDateLayout(layout))
}

func (i DateTimeInstant) ToLocal(zone any) string {
	loc := dtLocation(zone, "datetime.Instant.toLocal")
	return time.Unix(i.Unix, 0).In(loc).Format(time.RFC3339)
}

func (i DateTimeInstant) Add(d DateTimeDuration) DateTimeInstant {
	return DateTimeInstant{Unix: i.Unix + d.Seconds}
}

func (i DateTimeInstant) AddSeconds(seconds int64) DateTimeInstant {
	return DateTimeInstant{Unix: i.Unix + seconds}
}

func (i DateTimeInstant) AddDays(days int64) DateTimeInstant {
	return DateTimeInstant{Unix: time.Unix(i.Unix, 0).UTC().AddDate(0, 0, int(days)).Unix()}
}

func (i DateTimeInstant) AddMonths(months int64) DateTimeInstant {
	return DateTimeInstant{Unix: time.Unix(i.Unix, 0).UTC().AddDate(0, int(months), 0).Unix()}
}

func (i DateTimeInstant) AddYears(years int64) DateTimeInstant {
	return DateTimeInstant{Unix: time.Unix(i.Unix, 0).UTC().AddDate(int(years), 0, 0).Unix()}
}

// Diff returns the absolute span between two instants as a Duration.
func (i DateTimeInstant) Diff(other DateTimeInstant) DateTimeDuration {
	delta := other.Unix - i.Unix
	if delta < 0 {
		delta = -delta
	}
	return DateTimeDuration{Seconds: delta}
}

func (i DateTimeInstant) Sub(d DateTimeDuration) DateTimeInstant {
	return DateTimeInstant{Unix: i.Unix - d.Seconds}
}

func (i DateTimeInstant) IsBefore(other DateTimeInstant) bool { return i.Unix < other.Unix }

func (i DateTimeInstant) IsAfter(other DateTimeInstant) bool { return i.Unix > other.Unix }

func (i DateTimeInstant) Equals(other DateTimeInstant) bool { return i.Unix == other.Unix }

func (i DateTimeInstant) InZone(zone any) *OrderedDict[string, any] {
	loc := dtLocation(zone, "datetime.Instant.inZone")
	return timePartsDict(time.Unix(i.Unix, 0).In(loc), loc.String())
}

func (i DateTimeInstant) Parts() *OrderedDict[string, any] {
	return timePartsDict(time.Unix(i.Unix, 0).UTC(), "")
}

func (i DateTimeInstant) Year() int64   { return int64(time.Unix(i.Unix, 0).UTC().Year()) }
func (i DateTimeInstant) Month() int64  { return int64(time.Unix(i.Unix, 0).UTC().Month()) }
func (i DateTimeInstant) Day() int64    { return int64(time.Unix(i.Unix, 0).UTC().Day()) }
func (i DateTimeInstant) Hour() int64   { return int64(time.Unix(i.Unix, 0).UTC().Hour()) }
func (i DateTimeInstant) Minute() int64 { return int64(time.Unix(i.Unix, 0).UTC().Minute()) }
func (i DateTimeInstant) Second() int64 { return int64(time.Unix(i.Unix, 0).UTC().Second()) }

// Weekday returns ISO weekday: 1 (Monday) through 7 (Sunday).
func (i DateTimeInstant) Weekday() int64 {
	wd := int(time.Unix(i.Unix, 0).UTC().Weekday())
	if wd == 0 {
		return 7
	}
	return int64(wd)
}

func (i DateTimeInstant) DayOfYear() int64 { return int64(time.Unix(i.Unix, 0).UTC().YearDay()) }

func (i DateTimeInstant) IsWeekend() bool {
	wd := time.Unix(i.Unix, 0).UTC().Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// --- Duration methods ---

func (d DateTimeDuration) Seconds_() int64 { return d.Seconds }

func (d DateTimeDuration) InMillis() int64 { return d.Seconds * 1000 }

func (d DateTimeDuration) InNanos() int64 { return d.Seconds * 1_000_000_000 }

func (d DateTimeDuration) Abs() DateTimeDuration {
	s := d.Seconds
	if s < 0 {
		s = -s
	}
	return DateTimeDuration{Seconds: s}
}

func (d DateTimeDuration) Negate() DateTimeDuration { return DateTimeDuration{Seconds: -d.Seconds} }

func (d DateTimeDuration) Add(other DateTimeDuration) DateTimeDuration {
	return DateTimeDuration{Seconds: d.Seconds + other.Seconds}
}

func (d DateTimeDuration) Sub(other DateTimeDuration) DateTimeDuration {
	return DateTimeDuration{Seconds: d.Seconds - other.Seconds}
}

func (d DateTimeDuration) ToDict() *OrderedDict[string, int64] {
	seconds := d.Seconds
	if seconds < 0 {
		seconds = -seconds
	}
	out := NewOrderedDict[string, int64]()
	out.Set("days", seconds/86400)
	seconds %= 86400
	out.Set("hours", seconds/3600)
	seconds %= 3600
	out.Set("minutes", seconds/60)
	out.Set("seconds", seconds%60)
	return out
}

func (d DateTimeDuration) ToString() string { return fmt.Sprintf("%ds", d.Seconds) }

// --- Zone methods ---

func (z DateTimeZone) Name_() string { return z.Name }

func (z DateTimeZone) ToString() string { return z.Name }

func (z DateTimeZone) Offset() int64 {
	loc, err := time.LoadLocation(z.Name)
	if err != nil {
		panic(dtErr("datetime.Zone.offset: " + err.Error()))
	}
	_, offset := time.Now().In(loc).Zone()
	return int64(offset)
}

func (z DateTimeZone) OffsetAt(i DateTimeInstant) int64 {
	loc, err := time.LoadLocation(z.Name)
	if err != nil {
		panic(dtErr("datetime.Zone.offsetAt: " + err.Error()))
	}
	_, offset := time.Unix(i.Unix, 0).In(loc).Zone()
	return int64(offset)
}

// dtLocation resolves a zone argument (string or DateTimeZone) to a *Location.
func dtLocation(zone any, label string) *time.Location {
	var name string
	switch z := zone.(type) {
	case string:
		name = z
	case DateTimeZone:
		name = z.Name
	default:
		panic(dtErr(label + ": timezone must be string or datetime.Zone"))
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(dtErr(label + ": " + err.Error()))
	}
	return loc
}

// timePartsDict mirrors the interpreter's timePartsDictWithZone: keys inserted
// sorted (the interpreter's nil-Order dict renders alphabetically); a non-empty
// zone adds the "zone" key (inZone), parts omits it.
func timePartsDict(t time.Time, zone string) *OrderedDict[string, any] {
	parts := map[string]any{
		"timestamp": int64(t.Unix()),
		"year":      int64(t.Year()),
		"month":     int64(t.Month()),
		"day":       int64(t.Day()),
		"hour":      int64(t.Hour()),
		"minute":    int64(t.Minute()),
		"second":    int64(t.Second()),
		"weekday":   int64(t.Weekday()),
	}
	if zone != "" {
		parts["zone"] = zone
	}
	keys := make([]string, 0, len(parts))
	for k := range parts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	d := NewOrderedDict[string, any]()
	for _, k := range keys {
		d.Set(k, parts[k])
	}
	return d
}
