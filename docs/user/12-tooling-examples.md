# Tooling And Examples

## Command-Line Tools

Use `args` or `cli` for command-line interfaces:

```gb
import cli;

let parsed = cli.parseArgs(sys.args(), {
    "name": {"type": "string", "required": true},
    "verbose": {"type": "bool", "short": "v"}
});

let password = cli.password("Password: ");
```

`cli.command` provides source classes for command/option structure.

## Source API Documentation

Use `geblang doc` to generate API reference material from Geblang source
without executing the program:

```sh
go run ./cmd/geblang doc examples/api_docs.gb
go run ./cmd/geblang doc --format json examples/api_docs.gb
go run ./cmd/geblang doc --out build/api.md src
```

The command parses `.gb` files, reads docblocks, and emits declarations for
functions, classes, interfaces, type aliases, and enums. It also includes
decorator metadata, generic parameters, inheritance, class fields, class
methods, static methods, and interface method signatures.

When a file contains `export`, only exported declarations are included. This
matches package documentation expectations: private helpers can keep docblocks
for maintainers without appearing in public API output. In scripts or examples
with no `export` statements, all top-level documentable declarations are
included.

Doc comments immediately before `export` are attached to the exported symbol,
so this is valid API documentation syntax:

```gb
## Lists users for an API endpoint.
export @route("GET", "/users")
func listUsers(): list<string> {
    return ["Ada"];
}
```

Use JSON output for editor integrations, framework discovery tools, or custom
documentation pipelines. Use Markdown output when you want a quick reference
file that can be checked into project docs or converted by another site
generator.

## Static Checks

Use `geblang check` in editors and CI to validate code without executing it:

```sh
geblang check src/
geblang check --json src/
geblang check --no-lint src/
geblang check --strict src/
```

The checker parses files, runs semantic analysis, validates imports through the
normal module resolver, verifies declared module names resolve back to their
files, reports duplicate module declarations across a checked directory, and
reports lint warnings such as unused imports and unreachable statements.

It also performs cross-module symbol resolution: a `module.member` access or
`from module import name` is an error when the module does not export that
name. This works for both built-in modules and your own modules across a
multi-file project, so a typo like `io.foobar()` or an outdated API call is
caught before you run anything:

```sh
$ geblang check app.gb
app.gb:2:4: error[import]: io has no exported member foobar
```

Member checks resolve relative to each file and follow local scope, so a local
variable that shadows a module name (for example a list called `errors`) is not
mistaken for the module.

The same resolution covers methods on your own classes: calling a method that
does not exist on a typed instance's class - across its parent chain and
implemented interfaces, including classes imported from other modules - is an
error:

```sh
$ geblang check app.gb
app.gb:6:3: error[semantic]: Circle has no method bogus
```

The method check is conservative to avoid false positives: it stays silent when
the receiver's type is not a statically known class, when the class (or any
ancestor) defines `__call`, when the class carries decorators that may inject
members, or when any part of the hierarchy cannot be resolved.

`--no-lint` disables warning rules while keeping parse, semantic, import, and
module-layout validation. JSON output includes `severity`, `rule`, `file`,
`line`, `column`, and `message` fields so tools can route errors and warnings
separately.

## Module Invocation

Executable modules expose `main(args)` and can be launched with `-m`:

```sh
geblang -m app.cli --name Ada
geblang -m http.server 8080
```

This is useful for source-stdlib utilities and package-local tools because it
uses normal module resolution instead of requiring a wrapper file.

## Documentation Website

The reference manual website is generated from `docs/user/*.md`, configured
source API documentation roots, and curated runnable examples from
`docs/examples/`:

```sh
make docs
```

Static HTML is written to `docs/site/`. Open `docs/site/index.html` in a
browser or serve the directory with any static file server. By default,
`make docs` also parses `stdlib/` and adds generated API pages under
`docs/site/api/`. It also parses structured file docblocks from
`docs/examples/**/*.gb` and adds example source pages under
`docs/site/examples/`.

Override the generated API inputs when building project-local docs:

```sh
make docs DOCS_API_SRC="src examples/api_docs.gb"
```

Override the examples tree when building docs for another project:

```sh
make docs DOCS_EXAMPLES_SRC="docs/examples"
```

## Command Help

Use top-level help to list commands:

```sh
geblang --help
geblang help
```

Every command also supports local help with usage examples:

```sh
geblang test --help
geblang check --help
geblang build --help
```

## Standalone Executables

Use `geblang build` to package a Geblang application as a self-contained
binary that needs no Geblang installation on the target machine:

```sh
geblang build --entry myapp.main --out ./dist/myapp [<package-dir>]
```

The output binary embeds all source modules and precompiled bytecode. See the
[Bundling chapter](13-bundling.md) for the full package layout, how bundling
works, and complete examples.

## Docker Binary Build

Build a distributable binary and bundled stdlib through Docker:

```sh
make docker-build
```

The target writes:

```text
build/geblang
build/stdlib/
```

This build path only requires Docker and `make` on the host. It is intended for
reproducible binary builds and release packaging. Local development can use
`make build` instead.

To build both the binary and the VS Code extension together:

```sh
make compose-build
```

To build only the VS Code extension:

```sh
make vscode-build
```

See [VS Code Extension](14-vscode-extension.md) for full installation and usage
details.

## Example Scripts

Useful examples:

```sh
go run ./cmd/geblang examples/core.gb
go run ./cmd/geblang examples/functions.gb
go run ./cmd/geblang examples/classes.gb
go run ./cmd/geblang examples/generics.gb
go run ./cmd/geblang examples/generators.gb
go run ./cmd/geblang examples/collections_module.gb
go run ./cmd/geblang examples/api_docs.gb
go run ./cmd/geblang examples/markdown.gb
go run ./cmd/geblang examples/async/file.gb
go run ./cmd/geblang examples/async/io.gb
go run ./cmd/geblang examples/async/http.gb
go run ./cmd/geblang examples/async/sockets.gb
go run ./cmd/geblang examples/async/streams.gb
go run ./cmd/geblang examples/async/network_engine.gb
go run ./cmd/geblang examples/source_web_router.gb
go run ./cmd/geblang examples/web_decorators.gb
go run ./cmd/geblang examples/expense_tracker/src/main.gb
```

`examples/http_server.gb` starts a blocking HTTP server on
`127.0.0.1:8080`.

## Project Commands

```sh
make build
make test
make docs
make docker-build
make clean
```

Use `geblang init --name package.name` to create a package manifest.
