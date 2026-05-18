# Async

Geblang's async model is based on tasks - lightweight concurrent units of work
backed by goroutines. An `async func` starts immediately when called and returns
a `Task<T>`. A plain `func` can be wrapped into a task with `async.run`.

## Starting tasks

```gb
import async;

async func fetchUser(int id): dict {
    await async.sleep(10);   # simulate I/O
    return {"id": id, "name": "Alice"};
}

let task = fetchUser(1);       # starts immediately; returns Task<dict>
let user = await task;         # wait for result
```

`async.run(callable)` wraps any callable as a task:

```gb
let task = async.run(func(): string {
    return "done";
});
let result = await task;
```

## Concurrency: start first, await later

The whole point of tasks is to overlap work. Do not immediately `await` every
task:

```gb
# Good - both I/O operations run in parallel
let configTask   = aio.readText("config/app.json");
let templateTask = aio.readText("templates/page.html");

let config   = await configTask;
let template = await templateTask;

# Bad - sequential despite async
let config   = await aio.readText("config/app.json");
let template = await aio.readText("templates/page.html");
```

## Checking task status

```gb
let done = async.done(task);   # bool - true if the task has finished
```

Use `async.done` to poll rather than block, for example in a progress loop
with `sys.sleep`.

## Combining tasks

`async.all([tasks])` waits for every input task to complete and returns the
results as an ordered list. If any task fails, the rest are cancelled and the
first error is re-thrown when the result is awaited.

```gb
let pages = await async.all([fetchPage(1), fetchPage(2), fetchPage(3)]);
io.println("got " + str(pages.length) + " pages");
```

`async.race([tasks])` returns the value of the first task to finish. The
remaining tasks are cancelled.

```gb
let winner = await async.race([primary(), backup()]);
```

## Timeouts

`async.timeout(task, ms)` wraps a task with a millisecond deadline. If the
inner task does not complete in time it is cancelled and the wrapper task
re-throws a `RuntimeError` ("async.timeout: task did not complete within Nms")
when the wrapper is awaited. The error propagates out of the script unless
caught further up.

```gb
let response = await async.timeout(ahttp.get(url), 2000);
```

## Cancellation

A task can be cancelled with `async.cancel(task)` or `task.cancel()`. After
cancellation, `task.cancelled` is `true` and any `await` returns a sentinel
error. Long-running work should check periodically (for example by `await`-ing
short sleeps) to allow cancellation to take effect.

```gb
let job = async.run(func(): int {
    await async.sleep(1000);
    return 1;
});

job.cancel();
io.println(job.cancelled);    # true
```

## Sleeping

```gb
await async.sleep(1000);          # pause for 1 second inside an async func
async.sleep(500);                 # returns a Task<null>; await it to pause
```

`async.sleep` is non-blocking. It yields the goroutine for the given number of
milliseconds without freezing the process.

## Scheduling: Timer, Ticker, Interval

For callback-style scheduling (fire-once or repeating), import
`time.scheduler`. The classes wrap async tasks with cancellation:

```gb
import time.scheduler as sched;

# Fire once after 500ms; cancellable.
let t = sched.Timer(500, func(): void {
    io.println("done");
});

# Fire every 1s until stopped.
let ticker = sched.Ticker(1000, func(): void {
    io.println("tick");
});
# ...
ticker.stop();

# Run the callback immediately, then every interval.
let poll = sched.Interval(60000, refreshConfig);
poll.stop();
```

`sched.setTimeout(ms, fn)` and `sched.setInterval(ms, fn)` are aliases that
match JavaScript naming for callers more familiar with that vocabulary.

| Method | Type | Description |
|--------|------|-------------|
| `Timer.cancel()` | void | Prevents the callback from firing if it has not yet run. |
| `Timer.didFire()` | bool | True after the callback runs; false if cancelled in time. |
| `Timer.wait()` | Task | Resolves once the timer fires or is cancelled. |
| `Ticker.stop()` | void | Halts further ticks; an in-flight tick still completes. |
| `Ticker.ticks()` | int | How many times the callback has run so far. |
| `Ticker.wait()` | Task | Resolves once the ticker is stopped. |

## Error handling in tasks

If a task panics or returns an error, `await` re-throws it:

```gb
try {
    let result = await riskyTask;
} catch (error e) {
    io.println("task failed: " + e.message);
}
```

## Async I/O submodules

The `async.io`, `async.http`, `async.net`, and `async.stream` source modules
wrap the corresponding native modules so every call returns a task. This
allows them to be started concurrently and awaited later.

Import them with an alias:

```gb
import async.io     as aio;
import async.http   as ahttp;
import async.net    as anet;
import async.stream as astream;
import async.rate   as rate;
```

### `async.io`

Task-returning wrappers for file and directory operations:

```gb
let t1 = aio.readText("data/users.json");
let t2 = aio.readText("data/config.json");
let users  = await t1;
let config = await t2;
```

Functions: `readText`, `writeText`, `appendText`, `readBytes`, `writeBytes`,
`appendBytes`, `read`, `readAll`, `write`, `writeln`, `flush`, `close`,
`stat`, `listDir`.

### `async.http`

Task-returning HTTP client calls:

```gb
let t = ahttp.get("https://api.example.com/users");
let response = await t;
let users = ahttp.parseJson(response);
```

Functions: `get`, `post`, `postJson`, `request`, `requestWithOptions`,
`parseJson`.

### `async.net`

Task-returning TCP/UDP socket operations. See `14-http-net.md` for the
synchronous `net` equivalents.

```gb
let listener = await anet.listenTcp("127.0.0.1:9000");
let conn     = await anet.accept(listener);
let data     = await anet.read(conn, 1024);
await anet.write(conn, data);
await anet.close(conn);
```

### `async.stream`

Task-returning wrappers for streaming parsers:

```gb
let stream = await astream.jsonStream(source, func(value): void {
    io.println(json.stringify(value));
});
```

Functions: `jsonStream`, `jsonReader`, `yamlStream`, `yamlReader`,
`xmlStream`, `xmlReader`, `csvStream`, `csvReader`.

### `async.rate`

Rate-limit and debounce wrappers for callable values:

```gb
let log = rate.throttle(func(string m): void { io.println(m); }, 200);
log("a"); log("b"); log("c");                # only one io.println in 200ms

let save = rate.debounce(func(string text): void { persist(text); }, 300);
save("hello"); save("hello!"); save("hello world");
# persist("hello world") runs 300ms after the last call
```

| Function | Returns | Description |
|----------|---------|-------------|
| `rate.throttle(fn, ms)` | `func` | Calls `fn` at most once per `ms` ms; returns the cached last result for calls inside the window. |
| `rate.debounce(fn, ms)` | `func` | Returns a wrapper that, on each call, schedules `fn` to run after `ms` ms of quiet. Returns a `Task<any>` per call - the superseded ones resolve to `null`. |

## Task values and properties

A task value exposes two read-only properties:

```gb
let t = compute();
if (t.done) {
    io.println("already finished");
}
if (t.cancelled) {
    io.println("was cancelled");
}
```

These are equivalent to `async.done(t)` and inspecting the cancellation state
set by `async.cancel(t)` / `t.cancel()`.
