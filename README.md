# Geblang

Geblang is a statically-typed scripting language implemented in Go.
Current version: **1.6.0**. It combines the ergonomics of PHP and
Python with strong static typing, generics, decorators, async, and
runtime reflection.

```gb
import io;

class Greeter {
    string name;

    func Greeter(string name) {
        this.name = name;
    }

    func greet(): string {
        return "Hello, " + this.name;
    }
}

Greeter g = Greeter("world");
io.println(g.greet());
```

Types are checked statically. `any` is opt-in for dynamic boundaries
(parsed JSON, untyped foreign data); most code is typed end-to-end.

## Highlights

- **Static typing** with generics, unions (`T | U`), intersections
  (`T & U`), nullable types (`?T`), and explicit type assertions.
  `string`, `int`, `decimal`, `bool`, `bytes`, `list<T>`,
  `dict<K, V>`, `set<T>` are first-class.
- **Classes** with single inheritance and interfaces, decorators
  (callable + metadata), pattern matching, enums (with payloads
  and destructuring), and runtime reflection.
- **Concurrency** built on a cooperative async model: `async`
  functions, `await`, generators, structured task groups
  (`async.scope.scope`), and `async.race` / `async.all` /
  `async.timeout` combinators. Optional reactor backend for
  high-throughput TCP / HTTP via `{reactor: true}`.
- **FFI** for calling C-ABI shared libraries directly (libtorch,
  libsqlite, libcurl, ...). In-process dispatch with no IPC;
  capability-gated default-off.
- **Bytecode VM** with a tree-walking evaluator as the reference
  semantics and fallback. Compiled bytecode is cached on disk by
  source hash so subsequent runs skip parse and compile.
- **Batteries-included stdlib**: HTTP server / client, WebSocket,
  TCP / UDP, SQLite / PostgreSQL / MySQL, Redis, SMTP / IMAP,
  RabbitMQ / Kafka / SQS / STOMP, file + streaming I/O, archives,
  process management, schedulers, OTLP traces, Prometheus metrics,
  JSON / YAML / TOML / XML / Markdown, regex, crypto (HMAC,
  Argon2id, bcrypt, JWT, AES-GCM, XChaCha20-Poly1305, RSA / EC /
  Ed25519), pathlib, math, and a Twig-style template engine.
- **Tooling**: `geblang` CLI (run / repl / check / test / fmt /
  init / install / build / doctor / cache), VS Code extension with
  LSP + DAP + test explorer, Dockerised reproducible build,
  single-binary bundling via `geblang build --out`.

## Documentation

The full language and stdlib reference is in
[docs/user/](docs/user/) - start with
[01-getting-started.md](docs/user/01-getting-started.md). The
release notes are at
[docs/user/17-release-notes.md](docs/user/17-release-notes.md).
The same reference is rendered as a browsable HTML site at
[geblang.davegebler.com](https://geblang.davegebler.com/index.html).

## Install

```sh
make build              # produces ./geblang
make docs               # builds the static reference site
make docker-build       # produces ./build/{geblang,stdlib} via Docker
```

After `make docker-build`, the binary at `./build/geblang` runs
standalone; if you relocate it away from the bundled `stdlib/`,
set `GEBLANG_STDLIB` to point at the moved directory.

## Quickstart

### Hello world

```gb
# Geblang line comments use `#`. Block comments use `/* ... */`.
import io;

string name = "world";
io.println("Hello, " + name);
```

```sh
geblang hello.gb
```

Double-quoted strings evaluate escape sequences. Single-quoted
strings keep backslashes literal. `//` is integer division, not a
comment.

### Enums, pattern matching, typed methods

```gb
import io;

enum Shape {
    Circle(decimal),
    Rect(decimal, decimal),
    Triangle(decimal, decimal),
}

func area(Shape s): decimal {
    return match (s) {
        case Shape.Circle(decimal r)            => 3.14159 * r * r;
        case Shape.Rect(decimal w, decimal h)   => w * h;
        case Shape.Triangle(decimal b, decimal h) => 0.5 * b * h;
    };
}

list<Shape> shapes = [
    Shape.Circle(5.0),
    Shape.Rect(3.0, 4.0),
    Shape.Triangle(6.0, 2.5),
];

for (Shape s in shapes) {
    io.println(area(s));
}
```

Tagged enums destructure inside `match`, so the payload binds with
its declared type. Missing variants fail static analysis at
`geblang check` time.

### Generics + collections

```gb
import io;

class Repository<T> {
    list<T> items;

    func Repository() {
        this.items = [];
    }

    func add(T item): Repository<T> {
        this.items = this.items.push(item);
        return this;
    }

    func count(): int {
        return this.items.length();
    }
}

class User {
    string name;
    int age;
    func User(string name, int age) {
        this.name = name;
        this.age = age;
    }
}

Repository<User> users = Repository<User>();
users.add(User("Ada", 36)).add(User("Carla", 41));

io.println("count: " + (users.count() as string));
for (User u in users.items) {
    io.println(u.name + " (" + (u.age as string) + ")");
}
```

`list<T>.push` returns a new list, so type-safe chaining works
naturally. `dict<K, V>` and `set<T>` are generic in the same way.
Pass a `callable` parameter for predicate / mapper functions.

### File I/O with typed streams

```gb
import io;
import streams;

streams.IOStream src = streams.open("/etc/hostname", "r");
string hostname = src.readLine() as string;
src.close();

streams.IOStream out = streams.open("/tmp/report.txt", "w");
out.writeln("hostname: " + hostname);
out.writeln("processed at: " + io.timestamp());
out.close();
```

`streams.IOStream` is the typed wrapper around files, stdin /
stdout / stderr, in-memory buffers, sockets, and pipes; every
read returns `?string` or `?bytes` so the type system forces an
EOF check at the call site.

### Async + structured concurrency

```gb
import async;
import async.scope as scope;
import io;
import time;

async func fetchPrice(string symbol, int ms): decimal {
    await time.sleep(ms);
    return symbol.length().toDecimal() * 100.0;
}

let prices = scope.scope(func(scope.TaskGroup g): list<decimal> {
    let a = g.spawn(func(): decimal {
        return async.await(fetchPrice("AAPL", 50)) as decimal;
    });
    let b = g.spawn(func(): decimal {
        return async.await(fetchPrice("GOOG", 30)) as decimal;
    });
    return [
        async.await(a) as decimal,
        async.await(b) as decimal,
    ];
}) as list<decimal>;

for (decimal p in prices) {
    io.println(p.toString(2));
}
```

Children spawned inside `scope.scope` are awaited at scope exit.
A failing child cancels its siblings and rethrows after the
group drains - no orphan goroutines. `async.race`, `async.all`,
and `async.timeout` provide single-shot variants for simpler
cases.

### TCP echo server

```gb
import io;
import sockets;
import streams;
import sys;

sockets.Server server = sockets.serve("127.0.0.1", 9000,
    func(dict<string, any> raw): void {
        streams.IOStream stream = streams.IOStream(raw["stream"]);
        while (true) {
            ?string line = stream.readLine();
            if (line == null) { break; }
            stream.writeln("echo: " + (line as string));
        }
        stream.close();
    });

io.println("listening on " + server.localAddr());
sys.sleep(60000);   /* serve for one minute */
server.close();
```

`sockets.Server` and the per-connection `streams.IOStream` are
typed; the inbound `raw["stream"]` boundary is the only `any`
crossing in the example.

### FFI: calling C-ABI shared libraries

```gb
import ffi;
import io;

let lib = ffi.dlopen("libm.so.6");
let sin = lib.symbol("sin", [ffi.DOUBLE], ffi.DOUBLE);
let hypot = lib.symbol("hypot", [ffi.DOUBLE, ffi.DOUBLE], ffi.DOUBLE);

io.println(sin(1.5707963267948966));   /* 1.0 */
io.println(hypot(3.0, 4.0));           /* 5.0 */
lib.close();
```

Run with `geblang --allow-ffi 'libm.so.*' script.gb`, or declare
the allow-list in `geblang.yaml` under `permissions.ffi`. The
dispatch is in-process - no IPC overhead - and covers primitive
integers, floats, pointers, C strings, and bytes. Use it for
numeric kernels and library bindings; for sandboxed extensions,
the subprocess `ext` protocol is the better fit.

### HTTP server (web boundary)

The web stack is the one common place `dict<string, any>` shows up
in user code, since HTTP requests and responses are inherently
dynamic over the wire:

```gb
import http;
import json;
import web.router as router;

class User {
    int id;
    string name;
    func User(int id, string name) {
        this.id = id;
        this.name = name;
    }
}

dict<int, User> store = {1: User(1, "Ada"), 2: User(2, "Carla")};

let app = router.newRouter();

router.get(app, "/users/:id", func(dict<string, any> req): dict<string, any> {
    dict<string, any> params = req["params"] as dict<string, any>;
    int id = (params["id"] as string).toInt();
    if (!store.contains(id)) {
        return {"status": 404, "body": "not found"};
    }
    User u = store[id];
    return {
        "status": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.stringify({"id": u.id, "name": u.name}),
    };
});

http.serve("127.0.0.1:8080", func(dict<string, any> req): dict<string, any> {
    return router.handle(app, req);
});
```

The handler keeps `dict<string, any>` to the framework boundary
and uses typed locals (`User u`, `int id`) inside. For larger
typed-binding apps with controllers, validation, and DI, see the
WebSocket and `web.middleware` chapters in the manual.

## CLI

```sh
geblang script.gb                    # run a script (VM by default)
geblang --disable-vm script.gb       # run via evaluator
geblang repl                         # interactive REPL
geblang check script.gb              # static analysis (no execution)
geblang check --strict path/         # treat warnings as errors
geblang test tests/                  # run *_test.gb files
geblang fmt path/                    # canonical formatter
geblang init --name acme.tools       # scaffold a new project
geblang doctor                       # environment + toolchain report
geblang build --out app              # bundle script into one binary
geblang -m http.server 8080          # run a stdlib module's main()
geblang --version
geblang --help
```

## Run tests

```sh
make test                            # full Go test suite
geblang test examples/sample_test.gb # run a Geblang test file
geblang test --tag fast examples     # filter by @tag
```

## Project layout

- `cmd/geblang/` - CLI and REPL.
- `cmd/docsite/` - static reference manual generator.
- `internal/` - lexer, parser, semantic analyzer, AST,
  evaluator, bytecode compiler + VM, runtime, LSP, DAP,
  transpiler.
- `stdlib/` - source-distributed Geblang stdlib (loaded at
  runtime alongside the binary).
- `internal/native/` - pure stdlib functions shared by
  evaluator and VM.
- `internal/evaluator/` - tree-walking evaluator and stateful
  module implementations.
- `internal/bytecode/` - bytecode compiler, VM, format,
  parity tests.
- `docs/user/` - user reference manual (built into a static
  site by `make docs`).
- `examples/` - runnable examples for public APIs.
- `vscode-geblang/` - VS Code extension (syntax, LSP client,
  DAP, test explorer).

## License

MIT - see [LICENSE](LICENSE).
