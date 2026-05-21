# Reflection And Testing

## Reflect

Import `reflect` for runtime metadata:

- decorators: `decorators`, `hasDecorator`, `decorator`
- functions: `function`, `parameters`, `returnType`, `doc`, `docs`
- classes: `class`, `fields`, `methods`, `staticMethods`, `parent`,
  `interfaces`, `constructors`, `typeBindings`
- interfaces: `interfaceMethods`, `interfaceParents`
- modules: `module`, `exports`
- method lookup: `method`, `staticMethod`
- values: `typeOf`

Docblocks use `##` line comments or `/** ... */` block comments immediately
before the declaration they describe. They are attached to functions, classes,
interfaces, methods, static methods, constructors, and interface method
signatures.

Use `reflect.doc(value)` to read the raw docblock for a reflected value. It
returns the doc text as a string, or `null` when the target has no docblock.
Use `reflect.docs(value)` when tooling needs a structured dictionary:
`text`, `summary`, `body`, and `lines`.

For methods, first obtain the method handle with `reflect.method()` or
`reflect.staticMethod()`. Interface method docs are exposed in the dictionaries
returned by `reflect.interfaceMethods()`.

```gb
import io;
import reflect;

## Handles the user index route.
@route("GET", "/users")
func index(): string {
    return "users";
}

io.println(reflect.hasDecorator(index, "route"));
io.println(reflect.parameters(index));
io.println(reflect.doc(index));
io.println(reflect.docs(index)["summary"]);
```

```gb
import io;
import reflect;

/**
 * User-facing controller.
 * Routes are discovered by metadata.
 */
class UserController {
    ## Lists visible users.
    func list(): string {
        return "users";
    }
}

## Implemented by values with a display name.
interface Named {
    ## Returns the display name.
    func name(): string;
}

io.println(reflect.doc(UserController));
io.println(reflect.doc(reflect.method(UserController, "list")));
io.println(reflect.doc(Named));
io.println(reflect.interfaceMethods(Named)[0]["doc"]);
```

Frameworks should prefer reflection over custom syntax when discovering routes,
middleware, services, and metadata.

### Source locations (1.0.6)

`reflect.location(target)` returns a dict `{module, line, column}` for any
function, class, or instance, or `null` when the target carries no recorded
location (native builtins). Useful for diagnostics, code-generation, and
framework error messages that want to point back at the user's source.

```gb
import io;
import reflect;

func handler(): string {
    return "ok";
}

class Service {
    int id;
    func Service(int id) { this.id = id; }
}

io.println(reflect.location(handler));   # {column: 1, line: 4, module: ""}
io.println(reflect.location(Service));   # {column: 1, line: 8, module: ""}
io.println(reflect.location(Service(1)));# {column: 1, line: 8, module: ""}
```

The `module` field is the canonical module name as imported (empty for the
entry script).

## Test

Import `test` for class-based tests. Test cases are ordinary classes that
extend `test.Test`; methods decorated with `@test` are discovered by
`test.run()`. This means test code uses the same class, decorator, module, and
visibility rules as application code.

- class: `Test`
- function: `run`

Optional lifecycle hooks are called when present:

- `setupClass()` once before the selected tests in the class
- `teardownClass()` once after the selected tests in the class
- `setup()` before each selected test method
- `teardown()` after each selected test method

Use `@tag("name")` to group tests and pass `{"tags": ["name"]}` to `test.run`
to run only matching methods.

```gb
import io;
import test;

class MathTest extends test.Test {
    int value = 0;

    func setup(): void {
        this.value = 2;
    }

    @tag("fast")
    @test
    func addition(): void {
        this.assertEquals(4, this.value + 2);
        this.assertTrue(this.value > 0);
    }

    @test
    func collections(): void {
        this.assertContains(["red", "green"], "red");
        this.assertContains({"name": "Ada"}, "name");
        this.assertNotEmpty(["ok"]);
    }
}

let result = test.run(MathTest);
io.println(result["total"]);
io.println(result["passed"]);
io.println(result["failed"]);

let fast = test.run(MathTest, {"tags": ["fast"]});
io.println(fast["total"]);
```

`test.run()` returns a dictionary:

- `total`: number of selected test methods
- `passed`: number that completed without throwing
- `failed`: number that threw an assertion or runtime error
- `failures`: list of failure strings, prefixed with the test method name

## Test Assertions

The base `test.Test` class provides assertion methods. Assertions throw on
failure, and the test runner records the thrown error as a failed test.

| Method | Meaning |
| --- | --- |
| `equal(actual, expected)` | Legacy equality assertion; kept for concise tests |
| `assertEquals(expected, actual)` | Deep equality for primitives, lists, dicts, sets, enum variants, and object fields |
| `assertEqual(expected, actual)` | Singular alias for `assertEquals` |
| `assertNotEquals(expected, actual)` | Fails when values are deeply equal |
| `assertNotEqual(expected, actual)` | Singular alias for `assertNotEquals` |
| `isTrue(value)` | Legacy boolean true assertion |
| `assertTrue(value)` | Fails unless `value` is `true` |
| `isFalse(value)` | Legacy boolean false assertion |
| `assertFalse(value)` | Fails unless `value` is `false` |
| `assertNull(value)` | Fails unless `value` is `null` |
| `notNull(value)` | Legacy non-null assertion |
| `assertNotNull(value)` | Fails when `value` is `null` |
| `assertContains(haystack, needle)` | String substring, bytes subsequence/byte, list item, dict key, or set member |
| `assertNotContains(haystack, needle)` | Inverse of `assertContains` |
| `assertEmpty(value)` | Empty string, bytes, list, dict, set, range, or `null` |
| `assertNotEmpty(value)` | Inverse of `assertEmpty` |
| `assertGreaterThan(expected, actual)` | Ordered numeric or string comparison |
| `assertGreaterThanOrEqual(expected, actual)` | Ordered numeric or string comparison |
| `assertLessThan(expected, actual)` | Ordered numeric or string comparison |
| `assertLessThanOrEqual(expected, actual)` | Ordered numeric or string comparison |
| `fail()` / `fail(message)` | Fails immediately |

Prefer the `assert...` names in new tests. The shorter legacy names remain
available because older examples and small scripts use them.

```gb
import test;

class UserTest extends test.Test {
    @test
    func profileShape(): void {
        let profile = {"name": "Ada", "roles": ["admin", "editor"]};

        this.assertEquals("Ada", profile["name"]);
        this.assertContains(profile, "roles");
        this.assertContains(profile["roles"], "admin");
        this.assertNotContains(profile["roles"], "guest");
        this.assertGreaterThan(1, profile["roles"].length());
    }
}
```

`testing.assertions` is a source module with string assertion helpers:

- `contains(text, needle)`
- `startsWith(text, prefix)`
- `endsWith(text, suffix)`
- `isBlank(text)`

These helpers return booleans and are useful in application code or custom
assertions. In tests, combine them with `this.assertTrue(...)` when you want
the runner to record a failure:

```gb
import testing.assertions as assert;
import test;

class MessageTest extends test.Test {
    @test
    func greeting(): void {
        this.assertTrue(assert.contains("hello Ada", "Ada"));
        this.assertTrue(assert.startsWith("Geblang", "Geb"));
    }
}
```
