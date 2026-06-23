package native

import (
	"fmt"

	"geblang/internal/runtime"
)

type physUnit struct {
	dim    string
	factor float64
}

// physUnits maps scale units to (dimension, factor-to-base). Temperature is affine, handled separately.
var physUnits = map[string]physUnit{
	"m": {"length", 1}, "km": {"length", 1000}, "cm": {"length", 0.01}, "mm": {"length", 0.001},
	"mi": {"length", 1609.344}, "yd": {"length", 0.9144}, "ft": {"length", 0.3048}, "in": {"length", 0.0254}, "nmi": {"length", 1852},
	"kg": {"mass", 1}, "g": {"mass", 0.001}, "mg": {"mass", 1e-6}, "lb": {"mass", 0.45359237}, "oz": {"mass", 0.028349523125}, "t": {"mass", 1000},
	"s": {"time", 1}, "ms": {"time", 1e-3}, "us": {"time", 1e-6}, "ns": {"time", 1e-9}, "min": {"time", 60}, "h": {"time", 3600}, "day": {"time", 86400},
}

func physConst(r *Registry, name string, value float64) {
	r.Register("physics", name, func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("physics.%s takes no arguments", name)
		}
		return runtime.Float{Value: value}, nil
	})
}

func physTempToK(v float64, unit string) (float64, bool) {
	switch unit {
	case "K":
		return v, true
	case "C":
		return v + 273.15, true
	case "F":
		return (v-32)*5/9 + 273.15, true
	}
	return 0, false
}

func physKToTemp(k float64, unit string) float64 {
	switch unit {
	case "C":
		return k - 273.15
	case "F":
		return (k-273.15)*9/5 + 32
	}
	return k
}

func registerPhysics(r *Registry) {
	physConst(r, "c", 299792458)
	physConst(r, "G", 6.67430e-11)
	physConst(r, "planck", 6.62607015e-34)
	physConst(r, "hbar", 1.054571817e-34)
	physConst(r, "avogadro", 6.02214076e23)
	physConst(r, "boltzmann", 1.380649e-23)
	physConst(r, "gasConstant", 8.314462618)
	physConst(r, "elementaryCharge", 1.602176634e-19)
	physConst(r, "electronMass", 9.1093837015e-31)
	physConst(r, "protonMass", 1.67262192369e-27)
	physConst(r, "stefanBoltzmann", 5.670374419e-8)
	physConst(r, "gravity", 9.80665)
	r.Register("physics", "convert", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("physics.convert expects (value, fromUnit, toUnit)")
		}
		value, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		from, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("physics.convert: fromUnit must be a string")
		}
		to, ok := args[2].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("physics.convert: toUnit must be a string")
		}
		if k, isTemp := physTempToK(value, from.Value); isTemp {
			if _, toTemp := physTempToK(0, to.Value); !toTemp {
				return nil, fmt.Errorf("physics.convert: cannot convert temperature %q to %q", from.Value, to.Value)
			}
			return runtime.Float{Value: physKToTemp(k, to.Value)}, nil
		}
		if _, toTemp := physTempToK(0, to.Value); toTemp {
			return nil, fmt.Errorf("physics.convert: cannot convert %q to temperature %q", from.Value, to.Value)
		}
		fd, ok := physUnits[from.Value]
		if !ok {
			return nil, fmt.Errorf("physics.convert: unknown unit %q", from.Value)
		}
		td, ok := physUnits[to.Value]
		if !ok {
			return nil, fmt.Errorf("physics.convert: unknown unit %q", to.Value)
		}
		if fd.dim != td.dim {
			return nil, fmt.Errorf("physics.convert: dimension mismatch (%s is %s, %s is %s)", from.Value, fd.dim, to.Value, td.dim)
		}
		return runtime.Float{Value: value * fd.factor / td.factor}, nil
	})
}
