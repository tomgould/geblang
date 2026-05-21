# Deployment And Performance Tuning

This chapter covers running Geblang programs in production: shipping the
binary, picking sensible runtime settings, observing live behaviour, and
diagnosing hot spots. None of it changes how you write code - it's about
operating what you've already written.

## Shipping A Single Binary

`geblang build --entry <module> --out <path>` bundles your program, its
stdlib, and any installed dependencies into one statically-linked executable.
The output is a normal Linux/macOS/Windows binary you can `scp` to a server
or copy into a Docker image; the receiver does not need Geblang installed.

```sh
geblang build --entry app --out dist/myapp
./dist/myapp --version
```

For containerised deployments, build inside a minimal base image and copy
the artefact into a scratch image:

```dockerfile
FROM golang:1.26 AS build
WORKDIR /src
COPY . .
RUN GOTOOLCHAIN=local go build -o /out/geblang ./cmd/geblang
RUN /out/geblang build --entry app --out /out/myapp

FROM scratch
COPY --from=build /out/myapp /myapp
ENTRYPOINT ["/myapp"]
```

The resulting image is typically 15-25 MB and starts in tens of
milliseconds. There is no JVM warm-up, no interpreter fork, no source
parsing on the hot path.

## Runtime Settings

### GOMAXPROCS

Geblang inherits Go's scheduler. By default it uses one OS thread per
hardware core. Constrain it under cgroups (Kubernetes, ECS, systemd) so the
scheduler doesn't fight the container's CPU limits:

```sh
GOMAXPROCS=2 ./myapp
```

Tuning matters when the container is allocated fewer cores than the host
exposes - the default `nproc` reads host cores, which leads to many idle
goroutines and unhelpful context switching.

### Garbage Collection

Geblang programs allocate the same way any Go program does, so the same GC
knobs apply:

- `GOGC` (default 100) - target heap growth before each collection. Lower
  values trade more CPU for a smaller resident set; higher values trade
  memory for fewer collections. For latency-sensitive workloads,
  `GOGC=200` and `GOMEMLIMIT=2GiB` is a common starting point.
- `GOMEMLIMIT` - soft cap on total Go memory. Beyond this, GC runs more
  aggressively. Set it just below your container limit so the OOM killer
  is never the first signal that the heap grew.

```sh
GOGC=150 GOMEMLIMIT=1GiB ./myapp
```

For batch jobs that allocate heavily and then exit, `GOGC=off` and a
generous memory limit can shave noticeable seconds off total runtime.
Don't do this for long-running services.

### Bytecode Cache

`geblang` caches compiled bytecode in `~/.cache/geblang/bytecode/` (or
`$GEBLANG_CACHE_DIR`). First runs of a script parse + compile + execute;
subsequent runs skip parse/compile when the source hash matches. Pre-warm
the cache as part of your container build to make cold starts
deterministic:

```dockerfile
RUN /myapp warmup    # any startup that touches every imported module
```

Clean stale entries with `geblang cache clean` if you suspect the cache is
serving stale code (the source-hash check should make this unnecessary).

## Observability

### Structured Logging

Use the `log` stdlib module rather than `io.println` for anything that
should reach a log aggregator. It emits JSON lines with `level`, `time`,
`message`, and any `fields` you attach:

```gb
import log;

log.info("user.login", {"user_id": id, "ip": req.remoteAddr});
```

Pipe stdout straight into your platform's log collector
(stdout/stderr to Cloud Logging / Loki / Datadog / Elastic).

### Tracing And Metrics

The `metrics` module exposes counters, gauges, and histograms over a
Prometheus-compatible scrape endpoint:

```gb
import metrics;
metrics.inc("http.requests");
metrics.observe("http.latency_ms", 12.4);
```

For distributed tracing, the `tracer` module emits OpenTelemetry spans:

```gb
import tracer;
let span = tracer.start("loadUser");
try {
    return loadUser(id);
} finally {
    tracer.finish(span);
}
```

Both modules are pull-based: your service exposes the data and your
observability stack scrapes it. There is no opinionated reporter to fight
with.

### Profiling

Geblang ships with a built-in `profiler` module for capturing CPU, heap,
and goroutine snapshots from inside the running program:

```gb
import profiler;

let snap = profiler.snapshot();
doWork();
let delta = profiler.delta(snap);
io.println("elapsed_ms = " + (delta["elapsed_ms"] as string));
io.println("heap_alloc = " + (delta["heap_alloc"] as string));
```

For continuous profiling, expose a Go `net/http/pprof` endpoint via the
`pprof` stdlib module (when present) or build a small admin handler that
calls `profiler.snapshot()` and `profiler.memory()`. The output is
compatible with the standard `go tool pprof` workflow.

## Common Bottlenecks

When a program is slower than expected, the culprit is almost always one
of:

1. **String concatenation in a hot loop.** Geblang strings are immutable,
   so repeated `result = result + chunk` allocates O(n²) bytes. Use a
   list and `"".join(parts)` at the end, or `io.memory()` to write into
   a buffer.
2. **List indexing where a generator would do.** If you only consume the
   first N items of a large derived sequence, prefer a `generator<T>`
   function over building the full list.
3. **Per-request HTTP clients.** `http.get(...)` and friends use a
   shared default client, but if you wrap each call in a fresh
   `http.newClient(...)`, you also create a fresh connection pool. Build
   the client once at startup and reuse it.
4. **Synchronous I/O in async handlers.** A handler that calls
   `io.readText` while serving a request blocks the goroutine. Use
   `async.io.readText` (or `async.io.readBytes`) and await the result so
   other requests progress.
5. **Heavy work inside a class constructor.** Decorators and field
   defaults run for every instance. Cache shared state on the class
   (static fields) or behind a lazy `option.lazy(...)`.

Benchmark micro-suspects with the `benchmarks/` harness or the stdlib
`time.elapsed` helper:

```gb
import time;

let t = time.now();
doWork();
io.println("took " + (time.elapsed(t) as string) + "ms");
```

A 3-5x repeat is usually enough to filter noise; if you need
statistically robust numbers, lean on `benchmarks/run.py` which already
does a 7-run median across geblang/python/php for comparison.

## Production Checklist

- [ ] Binary built with `geblang build --out` (no source on the host).
- [ ] `GOMAXPROCS` matches the container's allocated CPU.
- [ ] `GOMEMLIMIT` is set just below the container memory limit.
- [ ] Bytecode cache is warmed during the image build.
- [ ] Structured logs (`log.info / warn / error`) flow to stdout.
- [ ] `metrics` endpoint is wired up to your scrape target.
- [ ] Health-check route returns 200 only when downstream deps are
      reachable.
- [ ] Graceful shutdown: register a SIGTERM handler that calls
      `http.shutdown(server, 5000)` so in-flight requests finish.
- [ ] Crash logs include the Geblang version (`geblang --version`) and
      the bytecode chunk version printed in panics.

For platform-specific deployment recipes (systemd, Docker Compose,
Kubernetes), see the matching guides under `docs/user/`.
