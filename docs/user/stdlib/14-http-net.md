# HTTP, Networking, And WebSockets

## HTTP

Import `http` for client calls and simple servers.

- server: `serve`, `listen`, `close`, `shutdown`, `serverAddr`
- client: `get`, `post`, `postJson`, `request`, `requestWithOptions`
- streaming: `stream`, `streamWrite`, `streamFlush`, `streamClose`
- helpers: `parseJson`, `Headers`, `Cookie`, `response`, `jsonResponse`
- classes exported by import: `Request`, `Response`

```gb
import http;

let response = http.get("https://example.com");
io.println(response["status"]);
```

Recoverable HTTP transport and server I/O failures are surfaced as Geblang
exceptions. Server bind failures, including port conflicts, are `IOError`
values:

```gb
try {
    http.serve("127.0.0.1:8080", handler);
} catch (IOError e) {
    io.println("server could not start: " + e.message);
}
```

HTTP response statuses are not exceptions. A `404`, `422` or `500` response is
returned as a normal response value; inspect `response["status"]` and decide how
your application should handle it.

### Client configuration

`http.newClient(opts)` creates a configurable, reusable client. Most options
shape the underlying connection pool and cookie behaviour:

| Key            | Type                          | Default | Meaning |
|----------------|-------------------------------|---------|---------|
| `timeoutMs`    | int                           | none    | Per-request deadline (entire request lifecycle) |
| `baseUrl`      | string                        | none    | Prefix for relative URLs passed to `client.get(...)` etc. |
| `headers`      | dict<string, string> / Headers | empty  | Default headers added to every request |
| `cookieJar`    | CookieJar or `true`           | none    | Persist cookies across requests. Pass an explicit `http.newCookieJar()` to inspect/share, or `true` to attach a fresh jar |
| `keepAlive`    | bool                          | `true`  | Reuse the underlying TCP/TLS connection. Set `false` to force a new connection per request (debugging only) |
| `maxIdleConns` | int                           | Go default | Idle-connection pool size (per host and total) |
| `proxy`        | string                        | none    | Forward all requests through this HTTP/HTTPS proxy URL |
| `proxyFromEnv` | bool                          | `false` | Honour `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` environment variables |

Per-call options accepted by `Client.request(opts)` cover retries and
timeouts in addition to the standard `method`, `url`, `body`, and `headers`:

| Key                  | Type      | Default                | Meaning |
|----------------------|-----------|------------------------|---------|
| `timeoutMs`          | int       | client default         | Overrides the per-request timeout for this call |
| `retries`            | int       | `1` (no retry)         | Total attempts including the first |
| `retryBackoffMs`     | int       | `100`                  | Base backoff before retry 2; doubled each retry, with full jitter |
| `retryBackoffMaxMs`  | int       | `5000`                 | Upper bound on a single sleep |
| `retryStatuses`      | list<int> | `[502, 503, 504, 429]` | Status codes that trigger a retry |

### Cookie jars

A cookie jar persists `Set-Cookie` responses and replays them on subsequent
requests to matching hosts. This is the right shape for any client that needs
to maintain a session (login, CSRF tokens, server-stored basket, etc.).

```gb
let jar     = http.newCookieJar();
let session = http.newClient({"cookieJar": jar, "baseUrl": "https://api.example.com"});

session.post("/login", "{\"user\":\"ada\",\"password\":\"...\"}",
             {"Content-Type": "application/json"});

# the session cookie is automatically sent on subsequent requests
let me = session.get("/me");

# inspect or persist the cookies between runs
for (c in jar.cookies("https://api.example.com")) {
    io.println(c["name"] + "=" + c["value"]);
}
```

`CookieJar.cookies(url)` returns a `list<dict>` with `name`, `value`,
`domain`, `path`, `secure`, and `httpOnly`. `CookieJar.setCookies(url, list)`
populates the jar from a saved list (useful for reusing a session across
process restarts). `CookieJar.clear()` empties the jar.

### Proxies and connection control

Most production workloads benefit from a small idle pool and a proxy:

```gb
let client = http.newClient({
    "baseUrl":      "https://internal.example.com",
    "proxy":        "http://proxy.example.com:3128",
    "keepAlive":    true,
    "maxIdleConns": 32,
    "timeoutMs":    10000
});
```

Set `"proxyFromEnv": true` to defer to `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`
environment variables instead of hard-coding a URL.

### User-Agent

Every outgoing HTTP request defaults to `User-Agent: Geblang/1.0`. Override
by setting your own value either in the per-call `headers` argument or as a
`headers` default on the client:

```gb
let c = http.newClient({"headers": {"User-Agent": "MyApp/2.0"}});
```

Network errors are always retried up to `retries`. When all attempts are
exhausted, the last response (or the last network error) is returned to
the caller; no exception is thrown.

```gb
let client = http.newClient({"timeoutMs": 5000});
let resp = client.request({
    "method": "GET",
    "url": "https://example.com/flaky",
    "retries": 5,
    "retryBackoffMs": 200,
    "retryStatuses": [502, 503, 504, 429, 500]
});
```

`http.stream(handler)` creates a low-level streaming response. The source-level
`web.sse.streaming(handler)` wrapper gives the handler an `sse.EventStream`
that can write and flush chunks while the request remains open:

```gb
import http;
import sys;
import web.sse as sse;

func events(dict<string, any> request): dict<string, any> {
    return sse.streaming(func(sse.EventStream stream): void {
        for (n in 1..3) {
            stream.write(sse.event("tick", "count " + (n as string), {}));
            stream.flush();
            sys.sleep(1000);
        }
        stream.close();
    });
}
```

### Concurrency limits

`http.listen` and `http.serve` accept an optional opts dict that caps
simultaneous in-flight handlers and decides what happens when the cap is
reached. Defaults preserve the unbounded behaviour - the opts dict is
opt-in.

```gb
let id = http.listen(":8080", handler, {
    "maxConcurrent": 1000,    # at most 1000 active handlers; 0 = unbounded
    "queueSize":     500,     # requests waiting for a slot
    "onOverload":    "reject" # "reject" | "wait" | "drop"
});
```

`onOverload` controls the action when slots and queue are both full:

- `"reject"` (default once a cap is set) responds with HTTP 503 and the
  body `server at capacity`.
- `"wait"` keeps the request blocked until a slot opens. There is no
  rejection at all; the queue size becomes informational.
- `"drop"` closes the connection silently without writing a body.

WebSocket connections inherit the same cap because the upgrade happens
inside an HTTP handler. A WebSocket holds its slot for the entire
connection lifetime, which gives you a hard cap on simultaneous
WebSocket clients.

Read the pool's running counters with `http.serverStats(server)`:

```gb
let stats = http.serverStats(id);
# {"active": 42, "queued": 7, "rejected": 0, "maxConcurrent": 1000}
```

#### Which concurrency model do I want?

There are three points on the spectrum; this section picks one for you
based on what your workload actually looks like.

- **Default (no opts).** Go's runtime spawns a goroutine per request
  and multiplexes them onto OS threads via its M:N scheduler. The
  scheduler uses epoll on Linux / kqueue on macOS / IOCP on Windows
  under the hood, so blocking-looking handlers do not actually pin a
  thread. This handles thousands of concurrent connections without
  any opt-in. Use this when your handlers are fast (sub-100 ms),
  total active connections are below ~10k, and load is bursty rather
  than sustained. Most apps stop here.

- **Bounded concurrency (`maxConcurrent` opt).** Adds a semaphore in
  front of the handler. Same Go scheduler underneath; the cap is on
  in-flight work, not on the runtime's I/O multiplexer. Use this
  when handlers are slow or hold expensive resources (DB connections,
  large memory buffers, long-lived WebSocket sessions) and an
  unbounded goroutine pile can run you out of memory or starve
  downstream services. The cap gives you predictable peak memory and
  a clean backpressure signal (503) instead of an OOM.

- **Full epoll/kqueue reactor.** Not built into Geblang. A real
  reactor (gnet-style) would bypass `net/http` entirely, manage file
  descriptors directly via the kernel poll APIs, and run each request
  on a fixed-size worker pool. It only meaningfully helps past ~100k
  persistent connections per box, where Go's per-goroutine stack
  (~8 KB minimum) and scheduler overhead start to matter. Below that
  scale the bounded-concurrency option above is the same wall-clock
  performance with a fraction of the implementation surface. If you
  hit a workload that needs more, the right move is dropping below
  Geblang into a Go program that uses `gnet` or `evio` directly.

Rule of thumb: start without opts, switch to `maxConcurrent` the first
time a slow handler or a memory ceiling matters, and don't reach for a
custom reactor until you have measurements proving the Go scheduler is
the bottleneck (it usually isn't).

## Net

Import `net` for DNS, TCP, and UDP:

- address helpers: `joinHostPort`, `splitHostPort`, `lookupHost`
- TCP: `listenTcp`, `connectTcp`, `accept`, `read`, `write`, `close`,
  `localAddr`, `remoteAddr`, `setDeadline`, `clearDeadline`
- UDP: `listenUdp`, `dialUdp`, `readFrom`, `writeTo`

For task-returning socket calls, import `async.net`.

`net.serve` accepts the same `maxConcurrent` / `queueSize` /
`onOverload` opts as `http.listen`. On overload, the listener closes
the accepted socket immediately (the same effect for `reject` and
`drop`). Poll counters with `net.serverStats(handle)`.

Socket operations use the same error model as HTTP: connection failures,
deadline failures, and bind conflicts throw `IOError` so applications can
recover or report a clean message.

## Static File Server

`http.server` is an executable source module. Run it with `-m` to serve the
current directory for local development:

```sh
geblang -m http.server 8080
```

The optional port defaults to `8000`. The server binds to `127.0.0.1`, serves
`index.html` for directories when present, and otherwise renders a simple
directory listing.

## WebSocket

Import `websocket` for low-level WebSocket client and server operations, or
`web.websocket` for the higher-level OO interface.

### Server: `web.websocket`

`ws.upgrade(handler)` returns a special HTTP response dict that triggers the
WebSocket handshake when returned from a route handler. Inside the handler,
`conn` is a `Connection` value with the full OO interface:

```gb
import web.router as router;
import web.websocket as ws;

let app = router.newRouter();

router.get(app, "/socket", func(dict<string, any> request): dict<string, any> {
    return ws.upgrade(func(ws.Connection conn): void {
        let msg = conn.readText();
        conn.sendText("echo: " + msg);
        conn.close();
    });
});
```

`Connection` methods: `sendText`, `readText`, `sendJson`, `readJson`,
`sendBytes`, `readBytes`, `close`.

### Client: `web.websocket`

```gb
import web.websocket as ws;

let conn = ws.connect("ws://127.0.0.1:8080/socket");
conn.sendJson({"message": "hello"});
let reply = conn.readJson();
conn.close();
```

`ws.connectWithHeaders(url, headers)` is available when custom headers are
needed (e.g. for authentication tokens).

### Low-level: `websocket`

For scripts that don't use the router, import the native `websocket` module
directly. `upgrade(handler)` works the same way - it returns a response dict
- but the handler receives a raw opaque handle rather than a `Connection`:

```gb
import http;
import websocket;

http.serve("127.0.0.1:8080", func(dict<string, any> request): dict<string, any> {
    return websocket.upgrade(func(any conn): void {
        websocket.sendText(conn, "hello " + websocket.readText(conn));
        websocket.close(conn);
    });
});
```

## URL Parsing And Manipulation

Import `url` to parse, construct, and transform URLs:

```gb
import url;

let u = url.URL("https://example.com:8080/search?q=hello#results");
io.println(u.scheme());    # https
io.println(u.host());      # example.com
io.println(u.port());      # 8080
io.println(u.path());      # /search
io.println(u.fragment());  # results

let q = u.query();
io.println(q["q"]);        # hello
```

### `url.URL` methods

| Method | Description |
|--------|-------------|
| `scheme()` | scheme string |
| `host()` | host without port |
| `port()` | port string (empty if default) |
| `path()` | path component |
| `query()` | `dict<string, string>` of query params |
| `fragment()` | fragment after `#` |
| `toString()` | full URL string |
| `toDict()` | dict with `scheme`, `host`, `port`, `path`, `query`, `fragment` keys |
| `withScheme(s)` | new URL with scheme replaced |
| `withHost(h)` | new URL with host replaced |
| `withPath(p)` | new URL with path replaced |
| `withQuery(q)` | new URL with query replaced (dict or raw string) |
| `withFragment(f)` | new URL with fragment replaced |
| `resolve(ref)` | resolve a reference URL against this base |
| `normalize()` | clean path and re-encode query string |

All `with*` methods return new `url.URL` values rather than mutating in place.

### Module-level functions

```gb
# parse returns a plain dict
let parts = url.parse("https://example.com/path?k=v");

# stringify reassembles a dict back into a URL string
let text = url.stringify({"scheme": "https", "host": "example.com", "path": "/users"});

# encode / decode query-string components
let encoded = url.encode("hello world");   # hello+world
let decoded = url.decode("hello+world");   # hello world

# joinPath appends path segments to a base URL string
let api = url.joinPath("https://api.example.com", "v1", "users");
# https://api.example.com/v1/users
```

Use `url.URL` for chaining or repeated access to URL parts. Use `url.parse`
when you need a plain dict, for example to pass directly to JSON.
