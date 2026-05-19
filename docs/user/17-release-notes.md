# Release Notes

## 1.0.3 (unreleased)

Small parity / ergonomics fixes uncovered while extending Gebweb.

### Bug fixes

- **`geblang check` accepts `import string;`.** The CLI's
  native-module allowlist was missing the `string` module, so files
  using `string.fromCodePoint(...)` / `string.compare(...)` failed
  the check pass with `cannot resolve import string` even though
  both backends registered the module.

- **Cross-module `implements` compiles to bytecode.** A class
  declaring `implements mod.Interface<T>` (e.g.
  `implements repository.Repository<Widget>`) previously errored
  with `bytecode compiler interface ... is not declared` and fell
  back to the evaluator. The compiler now accepts dotted interface
  refs under `implements` clauses and interface `extends` clauses,
  mirroring the existing parent-class case; the VM strips module
  prefixes when matching stored interface names against `instanceof`
  queries. New parity test `TestParityCrossModuleImplements`; new
  language test `tests/classes/cross_module_interface_test.gb`.

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
- **Chunk format Version 49 → 50.** New per-field decorator-
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
  now propagates `T → string` to subclass instances, visible via
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
  Dependency versions refreshed (`pgx` v5.5 → v5.9, `sqlite` v1.29 →
  v1.50, `golang.org/x/crypto` v0.17 → v0.51). `scripts/upgrade-go.sh`
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
