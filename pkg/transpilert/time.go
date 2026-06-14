package transpilert

import (
	"strconv"
	"time"
)

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// Typed adapters for the Geblang time module's monotonic-clock and wall-clock
// timing surface. Semantics match the interpreter: wall-clock helpers read
// time.Now(); monotonic helpers measure from process start so they never
// decrease. humanize renders a duration with integer math for cross-backend
// determinism.

// timeMonoStart anchors monotonic readings to process start, matching the
// interpreter's monoClockStart.
var timeMonoStart = time.Now()

func TimeNow() int64       { return time.Now().UnixMilli() }
func TimeUnix() int64      { return time.Now().Unix() }
func TimeUnixMilli() int64 { return time.Now().UnixMilli() }
func TimeUnixMicro() int64 { return time.Now().UnixMicro() }
func TimeUnixNano() int64  { return time.Now().UnixNano() }

func TimeMonotonic() int64   { return time.Since(timeMonoStart).Milliseconds() }
func TimeMonotonicNs() int64 { return time.Since(timeMonoStart).Nanoseconds() }

func TimeElapsed(start int64) int64 { return time.Now().UnixMilli() - start }

func TimeSleep(ms int64) any {
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	return nil
}

func TimeUnixFloat() float64 {
	now := time.Now()
	return float64(now.Unix()) + float64(now.Nanosecond())/1e9
}

func TimeElapsedFloat(start float64) float64 {
	now := time.Now()
	current := float64(now.Unix()) + float64(now.Nanosecond())/1e9
	return current - start
}

func TimeHumanize(ms int64) string { return humanizeMillis(ms) }

// humanizeMillis mirrors the interpreter's renderer: a compact 1-2 unit string
// using integer math so output is deterministic.
func humanizeMillis(ms int64) string {
	sign := ""
	if ms < 0 {
		sign = "-"
		ms = -ms
	}
	if ms < 1000 {
		return sign + itoa(ms) + "ms"
	}
	tenths := (ms + 50) / 100
	if tenths < 600 {
		whole, frac := tenths/10, tenths%10
		if frac == 0 {
			return sign + itoa(whole) + "s"
		}
		return sign + itoa(whole) + "." + itoa(frac) + "s"
	}
	totalSec := (ms + 500) / 1000
	units := []struct {
		v int64
		u string
	}{
		{totalSec / 86400, "d"},
		{(totalSec % 86400) / 3600, "h"},
		{(totalSec % 3600) / 60, "m"},
		{totalSec % 60, "s"},
	}
	i := 0
	for i < len(units) && units[i].v == 0 {
		i++
	}
	out := sign + itoa(units[i].v) + units[i].u
	if i+1 < len(units) && units[i+1].v > 0 {
		out += " " + itoa(units[i+1].v) + units[i+1].u
	}
	return out
}
