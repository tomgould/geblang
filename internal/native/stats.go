package native

import (
	"fmt"
	"math"
	mrand "math/rand"
	"time"

	"geblang/internal/runtime"
)

// DistributionMethods is the canonical method list for dir/catalog guards.
var DistributionMethods = []string{"pdf", "cdf", "ppf", "mean", "variance", "std", "sample"}

func statsIsDiscrete(kind string) bool {
	return kind == "binomial" || kind == "poisson"
}

func statsFloatArg(args []runtime.Value, label string) (float64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s expects one numeric argument", label)
	}
	return FloatLike(args[0])
}

func statsTwoFloats(args []runtime.Value, label, shape string) (float64, float64, error) {
	if len(args) != 2 {
		return 0, 0, fmt.Errorf("%s expects %s", label, shape)
	}
	a, err := FloatLike(args[0])
	if err != nil {
		return 0, 0, err
	}
	b, err := FloatLike(args[1])
	if err != nil {
		return 0, 0, err
	}
	return a, b, nil
}

// Standard series + Lentz continued-fraction algorithm for the regularized incomplete gamma.
func statsRegGammaP(a, x float64) float64 {
	if x <= 0 || a <= 0 {
		if x == 0 {
			return 0
		}
		return math.NaN()
	}
	lg, _ := math.Lgamma(a)
	if x < a+1 {
		sum := 1.0 / a
		term := sum
		for n := 1; n < 2000; n++ {
			term *= x / (a + float64(n))
			sum += term
			if math.Abs(term) < math.Abs(sum)*1e-16 {
				break
			}
		}
		return sum * math.Exp(-x+a*math.Log(x)-lg)
	}
	const tiny = 1e-300
	b := x + 1 - a
	c := 1.0 / tiny
	dd := 1.0 / b
	h := dd
	for i := 1; i < 2000; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		dd = an*dd + b
		if math.Abs(dd) < tiny {
			dd = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		dd = 1 / dd
		del := dd * c
		h *= del
		if math.Abs(del-1) < 1e-16 {
			break
		}
	}
	q := math.Exp(-x+a*math.Log(x)-lg) * h
	return 1 - q
}

// statsRegBetaI returns the regularized incomplete beta I_x(a, b).
func statsRegBetaI(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	front := math.Exp(a*math.Log(x) + b*math.Log(1-x) + lab - la - lb)
	if x < (a+1)/(a+b+2) {
		return front * statsBetaCF(x, a, b) / a
	}
	return 1 - front*statsBetaCF(1-x, b, a)/b
}

// Standard Lentz continued-fraction algorithm for the regularized incomplete beta.
func statsBetaCF(x, a, b float64) float64 {
	const tiny = 1e-300
	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	dd := 1 - qab*x/qap
	if math.Abs(dd) < tiny {
		dd = tiny
	}
	dd = 1 / dd
	h := dd
	for m := 1; m < 2000; m++ {
		mf := float64(m)
		m2 := 2 * mf
		aa := mf * (b - mf) * x / ((qam + m2) * (a + m2))
		dd = 1 + aa*dd
		if math.Abs(dd) < tiny {
			dd = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		dd = 1 / dd
		h *= dd * c
		aa = -(a + mf) * (qab + mf) * x / ((a + m2) * (qap + m2))
		dd = 1 + aa*dd
		if math.Abs(dd) < tiny {
			dd = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		dd = 1 / dd
		del := dd * c
		h *= del
		if math.Abs(del-1) < 1e-16 {
			break
		}
	}
	return h
}

// statsInvertCDF brackets and bisects cdf(x)=p for x in [lo, +inf).
func statsInvertCDF(cdf func(float64) float64, p, lo float64) float64 {
	hi := lo + 1
	for cdf(hi) < p && hi < 1e15 {
		hi = lo + (hi-lo)*2
	}
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		if cdf(mid) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// statsSampleGamma draws a Gamma(shape, scale) variate (Marsaglia-Tsang).
func statsSampleGamma(shape, scale float64, rng *mrand.Rand) float64 {
	if shape < 1 {
		u := rng.Float64()
		return statsSampleGamma(shape+1, scale, rng) * math.Pow(u, 1/shape)
	}
	dd := shape - 1.0/3.0
	cc := 1 / math.Sqrt(9*dd)
	for {
		x := rng.NormFloat64()
		v := 1 + cc*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := rng.Float64()
		if u < 1-0.0331*x*x*x*x {
			return dd * v * scale
		}
		if math.Log(u) < 0.5*x*x+dd*(1-v+math.Log(v)) {
			return dd * v * scale
		}
	}
}

func registerStats(r *Registry) {
	r.Register("stats", "binomial", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("stats.binomial expects (n, p)")
		}
		nv, ok := AsInt64(args[0])
		if !ok || nv < 0 {
			return nil, fmt.Errorf("stats.binomial: n must be a non-negative int")
		}
		p, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		if p < 0 || p > 1 {
			return nil, fmt.Errorf("stats.binomial: p must be in [0, 1]")
		}
		return &runtime.Distribution{Kind: "binomial", Params: []float64{float64(nv), p}}, nil
	})
	r.Register("stats", "poisson", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("stats.poisson expects (lambda)")
		}
		lambda, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		if lambda <= 0 {
			return nil, fmt.Errorf("stats.poisson: lambda must be positive")
		}
		return &runtime.Distribution{Kind: "poisson", Params: []float64{lambda}}, nil
	})
	r.Register("stats", "normal", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("stats.normal expects (mu, sigma)")
		}
		mu, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		sigma, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		if sigma <= 0 {
			return nil, fmt.Errorf("stats.normal: sigma must be positive")
		}
		return &runtime.Distribution{Kind: "normal", Params: []float64{mu, sigma}}, nil
	})
	r.Register("stats", "uniform", func(args []runtime.Value) (runtime.Value, error) {
		a, b, err := statsTwoFloats(args, "stats.uniform", "(a, b)")
		if err != nil {
			return nil, err
		}
		if a >= b {
			return nil, fmt.Errorf("stats.uniform: require a < b")
		}
		return &runtime.Distribution{Kind: "uniform", Params: []float64{a, b}}, nil
	})
	r.Register("stats", "exponential", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("stats.exponential expects (rate)")
		}
		rate, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		if rate <= 0 {
			return nil, fmt.Errorf("stats.exponential: rate must be positive")
		}
		return &runtime.Distribution{Kind: "exponential", Params: []float64{rate}}, nil
	})
	r.Register("stats", "lognormal", func(args []runtime.Value) (runtime.Value, error) {
		mu, sigma, err := statsTwoFloats(args, "stats.lognormal", "(mu, sigma)")
		if err != nil {
			return nil, err
		}
		if sigma <= 0 {
			return nil, fmt.Errorf("stats.lognormal: sigma must be positive")
		}
		return &runtime.Distribution{Kind: "lognormal", Params: []float64{mu, sigma}}, nil
	})
	r.Register("stats", "weibull", func(args []runtime.Value) (runtime.Value, error) {
		shape, scale, err := statsTwoFloats(args, "stats.weibull", "(shape, scale)")
		if err != nil {
			return nil, err
		}
		if shape <= 0 || scale <= 0 {
			return nil, fmt.Errorf("stats.weibull: shape and scale must be positive")
		}
		return &runtime.Distribution{Kind: "weibull", Params: []float64{shape, scale}}, nil
	})
	r.Register("stats", "gamma", func(args []runtime.Value) (runtime.Value, error) {
		shape, scale, err := statsTwoFloats(args, "stats.gamma", "(shape, scale)")
		if err != nil {
			return nil, err
		}
		if shape <= 0 || scale <= 0 {
			return nil, fmt.Errorf("stats.gamma: shape and scale must be positive")
		}
		return &runtime.Distribution{Kind: "gamma", Params: []float64{shape, scale}}, nil
	})
	r.Register("stats", "chiSquared", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("stats.chiSquared expects (df)")
		}
		df, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		if df <= 0 {
			return nil, fmt.Errorf("stats.chiSquared: df must be positive")
		}
		return &runtime.Distribution{Kind: "chiSquared", Params: []float64{df}}, nil
	})
	r.Register("stats", "beta", func(args []runtime.Value) (runtime.Value, error) {
		a, b, err := statsTwoFloats(args, "stats.beta", "(alpha, beta)")
		if err != nil {
			return nil, err
		}
		if a <= 0 || b <= 0 {
			return nil, fmt.Errorf("stats.beta: alpha and beta must be positive")
		}
		return &runtime.Distribution{Kind: "beta", Params: []float64{a, b}}, nil
	})
	r.Register("stats", "studentT", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("stats.studentT expects (df)")
		}
		df, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		if df <= 0 {
			return nil, fmt.Errorf("stats.studentT: df must be positive")
		}
		return &runtime.Distribution{Kind: "studentT", Params: []float64{df}}, nil
	})
	r.Register("stats", "f", func(args []runtime.Value) (runtime.Value, error) {
		d1, d2, err := statsTwoFloats(args, "stats.f", "(d1, d2)")
		if err != nil {
			return nil, err
		}
		if d1 <= 0 || d2 <= 0 {
			return nil, fmt.Errorf("stats.f: d1 and d2 must be positive")
		}
		return &runtime.Distribution{Kind: "f", Params: []float64{d1, d2}}, nil
	})
}

// DistributionMethod dispatches a method call on a Distribution; shared by both backends.
func DistributionMethod(d *runtime.Distribution, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "pdf":
		x, err := statsFloatArg(args, "pdf")
		if err != nil {
			return nil, err
		}
		return runtime.Float{Value: statsPdf(d, x)}, nil
	case "cdf":
		x, err := statsFloatArg(args, "cdf")
		if err != nil {
			return nil, err
		}
		return runtime.Float{Value: statsCdf(d, x)}, nil
	case "ppf":
		p, err := statsFloatArg(args, "ppf")
		if err != nil {
			return nil, err
		}
		if p < 0 || p > 1 {
			return nil, fmt.Errorf("ppf: p must be in [0, 1]")
		}
		return runtime.Float{Value: statsPpf(d, p)}, nil
	case "mean":
		if len(args) != 0 {
			return nil, fmt.Errorf("mean takes no arguments")
		}
		return runtime.Float{Value: statsMean(d)}, nil
	case "variance":
		if len(args) != 0 {
			return nil, fmt.Errorf("variance takes no arguments")
		}
		return runtime.Float{Value: statsVariance(d)}, nil
	case "std":
		if len(args) != 0 {
			return nil, fmt.Errorf("std takes no arguments")
		}
		return runtime.Float{Value: math.Sqrt(statsVariance(d))}, nil
	case "sample":
		return statsSample(d, args)
	default:
		return nil, fmt.Errorf("stats.Distribution has no method %s", name)
	}
}

func statsPdf(d *runtime.Distribution, x float64) float64 {
	switch d.Kind {
	case "binomial":
		n, p := d.Params[0], d.Params[1]
		if x < 0 || x > n || x != math.Trunc(x) {
			return 0
		}
		k := x
		if p == 0 {
			if k == 0 {
				return 1
			}
			return 0
		}
		if p == 1 {
			if k == n {
				return 1
			}
			return 0
		}
		lc1, _ := math.Lgamma(n + 1)
		lc2, _ := math.Lgamma(k + 1)
		lc3, _ := math.Lgamma(n - k + 1)
		return math.Exp(lc1 - lc2 - lc3 + k*math.Log(p) + (n-k)*math.Log(1-p))
	case "poisson":
		lambda := d.Params[0]
		if x < 0 || x != math.Trunc(x) {
			return 0
		}
		lg, _ := math.Lgamma(x + 1)
		return math.Exp(x*math.Log(lambda) - lambda - lg)
	case "normal":
		mu, sigma := d.Params[0], d.Params[1]
		z := (x - mu) / sigma
		return math.Exp(-0.5*z*z) / (sigma * math.Sqrt(2*math.Pi))
	case "uniform":
		a, b := d.Params[0], d.Params[1]
		if x < a || x > b {
			return 0
		}
		return 1 / (b - a)
	case "exponential":
		rate := d.Params[0]
		if x < 0 {
			return 0
		}
		return rate * math.Exp(-rate*x)
	case "lognormal":
		mu, sigma := d.Params[0], d.Params[1]
		if x <= 0 {
			return 0
		}
		z := (math.Log(x) - mu) / sigma
		return math.Exp(-0.5*z*z) / (x * sigma * math.Sqrt(2*math.Pi))
	case "weibull":
		k, lambda := d.Params[0], d.Params[1]
		if x < 0 {
			return 0
		}
		return (k / lambda) * math.Pow(x/lambda, k-1) * math.Exp(-math.Pow(x/lambda, k))
	case "gamma":
		k, theta := d.Params[0], d.Params[1]
		if x <= 0 {
			return 0
		}
		lg, _ := math.Lgamma(k)
		return math.Exp((k-1)*math.Log(x) - x/theta - lg - k*math.Log(theta))
	case "chiSquared":
		df := d.Params[0]
		if x <= 0 {
			return 0
		}
		k, theta := df/2, 2.0
		lg, _ := math.Lgamma(k)
		return math.Exp((k-1)*math.Log(x) - x/theta - lg - k*math.Log(theta))
	case "beta":
		a, b := d.Params[0], d.Params[1]
		if x <= 0 || x >= 1 {
			return 0
		}
		la, _ := math.Lgamma(a)
		lb, _ := math.Lgamma(b)
		lab, _ := math.Lgamma(a + b)
		return math.Exp((a-1)*math.Log(x) + (b-1)*math.Log(1-x) + lab - la - lb)
	case "studentT":
		nu := d.Params[0]
		lg1, _ := math.Lgamma((nu + 1) / 2)
		lg2, _ := math.Lgamma(nu / 2)
		return math.Exp(lg1-lg2) / math.Sqrt(nu*math.Pi) * math.Pow(1+x*x/nu, -(nu+1)/2)
	case "f":
		d1, d2 := d.Params[0], d.Params[1]
		if x <= 0 {
			return 0
		}
		la, _ := math.Lgamma(d1 / 2)
		lb, _ := math.Lgamma(d2 / 2)
		lab, _ := math.Lgamma((d1 + d2) / 2)
		logp := lab - la - lb + (d1/2)*math.Log(d1/d2) + (d1/2-1)*math.Log(x) - ((d1+d2)/2)*math.Log(1+d1*x/d2)
		return math.Exp(logp)
	}
	return math.NaN()
}

func statsCdf(d *runtime.Distribution, x float64) float64 {
	switch d.Kind {
	case "binomial":
		n, p := d.Params[0], d.Params[1]
		if x < 0 {
			return 0
		}
		k := math.Floor(x)
		if k >= n {
			return 1
		}
		// P(X <= k) = I_{1-p}(n-k, k+1)
		return statsRegBetaI(1-p, n-k, k+1)
	case "poisson":
		lambda := d.Params[0]
		if x < 0 {
			return 0
		}
		k := math.Floor(x)
		// P(X <= k) = 1 - regularized gamma P(k+1, lambda)
		return 1 - statsRegGammaP(k+1, lambda)
	case "normal":
		mu, sigma := d.Params[0], d.Params[1]
		return 0.5 * math.Erfc(-(x-mu)/(sigma*math.Sqrt2))
	case "uniform":
		a, b := d.Params[0], d.Params[1]
		if x < a {
			return 0
		}
		if x > b {
			return 1
		}
		return (x - a) / (b - a)
	case "exponential":
		if x < 0 {
			return 0
		}
		return 1 - math.Exp(-d.Params[0]*x)
	case "lognormal":
		mu, sigma := d.Params[0], d.Params[1]
		if x <= 0 {
			return 0
		}
		return 0.5 * math.Erfc(-(math.Log(x)-mu)/(sigma*math.Sqrt2))
	case "weibull":
		k, lambda := d.Params[0], d.Params[1]
		if x < 0 {
			return 0
		}
		return 1 - math.Exp(-math.Pow(x/lambda, k))
	case "gamma":
		k, theta := d.Params[0], d.Params[1]
		if x <= 0 {
			return 0
		}
		return statsRegGammaP(k, x/theta)
	case "chiSquared":
		df := d.Params[0]
		if x <= 0 {
			return 0
		}
		return statsRegGammaP(df/2, x/2)
	case "beta":
		return statsRegBetaI(x, d.Params[0], d.Params[1])
	case "studentT":
		nu := d.Params[0]
		ib := 0.5 * statsRegBetaI(nu/(nu+x*x), nu/2, 0.5)
		if x > 0 {
			return 1 - ib
		}
		return ib
	case "f":
		d1, d2 := d.Params[0], d.Params[1]
		if x <= 0 {
			return 0
		}
		return statsRegBetaI(d1*x/(d1*x+d2), d1/2, d2/2)
	}
	return math.NaN()
}

func statsPpf(d *runtime.Distribution, p float64) float64 {
	switch d.Kind {
	case "binomial", "poisson":
		if p == 0 {
			return 0
		}
		if p == 1 {
			if d.Kind == "binomial" {
				return d.Params[0]
			}
			return math.Inf(1)
		}
		k := 0.0
		for statsCdf(d, k) < p {
			k++
			if k > 1e9 {
				break
			}
		}
		return k
	case "normal":
		mu, sigma := d.Params[0], d.Params[1]
		if p == 0 {
			return math.Inf(-1)
		}
		if p == 1 {
			return math.Inf(1)
		}
		return mu + sigma*math.Sqrt2*math.Erfinv(2*p-1)
	case "uniform":
		a, b := d.Params[0], d.Params[1]
		return a + p*(b-a)
	case "exponential":
		if p == 1 {
			return math.Inf(1)
		}
		return -math.Log(1-p) / d.Params[0]
	case "lognormal":
		mu, sigma := d.Params[0], d.Params[1]
		if p == 0 {
			return 0
		}
		if p == 1 {
			return math.Inf(1)
		}
		return math.Exp(mu + sigma*math.Sqrt2*math.Erfinv(2*p-1))
	case "weibull":
		k, lambda := d.Params[0], d.Params[1]
		if p == 1 {
			return math.Inf(1)
		}
		return lambda * math.Pow(-math.Log(1-p), 1/k)
	case "gamma", "chiSquared":
		if p == 0 {
			return 0
		}
		if p == 1 {
			return math.Inf(1)
		}
		return statsInvertCDF(func(x float64) float64 { return statsCdf(d, x) }, p, 0)
	case "beta":
		if p == 0 {
			return 0
		}
		if p == 1 {
			return 1
		}
		lo, hi := 0.0, 1.0
		for i := 0; i < 200; i++ {
			mid := (lo + hi) / 2
			if statsCdf(d, mid) < p {
				lo = mid
			} else {
				hi = mid
			}
		}
		return (lo + hi) / 2
	case "studentT":
		if p == 0 {
			return math.Inf(-1)
		}
		if p == 1 {
			return math.Inf(1)
		}
		if p == 0.5 {
			return 0
		}
		if p < 0.5 {
			return -statsPpf(d, 1-p)
		}
		return statsInvertCDF(func(x float64) float64 { return statsCdf(d, x) }, p, 0)
	case "f":
		if p == 0 {
			return 0
		}
		if p == 1 {
			return math.Inf(1)
		}
		return statsInvertCDF(func(x float64) float64 { return statsCdf(d, x) }, p, 0)
	}
	return math.NaN()
}

func statsMean(d *runtime.Distribution) float64 {
	switch d.Kind {
	case "binomial":
		return d.Params[0] * d.Params[1]
	case "poisson":
		return d.Params[0]
	case "normal":
		return d.Params[0]
	case "uniform":
		return (d.Params[0] + d.Params[1]) / 2
	case "exponential":
		return 1 / d.Params[0]
	case "lognormal":
		mu, sigma := d.Params[0], d.Params[1]
		return math.Exp(mu + sigma*sigma/2)
	case "weibull":
		k, lambda := d.Params[0], d.Params[1]
		return lambda * math.Gamma(1+1/k)
	case "gamma":
		return d.Params[0] * d.Params[1]
	case "chiSquared":
		return d.Params[0]
	case "beta":
		a, b := d.Params[0], d.Params[1]
		return a / (a + b)
	case "studentT":
		nu := d.Params[0]
		if nu <= 1 {
			return math.NaN()
		}
		return 0
	case "f":
		d2 := d.Params[1]
		if d2 <= 2 {
			return math.NaN()
		}
		return d2 / (d2 - 2)
	}
	return math.NaN()
}

func statsVariance(d *runtime.Distribution) float64 {
	switch d.Kind {
	case "binomial":
		n, p := d.Params[0], d.Params[1]
		return n * p * (1 - p)
	case "poisson":
		return d.Params[0]
	case "normal":
		s := d.Params[1]
		return s * s
	case "uniform":
		w := d.Params[1] - d.Params[0]
		return w * w / 12
	case "exponential":
		return 1 / (d.Params[0] * d.Params[0])
	case "lognormal":
		mu, sigma := d.Params[0], d.Params[1]
		return (math.Exp(sigma*sigma) - 1) * math.Exp(2*mu+sigma*sigma)
	case "weibull":
		k, lambda := d.Params[0], d.Params[1]
		g1 := math.Gamma(1 + 1/k)
		g2 := math.Gamma(1 + 2/k)
		return lambda * lambda * (g2 - g1*g1)
	case "gamma":
		return d.Params[0] * d.Params[1] * d.Params[1]
	case "chiSquared":
		return 2 * d.Params[0]
	case "beta":
		a, b := d.Params[0], d.Params[1]
		return a * b / ((a + b) * (a + b) * (a + b + 1))
	case "studentT":
		nu := d.Params[0]
		if nu <= 2 {
			return math.NaN()
		}
		return nu / (nu - 2)
	case "f":
		d1, d2 := d.Params[0], d.Params[1]
		if d2 <= 4 {
			return math.NaN()
		}
		return 2 * d2 * d2 * (d1 + d2 - 2) / (d1 * (d2 - 2) * (d2 - 2) * (d2 - 4))
	}
	return math.NaN()
}

// statsRNG returns a seeded RNG from an optional opts dict {"seed": k}.
func statsRNG(opts runtime.Value) (*mrand.Rand, error) {
	seed := time.Now().UnixNano()
	if opts != nil {
		dict, ok := opts.(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("sample options must be a dict")
		}
		if v, ok := ndDictValue(dict, "seed"); ok {
			n, ok := AsInt64(v)
			if !ok {
				return nil, fmt.Errorf("sample seed must be an int")
			}
			seed = n
		}
	}
	return mrand.New(mrand.NewSource(seed)), nil //nolint:gosec
}

func statsDrawOne(d *runtime.Distribution, rng *mrand.Rand) float64 {
	switch d.Kind {
	case "binomial":
		n, p := int(d.Params[0]), d.Params[1]
		count := 0
		for i := 0; i < n; i++ {
			if rng.Float64() < p {
				count++
			}
		}
		return float64(count)
	case "poisson":
		lambda := d.Params[0]
		if lambda < 30 {
			l := math.Exp(-lambda)
			k := 0
			prod := 1.0
			for {
				k++
				prod *= rng.Float64()
				if prod <= l {
					return float64(k - 1)
				}
			}
		}
		// large lambda: inverse-CDF from a normal-approx starting point
		u := rng.Float64()
		k := math.Max(0, math.Floor(lambda+math.Sqrt(lambda)*rng.NormFloat64()))
		for statsCdf(d, k) < u {
			k++
		}
		for k > 0 && statsCdf(d, k-1) >= u {
			k--
		}
		return k
	case "normal":
		return rng.NormFloat64()*d.Params[1] + d.Params[0]
	case "uniform":
		a, b := d.Params[0], d.Params[1]
		return a + rng.Float64()*(b-a)
	case "exponential":
		return rng.ExpFloat64() / d.Params[0]
	case "lognormal":
		return math.Exp(rng.NormFloat64()*d.Params[1] + d.Params[0])
	case "weibull":
		k, lambda := d.Params[0], d.Params[1]
		return lambda * math.Pow(-math.Log(1-rng.Float64()), 1/k)
	case "gamma":
		return statsSampleGamma(d.Params[0], d.Params[1], rng)
	case "chiSquared":
		return statsSampleGamma(d.Params[0]/2, 2, rng)
	case "beta":
		a, b := d.Params[0], d.Params[1]
		ga := statsSampleGamma(a, 1, rng)
		gb := statsSampleGamma(b, 1, rng)
		return ga / (ga + gb)
	case "studentT":
		nu := d.Params[0]
		z := rng.NormFloat64()
		v := statsSampleGamma(nu/2, 2, rng)
		return z / math.Sqrt(v/nu)
	case "f":
		d1, d2 := d.Params[0], d.Params[1]
		v1 := statsSampleGamma(d1/2, 2, rng)
		v2 := statsSampleGamma(d2/2, 2, rng)
		return (v1 / d1) / (v2 / d2)
	}
	return math.NaN()
}

func statsSample(d *runtime.Distribution, args []runtime.Value) (runtime.Value, error) {
	if len(args) == 0 {
		rng, _ := statsRNG(nil)
		v := statsDrawOne(d, rng)
		if statsIsDiscrete(d.Kind) {
			return runtime.SmallInt{Value: int64(v)}, nil
		}
		return runtime.Float{Value: v}, nil
	}
	n, ok := AsInt64(args[0])
	if !ok || n < 0 {
		return nil, fmt.Errorf("sample(n): n must be a non-negative int")
	}
	if len(args) > 2 {
		return nil, fmt.Errorf("sample expects (n) or (n, opts)")
	}
	var opts runtime.Value
	if len(args) == 2 {
		opts = args[1]
	}
	rng, err := statsRNG(opts)
	if err != nil {
		return nil, err
	}
	count := int(n)
	if statsIsDiscrete(d.Kind) {
		out := make([]int64, count)
		for i := range out {
			out[i] = int64(statsDrawOne(d, rng))
		}
		return &runtime.NDArray{F64: nil, I64: out, Dtype: runtime.NDInt64,
			Shape: []int{count}, Strides: runtime.RowMajorStrides([]int{count})}, nil
	}
	out := make([]float64, count)
	for i := range out {
		out[i] = statsDrawOne(d, rng)
	}
	return &runtime.NDArray{F64: out, Dtype: runtime.NDFloat64,
		Shape: []int{count}, Strides: runtime.RowMajorStrides([]int{count})}, nil
}
