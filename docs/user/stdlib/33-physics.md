# physics

Physical constants and unit conversion (1.27.0). Constants are zero-argument
functions returning a float. `convert` scales or transforms a value between
units in the same physical dimension.

```gb
import physics;

let speed = physics.c();             /* speed of light in m/s */
let km = physics.convert(100.0, "mi", "km");  /* convert 100 miles to km */
```

## Constants

| Name | Value | Units |
|------|-------|-------|
| `c()` | 299792458 | m/s (speed of light) |
| `G()` | 6.6743e-11 | m^3 kg^-1 s^-2 (gravitational constant) |
| `planck()` | 6.62607015e-34 | J*s (Planck constant) |
| `hbar()` | 1.054571817e-34 | J*s (reduced Planck constant) |
| `avogadro()` | 6.02214076e23 | mol^-1 (Avogadro constant) |
| `boltzmann()` | 1.380649e-23 | J/K (Boltzmann constant) |
| `gasConstant()` | 8.314462618 | J mol^-1 K^-1 (ideal gas constant) |
| `elementaryCharge()` | 1.602176634e-19 | C (elementary charge) |
| `electronMass()` | 9.1093837015e-31 | kg |
| `protonMass()` | 1.67262192369e-27 | kg |
| `stefanBoltzmann()` | 5.670374419e-8 | W m^-2 K^-4 (Stefan-Boltzmann constant) |
| `gravity()` | 9.80665 | m/s^2 (standard gravity) |

## convert

```gb
physics.convert(value, fromUnit, toUnit)
```

Converts `value` from `fromUnit` to `toUnit`. Both units must belong to the
same physical dimension.

### Length

Units: `"m"`, `"km"`, `"cm"`, `"mm"`, `"mi"`, `"yd"`, `"ft"`, `"in"`, `"nmi"`

```gb
physics.convert(1.0, "mi", "km");     /* 1.609344 */
physics.convert(100.0, "cm", "m");    /* 1.0 */
```

### Mass

Units: `"kg"`, `"g"`, `"mg"`, `"lb"`, `"oz"`, `"t"`

```gb
physics.convert(1.0, "lb", "kg");     /* 0.45359237 */
```

### Time

Units: `"s"`, `"ms"`, `"us"`, `"ns"`, `"min"`, `"h"`, `"day"`

```gb
physics.convert(1.0, "h", "s");       /* 3600.0 */
```

### Temperature

Units: `"C"`, `"F"`, `"K"` (affine conversion; not a simple scaling)

```gb
physics.convert(100.0, "C", "F");     /* 212.0 */
physics.convert(0.0, "C", "K");       /* 273.15 */
physics.convert(32.0, "F", "C");      /* 0.0 */
```

## Error handling

- An unknown unit string raises `RuntimeError` with the message
  `physics.convert: unknown unit "..."`.
- Mixing units from different dimensions (e.g. `"km"` and `"kg"`) raises
  `RuntimeError` with the message
  `physics.convert: dimension mismatch (km is length, kg is mass)`.
- Mixing temperature units with scale units raises `RuntimeError`.

```gb
try {
    physics.convert(1.0, "km", "kg");
} catch (RuntimeError e) {
    io.println(e.message); /* physics.convert: dimension mismatch ... */
}
```
