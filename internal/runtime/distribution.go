package runtime

import (
	"strconv"
	"strings"
)

// Distribution is a probability distribution value (Params are positional constructor args).
type Distribution struct {
	Kind   string
	Params []float64
}

func (d *Distribution) TypeName() string { return "stats.Distribution" }

func (d *Distribution) Inspect() string {
	parts := make([]string, len(d.Params))
	for i, p := range d.Params {
		parts[i] = strconv.FormatFloat(p, 'g', -1, 64)
	}
	return "stats." + d.Kind + "(" + strings.Join(parts, ", ") + ")"
}
