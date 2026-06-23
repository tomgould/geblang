package bytecode_test

import "testing"

func TestParityComplex(t *testing.T) {
	runParity(t, `import io;
import complex;
let z = complex.of(3.0, 4.0);
io.println(z.abs());
io.println(z.re());
io.println(z.conj());
io.println(z.add(complex.of(1.0, 1.0)));
io.println(complex.of(1.0, 2.0) * complex.of(3.0, 4.0));
io.println(complex.of(1.0, 1.0) + 2.0);
io.println(-z);
io.println(complex.of(3.0, 4.0) == complex.of(3.0, 4.0));
`, "5\n3\n3-4i\n4+5i\n-5+10i\n3+1i\n-3-4i\ntrue\n")
}
