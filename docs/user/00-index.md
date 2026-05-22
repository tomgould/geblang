# Geblang Reference Manual

Geblang is a general-purpose scripting language implemented in Go.

Inspired by PHP and Python (and a little bit of Go), Geblang aims to offer the simplicity and ergonomics
of those languages while making static typing, generics, async programming,
runtime metadata and modern tooling part of the core language and standard
developer experience.

To that end, Geblang combines features that will feel familiar to developers
from both ecosystems.

Features inspired by PHP include:

- C-style, curly brace syntax; no significant whitespace
- Single inheritance, multiple interfaces
- A web-first feel, with tooling and standard-library support for HTTP APIs and web apps
- A familiar set of application-focused functionality in the standard library
- Practical request/response, session, routing, database, Redis, mailer and filesystem modules
- Familiar class-based application structure for services, controllers, handlers and domain objects
- Magic methods for callable objects and property access patterns
- Simple deployment for scripts, CLI tools and web applications
- Developer ergonomics aimed at everyday application work

Features inspired by Python include:

- First-class objects for all types
- Built-in dict, list and set types
- Simple module resolution based on source paths
- Aspect-oriented programming with decorators
- Operator overloading via magic/dunder methods
- Clear, readable syntax with low boilerplate
- REPL-first exploration and tooling
- Exceptions and custom error classes for structured failure handling
- A batteries-included standard library philosophy
- Operator overloading via dunder methods
- No private, protected, public modifiers
- Immutability via `freeze` module and `@immutable` decorator for classes

Other features offered by Geblang as core language or first-party tooling include:

- Static typing with type inference, plus the special `any` type for intentionally dynamic values
- Function and method overloading - multiple signatures of the same name, resolved by argument count and type at call time
- Generics with type checking and runtime reflection, including `instanceof T` checks
- Runtime type metadata via `typeof`, type values and reflection APIs
- Native async via cooperative scheduling as core syntax
- Built-in bundling of Geblang programs into distributable binaries containing the interpreter, stdlib,
  application and dependency packages
- First-party LSP, debugger, formatter, linter/static analysis and documentation tooling
- Bundled Visual Studio Code extension for syntax highlighting, stdlib autocomplete and step debugging.
- A language design that keeps PHP/Python-style productivity while making types, metadata, packaging and
  tooling coherent from the start

Compared with e.g. TypeScript, Geblang is not a typed layer over another dynamic
runtime. Types, generics, reflection metadata, async execution, decorators,
standard-library modules and bundling are all part of the language/runtime
contract rather than erased or delegated to a separate JavaScript platform.
That gives Geblang a simpler deployment story for server-side scripts, CLI
tools and web services: the same language owns parsing, type checks, bytecode
execution, native modules, debugging and standalone executable bundling.
TypeScript remains a strong fit when you need the JavaScript and browser
ecosystem; Geblang is aimed at the places where you want scripting ergonomics
with a purpose-built backend/runtime and fewer layers between source code and
the deployed program.

## Repository

Geblang is open source under the MIT License. The repository is at https://github.com/dwgebler/geblang

## What Geblang Is For

Geblang is designed for:

- Scripts that can grow into maintainable applications
- CLI tools, automation and developer utilities
- HTTP APIs, web apps and practical web services
- Data processing, file transformation and integration jobs
- Things you'd write in PHP or Python but where you want the language benefits of static types
- Applications that want to leverage runtime metadata
- Projects that should be easy to run, test, bundle and deploy

## Philosophy

Geblang aims to offer the same simplicity for developers of languages like PHP and Python while
adding the static guarantees those ecosystems often have to recover later with
community-driven conventions, frameworks or third party static analysis tools. 

Geblang was designed from the start to deliver the web-first benefits of PHP without being
constrained by some of the historical design decisions that have held PHP back. 
Common web, CLI, data, Redis, database, HTTP, socket, Markdown, and serialization tasks are available
out of the box. The goal is to provide enough well-typed building blocks that small applications can
stay small, and larger applications can build clear framework layers on top.

Geblang favors explicitness at module and API boundaries. Imports name the
module being used, user modules export their public surface deliberately, and
type hints document the values an API expects. Dynamic features exist, but they
are intended to work with typed code rather than replace it.

### Coming From PHP

PHP developers will find Geblang's syntax familiar: semicolons, curly braces,
`class`/`extends`/`implements`, `foreach`, `match`, closures, and a rich
built-in standard library. The key differences are all additions:

| Feature              | PHP                                       | Geblang |
|----------------------|-------------------------------------------|---------|
| Static typing        | Optional type hints, loose enforcement    | Always-on, enforced at declaration and call sites |
| Generics             | Docblock comments only                    | First-class, reified - `func identity<T>(T v): T` works at runtime |
| Null safety          | All types are nullable by default         | Non-null by default; `?string` opts in to null |
| Null coalescing      | `??` operator, ?-> operator               | `??` and `?.` optional chaining |
| Interfaces           | Runtime checks only                       | Statically enforced at declaration and call sites |
| Enums                | `enum` with scalar backing                | `enum` with or without associated values, full pattern matching |
| Async/await          | Fibers (PHP 8.1+, manual)                 | First-class `async func`, `await`, `Task<T>` |
| Decorators           | Attributes (PHP 8, metadata only)         | Callable decorators that wrap functions/methods, `reflect` module |
| Module system        | `require`/`include`/`use` with namespaces | Single `import` with explicit module resolution |
| Type introspection   | `gettype()`, `instanceof`, `get_class()`  | `typeof(x)`, `x instanceof T`, `x.type`, `reflect` module |
| Generics enforcement | Not available                             | `x instanceof T` resolves `T` to its concrete bound type at runtime |
| Bytecode VM          | Zend Engine                               | Embedded Go VM - scripts, APIs, and CLI tools in one binary |
| Distribution         | PHP runtime required                      | `geblang build` produces a self-contained executable |

Geblang also drops `public`, `protected`, `private`, and `final`
modifiers on classes and members. The reasoning follows the Python
side of the inspiration: visibility modifiers exist mainly to
enforce a convention that thoughtful API design and tooling already
handle.  The discipline that visibility modifiers normally encode lives 
in `export`, `@immutable`, interface contracts and being a responsible developer.

### Coming From Python

Python developers will recognise the `import` system, list/dict comprehension
style, `async`/`await`, decorators, and the goal of staying readable at small
scale. The key differences are:

| Feature | Python | Geblang |
|---------|--------|---------|
| Static typing | Annotations + mypy (optional, bolted on) | Built-in, enforced - no separate tool needed |
| Type syntax | `Optional[T]`, `list[T]`, `Union[T, U]` | `?T`, `list<T>`, interface union constraints |
| Null safety | `None` is any type; runtime `AttributeError` | `null` is only valid where `?type` is declared |
| Generics | PEP 484 annotations, not reified | Reified - `instanceof T` and `reflect.typeBindings` work at runtime |
| Classes and OO | Duck typing, no enforced interface contracts | Formal interfaces, static method signatures, `implements` checked |
| Module imports | Implicit `__init__.py`, star imports possible | Explicit `import module;`, deliberate export surface |
| Decimal math | `decimal.Decimal` class (import required) | `decimal` is a primitive type; `3.14` is decimal by default |
| Async runtime | `asyncio`, event loop plumbing visible | `async func`, `await`, and `Task<T>` - runtime managed, no loop setup |
| Decorators | Callable wrappers only, no runtime metadata | Both callable wrappers and inspectable metadata via `reflect` |
| Type introspection | `type()`, `isinstance()`, `__class__` | `typeof(x)`, `x instanceof T`, `x.type`, consistent everywhere |
| Enums | `enum.Enum` (import, class, `.value` access) | `enum Color { Red, Green }` - first-class syntax, pattern matching |
| Generators | `yield`, `yield from`, generator expressions | `yield` in functions and closures, `generator<T>` type hint |
| Distribution | `venv`, `pip`, runtime required | `geblang build` embeds interpreter, stdlib, and source into one binary |
| Indentation sensitivity | Required (syntax-level) | Braces (no whitespace sensitivity) |

Python features that Geblang omits include comprehensions as
a distinct syntax (use `list.map`/`list.filter`), multiple inheritance, `*args`
and `**kwargs` on every function by default (but there is spread support).

## Quick Example

```gb
import io;
import collections;

interface Scored {
    func score(): int;
}

@immutable
class Player implements Scored {
    string name;
    int rating;

    func Player(string name, int rating) {
        this.name = name;
        this.rating = rating;
    }

    func score(): int {
        return this.rating;
    }
}

func topBy<T implements Scored>(list<T> items): T {
    return collections.maxBy(items, func(T x): int { return x.score(); });
}

let players = [Player("Ada", 10), Player("Grace", 12), Player("Linus", 7)];
io.println(topBy(players).name);   # Grace
```

The example shows a few of the language pieces a typical Geblang program might use:
a typed `@immutable` class with a constructor, an `interface` it
explicitly `implements`, a generic function with an interface-bounded
type parameter (`T implements Scored`) that is checked at compile time
and reified at runtime, a lambda whose `T` parameter resolves to the
outer call site's concrete bound (`Player` in this run), and a stdlib
higher-order helper (`collections.maxBy`) that takes the lambda by
value. The same code could later move `Player` into a module, hydrate
the list from a database, expose `topBy` through an HTTP handler, or
run inside an async task without changing the basic shape.

## Performance

Geblang is designed around a bytecode VM as the normal execution path, with the
tree-walking evaluator kept as a compatibility and implementation path. Use
`--trace-exec` when running a script to confirm whether the VM or evaluator was
used.

The repository includes a small benchmark harness for local performance checks:

```sh
make bench
```

The benchmark suite covers integer-heavy loops, recursive function
calls, list and dict construction, string concatenation, method
dispatch on a hot class, regex matching, JSON parse + stringify
round-trips, and lazy/eager functional collection pipelines. It
reports Geblang timings alongside equivalent Python and PHP scripts
when those runtimes are installed. There is also a version
available via Docker if you don't have PHP or Python locally:

```sh
make bench-docker
```

These numbers should be treated as loose signals only. Both PHP and
Python have made significant performance strides in recent years;
PHP in particular ships a real JIT in PHP 8, and CPython 3.11+ has
an improved interpreter. Geblang aims to be in the same ballpark as
those interpreters on realistic application code without attempting
to match the JIT.

Indicative numbers from a development machine (the absolute values
will vary):

| Benchmark          | Geblang | Python | PHP    |
|--------------------|---------|--------|--------|
| `numeric_loop`     | 131 ms  | 141 ms | 33 ms  |
| `recursive_fib`    | 75 ms   | 44 ms  | 25 ms  |
| `list_pipeline`    | 9 ms    | 17 ms  | 13 ms  |
| `string_concat`    | 9 ms    | 21 ms  | 13 ms  |
| `dict_ops`         | 18 ms   | 22 ms  | 15 ms  |
| `class_dispatch`   | 21 ms   | 23 ms  | 14 ms  |
| `regex_match`      | 66 ms   | 51 ms  | 17 ms  |
| `json_roundtrip`   | 520 ms  | 545 ms | 340 ms |
| `list_functional`  | 13 ms   | 18 ms  | 14 ms  |

### What Geblang is quick at

`list_pipeline`, `list_functional`, and `string_concat` are
allocation-heavy patterns the VM has been tuned for: building lists
with `.push`, chaining `.map` / `.filter` / `.reduce`, and
concatenating string literals into a builder in tight loops.

`numeric_loop` runs a counted `for` loop two million times with a
small if/else and integer arithmetic in the body. For loops where
the compiler can tell the variables stay as integers, Geblang puts
serious effort into making the body cheap.

`json_roundtrip` parses a 1 MB JSON payload and stringifies it back
200 times. After the 1.0.6 token-driven parser and direct encoder,
Geblang outperforms Python's C-implemented `json` module on this
workload.

`dict_ops` and `class_dispatch` exercise dict get/set and instance
method dispatch in a tight loop; both are competitive with Python.

### What it's slower at

`recursive_fib(28)` is a large number of recursive calls and not
much else; Geblang is noticeably slower than Python and PHP there.
`regex_match` is similar; regex match cost is dominated by the Go
regex engine, which trails the C runtimes of Python and PHP.

Geblang's conscious language design choices, particularly around
generics and type-safety, mean there is more runtime overhead per
function call than a dynamically-typed interpreter pays. For
everyday code (request handlers, parsing JSON, walking lists and
dicts, modest loops) the difference is hopefully invisible.

> :bulb: Geblang is a personal project, built for fun, interest, curiosity and to help
> me learn Go and a bit about compiler and interpreter design.
> It's not meant to be super fast. This is not something I've built with a
> "you should actually stop using PHP/Python and use this instead" angle.
>
> If you decide to try it out, do your own benchmarks and decide if the performance is right for you.

## Manual Chapters

- Getting started: running scripts, checking code, REPL basics, bytecode cache.
- Syntax basics: comments, declarations, expressions, strings, collections.
- Types: primitive types, nullability, casts, type aliases, generics, type
  values.
- Control flow: conditionals, loops, pattern matching, defer.
- Functions and callables: defaults, named arguments, variadic parameters,
  anonymous functions, closures, decorators.
- Classes and interfaces: constructors, inheritance, static members, magic
  methods, interfaces, enums.
- Modules and packages: imports, exports, manifests, source stdlib.
- Errors: throwing, catching, built-in error classes, stack traces.
- Async and generators: tasks, await, lazy generators, iterable type hints.
- Testing: writing `*_test.gb` files with `test.Test`, `@test`/`@tag`,
  built-in assertions, `geblang test` runner, CI integration.
- Standard library reference: native modules and bundled source modules.
- Web development: HTTP, routing, middleware, sessions, cache, SSR helpers.
- Tooling and examples: CLI commands, tests, docs, Docker builds.
- Bundling and standalone executables: `geblang build`, bundle format, package
  layout, first-run extraction, limitations.
- VS Code extension: building and installing the extension, syntax highlighting,
  live diagnostics, step debugging, launch configurations.
- Internals: pipeline overview, lexer, parser, AST, semantic analyzer, runtime
  values, bytecode format, compiler, VM, evaluator, module system, native and
  source stdlib, CLI structure.

## Status

I've released an initial 1.0. Geblang is actively evolving.
The bytecode VM is the preferred execution path when a feature is supported; 
the evaluator remains a compatibility path and an implementation aid. 
Use `--trace-exec` to see which engine ran a script.
