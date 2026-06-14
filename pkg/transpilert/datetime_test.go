package transpilert

import "testing"

func TestDatetimeParseFormatRoundTrip(t *testing.T) {
	sec := DatetimeParse("2024-01-15T10:30:00Z")
	if got := DatetimeFormat(sec, "%Y-%m-%d %H:%M:%S"); got != "2024-01-15 10:30:00" {
		t.Errorf("format = %q", got)
	}
	if got := DatetimeUnix(sec); got != "2024-01-15T10:30:00Z" {
		t.Errorf("unix = %q", got)
	}
	if DatetimeMake(2024, 1, 15, 10, 30, 0) != sec {
		t.Error("make should match parsed instant")
	}
}

func TestDatetimeArithmetic(t *testing.T) {
	sec := DatetimeParse("2024-01-15T10:30:00Z")
	if DatetimeFormatDate(DatetimeAddDays(sec, 5)) != "2024-01-20" {
		t.Error("addDays wrong")
	}
	if DatetimeAddSeconds(sec, 60)-sec != 60 {
		t.Error("addSeconds wrong")
	}
}

func TestDatetimeDiff(t *testing.T) {
	a := DatetimeParse("2024-01-15T10:30:00Z")
	b := DatetimeAddDays(a, 5)
	d := DatetimeDiff(b, a)
	if keys := d.Keys(); len(keys) != 4 || keys[0] != "days" || keys[3] != "seconds" {
		t.Errorf("diff keys = %v", keys)
	}
	if v, _ := d.Get("days"); v != 5 {
		t.Errorf("diff days = %d", v)
	}
}

func TestDatetimeNames(t *testing.T) {
	if DatetimeWeekdayName(1) != "Monday" {
		t.Error("weekdayName wrong")
	}
	if DatetimeMonthName(1) != "January" {
		t.Error("monthName wrong")
	}
}
