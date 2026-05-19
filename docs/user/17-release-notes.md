# Release Notes

## 1.0.2 (2026-05)

A quality-of-life release.

- Top-level `range(start, end[, step])` builtin returning an
  inclusive `list<int>`. `Range.toList()` and `list.toList()` round
  out the conversion options for symmetry.
- Char-range literal: `'a'..'z'` evaluates to a `list<string>` of
  single-character entries.
- `list<T>` / `dict<K,V>` / `set<T>` values flowing through a typed
  declaration or parameter boundary carry reified bindings.
  `reflect.typeBindings(coll)` reads them; `instanceof list<int>`
  and friends now parse and dispatch (invariant for tagged values,
  structural element walk for untagged).
- REPL: `>` and `>>` now end statements for ASI, so
  `xs instanceof list<int>` and `Box<int>()` don't need a trailing
  semicolon.
- Build: new `make build-with-path INSTALL_DIR=/path` target.
- Bug fix: VM closures whose captured names contained uppercase
  letters (`pathParamNames`, `userId`, ...) silently missed the
  capture and read the wrong slot at runtime. The compiler's
  free-variable scanner now matches the case-sensitive resolution
  used everywhere else.
- Cross-module class identity: `instanceof mod.Class`,
  `e as mod.Class`, and `catch (mod.Class e)` walk the parent chain
  across modules. Error-derived classes capture their parent chain
  at construction so cross-module catch still works.
- Reflect API harmonised across backends and made primitive-aware:
  `reflect.class("Name")` resolves a class from any loaded module;
  `reflect.methods`, `reflect.fields`, `reflect.parent`,
  `reflect.interfaces` all accept a class or an instance;
  `reflect.methods` over `list` / `dict` / `set` / `string` / `bytes`
  / `range` returns the built-in method surface.
  `reflect.fields(class)` returns a list of
  `{name, type, nullable, hasDefault}` dicts (the type info was
  previously discarded).
- `json.parseAs(text, DataClass)`: classes without an explicit
  constructor have their fields populated from the parsed dict.
- Bytecode chunk format: Version 48 → 49 (added per-field type
  strings on `ClassInfo` for cross-chunk reflect).
- VM bug fix: `null` argument now matches an `any`-typed
  parameter in method overload resolution.
- VM bug fix: `reflect.class(string)` from inside a stdlib
  module also scans the main chunk so framework helpers can
  resolve user-script classes.
- VM bug fix: `json.parseAs(text, UserDTO)` from a stdlib
  module dispatches deserialization to a sub-VM bound to the
  right chunk (no more "class index out of range").
- VM bug fix: throws crossing a sub-VM boundary now retain
  their class + parent chain, so a stdlib
  `catch (errors.HttpException e)` matches a user-thrown
  `NotFoundError`.
- Both backends: `as <ClassName>` widens to a declared parent
  class. Previously a cast required an exact type-name match.
- Evaluator bug fix: a method invoked via `reflect.method(...)()`
  preserves access to imported modules. Previously the bound
  Native ran on a stub Evaluator with no module loader.
- Parser fix: `(obj.fn)(args)` invokes the value of `obj.fn`
  rather than dispatching as a method call on `obj`. Required
  for callable fields on instances.
- New `reflect.className(value)` builtin returns the class's own
  identifier from a class value or an instance (and the runtime
  type name for primitives). Symmetric with `reflect.class(name)`.
- VM fix: `classRef()` via a variable or `reflect.class` lookup
  now constructs an instance instead of trying to dispatch a
  static method named `__invoke`. Routes through the module
  loader for classes declared in other chunks.
- VM + module loader: new `ConstructorsForModuleClass` hook so
  `reflect.constructors(cls)` works across chunk boundaries.
- Dotted decorator names: `@Foo.bar`, `@Foo.bar.baz` parse as a
  single composite identifier. Useful for framework annotation
  families (`@Assert.email`, `@Assert.minLength(2)`).
- Field-level decorators: `@`-prefixed annotations on class
  fields parse and persist into `reflect.fields(cls)`. Pure
  metadata; never auto-executed.
- Chunk format Version 49 → 50 (added per-field decorator
  metadata).
- Analyzer: bare `return;` in a `void`-returning function is
  legal again (was rejected as "cannot return null"). Early-exit
  pattern restored.
- Parser: trailing commas in list literals; nested class
  declarations rejected at parse time.
- Resolution: a local variable shadowing a built-in module name
  takes precedence at `.method(...)` call sites. Eliminates
  "X is not a module" confusion. Analyzer warning at the
  shadowing point.
- Reflection: new `reflect.getField` / `reflect.setField` for
  dynamic field-by-name access on instances.
- Compiler: forward function references in the same file now
  resolve correctly. Signatures populated during the pre-pass.
- Cross-chunk `reflect.fields(instance)` returns the originating
  class's field decorators - the `runtime.Class.Fields` slice is
  populated at instance construction so frameworks in other
  modules see the same annotations the declaring module does.
- Numeric operators close cross-type gaps. `//` is now defined on
  `decimal // decimal` and `float // float` in addition to
  `int // int`, with same-kind result: `7.5 // 2.0` is the decimal
  `3.0000000000`. The companion `%` is a floor-modulo on all three
  types - the sign of the remainder follows the divisor. `as int`
  truncates toward zero from `decimal` and `float` (was rejected
  for non-integer-valued operands) and accepts `bool`
  (`true as int == 1`).
- VM bug fix: a failed `x as Y` is now a catchable `RuntimeError`
  on both backends (was an uncatchable "bytecode runtime error" on
  the VM, even inside a surrounding `try / catch`).
- Built-in errors now expose `e.getMessage()` and `e.getClass()`
  methods alongside the existing `.message` / `.class` fields,
  matching the Java / PHP / Python convention. User-defined error
  subclasses inherit both accessors through the built-in dispatch.
- Cross-type casts:
  - `string as bytes` encodes UTF-8; `bytes as string` decodes
    UTF-8 (catchable `RuntimeError` if not valid).
  - `list as set<T>` deduplicates (first occurrence wins).
  - `set as list<T>` materializes in unspecified order.
- New `string` module: a small namespace for static / factory
  helpers that don't fit as instance methods.
  - `string.fromCodePoint(n)` -> single-character string for
    a Unicode codepoint; rejects surrogates and out-of-range.
  - `string.fromCodePoints(list<int>)` -> multi-character
    string from a codepoint list.
  - `string.compare(a, b)` -> three-way comparison
    (-1 / 0 / +1) suitable as a sort key.
  - `string.equalsFold(a, b)` -> Unicode case-insensitive
    equality.
  For timing-attack-safe comparison see
  `secrets.constantTimeEqual` - the string-module helpers are
  not constant-time.

## 1.0.1 (2026-05)

A correctness release for the generics surface.

- Generic class types are now invariant in their type parameters:
  `Box<Sub>` is not assignable to `Box<Base>` even when `Sub extends Base`.
- Explicit type arguments at call sites: `Box<int>()` and
  `assertIs<string>(x)` now parse and bind T.
- On a generic function call, an explicit `<T>` replaces T in every
  position of the signature - parameters, return type, and body.
- Invariance error messages include the offending value's reified
  bindings: `got Container<Sub>` rather than the bare `got Container`.
- Build: Go 1.26.3 minimum.

## 1.0.0 (2026-05)

The first stable release. Everything documented in this manual is in scope
for the 1.0 stability promise: source-level syntax, stdlib APIs, runtime
semantics and the bytecode chunk format. Future 1.x releases will add
features but not break backwards compatibility.


