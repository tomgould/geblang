package main

import (
	"os"
	"os/exec"
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

// VM manifestation of the same root bug: a deep-cloned module value lost its
// canonical, so a stateful-native call through it (not in the module's Exports)
// hit the VM's `Canonical != ""` guard and failed. VM-only because the evaluator
// does not support dispatching through a module held as a value at all (a
// separate pre-existing divergence).
func TestCloneCanonicalModuleValueDispatchVM(t *testing.T) {
	bin := buildCMBinary(t)
	dir := t.TempDir()

	mainPath := filepath.Join(dir, "main.gb")
	os.WriteFile(mainPath, []byte(
		"import io;\n"+
			"import clone;\n"+
			"import async.sync as sync;\n"+
			"let cloned = clone.deep({\"s\": sync});\n"+
			"let cmod = cloned[\"s\"];\n"+
			"cmod.mutexNew();\n"+
			"io.println(\"ok\");\n"), 0644)

	out, err := exec.Command(bin, mainPath).CombinedOutput()
	if err != nil {
		t.Fatalf("VM run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("expected 'ok', got: %q", string(out))
	}
}
