package native

import (
	"fmt"
	"geblang/internal/runtime"
	"math/big"
	"time"
)

// monoClockStart anchors time.monotonic() to process start so it reports
// a monotonic (never-decreasing) millisecond counter via the monotonic
// reading embedded in time.Since.
var monoClockStart = time.Now()

// registerTime exposes monotonic-style timing primitives distinct from
// the calendar/zone-aware datetime module. Use time for measuring
// elapsed durations, throttling, debouncing, and blocking sleeps.
func registerTime(r *Registry) {
	r.Register("time", "now", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.now expects no arguments")
		}
		return runtime.NewInt64(time.Now().UnixMilli()), nil
	})
	r.Register("time", "elapsed", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("time.elapsed expects one argument (start time in ms)")
		}
		start, ok := AsInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("time.elapsed start must be int")
		}
		return runtime.NewInt64(time.Now().UnixMilli() - start), nil
	})
	r.Register("time", "sleep", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("time.sleep expects one argument (milliseconds)")
		}
		ms, ok := AsInt64(args[0])
		if !ok || ms < 0 {
			return nil, fmt.Errorf("time.sleep milliseconds must be a non-negative int")
		}
		if ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		return runtime.Null{}, nil
	})
	r.Register("time", "unix", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unix expects no arguments")
		}
		return runtime.NewInt64(time.Now().Unix()), nil
	})
	r.Register("time", "monotonic", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.monotonic expects no arguments")
		}
		// Monotonic milliseconds since process start. Unlike time.now /
		// time.unix (wall clock, which can jump backwards on NTP / VM
		// clock correction), this never decreases, so it is the correct
		// source for measuring durations, timeouts, and TTLs.
		return runtime.NewInt64(time.Since(monoClockStart).Milliseconds()), nil
	})
	r.Register("time", "monotonicNs", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.monotonicNs expects no arguments")
		}
		return runtime.NewInt64(time.Since(monoClockStart).Nanoseconds()), nil
	})
	r.Register("time", "unixMilli", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unixMilli expects no arguments")
		}
		return runtime.NewInt64(time.Now().UnixMilli()), nil
	})
	r.Register("time", "unixMicro", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unixMicro expects no arguments")
		}
		return runtime.NewInt64(time.Now().UnixMicro()), nil
	})
	r.Register("time", "unixNano", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unixNano expects no arguments")
		}
		return runtime.NewInt64(time.Now().UnixNano()), nil
	})
	r.Register("time", "unixFloat", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unixFloat expects no arguments")
		}
		now := time.Now()
		return runtime.Float{Value: float64(now.Unix()) + float64(now.Nanosecond())/1e9}, nil
	})
	r.Register("time", "unixDecimal", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unixDecimal expects no arguments")
		}
		now := time.Now()
		num := new(big.Int).Mul(big.NewInt(now.Unix()), big.NewInt(1_000_000_000))
		num.Add(num, big.NewInt(int64(now.Nanosecond())))
		return runtime.Decimal{Value: new(big.Rat).SetFrac(num, big.NewInt(1_000_000_000))}, nil
	})
	r.Register("time", "elapsedFloat", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("time.elapsedFloat expects one argument (start time in seconds)")
		}
		start, ok := asFloat64Strict(args[0])
		if !ok {
			return nil, fmt.Errorf("time.elapsedFloat start must be a number")
		}
		now := time.Now()
		current := float64(now.Unix()) + float64(now.Nanosecond())/1e9
		return runtime.Float{Value: current - start}, nil
	})
	r.Register("time", "humanize", func(args []runtime.Value) (runtime.Value, error) {
		ms, ok := singleInt64Arg(args, "time.humanize")
		if !ok {
			return nil, fmt.Errorf("time.humanize expects one int argument (milliseconds)")
		}
		return runtime.String{Value: humanizeMillis(ms)}, nil
	})
}

func singleInt64Arg(args []runtime.Value, label string) (int64, bool) {
	if len(args) != 1 {
		return 0, false
	}
	return AsInt64(args[0])
}

// humanizeMillis renders a millisecond duration as a compact 1-2 unit string
// (e.g. "45ms", "1.5s", "3m 4s", "2h 5m", "1d 1h"). Integer math throughout
// so output is deterministic across backends.
func humanizeMillis(ms int64) string {
	sign := ""
	if ms < 0 {
		sign = "-"
		ms = -ms
	}
	if ms < 1000 {
		return fmt.Sprintf("%s%dms", sign, ms)
	}
	tenths := (ms + 50) / 100
	if tenths < 600 {
		whole, frac := tenths/10, tenths%10
		if frac == 0 {
			return fmt.Sprintf("%s%ds", sign, whole)
		}
		return fmt.Sprintf("%s%d.%ds", sign, whole, frac)
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
	out := fmt.Sprintf("%s%d%s", sign, units[i].v, units[i].u)
	if i+1 < len(units) && units[i+1].v > 0 {
		out += fmt.Sprintf(" %d%s", units[i+1].v, units[i+1].u)
	}
	return out
}

// asFloat64Strict accepts int/float/decimal values and returns a
// float64 approximation. Returns false for non-numeric types.
func asFloat64Strict(value runtime.Value) (float64, bool) {
	switch v := value.(type) {
	case runtime.SmallInt:
		return float64(v.Value), true
	case runtime.Int:
		f, _ := new(big.Float).SetInt(v.Value).Float64()
		return f, true
	case runtime.Float:
		return v.Value, true
	case runtime.Decimal:
		f, _ := v.Value.Float64()
		return f, true
	}
	return 0, false
}
