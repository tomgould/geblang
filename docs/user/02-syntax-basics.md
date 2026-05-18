# Syntax Basics

## Comments

```gb
# line comment

/* block
   comment */
```

`//` is integer division, not a comment.

Doc comments are retained for reflection and documentation tools. Use `##` for
line docblocks or `/** ... */` for block docblocks immediately before the
declaration they describe:

```gb
## Returns the display name.
func name(): string {
    return "Ada";
}

/**
 * Handles user routes.
 */
class UserController {}
```

## Variables And Constants

```gb
string name = "Ada";
int count = 3;
let inferred = 42;
const VERSION = "0.1.0";
```

Variables are mutable. Constants cannot be reassigned.

Use `let` when the type is obvious from the initializer. Use an explicit type
when it documents an API boundary, narrows a nullable value, or prevents an
accidental wider type.

```gb
let retries = 3;
dict<string, any> payload = json.parse(text);
```

### The `any` type

`any` is Geblang's explicit opt-in for dynamic values. It is useful at
boundaries where the concrete shape is not known until runtime: decoded JSON,
HTTP request payloads, framework metadata, extension values, and generic
dictionary contents.

```gb
any value = "queued";
value = 42;
value = {"status": "done"};
```

The binding is still statically typed: its static type is `any`. That means the
analyzer allows assignments from different runtime types, but it also means you
must narrow or cast before using type-specific behaviour:

```gb
dict<string, any> payload = json.parse(text);
string name = payload["name"] as string;
int count = payload["count"] as int;
```

Prefer precise types for application logic and public APIs. Use `any` only
where a value is intentionally dynamic or where a generic container needs to
hold mixed values.

## Strings

Double-quoted strings process escape sequences and support interpolation:

```gb
string msg = "Hello ${name}!\n";
```

Any expression can appear inside `${...}`:

```gb
io.println("${a} + ${b} = ${a + b}");
io.println("count: ${items.length()}");
```

Non-string values are automatically converted to their string representation:

```gb
let n = 42;
io.println("n = ${n}");  # n = 42
```

Single-quoted strings are raw - no escape processing and no interpolation:

```gb
string raw = 'Hello ${name}\n';  # literal: Hello ${name}\n
```

Multiline strings use triple quotes:

```gb
string html = """
<h1>${name}</h1>
""";
```

Choose single-quoted strings for regex patterns, shell fragments, or examples
where backslashes should stay literal. Choose triple-quoted strings for HTML,
SQL, Markdown, and larger fixture text.

**Limitation:** double-quoted string literals inside `${...}` are not supported.
Use single-quoted strings or variables instead:

```gb
let label = "items";
io.println("${label}: ${count}");   # ok
io.println("${'items'}: ${count}"); # ok - single-quoted inside expression
```

## Numbers

Three numeric types:

```gb
int     count  = 10;
decimal money  = 12.50;
float   ratio  = 0.25f;
```

Use `int` for counts, indexes, and whole-number values. Use `decimal` for
money, measurements, and any quantity where rounding errors are unacceptable.
Use `float` for scientific or graphics calculations where IEEE 754 semantics
and performance matter more than exactness.

Integer literals support decimal, binary, octal, hexadecimal, and `_`
separators:

```gb
let flags   = 0b1010;
let mode    = 0o644;
let mask    = 0xFF;
let million = 1_000_000;
```

### The `decimal` type

`decimal` stores values as exact rational numbers (numerator/denominator
pairs). Arithmetic never rounds - `0.1 + 0.2` is exactly `0.3`, not a
floating-point approximation:

```gb
decimal a = 0.1;
decimal b = 0.2;
decimal c = a + b;
io.println(c);   # 0.3000000000 (exact)
```

Because values are stored as fractions, dividing `1` by `3` produces the
exact fraction `1/3`, not a truncated approximation:

```gb
decimal third = 1.0 / 3.0;
io.println(third);   # 0.3333333333  (displayed to 10 decimal places)
io.println(third * 3);  # 1.0000000000  (exactly 1, no rounding error)
```

The default display always uses 10 decimal places, rounding the last digit
where needed. The stored value is always exact regardless of what the display
shows.

#### Controlling decimal places

`toString(scale)` and `format(scale)` format a `decimal` to a specific number
of decimal places:

```gb
decimal price = 4.0 / 3.0;

io.println(price.toString());    # 1.3333333333  (default: 10 dp)
io.println(price.toString(2));   # 1.33
io.println(price.toString(4));   # 1.3333
io.println(price.format(2));     # 1.33  (same result; format requires scale)
```

`toString()` with no argument is equivalent to `toString(10)`. `format(scale)`
always requires the scale argument.

Casting to `string` also uses 10 decimal places:

```gb
let s = price as string;   # "1.3333333333"
```

When you need to display a currency value at exactly two places, always call
`.format(2)` or `.toString(2)` explicitly:

```gb
decimal subtotal = 19.99;
decimal tax      = subtotal * 0.2;
io.println("Tax: " + tax.format(2));   # Tax: 4.00
```

#### Rounding to an integer

`math.floor`, `math.round`, and `math.ceil` accept `decimal` and return `int`:

```gb
import math;

io.println(math.floor(2.9 as decimal));   # 2
io.println(math.round(2.5 as decimal));   # 3
io.println(math.ceil(2.1 as decimal));    # 3
```

To keep the result as a `decimal`, round to a scale and cast back:

```gb
decimal rounded = (2.567).toString(2) as decimal;   # 2.57
```

#### Mixed arithmetic

Arithmetic between `decimal` and `int` promotes the `int` to `decimal` and
returns `decimal`:

```gb
decimal price = 9.99;
int     qty   = 3;
decimal total = price * qty;   # 29.97 (exact)
```

Arithmetic between `decimal` and `float` is not directly supported - cast one
side explicitly:

```gb
decimal d = 1.5;
float   f = 2.0f;
decimal result = d * (f as decimal);
```

#### Type conversions

```gb
decimal d = 3.75;
int     i = d as int;     # 3  (truncates; error if d has fractional part and it's non-zero)
float   f = d as float;   # 3.75 (approximate; may lose precision)
string  s = d as string;  # "3.7500000000"

# Convert from string or int
decimal fromStr = "12.50" as decimal;
decimal fromInt = 7 as decimal;
```

Casting a fractional decimal to `int` truncates toward zero. If exact integer
conversion is needed, verify first:

```gb
if (d.isZero() || (d - (d as int as decimal)).isZero()) {
    int whole = d as int;
}
```

## Collections

```gb
list<int> nums = [1, 2, 3];
dict<string, int> scores = {"ada": 10, "grace": 12};
set<int> ids = {1, 2, 2, 3};
```

Indexing is zero-based. Negative indexes count from the end:

```gb
nums[0];
nums[-1];
scores["ada"];
```

Use `length()` to count the number of elements or entries in a collection:

```gb
list<int> nums = [1, 2, 3];
set<int> unique = {1, 2, 2, 3};
dict<string, int> scores = {"ada": 10, "grace": 12};

io.println(nums.length());   # 3
io.println(unique.length()); # 3
io.println(scores.length()); # 2
```

Use `isEmpty()` when you only need to know whether a collection has no
elements:

```gb
if (!nums.isEmpty()) {
    io.println(nums.first());
}
```

Use `hasKey` or `contains` for dictionary key membership:

```gb
let data = {"name": "Ada", "middle": null};
io.println(data.contains("middle")); # true
io.println(data.hasKey("missing"));  # false
```

Collection methods mutate when their name implies mutation:

```gb
nums.push(4);
nums.removeAt(0);
scores.set("linus", 7);
scores.delete("ada");
```

Use `collections.map`, `collections.filter`, and related helpers when you want
to return a transformed collection rather than update the original.

## Operators

Arithmetic, comparison, equality, boolean, bitwise, null coalescing, optional
chaining, ternary, and casts are available:

```gb
let total = price * quantity;
let ok = enabled && count > 0;
let name = maybeName ?? "anonymous";
let city = user?.address?.city;
let text = value as string;
let label = count > 0 ? "items" : "empty";
```

The ternary operator `condition ? then_expr : else_expr` is a compact inline
conditional. The condition must be a `bool` - values are never implicitly
treated as truthy or falsy:

```gb
let status = isActive ? "on" : "off";
io.println(score > 90 ? "A" : (score > 70 ? "B" : "C"));
```

Compound assignment operators are available for all binary operators:

```gb
x += 1;    x -= 1;    x *= 2;    x /= 4;    x //= 3;
x %= 10;   x **= 2;
x &= 0xff; x |= 0x01; x ^= mask; x <<= 2;   x >>= 1;
n ??= "default";   # assigns only if n is null
```

Conditions are explicit. Values are not implicitly treated as truthy or falsy:

```gb
if (name != "") {
    io.println(name);
}

if (items.length() > 0) {
    io.println(items.first());
}
```
