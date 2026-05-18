# Types

## Primitive Types

Core primitive types:

- `int`
- `decimal`
- `float`
- `bool`
- `string`
- `char`
- `bytes`
- `null`
- `any`

Collection types:

- `list<T>` or `T[]`
- `dict<K, V>`
- `set<T>`

Runtime and framework types include `Type`, `Task<T>`, `generator<T>`,
`iterable<T>`, classes, interfaces, and enums.

`func` (or its aliases `callable` and `function`) is the catch-all type for
any callable value - a function, lambda, method reference, or closure. Use it
when storing a function in a field or accepting one as a parameter:

```gb
class Scheduler {
    func cb;
    func Scheduler(func cb) { this.cb = cb; }
    func fire(): void {
        let handler = this.cb;
        handler();
    }
}
```

Use concrete collection element types whenever possible:

```gb
list<string> names = ["Ada", "Grace"];
dict<string, int> scores = {"Ada": 10};
```

Use `any` at dynamic boundaries such as decoded JSON, request dictionaries, and
extension responses. Convert or validate it before passing values deeper into
typed application code.

## Nullability

Types are non-null by default. Prefix with `?` to allow `null`:

```gb
string name = "Ada";
?string maybeName = null;

if (maybeName != null) {
    io.println(maybeName.length());
}
```

A nullable value must be checked or handled before calling methods that require
a non-null value. Optional chaining is useful when absence is acceptable:

```gb
let city = user?.address?.city ?? "unknown";
```

## Casts And Type Checks

```gb
let text = value as string;
io.println(value instanceof string);
io.println(user instanceof User);
```

`typeof(value)` returns a `Type` value. Compare it with a built-in type name,
class name, or `.type` selector for exact runtime type equality; use
`instanceof` when subclasses or interface implementations should match:

```gb
io.println(typeof("x"));           # string
io.println(typeof(typeof("x")));   # Type
io.println(typeof("x") == string); # true
io.println("x" instanceof string); # true

class User {}
User u = User();
io.println(typeof(u) == User);     # true
io.println(u.type == User);        # true (shorthand for typeof)
io.println("x".type == string);    # true
io.println(string.type);           # string
```

The `.type` selector is a shorthand for `typeof` - `expr.type` is equivalent to
`typeof(expr)`. Primitive type names (`string`, `int`, `bool`, `decimal`, etc.)
and class names can be used directly as type values in comparisons.

### Generic type parameters and built-in collections

Generic type parameters on built-in collection types (`list<T>`, `dict<K,V>`,
`set<T>`) are **not preserved at runtime**. `typeof([1, 2, 3])` returns `list`,
not `list<int>`. Compare against the base type name only: `typeof(myList) == list`.
There is no `Type<T>` expression syntax.

Type annotations on parameters using generic collections are enforced at typed
function/method call boundaries and typed declaration boundaries:
`list<int>` on a parameter checks that the value is a list **and** that every
element is an `int` when the call is made. The same boundary check applies when
a typed variable declaration is initialized.

```gb
func sumInts(list<int> items): int {
    let total = 0;
    for (item in items) { total = total + item; }
    return total;
}

sumInts([1, 2, 3]);      # ok
sumInts(["a", "b"]);     # runtime type error  -  list<string> does not satisfy list<int>

list<int> nums = [1, 2, 3];   # ok
list<int> bad  = ["a", "b"];  # runtime type error  -  list<string> does not satisfy list<int>
```

The error message names the function (or variable) and describes both the
expected and actual element types, e.g.:

```
sumInts expects list<int> for parameter 'items', got list<string>
type error: cannot assign list<string> to list<int>
```

If you need a custom error message you can inspect elements yourself before
calling or assigning.

These checks do not make primitive collections permanently typed containers.
After a value has been accepted at a boundary, normal collection mutation
methods still operate on the mutable runtime value. Re-check at the next typed
boundary, or validate before mutation when the collection is shared widely.

### Reified generics on user-defined classes

Generic type parameters **are** preserved for user-defined generic classes.
`reflect.typeBindings(instance)` returns a dict mapping each type parameter
name to the concrete type it was bound with:

```gb
import reflect;

class Box<T> {
    T value;
    func Box(T v) { this.value = v; }
}

Box<string> b = Box("hello");
io.println(typeof(b) == Box);              # true
io.println(reflect.typeBindings(b));       # {T: string}
io.println(reflect.typeBindings(b)["T"]);  # string
```

Use `reflect.typeOf(value)` when you want a stdlib function form that can be
passed around like any other callable.

## The bytes Type

`bytes` represents a raw, immutable sequence of octets. It is the correct type
for binary data: file content read in binary mode, cryptographic hashes and
ciphertext, compressed payloads, HTTP request/response bodies that are not
guaranteed to be valid UTF-8, and any value that must survive a round-trip
without text encoding assumptions.

`bytes` is distinct from `string`. A `string` is always valid text encoded as
UTF-8. A `bytes` value is just a sequence of unsigned octets with no inherent
encoding; converting it to `string` requires an explicit call.

### Creating bytes values

Use the `bytes` module to produce `bytes` values:

```gb
import bytes;

let raw  = bytes.fromString("hello");          # encode UTF-8 string to bytes
let hex  = bytes.fromHex("48656c6c6f");        # decode hexadecimal
let b64  = bytes.fromBase64("aGVsbG8=");       # decode Base64
let both = bytes.concat([raw, hex]);           # join two byte sequences
```

### bytes instance methods

`bytes` values expose these methods directly:

| Method | Returns | Description |
|--------|---------|-------------|
| `length()` | `int` | Number of bytes (also available as the `.length` property) |
| `isEmpty()` | `bool` | True when length is zero |
| `get(int index)` | `int` | Byte value at position (0-255) |
| `contains(int byte)` | `bool` | True if the byte value appears anywhere |
| `toString()` | `string` | Decode as UTF-8; throws on invalid bytes |
| `toHex()` | `string` | Lowercase hex representation |
| `toBase64()` | `string` | Standard Base64 encoding |

```gb
import bytes;

let data = bytes.fromString("Geblang");
io.println(data.length);        # 7  (property form)
io.println(data.length());      # 7  (method form)
io.println(data.toHex());       # 4765626c616e67
io.println(data.toBase64());    # R2VibGFuZw==
io.println(data.get(0));        # 71 (ASCII 'G')
```

> The `.length` property works on every sequence-like type: `list`, `dict`,
> `set`, `string`, `bytes`, and `range`. It is identical to calling
> `.length()` and is preferred for readability when no other arguments are
> needed.

### Common patterns

**Cryptography and hashing**: hash and sign functions in the `crypt` module
return `bytes`. Pass them directly to `bytes.toHex()` or `bytes.toBase64()` for
storage or transport:

```gb
import crypt;
import bytes;

let hash = crypt.sha256("password123");
io.println(hash.toHex());     # hex digest
io.println(hash.toBase64());  # Base64 digest
```

**Compression**: `compress.gzip` takes and returns `bytes`:

```gb
import bytes;
import compress;
import io;

let payload  = bytes.fromString("lots of repeated text...");
let packed   = compress.gzip(payload);
let unpacked = compress.gunzip(packed);
io.println(unpacked.toString());
```

**HTTP bodies**: when a response body is not guaranteed to be text, read it as
`bytes` and convert only when you know the encoding:

```gb
import http;
import bytes;
import io;

let resp = http.get("https://example.com/data.bin");
let body = bytes.fromString(resp["body"]);   # or use bodyBytes() on Response
io.println(body.length());
```

**Binary file I/O**: read/write binary files using the `io` module's binary
helpers; the value in memory is `bytes`.

### bytes vs string summary

| | `string` | `bytes` |
|---|----------|---------|
| Content | Valid UTF-8 text | Raw octets, any content |
| Literal syntax | `"hello"` or `'hello'` | No literal; use `bytes.fromString` / `bytes.fromHex` |
| Length | Characters (Unicode) | Octets |
| Indexing | Not directly - use `.chars()` | `.get(i)` returns `int` (0-255) |
| Concatenation | `+` operator | `bytes.concat([a, b])` |
| Conversion | `value as string` | `.toString()` (UTF-8 decode) / `.toHex()` / `.toBase64()` |

## Type Aliases

```gb
type UserId = string;
type Money = decimal;
type IntList = int[];
```

Aliases document intent but do not create distinct runtime types.

## Generics

Functions, classes, methods, and interfaces can declare type parameters inside
angle brackets. The type parameter is then available as a type name throughout
the declaration:

```gb
func identity<T>(T value): T {
    return value;
}

class Box<T> {
    T value;
    func Box(T value) { this.value = value; }
    func get(): T { return this.value; }
}

let b = Box<string>("hello");
io.println(b.get());   # hello
```

### Reified type bindings

Geblang generics are *reified* - type bindings are tracked at runtime, not
erased. This means `instanceof T` works inside generic functions and methods,
and framework code can inspect actual bound types with `reflect.typeBindings`:

```gb
func assertIs<T>(T value): bool {
    return value instanceof T;
}

io.println(assertIs<string>("hello"));   # true
io.println(assertIs<int>("hello"));      # false
```

Type bindings flow through call chains. When you call a method on a generic
class instance, the method sees the concrete binding that was established when
the instance was created:

```gb
class Typed<T> {
    T value;
    func Typed(T v) { this.value = v; }

    func isCorrectType(any candidate): bool {
        return candidate instanceof T;   # T is the concrete type at runtime
    }
}

let t = Typed<string>("Ada");
io.println(t.isCorrectType("Grace"));   # true
io.println(t.isCorrectType(42));        # false
```

### Inspecting type bindings

`reflect.typeBindings(instance)` returns a dict mapping each type parameter
name to its concrete bound type. This is how frameworks discover what types a
repository, container, or validator is parameterized with:

```gb
import reflect;

class Repository<T> {
    list<T> items = [];

    func add(T item): void {
        this.items.push(item);
    }
}

let repo = Repository<User>();
io.println(reflect.typeBindings(repo));   # {"T": <Type User>}
```

### Type inference

The type parameter is inferred from the call arguments - you rarely need to
write it explicitly for functions:

```gb
func first<T>(list<T> items): ?T {
    return items.length() > 0 ? items[0] : null;
}

let names = ["Ada", "Grace"];
let name = first(names);   # T inferred as string; returns ?string
```

For classes, specify the type parameter at construction when inference is not
possible from the constructor arguments:

```gb
let empty = Repository<User>();   # T cannot be inferred from an empty constructor
```

### Container types

Generic collection type hints document and enforce call/declaration boundaries.
The type binder infers `T` from the element type in the call arguments:

```gb
func sum<T>(list<T> items, func(T): int selector): int {
    int total = 0;
    for (let item of items) {
        total += selector(item);
    }
    return total;
}

let total = sum(orders, func(Order o): int { return o.amount; });
```

### Constraints

Add `implements InterfaceName` after the type parameter to require the
concrete type to satisfy an interface. The constraint lets you call interface
methods inside the function:

```gb
interface Printable {
    func print(): string;
}

func show<T implements Printable>(T item): string {
    return item.print();   # valid because T implements Printable
}
```

Without the constraint, calling methods on `T` is a type error because the
compiler cannot verify that the method exists.

Union constraints (`|`) mean the type must satisfy at least one of the
interfaces. Intersection constraints (`,`) mean it must satisfy all of them:

```gb
# T must implement Swimmer OR Runner
func move<T implements Swimmer | Runner>(T item): void {}

# T must implement both Printable AND Persistable
func persist<T implements Printable, Persistable>(T item): void {}
```

### Generics on methods and interfaces

Type parameters can appear on individual methods inside a class, not just on
the class itself. Interface declarations can also be generic:

```gb
class Converter {
    func to<T>(string raw): T {
        return raw as T;
    }
}

interface Container<T> {
    func get(): T;
    func set(T value): void;
}
```

### Generics across function boundaries

A lambda declared inside a generic function's body inherits the
outer call site's type bindings. The same is true when a generic
function is referenced by name and passed as a value: the reference
captures the surrounding generic frame's bindings at the moment the
value is taken. This lets higher-order code keep its type guarantees
even when control flows through a stdlib helper such as
`collections.maxBy`.

```gb
import collections;

interface Scored { func score(): int; }

func topBy<T implements Scored>(list<T> items): T {
    # The lambda's `T x` parameter resolves to the same concrete type
    # as the outer call - if topBy is called with a list<Player>, T is
    # Player inside the lambda too, and a non-Player would be rejected
    # at the parameter boundary.
    return collections.maxBy(items, func(T x): int { return x.score(); });
}
```

If the surrounding frame has no binding for the named type parameter
(for example when the lambda escapes its creation scope and is called
later from somewhere else), the matcher falls back to the literal
type-parameter behaviour and accepts any value.
