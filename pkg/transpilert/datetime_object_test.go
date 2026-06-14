package transpilert

import "testing"

func TestDateTimeInstantAccessors(t *testing.T) {
	a := NewDateTimeInstant(int64(1700000000))
	if a.ToString() != "2023-11-14T22:13:20Z" {
		t.Errorf("toString: %q", a.ToString())
	}
	if a.Year() != 2023 || a.Month() != 11 || a.Day() != 14 {
		t.Errorf("date: %d-%d-%d", a.Year(), a.Month(), a.Day())
	}
	if a.Hour() != 22 || a.Minute() != 13 || a.Second() != 20 {
		t.Errorf("time: %d:%d:%d", a.Hour(), a.Minute(), a.Second())
	}
	// Tuesday is ISO weekday 2.
	if a.Weekday() != 2 {
		t.Errorf("weekday: %d", a.Weekday())
	}
	if a.IsWeekend() {
		t.Error("Tuesday is not a weekend")
	}
	if got := a.Format("%Y-%m-%d %H:%M:%S"); got != "2023-11-14 22:13:20" {
		t.Errorf("format: %q", got)
	}
}

func TestDateTimeInstantChainAndDiff(t *testing.T) {
	a := NewDateTimeInstant(int64(1700000000))
	b := a.AddDays(10).AddSeconds(3600)
	if b.ToUnixMillis() != (1700000000+10*86400+3600)*1000 {
		t.Errorf("chained add: %d", b.Unix)
	}
	span := NewDateTimeInstant(int64(1700086400)).Diff(a)
	if span.Seconds != 86400 {
		t.Errorf("diff seconds: %d", span.Seconds)
	}
	if span.ToString() != "86400s" {
		t.Errorf("duration toString: %q", span.ToString())
	}
	if !a.IsBefore(NewDateTimeInstant(int64(1700086400))) || !a.Equals(a) {
		t.Error("comparisons")
	}
}

func TestDateTimeInstantPartsSortedKeys(t *testing.T) {
	a := NewDateTimeInstant(int64(1700000000))
	parts := a.Parts()
	wantKeys := []string{"day", "hour", "minute", "month", "second", "timestamp", "weekday", "year"}
	if got := parts.Keys(); !equalStrings(got, wantKeys) {
		t.Errorf("parts keys: %v", got)
	}
	// parts weekday is the raw Go weekday (Sunday=0), not ISO.
	if v, _ := parts.Get("weekday"); v.(int64) != 2 {
		t.Errorf("parts weekday: %v", v)
	}
	inZone := a.InZone("UTC")
	wantZoneKeys := []string{"day", "hour", "minute", "month", "second", "timestamp", "weekday", "year", "zone"}
	if got := inZone.Keys(); !equalStrings(got, wantZoneKeys) {
		t.Errorf("inZone keys: %v", got)
	}
	if v, _ := inZone.Get("zone"); v.(string) != "UTC" {
		t.Errorf("inZone zone: %v", v)
	}
}

func TestDateTimeDuration(t *testing.T) {
	d := NewDateTimeDuration(90061)
	dict := d.ToDict()
	for k, want := range map[string]int64{"days": 1, "hours": 1, "minutes": 1, "seconds": 1} {
		if v, _ := dict.Get(k); v != want {
			t.Errorf("toDict %s: %d", k, v)
		}
	}
	if d.InMillis() != 90061000 {
		t.Errorf("inMillis: %d", d.InMillis())
	}
	if d.Add(NewDateTimeDuration(39)).Seconds_() != 90100 {
		t.Errorf("add: %d", d.Add(NewDateTimeDuration(39)).Seconds_())
	}
	if d.Negate().ToString() != "-90061s" {
		t.Errorf("negate: %q", d.Negate().ToString())
	}
	if d.Negate().Abs().Seconds_() != 90061 {
		t.Errorf("abs: %d", d.Negate().Abs().Seconds_())
	}
}

func TestDateTimeZone(t *testing.T) {
	z := NewDateTimeZone("UTC")
	if z.Name_() != "UTC" || z.ToString() != "UTC" {
		t.Errorf("name: %q", z.Name_())
	}
	if z.Offset() != 0 {
		t.Errorf("UTC offset: %d", z.Offset())
	}
	if z.OffsetAt(NewDateTimeInstant(int64(1700000000))) != 0 {
		t.Errorf("offsetAt UTC: %d", z.OffsetAt(NewDateTimeInstant(int64(1700000000))))
	}
}

func TestDateTimeInstantConstructorForms(t *testing.T) {
	cal := NewDateTimeInstant(int64(2023), int64(11), int64(14), int64(22), int64(13), int64(20))
	if cal.Unix != 1700000000 {
		t.Errorf("calendar ctor: %d", cal.Unix)
	}
	str := NewDateTimeInstant("2023-11-14T22:13:20Z")
	if str.Unix != 1700000000 {
		t.Errorf("string ctor: %d", str.Unix)
	}
	if NewDateTimeInstant(cal).Unix != cal.Unix {
		t.Error("copy ctor")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
