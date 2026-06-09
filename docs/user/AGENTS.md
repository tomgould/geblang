# AGENTS.md: Geblang for AI agents

Dense cheatsheet for AI coding agents writing Geblang. Read once
at the start of a session.

## What Geblang is

A statically-typed scripting language for backend services,
implemented in Go. Files end in `.gb`. Two backends share the
language: an evaluator (`geblang test`) and a bytecode VM
(`geblang run`, `geblang build`). They must produce identical
output; report a divergence as a bug rather than working around
it.

## Project layout

A package has a `geblang.yaml` manifest:

```yaml
name: myapp
version: 0.1.0
source: src
paths: []
dependencies:
  somelib:
    path: ../somelib
```

Source files live under `src/` (whatever `source` points at).
Tests are `*_test.gb` files next to source.

```
geblang run main.gb              # run a script
geblang test tests/              # run *_test.gb under a path
geblang check src/               # static check (no execution)
geblang fmt src/                 # format in place
geblang doc <module>             # print module signatures
geblang build --entry main.gb --out app
```

## Syntax

### Comments

```gb
# line comment
io.println(x);  # trailing line comment
/* block comment
   on multiple lines */
```

`//` is integer division, not a comment. Never use `//` for
comments anywhere.

### Imports

```gb
import io;
import json;
import datetime as dt;          # alias
```

Imports go at file top. Stdlib modules don't need a path. Local
modules resolve through the manifest's `paths` or
`dependencies`.

### Variables

```gb
let x = 1;                   # inferred (int)
int  y = 2;                  # explicit
?string maybe = null;        # nullable
const PI = 3.14;             # immutable; PI is decimal
```

`let x = 1.0` infers `decimal`, not `float`. Use `1.0 as float`
when you specifically want the float type. `decimal` is the
default for floating-point literals because money and precise
arithmetic are more common than fast IEEE math.

### Primitive types

`int`, `float`, `decimal`, `string`, `bool`, `bytes`, `any`,
`null`. Integers are arbitrary precision. Strings are UTF-8 and
interpolate inside double quotes:

```gb
let name = "Ada";
io.println("hello ${name}");    # interp
io.println('hello ${name}');    # literal (single quotes are raw)
```

### Collections

```gb
list<int>          xs = [1, 2, 3];
dict<string, int>  d  = {"a": 1, "b": 2};
set<string>        s  = {"x", "y"} as set<string>;
```

- **List mutators work in place** (1.16.0). `xs.push(x)` appends to
  `xs` and returns it, so pushes chain; no reassignment needed.
- **Dicts iterate in insertion order**. The same applies to
  `.keys()`, `.values()`, `.items()`, and `for ... in dict`.
- **Sets need an explicit type cast** at construction because
  `{}` defaults to dict.
- `instanceof list<T>` walks elements when untagged, matches the
  tag when the collection came from a typed declaration.
  `list<any>` accepts every list. Union args like
  `list<int|string>` match elementwise.

### Control flow

```gb
if (cond) { } else if (other) { } else { }
for (x in xs) { }
for (k, v in d.items()) { }
for (i in 0..<10) { }
while (cond) { }

match (x) {
    case int n when n > 0 => handlePositive(n);
    case string s         => handleString(s);
    default               => handleOther();
}
```

`match` uses `=>`, guards are `when`, patterns can match by type
and bind. As an expression, each branch ends with `;` and the
closing `}` is followed by `;`. As a statement (no value used),
no trailing `;`.

### Null handling

```gb
?string name = lookup(id);

if (name != null) {
    io.println(name.length());          # narrowed to string inside
}

let display = name ?? "anonymous";      # null coalesce
```

After an `!= null` check the checker narrows the nullable to its
non-null type for the rest of the branch. `??` is null coalesce.

### Functions

```gb
func add(int a, int b): int { return a + b; }

let inc = func(int n): int { return n + 1; };

func greet(string name, string greeting = "Hello"): string {
    return greeting + ", " + name;
}

greet("Ada");                  # default arg works in any position
```

Functions can carry the same name with different signatures
(overloading). Closures capture their environment by reference;
captured variables can be reassigned through the closure.

### Generators

```gb
func counter(): generator<int> {
    yield 1;
    yield 2;
    yield 3;
}

for (n in counter()) { io.println(n); }
```

Return type is `generator<T>`. Use `iterable<T>` for parameters
that accept any iterable (generators, lists, sets, ranges).

### Async

```gb
import async;

async func slow(): int {
    async.sleep(10);
    return 42;
}

let token  = async.run(slow);
let result = async.await(token);
```

`async.all([t1, t2])`, `async.race([...])`, `async.timeout(ms,
fn)`, `async.cancel(token)` compose tasks.

### Classes

```gb
class Counter {
    int value;
    func Counter(int start = 0) { this.value = start; }
    func bump(): int {
        this.value = this.value + 1;
        return this.value;
    }
}

class TaskCounter extends Counter {
    string label;
    func TaskCounter(string label) {
        parent(0);                  # calls parent CONSTRUCTOR
        this.label = label;
    }
    func describe(): string {
        return parent.describe() + ":" + this.label;
    }
}
```

- `parent(args)` calls the parent CONSTRUCTOR; `parent.method(args)`
  calls a parent METHOD. `super` is not a keyword.
- `@abstract` on a class blocks direct instantiation. `@abstract`
  on a method (which still needs a body that may be empty) forces
  subclasses to override.
- Methods named like decorators that resolve in scope (e.g.
  `@upper` where `upper(string): string` exists) run on every
  field assignment. Other decorators are pure metadata.

### Interfaces

```gb
interface Notifier {
    func send(string msg): void;
}

class EmailNotifier implements Notifier {
    func EmailNotifier() {}
    func send(string msg): void { /* ... */ }
}
```

Interfaces may carry default method bodies, default field values,
and `extends` other interfaces. Use `instanceof Notifier` to test
implementation.

### Errors

```gb
try {
    risky();
} catch (RuntimeError e) {
    io.println(e.message);
}

class MyError extends RuntimeError {
    func MyError(string m) { parent(m); }
}

throw MyError("boom");
```

Catches dispatch by class hierarchy. `errors.new(msg)`,
`errors.wrap(inner, msg)`, `errors.is(e, cls)`,
`errors.stackTrace(e)` are the stdlib helpers.

### Reflection

```gb
import reflect;

reflect.typeOf(value);                # Type<list>, Type<int>, etc.
reflect.className(cls);               # "Foo" or null
reflect.parent(cls);                  # parent class name or null
reflect.classes();                    # every loaded class
reflect.interfaces(cls);              # DIRECT interfaces; walk parent for inherited
reflect.constructors(cls);            # list of param dicts
reflect.fields(cls);                  # list of field metadata
reflect.methods(cls);                 # list of method name strings
reflect.decorators(target);           # list of {name, args, namedArgs}
reflect.hasDecorator(target, "Get");
reflect.typeBindings(value);          # {"T": "int"} for tagged collections
```

`reflect.interfaces(cls)` returns the DIRECT `implements` clause
only; walk parents through `reflect.parent` if you need the full
chain.

## Idioms

- **Type cast for access**: `(value as Foo).method()`.
- **Stringify primitives**: `n.toString()` or `(n as string)`.
  Either works on every primitive type. String concat does not
  coerce, so cast or stringify before `+`.
- **Joint dict iteration**: `for (k, v in d.items()) { }`.
- **List filter inline**: `xs.filter(func(int n): bool { return n > 0; })`.
- **Null coalesce**: `let v = maybe ?? fallback;`.
- **Accumulate with push**: `xs.push(item);` mutates in place (1.16.0).
- **Single-token YAML/parameter**: a `"%key%"` string that wraps
  exactly one marker preserves the referenced value's native type.

## Anti-idioms

- Don't use `//` for comments (it's integer division).
- Don't expect `list.push(x)` to mutate the list. It returns a new
  list; reassign or capture the result.
- Don't write `"hello" + 5`. String concat does not coerce. Cast:
  `"hello " + (5 as string)`. Or interpolate: `"hello ${5}"`.
- Don't use `super`. Use `parent(args)` for the constructor and
  `parent.method(args)` for parent methods.
- Don't name a source file the same as a stdlib module you import.
  The resolver picks the local file by filename. Either rename
  your file or use a non-colliding alias.
- Don't write multi-paragraph code comments. One short line for
  WHY, never WHAT (the code already says WHAT).

## Stdlib at a glance

| Module | Purpose |
|--------|---------|
| `io` | stdin/stdout, `readText`, `writeText`, `readBytes`, `exists`, `tempDir`. |
| `sys` | env vars (`getenv`, `setenv`), args, `cwd`, `exit`. |
| `path` | `join`, `dir`, `base`, `ext`, `abs`. |
| `json`, `yaml`, `toml`, `xml`, `csv` | parse / stringify; insertion-ordered. |
| `crypt` | `aesEncrypt/Decrypt`, `hmacSha256`, `jwtSign/Verify`, `base64*`, `sha*`. |
| `bytes` | `fromHex/toHex`, `fromBase64/toBase64`, `concat`, `slice`. |
| `string` | `compare`, `equalsFold`, `fromCodePoint`. |
| `re`, `pcre` | regex. |
| `db` | SQL connect / query / exec; supports sqlite, postgres, mysql. |
| `http` | client (`get`, `post`, `request`) and `serve`. |
| `time` | `now`, `sleep`, `elapsed`. |
| `datetime` | `parse`, `format`, `Instant`, `Zone`. |
| `async` | tasks, sleep, race, timeout. |
| `reflect` | class / function / decorator metadata. |
| `collections` | `range`, `filter`, `map`, `sort`, `groupBy`, `topK`. |
| `strbuilder` | O(n) string accumulation; reach for it in hot loops. |
| `math` | trig, log, floor, ceil, prime tests. |
| `errors` | `new`, `wrap`, `is`, stack traces. |
| `streams` | lazy iteration with `map`, `filter`, `reduce` chains. |

`geblang doc <module>` prints the exact signatures.

## Testing

```gb
import test;

class MathTest extends test.Test {
    @test
    func addsTwoIntegers(): void {
        this.assertEquals(5, 2 + 3);
    }

    @test
    func throwsOnInvalidInput(): void {
        this.assertThrows(func(): void { divideBy(0); });
    }
}
```

`setUp()` and `tearDown()` run around each test method. The test
runner picks up `Test` subclasses; no manual `test.run()`.

Run with `geblang test path/`. Asserters:
`assertEquals`, `assertNotEquals`, `assertTrue`, `assertFalse`,
`assertNull`, `assertNotNull`, `assertThrows`.

## Pitfalls in priority order

1. **`//` is integer division**, not a comment.
2. **Don't shadow stdlib module filenames** in your source files.
3. **List mutators are in-place** (1.16.0). `xs.push(item)` mutates
   `xs` and returns it; use `copy()`/`sorted()`/`reversed()` for copies.
4. **`as` is a cast, not a type test**. It throws on mismatch.
   Use `instanceof` for tests.
5. **Dict iteration is insertion order**, not alphabetical, not
   undefined.
6. **`reflect.interfaces(cls)`** returns the DIRECT `implements`
   clause only; walk `reflect.parent(cls)` for the full chain.
7. **String interpolation requires double quotes**. Single
   quotes are literal and don't process `\n` or `${}`.

## When in doubt

Write a five-line script and run it with `geblang run` or
`geblang check`. Both backends are fast on tiny programs and the
error messages name the line and the missing piece.
