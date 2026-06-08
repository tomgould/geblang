package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Server concurrency verification for direct http.serve/listen handlers. After
// Phase 5, such handlers run with per-request isolated state on BOTH backends
// (the evaluator deep-clones the handler closure; the VM runs the handler on a
// fresh callVM with a deep-cloned globals snapshot), so a handler mutating a
// captured singleton neither races under concurrent load nor persists across
// requests. These tests build geblang (one with -race) and confirm that.
//
// NOTE: this covers DIRECT http.serve/listen handlers. Complete isolation for
// framework route handlers that cross module boundaries (web.router / gebweb) is
// a separate, scoped engine project (see docs/http-concurrency-evaluation.md).
//
// Gated behind GEBLANG_CONCURRENCY_REPRO=1 because each builds the binary
// (the race one builds with -race), which is slow for the normal suite.
//
//	GEBLANG_CONCURRENCY_REPRO=1 go test -run TestConcurrency ./cmd/geblang

const sharedStateRaceProgram = `import http;
import io;
class Store { dict<string, int> hits; func Store() { this.hits = {}; } }
let store = Store();
let server = http.listen("127.0.0.1:0", func(any req): dict<string, any> {
    store.hits["c"] = (store.hits.get("c") ?? 0) + 1;
    return {"status": 200, "body": "ok"};
});
let base = "http://" + http.serverAddr(server) + "/";
let urls = [];
for (i in 1..300) { urls = urls.push(base); }
let results = await http.getAll(urls, {"limit": 32});
io.println("done " + (results.length() as string));
http.close(server);
`

const statePersistenceProgram = `import http;
import io;
class Store { int n; func Store() { this.n = 0; } }
let store = Store();
let server = http.listen("127.0.0.1:0", func(any req): dict<string, any> {
    store.n = store.n + 1;
    return {"status": 200, "body": "${store.n}"};
});
let addr = http.serverAddr(server);
for (i in 1..3) {
    let r = http.get("http://" + addr + "/");
    io.println(r.text());
}
http.close(server);
`

func buildGeblangBinary(t *testing.T, raceDetector bool) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "geblang")
	args := []string{"build"}
	if raceDetector {
		args = append(args, "-race")
	}
	args = append(args, "-o", bin, ".")
	if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
		t.Fatalf("go build geblang: %v\n%s", err, out)
	}
	return bin
}

func writeProgram(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prog.gb")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write program: %v", err)
	}
	return path
}

// TestConcurrencyDirectHandlerNoRace confirms, via the race detector, that a
// direct http handler mutating a captured singleton under concurrent load does
// NOT race on either backend (both isolate handler state per request).
func TestConcurrencyDirectHandlerNoRace(t *testing.T) {
	if os.Getenv("GEBLANG_CONCURRENCY_REPRO") != "1" {
		t.Skip("set GEBLANG_CONCURRENCY_REPRO=1 to run the server concurrency check")
	}
	bin := buildGeblangBinary(t, true)
	prog := writeProgram(t, sharedStateRaceProgram)

	vmOut, vmErr := exec.Command(bin, prog).CombinedOutput()
	t.Logf("VM (-race) exit=%v output:\n%s", vmErr, vmOut)
	if strings.Contains(string(vmOut), "DATA RACE") {
		t.Errorf("VM should not race on a direct handler (per-request isolated); got:\n%s", vmOut)
	}
	if vmErr != nil {
		t.Errorf("VM run should succeed; got %v\n%s", vmErr, vmOut)
	}

	evOut, evErr := exec.Command(bin, "--disable-vm", prog).CombinedOutput()
	t.Logf("evaluator (-race) exit=%v output:\n%s", evErr, evOut)
	if strings.Contains(string(evOut), "DATA RACE") {
		t.Errorf("evaluator should not race; got:\n%s", evOut)
	}
	if evErr != nil {
		t.Errorf("evaluator run should succeed; got %v\n%s", evErr, evOut)
	}
}

// TestConcurrencyDirectHandlerIsolation confirms both backends isolate a direct
// handler's state per request: a captured counter stays 1 across requests rather
// than accumulating (cross-request state must use a thread-safe handle).
func TestConcurrencyDirectHandlerIsolation(t *testing.T) {
	if os.Getenv("GEBLANG_CONCURRENCY_REPRO") != "1" {
		t.Skip("set GEBLANG_CONCURRENCY_REPRO=1 to run the server concurrency check")
	}
	bin := buildGeblangBinary(t, false)
	prog := writeProgram(t, statePersistenceProgram)

	vmOut, err := exec.Command(bin, prog).CombinedOutput()
	if err != nil {
		t.Fatalf("VM run: %v\n%s", err, vmOut)
	}
	if got := strings.Fields(string(vmOut)); strings.Join(got, ",") != "1,1,1" {
		t.Errorf("VM should isolate handler state per request; want 1,1,1, got %q", string(vmOut))
	}

	evOut, err := exec.Command(bin, "--disable-vm", prog).CombinedOutput()
	if err != nil {
		t.Fatalf("evaluator run: %v\n%s", err, evOut)
	}
	if got := strings.Fields(string(evOut)); strings.Join(got, ",") != "1,1,1" {
		t.Errorf("evaluator should isolate handler state per request; want 1,1,1, got %q", string(evOut))
	}
}
