package bytecode_test

import "testing"

func TestParityGeo(t *testing.T) {
	runParity(t, `import io;
import geo;
io.println(geo.haversineDistance(51.5074, -0.1278, 48.8566, 2.3522));
io.println(geo.haversineDistance(51.5074, -0.1278, 48.8566, 2.3522, "mi"));
io.println(geo.bearing(51.5074, -0.1278, 48.8566, 2.3522));
let m = geo.midpoint(51.5074, -0.1278, 48.8566, 2.3522);
io.println(m["lat"]);
let d2 = geo.destination(51.5074, -0.1278, 148.0, 343.5);
io.println(d2["lat"]);
io.println(d2["lon"]);
`, "343.5560603410417\n213.4758388144745\n148.11561687105336\n50.188594877568285\n48.86015519537055\n2.3600157027549358\n")
}
