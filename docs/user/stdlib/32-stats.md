# stats

Probability distributions as objects (1.27.0). Each constructor returns
a `Distribution` with a uniform method set: `pdf`, `cdf`, `ppf`, `mean`,
`variance`, `std`, and `sample`.

```gb
import stats;

let d = stats.normal(0.0, 1.0);
d.pdf(0.0);               /* 0.3989... */
d.cdf(1.96);              /* 0.9750... */
d.ppf(0.975);             /* 1.9599... */
d.mean(); d.variance(); d.std();

d.sample(1000);                   /* 1-D ndarray of 1000 draws */
d.sample(1000, {"seed": 42});     /* reproducible draw */
d.sample();                       /* single scalar */
```

## Distributions

| Constructor | Parameters | Support |
|-------------|-----------|---------|
| `normal(mu, sigma)` | sigma > 0 (std dev) | all reals |
| `uniform(a, b)` | a < b | [a, b] |
| `exponential(rate)` | rate > 0 | x >= 0 |
| `gamma(shape, scale)` | shape > 0, scale > 0 | x > 0 |
| `beta(alpha, beta)` | alpha > 0, beta > 0 | [0, 1] |
| `chiSquared(df)` | df > 0 | x >= 0 |
| `studentT(df)` | df > 0 | all reals |
| `f(d1, d2)` | d1 > 0, d2 > 0 | x >= 0 |
| `lognormal(mu, sigma)` | sigma > 0 | x > 0 |
| `weibull(shape, scale)` | shape > 0, scale > 0 | x >= 0 |
| `binomial(n, p)` | n >= 0 (int), 0 <= p <= 1 | {0, ..., n} |
| `poisson(lambda)` | lambda > 0 | {0, 1, 2, ...} |

## Moments

| Distribution | mean | variance |
|--------------|------|----------|
| normal | mu | sigma^2 |
| uniform | (a+b)/2 | (b-a)^2/12 |
| exponential | 1/rate | 1/rate^2 |
| gamma | shape*scale | shape*scale^2 |
| beta | a/(a+b) | ab/((a+b)^2(a+b+1)) |
| chiSquared | df | 2*df |
| studentT | 0 (df > 1), NaN otherwise | df/(df-2) (df > 2), NaN otherwise |
| f | d2/(d2-2) (d2 > 2), NaN otherwise | standard closed form |
| lognormal | exp(mu + sigma^2/2) | standard closed form |
| weibull | scale*gamma(1+1/shape) | standard closed form |
| binomial | n*p | n*p*(1-p) |
| poisson | lambda | lambda |

`mean()` and `variance()` return NaN where the moment is undefined for
the given parameters (for example, `studentT` mean when df <= 1). This
follows the `math` module's NaN convention rather than throwing.

## Methods

- `pdf(x)` - probability density (continuous) or probability mass (discrete).
  For discrete distributions this is the mass function; `pdf` at a
  non-integer argument returns 0.0.
- `cdf(x)` - cumulative distribution function.
- `ppf(p)` - inverse CDF (quantile function); p must be in [0, 1].
- `mean()`, `variance()`, `std()` - closed-form moments. `std()` is
  `sqrt(variance())`.
- `sample(n)` - draw n variates, returned as a 1-D ndarray (`float64` for
  continuous distributions, `int64` for discrete).
- `sample(n, {"seed": k})` - reproducible draw; the same seed produces the
  same sequence on both the evaluator and the VM.
- `sample()` - single scalar draw (float for continuous, int for discrete).

## Reproducibility

The seed is local to the call: passing `{"seed": k}` does not affect the
module's shared RNG used by unseeded calls. The same seed produces
byte-identical results on both backends and across process restarts.

```gb
let a = stats.normal(0.0, 1.0).sample(5, {"seed": 1});
let b = stats.normal(0.0, 1.0).sample(5, {"seed": 1});
/* a and b are identical */
```

## Error handling

- Constructors throw `RuntimeError` if parameters are out of range (e.g.
  `sigma <= 0`, `a >= b`, `df <= 0`, `p < 0 or p > 1`, `n < 0`).
- `ppf(p)` throws `RuntimeError` if p is outside [0, 1].
- `sample(n)` throws `RuntimeError` if n < 0.

```gb
try {
    stats.normal(0.0, -1.0);
} catch (RuntimeError e) {
    io.println(e.message()); /* sigma must be > 0 */
}
```

## Example: large-sample mean convergence

```gb
import stats;

let d = stats.poisson(4.0);
let s = d.sample(10000, {"seed": 42});
io.println(s.mean()); /* close to 4.0 */
```
