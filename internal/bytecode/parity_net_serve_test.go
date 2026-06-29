package bytecode_test

import "testing"

// TestParityNetServeIsolation (review 3 finding 3): net.serve isolates its handler per connection by default and shares it under shareHandler, identically on both backends (the evaluator shared it unconditionally and the VM ignored shareHandler for net).
func TestParityNetServeIsolation(t *testing.T) {
	prog := func(opts string) string {
		return `import net;
import io;
func makeHandler(): callable {
    let state = {"n": 0};
    return func(dict<string, any> sock): void {
        state["n"] = (state["n"] as int) + 1;
        io.write(sock["stream"], state["n"] as string);
        io.close(sock["stream"]);
    };
}
let server = net.serve("127.0.0.1", 0, makeHandler()` + opts + `);
let parts = (server["localAddr"] as string).split(":");
let port = parts[parts.length() - 1] as int;
let a = net.dial("127.0.0.1", port);
io.println(io.readAll(a["stream"]));
let b = net.dial("127.0.0.1", port);
io.println(io.readAll(b["stream"]));
net.closeListener(server["handle"]);
`
	}
	runParityStateful(t, prog(""), "1\n1\n")
	runParityStateful(t, prog(`, {"shareHandler": true}`), "1\n2\n")
}
