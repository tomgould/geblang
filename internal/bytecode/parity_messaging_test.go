package bytecode_test

import "testing"

// TestParityMessagingBridgesAmqp guards the regression where amqp/kafka were in the compiler's stateful-module list but not the VM's, so amqp.dial worked on the evaluator but was "unsupported native call" on the VM. Both backends must bridge it to the engine and surface the same dial error.
func TestParityMessagingBridgesAmqp(t *testing.T) {
	runParityStateful(t, `import io;
import amqp;
let r = "?";
try { amqp.dial("amqp://127.0.0.1:59999/"); }
catch (Error e) { r = e.message.contains("amqp.dial") && e.message.contains("refused") ? "amqp-dialed" : "gap"; }
io.println(r);
`, "amqp-dialed\n")
}
