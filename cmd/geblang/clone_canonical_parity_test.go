package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Dropping Module.Canonical on clone made an aliased-native call in a cloned
// request handler fall back to the global last-write-wins importNames map.
func TestCloneCanonicalServeNativeAlias(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	mainPath := filepath.Join(dir, "main.gb")
	// profiler must be imported last: it wins the global importNames["native"]
	// slot, so the bug only reproduces when the env-local canonical is lost.
	os.WriteFile(mainPath, []byte(
		"import http;\n"+
			"import io;\n"+
			"import async.sync as sync;\n"+
			"import profiler;\n"+
			"func handle(dict<string, any> req): dict<string, any> {\n"+
			"    let m = sync.Mutex();\n"+
			"    m.lock();\n"+
			"    m.unlock();\n"+
			"    return {\"status\": 200, \"body\": \"ok\"};\n"+
			"}\n"+
			"let server = http.listen(\"127.0.0.1:0\", handle);\n"+
			"let resp = http.request(\"http://\" + http.serverAddr(server) + \"/\").send();\n"+
			"io.println(\"status:\" + (resp.status() as string) + \" body:\" + resp.text());\n"+
			"http.close(server);\n"), 0644)

	vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
	assertParitySuccess(t, "cloned-handler native alias", vm, eval, vmErr, evalErr)
	if !strings.Contains(vm, "status:200 body:ok") {
		t.Fatalf("expected 'status:200 body:ok', got: %q", vm)
	}
}

// The VM module-value manifestation of this bug is unreachable now that a module
// identifier cannot be held as a value (semantic analyzer rejects it). The
// canonical-preservation invariant is guarded by
// internal/runtime/clone_test.go TestCloneModulePreservesCanonical and
// tests/stdlib/native_alias_serve_test.gb.
