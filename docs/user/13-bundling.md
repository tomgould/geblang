# Bundling And Standalone Executables

`geblang build` produces a self-contained binary from a Geblang package. The
resulting file runs on any machine with the same OS and architecture as the
machine that built it. No Geblang installation, no stdlib directory, and no
`geblang.yaml` manifest are required on the target machine.

## How Bundling Works

A Geblang bundle is a standard executable with a zip archive appended to it.

```
┌──────────────────────────────────────────────────┐
│  geblang interpreter binary (ELF/Mach-O/PE)      │
├──────────────────────────────────────────────────┤
│  zip archive                                      │
│    BUNDLE.json          manifest                  │
│    src/app/main.gb      source files              │
│    src/app/main.gbc     precompiled bytecode      │
│    stdlib/collections.gb  bundled source stdlib   │
│    stdlib/collections.gbc                         │
│    ...                                            │
├──────────────────────────────────────────────────┤
│  8-byte zip size (little-endian uint64)           │
│  4-byte magic: GEBX                               │
└──────────────────────────────────────────────────┘
```

At startup the interpreter checks whether a 12-byte trailer is present and
whether it contains the `GEBX` magic value. If so, it reads the zip, parses
`BUNDLE.json`, and runs the bundled entry module instead of looking for
command-line arguments.

The zip archive is a standard zip file and can be inspected with `unzip -l` or
any zip reader.

## What Gets Bundled

`geblang build` walks the import graph starting from the entry module and
collects every non-native module it can reach:

- **User source modules** are included under `src/` inside the zip.
- **Imported package dependencies** declared in `geblang.yaml` are included
  when they are installed under `vendor/<name>/` and reached by the import
  graph.
- **Source-stdlib modules** (Geblang-written standard library files) are
  included under `stdlib/` inside the zip.
- **Native modules** - Go-backed modules like `io`, `sys`, `collections`,
  `http`, `db`, and so on - are part of the interpreter binary itself and are
  not duplicated in the zip.

Each collected source file is also compiled to bytecode and stored alongside it
as a `.gbc` file. The precompiled bytecode is loaded directly into the
interpreter's bytecode cache on first run, so the bundled program starts at
full speed with no warm-up compilation step.

Only imported modules are bundled. `geblang build` does not copy the entire
package directory, the entire `vendor/` directory, test files, or generated
artifacts. If a package dependency is declared but no module from that package
is imported, it is not included in the bundle. Non-code files (templates, static
assets, data files) are embedded only when listed under `resources:` in the
manifest; see [Embedding Resources](#embedding-resources).

## Embedding Resources

Programs often ship non-code files alongside their source: HTML templates,
static assets, data files. List them under `resources:` in `geblang.yaml` and
`geblang build` embeds them in the bundle ZIP at their project-relative path,
next to `src/` and `stdlib/`.

```yaml
name: myapp
source: src
resources:
  - templates        # a directory: embedded recursively
  - static           # another directory
  - data/*.json      # a glob: matched files only
```

Each entry is either a directory (embedded recursively) or a glob pattern. A
pattern that matches nothing is a build error, so a typo fails loudly rather
than silently shipping an empty bundle. Resource paths may not collide with the
reserved `src/` or `stdlib/` bundle directories.

At run time a program finds its embedded files through `sys.bundleDir()`, which
returns the bundle's extract directory (see [First-Run
Extraction](#first-run-extraction)) or the empty string when the program is not
running from a bundle. Resolve resources against it, falling back to the project
directory in development, so the same code path works in both cases:

```gb
import io;
import sys;

func loadTemplate(string name): string {
    let base = sys.bundleDir();
    if (base == "") { base = "."; }   /* dev: read from the project tree */
    return io.readText(base + "/templates/" + name);
}
```

Because resources keep their project-relative path inside the bundle, the same
relative path (`templates/page.html`) resolves correctly whether `base` is the
project directory in development or the extract directory in a built binary.

## Package Layout

A bundle is built from a Geblang package. The package needs:

- A `geblang.yaml` manifest that declares the package name and source root.
- At least one source module that exports a `main(list<string> args)` function
  to serve as the entry point.

Minimal package layout:

```
myapp/
  geblang.yaml
  src/
    myapp/
      main.gb
```

`geblang.yaml`:

```yaml
name: myapp
source: src
```

`src/myapp/main.gb`:

```gb
module myapp.main;

import io;
import sys;

export func main(list<string> args): void {
    io.println("Hello from a bundled app!");
}
```

The entry module name must match a `module` declaration that the package
resolver can find. In this layout the canonical name is `myapp.main`.

The same entry file runs directly during development: `geblang <file>`
auto-invokes an exported top-level `main` when the file declares one, forwarding
command-line arguments and using an `int` return value as the exit code. So a
`module` that `export func main(list<string> args)` behaves identically whether
run directly or built. A file with no exported `main` runs as a plain script.

## Running geblang build

```sh
geblang build --entry <module.name> --out <output-path> [<package-dir>]
```

| Argument | Required | Description |
|---|---|---|
| `--entry` | yes | Canonical name of the entry module (must export `main`) |
| `--out` | yes | Path for the output binary |
| `--resource` | no | Extra resource to embed, in addition to the manifest `resources:`. Repeatable. `--resource <path>` keeps the project-relative path; `--resource <path>=<bundlePath>` remaps it (a directory's contents mirror under `<bundlePath>`), so a build step can embed processed copies without altering the source tree |
| `--docker` | no | Also write a `Dockerfile` next to the binary (see below) |
| `--docker-port` | no | Add `EXPOSE <port>` to the generated Dockerfile |
| `--force` | no | Overwrite an existing generated Dockerfile |
| `<package-dir>` | no | Package root directory (default: `.`) |

Build the example package above:

```sh
geblang build --entry myapp.main --out ./dist/myapp ./myapp
```

Or from inside the package directory:

```sh
cd myapp
geblang build --entry myapp.main --out ../dist/myapp
```

The output binary is set executable (`chmod 755`). On Unix you can run it
directly:

```sh
./dist/myapp
```

### Third-party notices

Alongside the binary, `geblang build` writes a `<output-path>.NOTICES.txt`
file containing the third-party attribution notices for the components the
binary embeds (the Geblang runtime and its dependencies). Ship this file with
the binary to keep the distribution licence-compliant. It is a sidecar file
rather than a built-in flag, so it never clashes with a `licenses`
subcommand or argument your own program might define. The same applies to
binaries produced by `gebweb build`, which delegates to `geblang build`.

Pass arguments to the bundled program the same way you would any other binary:

```sh
./dist/myapp --port 8080 --verbose
```

The argument list is forwarded to the entry module's `main` function via
`sys.args()`.

## Docker Output

`geblang build --docker` writes a `Dockerfile` into the output directory
alongside the binary (1.19.0). The image copies the binary and its
`NOTICES` sidecar into `gcr.io/distroless/base-debian12` (glibc included -
the binary links libc dynamically) and runs it as the entrypoint:

```sh
geblang build --entry app.main --out dist/app --docker --docker-port 8085 .
cd dist && docker build -t myapp . && docker run -p 8085:8085 myapp
```

`EXPOSE` is only emitted when `--docker-port` is given - a built binary is
not necessarily a server. An existing `Dockerfile` is left unchanged
unless `--force` is passed, so manual edits survive rebuilds. Arguments
after `docker run <image>` flow to the binary as normal program
arguments; the standard flags below work inside the container too. The
typical reverse-proxy deployment runs the container on an internal port
and fronts it with nginx.

## Standard Flags Of Built Binaries

Every built binary answers a small set of standard flags, recognised
only when they are the FIRST argument:

| Flag | Effect |
|---|---|
| `--help`, `-h` | Application name/version, usage, and this flag list |
| `--version` | `<name> <version> (geblang <engine version>)`, from `geblang.yaml` |
| `--notices`, `--licences` | Prints the embedded third-party licence notices |
| `--` | Passes everything after it to the application untouched |

Any other first argument (and all later arguments) flow to the
application's `main(list<string> args)` unchanged, so an application
that defines its own `--help` can still receive it via
`./app -- --help`.

## Full Example: A CLI Tool

Package layout:

```
greet/
  geblang.yaml
  src/
    greet/
      main.gb
      formatter.gb
```

`geblang.yaml`:

```yaml
name: greet
source: src
```

`src/greet/formatter.gb`:

```gb
module greet.formatter;

export func format(string name): string {
    return "Hello, " + name + "!";
}
```

`src/greet/main.gb`:

```gb
module greet.main;

import io;
import sys;
import greet.formatter as fmt;

export func main(list<string> args): void {
    if (args.length() == 0) {
        io.println("Usage: greet <name>");
        sys.exit(1);
    }
    io.println(fmt.format(args[0]));
}
```

Build and run:

```sh
geblang build --entry greet.main --out ./greet-bin ./greet
./greet-bin Ada
# Hello, Ada!
```

Both `greet.main` and `greet.formatter` are discovered automatically by the
import-graph walk and bundled together.

## Bundling Package Dependencies

Geblang's package installer places git dependencies under `vendor/`. Bundling
uses the same package resolver as normal execution, so imported dependency
modules are included automatically once they are installed.

Example application manifest:

```yaml
name: webapp
source: src
dependencies:
  authlib:
    git: https://example.com/authlib.git
    version: main
```

After running `geblang install`, the dependency is available at:

```
webapp/
  geblang.yaml
  src/
    main.gb
  vendor/
    authlib/
      geblang.yaml
      src/
        authlib/
          tokens.gb
```

Application code can import it normally:

```gb
module webapp.main;

import io;
import authlib.tokens as tokens;

export func main(list<string> args): void {
    io.println(tokens.issue("ada"));
}
```

Build the application:

```sh
geblang build --entry webapp.main --out ./webapp-bin ./webapp
```

The resulting executable contains `webapp.main`, `authlib.tokens`, any other
non-native modules reached from those imports, and the source stdlib modules
they need. The target machine does not need `vendor/`, `geblang.yaml`, or a
separate Geblang installation.

`geblang build` does not fetch missing dependencies. If `geblang.yaml` declares
a git dependency and `vendor/<name>/` is absent, run `geblang install` before
building.

## Full Example: A Web Server

```
webapi/
  geblang.yaml
  src/
    webapi/
      main.gb
      routes.gb
```

`geblang.yaml`:

```yaml
name: webapi
source: src
```

`src/webapi/routes.gb`:

```gb
module webapi.routes;

import http;
import web;

export func register(any app): void {
    web.get(app, "/", func(dict<string, any> request): dict<string, any> {
        return http.jsonResponse({"status": "ok"});
    });
}
```

`src/webapi/main.gb`:

```gb
module webapi.main;

import io;
import http;
import web;
import webapi.routes as routes;

export func main(list<string> args): void {
    let port = args.length() > 0 ? args[0] : "8080";
    let app = web.new();
    routes.register(app);
    io.println("Listening on :" + port);
    http.serve("127.0.0.1:" + port, func(dict<string, any> request): dict<string, any> {
        return web.handle(app, request);
    });
}
```

Build a distributable API server:

```sh
geblang build --entry webapi.main --out ./webapi-server ./webapi
./webapi-server 9000
```

## First-Run Extraction

On the first invocation of a bundled binary, the zip is extracted to a
temporary directory under the system temp path:

```
/tmp/geblang-<hash>/
  src/
    ...
  stdlib/
    ...
```

The directory name includes a SHA-256 hash of the zip contents, so different
bundle versions use different directories and never collide. On subsequent runs,
the extracted directory already exists and extraction is skipped - the startup
overhead is paid only once.

Precompiled bytecode is loaded into the interpreter's bytecode cache at
extraction time so recompilation is avoided even after the cache directory is
cleared.

## Inspecting A Bundle

Because the bundle is a valid zip appended to the binary, standard tools work:

```sh
# List bundled files
unzip -l ./dist/myapp

# Extract the source files manually
unzip ./dist/myapp -d ./bundle-contents
```

`BUNDLE.json` inside the zip records the entry module name, the Geblang version
the bundle was built with, and the canonical name, zip path, and source hash
for every bundled module.

## Limitations

- **OS and architecture**: A bundle built on Linux/amd64 runs only on
  Linux/amd64. Cross-compilation is not yet supported; build on the target
  platform or use a cross-compilation container.
- **No hot-reload**: Bundled source is extracted once and cached. Changes to
  the original source files have no effect on a built bundle.
- **Import-graph walking**: `geblang build` discovers modules by parsing import
  statements statically. Dynamic module loading (using string variables as
  import paths) is not currently traversed. Any module loaded this way must be
  imported explicitly elsewhere in the package so it is included in the bundle.
- **Vendored dependencies**: imported modules from installed package
  dependencies are bundled, but unimported files under `vendor/` are not copied
  wholesale.
- **Native extensions**: Go-backed `ext.*` extensions are not bundled. They must
  be present on the target machine alongside the bundle binary.
