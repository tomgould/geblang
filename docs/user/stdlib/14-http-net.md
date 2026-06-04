# HTTP, Networking, And WebSockets

## HTTP

Import `http` for client calls and simple servers.

- server: `serve`, `listen`, `close`, `shutdown`, `serverAddr`, `serverCert`
- client: `get`, `post`, `postJson`, `request`, `requestWithOptions`
- streaming: `stream`, `streamWrite`, `streamFlush`, `streamClose`
- helpers: `parseJson`, `Headers`, `Cookie`, `response`, `jsonResponse`
- classes exported by import: `Request`, `Response`

```gb
import http;

let response = http.get("https://example.com");
io.println(response["status"]);
```

### The Response object

Client calls (`get`, `post`, `postJson`, `request`, the request builder's
`send`, the client methods, and `fetchAll`) return a `Response` object with
reader methods:

```gb
let r = http.get("https://api.example.com/users/7");

r.status();         # 200 (int)
r.ok();             # true for any 2xx
r.text();           # body as a string
r.bytes();          # body as raw bytes
r.json();           # body parsed as JSON
r.header("ETag");   # first value of a header, or null
r.headers();        # all response headers

r.isSuccessful();   # 2xx
r.isRedirect();     # 3xx
r.isClientError();  # 4xx
r.isServerError();  # 5xx
r.isNotFound();     # 404
```

For a single call like `http.get`, a request that never reaches the server
(DNS failure, connection refused, timeout) is raised as an `IOError` (catch
it with `try { ... } catch (IOError e) { ... }`). In a parallel batch
(`getAll` / `fetchAll`) the same failure is reported on the Response instead,
so one bad request does not abort the batch:

```gb
r.isError();   # true only for a transport-level failure
r.error();     # the failure message, or null when isError() is false
```

The `Response` is also index-compatible with the plain dict shape used in
earlier versions, so existing code keeps working:

```gb
r["status"];        # same as r.status()
r["body"];          # same as r.text()
r["headers"];       # same as r.headers()
let plain = r.toDict();   # a plain dict<string, any> snapshot
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

### The request builder

`http.request(url)` with a single argument starts an immutable, fluent
request builder. Each `withX` method returns a new builder, so a base
builder can be safely reused for several requests:

```gb
let r = http.request("https://api.example.com/users")
    .withMethod("POST")
    .withQuery("page", "2")
    .withHeader("X-Trace", "abc")
    .withHeaders({"X-A": "1", "X-B": "2"})
    .withJson({"name": "ada"})       # sets the body and Content-Type
    .withBearer(token)               # Authorization: Bearer <token>
    .withTimeout(2000)               # per-request timeout in ms
    .send();                         # returns a Response

io.println(r.status());
```

Available builder methods: `withMethod`, `withHeader`, `withHeaders`,
`withQuery`, `withBody`, `withJson`, `withBearer`, `withBasicAuth`,
`withTimeout`, and `send`. Because each step is immutable, sibling
requests never leak each other's headers:

```gb
let base = http.request(url).withMethod("POST").withJson(payload);
let withAuth = base.withBearer(token);   # base is unchanged
```

The older mutating builder from `http.build(url)` (`.method`, `.header`,
`.body`, `.timeout`) still works.

### Parallel requests

`http.getAll(urls)` issues parallel GETs and returns a list of Responses
in the same order as the input. `http.fetchAll(requests)` is the general
form: each element may be a request builder or a request-spec dict. Both
accept an options dict whose `limit` caps how many run at once (0 or
omitted means unbounded), and both are awaitable.

When every request is a plain GET, pass the URLs directly:

```gb
import async;

let pages = await http.getAll([
    "https://example.com/page/1",
    "https://example.com/page/2",
    "https://example.com/page/3"
], {"limit": 4});
```

When the requests differ, build each one with the request builder, bind it
to a name, and hand the list to `fetchAll`. Because each builder is an
immutable value, this reads as a set of described requests sent together:

```gb
let createUser = http.request(usersUrl)
    .withMethod("POST")
    .withBearer(token)
    .withJson({"name": "ada"});

let listTeams = http.request(teamsUrl).withBearer(token);

let responses = await http.fetchAll([createUser, listTeams]);

for (response in responses) {
    if (response.isError()) {
        /* The request never reached the server (see below). */
        io.println("request failed: " + response.error());
    } else if (response.ok()) {
        io.println(response.json());
    } else {
        io.println("server returned status " + (response.status() as string));
    }
}
```

**Reading the results.** Every entry is a `Response`, in the same order as
the input, so the list is uniform and you never have to type-check it. There
are three cases to tell apart:

- A request that succeeded: `response.ok()` is true (a 2xx status).
- A request that reached the server but came back with an HTTP error status:
  `response.ok()` is false, but `response.isError()` is *also* false. A
  `404` or `500` is a normal `Response`; check `response.status()` or the
  `isClientError()` / `isServerError()` predicates.
- A request that never completed a round trip at all (DNS lookup failed,
  connection refused, the timeout elapsed): `response.isError()` is true and
  `response.error()` holds the failure message. Its `status()` is `0`.

The batch never throws because one request failed, so a single bad entry
does not lose the others; check `response.isError()` first, as above.

A configured client exposes the same `fetchAll` for connection-pool reuse.

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
| `tls`          | dict                          | none    | TLS settings (see TLS below): `verify`, `caCerts`, `caCertsOnly`, `clientCert`, `clientKey` |

Per-call options accepted by `Client.request(opts)` cover retries and
timeouts in addition to the standard `method`, `url`, `body`, and `headers`:

| Key                  | Type      | Default                | Meaning |
|----------------------|-----------|------------------------|---------|
| `timeoutMs`          | int       | client default         | Overrides the per-request timeout for this call |
| `retries`            | int       | `1` (no retry)         | Total attempts including the first |
| `retryBackoffMs`     | int       | `100`                  | Base backoff before retry 2; doubled each retry, with full jitter |
| `retryBackoffMaxMs`  | int       | `5000`                 | Upper bound on a single sleep |
| `retryStatuses`      | list<int> | `[502, 503, 504, 429]` | Status codes that trigger a retry |

### TLS (HTTPS)

Clients verify TLS certificates against the system trust store by default. The
`tls` option customises this:

| Key           | Type           | Default | Meaning |
|---------------|----------------|---------|---------|
| `verify`      | bool           | `true`  | Set `false` to skip certificate verification (testing / self-signed only) |
| `caCerts`     | string / bytes | none    | PEM certificate(s) to trust in addition to the system roots |
| `caCertsOnly` | bool           | `false` | When `true`, trust *only* `caCerts` and ignore the system roots |
| `clientCert`  | string / bytes | none    | PEM client certificate for mutual TLS |
| `clientKey`   | string / bytes | none    | PEM private key for `clientCert` (the two must be supplied together) |

`http.serve` / `http.listen` serve HTTPS when their opts dict carries a `tls`
block: pass `{cert, key}` (PEM) for a real certificate, or `{selfSigned: true}`
to generate an in-memory certificate for local development. A self-signed cert
covers `localhost`, `127.0.0.1`, `::1`, and the bind host; pass
`selfSigned: ["host", ...]` to set the SANs explicitly. `http.serverCert(server)`
returns the served certificate as PEM so a client can trust it precisely
instead of disabling verification.

```gb
import http;
import io;

let server = http.listen("127.0.0.1:0", func(dict<string, any> req): dict<string, any> {
    return {"status": 200, "body": "secure"};
}, {"tls": {"selfSigned": true}});

let url = "https://" + http.serverAddr(server) + "/";

# trust the server's self-signed cert precisely (no verify:false needed)
let client = http.newClient({"tls": {"caCerts": http.serverCert(server)}});
io.println(client.get(url)["body"]);   # secure

http.close(server);
```

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

### IP and CIDR utilities (1.6.0)

`net` also exposes pure helpers for IP addresses and CIDR ranges,
useful for allow-lists, deny-lists, network classification, and
binary protocols. Backed by Go's `net/netip`.

| Function | Returns | Description |
|----------|---------|-------------|
| `net.parseIp(s)` | `dict<string, any>` | `{version: 4\|6, address: canonical, bytes: 4 or 16}` for any valid IPv4 / IPv6 address. Throws on malformed input. |
| `net.parseCidr(s)` | `dict<string, any>` | `{network, prefixLen, version, first, last, count}`. The network field is the masked base; count is the total host count and lifts to bigint for IPv6 ranges. Throws on malformed input. |
| `net.cidrContains(cidr, ip)` | `bool` | True when `ip` falls within `cidr`. |
| `net.cidrRange(cidr)` | `dict<string, any>` | `{first, last, count}` for the CIDR's inclusive range. |
| `net.isIpv4(s)` | `bool` | True when `s` parses as IPv4. Never throws. |
| `net.isIpv6(s)` | `bool` | True when `s` parses as a native IPv6 address. Never throws; returns false for IPv4-mapped addresses. |
| `net.ipToBytes(s)` | `bytes` | Raw 4-byte (IPv4) or 16-byte (IPv6) representation. Throws on malformed input. |
| `net.ipFromBytes(b)` | `string` | Decodes 4 or 16 bytes back to a canonical IP string. Throws on any other length. |

```gb
import net;

let allow = ["10.0.0.0/8", "192.168.0.0/16", "127.0.0.1/32"];

func isInternal(string ip): bool {
    for (var cidr in allow) {
        if (net.cidrContains(cidr, ip)) { return true; }
    }
    return false;
}

io.println(isInternal("10.5.5.5"));     # true
io.println(isInternal("8.8.8.8"));      # false

let c = net.parseCidr("2001:db8::/32");
io.println(c["count"]);   # 79228162514264337593543950336
```

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
