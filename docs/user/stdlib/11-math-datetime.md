# Math, Dates, And UUIDs

## Math

```gb
import math;
```

The `math` module provides numeric helpers, rounding functions, trigonometry,
roots, exponentiation, logarithms, and constants. Most functions accept `int`,
`float`, or `decimal` and return either `int` or `float` depending on the
operation.

### Rounding and clamping

`math.floor(n)`, `math.ceil(n)`, and `math.round(n)` accept any numeric value
and return `int`. `math.trunc(n)` truncates toward zero and returns `float`.

```gb
import math;

io.println(math.floor(2.9));    # 2
io.println(math.ceil(2.1));     # 3
io.println(math.round(2.5));    # 3
io.println(math.round(2.4));    # 2
io.println(math.trunc(-2.9f));  # -2.0 (float)

# floor/ceil/round also accept decimal
decimal price = 4.0 / 3.0;
io.println(math.floor(price));  # 1
io.println(math.ceil(price));   # 2
io.println(math.round(price));  # 1
```

These functions return `int` (dropping all fractional precision). When you
want to round to a number of decimal places and keep the value's type, use
the `round`, `floor`, `ceil`, and `truncate` methods on `decimal` and
`float` instead; each takes an optional precision and returns the same type
(`(2.567).round(2)` -> `2.57`). The `sign` and `clamp` helpers are also
numeric methods. See the syntax-basics chapter for details.

`math.abs(n)` returns the absolute value, preserving the type of the argument:

```gb
io.println(math.abs(-5));     # 5      (int)
io.println(math.abs(-3.7f));  # 3.7    (float)
io.println(math.abs(-2.5));   # 2.5000000000  (decimal)
```

`math.clamp(value, min, max)` constrains a value to the range `[min, max]`:

```gb
io.println(math.clamp(12, 0, 10));   # 10
io.println(math.clamp(-5, 0, 10));   # 0
io.println(math.clamp(7, 0, 10));    # 7
```

`math.min(a, ...)` and `math.max(a, ...)` accept one or more numeric arguments:

```gb
io.println(math.min(3, 1, 4, 1, 5));   # 1
io.println(math.max(3, 1, 4, 1, 5));   # 5
```

`math.sign(n)` returns `-1`, `0`, or `1` as `int`:

```gb
io.println(math.sign(-7));    # -1
io.println(math.sign(0));     # 0
io.println(math.sign(3.5f));  # 1
```

### Roots, powers, and geometry

`math.sqrt(n)`, `math.cbrt(n)` - square root and cube root, return `float`:

```gb
io.println(math.sqrt(16.0f));    # 4.0
io.println(math.cbrt(27.0f));    # 3.0
```

`math.pow(base, exponent)` - raise to a power, returns `float`:

```gb
io.println(math.pow(2.0f, 10.0f));   # 1024.0
io.println(math.pow(9.0f, 0.5f));    # 3.0
```

`math.hypot(a, b)` - length of the hypotenuse, returns `float`:

```gb
io.println(math.hypot(3.0f, 4.0f));   # 5.0
```

### Trigonometry

All trig functions work in radians and return `float`:

```gb
io.println(math.sin(math.pi() / 2.0f));   # 1.0
io.println(math.cos(0.0f));               # 1.0
io.println(math.tan(math.pi() / 4.0f));   # ~1.0

io.println(math.asin(1.0f));              # ~1.5707963...  (π/2)
io.println(math.acos(1.0f));              # 0.0
io.println(math.atan(1.0f));              # ~0.7853...      (π/4)
io.println(math.atan2(1.0f, 1.0f));       # ~0.7853...      (π/4)
```

Convert degrees to radians and back:

```gb
let deg = 90.0f;
let rad = deg * math.pi() / 180.0f;
io.println(math.sin(rad));   # 1.0

let back = rad * 180.0f / math.pi();
io.println(back);   # 90.0
```

### Logarithms and exponentials

`math.log(n)` - natural logarithm (base e), `math.log2(n)`, `math.log10(n)`,
`math.exp(n)` - all accept numeric and return `float`:

```gb
io.println(math.log(math.e()));    # 1.0
io.println(math.log2(8.0f));       # 3.0
io.println(math.log10(1000.0f));   # 3.0
io.println(math.exp(1.0f));        # ~2.71828...
```

### Constants

`math.pi()` and `math.e()` return the constant as `float`. Geblang 1.6.0
adds the following additional zero-arg constant functions:

| Constant | Value |
|----------|-------|
| `math.tau()` | `2 * pi` |
| `math.ln2()` | natural log of 2 |
| `math.ln10()` | natural log of 10 |
| `math.sqrt2()` | square root of 2 |
| `math.phi()` | golden ratio `(1 + sqrt(5)) / 2` |
| `math.sqrt2Pi()` | sqrt(2 * pi); used in normal-distribution formulas |
| `math.log2Pi()` | log(2 * pi) |
| `math.maxInt()` | largest representable int64 |
| `math.minInt()` | smallest representable int64 |
| `math.maxFloat()` | largest finite float64 |
| `math.minFloat()` | smallest positive non-zero float64 |
| `math.epsilon()` | smallest float `eps` such that `1 + eps != 1` |

```gb
io.println(math.pi());        # 3.141592653589793
io.println(math.tau());       # 6.283185307179586
io.println(math.phi());       # 1.618033988749895
io.println(math.maxInt());    # 9223372036854775807
```

`math.inf()` returns positive infinity. `math.nan()` returns a not-a-number
value. Use `math.isNaN(n)` and `math.isInf(n)` to test:

```gb
let inf = math.inf();
let nan = math.nan();

io.println(math.isInf(inf));   # true
io.println(math.isNaN(nan));   # true
io.println(math.isNaN(42.0f)); # false
io.println(inf > 1e308f);      # true
```

`math.isPrime(n)` tests an integer for primality. Returns `false` for
`n < 2`. Uses Baillie-PSW plus 20 rounds of Miller-Rabin under the
hood, so it's deterministic for inputs that fit in an `int64` and
effectively certain for larger values.

```gb
io.println(math.isPrime(2));       # true
io.println(math.isPrime(97));      # true
io.println(math.isPrime(1000003)); # true
io.println(math.isPrime(1));       # false
io.println(math.isPrime(561));     # false (Carmichael number)
```

### Statistics

Aggregate stats over a numeric list. `percentile` / `quantile` use R's
type-7 linear-interpolation algorithm - the most common default
across numpy, pandas, R, and Excel.

| Function | Returns | Description |
|----------|---------|-------------|
| `math.median(xs)` | `float` | 50th percentile, equivalent to `math.quantile(xs, 0.5f)`. |
| `math.percentile(xs, p)` | `float` | p-th percentile, `p` in `[0, 100]`. |
| `math.quantile(xs, q)` | `float` | q-quantile, `q` in `[0, 1]`. |
| `math.mode(xs)` | `float` | Most-frequent value; ties broken by lowest value (deterministic). |

```gb
let xs = [0, 10, 20, 30, 40];
io.println(math.median(xs));            # 20
io.println(math.percentile(xs, 25));    # 10
io.println(math.percentile(xs, 75));    # 30
io.println(math.mode([1, 1, 2, 2, 3])); # 1
```

---

## Datetime

```gb
import datetime;
```

The `datetime` module works with two representations:

- **Unix seconds** (`int`) - a raw integer timestamp. Lightweight and
  interoperable. Most module-level functions take and return Unix seconds.
- **`Instant`** - an object with chainable methods. Useful when you need to
  apply several operations in sequence without passing the timestamp through
  every call.

Both representations are exact to the second. Sub-second precision is not
currently supported.

### Current time

```gb
import datetime;

let ts = datetime.nowUnix();             # int: current Unix seconds
let parts = datetime.now();              # dict with year, month, day, etc.
let instant = datetime.nowInstant();     # Instant object

io.println(ts);
io.println(parts["year"]);
io.println(instant.formatRFC3339());
```

`datetime.now()` returns a dict with these keys:

| Key | Type | Meaning |
|---|---|---|
| `timestamp` | int | Unix seconds |
| `year` | int | Full year, e.g. 2025 |
| `month` | int | 1-12 |
| `day` | int | 1-31 |
| `hour` | int | 0-23 |
| `minute` | int | 0-59 |
| `second` | int | 0-59 |
| `weekday` | int | 0 (Sunday) - 6 (Saturday) |
| `zone` | string | IANA timezone name (`"UTC"` by default) |

### Timezone-aware now

`datetime.now(zoneName)` returns the same parts dict shifted into the
given IANA timezone (e.g. `"Europe/London"`, `"America/New_York"`,
`"Asia/Tokyo"`). The `zone` key reflects the requested timezone:

```gb
let london = datetime.now("Europe/London");
io.println(london["hour"], london["zone"]);
```

### Parts at a specific timezone for a known unix timestamp

`datetime.partsInZone(unixSeconds, zoneName)` is the equivalent
constructor when you already have a Unix timestamp:

```gb
let parts = datetime.partsInZone(1700000000, "America/New_York");
io.println(parts["hour"]);   # 17 (5pm EST)
```

### HTTP-date / RFC1123 formatting

`datetime.formatHTTP(unixSeconds)` produces an RFC1123 GMT timestamp
suitable for HTTP headers (`Last-Modified`, `Date`, `Expires`,
`Set-Cookie expires=`):

```gb
io.println(datetime.formatHTTP(1700000000));
# Tue, 14 Nov 2023 22:13:20 GMT
```

### Constructing timestamps

`datetime.make(year, month, day)` or `datetime.make(year, month, day, hour, minute, second)` builds a Unix timestamp from components:

```gb
let ts = datetime.make(2025, 12, 25);            # midnight UTC
let ts2 = datetime.make(2025, 12, 25, 9, 30, 0); # 09:30 UTC
```

`datetime.unix(seconds)` converts a Unix timestamp to an RFC3339 string:

```gb
io.println(datetime.unix(0));   # 1970-01-01T00:00:00Z
```

`datetime.Instant(ts_or_string)` wraps a Unix timestamp or RFC3339 string as
an `Instant` object:

```gb
let a = datetime.Instant(1735128600);                # from Unix seconds
let b = datetime.Instant("2025-12-25T09:30:00Z");   # from RFC3339
```

`datetime.Duration(seconds)` wraps a number of seconds as a `Duration` object:

```gb
let oneDay = datetime.Duration(86400);
let oneHour = datetime.Duration(3600);
```

`datetime.Zone(name)` creates a `Zone` object from an IANA timezone name:

```gb
let london = datetime.Zone("Europe/London");
let tokyo  = datetime.Zone("Asia/Tokyo");
```

### Parsing and formatting

`datetime.parse(s)` and `datetime.parseRFC3339(s)` both parse an RFC3339
string and return Unix seconds:

```gb
let ts = datetime.parse("2025-06-01T12:00:00Z");
let ts2 = datetime.parseRFC3339("2025-06-01T12:00:00Z");
```

`datetime.format(unixSeconds, layout)` formats a timestamp using a Go-style
layout string. The reference time is `2006-01-02T15:04:05Z07:00`:

```gb
io.println(datetime.format(ts, "2006-01-02"));           # 2025-06-01
io.println(datetime.format(ts, "2006-01-02 15:04:05"));  # 2025-06-01 12:00:00
io.println(datetime.format(ts, "Jan 2, 2006"));          # Jun 1, 2025
io.println(datetime.format(ts, "3:04 PM"));              # 12:00 PM
```

Convenience formatters:

```gb
io.println(datetime.formatRFC3339(ts));  # 2025-06-01T12:00:00Z
io.println(datetime.formatDate(ts));     # 2025-06-01
io.println(datetime.formatTime(ts));     # 12:00:00
```

`datetime.toUtc(ts)` and `datetime.toLocal(ts, tz)` format to RFC3339, with
`toLocal` converting to the given timezone:

```gb
let tokyo = "Asia/Tokyo";
io.println(datetime.toLocal(ts, tokyo));   # 2025-06-01T21:00:00+09:00
io.println(datetime.toUtc(ts));            # 2025-06-01T12:00:00Z
```

The timezone can be a string name or a `datetime.Zone` object.

### Arithmetic with Unix seconds

All arithmetic functions take and return Unix timestamps (`int`):

```gb
let now = datetime.nowUnix();
let tomorrow   = datetime.addDays(now, 1);
let nextMonth  = datetime.addMonths(now, 1);
let nextYear   = datetime.addYears(now, 1);
let inOneHour  = datetime.addSeconds(now, 3600);
```

`datetime.diff(start, end)` returns a dict:

```gb
let start = datetime.make(2025, 1, 1);
let end   = datetime.make(2025, 6, 15);
let d = datetime.diff(start, end);

io.println(d["days"]);     # 165
io.println(d["hours"]);    # 0
io.println(d["minutes"]);  # 0
io.println(d["seconds"]);  # 0
```

The `diff` result is always non-negative; the order of arguments does not
matter.

### The `Instant` class

`Instant` is an object-oriented interface to the same timestamp. Methods chain
and always return `Instant` or a value - they never mutate in place.

```gb
let appt = datetime.nowInstant()
    .addDays(7)
    .addSeconds(3600);

io.println(appt.formatRFC3339());   # one week and one hour from now
io.println(appt.unix());            # as Unix seconds
```

**Formatting methods:**

```gb
let i = datetime.Instant("2025-12-25T09:30:00Z");

i.toString();           # "2025-12-25T09:30:00Z"  (same as formatRFC3339)
i.formatRFC3339();      # "2025-12-25T09:30:00Z"
i.toUtc();              # "2025-12-25T09:30:00Z"
i.format("Jan 2, 2006 15:04");          # "Dec 25, 2025 09:30"
i.toLocal("America/New_York");          # "2025-12-25T04:30:00-05:00"
```

**Arithmetic methods** (each returns a new `Instant`):

```gb
i.add(datetime.Duration(3600));   # add one hour
i.addSeconds(3600);               # same
i.addDays(1);
i.addMonths(1);
i.addYears(1);
```

**Components and comparison:**

```gb
i.unix();    # int: Unix seconds
i.parts();   # dict with year, month, day, hour, minute, second, weekday, timestamp

let diff = a.diff(b);   # Duration (absolute difference)
```

Full chained example - schedule a reminder for 9 AM next Monday:

```gb
import datetime;

func nextMonday(Instant from): Instant {
    let parts = from.parts();
    let daysUntilMonday = (8 - parts["weekday"]) % 7;
    if (daysUntilMonday == 0) { daysUntilMonday = 7; }
    return from.addDays(daysUntilMonday)
               .add(datetime.Duration(9 * 3600 - (parts["hour"] * 3600 + parts["minute"] * 60 + parts["second"])));
}

let reminder = nextMonday(datetime.nowInstant());
io.println(reminder.formatRFC3339());
```

### The `Duration` class

`datetime.Duration(seconds)` wraps a number of seconds. Use it with
`Instant.add()` for semantic arithmetic.

```gb
let d = datetime.Duration(90061);   # 1 day + 1 hour + 1 minute + 1 second

io.println(d.seconds());   # 90061
io.println(d.toString());  # "90061s"

let parts = d.toDict();
io.println(parts["days"]);     # 1
io.println(parts["hours"]);    # 1
io.println(parts["minutes"]);  # 1
io.println(parts["seconds"]);  # 1
```

**Predefined durations** using `datetime.Duration`:

```gb
let oneMinute = datetime.Duration(60);
let oneHour   = datetime.Duration(3600);
let oneDay    = datetime.Duration(86400);
let oneWeek   = datetime.Duration(604800);
```

### The `Zone` class

`datetime.Zone(name)` validates and wraps an IANA timezone identifier:

```gb
let tz = datetime.Zone("America/Los_Angeles");

io.println(tz.name());           # "America/Los_Angeles"
io.println(tz.toString());       # "America/Los_Angeles"

let now = datetime.nowInstant();
let offset = tz.offsetAt(now);   # offset in seconds, e.g. -28800 (UTC-8)
io.println(offset / 3600);       # -8
```

`Zone.offsetAt(instant)` returns the UTC offset in seconds at the given instant
(accounts for DST):

```gb
let summerTs  = datetime.Instant("2025-07-01T00:00:00Z");
let winterTs  = datetime.Instant("2025-01-01T00:00:00Z");
let la = datetime.Zone("America/Los_Angeles");

io.println(la.offsetAt(summerTs) / 3600);   # -7  (PDT)
io.println(la.offsetAt(winterTs) / 3600);   # -8  (PST)
```

### Labels

```gb
io.println(datetime.weekdayName(0));   # Sunday  (0=Sunday, 1=Monday, …)
io.println(datetime.weekdayName(1));   # Monday
io.println(datetime.monthName(1));     # January
io.println(datetime.monthName(12));    # December
```

Pull the day name from a timestamp:

```gb
let parts = datetime.now();
io.println(datetime.weekdayName(parts["weekday"]));
io.println(datetime.monthName(parts["month"]));
```

### Sleeping

`datetime.sleep(ms)` pauses execution for the given number of milliseconds:

```gb
datetime.sleep(1000);   # wait 1 second
```

### Complete example

```gb
import datetime;
import io;

# Build a human-readable countdown to a deadline
func countdown(string deadline): string {
    let target = datetime.Instant(deadline);
    let now    = datetime.nowInstant();
    let diff   = now.diff(target);
    let parts  = diff.toDict();

    if (parts["days"] > 0) {
        return "${parts["days"]} days, ${parts["hours"]} hours";
    }
    if (parts["hours"] > 0) {
        return "${parts["hours"]} hours, ${parts["minutes"]} minutes";
    }
    return "${parts["minutes"]} minutes";
}

io.println(countdown("2026-01-01T00:00:00Z"));
```

---

## Time - Elapsed durations

```gb
import time;
```

The `time` module exposes simple monotonic-style timing primitives for
measuring elapsed wall-clock durations, throttling, debouncing and
blocking sleeps. It is distinct from `datetime`, which models
calendar-aware moments in time.

Reach for `time` when you want to time how long something took, profile
a block, throttle a loop, or pause execution synchronously. Reach for
`datetime` when you need a zone-aware timestamp or a formatted date.

### Functions

| Function | Returns | Description |
|----------|---------|-------------|
| `time.now()` | `int` | Wall-clock milliseconds since the Unix epoch. Good for timestamps; can jump backwards on clock correction, so do not use it to measure durations. |
| `time.monotonic()` | `int` | Monotonic milliseconds since process start; never decreases (1.7.0). The correct source for measuring durations, timeouts, and TTLs - immune to wall-clock jumps. |
| `time.elapsed(start)` | `int` | Convenience for `time.now() - start`. Returns milliseconds elapsed since the `start` value (also in ms). |
| `time.sleep(ms)` | `null` | Pauses the current thread for `ms` milliseconds. Use `async.sleep` instead inside async tasks where you want cooperative scheduling. |

```gb
import time;
import io;

let start = time.now();
# ... work ...
io.println("took " + (time.elapsed(start) as string) + " ms");
```

A stopwatch is just two values:

```gb
let started = time.now();
let lap     = started;
# ... work block A ...
io.println("lap A: " + (time.elapsed(lap) as string) + " ms");
lap = time.now();
# ... work block B ...
io.println("lap B: " + (time.elapsed(lap) as string) + " ms");
io.println("total: " + (time.elapsed(started) as string) + " ms");
```

### Unix time precision

`time.now()` returns milliseconds since the Unix epoch, matching most
JS / Node code. For PHP / Python ergonomics and sub-millisecond
precision, the `time` module also exposes:

| Function | Returns | Equivalent |
|----------|---------|------------|
| `time.unix()` | `int` whole seconds | PHP `time()`, `int(time.time())` |
| `time.unixMilli()` | `int` milliseconds | alias of `time.now()` |
| `time.unixMicro()` | `int` microseconds | |
| `time.unixNano()` | `int` nanoseconds | Python `time.time_ns()` |
| `time.unixFloat()` | `float` fractional seconds | PHP `microtime(true)`, Python `time.time()` |
| `time.unixDecimal()` | `decimal` lossless seconds | full nanosecond precision |
| `time.elapsedFloat(start)` | `float` seconds elapsed | float-seconds analogue of `time.elapsed` |

Pick by what your data looks like:

- **Whole seconds**: `time.unix()` (or the existing `datetime.nowUnix()`,
  same value).
- **Web-tier wall clock / 99% of cases**: `time.now()` /
  `time.unixMilli()`. Whole-millisecond timestamps log cleanly and
  fit in `int` without precision concerns.
- **Microbenchmarks**: `time.unixMicro()` or `time.unixNano()` -
  integer math, no float rounding.
- **Sub-second wall clock you'll log or do math on**:
  `time.unixFloat()`. Float64 covers microsecond precision; sub-
  microsecond bits round off.
- **Lossless for cryptographic timestamps / cross-host correlation**:
  `time.unixDecimal()`. Backed by `(seconds * 1e9 + nanos) / 1e9`
  via `decimal`'s big.Rat, so nanosecond precision survives
  arithmetic.

```gb
import time;
import io;

let start = time.unixFloat();
# ... fast work ...
io.println("took " + (time.elapsedFloat(start) as string) + " s");

# Lossless when nanoseconds matter:
let ts = time.unixDecimal();   # e.g. 1779882656.123456789
```

Everything else in Geblang (the `time.scheduler` Timer / Ticker /
Interval, `async.sleep`, HTTP / DB / SSH `timeoutMs`, and gebweb's
job scheduler) continues to use milliseconds.

---

## UUID

```gb
import uuid;
```

The `uuid` module generates and validates UUIDs (Universally Unique
Identifiers). All functions return standard lowercase hyphenated strings
(`xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`).

### Generating random UUIDs

`uuid.v4()` - random UUID, no inherent ordering:

```gb
let id = uuid.v4();
io.println(id);   # e.g. f47ac10b-58cc-4372-a567-0e02b2c3d479
```

`uuid.v7()` - random UUID with a millisecond-precision timestamp prefix. Use
this when you want UUIDs that sort chronologically:

```gb
let id = uuid.v7();
io.println(id);   # e.g. 018f9ca2-3c40-7b4d-9e1a-5bfce2a58841
```

`uuid.v1()` - time-based UUID using the current time and MAC address (or a
random node identifier). Less portable than v7 for new designs:

```gb
let id = uuid.v1();
```

### Namespace UUIDs (v3 and v5)

`uuid.v3(namespace, name)` produces a deterministic UUID using MD5 hashing.
`uuid.v5(namespace, name)` uses SHA-1. Both accept a namespace UUID string and
a name string:

```gb
let ns  = uuid.namespaceDNS();
let id3 = uuid.v3(ns, "example.com");
let id5 = uuid.v5(ns, "example.com");

io.println(id3);   # 9073926b-929f-31c2-abc9-fad77ae3e8eb  (always the same)
io.println(id5);   # 2ed6657d-e927-568b-95e3-af9a23cb4481  (always the same)
```

The same namespace and name always produce the same UUID. Use v5 in new
code - SHA-1 produces better distribution than MD5.

**Predefined namespaces:**

```gb
uuid.namespaceDNS();    # 6ba7b810-9dad-11d1-80b4-00c04fd430c8
uuid.namespaceURL();    # 6ba7b811-9dad-11d1-80b4-00c04fd430c8
uuid.namespaceOID();    # 6ba7b812-9dad-11d1-80b4-00c04fd430c8
uuid.namespaceX500();   # 6ba7b814-9dad-11d1-80b4-00c04fd430c8
```

Custom namespaces can be any UUID string:

```gb
let myNs  = uuid.v4();   # generate once and persist it
let docId = uuid.v5(myNs, "invoice-2025-001");
```

### Parsing and validation

`uuid.parse(s)` normalises a UUID string (lowercase, hyphenated). Throws if
the input is not a valid UUID:

```gb
let id = uuid.parse("F47AC10B-58CC-4372-A567-0E02B2C3D479");
io.println(id);   # f47ac10b-58cc-4372-a567-0e02b2c3d479
```

`uuid.isValid(s)` returns `true` if the string is a valid UUID:

```gb
io.println(uuid.isValid("f47ac10b-58cc-4372-a567-0e02b2c3d479"));   # true
io.println(uuid.isValid("not-a-uuid"));                              # false
```

`uuid.nil()` returns the nil UUID (all zeros):

```gb
io.println(uuid.nil());   # 00000000-0000-0000-0000-000000000000
```

### Binary conversion

`uuid.toBytes(s)` converts a UUID string to a 16-byte `bytes` value.
`uuid.fromBytes(b)` does the reverse:

```gb
let id    = uuid.v4();
let raw   = uuid.toBytes(id);      # 16-byte bytes value
let back  = uuid.fromBytes(raw);   # round-trips to the same string
io.println(id == back);   # true
```

This is useful when storing UUIDs as binary in databases or passing them as
binary extension payloads.

### ULID

`uuid.ulid()` generates a ULID (Universally Unique Lexicographically Sortable
Identifier). A ULID encodes a 48-bit millisecond timestamp and 80 bits of
randomness as a 26-character Crockford base32 string:

```gb
let id = uuid.ulid();
io.println(id);   # e.g. 01ARZ3NDEKTSV4RRFFQ69G5FAV
```

ULIDs sort correctly as plain strings, are case-insensitive, and carry no
dashes. Two ULIDs generated in the same millisecond have the same timestamp
prefix but differ in the random suffix.

### Choosing a UUID version

| Version | Use when |
|---|---|
| `v4` | You need a random ID with no ordering or determinism requirements |
| `v7` | You need random IDs that sort chronologically (primary keys, event streams) |
| `v5` | You need the same ID every time for the same input (content addressing, idempotent inserts) |
| `v3` | Legacy systems that require MD5-based namespace UUIDs |
| `v1` | Interoperability with systems that require time-and-MAC UUIDs |
| `ulid` | You want sortable, URL-safe, human-readable IDs without dashes |

## Cron - schedule expressions (1.6.0)

The `cron` module parses standard 5-field cron expressions and
computes their next firing times. Hand-rolled, no Go dependency.

```gb
import cron;
import time;

if (cron.isValid(userSpec)) {
    let next = cron.nextAfter(userSpec, time.unix());
    io.println("next fires at " + (next as string));
}
```

### Functions

| Function | Returns | Description |
|----------|---------|-------------|
| `cron.parse(spec)` | `dict<string, any>` | `{spec, special, minute, hour, dayOfMonth, month, dayOfWeek}`. Each field is a sorted list of valid integers. `special` is the shortcut name (e.g. `"@daily"`) or `null`. Throws on malformed input. |
| `cron.isValid(spec)` | `bool` | Cheap parse check; returns `false` instead of throwing. |
| `cron.nextAfter(spec, t)` | `int` | The next firing time **strictly after** unix-seconds `t`. Throws if no firing falls within 5 years (a guard against pathological specs like Feb 30). |
| `cron.nextN(spec, t, n)` | `list<int>` | The next `n` firing times in ascending order. |

### Field syntax

Standard Vixie cron form: `minute hour day-of-month month day-of-week`.

| Field | Range | Names accepted |
|-------|-------|----------------|
| minute | 0-59 | - |
| hour | 0-23 | - |
| day-of-month | 1-31 | - |
| month | 1-12 | `jan`-`dec` (case-insensitive) |
| day-of-week | 0-6 (Sun-Sat) | `sun`-`sat` (case-insensitive); `7` is also Sunday |

Each field accepts:

- `*` for "every value"
- A single integer or name
- Comma-separated lists: `1,15,30`
- Ranges: `8-18`
- Step expressions: `*/15`, `0-30/5`, `5/3` (last form means "from 5 to max, step 3")

Day-of-month and day-of-week use **OR** semantics when both are
restricted: a date matches if **either** field matches (this is
Vixie cron's behaviour; `0 0 1 * 1` fires on the 1st of every
month and on every Monday).

### Special strings

| Spec | Equivalent |
|------|------------|
| `@hourly` | `0 * * * *` |
| `@daily` (or `@midnight`) | `0 0 * * *` |
| `@weekly` | `0 0 * * 0` |
| `@monthly` | `0 0 1 * *` |
| `@yearly` (or `@annually`) | `0 0 1 1 *` |
| `@reboot` | **rejected** - it has no scheduled firing time |
