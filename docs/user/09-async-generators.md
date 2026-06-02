# Async And Generators

## Async Functions

Async functions use `async func`. Calling one starts it and returns a `Task`.
The call itself does not block; the program only waits when it reaches `await`
or `task.await()`.

```gb
async func double(int x): int {
    return x * 2;
}

let task = double(21);
io.println("calculation has started");
io.println(await task);
```

Tasks can also be inspected:

```gb
io.println(task.done());
io.println(task.await());
```

## Async Module

```gb
import async;

let worker = async.run(func(): int {
    return 42;
});

io.println(async.await(worker));
await async.sleep(100);
```

`async.done(task)` reports completion.

### Combining tasks

`async.all([tasks])` returns a new task that completes when every input task
has completed. The result is a list of values in the original order. If any
input fails, the returned task fails immediately with the first error and the
remaining tasks are cancelled.

```gb
import async;
import io;

let pageA = async.run(func(): string {
    await async.sleep(40);
    return "A";
});
let pageB = async.run(func(): string {
    await async.sleep(60);
    return "B";
});

let pages = await async.all([pageA, pageB]);
io.println(pages[0]);  # "A"
io.println(pages[1]);  # "B"
```

`async.race([tasks])` returns a task that completes with the first finisher's
value. The rest are cancelled.

```gb
let fast = async.run(func(): string {
    await async.sleep(20);
    return "fast";
});
let slow = async.run(func(): string {
    await async.sleep(200);
    return "slow";
});

io.println(await async.race([fast, slow]));  # "fast"
```

### Timeouts

`async.timeout(task, ms)` wraps a task with a deadline. If the task does not
complete within the given milliseconds, the wrapping task fails with a
`RuntimeError` and the inner task is cancelled.

```gb
let slow = async.run(func(): string {
    await async.sleep(500);
    return "never";
});

# Awaiting the timeout wrapper raises a runtime error when the deadline expires:
let result = await async.timeout(slow, 50);   # runtime error after 50ms
```

### Cancellation

A task can be cancelled explicitly with `async.cancel(task)` or `task.cancel()`.
Cancelling marks the task complete with a sentinel error, so any pending or
later `await` returns with that error. Use `task.cancelled` to check the state
non-destructively.

```gb
let job = async.run(func(): int {
    await async.sleep(1000);
    return 1;
});

job.cancel();
io.println(job.cancelled);   # true
```

## Doing Work While A Task Runs

A common mistake is to start a task and immediately await it. That is valid, but
it gives the program no opportunity to do useful work concurrently.

This version blocks straight away:

```gb
let result = await fetchReport();
io.println(result);
```

This version starts the work, continues with local work, then waits only when
the result is actually needed:

```gb
async func fetchReport(): string {
    await async.sleep(100);
    return "report ready";
}

let reportTask = fetchReport();

io.println("started report");
io.println("loading local config");
io.println("rendering progress indicator");

let report = await reportTask;
io.println(report);
```

For polling-style workflows, use `async.done`:

```gb
import async;
import io;

async func background(): string {
    await async.sleep(150);
    return "background result";
}

let task = background();

while (!async.done(task)) {
    io.println("tick");
    await async.sleep(25);
}

io.println(async.await(task));
```

The important rule is that a task value is just a value. Store it in a variable,
put it in a list, pass it to another function, or await it later.

```gb
func awaitAll(list<Task<string>> tasks): list<string> {
    list<string> results = [];
    for (task in tasks) {
        results.push(await task);
    }
    return results;
}
```

## Concurrent I/O Shape

Async is most useful around I/O, timers, HTTP calls, sockets, and work that can
run independently. Prefer the async stdlib wrappers for I/O instead of writing
your own `async func` around synchronous calls:

```gb
import async.io as aio;

let configTask = aio.readText("config/app.json");
let templateTask = aio.readText("templates/page.html");

io.println("both reads have started");

let config = await configTask;
let template = await templateTask;
```

If the second operation depends on the first result, await between them:

```gb
let config = await readConfig();
let template = await readTemplateFor(config);
```

That sequential form is clearer and avoids pretending dependent work is
parallel.

Async modules currently include:

- `async.io`: file and handle reads/writes.
- `async.http`: HTTP client calls.
- `async.net`: TCP/UDP socket operations.
- `async.stream`: JSON, YAML, XML, and CSV streaming parser work.

These APIs return `Task` values. Their current implementation runs host I/O work
outside the caller's flow; the roadmap target is a shared event-loop scheduler
that can power higher-throughput networking engines without changing these
public module APIs.

Example socket workflow:

```gb
import async;
import async.net as anet;
import bytes;
import net;

let listener = await anet.listenTcp("127.0.0.1:0");
let address = net.localAddr(listener);

let server = async.run(func(): string {
    let conn = await anet.accept(listener);
    let message = await anet.read(conn, 4);
    await anet.write(conn, "pong");
    await anet.close(conn);
    return bytes.toString(message);
});

let client = await anet.connectTcp(address);
await anet.write(client, "ping");
io.println(bytes.toString(await anet.read(client, 4)));
io.println(await server);
```

## Error Handling In Tasks

Errors thrown inside an async task are re-raised when the task is awaited:

```gb
async func loadRequired(string path): string {
    if (!io.exists(path)) {
        throw IOError("missing file: " + path);
    }
    return io.readText(path);
}

let task = loadRequired("settings.json");

try {
    io.println(await task);
} catch (IOError e) {
    io.println("could not load settings: " + e.message);
}
```

Use this pattern to start work early, then handle the failure at the point where
the result becomes necessary.

## Generators

Generator functions use `yield` and return lazy `generator` values:

```gb
func numbers(): generator<int> {
    yield 1;
    yield 2;
    yield 3;
}

for (n in numbers()) {
    io.println(n);
}
```

Function literals can be generators:

```gb
let upTo = func(int max): generator<int> {
    for (let int i = 1; i <= max; i++) {
        yield i;
    }
};
```

## Iterable Parameters

Use `iterable<T>` when an API accepts generator-like inputs:

```gb
func sum(iterable<int> values): int {
    int total = 0;
    for (n in values) {
        total += n;
    }
    return total;
}
```

Generators are lazy. Breaking out of a `for-in` loop closes the producer so
unbounded streams do not continue running.

```gb
func lines(list<string> paths): generator<string> {
    for (path in paths) {
        for (line in io.readText(path).split("\n")) {
            yield line;
        }
    }
}

for (line in lines(["a.log", "b.log"])) {
    if (line.contains("ERROR")) {
        io.println(line);
        break;
    }
}
```

Generators and async solve different problems. Generators avoid building a full
collection before iteration. Async tasks allow other work to continue while a
result is pending. For a large remote dataset, an API should usually combine
both ideas: fetch or read chunks asynchronously, then yield records lazily.

## Synchronisation Primitives (1.6.0)

Tasks spawned via `async.run` execute as real goroutines, so when
multiple tasks coordinate over shared state the standard
synchronisation primitives apply. They live in two sub-modules
that wrap Go's `sync` and `sync/atomic` packages.

### `async.sync`: locks, semaphores, wait groups

```gb
import async.sync as sync;

let m = sync.Mutex();
m.lock();
try {
    # protected section
} finally {
    m.unlock();
}
```

| Class | Methods | Description |
|-------|---------|-------------|
| `Mutex` | `lock`, `unlock`, `tryLock` | Mutual-exclusion lock. `tryLock()` returns `false` instead of blocking when the lock is held. |
| `RWMutex` | `lock`, `unlock`, `tryLock`, `rLock`, `rUnlock`, `tryRLock` | Reader-writer lock. Many readers may coexist; writers are exclusive. |
| `Semaphore` | `acquire`, `release`, `tryAcquire` | Counted concurrency guard. Constructor: `Semaphore(permits)`; permits must be at least 1. |
| `WaitGroup` | `add(delta)`, `done`, `wait` | Counter for "this many tasks are running"; `wait()` blocks until the counter hits zero. |

### `async.atomic`: lock-free cells

```gb
import async.atomic as atomic;

let counter = atomic.AtomicInt(0);
counter.add(1);
if (counter.compareAndSwap(1, 42)) {
    /* succeeded */
}
```

| Class | Methods | Description |
|-------|---------|-------------|
| `AtomicInt` | `load`, `store(v)`, `add(delta)`, `compareAndSwap(old, new)` | Sequentially consistent int64. `add` returns the new value; `delta` may be negative. |
| `AtomicBool` | `load`, `store(v)`, `compareAndSwap(old, new)` | Sequentially consistent bool. Common for one-shot flags. |

### Typical patterns

**Counting completed tasks**:

```gb
import async;
import async.sync as sync;

let wg = sync.WaitGroup();
for (let int i = 0; i < n; i++) {
    wg.add(1);
    async.run(func(): void {
        try { doWork(i); } finally { wg.done(); }
    });
}
wg.wait();
```

**Bounded concurrency**:

```gb
import async;
import async.sync as sync;

let throttle = sync.Semaphore(10);   # at most 10 in flight
for (var url in urls) {
    async.run(func(): void {
        throttle.acquire();
        try { fetch(url); } finally { throttle.release(); }
    });
}
```

**Lock-free counter**:

```gb
import async.atomic as atomic;
let hits = atomic.AtomicInt(0);
// in many tasks:
hits.add(1);
// later:
io.println(hits.load());
```
