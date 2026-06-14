package transpilert

import "testing"

// TestHumanizeMillis pins the renderer to the interpreter's documented outputs;
// drift here is a cross-backend divergence.
func TestHumanizeMillis(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{45, "45ms"},
		{999, "999ms"},
		{1500, "1.5s"},
		{2000, "2s"},
		{59000, "59s"},
		{184000, "3m 4s"},
		{7500000, "2h 5m"},
		{90000000, "1d 1h"},
		{-45, "-45ms"},
		{-1500, "-1.5s"},
	}
	for _, c := range cases {
		if got := TimeHumanize(c.ms); got != c.want {
			t.Errorf("TimeHumanize(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestMonotonicNonDecreasing(t *testing.T) {
	a := TimeMonotonicNs()
	b := TimeMonotonicNs()
	if b < a {
		t.Errorf("monotonic decreased: %d then %d", a, b)
	}
}
