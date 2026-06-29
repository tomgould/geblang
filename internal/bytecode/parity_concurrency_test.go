package bytecode_test

import "testing"

// Concurrent callbacks invoking a shared callable must be race-free (guards the callCallableSlow fix; enforced under `go test -race`).
func TestParityConcurrentCallbacksShareCallable(t *testing.T) {
	runParityWithStdlib(t, `import io;
import async.tasks as task;

func square(int x): int { return x * x; }

let xs = [];
for (int i = 0; i < 40; i++) { xs = xs.push(i); }

let out = task.map(xs, func(int x): int { return square(x); }, {"concurrency": 8});
let total = 0;
for (v in out) { total = total + (v as int); }
io.println(total);
`, "20540\n")
}
