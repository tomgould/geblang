package bytecode_test

import "testing"

func TestParityStatsNormal(t *testing.T) {
	runParity(t, `import io;
import stats;
let d = stats.normal(0.0, 1.0);
io.println(d.pdf(0.0));
io.println(d.cdf(1.96));
io.println(d.ppf(0.975));
io.println(d.mean());
io.println(d.variance());
io.println(d.std());
io.println(d.sample(3, {"seed": 1}));
`, "0.3989422804014327\n0.9750021048517795\n1.9599639845400534\n0\n1\n1\n<ndarray float64 [3]>\n")
}

func TestParityStatsClosedForm(t *testing.T) {
	runParity(t, `import io;
import stats;
io.println(stats.uniform(0.0, 10.0).cdf(2.5));
io.println(stats.uniform(0.0, 10.0).ppf(0.25));
io.println(stats.exponential(2.0).cdf(1.0));
io.println(stats.exponential(2.0).mean());
io.println(stats.lognormal(0.0, 1.0).cdf(1.0));
io.println(stats.weibull(2.0, 1.0).cdf(1.0));
io.println(stats.weibull(2.0, 1.0).mean());
`, "0.25\n2.5\n0.8646647167633873\n0.5\n0.5\n0.6321205588285577\n0.8862269254527579\n")
}

func TestParityStatsGamma(t *testing.T) {
	runParity(t, `import io;
import stats;
io.println(stats.gamma(2.0, 2.0).cdf(4.0));
io.println(stats.gamma(2.0, 2.0).mean());
io.println(stats.chiSquared(3.0).cdf(3.0));
io.println(stats.chiSquared(3.0).ppf(0.5));
`, "0.5939941502901617\n4\n0.608374823728911\n2.3659738843753377\n")
}

func TestParityStatsBetaFamily(t *testing.T) {
	runParity(t, `import io;
import stats;
io.println(stats.beta(2.0, 3.0).cdf(0.5));
io.println(stats.beta(2.0, 3.0).mean());
io.println(stats.studentT(10.0).cdf(2.228));
io.println(stats.studentT(10.0).ppf(0.975));
io.println(stats.f(5.0, 10.0).cdf(3.326));
`, "0.6875\n0.4\n0.9749941140914443\n2.228138851986272\n0.9500067152560671\n")
}

func TestParityStatsDiscrete(t *testing.T) {
	runParity(t, `import io;
import stats;
io.println(stats.binomial(20, 0.5).pdf(10.0));
io.println(stats.binomial(20, 0.5).cdf(12.0));
io.println(stats.binomial(20, 0.5).mean());
io.println(stats.poisson(4.0).pdf(3.0));
io.println(stats.poisson(4.0).cdf(5.0));
io.println(stats.poisson(4.0).sample(3, {"seed": 7}));
`, "0.1761970520019523\n0.8684120178222655\n10\n0.19536681481316454\n0.7851303870304054\n<ndarray int64 [3]>\n")
}

func TestParityStatsSampleMean(t *testing.T) {
	runParity(t, `import io;
import stats;
let s = stats.normal(5.0, 2.0).sample(20000, {"seed": 123});
io.println(s.mean() > 4.8 && s.mean() < 5.2);
let p = stats.poisson(4.0).sample(20000, {"seed": 9});
io.println(p.mean() > 3.8 && p.mean() < 4.2);
`, "true\ntrue\n")
}
