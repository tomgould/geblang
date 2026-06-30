package evaluator

import (
	"errors"
	"sync"
	"testing"
	"time"

	"geblang/internal/ast"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

type notifyWriter struct {
	once    sync.Once
	started chan struct{}
}

func (w *notifyWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	return len(p), nil
}

func parseForTest(t *testing.T, src string) *ast.Program {
	t.Helper()
	pr := parser.New(lexer.New(src))
	prog := pr.ParseProgram()
	if errs := pr.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return prog
}

func TestWorkerThreadsGetFreshIDs(t *testing.T) {
	// func* is not Geblang syntax; generators use named funcs with generator<T> return type
	src := `import async;
func gen(): generator<int> { yield 1; yield 2; }
let t = async.run(func(): int { let x = 1; return x; });
let _ = await t;
for (v in gen()) { let _ = v; }
`
	program := parseForTest(t, src)

	var mu sync.Mutex
	started := map[int]string{}
	exited := map[int]bool{}
	pausedThreads := map[int]bool{}

	ev := NewWithArgsAndModulePaths(discardWriter{}, nil, nil)
	ev.SetDebugSourcePath("test.gb")
	ev.SetDebugThreadHooks(
		func(id int, name string) { mu.Lock(); started[id] = name; mu.Unlock() },
		func(id int) { mu.Lock(); exited[id] = true; mu.Unlock() },
	)
	ev.SetDebugHook(func(p DebugPause) { mu.Lock(); pausedThreads[p.ThreadID] = true; mu.Unlock() })

	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("eval: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(started) < 2 {
		t.Fatalf("expected at least 2 worker threads (async + generator), got %#v", started)
	}
	for id := range started {
		if id < 2 {
			t.Fatalf("worker thread id must be >= 2, got %d", id)
		}
		if !exited[id] {
			t.Fatalf("worker thread %d started but never exited", id)
		}
		if !pausedThreads[id] {
			t.Fatalf("worker thread %d started but never executed a statement", id)
		}
	}
}

func TestDebugHookFiresInsideAsyncWorker(t *testing.T) {
	src := `import async;
let t = async.run(func(): int {
    let total = 0;
    total = total + 1;
    return total;
});
let _ = await t;
`
	program := parseForTest(t, src)

	var mu sync.Mutex
	threadIDs := map[int]int{} // threadID -> count of statements seen
	ev := NewWithArgsAndModulePaths(discardWriter{}, nil, nil)
	ev.SetDebugSourcePath("test.gb")
	ev.SetDebugHook(func(p DebugPause) {
		mu.Lock()
		threadIDs[p.ThreadID]++
		mu.Unlock()
	})

	if _, err := ev.Eval(program); err != nil {
		t.Fatalf("eval: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if threadIDs[1] == 0 {
		t.Fatalf("main thread (id 1) should have fired; got %#v", threadIDs)
	}
	total := 0
	for _, c := range threadIDs {
		total += c
	}
	// main fires ~2 stmts; fewer than 5 means the worker body was skipped
	if total < 5 {
		t.Fatalf("hook did not reach the worker body; only %d statements fired %#v", total, threadIDs)
	}
	// catch wrong-thread-id: at least one worker thread (id >= 2) must have fired
	workerSeen := false
	for id := range threadIDs {
		if id >= 2 {
			workerSeen = true
		}
	}
	if !workerSeen {
		t.Fatalf("expected a worker thread (id >= 2) to fire the hook; got %#v", threadIDs)
	}
}

func TestDebugEvaluationCancellationStopsRunningLoop(t *testing.T) {
	runDebugCancellationTest(t, `import io;
func spin(): int {
    io.println("started");
    while (true) {}
    return 0;
}
let result = spin();
`)
}

func TestDebugEvaluationCancellationStopsAsyncWorker(t *testing.T) {
	runDebugCancellationTest(t, `import async;
import io;
let task = async.run(func(): int {
    io.println("started");
    while (true) {}
    return 0;
});
let result = await task;
`)
}

func runDebugCancellationTest(t *testing.T, src string) {
	t.Helper()
	program := parseForTest(t, src)
	writer := &notifyWriter{started: make(chan struct{})}
	ev := NewWithArgsAndModulePaths(writer, nil, nil)
	cancel := make(chan struct{})
	restore := ev.BeginDebugEvaluation(cancel)
	defer restore()

	result := make(chan error, 1)
	go func() {
		_, err := ev.Eval(program)
		result <- err
	}()

	select {
	case <-writer.started:
	case <-time.After(2 * time.Second):
		t.Fatal("evaluation did not enter the loop")
	}
	close(cancel)

	select {
	case err := <-result:
		if !errors.Is(err, ErrDebugEvaluationCancelled) {
			t.Fatalf("Eval error = %v, want cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled evaluation did not exit")
	}
}
