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

// TestParityHTTPHandlerGlobalAlias (review 3 finding 2): a global value aliased by a closure upvalue must clone as ONE shared object under per-request isolation (the VM split it across two clone states: eval 1, VM 0).
func TestParityHTTPHandlerGlobalAlias(t *testing.T) {
	runParityStateful(t, `import http;
import io;
let shared = [];
func makeHandler(): callable {
    let alias = shared;
    return func(dict<string, any> req): dict<string, any> {
        alias.push(1);
        return {"status": 200, "body": shared.length() as string, "headers": {}};
    };
}
let server = http.listen("127.0.0.1:0", makeHandler());
let base = "http://" + http.serverAddr(server);
let body = http.get(base + "/")["body"] as string;
http.close(server);
io.println(body);
`, "1\n")
}

// TestParityHTTPHandlerCyclicGlobal (review 4 finding 2): per-request isolation deep-clones the globals snapshot; a self-referential dict in scope must not infinitely recurse during that clone (cloneState now memoizes dicts). Both backends must serve the request and terminate.
func TestParityHTTPHandlerCyclicGlobal(t *testing.T) {
	runParityStateful(t, `import http;
import io;
let shared = {};
shared["self"] = shared;
func makeHandler(): callable {
    return func(dict<string, any> req): dict<string, any> {
        return {"status": 200, "body": "ok", "headers": {}};
    };
}
let server = http.listen("127.0.0.1:0", makeHandler());
let base = "http://" + http.serverAddr(server);
let body = http.get(base + "/")["body"] as string;
http.close(server);
io.println(body);
`, "ok\n")
}
