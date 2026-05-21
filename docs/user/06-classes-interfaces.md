# Classes, Interfaces, And Enums

## Classes

```gb
class User {
    string name;

    func User(string name) {
        this.name = name;
    }

    func displayName(): string {
        return this.name;
    }
}
```

Constructors use the class name. Instance methods use `this`.

Fields should be declared on the class so instances have a predictable shape:

```gb
class User {
    string id;
    string email;

    func User(string id, string email) {
        this.id = id;
        this.email = email;
    }
}
```

Keep constructors focused on valid initialization. Use named static factories
or module functions when construction needs parsing, I/O, or fallback behavior.

## Method Overloading

A class may define multiple methods (or constructors) with the same name, as
long as the signatures differ by the number or types of parameters. The runtime
selects the best-matching overload at call time:

```gb
import io;

class Printer {
    func print(string s): void {
        io.println("string: " + s);
    }

    func print(int n): void {
        io.println("int: " + n);
    }

    func print(string label, int n): void {
        io.println(label + ": " + n);
    }
}

let p = Printer();
p.print("hello");        # string: hello
p.print(42);             # int: 42
p.print("count", 7);     # count: 7
```

Constructor overloading follows the same rules:

```gb
class Point {
    decimal x;
    decimal y;

    func Point() {
        this.x = 0;
        this.y = 0;
    }

    func Point(decimal x, decimal y) {
        this.x = x;
        this.y = y;
    }
}

let origin = Point();
let p = Point(3.0, 4.0);
```

When a call matches no overload, or multiple overloads match equally well, a
runtime type error is raised identifying the method name and the arguments
that were passed.

Methods and constructors may also differ by return type when the surrounding
context provides an expected type. For example, assigning the result to a typed
variable can select the overload. Without that expected type, a call that
matches only by return type is ambiguous and raises a runtime type error.

## Inheritance

Classes use `extends` to inherit from one parent class:

```gb
class Admin extends User {
    func Admin(string name) {
        parent(name);
    }

    func displayName(): string {
        return "admin " + parent.displayName();
    }
}
```

Geblang classes currently support single class inheritance: one class can extend
one parent class. Multiple class inheritance is not part of the language today.
Use interfaces for multiple behavioral contracts and composition for sharing
services or collaborators.

Child classes inherit parent fields and methods. If the parent has a
zero-argument constructor and the child constructor does not explicitly call
`parent(...)`, Geblang calls the parent constructor automatically. If the parent
constructor requires arguments, call `parent(...)` yourself:

```gb
class Animal {
    string name;

    func Animal(string name) {
        this.name = name;
    }

    func speak(): string {
        return this.name + " makes a sound";
    }
}

class Dog extends Animal {
    func Dog(string name) {
        parent(name);
    }

    func speak(): string {
        return parent.speak() + ", then barks";
    }
}
```

`parent.method()` calls the parent implementation from an override. `parent(...)`
calls the parent constructor. An explicit `parent(...)` call suppresses the
automatic no-argument parent constructor call, so the parent constructor only
runs once.

Automatic parent constructor example:

```gb
class Base {
    int count = 0;

    func Base() {
        this.count = this.count + 1;
    }
}

class Child extends Base {
    func Child() {
        # Base() is called automatically before this body runs.
    }
}

io.println(Child().count); # 1
```

Use inheritance for true specialization. Prefer composition when one object
merely needs to use another service:

```gb
class UserService {
    UserRepository repo;

    func UserService(UserRepository repo) {
        this.repo = repo;
    }
}
```

## Static Members

Classes can declare both immutable constants and mutable fields at
class scope, plus static methods. `static const` makes an immutable
class-level binding; `static let` and the typed form `static <type>`
declare a mutable class-level field that any code in scope can read
and reassign.

```gb
class Build {
    static const VERSION = "0.1.0";

    static func label(): string {
        return Build.VERSION;
    }
}

class Counter {
    static let count = 0;            # untyped, mutable
    static int errors = 0;           # typed, mutable

    static func tick(): int {
        Counter.count = Counter.count + 1;
        return Counter.count;
    }
}

Counter.tick();
Counter.tick();
io.println(Counter.count);           # 2
Counter.errors = 5;                  # external assignment also works
io.println(Counter.errors);          # 5
```

Reading static members from inside the class uses the same `ClassName.member`
syntax; there is no implicit `self` for static methods.

## Immutable Classes

Apply the `@immutable` decorator to freeze every instance after its constructor
returns. Fields are readable; any assignment to a field raises `ImmutableError`.

```gb
@immutable class Point {
    int x;
    int y;
    func Point(int x, int y) { this.x = x; this.y = y; }
}

Point p = Point(3, 4);
io.println(p.x);   # 3
p.x = 99;          # throws ImmutableError
```

Produce modified copies using the *wither* pattern instead of mutation:

```gb
@immutable class Point {
    int x;
    int y;
    func Point(int x, int y) { this.x = x; this.y = y; }
    func withX(int x): Point { return Point(x, this.y); }
}
```

See the `freeze` module documentation for `freeze.shallow`, `freeze.deep`,
`freeze.isFrozen`, `.copy()`, and `const` collection auto-freeze behavior.

## Interfaces

```gb
interface Printable {
    func print(): string;
}

class Report implements Printable {
    func print(): string {
        return "report";
    }
}
```

Interfaces can inherit from other interfaces. Classes explicitly declare
`implements`.

Interfaces may extend multiple interfaces:

```gb
interface Printable {
    func print(): string;
}

interface Serializable {
    func serialize(): string;
}

interface Reportable extends Printable, Serializable {
    func title(): string;
}
```

Classes can implement multiple interfaces:

```gb
class Report implements Printable, Serializable {
    func print(): string {
        return "report";
    }

    func serialize(): string {
        return "{\"type\":\"report\"}";
    }
}
```

This is the main way to model "multiple inheritance" style contracts in
Geblang: one concrete parent class for implementation inheritance, many
interfaces for capabilities.

Interfaces are structural contracts for application boundaries. They work well
for repositories, renderers, cache stores, middleware, and test doubles.

```gb
interface CacheStore {
    func get(string key): any;
    func set(string key, any value, int ttl): void;
    func delete(string key): void;
}
```

Interfaces work well with dependency injection:

```gb
class CachedUsers {
    CacheStore cache;

    func CachedUsers(CacheStore cache) {
        this.cache = cache;
    }
}
```

## Decorators

Decorators are `@`-prefixed annotations applied to classes, functions,
methods, or fields. They come in two flavours depending on where you
put them, and the two flavours are doing very different things. This
section explains both.

### Behavioural decorators on functions, methods, and classes

A decorator applied to a `func` or `class` declaration whose name
resolves to a function in scope **wraps the target**. The target value
is passed to the decorator at definition time, and the decorator's
return value replaces it. Use this to add cross-cutting behaviour like
logging, caching, retry, or timing.

```gb
func log(callable fn): callable {
    return func(...args) {
        io.println("calling", reflect.className(fn));
        return fn(...args);
    };
}

@log
func greet(string name): string {
    return "hi, " + name;
}
```

Calling `greet("Ada")` invokes the wrapped value: the log line prints
first, then the underlying body runs. Multiple decorators stack in
source order. The topmost decorator wraps the inner wrapped value
last.

### Annotation-only (metadata) decorators

A decorator whose name does **not** resolve to a function in scope is
treated as **pure metadata**. The runtime does not execute it. The
name and any arguments are recorded on the target so reflection can
read them back. This is the form frameworks use to drive
configuration-by-annotation:

```gb
@Get("/users/{id}")
@Summary("Fetch one user")
func getUser(string id): User { ... }
```

`@Get` and `@Summary` are not functions; nothing happens at definition
time. A web framework reads them later via
`reflect.decorators(getUser)`.

Dotted names like `@Assert.email` or `@Foo.bar.baz` are valid and
parse as a single composite identifier. The dot is part of the name,
so dispatch is by exact string match. Use this to group related
annotations under a common prefix.

### Field-level decorators

Decorators on a field declaration **are always annotation-only**.
There is no value to wrap (a field is not a callable), and the runtime
does not intercept field reads or writes. They exist exclusively for
reflective consumption.

```gb
class CreateUserDTO {
    @Assert.email
    string email;

    @Assert.minLength(2)
    @Assert.maxLength(64)
    string name;
}
```

`reflect.fields(CreateUserDTO)` returns one entry per field. When a
field has annotations, the entry includes a `decorators` key: a list
of `{name, args, namedArgs, ...}` dicts. Frameworks like Gebweb read
these to drive validation, serialisation filters, ORM hints, OpenAPI
schema enrichment, etc.

**When do field decorators run?** Never automatically. They are
*static* metadata: parsed once at class compile time and frozen onto
the class definition. The runtime never executes a field decorator
on its own. Assigning or reading the field always proceeds without
consulting the annotation list. Anything dynamic happens because
*some piece of code* (your framework, your test harness, your code)
reads the decorators via reflection and decides what to do.

**What can a field decorator's arguments be?** Literal values
(strings, ints, floats, decimals, bools, null) and literal
list / dict / set composites built from those. Names from scope are
not resolved at field-decorator time; the value must be expressible
as a constant. This keeps the metadata stable and serialisable into
the compiled chunk.

```gb
class Item {
    @Assert.range(1, 100)
    @Groups("read", "write", "admin")
    int quantity;
}
```

`@Assert.range(1, 100)` to metadata `{name: "Assert.range", args: [1, 100]}`.
`@Groups("read", "write", "admin")` to metadata
`{name: "Groups", args: ["read", "write", "admin"]}`.

### Reading decorator metadata

| Call | Returns |
| --- | --- |
| `reflect.decorators(target)` | List of `{name, args, namedArgs, line, column}` dicts. |
| `reflect.hasDecorator(target, name)` | Bool. `true` when at least one decorator with that name is present. |
| `reflect.decorator(target, name)` | First decorator dict with that name, or `null`. |
| `reflect.fields(class)` | List of field dicts; each has a `decorators` key when at least one annotation is present. |

`target` is either a class value, a function/method value, or a class
instance (in which case the call delegates to the instance's class).
Names match exactly: `@Assert.email` is the name `"Assert.email"`,
not `"Assert"` with a sub-key.

## Magic Methods

Magic methods are ordinary methods with reserved names. They let a class opt
into dynamic property access, callable object behavior, and operator
overloading. Keep them focused: a class should only implement the magic methods
that match its public role.

## Dynamic Access And Method Dispatch

Use `__get`, `__set`, and `__call` for dynamic objects such as records, proxies,
configuration wrappers, or framework adapters.

```gb
class Bag {
    dict<string, any> values;

    func Bag() {
        this.values = {};
    }

    func __get(string name): any {
        if (this.values.hasKey(name)) {
            return this.values[name];
        }
        return null;
    }

    func __set(string name, any value): void {
        this.values[name] = value;
    }

    func __call(string name, list<any> args): any {
        return {"method": name, "args": args};
    }
}
```

Dynamic access is useful at framework boundaries, but normal declared fields and
methods should be preferred for domain code because they are easier to type
check and document.

## Callable Objects

Implement `__invoke` when an object should be usable like a function. This is
useful for middleware, guards, predicates, command handlers, and strategy
objects that need constructor state.

```gb
class Prefixer {
    string prefix;

    func Prefixer(string prefix) {
        this.prefix = prefix;
    }

    func __invoke(string value): string {
        return this.prefix + value;
    }
}

let shout = Prefixer("hello ");
io.println(shout("Ada"));
```

Callable objects can be passed to parameters typed as `callable`.

## Operator Overloading

Operator methods customize how instances interact with operators:

- equality: `__eq(other)`
- ordering: `__lt(other)`, `__lte(other)`, `__gt(other)`, `__gte(other)`
- arithmetic: `__add`, `__sub`, `__mul`, `__div`, `__intdiv`, `__mod`,
  `__pow`
- bitwise: `__bitand`, `__bitor`, `__bitxor`, `__lshift`, `__rshift`
- prefix: `__not`, `__neg`, `__bitnot`

Example:

```gb
class Money {
    int cents;

    func Money(int cents) {
        this.cents = cents;
    }

    func __add(Money other): Money {
        return Money(this.cents + other.cents);
    }

    func __eq(Money other): bool {
        return this.cents == other.cents;
    }

    func __lt(Money other): bool {
        return this.cents < other.cents;
    }
}

let total = Money(500) + Money(250);
io.println(total == Money(750));
```

Operator methods should return the type users expect from the operator.
Comparison and equality methods must return `bool`; arithmetic methods should
usually return the same domain type.

## Cast Overloading

A class can control how its instances respond to `as TYPE` casts by
defining a cast dunder for each target primitive:

- `__string(): string` for `as string`
- `__int(): int` for `as int`
- `__float(): float` for `as float`
- `__bool(): bool` for `as bool`
- `__decimal(): decimal` for `as decimal`
- `__bytes(): bytes` for `as bytes`

Each dunder must declare the matching return type. The semantic
analyzer rejects mismatches at compile time, and the runtime checks
the actual returned value as a defensive backstop.

```gb
class Money {
    int cents;

    func Money(int cents) {
        this.cents = cents;
    }

    func __string(): string {
        return "$" + (this.cents as string);
    }

    func __int(): int {
        return this.cents;
    }

    func __bool(): bool {
        return this.cents != 0;
    }
}

let m = Money(550);
io.println(m as string);   # $550
io.println(m as int);      # 550
io.println(m as bool);     # true
io.println(Money(0) as bool);  # false
```

When the class does not define a dunder for the target primitive, the
default cast logic runs (errors when the conversion is undefined for
the receiver's type).

## Destructors

A class can declare a destructor with the `func ~ClassName()` syntax. The
destructor takes no arguments and is called when an instance reaches the end
of its lifetime - either at program exit (the runtime sweeps every
destructor-bearing instance that hasn't already been destroyed) or via an
explicit `del x;` statement. Destructors are end-of-lifetime hooks; they are
**not** tied to `with`-blocks, which serve a separate purpose (see *Context
Managers* below).

```gb
import io;

class FileHandle {
    string path;
    int fd;

    func FileHandle(string path) {
        this.path = path;
        this.fd = io.open(path, "r");
    }

    func ~FileHandle() {
        io.close(this.fd);
        io.println("closed " + this.path);
    }
}

let f = FileHandle("data.txt");
/* ... use f ... */
/* At program exit (or when `del f;` runs), ~FileHandle fires. */
```

At the program-exit sweep, destructors fire in reverse-creation order (LIFO)
so younger instances - which may depend on older ones - clean up first.
Destructor exceptions are logged to stderr but never abort the sweep; every
remaining instance still gets a chance to run.

### Explicit destruction with `del`

Use `del x;` to retire a binding mid-script. The runtime invokes the
destructor (if the class declares one) immediately and removes the binding
from the surrounding scope:

```gb
let f = FileHandle("data.txt");
useFile(f);
del f;          /* ~FileHandle fires here. */
io.println("file already closed");
```

`del` only accepts an identifier - `del a.b;` and `del a[0];` are parse
errors. After `del x`, the static analyzer rejects subsequent references to
`x` in the same control-flow path with `use of destroyed binding "x"`. A
fresh `let x = ...;` re-introduces the name with a new lifetime.

Destructors that throw during a sweep print the error to stderr but do not
crash the sweep.

## Context Managers (`with`, `__enter__`, `__exit__`)

The `with` statement runs the magic methods `__enter__()` and `__exit__()`
on the bound resource. It is a scoped-cleanup construct, distinct from the
destructor lifecycle.

```gb
class Transaction {
    string label;

    func Transaction(string label) { this.label = label; }

    func __enter__(): Transaction {
        io.println("begin " + this.label);
        return this;
    }

    func __exit__(): void {
        io.println("commit " + this.label);
    }
}

with (tx = Transaction("write")) {
    io.println("inside " + tx.label);
}
/* Output:
 *   begin write
 *   inside write
 *   commit write
 */
```

Two forms are accepted: `with (expr) { ... }` discards the value;
`with (name = expr) { ... }` binds the result of `__enter__()` (or the
expression itself when `__enter__()` is undefined) to `name`. At block exit
- normal completion, exception, `return`, `break`, or `continue` - the
runtime invokes `__exit__()` if defined; otherwise the block exits as a
no-op. The class destructor is **not** called at this point; it fires later
via the lifetime mechanism described above.

If you want both - per-block cleanup AND end-of-lifetime cleanup - declare
both methods.

## Serialisation: `__serialize__` And `__deserialize__`

Class instances serialise out of the box. `json.stringify`, `yaml.stringify`,
and `toml.stringify` accept an instance and dump its **public** fields:

- Fields whose name does not start with `_` are emitted.
- Fields beginning with `_` or `__` are treated as private and skipped.

No opt-in is needed for plain data classes.

```gb
import json;

class Point {
    int x;
    int y;
    int _secret;
    func Point(int x, int y) { this.x = x; this.y = y; this._secret = 99; }
}

io.println(json.stringify(Point(3, 4)));
/* {"x":3,"y":4} - _secret is omitted. */
```

A class can replace the default by implementing `__serialize__()`. The return
value is itself serialised by the stringify call, so any dict/list/scalar shape
works:

```gb
class Tagged {
    string kind;
    string label;
    func Tagged(string kind, string label) {
        this.kind = kind; this.label = label;
    }
    func __serialize__(): dict {
        return {"kind": this.kind, "label": this.label, "v": 1};
    }
}
```

The symmetric `parseAs(text, ClassRef)` reconstructs an instance:

```gb
let p = json.parseAs("{\"x\": 3, \"y\": 4}", Point);
io.println(p.x);
io.println(p.y);
```

`parseAs` first looks for a static `__deserialize__(dict)` factory on the
target class. When present it is called with the parsed value:

```gb
class Tagged {
    string kind;
    string label;
    func Tagged(string kind, string label) {
        this.kind = kind; this.label = label;
    }
    static func __deserialize__(dict d): Tagged {
        return Tagged(d["kind"], d["label"]);
    }
}
```

When no `__deserialize__` exists, `parseAs` matches the dict keys to the
constructor's parameter names and calls the constructor positionally. A
missing required parameter raises a runtime error.

The same machinery applies to `yaml.parseAs`, `toml.parseAs`, and
`xml.parseAs`.

## Enums

```gb
enum Color { Red, Green, Blue }
enum Result { Ok(string), Err(string) }

let color = Color.Red;
let result = Result.Ok("saved");
```

Enums support equality, `instanceof`, and match destructuring.

Enums are a good fit for closed sets and tagged results:

```gb
enum SaveResult {
    Saved(string id),
    Duplicate(string field),
    Failed(string message)
}

let message = match (result) {
    case SaveResult.Saved(string id) => "saved " + id;
    case SaveResult.Duplicate(string field) => "duplicate " + field;
    case SaveResult.Failed(string error) => error;
};
```
