# Sockets

The `sockets` module (1.2.0) is the high-level TCP / TLS interface,
sitting on top of the lower-level `net.*` primitives. Sockets are
`streams.IOStream`-shaped, so the stream methods and dunder
protocol apply directly. For raw byte-oriented operations or UDP,
keep using the `net` module.

## Client side

```gb
import io;
import sockets;

let client = sockets.dial("example.com", 80);
client.writeln("GET / HTTP/1.0");
client.writeln("Host: example.com");
client.writeln("");
io.println(client.readAll());
client.close();
```

`sockets.dial(host, port, opts = {})`:

| `opts` key | Type | Description |
|------------|------|-------------|
| `tls` | `bool` | Wrap the connection with TLS (uses the system trust store). |
| `timeoutMs` | `int` | Bound the connect step. |

Returns a `Socket` with:

- `read(n)` / `readAll()` / `readLine()` / `lines()` - stream reads.
- `write(buf)` / `writeln(buf)` - stream writes.
- `close()` - shut the connection (idempotent).
- `isClosed()` - state check.
- `localAddr()` / `remoteAddr()` - peer addresses.
- Stream-protocol dunders `__read` / `__write` / `__close` /
  `__iter` so `streams.copy(sock, dst)` and `for (line in sock)`
  work directly.

## Server side

```gb
import sockets;
import streams;

let server = sockets.serve("0.0.0.0", 8080, func(dict<string, any> raw): void {
    let stream = streams.IOStream(raw["stream"]);
    stream.writeln("hello from sockets");
    stream.close();
});

# ...

server.close();
```

`sockets.serve(host, port, handler)` binds a listener and dispatches
each accepted connection to `handler(socket)`. The handler runs in a
child evaluator with the original closure (mutations to module-level
globals propagate; local captures don't, same caveat as `watch.start`).

The handler argument is a dict `{handle, stream, localAddr,
remoteAddr}`. Wrap `raw["stream"]` in `streams.IOStream` for the
fluent surface.

`server.close()` stops accepting and joins the goroutine so the next
read of module-level state from the parent happens-after the last
handler invocation. Use this as the synchronisation barrier instead
of `sys.sleep`.

## TLS

```gb
let conn = sockets.dial("api.example.com", 443, {"tls": true});
conn.writeln("GET / HTTP/1.0");
conn.writeln("Host: api.example.com");
conn.writeln("");
io.println(conn.readAll());
conn.close();
```

The TLS dial uses Go's standard library trust store. Pass `host`
matching the certificate (SNI is set from the host argument).

## When to use sockets vs net

| Use case | Module |
|----------|--------|
| Talk to a TCP/TLS server line-by-line | `sockets.dial` |
| Build a TCP server with a callback handler | `sockets.serve` |
| Raw bytes, custom framing, UDP, deadlines, DNS helpers | `net.*` |
| Streaming a socket alongside files / processes | `sockets` (uses the stream protocol) |
