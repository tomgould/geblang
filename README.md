# Geblang

Geblang is a type-safe interpreted scripting language implemented in Go. The
project is at version **1.0.2**. Geblang takes inspiration from both
PHP and Python, but adds many features these languages are unable to
offer.

## Architecture

The interpreter uses an AST-centered pipeline with a bytecode VM as the
production execution path; the tree-walking evaluator remains as a
fallback for unsupported VM constructs and as the reference semantics:

```text
source
  -> lexer
  -> parser
  -> AST
  -> semantic analysis (type checks, nullability, decorator metadata)
  -> bytecode compiler
  -> VM (with evaluator fallback)
```

Compiled bytecode is cached on disk (`~/.cache/geblang/bytecode/`)
keyed by source hash so subsequent runs skip parse / compile entirely.

## Target Capabilities

- Complete interpreted runtime
- Modules
- Classes with single inheritance
- Interfaces
- Standard modules for common scripting needs:
  - system and OS interaction
  - file and stream I/O
  - process management
  - HTTP server/client
  - networking helpers
  - SQLite, PostgreSQL, and MySQL database access
  - encryption and hashing
  - JSON parsing and encoding
  - TOML parsing and encoding
  - YAML parsing and encoding
  - common math helpers

## Reference Manual Website

The user manual lives in `docs/user/` as separate Markdown chapters. Build the
Bootstrap 5 themed static reference site with:

```sh
make docs
```

The generated HTML is written to `docs/site/`. The site includes the manual,
generated source API pages for `stdlib/`, and generated example pages from
`docs/examples/`. Open `docs/site/index.html` directly, or serve the directory
with any static file server:

```sh
python3 -m http.server --directory docs/site 8080
```

The static site generator is Go code in `cmd/docsite`. Use
`DOCS_API_SRC` and `DOCS_EXAMPLES_SRC` to point generated API and example
sections at other source trees.

## Docker Binary Build

To build a distributable Geblang binary and bundled source stdlib in Docker:

```sh
make docker-build
```

This creates:

```text
build/
  geblang
  stdlib/
```

Run the packaged binary:

```sh
./build/geblang --version
./build/geblang examples/hello.gb
```

If you move the binary away from the bundled `stdlib/` directory, set
`GEBLANG_STDLIB` to the copied stdlib path:

```sh
GEBLANG_STDLIB=/path/to/stdlib /path/to/geblang script.gb
```

## Quick Example

```gb
# This is a single-line Geblang comment.
/* This is a multi-line
   Geblang comment. */
import io;
import sys;

io.print("Hello world\n");
io.println("Hello world");
io.print('Hello world\n');

sys.exit(0);
```

Double-quoted strings evaluate escape sequences. Single-quoted strings keep
backslash escapes literal.

Geblang line comments use `#`; `//` is reserved for integer division.

The full language reference lives in [docs/user/](docs/user/) — start with
[01-getting-started.md](docs/user/01-getting-started.md). The complete API
reference is generated from source into the docs site.

Run the example:

```sh
make build
make docs
make docker-build
./geblang
./geblang repl
./geblang -m http.server 8080
./geblang check examples/hello.gb
./geblang check --json examples/hello.gb
./geblang check --strict examples/
./geblang init --name acme.tools
./geblang doctor
./geblang doctor --json
./geblang --help
./geblang help test
./geblang --version
go run ./cmd/geblang examples/hello.gb
```

Run tests:

```sh
make test
go run ./cmd/geblang test examples/sample_test.gb
go run ./cmd/geblang test --tag fast examples
```

## What's in 1.0

The full language and stdlib reference is at [docs/user/](docs/user/).
At a glance, 1.0 covers:

**Language.** Static typing with generics, nullability, decorators,
classes (single inheritance + interfaces), enums, pattern matching,
async functions, generators, `defer`, `try`/`catch`/`finally`, named
arguments, top-level await, and reflection.

**Runtime.** Bytecode VM with an evaluator fallback; on-disk bytecode
cache; cooperative goroutine-backed async; `runtime.Task` with
cancellation and combinators (`async.all`, `async.race`,
`async.timeout`).

**Stdlib.** `io`, `sys`, `bytes`, `encoding` (base64/32/58, URL,
HTML), `crypt` (hashes, HMAC, Argon2id, bcrypt, JWT, AES-GCM,
XChaCha20-Poly1305, RSA/EC/Ed25519, certs), `re` (RE2 regex with
named groups + matchAll), `json` / `yaml` / `xml` / `toml` /
`markdown`, `datetime`, `time` (+ `time.scheduler` for Timer / Ticker
/ Interval), `random`, `secrets`, `collections`, `path`, `pathlib`,
`math`, `http` (server + reusable client with cookie jar, keep-alive,
proxy), `websocket`, `net`, `db` (sqlite / postgres / mysql), `web`
(router), `test`, `log`, `metrics`, `tracer`, `profiler`, `schema`,
`serde`, `watch`, `mailer`, `redis`, `functools`, `option`, `result`,
`config`, plus async wrappers under `async.io`, `async.http`,
`async.net`, `async.stream`, `async.rate`.

**Tooling.** `geblang` (run/repl/check/test/fmt/init/install/build/
doctor/cache), VS Code extension (syntax highlighting, LSP, DAP,
test explorer, code lenses, snippets), Dockerised reproducible
build, single-binary `geblang build --out` bundling, Linux/macOS/
Windows.

