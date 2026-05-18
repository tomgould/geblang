# Observability

Geblang provides lightweight observability modules for scripts, services, tests,
and framework code:

- `log` for structured logging to stdout, stderr, files, or custom handlers.
- `metrics` for in-process counters, gauges, and simple timings.
- `trace` for span-style request/workflow traces.
- `profile` for runtime memory and GC diagnostics.

These modules are intentionally small. They provide useful defaults and clear
extension points without requiring a collector, daemon, or external service.

## Logging

Import `log` for structured log entries. A logger is a runtime handle returned
by `log.stdout`, `log.stderr`, `log.file`, or `log.custom`.

```gb
import log;

let logger = log.stdout();
log.info(logger, "server started", {"port": 8080});
```

### Built-In Logger Destinations

| Function | Returns | Description |
|----------|---------|-------------|
| `stdout()` | logger handle | Write JSON log lines to stdout |
| `stderr()` | logger handle | Write JSON log lines to stderr |
| `file(path)` | logger handle | Append JSON log lines to a file |
| `close(logger)` | `void` | Close and unregister a logger |

```gb
let out = log.stdout();
let err = log.stderr();
let file = log.file("/var/log/app.log");
defer log.close(file);

log.info(out, "ready");
log.warn(err, "using fallback config", {"path": "config/local.yaml"});
log.error(file, "request failed", {"status": 500, "path": "/api/users"});
```

Built-in loggers emit one JSON object per line. The exact field order is not
part of the API, but entries include at least:

| Field | Type | Description |
|-------|------|-------------|
| `level` | `string` | `info`, `warn`, `error`, or `debug` |
| `message` | `string` | Log message |
| `fields` | `dict` | Structured fields passed by the caller |
| `timeUnix` | `int` | Timestamp as Unix seconds |

### Levels

| Function | Description |
|----------|-------------|
| `info(logger, message)` | Informational event |
| `info(logger, message, fields)` | Informational event with fields |
| `warn(logger, message)` | Warning event |
| `warn(logger, message, fields)` | Warning event with fields |
| `error(logger, message)` | Error event |
| `error(logger, message, fields)` | Error event with fields |
| `debug(logger, message)` | Debug event |
| `debug(logger, message, fields)` | Debug event with fields |

`fields` must be a dictionary. Keep fields machine-readable: prefer ids,
status codes, durations, paths, and booleans over preformatted message text.

```gb
log.info(logger, "user login", {
    "userId": "42",
    "ip": request["remoteAddr"],
    "remember": true
});
```

### `log.LogInterface`

Custom handlers can implement the exported `log.LogInterface`. Its required
method has this shape:

```gb
func handle(string level, string message, dict<string, any> fields): void;
```

Use it when a class should be accepted by logging-aware code or when you want
compile-time checking of the handler shape.

```gb
import io;
import json;
import log;

class JsonSink implements log.LogInterface {
    func handle(string level, string message, dict<string, any> fields): void {
        io.println(json.stringify({
            "level": level,
            "message": message,
            "fields": fields
        }));
    }
}

let logger = log.custom(JsonSink());
log.info(logger, "custom logger ready", {"handler": "json"});
```

`log.custom(handler)` still checks structurally for a compatible `handle`
method, but implementing `log.LogInterface` makes the contract explicit:

```gb
class MemoryLogger implements log.LogInterface {
    list<dict<string, any>> entries = [];

    func handle(string level, string message, dict<string, any> fields): void {
        this.entries = this.entries.push({
            "level": level,
            "message": message,
            "fields": fields
        });
    }
}

let sink = MemoryLogger();
let logger = log.custom(sink);

log.error(logger, "validation failed", {"field": "email"});
io.println(sink.entries[0]["message"]);
```

Custom handlers are useful for:

- writing to external services;
- forwarding logs to test assertions;
- adapting logs into framework-specific event systems;
- redacting or transforming fields before output.

### Logger Lifecycle

Call `log.close(logger)` when a logger is no longer needed. File loggers close
their underlying file handle. Stdout/stderr/custom loggers unregister the
runtime handle.

```gb
let file = log.file("app.log");
defer log.close(file);
```

Do not write to a logger after closing it; the runtime will report an unknown
logger handle.

### Logging Patterns

Create a request logger:

```gb
func logRequest(any logger, dict<string, any> req, int status, int durationMs): void {
    log.info(logger, "http request", {
        "method": req["method"],
        "path": req["path"],
        "status": status,
        "durationMs": durationMs
    });
}
```

Capture logs in tests:

```gb
class Capture implements log.LogInterface {
    list<dict<string, any>> entries = [];

    func handle(string level, string message, dict<string, any> fields): void {
        this.entries = this.entries.push({
            "level": level,
            "message": message,
            "fields": fields
        });
    }
}

let capture = Capture();
let logger = log.custom(capture);
log.warn(logger, "rate limit", {"limit": 100});

io.println(capture.entries.length());
```

## Metrics

Import `metrics` for process-local counters and gauges. Metric names are
free-form strings; use dot-separated names by convention:
`"http.requests"`, `"jobs.completed"`, `"db.pool.open"`.

### Recording Values

| Function | Returns | Description |
|----------|---------|-------------|
| `inc(name)` | `void` | Increment a counter by 1 |
| `inc(name, amount)` | `void` | Increment by an integer amount |
| `set(name, value)` | `void` | Set a metric to an absolute numeric value |
| `reset()` | `void` | Clear all metrics |

```gb
metrics.inc("jobs.completed");
metrics.inc("bytes.sent", 4096);
metrics.set("queue.depth", 12);
```

### Reading Values

| Function | Returns | Description |
|----------|---------|-------------|
| `get(name)` | number | Current value for one metric |
| `snapshot()` | `dict<string, number>` | Copy of all metrics |

```gb
let jobs = metrics.get("jobs.completed");
let all = metrics.snapshot();
```

### Timing

| Function | Returns | Description |
|----------|---------|-------------|
| `now()` | opaque timestamp | Monotonic timestamp |
| `duration(start)` | `int` | Milliseconds since `start` |

```gb
let start = metrics.now();
# work
metrics.set("job.durationMs", metrics.duration(start));
```

Metrics are in-process. Export snapshots to logs, HTTP endpoints, or external
collectors when you need persistence.

## Trace

Import `trace` for lightweight span-based tracing. Spans are handles while they
are active and dictionaries after `snapshot`.

### Span API

| Function | Returns | Description |
|----------|---------|-------------|
| `start(name)` | span handle | Start a span |
| `event(span, name, fields)` | `void` | Attach an event |
| `end(span)` | `void` | Finish a span |
| `snapshot()` | `list<dict>` | Completed spans |
| `reset()` | `void` | Clear completed spans |

```gb
let span = trace.start("load-users");
trace.event(span, "query", {"table": "users"});
trace.event(span, "marshal", {"format": "json"});
trace.end(span);

let spans = trace.snapshot();
```

Completed span dictionaries contain:

| Key | Type | Description |
|-----|------|-------------|
| `name` | `string` | Span name |
| `startUnix` | `int` | Start timestamp |
| `endUnix` | `int` | End timestamp |
| `durationMs` | `int` | Duration in milliseconds |
| `events` | `list<dict>` | Span events |

Event dictionaries contain `name`, `fields`, and `timeUnix`.

## Profile

Import `profile` for runtime diagnostics. These helpers are useful for
debugging memory-heavy scripts and checking allocation pressure.

### Memory Stats

| Function | Returns | Description |
|----------|---------|-------------|
| `memStats()` | `dict` | Runtime memory and GC counters |
| `gc()` | `void` | Force a garbage collection cycle |

```gb
let mem = profile.memStats();
io.println("heap alloc bytes: " + mem["heapAlloc"] as string);
io.println("sys bytes: " + mem["sys"] as string);
io.println("gc cycles: " + mem["numGC"] as string);
```

Common `memStats` fields include `heapAlloc`, `heapSys`, `heapInuse`,
`heapObjects`, `sys`, `numGC`, and `pauseTotalNs`.

### Timing

| Function | Returns | Description |
|----------|---------|-------------|
| `now()` | opaque timestamp | Monotonic timestamp |
| `elapsed(start)` | `float` | Milliseconds since `start` |

```gb
let start = profile.now();
# work
let ms = profile.elapsed(start);
io.println("elapsed ms: " + ms as string);
```

## Profiler

Import `profiler` for precise CPU time and heap measurements from a native
Go runtime interface. Use `profiler.snapshot()` and `profiler.delta()` together
to bracket a section of code, or call `profiler.memory()` and `profiler.cpu()`
for one-off readings.

This module is always available as a native module and does not require
importing from `stdlib/`.

### Functions

| Function | Returns | Description |
|----------|---------|-------------|
| `snapshot()` | `dict` | Captures wall clock (`wall_ns`), heap allocation (`heap_alloc`, `peak_alloc`, `total_alloc`), GC count (`num_gc`), and CPU nanoseconds (`cpu_user_ns`, `cpu_sys_ns`) |
| `delta(snapshot)` | `dict` | Returns measurements since the snapshot: `elapsed_ms`, `cpu_ms`, `heap_alloc`, `allocs`, `gc_count` |
| `memory()` | `dict` | Returns `heap_alloc`, `peak_alloc`, `heap_sys`, `stack_sys`, `total_alloc`, `gc_count` |
| `cpu()` | `dict` | Returns `user_ms` and `sys_ms` CPU time used by this process |
| `peak()` | `dict` | Returns `peak_alloc`: the highest heap allocation observed since the profiler was first called |

`peak_alloc` tracks the maximum live heap bytes seen across all profiler calls in the
process lifetime - equivalent to PHP's `memory_get_peak_usage()`. It is updated on every
call to `snapshot()`, `memory()`, and `peak()`.

### Example

```gb
import profiler;
import io;

let snap = profiler.snapshot();

# do some work
let sum = 0;
for i in range(0, 1000000) {
    sum = sum + i;
}

let d = profiler.delta(snap);
io.println("elapsed: " + d["elapsed_ms"] as string + " ms");
io.println("cpu: " + d["cpu_ms"] as string + " ms");
io.println("heap delta: " + d["heap_alloc"] as string + " bytes");
io.println("allocations: " + d["allocs"] as string + " bytes total");
io.println("gc cycles: " + d["gc_count"] as string);
```

For a one-off memory check including peak:

```gb
import profiler;
import io;

let mem = profiler.memory();
io.println("heap in use: " + mem["heap_alloc"] as string + " bytes");
io.println("peak heap:   " + mem["peak_alloc"] as string + " bytes");
io.println("heap from OS: " + mem["heap_sys"] as string + " bytes");
```

To check peak memory at the end of a script (similar to `memory_get_peak_usage()` in PHP):

```gb
import profiler;
import io;

# ... script work ...

let p = profiler.peak();
io.println("peak memory: " + p["peak_alloc"] as string + " bytes");
```
