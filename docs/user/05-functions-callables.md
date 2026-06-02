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

connect("example.com");                       # uses both defaults
connect("example.com", 443);                  # overrides port
connect("example.com", 443, false);           # overrides both
```

Any parameter can be passed by name. The name matches the parameter
identifier in the function declaration:

```gb
connect(host: "example.com", port: 443, tls: false);
connect(host: "example.com", tls: false);     # default port kept
```

Positional and named arguments can be mixed, but every positional
argument must precede the first named one:

```gb
connect("example.com", tls: false);           # ok - positional then named
connect(host: "example.com", 443);            # error - positional after named
```

Unknown names raise an error so typos surface immediately:

```gb
connect("example.com", tsl: false);           # error - no parameter "tsl"
```

Named arguments also feed overload resolution. When overloads share
an arity, naming an argument can pick the intended one:

```gb
func area(decimal w, decimal h): decimal { return w * h; }
func area(decimal radius): decimal       { return 3.14159 * radius * radius; }

area(radius: 5.0);                            # picks the one-arg overload
```

## Variadic Parameters And Spread

A variadic parameter collects trailing arguments into a list inside
the function:

```gb
func sum(int ...values): int {
    int total = 0;
    for (v in values) {
        total += v;
    }
    return total;
}

sum(1, 2, 3);                                 # 6
sum();                                        # 0
```

At the call site, `...` (spread) does the inverse: it expands a
collection into arguments. Spread dispatches on the runtime type
of its operand.

### List spread to positional arguments

A list spread fills positional slots in order:

```gb
let nums = [1, 2, 3];
sum(...nums);                                 # 6

connect(...["example.com", 443, false]);      # host, port, tls
```

A list spread can follow positional arguments:

```gb
let tail = [443, false];
connect("example.com", ...tail);
```

### Dict spread to named arguments

A string-keyed dict spread maps each entry's key to the parameter
of the same name:

```gb
let opts = {"port": 443, "tls": false};
connect("example.com", ...opts);              # host="example.com", port=443, tls=false
```

Keys that do not name a parameter of the target function are
silently ignored, so options dicts can carry more entries than the
target consumes:

```gb
let opts = {"port": 443, "tls": false, "loggedAt": 1717000000};
connect("example.com", ...opts);              # loggedAt is dropped
```

A required parameter that the dict does not cover still errors:

```gb
let opts = {"port": 443};
connect(...opts);                             # error - missing host
```

A spread and an explicit named argument that target the same
parameter raise a "passed more than once" error. Build the dict
the way you want or override on the dict, not at the call site:

```gb
let opts = {"port": 443, "tls": true};
connect("example.com", ...opts, tls: false);  # error - tls passed twice
connect("example.com", ...opts.merge({"tls": false}));   # ok
```

When dict spread is involved in overload resolution and multiple
overloads can bind, the runtime prefers the overload that drops the
fewest spread keys. Two overloads tied on that score are still
reported as ambiguous.

## Pipe Operator

`x |> f(...)` desugars to a call where `x` is injected as the first
positional argument: `f(x, ...)`. The pipe is left-associative, so
chains read left-to-right as a transformation pipeline.

```gb
import io;

func double(int x): int     { return x * 2; }
func add(int a, int b): int { return a + b; }

io.println(5 |> double);                 # 10  (bare callable form)
io.println(5 |> double());               # 10  (explicit-paren form)
io.println(5 |> add(3));                 # 8   (extra positional args follow)
io.println(5 |> double() |> add(1));     # 11  (chained)
```

The right-hand side can be a bare identifier, a `module.fn` selector,
a free-function call, or a `class.staticMethod(...)` selector call.
For each, the pipe value is prepended to whatever arguments are
already there.

```gb
"hello" |> Util.wrap("[", "]")           # Util.wrap("hello", "[", "]")
xs |> collections.maxBy(scorer)          # collections.maxBy(xs, scorer)
```

The operator binds at very low precedence (just above assignment), so
each side absorbs full expressions:

```gb
2 + 3 |> double                          # (2 + 3) |> double  ->  10
(true ? 5 : 0) |> double                 # parenthesise to control grouping
```

If the right-hand side isn't a call, identifier, or selector, the
pipe is a parse-time error - `x |> 42` is rejected.

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
