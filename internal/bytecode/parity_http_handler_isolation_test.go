package bytecode_test

import "testing"

// TestParityHTTPHandlerIsolation: a bare handler is isolated per request, shareHandler shares it, identically on both backends (the VM previously shared the closure's upvalues: 1,1 eval vs 1,2 VM).
func TestParityHTTPHandlerIsolation(t *testing.T) {
	isolated := `
import http;
import io;
func makeHandler(): callable {
    let state = {"n": 0};
    return func(dict<string, any> req): dict<string, any> {
        state["n"] = (state["n"] as int) + 1;
        return {"status": 200, "body": (state["n"] as string), "headers": {}};
    };
}
let server = http.listen("127.0.0.1:0", makeHandler());
let base = "http://" + http.serverAddr(server);
let a = http.get(base + "/")["body"] as string;
let b = http.get(base + "/")["body"] as string;
http.close(server);
io.println(a + "," + b);
`
	runParityStateful(t, isolated, "1,1\n")

	shared := `
import http;
import io;
func makeHandler(): callable {
    let state = {"n": 0};
    return func(dict<string, any> req): dict<string, any> {
        state["n"] = (state["n"] as int) + 1;
        return {"status": 200, "body": (state["n"] as string), "headers": {}};
    };
}
let server = http.listen("127.0.0.1:0", makeHandler(), {"shareHandler": true});
let base = "http://" + http.serverAddr(server);
let a = http.get(base + "/")["body"] as string;
let b = http.get(base + "/")["body"] as string;
http.close(server);
io.println(a + "," + b);
`
	runParityStateful(t, shared, "1,2\n")
}
