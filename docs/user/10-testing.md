# Testing

Geblang ships a built-in test runner and an assertion framework in the
`test` module. Tests are ordinary Geblang classes that extend `test.Test`
and mark methods with the `@test` decorator. The CLI command `geblang test`
discovers `*_test.gb` files, instantiates each test class, and runs every
`@test` method.

## A first test

```geblang
import test;

class CalculatorTest extends test.Test {
    @test
    func adds(): void {
        this.assertEquals(4, 2 + 2);
    }

    @test
    func subtracts(): void {
        this.assertEquals(0, 2 - 2);
    }
}
```

Save as `tests/calculator_test.gb` and run:

```sh
geblang test tests/calculator_test.gb
```

You can pass either a single file or a directory. Directory paths are
walked recursively for files matching `*_test.gb`.

## Assertions

`test.Test` exposes a fluent set of assertion methods. Each raises an
error with a descriptive message on failure; the runner counts the
failure and continues to the next method.

| Method | Meaning |
|--------|---------|
| `assertEquals(expected, actual)` | Deep-equal comparison |
| `assertNotEquals(unexpected, actual)` | Inverse |
| `assertTrue(value)` | `value` must be `true` |
| `assertFalse(value)` | `value` must be `false` |
| `assertNull(value)` | `value` must be `null` |
| `assertNotNull(value)` | `value` must not be `null` |
| `assertContains(container, needle)` | String/list/dict contains check |
| `assertNotContains(container, needle)` | Inverse |
| `assertEmpty(value)` | Empty string/list/dict/set/null |
| `assertNotEmpty(value)` | Inverse |
| `assertGreaterThan(threshold, actual)` | Numeric / string ordering |
| `assertGreaterThanOrEqual(threshold, actual)` | |
| `assertLessThan(threshold, actual)` | |
| `assertLessThanOrEqual(threshold, actual)` | |
| `fail([message])` | Unconditional failure |

Equality uses Geblang's value-equality rules: lists, dicts, sets, and
class instances are compared field by field; enum variants compare on
their name and payload.

## Setup and teardown

`test.Test` recognises four optional lifecycle hooks. Override any of them
on your subclass:

```geblang
class DatabaseTest extends test.Test {
    int conn;

    func setupClass(): void {
        # runs once before any @test method on this class
    }

    func setup(): void {
        # runs before every @test method
    }

    func teardown(): void {
        # runs after every @test method, even if it failed
    }

    func teardownClass(): void {
        # runs once after the last @test method
    }

    @test
    func selectsRows(): void {
        # ...
    }
}
```

## Tags and selective runs

Decorate `@test` methods with `@tag("name")` to put them into a category:

```geblang
class WebTest extends test.Test {
    @tag("integration")
    @test
    func talksToAServer(): void {
        # ...
    }

    @test
    func parsesAUrlOffline(): void {
        # ...
    }
}
```

Then on the command line:

```sh
geblang test --tag integration tests/
geblang test --tag integration --tag slow tests/
```

`geblang test` runs only methods that carry at least one of the supplied
tags. Without `--tag` it runs every `@test` method.

## Verbose output

Pass `--verbose` (or `-v`) to print each test class and method as it
runs, with `PASS` or `FAIL` per case. This is similar to PHPUnit's
testdox format:

```sh
geblang test --verbose tests/
```

```text
FunctoolsTest
  PASS pipeLeftToRight
  PASS pipeIdentityForEmpty
  PASS composeRightToLeft
  FAIL memoizeLruEvictsOldest: expected 4, got 3
tests: total=8 failed=1 passed=7
```

Without `--verbose`, only the failure lines and the summary are
printed.

## Capturing standard streams from a test

Tests that exercise code which writes to stdout, stderr, or reads
from stdin can intercept those streams in-process. The capture
helpers are evaluator-local; they do not touch the real terminal.

```gb
import io;
import test;

class GreetTest extends test.Test {
    @test
    func writesGreeting(): void {
        let capture = io.captureStdout();
        greet("Ada");
        let text = io.toString(capture);
        io.close(capture);
        this.assertTrue(text.contains("Hello, Ada"));
    }
}
```

`io.captureStdout()` and `io.captureStderr()` redirect the named
stream into a memory buffer that you read with `io.toString`. Closing
the capture restores the previous target. For more control, the
lower-level `io.redirectStdout(stream)` / `redirectStderr` /
`redirectStdin` family returns a `restore` callable; pair them with
`defer restore();` when a test temporarily swaps a stream.

See `docs/user/stdlib/01-io.md` (Streams And Capture) for the
complete API and a worked memory-stream example.

## Running the suite from `make`

If your project uses a Makefile, add:

```makefile
test-lang: build
	./geblang test tests/
```

and depend `make test` on both `test-lang` and the Go test target. The
Geblang reference repo's own `Makefile` is a working example.

## Running tests in CI

`geblang test` exits with code 0 if every test passes, 1 if any test
fails, and 2 on usage errors. The summary line prints
`tests: total=<N> failed=<M> passed=<N-M>` on stdout, so CI scripts can
both rely on the exit code and parse the summary if they need it.

## Test layout convention

The reference project structure groups tests by feature area:

```
tests/
  core/            # syntax, control flow, operators
  types/           # narrowing, aliases, optional
  generics/        # generic functions, reified runtime checks
  classes/         # inheritance, interfaces, immutability
  functions/       # closures, overloading, decorators
  async_generators/
  errors/
  regex/
  stdlib/          # one file per stdlib module
  check/           # files that must fail `geblang check`
```

Files under `check/` are not part of the regular `geblang test` run; they
exercise `geblang check`'s static diagnostics. The reference `Makefile`
has a `check-lang` target that drives a script which asserts every file
under `tests/check/` produces a diagnostic while every other test file
checks clean.

## Reified generics in tests

The runtime enforces declared element types for `list<T>`, `set<T>`, and
`dict<K,V>`. A test can confirm the enforcement:

```geblang
@test
func listRejectsWrongElement(): void {
    let threw = false;
    try {
        list<int> bad = [1, "two", 3];
    } catch (RuntimeError e) {
        threw = true;
    }
    this.assertTrue(threw);
}
```

These tests act as guards against regressions in the type checker (the
static side) and the reified-generics runtime (the dynamic side).
