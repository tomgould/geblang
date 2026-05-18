# Functions And Callables

## Functions

```gb
func add(int a, int b): int {
    return a + b;
}
```

Return types may be explicit or inferred.

Use explicit return types for exported functions and public methods. Inferred
return types are convenient for short local helpers, but explicit signatures are
better module documentation.

```gb
export func loadUser(string id): ?User {
    return repository.find(id);
}
```

## Defaults And Named Arguments

Default parameters must be trailing:

```gb
func connect(string host, int port = 80, bool tls = true): void {}

connect("example.com", tls: false);
```

Named arguments are useful when a function has several optional settings. They
also pair with dictionary spread for option objects:

```gb
let opts = {"port": 443, "tls": true};
connect("example.com", ...opts);
```

## Variadic Parameters And Spread

```gb
func sum(int ...values): int {
    int total = 0;
    for (v in values) {
        total += v;
    }
    return total;
}

let nums = [1, 2, 3];
sum(...nums);
```

A string-keyed dictionary can be spread into named arguments:

```gb
let opts = {"port": 8080, "tls": false};
connect("example.com", ...opts);
```

Variadic parameters are collected into a list inside the function. Spread does
the inverse at the call site.

## Function Overloading

Multiple functions may share the same name as long as they differ in the number
or types of their parameters. At call time the runtime picks the best-matching
overload:

```gb
import io;

func describe(string s): string { return "string: " + s; }
func describe(int n): string    { return "int: " + n; }
func describe(any x): string    { return "other: " + x; }

io.println(describe("hello"));  # string: hello
io.println(describe(42));       # int: 42
io.println(describe(true));     # other: true
```

Overload resolution rules:

1. A call must match exactly one overload by argument count and compatible
   parameter types.
2. If no overload matches, or more than one overload matches, a runtime error is
   raised.
3. The `any` type matches any value. Avoid mixing an `any` overload with more
   specific overloads of the same arity unless the call site provides enough
   context to avoid ambiguity.

When overloads differ only by return type, Geblang needs an expected type from
the surrounding context. A typed assignment, typed argument, or explicit cast can
select the intended overload. A bare call with no expected return type is
ambiguous:

```gb
func load(string id): User { return findUser(id); }
func load(string id): Order { return findOrder(id); }

User user = load("u-123");      # expected type selects User overload
Order order = load("o-123");    # expected type selects Order overload
# load("x");                    # ambiguous without an expected return type
```

Overloading works in the same way for class methods (see the Classes chapter).
Named overloads improve error messages: when a call fails you will see which
name and which expected types did not match.

## Anonymous Functions And Closures

```gb
let inc = func(int x): int {
    return x + 1;
};

func makeCounter(): callable {
    int n = 0;
    return func(): int {
        n++;
        return n;
    };
}
```

Closures capture outer variables by reference.

That means updates made inside the closure are visible to later calls:

```gb
let next = makeCounter();
io.println(next()); # 1
io.println(next()); # 2
```

Use closures for callbacks, collection helpers, middleware, decorators, and
small pieces of behavior that do not need a full class.

## Callable Objects

Objects can be callable with `__invoke`:

```gb
class Prefixer {
    string prefix;
    func Prefixer(string prefix) { this.prefix = prefix; }
    func __invoke(string name): string { return this.prefix + name; }
}

let hello = Prefixer("hello ");
io.println(hello("Ada"));
```

Use `callable` as a type hint when an API accepts a function literal, named
function, decorated callable, or object implementing `__invoke`.

```gb
func twice(callable fn, int value): int {
    return fn(fn(value));
}
```

## Decorators

Decorators attach metadata and can also be callable wrappers when their names
resolve to functions:

```gb
func logged(any next): any {
    return func(string name): string {
        log.info("enter");
        defer log.info("exit");
        return next(name);
    };
}

@logged
func greet(string name): string {
    return "hello " + name;
}
```

`reflect.decorators(value)` exposes decorator metadata for functions, classes,
methods, and static methods.

Decorators are evaluated at declaration time. A decorator can be used as pure
metadata for reflection, as a callable wrapper, or both. Framework code can scan
metadata with `reflect` and register handlers without introducing framework
syntax into the language.
