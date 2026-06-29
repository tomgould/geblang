package bytecode_test

import "testing"

// Concurrent __serialize dispatch (the native instance invoker) must run each call on its own loader worker, never a stale pooled VM. Correct output AND -race clean.
func TestParityConcurrentInstanceSerialize(t *testing.T) {
	runParityWithStdlib(t, `import io;
import json;
import async.tasks as task;
class Box {
    int v;
    func Box(int v) { this.v = v; }
    func __serialize(): any { return {"boxed": this.v}; }
}
let xs = [1, 2, 3, 4, 5, 6, 7, 8];
let out = task.map(xs, func(int x): string { return json.stringify(Box(x)); }, {"concurrency": 4});
for (s in out) { io.println(s); }
`, "{\"boxed\":1}\n{\"boxed\":2}\n{\"boxed\":3}\n{\"boxed\":4}\n{\"boxed\":5}\n{\"boxed\":6}\n{\"boxed\":7}\n{\"boxed\":8}\n")
}

// Cross-module json.parseAs into a class with a constructor must skip the implicit "this" param (guards the DeserializeIntoChunkClass fix).
func TestParityCrossModuleDeserializeConstructorClass(t *testing.T) {
	runMultiModuleParity(t, map[string]string{
		"dto": "module dto;\nexport class Point {\n    int x;\n    int y;\n    func Point(int x, int y) { this.x = x; this.y = y; }\n}\n",
	}, "import io;\nimport json;\nimport dto;\nlet p = json.parseAs(\"{\\\"x\\\":3,\\\"y\\\":4}\", dto.Point);\nio.println(p.x + p.y);\n", "7\n")
}

// Concurrent json.parseAs into a user class (the native class deserializer) must construct on its own loader worker.
func TestParityConcurrentDeserialize(t *testing.T) {
	runParityWithStdlib(t, `import io;
import json;
import async.tasks as task;
class Point {
    int x;
    int y;
    func Point(int x, int y) { this.x = x; this.y = y; }
}
let xs = ["{\"x\":1,\"y\":2}", "{\"x\":3,\"y\":4}", "{\"x\":5,\"y\":6}", "{\"x\":7,\"y\":8}"];
let out = task.map(xs, func(string s): int { let p = json.parseAs(s, Point); return p.x + p.y; }, {"concurrency": 4});
io.println(out);
`, "[3, 7, 11, 15]\n")
}
