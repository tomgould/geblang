# Control Flow

## Conditionals

Conditions must be `bool`.

```gb
if (score >= 90) {
    io.println("excellent");
} elseif (score >= 60) {
    io.println("pass");
} else {
    io.println("retry");
}
```

There is no implicit truthiness. Check length, nullability, or equality
directly:

```gb
if (items.length() > 0 && currentUser != null) {
    io.println("ready");
}
```

## Loops

```gb
while (i < 10) {
    i++;
}

for (let int i = 0; i < 3; i++) {
    io.println(i);
}

for (n in [1, 2, 3]) {
    io.println(n);
}

for (key in data.keys()) {
    io.println(key);
}
```

`break` exits a loop. `continue` skips to the next iteration.

Use `for-in` for collections and generators. Use C-style `for` loops when the
index itself matters:

```gb
list<string> names = ["Ada", "Grace", "Linus"];

for (let int i = 0; i < names.length(); i++) {
    io.println("${i}: ${names[i]}");
}
```

This is the direct equivalent of a basic counter loop in C, JavaScript, PHP, or
Go. The loop variable is available in the loop body and can be used as an index.
Use this pattern when you need both the current value and its position.

Range literals support an optional `by` step:

```gb
for (i in 0..10 by 2) {    # 0 2 4 6 8 10
    io.print("${i} ");
}

for (i in 10..0 by -2) {   # 10 8 6 4 2 0
    io.print("${i} ");
}

for (i in 0..<10 by 3) {   # 0 3 6 9 (exclusive upper bound)
    io.print("${i} ");
}
```

The step defaults to 1 when omitted. It can be any integer expression.

For lazy range iteration over large sequences, import `collections` and use
`collections.range(start, end, step)`:

```gb
import collections;

for (i in collections.range(0, 5, 1)) {
    io.println(i);
}

for (i in collections.range(10, 0, -2)) {
    io.println(i);
}
```

`collections.range` is lazy and suitable for large ranges. Use the `by` clause
on range literals for the common case; use `collections.range` when you need
lazy evaluation or runtime-constructed sequences.

## Range methods and properties

Range literals produce first-class values with methods and read-only properties.

```gb
let r = 0..10 by 2;

r.length()       # 6
r.isEmpty()      # false
r.contains(4)    # true
r.contains(3)    # false
r.first()        # 0
r.last()         # 10
r.toList()       # [0, 2, 4, 6, 8, 10]

r.start          # 0
r.end            # 10
r.step           # 2
```

`toList()` materialises the range into a list. Avoid it for large ranges -
iterate with `for-in` instead.

`first()` and `last()` return `null` for empty ranges (e.g. `5..3`).

A range converts to a string via `as string` or interpolation:

```gb
io.println(r as string);         # 0..10 by 2
io.println("range: ${r}");       # range: 0..10 by 2
```

## Destructuring

```gb
let [first, second] = pair;
let {name, age} = person;

for ([key, value] in data.items()) {
    io.println(key);
}
```

Destructuring is best for small, well-known shapes. For request bodies, decoded
JSON, or optional fields, check membership first so errors are clearer:

```gb
if (person.hasKey("name")) {
    let name = person["name"];
    io.println(name);
}
```

## Match

`match` dispatches on a value, comparing it against a sequence of `case`
patterns. It works as either a **statement** or an **expression** depending
on context.

### Match expression

When assigned to a variable or used inside a larger expression, `match`
produces a value. Every branch must end with a semicolon and produce a value.
A trailing `;` after the closing `}` marks it as an expression statement:

```gb
let label = match (status) {
    case 200 => "ok";
    case 404 => "missing";
    default  => "error";
};
```

The entire `match (status) { ... }` evaluates to the value of the matched
branch. Use `default` (or `case _`) to ensure every possible input is covered;
a `MatchError` is thrown if no case matches and there is no default.

Match expressions can appear anywhere a value is expected:

```gb
io.println(match (x % 2) {
    case 0 => "even";
    default => "odd";
});
```

### Match statement

When `match` appears as a top-level statement - not assigned or used as a
value - each branch executes an action and there is no trailing `;` after `}`:

```gb
match (command) {
    case "serve"   => startServer();
    case "migrate" => runMigrations();
    default        => showHelp();
}
```

The distinction is syntactic: expression `match` is terminated by `;` after
`}` and the whole expression has a type; statement `match` is not and produces
no value.

### Multi-statement branches

Use a block body `{ ... }` when a branch needs more than one statement:

```gb
match (status) {
    case "ok" => {
        io.println("success");
        return true;
    }
    default => {
        io.println("failed");
        return false;
    }
}
```

### Guard clauses

A `when` guard filters a case with an additional boolean condition:

```gb
match (score) {
    case int n when n >= 90 => io.println("A");
    case int n when n >= 70 => io.println("B");
    default                 => io.println("C");
}
```

### Pattern matching with types

`case` can match by type, binding the value to a name if it matches:

```gb
match (value) {
    case int n    => io.println("int: " + (n as string));
    case string s => io.println("string: " + s);
    case null     => io.println("null");
    default       => io.println("other");
}
```

### Enum payload destructuring

Enum variants with associated values can be destructured in `case`:

```gb
enum Result { Ok(string), Err(string) }

let text = match (result) {
    case Result.Ok(string value)   => value;
    case Result.Err(string message) => "error: " + message;
};
```

### List patterns

A bracketed pattern matches a list of exactly that length and
binds each element. Bindings may be typed (the element must match)
or untyped (matches any value); `_` is a wildcard that skips
binding. Length mismatch and type mismatch both fall through to
the next case.

```gb
let pair = [3, 7];
let label = match (pair) {
    case [int x, int y] if (x > y)   => "first wins";
    case [int x, int y] if (x == y)  => "tie";
    case [int x, int y]              => "second wins";
    default                          => "no match";
};
```

Mixed-type rows and wildcard slots:

```gb
let mixed = ["ada", 37];
let greet = match (mixed) {
    case [string name, int age]      => name + " (" + (age as string) + ")";
    default                          => "unknown";
};

let xs = [99, 100];
let first = match (xs) {
    case [int kept, _]               => kept;     # discard second element
    default                          => 0;
};
```

A non-list value (string, dict, int, ...) never matches a list
pattern; the case falls through to whichever later case (typed,
literal, or default) can handle it.

```gb
let value = "hello";
match (value) {
    case [int a, int b] => io.println("a pair");
    case string s       => io.println("a string: " + s);
}
# prints: a string: hello
```

## Defer

`defer` registers a call to run when the surrounding function or top-level
script exits. Deferred calls run in last-in, first-out order.

```gb
func run(): void {
    defer io.println("done");
    io.println("working");
}
```

Arguments to deferred calls are evaluated when the `defer` statement is
executed.

`defer` is especially useful with files, sockets, database transactions, and
locks:

```gb
func writeLocked(any file, string text): void {
    io.lock(file);
    defer io.unlock(file);
    io.writeln(file, text);
}
```
