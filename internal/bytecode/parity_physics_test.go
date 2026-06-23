package bytecode_test

import "testing"

func TestParityPhysics(t *testing.T) {
	runParity(t, `import io;
import physics;
io.println(physics.c());
io.println(physics.convert(5.0, "km", "mi"));
io.println(physics.convert(100.0, "C", "F"));
io.println(physics.convert(0.0, "C", "K"));
io.println(physics.convert(1.0, "h", "s"));
`, "2.99792458e+08\n3.1068559611866697\n212\n273.15\n3600\n")
}
