# Release Notes

## 1.4.1

### Stdlib

- `int.toString(base)` and `string.toInt(base)` accept any base
  2-36 for arbitrary base conversion (lowercase digits a-z).
- `encoding.base64UrlEncode` / `base64UrlDecode` for unpadded
  URL-safe Base64 (RFC 4648 section 5); decoder accepts padded
  or unpadded input.
- `bytes.toBase64Url` / `bytes.fromBase64Url` module helpers and
  a `b.toBase64Url()` method on bytes values.
- New `binary` module with Python `struct`-style pack/unpack:
  `binary.pack(format, ...values)`, `binary.unpack(format, data)`,
  `binary.unpackNamed(spec, data)`, and `binary.size(format)`.
  Format codes cover signed/unsigned 8/16/32/64-bit ints, 32/64-bit
  floats, fixed-length byte strings, and pad bytes; the first
  character may set endianness (`<` little, `>` big, `!` network,
  `=` native).

## 1.4.0

### Performance

- Tight integer loops are noticeably faster: `BenchmarkIntLoop`
  3.7 ms to 2.74 ms, `BenchmarkIntArithmetic` 6.2 ms to 5.2 ms.
- `recursive_fib` scoreboard 86 ms to 67 ms.
- `list_functional` scoreboard 14 ms to 12 ms (matches PHP).
- Recursive call paths drop ~20 allocations per call from the
  new function-frame layout; long-running recursive workloads
  see lower GC pressure.

### Language

- List-shape patterns in `match`:
  `case [int x, int y] if x > y => ...`. Each binding may be
  typed (must match) or untyped (any value); `_` is a wildcard
  that skips binding. Length and type mismatches both fall
  through to the next case.
- Union types (`T | U`) at parameter and return positions:
  `func get(int | string id): User | NotFoundError`. The runtime
  enforces "any branch matches" and throws a catchable
  `RuntimeError` on mismatch (parameter-validation errors now
  go through the standard throw path on the VM, matching the
  evaluator). Intersection (`T & U`) supported with "every
  branch matches" semantics.
- Structured concurrency via the `async.scope` module:
  `async.scope.TaskGroup` with `.spawn(fn)` / `.cancel()` and
  the `async.scope.scope(body)` runner. The body receives the
  group; spawned children are awaited at scope exit; if the
  body or any child throws, remaining children are cancelled
  and the first error is rethrown after the drain completes.

### Stdlib

- New `messaging` module with a unified queue + topic facade and
  pluggable backends. Backends: AWS SQS over HTTPS with sigv4,
  RabbitMQ over AMQP 0.9.1, STOMP 1.2 (covers ActiveMQ natively
  and RabbitMQ via the STOMP plugin), and Kafka.
  `messaging.connect({driver, ...})` returns a queue handle with
  `publish` / `receive` / `ack` / `consume` / `close`.
  `messaging.topic({driver, ...})` returns a pub/sub handle with
  `publish` / `subscribe` / `close`; RabbitMQ uses a fanout
  exchange, STOMP uses `/topic/` destinations, Kafka uses a fresh
  consumer group per subscriber. SQS is queue-only on this
  surface; use AWS SNS for fan-out and target SQS queues as SNS
  subscriptions. Lower-level `amqp` and `kafka` native modules
  are exposed for cases beyond the facade.
- New `archive` module with zip / tar / tar.gz readers and
  writers: `archive.zipRead` / `zipWrite` / `tarRead` /
  `tarWrite` / `tarGzRead` / `tarGzWrite`. Entries are dicts
  with `name`, `data` (bytes), `isDir`, and `size`; writers
  accept string or bytes payloads and sort tar entries by name
  for deterministic output.
- `crypt.jwtSign(payload, key, opts?)` and
  `crypt.jwtVerify(token, key, opts?)` now dispatch on the
  `alg` option (or the token header) and cover every supported
  algorithm in one pair: HS256 / HS384 / HS512, RS256 / RS384 /
  RS512, ES256 / ES384 / ES512, and EdDSA (Ed25519). Pass
  `opts.allowedAlgs` to defend against alg-confusion attacks.
  The default allow-list excludes `none` on both sign and
  verify; opt in by passing `"none"` inside `opts.allowedAlgs`
  when you genuinely need unsigned tokens.
  `crypt.jwtSignRS256` / `jwtVerifyRS256` / `jwtSignES256` /
  `jwtVerifyES256` remain as deprecated shims for 1.5.0 removal.
- New `crypt.jweEncrypt(payload, key, opts)` and
  `crypt.jweDecrypt(token, key)` for encrypted JWTs. Key-wrap
  algorithms: `dir` (32-byte CEK) and `RSA-OAEP-256` (wraps a
  fresh CEK with an RSA public key). Content encryption is
  `A256GCM`. Tampered tokens fail the AEAD authentication and
  throw.
- New `crypt.pkcs12Decode(pfx, password)` returning
  `{key, cert, caCerts}` where `key` is a PKCS#8 PEM and the
  certificate fields are CERTIFICATE PEM strings. Encoding to
  PFX is not in scope for 1.4.0.
- New `crypt.signCertificate(options)` signs a CSR with a CA
  certificate and key, returning the issued certificate PEM.
  Options: `csr`, `caCert`, `caKey` (all PEMs), `validDays`
  (default 365), `isCA` (default false), `dnsNames`,
  `ipAddresses`, `serialBits` (default 128). Completes the
  CSR-to-issued-cert pipeline (`generateCsr` to
  `signCertificate` to `parseCert`).
- `metrics.counter(name, opts)` / `gauge` / `histogram` declare
  typed metrics with optional labels; `metrics.observe(name,
  value, labels)` records histogram samples; `metrics.toPrometheus()`
  emits Prometheus v0.0.4 text exposition format. Legacy
  `metrics.inc` / `set` keep working unchanged.
- `log.toStream(stream)` writes JSON log lines to any
  `streams.IOStream` (memory buffer, TCP socket, pipe).
- `trace.toOtlpJson(opts?)` serialises recorded spans as OTLP/HTTP
  JSON; `trace.exportOtlp(endpoint, opts?)` POSTs them to a
  collector at `endpoint/v1/traces`. Child spans via
  `trace.start(name, attrs, {parent})`.

### Performance

- Faster tight loops that mix arithmetic with collection length /
  modulo: `regex_match` 62 ms to 45 ms, `numeric_loop` 123 ms to
  92 ms, `recursive_fib` 65 ms to 58 ms.
- `BenchmarkRecursiveFib` allocs/op 301 to 24.

### Tooling

- `make bench` now builds geblang via `make build` first and
  benches that binary, so source changes always reach the
  scoreboard.

## 1.3.0

### Stdlib

- New `pcre` module: PCRE-compatible regex engine for patterns
  that need lookahead, lookbehind, backreferences, atomic groups,
  or named captures via either `(?P<name>...)` or `(?<name>...)`.
  Surface mirrors `re.*` (test, find, findAll, match, matchAll,
  replace, split, quote) with an optional flags string (`imsx`).
  Coexists with the existing `re` module (RE2, linear-time, no
  catastrophic backtracking) - reach for `pcre` only when you
  need PCRE-only features.

### Language

- `test.Test.assertThrows(callable, expectedSubstring = "")` is
  now a built-in assertion on both the evaluator and the VM.
  Fails if the no-arg callable returns without raising; when the
  optional substring is given it must appear in the error
  message.
- `test.run(class, opts)` accepts a new `methods` option (a list
  of method names) so tooling can run a single test.
- `geblang test --class ClassName` and `--method methodName`
  flags filter to a single class or method within the discovered
  test files.
- `geblang test --format teamcity` emits `##teamcity[...]`
  service messages (with `locationHint='geblang_test://Class/
  method'`) so JetBrains IDE test runners parse events natively.
  Replaces the verbose PASS/FAIL output for IDE integration.
- `test.mock(moduleName, {fname: callable})` swaps stdlib
  functions for the duration of the current `@test` method;
  the runner snapshots patches before each method and restores
  them after, so mocks never leak across tests. Pair with
  `test.restore(module, fname)` / `test.restoreAll()` for mid-
  method toggling.
- New `crypt.hmacSha256Bytes(key, message): bytes` returns the
  raw HMAC output (instead of hex). Useful when the HMAC output
  is the next round's key - sigv4, HKDF, TLS PRF, etc. Verified
  against the AWS sigv4 reference vector.

### Fixes

- VM: string `<`, `<=`, `>`, `>=` now work the same way they did
  in the evaluator (lexicographic comparison). Previously the VM
  dispatched relational ops through `native.NumericCompare` which
  rejected strings with "comparison expects compatible numeric
  operands"; the evaluator's own `compareValues` already covered
  it. Both paths now share the extended `NumericCompare`. New
  parity test guards the behaviour.

### Tooling

- LSP: `this.<TAB>` inside a class extending `test.Test` now
  surfaces every inherited assertion (assertEquals, assertTrue,
  assertThrows, fail, etc.).
- LSP: `<typedVar>.<TAB>` and `<typedVar>.<method>(<cursor>)` on
  stdlib-class locals (http.Request, db.Connection, datetime.Instant,
  url.URL, streams.IOStream, proc.Process, sockets.Socket/Listener,
  ssh.SSHClient/SSHSession/SSHTunnel, strbuilder.StringBuilder,
  random.Generator, websocket.Connection, template.Template/Engine,
  log.Logger) surfaces methods with parameter and return types.
  Triggers when the local is declared via `<module>.<Class> name;`.
- VS Code: new `assertThrows` snippet under the Geblang language.

## 1.2.0

### Stdlib

- New `sockets` module: `sockets.dial(host, port, opts)` opens a
  TCP or TLS connection and returns a `Socket` wrapping the
  stream protocol. `sockets.serve(host, port, handler)` binds a
  listener and dispatches each accepted connection to the handler
  callback. `server.close()` joins the accept goroutine so reads
  of module-level state from the parent happen-after the last
  handler invocation. Sockets implement `read` / `readAll` /
  `readLine` / `lines` / `write` / `writeln` / `close` plus the
  dunder protocol for `streams.copy` and `for (line in sock)`.
- New `ssh` module: a Geblang-native SSH client.
  `ssh.connect(target, opts)` opens an authenticated connection
  (password / private key / passphrase / agent), with host-key
  verification via `knownHostsFile`. `client.exec(cmd)` runs a
  one-shot command returning `{stdout, stderr, exitCode}`.
  `client.spawn(cmd)` returns an `SSHSession` with
  `streams.IOStream`-shaped stdin / stdout / stderr (same shape
  as `proc.Process`), plus `wait()`, `kill()`, `signal(name)`.
  SFTP: `upload`, `download`, `sftpList`, `sftpRemove`,
  `sftpMkdir`, and `sftpOpen` (returns an IOStream for piping
  remote files through `streams.copy`). Port forwarding via
  `forwardLocal(port, target)` and `forwardRemote(port, target)`
  returns `SSHTunnel` handles that close cleanly.
- `http.post` / `http.request` / `http.requestWithOptions` accept
  a `streams.IOStream` (or any class wrapping one) for the
  request body, in addition to the existing string and bytes
  shorthand. Useful for multi-GB uploads that shouldn't load
  into memory.
- New `cli.widgets.Spinner` and `cli.widgets.ProgressBar` render
  ANSI control sequences to stderr (so stdout piping stays
  usable). The spinner has `tick()` / `update(msg)` / `stop()`;
  the bar has `advance(n)` / `set(value)` / `updateLabel(label)`
  / `finish()`.

### Bug fixes

- File / stream / socket close paths now suppress
  already-closed errors so user code that closes the same handle
  twice no longer surfaces the harmless errno (covers
  `os.ErrClosed`, `net.ErrClosed`, and the "use of closed
  network connection" string fallback).

## 1.1.0

### Stdlib

- New `streams.IOStream` class wraps a file or in-memory handle
  with `read`, `readAll`, `readLine`, `lines`, `write`, `writeln`,
  `flush`, `close`, and `for (line in stream)` iteration.
  Memory-backed instances also expose `toString()`.
- New factories: `streams.open(path, mode)`,
  `streams.memory(initial)`, and
  `streams.stdin / stdout / stderr()`.
- New helpers `streams.readAll(src)` and
  `streams.copy(src, dst)` consume any value implementing the
  stream protocol.
- New `proc` module starts child processes that stream
  concurrently with the parent: `proc.spawn(cmd, args, opts)`
  returns a `Process` with `stdin`, `stdout`, `stderr` (each an
  `IOStream`), plus `wait()`, `kill()`, `signal(name)`, and
  `pid`. `{pty: true}` attaches a pseudo-terminal so interactive
  programs see a TTY. The existing synchronous `process.run` /
  `sys.run` are unchanged.
- New `watch.start(path, callback, opts)` registers an fsnotify
  watcher and fires `callback({path, type})` for each filesystem
  event. `{recursive: true}` walks subdirectories at register
  time. `watch.stop(handle)` waits for the in-flight callback to
  finish before returning. Polling helpers (`watch.snapshot` /
  `watch.wait`) remain available.

### Language features

- Stream protocol dunders: classes implementing `__read(int)`,
  `__write(string)`, and `__close()` plug into `streams.copy`,
  `streams.readAll`, and `for (line in stream)` directly.
- Generator methods on user classes now run on the VM.

### Bug fixes

- `io.readLine` over an in-memory stream returned `null` after
  the first line; now reads each line in turn.
- Cross-goroutine timer and ticker callbacks no longer trip the
  race detector.
- Methods on a main-script class invoked from a stdlib module
  work without an `unknown class` error.
- `sys.sleep` and `process.signal` accept any int value.

### Performance

- Tight integer loops stay lock-free after the timer-race fix;
  `numeric_loop` 166 ms to 136 ms.

## 1.0.6

### Performance

- JSON parse + stringify overhaul: shorter dict-key tags, repeated-
  key interning, direct float/string encoders, pooled scratch
  buffers; `json_roundtrip` 1665 to 520 ms, faster than Python's C
  json on the bench.
- Function-call hot path passes function metadata by pointer rather
  than copying a 350-byte struct on every call; `recursive_fib` 88
  to 75 ms.
- Method dispatch VMValue fast path on `instance.method(args)`;
  `class_dispatch` 30 to 21 ms.
- Tail-call elimination: `return f(args)` reuses the current frame
  for primitive-typed-arg functions, removing the stack-depth
  ceiling on tail-recursive loops.

### Language features

- Iterator protocol: classes with `__iter()` / `__done()` /
  `__next()` work in `for (x in obj)`.
- `streams.Stream` fluent collection pipeline (`map`, `filter`,
  `take`, `toList`, `toSet`, `count`, `first`, `reduce`, `forEach`,
  `anyMatch`, `allMatch`); lazy by default.
- `reflect.location(target)` returns `{module, line, column}` for
  functions, classes, closures, decorator targets, and instances.
- Named arguments in `defer` for callable, instance-method, and
  module-function shapes.
- Nested generic call-site inference: `list<dict<K, V>>` and
  deeper shapes bind every leaf type parameter.

### Bug fixes

- Cross-chunk closures invoked through a stdlib-module class
  method no longer resolve to the wrong function index.

### Stdlib

- New `streams` module: `streams.of(source)` to wrap an iterable,
  plus the `Stream` class.

## 1.0.5

### Performance

- `acc = acc + "literal"` in tight loops now uses a hidden builder;
  `string_concat` 78 to 8 ms.
- Callbacks (`collections.map` / `filter` / `reduce` etc.) no longer
  rebuild a sub-VM per call; `list_functional` 1604 to 13 ms.
- Regex compile cache (`re.*`); `regex_match` 187 to 64 ms.
- Field-access inline cache; `class_dispatch` 26 to 21 ms.
- Tagged-`VMValue` arithmetic on hot ops; `dict_ops` 27 to 19 ms.
- Compile-time folding of literal arithmetic; div-by-zero is a
  check-time error.
- Direct `runtime.Value` to JSON encoder.

### Stdlib

- `strings.StringBuilder` class for explicit builder-backed string
  assembly.
- `csv.parse` / `csv.parseDict` / `csv.stringify` for in-memory CSV;
  options: `delimiter`, `trimSpace`.
- `path.glob` now supports Python-style `**` recursive matches.
- `math.median` / `percentile` / `quantile` / `mode` over numeric
  lists. R type-7 linear interpolation.
- String methods `splitRegex`, `replaceRegex`, `matchesRegex` for
  regex-aware split / replace-all / test.

### Benchmarks

Three new scoreboard benches: `regex_match`, `json_roundtrip`,
`list_functional`.

## 1.0.4

Bytecode VM hot-path performance + a lifted compiler parity gap,
on top of the type-matcher fixes that surfaced building the Gebweb
Tasks example app.

### Language features

- **Cast overloading via dunder methods.** A class can now control
  how its instances respond to `as TYPE` casts by defining
  `__string`, `__int`, `__float`, `__bool`, `__decimal`, or
  `__bytes`. The dunder's declared return type must match the
  target primitive; the semantic analyzer rejects mismatches at
  compile time, and the runtime double-checks the returned value.
  Falls back to the existing built-in cast logic when no dunder is
  defined for the target type. Parallel to the existing operator
  overloading dunders (`__add`, `__lt`, etc.). New
  `async.token()` builtin returns a fresh uncompleted Task used as
  a pure cancellation signal by the redesigned Timer/Ticker stdlib.

### Known limitations

- **Async callbacks that close over a `BytecodeClosure` / `Function`
  passed to a stateful native module can race with parent VM state.**
  The wrap layer that bridges Geblang callable values into native
  code captures the parent VM by pointer; when the native side then
  invokes the wrapped value on a goroutine (as `async.run`,
  `async.sleep`, etc. do), reads of `vm.globals` from the worker
  goroutine race with continued writes on the main goroutine. The
  go test race detector flags two parity tests
  (`TestParityTimerFires`, `TestParityTickerStops`); functional
  output is correct on both backends but ordering of those reads is
  technically unsynchronised. Closing the race needs a refactor of
  the native bridge to thread a per-goroutine VM context; queued
  for a 1.0.5 architectural pass.

### Performance

Profile-guided round (Geblang's own `profiler.snapshot()` /
`profiler.delta()` plus Go's pprof on `recursive_fib`,
`string_concat`, `class_dispatch`):

- **Static-type propagation through function calls and class
  fields.** `staticIntExpr` / `staticStringExpr` now recurse into
  `CallExpression` (when every matching overload declares the
  target return type) and `SelectorExpression` (when the receiver
  is `this` or a typed class instance and the named field is
  declared with the target type). Required flushing
  `class.FieldNames` / `class.FieldTypes` into `chunk.Classes`
  before compiling each method body so field metadata is visible
  during method compilation. Profile shows `vm.add` disappearing
  from `recursive_fib` (was 10.26% of CPU) and `class_dispatch`
  hot paths; replaced by the inline-handled `OpAddInt` family.

- **Fused string-append peephole.** `local = local + "literal"`
  and `global = global + "literal"` now compile to a single
  `OpAppendStringConst` / `OpAppendGlobalStringConst` instead of
  the three-opcode sequence `OpGetLocal`/`OpAddStringConst`/
  `OpSetLocal`. Bytecode chunk format version bumped 53 to 54.

- **REPL accepts `1 as bool`.** The REPL's auto-semicolon-insertion
  treated only identifier / numeric-literal stmt-end tokens as
  triggering insertion. `bool` is the only type name that lexes as
  a keyword (string/int/float/decimal/bytes lex as `Ident`), so
  `as bool` at the end of a REPL line gave `expected ;, got EOF`.
  `Bool` is now in the stmt-end set.

Bench impact (3-run medians, after the full sequence of Tier A +
type-propagation + peephole + earlier 1.0.4 work):

| Bench | Pre-1.0.4 | After 1.0.4 | Δ |
|-------|----------:|------------:|--:|
| numeric_loop   | 131 | 124 | -5% |
| recursive_fib  |  92 |  86 | -7% |
| list_pipeline  |  13 |   9 | -31% |
| string_concat  |  84 |  70 | -17% |
| dict_ops       |  24 |  19 | -21% |
| class_dispatch |  47 |  37 | -21% |

Geblang now beats Python on `numeric_loop`, `list_pipeline`, and
`dict_ops`; competitive on the rest. The remaining gap to PHP
(2.5x to 5.4x on the slow benches) is structural: Go interpreter
floor on dispatch, Go string-concat allocator pressure on
`string_concat`, and per-call frame setup on `recursive_fib`. The
roadmap entries for B1 (flat-stack locals) and C-tier interface
removal would address those; both queued for 1.0.5.

### Tooling

- **`profiler.delta` / `memory` / `peak` / `cpu` return dicts that
  are usable from Geblang.** They previously returned
  `*runtime.Dict` (pointer); the VM's index handler only matches
  `runtime.Dict` by value, so `d["elapsed_ms"]` raised "dict is
  not indexable". Now returns `runtime.Dict` directly; the docs
  example actually works.

### Documentation

- **`docs/user/03-types.md`** now points to
  `stdlib/08-collections.md` for the full list/dict/set method
  catalogue (`length`, `push`, `pop`, `slice`, `map`, `filter`,
  `reduce`, `keys`, `values`, `items`, `add`, `union`,
  `difference`, etc.).
- **`docs/user/stdlib/08-collections.md`** gains a "Keyed
  Functional Helpers" section enumerating the instance-method form
  of `sortBy`, `minBy`/`maxBy`, `topK`/`bottomK`, `sumBy`,
  `frequencies`, `indexBy`, `containsBy`, `binarySearch`,
  `lowerBound`/`upperBound`, `take`, `zipWith`, `lazyMap` /
  `lazyFilter`, etc. (interchangeable with the `collections.X(list,
  ...)` module form).

### Earlier 1.0.4 performance work

- **`OpAdd` string fast-path** (`vm.go:add`). The dispatcher used
  to call `callBinaryOperatorMethod` first on every add, including
  the common `string + string` case where the built-in `string`
  type has no `__add` magic method. The runtime.String check now
  short-circuits at the top of `OpAdd` when both operands are
  strings; the method-dispatch detour is preserved for class
  instances on the left.

- **Single-overload method dispatch shortcut**
  (`vm.go:selectRuntimeFunction`). Most user classes declare a
  single overload per method. The dispatcher now skips the
  matches-slice allocation + post-loop "ambiguous overload" check
  for `len(indices) == 1`, going straight to arity + type
  validation on the lone candidate. Behaviour is unchanged.

- **String-key dict fast path** (`vm.go:dictKeyFor`). A new helper
  inlines `runtime.String` and `runtime.SmallInt` key conversion
  (the 99% case) and falls through to `native.DictKey` for
  composite keys. Wired into the hot dict ops: index get/set,
  `dict.contains`, `dict.get`, and the `set` membership check.

- **Compile-time `OpAddString` opcode** (`compiler.go` +
  `vm.go`). When the compiler can prove both operands of `+` are
  statically typed `string` (via `staticStringExpr` mirroring the
  existing `staticIntExpr`), it emits a specialised `OpAddString`
  opcode that runs the concat inline with no type switch or
  method-dispatch detour. Mirrors the existing `OpAddInt` family
  for ints. Bytecode chunk format version bumped 50 to 51.

- **Method-pointer lookup cache** (`vm.go:lookupMethodLower`).
  A single-slot cache keyed by (class name, lowered method name)
  short-circuits the `classInfo.Methods` Go-map access on the
  second-and-later dispatches to the same method on the same
  class. Tight loops calling one method on one class (every
  `class_dispatch`-shaped workload) hit the cache on >99% of
  calls and skip the map lookup entirely.

Bench impact (median ms, before to after Tier 1 + Tier 2,
over 3 runs):

| Bench | Before | After | Δ |
|-------|-------:|------:|--:|
| numeric_loop   | 131 | 129 |  -2% |
| recursive_fib  |  89 |  85 |  -4% |
| list_pipeline  |  13 |   9 | -31% |
| string_concat  |  84 |  70 | -17% |
| dict_ops       |  24 |  19 | -21% |
| class_dispatch |  47 |  38 | -19% |

Geblang is now faster than Python on `numeric_loop`,
`list_pipeline`, and `dict_ops`; competitive on the rest.

New parity tests `TestParityStringAddFastPath`,
`TestParitySingleOverloadMethodDispatch`,
`TestParityDictKeyFastPath`, `TestParityOpAddStringStaticTyping`,
`TestParityMethodLookupCache`; new language test
`tests/core/vm_hot_path_test.gb`.

Tier A follow-up (`vm.go` + `bytecode.go`):

- **`OpAddString` writes the result VMValue directly into the
  stack slot**, mirroring the `OpAddInt`-family inline write. The
  handler reads operands from `vm.stack[n-2]` / `vm.stack[n-1]`
  without calling `vm.pop()`/`vm.push()`, so the interface
  materialise + function call overhead drops out. The interface
  box for the result `runtime.String` is unchanged and dominates
  the per-iteration cost; closing that gap is the job of B2
  (VMKindString variant on VMValue), planned next.

- **Per-call type-validation loop is now skipped for functions
  whose params are entirely empty / `any`-typed.** A new
  `FunctionInfo.requiresParamValidation` bool is precomputed at
  chunk-load time (in `prepareFunctionTypeMetadata`); both the
  fast and slow call paths short-circuit the whole validation
  walk when it's false.

- **Inline-cache experiment on `OpMethodCall` / `OpMethodCallNamed`
  did NOT ship.** A per-instruction class-pointer cache was
  implemented, measured, and reverted: the existing VM-global
  `methodLookupClass/Name/Indices` single-slot cache already hits
  >99% on the monomorphic call sites the `class_dispatch` bench
  exercises, so the per-call bounds check and cache compares the
  inline cache added cost more than they saved. Documented as a
  future candidate when a polymorphic-call benchmark exists.

- **`VMKindString` variant on `VMValue` did NOT ship.** A new kind
  was added with an inline `Str string` field so `OpAddString`
  could skip the interface-box heap allocation per push. Grew
  `VMValue` from 32 B to 48 B (+50 %), which regressed
  `string_concat` (69 to 82 ms), `dict_ops` (18 to 23 ms),
  `class_dispatch` (37 to 42 ms), and `recursive_fib` (85 to 95 ms)
  via worse cache locality on the stack/locals/globals slices.
  Reverted. The runtime.String interface box remains the dominant
  per-iteration cost on `string_concat`; closing it cleanly needs
  either a smarter VMValue layout (e.g. unsafe-overlay onto the
  existing Boxed field) or compile-time string interning that
  reduces the alloc rate enough to make the GC pressure go away.

### Parity

- **Empty-container defaults compile to bytecode directly.** Parameter
  and class-field defaults of the form `dict opts = {}` and
  `list xs = []` previously routed through the evaluator
  (`compiler.go:4757` rejected anything beyond primitive literals).
  The compiler now accepts empty `DictLiteral`, `ListLiteral`, and
  `SetLiteral` as defaults; the runtime constant pool gets three
  new tags (10/11/12) for the empty containers. To avoid the
  Python-style mutable-default trap, the VM clones the container
  at fill time via a new `cloneContainerDefault` helper, so each
  call (or new class instance) sees a fresh empty container.
  Non-empty container defaults (e.g. `list xs = [1, 2, 3]`) still
  fall back to the evaluator - lifting those needs full
  expression evaluation at call time, which is a bigger
  restructuring of the calling convention. Bytecode chunk format
  version bumped 51 to 52. New parity test
  `TestParityEmptyContainerDefaults`; new language test
  `tests/functions/empty_container_defaults_test.gb`.

- **`static func` methods compile to bytecode directly.** Previously
  any class with a `static func` declaration tripped the
  `compiler.go:741` "does not support static functions yet" parity
  error and the CLI fell back to the tree-walking evaluator. The
  parity error reflected an incomplete implementation, not a real
  constraint: the runtime infrastructure for static methods
  (`class.StaticMethods`, `OpCallStaticMethod`, `OpGetStaticValue`)
  was already in place. The compiler now lowers static method
  bodies through the same pipeline as regular methods (skipping
  the implicit `this` receiver), so scripts using static methods
  (including every `@ApiResource` entity in Gebweb that carries
  `static func repositoryClass()`) now run purely on the VM.
  New parity test `TestParityStaticFunctionLifted`; new language
  test `tests/classes/static_methods_test.gb`. Two pre-existing
  Go tests that asserted the static-func rejection were updated
  to use non-literal class field defaults as their canonical
  "still-unsupported" feature.

- **Spread arguments on a callable VALUE compile to bytecode
  directly.** Two compiler.go sites used to reject spread on
  callable values: parenthesized-selector callable expressions
  (`(obj.fn)(...args)`, line 2440) and complex callable
  expressions (`fns[i](...args)`, `getFn()(...args)`, line
  2602). Both forms now emit the same `OpMethodCallSpread`
  with the `__invoke` method name that the existing
  identifier-callable spread path uses. Static args before the
  spread are supported (`(h.adder)(1, ...rest)`); named args
  mixed with spread are rejected at compile time as before.
  New parity test `TestParityCallableSpread`; new language
  test `tests/functions/callable_spread_test.gb`.

### Bug fixes

- **`cli.table` accepts the documented options-dict form.** The
  user manual (`docs/user/stdlib/13-cli.md`) showed
  `cli.table(rows, {columns: [...], headers: [...], separator: " | "})`
  but the implementation only accepted an optional bare list of
  header strings; calling with a dict raised
  `cli.table headers must be list<string>`. The implementation
  now accepts both forms: the legacy `cli.table(rows, ["A", "B"])`
  AND the documented options dict. `columns` picks the dict
  fields to render and their order; `headers` overrides the
  display labels (defaulting to the column key names);
  `separator` customises the inter-column gap (defaulting to two
  spaces). The legacy list form continues to work unchanged.

- **REPL multi-line container literals no longer get a spurious
  semicolon injected** (`cmd/geblang/repl.go:replInsertSemicolons`).
  The ASI-style semicolon-insertion walked the token stream looking
  for statement-ender tokens at line ends, but didn't track bracket
  nesting. A list of dict literals like:

  ```
  let rows = [
      {"name": "Alice"},
      {"name": "Bob"}
  ];
  ```

  had a `;` inserted after the closing `}` of each inner dict
  (because `}` is a statement-ender token), splitting the outer
  list literal and producing `expected next token to be ], got ;`.
  The injector now tracks `(`/`[` nesting depth across the source
  and only inserts at depth 0. Braces `{` are deliberately not
  counted so semicolons are still inserted inside function /
  if / for bodies. New regression test
  `TestReplInsertSemicolonsRespectsNesting`.

- **User class named `Task` no longer collides with the runtime
  async `Task`.** The evaluator's overload / parameter type-matcher
  unconditionally rejected any value flowing into a `Task`-typed
  parameter when the value wasn't a `*runtime.Task`. A user-declared
  `class Task { ... }` therefore broke at every dispatch. `repo.save(t)`
  with `save(Task entity)` and a user-class Task argument errored as
  `no matching overload`. The matcher now falls through to user-class
  matching when the value isn't the async Task primitive, so a
  user class named `Task` works exactly like any other user class.
  The VM was already correct (its type-name dispatch routes through
  `vmTypeKindForBase` and never hard-codes the `Task` string), so
  only the evaluator path needed the fix.

- **`?UserClass` parameter matching on the VM.** The VM's
  `parseVMTypeSpec` kept the leading `?` on `spec.base`, so the
  user-class comparison `value.TypeName() == spec.base` always
  failed for nullable parameter types like `?AuthConfig` or
  `?Task`. Only the `?T` shape for primitives was working
  (those routed through the kind switch). The parser now strips
  the `?` from `spec.base` at parse time and carries the
  nullability on the separate `spec.nullable` flag, so a
  `?UserClass` parameter accepts a `UserClass` instance again.

New parity test `TestParityUserClassNamedTaskNoCollision`; new
language test `tests/classes/user_class_named_task_test.gb`.

## 1.0.3 (released)

Small parity / ergonomics fixes uncovered while extending Gebweb.

### New

- **`web.parseMultipart(request)`.** New native that decodes a
  `multipart/form-data` request body into a
  `{fields: dict<string, string>, files: dict<string, dict>}` dict,
  where each file entry is `{filename, contentType, bytes}`.
  Returns an error (catchable, wrappable as 400) when the body
  isn't multipart or the boundary is missing. Gebweb's
  `dict<string, UploadedFile>` parameter binding is built on top
  of this. New parity test `TestParityWebParseMultipart`.

### Bug fixes

- **Import alias collisions no longer leak across files (evaluator).**
  The evaluator kept a process-wide `importNames` map that recorded
  the LAST `import X as Y` per alias `Y`, so two files that both
  used the same alias for different canonical modules collided: e.g.
  a user file `import web.websocket as websocket;` overwrote the
  alias mapping that stdlib `import websocket;` had registered for
  the native, and stdlib code calling `websocket.upgrade(...)`
  routed through the user's wrapped module instead, surfacing as
  "module websocket has no export upgrade". The fix adds a
  `Canonical` field to `runtime.Module` (per-binding canonical
  module name) and has the call dispatcher consult the env-local
  binding's `Canonical` before falling back to the shared map. The
  VM was already correct because each compiled chunk owns its own
  globals; only the evaluator needed the fix. New parity test
  `TestParityImportAliasDoesNotCollideAcrossFiles`; new language
  test `tests/core/import_alias_collision_test.gb`.

- **`list.sort()` is an alias for `list.sorted()`.** The LSP
  catalog (`catalog.go:142-143`) advertised both names but only
  `sorted` was wired into the method dispatcher, so user code reading
  the documented surface and calling `xs.sort()` failed with `list
  has no method sort` on both backends. Both names now route to the
  same implementation. New parity test
  `TestParityListSortAliasesSorted`; new test methods
  `listSortMethod` / `listSortWithComparator` in
  `tests/stdlib/collections_test.gb`.

- **`geblang check` recognises the `string` native module.** The
  CLI's `nativeImportModules` allowlist - used by `geblang check`'s
  import-resolution pass to skip native modules that have no
  on-disk source - was missing `string`, so any file with
  `import string;` produced `error[import]: cannot resolve import
  string` even though the evaluator and VM both registered the
  module. Tests passed because the test runner doesn't gate on
  check diagnostics; `make check-lang` exposed the
  false-positive. Added `"string"` to the allowlist alongside
  `"smtp"`. `tests/core/cross_type_casts_test.gb` is now check-clean.

- **Cross-module `implements` on the bytecode compiler.** A class
  declaring `implements mod.Interface<T>` against an interface
  exported from another module - the canonical case is
  `class WidgetRepo implements repository.Repository<Widget>` in
  Gebweb - failed the bytecode compile with `bytecode compiler
  interface mod.Interface is not declared` and fell back silently to
  the evaluator. The evaluator's `resolveTypeValue` already walks
  imports; the compiler's `c.interfaces` lookup only knew about
  locally-declared interfaces, mirroring a parent-class case that
  was already allowed at `compiler.go:891`. The compiler now accepts
  any dotted name (`strings.Contains(name, ".")`) under both
  `implements` clauses and interface `extends` clauses, and the VM's
  `interfaceMatches` strips the module prefix from the stored name
  so `instanceof Repository` continues to match the
  module-qualified entry stored on the class. Six gebweb test files
  + the `widgets.gb` example that previously errored on
  `geblang check` now compile cleanly to bytecode. New parity test
  `TestParityCrossModuleImplements`; new language test
  `tests/classes/cross_module_interface_test.gb`.

- **`func` value casts to `callable` on the evaluator.** The
  evaluator's CastExpression handler matched the value's
  `TypeName()` ("func") against the target ("callable") and
  rejected the cast with `cannot cast func to callable`. The VM
  accepted it because its `castValue` returned `value` when the
  target was a known alias of the value's runtime type. Both
  backends now route the `callable` / `func` / `function` family
  through `runtime.IsCallableValue`, so any callable runtime value
  (Function, OverloadedFunction, BytecodeFunction, instance with
  `__invoke`) casts cleanly. Surfaced from gebweb middleware
  helpers that store user callbacks in a `dict<string, any>`
  options dict and later cast them back to `callable`. New
  parity test `TestParityFuncAsCallable`; new test class
  `FuncAsCallableTest` in `tests/core/cross_type_casts_test.gb`.

## 1.0.2 (2026-05)

A quality-of-life release polishing two papercuts that surfaced while
building Gebweb.

### Highlights

- **Range-to-list shorthand.** The top-level `range(start, end[, step])`
  builtin returns a `list<int>` directly, inclusive of both endpoints.
  `Range` gains a `.toList()` method for symmetry with the literal form
  `(1..5).toList()`. The char-range literal `'a'..'z'` now produces a
  `list<string>` of single-character entries eagerly, so
  `let list<string> letters = 'a'..'e'` works without an intermediate
  conversion. `list.toList()` is a no-op pass-through so the same
  `.toList()` call works whether you have a `Range`, a `Set`, or
  already a list.
- **Tagged generic collections.** `list<T>`, `dict<K,V>`, and `set<T>`
  values flowing through a typed declaration or parameter boundary
  now carry their declared element types as a reified tag.
  `reflect.typeBindings(xs)` on a `list<int>` returns `{"T": "int"}`;
  untagged collections (raw literals not bound to a typed name)
  return `{}` rather than erroring. The "not preserved at runtime"
  caveat in chapter 3 is gone.
- **`instanceof <TypeRef>`.** The right operand of `instanceof` is
  now a full type reference - `xs instanceof list<int>`,
  `d instanceof dict<string, User>`, `x instanceof ?int` all parse
  and dispatch. Tagged collections compare bindings invariantly
  (same rule as 1.0.1 user-class generics); untagged collections fall
  back to a structural walk over their elements.
- **Reflection harmonised across backends and primitives.** The
  reflect API now produces the same shape on the evaluator and the
  VM, regardless of whether you pass a class value, a class instance,
  or a name string. `reflect.class("Foo")` resolves a class declared
  in another loaded module via the module loader.
  `reflect.methods(value)` accepts an instance or a primitive
  (`reflect.methods([1, 2, 3])` returns the list method names,
  `reflect.methods("hi")` returns the string method names).
  `reflect.fields(class)` now returns a list of
  `{name, type, nullable, hasDefault}` dicts (was a list of name
  strings) - the type info was previously discarded. Cross-module
  `instanceof Parent`, `e as Parent`, and `catch (Parent e)` all
  walk the full parent chain (error-derived classes capture their
  parents at construction so the chain is reachable even when the
  catch site is in a different chunk).
- **`json.parseAs(text, ClassWithoutCtor)` data-class shape.**
  Classes without a constructor now have their fields populated
  directly from the parsed dict (previously they were instantiated
  with empty fields). Matches the canonical "data class" usage.
- **Numeric `//` and `as int` close cross-type gaps.** The floor-
  division operator `//` now accepts `decimal // decimal` and
  `float // float` in addition to `int // int`. Result type
  matches the operands (same-kind policy): `7 // 2` is `3`,
  `7.5 // 2.0` is the decimal `3.0000000000`, `5.5 // 2.0` (float)
  is `2.0`. The companion `%` is a floor-modulo on all three
  numeric types, so the sign of the remainder follows the divisor
  (`-7 % 2 == 1`, `-7.5 % 2.0 == "0.5000000000"`). The `as int`
  cast now truncates toward zero from `decimal` and `float` (e.g.
  `2.7 as int == 2`, `-2.7 as int == -2`) and accepts `bool`
  (`true as int == 1`). Previously `decimal as int` rejected any
  non-integer-valued operand and `bool as int` was not supported.
  New parity tests `TestParityFloorDivOnDecimalAndFloat` and
  `TestParityCastTruncatesDecimalAndFloat`; new language test
  `tests/core/floor_div_and_cast_test.gb`.
- **Cast failures are catchable on both backends.** A failed
  `x as Y` (e.g. `"hi" as bytes`) used to escape the VM as an
  uncatchable `bytecode runtime error: cannot cast ...`, even
  inside a surrounding `try / catch (RuntimeError e)`. The
  evaluator already raised a catchable `RuntimeError`. The VM
  now throws the same catchable `RuntimeError` via the typed-
  throw path, so frameworks and user code can defensively
  `try` a cast on both backends. New parity test
  `TestParityCastErrorIsCatchable`; new test case
  `castFailureIsCatchable` in `tests/errors/try_catch_test.gb`.
- **`getMessage()` and `getClass()` on built-in errors.** Built-
  in error values previously only exposed the `.message` and
  `.class` *fields*. The Java / PHP / Python idiom -
  `e.getMessage()`, `e.getClass()` - errored with "X has no
  method getMessage". Both accessors are now methods on every
  `Error`-derived class, including user-defined subclasses
  (they dispatch through the same built-in path). The fields
  still work; choose either form. New parity test
  `TestParityErrorGetMessageAndGetClass`; new test cases
  `getMessageAndGetClassOnBuiltin` and
  `getMessageOnUserDerivedError` in
  `tests/errors/try_catch_test.gb`.
- **Cross-type casts: `string <-> bytes` and `list <-> set`.**
  `"hello" as bytes` encodes UTF-8; a `bytes` value
  `as string` decodes UTF-8 (the cast raises a catchable
  `RuntimeError` if the byte sequence isn't valid UTF-8).
  `[1, 1, 2, 3] as set<int>` deduplicates (first occurrence
  wins); `{1, 2, 3} as list<int>` materializes in unspecified
  order. Previously each of these raised "cannot cast X to Y".
  The element-type generic argument is required for the
  collection casts to match the typed-declaration rules.
  New parity test
  `TestParityCrossTypeCastsForBytesAndCollections`; new test
  classes `StringBytesCastTest` and `ListSetCastTest` in
  `tests/core/cross_type_casts_test.gb`.
- **New `string` module: factory and static helpers.** A small
  namespace for things that don't fit as instance methods on a
  string value:
  - `string.fromCodePoint(n)` -> single-character string for
    Unicode codepoint `n`. Rejects negative values, codepoints
    above U+10FFFF, and the UTF-16 surrogate range
    (U+D800..U+DFFF). Counterpart to the existing
    `.codePointAt(i)` instance method.
  - `string.fromCodePoints(list<int>)` -> multi-character
    string built from a list of codepoints, same validation
    per element.
  - `string.compare(a, b)` -> three-way comparison returning
    -1 / 0 / +1. Useful as a sort key
    (`xs.sortBy(string.compare)`). Java / Go convention.
  - `string.equalsFold(a, b)` -> case-insensitive equality
    respecting Unicode case folding (`"CafÉ" == "café"`).
  For timing-attack-safe equality (HMAC verification, token
  comparison) use `secrets.constantTimeEqual` instead; the
  string-module helpers are not constant-time.
  New parity test `TestParityStringModule`; new test class
  `StringModuleTest` in `tests/core/cross_type_casts_test.gb`.
- **`null as ?T` is a successful cast on both backends.** Casting
  a `null` value to a nullable type previously errored with
  "cannot cast null to T" on the evaluator (the cast path
  dropped the nullable bit from the target TypeRef before calling
  castValue). The VM accepted it after the 1.0.2 cast-error
  catchability work but the eval side still failed. The
  evaluator's CastExpression handler now special-cases a
  nullable target ahead of the class-chain match, mirroring the
  VM. New parity test `TestParityNullAsNullableType`; new test
  class `NullableCastTest` in
  `tests/core/cross_type_casts_test.gb`.
- **Method-dispatch hot path: name-lowering and classInfo lookup
  caches.** Two small VM-side memoising caches reduce per-call
  overhead on tight method-call loops. `nameLowerCache` skips
  the repeated `strings.ToLower(methodName)` on every dispatch
  (the chunk's `ClassInfo.Methods` is keyed lowercase, so the
  lowered form is what the lookup actually needs).
  `classInfoNameCache` skips the `vm.classIndex` lookup when
  the receiver's `instance.Class.Name` has already been
  resolved. Measurable on the `class_dispatch` extended
  benchmark (~12% improvement, 42ms -> 37ms median for 50000
  calls); numeric / list / string benchmarks unchanged.

### Bug fixes

- **VM closure capture for camelCase identifiers.** The compiler's
  free-variable scanner lowercased identifier names while local
  scope entries kept their original case, so closures that captured
  a variable with uppercase letters silently missed the capture.
  The closure body then read the wrong stack slot at runtime,
  producing puzzling type errors. Both `freeVarSet` and the
  enclosing scope are now case-sensitive throughout. New parity
  test `TestParityClosureCaptureCamelCase`.
- **`null` matches `any` in VM method overload resolution.** The
  VM rejected `obj.send(null)` when `send` was declared as
  `func send(any body)` - the early null-vs-nullable check fired
  before the `vmTypeAny` short-circuit, so the overload selector
  reported "no matching overload". Evaluator was already correct.
  New parity test `TestParityNullMatchesAnyParam`.
- **Cross-module typed-parameter dispatch.** A function declared
  with a parameter typed as a module-qualified class (e.g.
  `func f(appmod.GebwebApp app)`) failed at the dispatch boundary
  because the runtime kept the qualified name. The strip-prefix
  path now applies uniformly so a stdlib facade can declare
  `appmod.GebwebApp` parameters and accept values built by the
  same module.
- **`reflect.class(name)` finds user classes from stdlib modules.**
  When called from inside an imported module (e.g.
  `gebweb.binding`), `reflect.class("UserDTO")` previously
  returned null because the module loader only scanned imported
  modules' chunks. The loader now also scans the main chunk, so
  framework code can resolve user-script classes.
- **Cross-chunk deserialize.** `json.parseAs(text, UserDTO)` from
  inside a stdlib module crashed with "class index out of range"
  because the VM tried to resolve the user class's index against
  the wrong chunk. The deserialize path now dispatches via the
  module loader to a sub-VM bound to the right chunk, so the
  framework can deserialize main-chunk DTOs from binding helpers.
- **Cross-VM exception propagation.** A throw originating in user
  code that bubbled across a sub-VM boundary collapsed to a plain
  "uncaught RuntimeError" string at the boundary, losing the
  original class and parent chain. `catch (errors.HttpException e)`
  in a stdlib closure no longer matched the original
  `NotFoundError`. The VM now wraps the underlying `runtime.Error`
  in a `vmThrownError` so the calling VM can recover it via
  `errors.As` and re-throw it as a typed `pendingThrow`. New
  parity test `TestParityCrossModuleThrowCatch`.
- **`as` widens to a parent class on both backends.** The cast
  operator previously rejected widening an error- or instance-
  derived value to a declared parent (`e as errors.HttpException`
  for a `NotFoundError`). Both backends now walk the parent chain
  the same way `instanceof` does and treat the cast as a no-op
  when the value is already an instance of the target.
- **Eval `reflect.method(...)()` preserves module access.** A
  bound method returned by `reflect.method` ran on a fresh stub
  Evaluator with no module loader, so the method body couldn't
  reference any imported module (`gebweb.notFound(...)`,
  `json.stringify(...)`, ...). The bound Native closure now
  captures the live host Evaluator. New parity test
  `TestParityReflectMethodPreservesModuleAccess`.
- **Parenthesized selector forces value-then-call semantics.**
  `(obj.fn)(args)` previously parsed identically to `obj.fn(args)`
  and dispatched as a method call on `obj`, ignoring the parens.
  The parser now flags the SelectorExpression so the evaluator and
  the VM both invoke the VALUE of `obj.fn` (a closure stored in a
  field, a method reference, anything callable) instead. Required
  for caching callables on instance fields - `let response =
  (this.dispatch)(request);`. New parity test
  `TestParityParenthesizedSelectorInvokesValue`.
- **`reflect.className(value)`.** New reflect builtin returning
  the class's own identifier. For a class value, returns its name
  (`reflect.className(User)` returns `"User"`); for an instance,
  same as `reflect.typeOf(instance)`; for primitives, the runtime
  type name (`"int"`, `"string"`). Symmetric with `reflect.class(name)`
  going the other way. Required by gebweb's DI container to
  identify a service by its class without instantiating it. New
  parity test `TestParityReflectClassName`.
- **Class-ref runtime construction via `classRef()`.** A class
  value passed through a variable or obtained from `reflect.class`
  is now callable to construct an instance - previously the VM
  treated `classRef()` as a static-method lookup of `__invoke` and
  errored with "unknown static method ... __invoke". Both backends
  now construct the class (routing through the moduleLoader when
  the class was declared in a different chunk). Required by
  `gebweb.app([HelloController])` to instantiate user controllers
  via DI. New parity test `TestParityClassRefRuntimeConstruction`.
- **Cross-chunk `reflect.constructors`.** When given a class value
  declared in another chunk, the VM now dispatches through a new
  `ConstructorsForModuleClass` loader hook so the metadata
  reflects the originating chunk's constructor list rather than
  the caller chunk's stale view. Same pattern as the deserialize
  and construct module-class hooks.
- **Dotted decorator names.** `@Foo.bar`, `@Foo.bar.baz` and
  longer chains parse as a single composite identifier. The whole
  dotted string is the decorator's name; dispatch is by exact
  string match. Lets framework-style families like
  `@Assert.email`, `@Assert.minLength(2)` group related rules
  under a common prefix without naming-collision worries.
- **Field-level decorators.** `@`-prefixed annotations on field
  declarations inside a class body now parse and persist into
  `reflect.fields(class)` as a per-field `decorators` list. Field
  decorators are pure metadata - the runtime never executes them
  automatically; frameworks read them via reflection to drive
  validation, serialisation filters, OpenAPI enrichment, etc.
  See *Classes And Interfaces > Decorators* in the manual for the
  semantics. New parity test
  `TestParityFieldDecoratorsAndDottedNames`.
- **Chunk format Version 49 to 50.** New per-field decorator-
  metadata list parallel to `FieldNames` so reflection over
  cross-chunk classes returns the field annotations from the
  declaring chunk.
- **Bare `return;` in a `void` function.** The static analyzer
  previously rejected `return;` inside a function declared as
  returning `void`, raising "cannot return null from F returning
  void". An early exit from a void body is legal - there is no
  value being returned, only an early termination. Both the
  evaluator and the VM already handled this at runtime; only the
  analyzer needed adjusting. New parity test
  `TestParityBareReturnInVoidFunction`.
- **Trailing comma in list literals.** `[1, 2, 3,]` and multi-line
  variants now parse. Dict / set literals already supported it;
  list literals catch up. New parity test
  `TestParityTrailingCommaInListLiteral`.
- **Class declaration inside a block is rejected at parse time.**
  Previously `class X {...}` inside a method body parsed but
  produced confusing downstream failures. Now emits a clear
  "class declaration is only allowed at the top level" error.
  Same for `interface` and `enum`.
- **Lexical shadowing wins over module dispatch.** When a local
  variable shadows a built-in module name (e.g.
  `let errors = [...]` while the `errors` module is loaded), the
  receiver of an `X.method(...)` call now resolves to the local
  rather than the module. Eliminates a class of confusing
  "X is not a module" runtime errors. Both backends. The analyzer
  also emits a *warning* (not an error) at the shadowing
  declaration so a reader doesn't have to wonder why a familiar
  module name is bound to a list. New parity test
  `TestParityLocalShadowsBuiltinModule`.
- **`reflect.getField(instance, name)` and `reflect.setField(instance,
  name, value)`** native builtins. Dynamic field-by-name access /
  assignment for framework code (Gebweb's `@Assert` validator
  walks an instance's fields by name; the `@ApiResource` PATCH
  handler updates an entity field-by-field). Replaces the previous
  `json.parse(json.stringify(instance))` round-trip workaround.
  Both backends. New parity test `TestParityReflectGetFieldSetField`.
- **Forward function references in the bytecode compiler.**
  Previously `func a() { return b(); } func b() { ... }` failed
  with "no matching overload for b" because `b`'s parameter / return
  metadata wasn't populated until its body was compiled. The
  compiler now pre-populates function signatures during the
  initial sweep so call sites see the real shapes. New parity test
  `TestParityForwardFunctionReferences`.
- **Cross-chunk `reflect.fields(instance)` preserves field
  decorators.** Previously `reflect.fields(instance)` on a value
  handed to a sub-module returned no decorator info because the
  originating chunk's ClassInfo wasn't reachable. The instance's
  `runtime.Class.Fields` is now populated at construction time
  with each field's name and decorator metadata so framework
  code in another module sees the same annotations the
  declaring module does. The chunk-local path still wins when
  available (full type strings); the new path is the fallback
  for cross-chunk reflection. New parity test
  `TestParityCrossChunkInstanceFields`.

### Bytecode

- Chunk format Version 48 -> 49. New per-field type strings
  parallel to `FieldNames` so cross-chunk reflection on classes
  produces the same shape as the evaluator without consulting the
  source AST.

### Other

- Build: still Go 1.26.3.
- VS Code extension: bumped to 1.0.2 for parity.

## 1.0.1 (2026-05)

A correctness release that closes two related holes in the 1.0 generics
story.

### Highlights

- **Generic invariance** - User-defined generic class types are now
  *invariant* in their type parameters. Even when `Sub extends Base`,
  a `Box<Sub>` is not assignable to a `Box<Base>` parameter or typed
  variable. The static analyzer rejects the assignment at compile
  time; the runtime rejects it at the function-parameter boundary
  when the value's reified bindings disagree with the declared
  bindings. This is the standard invariance rule that
  Kotlin/Java/C# enforce, and it eliminates the classic unsoundness
  where a function widens `Box<Sub>` to `Box<Base>` and then inserts
  a sibling subtype. See chapter 3's *Generics: invariance*.
- **Explicit type-argument call syntax** - `ClassName<T>(args)` and
  `funcName<T>(args)` now parse correctly. Before 1.0.1 the parser
  treated `<` and `>` as comparison operators in these positions, so
  `Box<int>()` failed with a syntax error and `assertIs<string>("x")`
  silently compiled into a chained comparison that exploded at
  runtime. The new lookahead disambiguates: an identifier followed by
  `<TypeRef (, TypeRef)*>` immediately followed by `(` is a generic
  call; everything else stays a comparison. This is the form needed
  to write the invariance check above: `Box<Sub> b = Box<Sub>();`

### Tooling

- VS Code extension bumped to 1.0.1.

### Build

- Documentation now references the Go 1.26.3 toolchain (matching
  `go.mod`) as the minimum supported build environment.

## 1.0.0 (2026-05)

The first stable release. Everything documented in this manual is in scope
for the 1.0 stability promise: source-level syntax, stdlib APIs, runtime
semantics, and the bytecode chunk format. Future 1.x releases will add
features but not break what is below.

### Highlights

- **Destructors and context managers (two separate concerns)** -
  `func ~ClassName()` declares an end-of-lifetime hook. Destructors
  fire at program exit (the runtime sweeps every destructor-bearing
  instance that hasn't already been destroyed, in reverse-creation
  order) or via an explicit `del x;` statement, which retires the
  binding and invokes the destructor inline. `del` accepts an
  identifier only; the semantic analyzer flags any reference to a
  destroyed binding with `use of destroyed binding "x"` so the type
  system stays sound. The unrelated `with (resource) { ... }` block
  is the context-manager construct: it calls `__enter__()` on entry
  and `__exit__()` on exit (any exit path - normal, exception,
  return, break, continue) but **does not** invoke the destructor.
  See chapter 6's *Destructors* and *Context Managers* sections.
- **Class (de)serialisation** - `json.stringify(instance)` (and the
  YAML / TOML equivalents) accept user-defined class instances and
  dump their public fields by default (`_` / `__` prefixed names are
  private and skipped). Classes can override with `__serialize__()`
  (any returned dict / list / scalar is recursively serialised). The
  symmetric `json.parseAs(text, ClassRef)` reconstructs an instance,
  preferring a static `__deserialize__(dict)` factory when defined and
  falling back to positional constructor calls matched on parameter
  names. Same for `yaml.parseAs`, `toml.parseAs`, `xml.parseAs`. See
  chapter 6's *Serialisation*.
- **Async combinators** - `async.all([tasks])`, `async.race([tasks])`,
  `async.timeout(task, ms)`, and `async.cancel(task)` (also reachable
  via `task.cancel()` and the `task.cancelled` property). The pre-1.0
  "cancellation and structured scheduling remain roadmap items" caveat
  is gone. See chapter 9.
- **Scheduling primitives** - `time.scheduler.Timer`,
  `time.scheduler.Ticker`, and `time.scheduler.Interval` give callback-
  style scheduling with cancellation. Use `setTimeout` / `setInterval`
  aliases if you prefer the JavaScript naming.
- **Symmetric encryption** - `crypt.aesEncrypt` /
  `crypt.aesDecrypt` (AES-256-GCM) and `crypt.chacha20Encrypt` /
  `crypt.chacha20Decrypt` (XChaCha20-Poly1305) join the existing
  hash / HMAC / Argon2id / RSA / EC stack. See `stdlib/12-security.md`.
- **Reusable HTTP clients** - `http.newClient({...})` now accepts
  `cookieJar`, `keepAlive`, `maxIdleConns`, `proxy`, and
  `proxyFromEnv` options for production-shape HTTP usage. Session
  flows that need to retain `Set-Cookie` across requests just pass
  `"cookieJar": http.newCookieJar()`. Default User-Agent is now
  `Geblang/1.0` (override via `headers`).
- **Improved regex API** - `re.match` returns a clean `{text, groups,
  named}` dict instead of ad-hoc numeric-string keys, and `re.matchAll`
  iterates every non-overlapping match. See `stdlib/09-text.md`.
- **Wider encoding coverage** - `encoding.base32Encode` /
  `base32Decode` (RFC 4648, padded or unpadded) and
  `encoding.base58Encode` / `base58Decode` (Bitcoin / IPFS alphabet,
  preserves leading zeros). Accepts both string and bytes inputs.
- **`.length` everywhere** - `list`, `dict`, `set`, `string`, `bytes`,
  and `range` all expose `.length` as a property (alongside the
  existing `.length()` method).
- **Inherited generic type bindings** - `class Sub extends Base<string>`
  now propagates `T to string` to subclass instances, visible via
  `reflect.typeBindings(instance)["T"]`.
- **`func` as a field type** - `class Holder { func cb; ... }` parses
  correctly. `callable` and `function` work too.
- **Module top-level discipline** - files that begin with `module name;`
  now require declarative top-level statements only (`import`, `export`,
  `const` / `let` / typed declarations, `func`, `class`, `interface`,
  `enum`, `type` alias, and at most one `init { ... }` block).
  Free-standing calls, `if`/`while`/`for`/`match`/`try`, and bare
  assignments are rejected with a clear diagnostic; the rule does not
  apply to script files. See `docs/user/07-modules-packages.md`.
- **Static errors abort `geblang run`** - the previous behaviour
  printed `warning: ...` for static-analysis errors caught by the
  bytecode compiler (e.g. no matching overload, type mismatch),
  fell back to the evaluator, and crashed partway through the run.
  Now those errors abort the run cleanly before any statement is
  executed. Genuine compiler gaps (the bytecode compiler doesn't yet
  support a feature) still fall back silently. `geblang check`
  output is the source of truth for what blocks `geblang run`.
- **Diagnostic severity** - `semantic.Diagnostic` now carries
  `Severity` (Error / Warning). All existing checks remain at Error
  by default; the field is in place so future analyzer passes can
  emit Warning-level findings that surface in VS Code's Problems
  panel and in `geblang check` but don't block execution.
- **Aliased native imports compile on the VM** - the bytecode
  compiler now recognises `import path as natpath; natpath.clean(...)`
  and dispatches to the canonical `path.clean(...)` directly. Previously
  these calls failed to compile with `unknown bytecode name natpath`
  and fell back silently to the evaluator; under `--vm-strict` they
  were rejected outright. `stdlib/pathlib.gb` and
  `stdlib/schema/validator.gb` now also run on the VM rather than
  via fallback. Unknown-identifier failures from the bytecode
  compiler are no longer treated as parity gaps and abort the run
  along with the other static errors.
- **Autocomplete for primitive-type methods** - typing
  `someDecimal.<TAB>` after `decimal d = 3.14;` now surfaces
  `format`, `abs`, `toString`, etc. The LSP server's completion
  path detects the receiver type via a light lexical scan
  (`<primitiveType> <name>` declarations) and looks up the methods
  in a per-type table. Covers `string`, `int`, `float`, `decimal`,
  `bool`, `bytes`, `list`, `dict`, `set`, and `range`. Inferred
  declarations (`let x = ...`) and complex annotations (`?string`,
  `list<int>`) are best-effort; the catalog can be extended later
  if needed.
- **DAP launch pre-flight** - VS Code's *Run Without Debugging*
  (Ctrl+F5) and *Debug* launches both went through the DAP server's
  evaluator-only path, which skipped the static-analysis pre-flight
  the CLI run path performs. A script with a static error
  (no-matching-overload, type mismatch, undeclared identifier,
  module-top-level violation) would run partway through before
  crashing in the Debug Console, instead of aborting cleanly. The
  DAP server now runs the same semantic + bytecode pre-checks as
  `geblang run`; static errors abort the launch and surface the
  diagnostic in the Debug Console. `bytecode.IsParityError` is
  exported so cmd/geblang and internal/dap share the same
  parity-gap classifier.

### Toolchain

- **Go 1.26.3 build** - Docker image and `go.mod` now target Go 1.26.3.
  Dependency versions refreshed (`pgx` v5.5 to v5.9, `sqlite` v1.29 to
  v1.50, `golang.org/x/crypto` v0.17 to v0.51). `scripts/upgrade-go.sh`
  installs the matching toolchain on Ubuntu/WSL.
- **Vet-clean** - audited and fixed 181 printf-style call sites that
  Go 1.26's promoted vet check flagged in parser / semantic / vm.
- **Cleaner build output** - `make docker-build` / `make vscode-build`
  no longer print spurious "Error response from daemon: No such
  container" lines on first runs.

### Performance

Geblang's bytecode VM remains competitive with the reference
interpreters. Measured on this build (median of 7 runs; lower is better):

| Benchmark       | Geblang | Python  | PHP     |
|-----------------|---------|---------|---------|
| numeric_loop    | 122 ms  | 135 ms  | 29 ms   |
| recursive_fib   | 83 ms   | 46 ms   | 22 ms   |
| list_pipeline   | 7 ms    | 14 ms   | 11 ms   |

The 1.0 round of work was deliberately feature-focused; the post-Phase-O
performance baseline holds. See `benchmarks/run.py` for the harness.

### Bug fixes worth flagging

- **VM `??` infinite loop**: `value ?? default` inside an async-run
  callback or HTTP handler used to enter an infinite loop because
  `OpNullCoalesce` was missing from the bytecode VM's jump-shift
  list. Same fix for `?.` (`OpOptionalChain`). Fixed; both paths now
  have parity tests.

### Migration from 0.9.x to 1.0

If you have code written against a pre-release Geblang build, two changes
need source updates:

1. **`re.match` result shape**

   ```diff
   - let m = re.match("(?P<name>\\w+):(\\d+)", text);
   - io.println(m["0"]);     // full match (string key "0")
   - io.println(m["1"]);     // group 1
   - io.println(m["name"]);  // named group

   + let m = re.match("(?P<name>\\w+):(\\d+)", text);
   + io.println(m["text"]);          // full match
   + io.println(m["groups"][1]);     // group 1
   + io.println(m["named"]["name"]); // named group
   ```

   `re.findAll` was unchanged. The new `re.matchAll` returns a list of
   the new-shape dicts for iteration.

2. **Default User-Agent**

   Outgoing HTTP requests used to inherit Go's `Go-http-client/1.1`.
   They now send `Geblang/1.0`. If a server allow-lists by User-Agent,
   update its rules or pass `headers: {"User-Agent": "..."}`.

Everything else - syntax, control flow, classes, async, generators,
stdlib calls, decorators, error model - is source-compatible with the
last pre-1.0 build.

### Bytecode chunk format

Chunk `Version` bumped to 48 (was 46) to accommodate the new
`ParentArguments` slot and the `DestructorIndex` slot on `ClassInfo`.
Compiled `.gbc` artefacts from 0.9.x will not load on 1.0; rebuild
them with `geblang` 1.0 or run `geblang cache clean`.
