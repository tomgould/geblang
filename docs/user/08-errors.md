# Errors

## Throwing

Only objects extending `Error` can be thrown.

```gb
throw RuntimeError("failed");
```

Throw errors when the caller can reasonably recover or report a domain-specific
failure. Use return values for ordinary negative results such as "not found"
when absence is expected.

## Catching

```gb
try {
    risky();
} catch (IOError e) {
    io.println("io failed: " + e.message);
} catch (Error e) {
    io.println("unexpected: " + e.message);
} finally {
    cleanup();
}
```

`finally` runs after the `try`/`catch` path completes.

Catch clauses are checked in order. Put specific error classes before broader
base classes. `catch (Error e)` acts as a catch-all for any throwable error.
An untyped `catch { ... }` block also catches everything but does not bind the
error value.

Geblang converts recoverable host/runtime failures into language-level
exceptions. For example, file and network failures such as a missing file,
connection refusal, or trying to bind a port that is already in use are raised
as `IOError` and can be caught. Unrecoverable interpreter and bytecode integrity
failures still abort execution because they indicate an implementation or
corrupt-program problem rather than an application error.

```gb
import io;
import net;

let listener = net.listenTcp("127.0.0.1:0");
let addr = net.localAddr(listener);

try {
    let other = net.listenTcp(addr);
    net.close(other);
} catch (IOError e) {
    io.println("could not bind: " + e.message);
} finally {
    net.close(listener);
}
```

## Custom Error Classes

Define application error classes by extending `Error` or any built-in error
subclass:

```gb
class AppError extends Error {}
class NetworkError extends RuntimeError {}

try {
    throw AppError("something failed");
} catch (NetworkError e) {
    io.println("network: " + e.message);
} catch (AppError e) {
    io.println("app: " + e.message);
} catch (Error e) {
    io.println("other: " + e.message);
}
```

User error classes take an optional string message argument:

```gb
throw AppError();                     # no message
throw AppError("connection refused"); # with message
```

Caught error values expose `class` and `message` fields:

```gb
} catch (AppError e) {
    io.println(e.class + ": " + e.message);
}
```

Error class inheritance is fully hierarchical. A class extending `RuntimeError`
is also caught by `catch (Error e)`:

```gb
class DatabaseError extends RuntimeError {}
class QueryError extends DatabaseError {}

try {
    throw QueryError("syntax error near SELECT");
} catch (DatabaseError e) {
    io.println("db problem: " + e.message);
}
```

### Custom fields on error classes

Error classes can declare fields and a constructor. The constructor calls
`parent(msg)` to set the error message and then sets any custom fields on
`this`:

```gb
class HttpError extends RuntimeError {
    int code;
    func HttpError(int code, string msg) {
        parent(msg);
        this.code = code;
    }
}

try {
    throw HttpError(404, "page not found");
} catch (HttpError e) {
    io.println(e.code as string);    # 404
    io.println(e.message);           # page not found
}
```

Custom fields are accessible on the caught error by name just like `message`
and `class`.

## Built-In Error Classes

| Class | Description |
|-------|-------------|
| `Error` | Base class for all errors |
| `RuntimeError` | General runtime failures |
| `TypeError` | Type mismatch errors |
| `ValueError` | Invalid value errors |
| `IOError` | File and network I/O errors |
| `ParseError` | Parsing failures |
| `MatchError` | Non-exhaustive match |

## The `errors` Module

```gb
import errors;

# Check error class hierarchy
let err = AppError("bad input");
io.println(errors.class(err));            # AppError
io.println(errors.message(err));          # bad input
io.println(errors.is(err, "Error"));      # true
io.println(errors.is(err, "TypeError"));  # false

# Create an error from a class name string
let dyn = errors.new("AppError", "dynamic");

# Wrap an error with additional context
let wrapped = errors.wrap(err, "request failed");
io.println(wrapped.message);  # request failed: bad input
```

`errors.is` performs a full hierarchy-aware check. It traverses user-defined
class chains as well as the built-in error hierarchy.

### Structured stack traces

Caught errors keep stack information. Use `errors.stackTrace(e)` or
`e.stackTrace()` to get an `errors.StackTrace` value instead of parsing the
formatted uncaught-error text yourself:

```gb
import errors;
import io;

func inner(): void {
    throw RuntimeError("boom");
}

func outer(): void {
    inner();
}

try {
    outer();
} catch (RuntimeError e) {
    let trace = e.stackTrace();
    let first = trace.first();

    io.println(errors.hasStackTrace(e)); # true
    io.println(trace.length() > 0);      # true
    io.println(first.function());        # inner
    io.println(first.line() > 0);        # true
}
```

`errors.StackTrace` methods:

| Method | Returns | Description |
|--------|---------|-------------|
| `frames()` | `list<errors.Frame>` | Structured frame values |
| `length()` | `int` | Number of frames |
| `first()` | `errors.Frame|null` | Innermost frame, or `null` when empty |
| `toList()` | `list<dict>` | Plain dictionaries with `function` and `line` |
| `toString()` | `string` | Original formatted stack text |

`errors.Frame` methods:

| Method | Returns | Description |
|--------|---------|-------------|
| `function()` | `string` | Function name, or `<top level>` |
| `line()` | `int` | Source line when available, otherwise `0` |
| `toDict()` | `dict` | `{function, line}` |
| `toString()` | `string` | Human-readable single frame |

For dictionary-oriented code, `errors.frames(e)` is shorthand for
`errors.stackTrace(e).toList()`.

## Runtime Failures

Most evaluator runtime failures, such as invalid operations, unknown fields,
bad argument types, and division by zero, are raised as catchable
`RuntimeError` exceptions. Parse, semantic, startup, and host-level internal
failures are reported directly because the script has not reached a recoverable
runtime point.

## Stack Traces

Uncaught runtime errors include source-aware stack information when available.
Caught errors expose the same information through `errors.StackTrace` and
`errors.Frame`, which is more stable for logging, testing, and tooling than
parsing the displayed uncaught-error output.

When you convert low-level failures into domain errors, preserve the useful
message:

```gb
func loadProfile(string path): dict<string, any> {
    try {
        return json.parse(io.readText(path));
    } catch (Error e) {
        throw RuntimeError("could not load profile " + path + ": " + e.message);
    }
}
```
