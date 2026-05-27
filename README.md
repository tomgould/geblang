# Geblang

Geblang is a statically-typed scripting language implemented in Go.
Current version: **1.4.5**. It combines the ergonomics of PHP and
Python with strong static typing, generics, decorators, async, and
runtime reflection.

```gb
import http;
import web.router as router;

let app = router.newRouter();
router.get(app, "/", func(dict<string, any> req): dict<string, any> {
    return {"status": 200, "body": "Hello from Geblang"};
});
http.serve("127.0.0.1:8080", func(dict<string, any> req): dict<string, any> {
    return router.handle(app, req);
});
```

## Highlights

- **Static typing** with generics, unions (`T | U`), intersections
  (`T & U`), nullable types (`?T`), and explicit type assertions.
- **Classes** with single inheritance and interfaces, decorators
  (callable + metadata), pattern matching, enums, and reflection.
- **Concurrency** built on a cooperative async model: `async`
  functions, `await`, generators, structured task groups
  (`async.scope.scope`), and `async.race` / `async.all` /
  `async.timeout` combinators. Optional reactor backend for
  high-throughput TCP / HTTP via `{reactor: true}`.
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

The full language and stdlib reference is in
[docs/user/](docs/user/) - start with
[01-getting-started.md](docs/user/01-getting-started.md). The
release notes are at
[docs/user/17-release-notes.md](docs/user/17-release-notes.md).

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

io.println("Hello, world");
```

```sh
geblang hello.gb
```

Double-quoted strings evaluate escape sequences. Single-quoted
strings keep backslashes literal. `//` is integer division, not a
comment.

### HTTP server with typed binding

```gb
import http;
import web.router as router;
import json;

let app = router.newRouter();

router.get(app, "/users/:id", func(dict<string, any> req): dict<string, any> {
    let params = req["params"] as dict<string, any>;
    return {
        "status": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.stringify({"id": params["id"]}),
    };
});

http.serve("127.0.0.1:8080", func(dict<string, any> req): dict<string, any> {
    return router.handle(app, req);
});
```

### WebSocket echo server

```gb
import http;
import web.router as router;
import web.websocket as ws;

let app = router.newRouter();

router.get(app, "/ws", func(dict<string, any> req): dict<string, any> {
    return ws.upgrade(func(ws.Connection conn): void {
        while (true) {
            let msg = conn.readJson();
            if (msg == null) { return; }
            conn.sendJson({"echo": msg["text"]});
        }
    });
});

http.serve("127.0.0.1:8080", func(dict<string, any> req): dict<string, any> {
    return router.handle(app, req);
});
```

The upgrade callback runs once per connection. Inside, `conn.read`
/ `conn.readJson` block until the next frame; `conn.send` /
`conn.sendJson` write back. The connection closes when the callback
returns or the peer disconnects.

### Classes, generics, decorators

```gb
import io;
import reflect;

func cache(any fn): any {
    dict<string, any> hits = {};
    return func(string key): any {
        if (hits.contains(key)) { return hits[key]; }
        let value = fn(key);
        hits[key] = value;
        return value;
    };
}

class Repository<T> {
    list<T> items;

    func Repository() {
        this.items = [];
    }

    func add(T item): void {
        this.items = this.items.push(item);
    }

    func count(): int {
        return this.items.length();
    }
}

class User {
    string name;
    func User(string name) { this.name = name; }
}

let users = Repository<User>();
users.add(User("Ada"));
users.add(User("Carla"));
io.println(users.count());
io.println(reflect.typeOf(users));
```

### Async + structured concurrency

```gb
import async;
import async.scope as scope;
import io;
import time;

async func fetch(string label, int ms): string {
    await time.sleep(ms);
    return label + " done";
}

let results = scope.scope(func(scope.TaskGroup g): list<string> {
    let a = g.spawn(func(): string { return fetch("a", 100).await(); });
    let b = g.spawn(func(): string { return fetch("b", 50).await(); });
    return [a.await(), b.await()];
});

io.println(results);
```

Spawn errors propagate at scope exit; the first failure cancels
remaining siblings. `async.race`, `async.all`, and `async.timeout`
provide single-shot variants for simpler cases.

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
