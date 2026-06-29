package bytecode_test

import "testing"

func TestParityStatsNormal(t *testing.T) {
	runParityNumeric(t, `import io;
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
	runParityNumeric(t, `import io;
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
	runParityNumeric(t, `import io;
import stats;
io.println(stats.gamma(2.0, 2.0).cdf(4.0));
io.println(stats.gamma(2.0, 2.0).mean());
io.println(stats.chiSquared(3.0).cdf(3.0));
io.println(stats.chiSquared(3.0).ppf(0.5));
`, "0.5939941502901617\n4\n0.608374823728911\n2.3659738843753377\n")
}

func TestParityStatsBetaFamily(t *testing.T) {
	runParityNumeric(t, `import io;
import stats;
io.println(stats.beta(2.0, 3.0).cdf(0.5));
io.println(stats.beta(2.0, 3.0).mean());
io.println(stats.studentT(10.0).cdf(2.228));
io.println(stats.studentT(10.0).ppf(0.975));
io.println(stats.f(5.0, 10.0).cdf(3.326));
`, "0.6875\n0.4\n0.9749941140914443\n2.228138851986272\n0.9500067152560671\n")
}

func TestParityStatsDiscrete(t *testing.T) {
	runParityNumeric(t, `import io;
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
	runParityNumeric(t, `import io;
import stats;
let s = stats.normal(5.0, 2.0).sample(20000, {"seed": 123});
io.println(s.mean() > 4.8 && s.mean() < 5.2);
let p = stats.poisson(4.0).sample(20000, {"seed": 9});
io.println(p.mean() > 3.8 && p.mean() < 4.2);
`, "true\ntrue\n")
}

func TestParityStatsTTests(t *testing.T) {
	runParityNumeric(t, `import io;
import stats;
io.println(stats.tTestOneSample([1, 2, 3, 4, 5], 2.0)["statistic"]);
io.println(stats.tTestOneSample([1, 2, 3, 4, 5], 2.0)["pvalue"]);
io.println(stats.tTestIndependent([1, 2, 3, 4, 5], [2, 4, 6, 8, 10])["statistic"]);
io.println(stats.tTestIndependent([1, 2, 3, 4, 5], [2, 4, 6, 8, 10], {"equalVar": false})["df"]);
io.println(stats.tTestPaired([1, 2, 3, 4], [2, 4, 6, 8])["statistic"]);
`, "1.414213562373095\n0.23019964108049873\n-1.8973665961010275\n5.882352941176471\n-3.872983346207417\n")
}

func TestParityStatsChiSquare(t *testing.T) {
	runParityNumeric(t, `import io;
import stats;
io.println(stats.chiSquareTest([10, 20, 30, 40], [25, 25, 25, 25])["statistic"]);
io.println(stats.chiSquareTest([10, 20, 30, 40], [25, 25, 25, 25])["df"]);
io.println(stats.chiSquareIndependence([[10, 20], [30, 40]])["statistic"]);
io.println(stats.chiSquareIndependence([[10, 20], [30, 40]])["df"]);
`, "20\n3\n0.7936507936507936\n1\n")
}

func TestParityStatsNonparametric(t *testing.T) {
	runParityNumeric(t, `import io;
import stats;
io.println(stats.mannWhitneyU([1, 2, 3, 4], [5, 6, 7, 8])["statistic"]);
io.println(stats.mannWhitneyU([1, 2, 3, 4], [5, 6, 7, 8])["pvalue"]);
io.println(stats.ksTest([1, 2, 3, 4, 5], [6, 7, 8, 9, 10])["statistic"]);
`, "0\n0.0303828219765776\n1\n")
}

func TestParityStatsConfidenceIntervals(t *testing.T) {
	runParityNumeric(t, `import io;
import stats;
io.println(stats.confidenceIntervalMean([1, 2, 3, 4, 5], 0.95)["low"]);
io.println(stats.confidenceIntervalMean([1, 2, 3, 4, 5], 0.95)["high"]);
io.println(stats.confidenceIntervalProportion(40, 100, 0.95)["low"]);
io.println(stats.confidenceIntervalDiffMeans([1, 2, 3, 4, 5], [2, 4, 6, 8, 10], 0.95)["low"]);
io.println(stats.confidenceIntervalDiffMeans([1, 2, 3, 4, 5], [2, 4, 6, 8, 10], 0.95)["high"]);
`, "1.0367568385224448\n4.963243161477555\n0.3039817664728939\n-6.646112680506018\n0.6461126805060187\n")
}

func TestParityStatsLinregress(t *testing.T) {
	runParityNumeric(t, `import io;
import stats;
let fit = stats.linregress([1, 2, 3, 4, 5], [2, 4, 5, 4, 5]);
io.println(fit["slope"]);
io.println(fit["intercept"]);
io.println(fit["r2"]);
io.println(fit["stderr"]);
io.println(fit["pvalue"]);
`, "0.6\n2.2\n0.6000000000000001\n0.28284271247461906\n0.12402706265755459\n")
}

func TestParityStatsPolyfit(t *testing.T) {
	runParityNumeric(t, `import io;
import stats;
let c = stats.polyfit([0, 1, 2, 3], [1, 3, 7, 13], 2);
io.println(c[0]);
io.println(c[1]);
io.println(c[2]);
io.println(stats.polyval(c, 2.0));
io.println(stats.polyval([2.0, 0.0, -1.0], 3.0));
`, "0.9999999999999973\n1.0000000000000087\n0.9999999999999962\n7.0000000000000036\n17\n")
}

func TestParityStatsDescriptive(t *testing.T) {
	runParityNumeric(t, `import io;
import stats;
io.println(stats.skewness([1, 2, 3, 4, 5]));
io.println(stats.kurtosis([1, 2, 3, 4, 5]));
io.println(stats.skewness([1, 2, 3, 4, 10]));
io.println(stats.covariance([1, 2, 3, 4, 5], [2, 4, 5, 4, 5]));
io.println(stats.corrcoef([1, 2, 3, 4, 5], [2, 4, 5, 4, 5]));
`, "0\n-1.3\n1.1384199576606164\n1.5\n0.7745966692414834\n")
}
