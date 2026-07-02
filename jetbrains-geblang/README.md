# Geblang — IntelliJ Plugin

JetBrains/IntelliJ plugin providing Geblang (`.gb`) language support.

## Requirements

- **IntelliJ IDEA Community or Ultimate 2024.2.x** (build 242.x)
- **JDK 17+** to build from source
- **`geblang` binary** on your PATH (or configured in settings) for LSP features
  (diagnostics, code completion, hover, go-to-definition, find usages, rename, formatting)
- **LSP4IJ** — declared as a required plugin dependency; IntelliJ installs it automatically
  when you install this plugin from a zip

## Install from Zip (local trial)

1. Build the zip: `./gradlew buildPlugin`
   Produced at: `build/distributions/geblang-intellij-0.1.0.zip`
2. In IntelliJ: **Settings > Plugins > ⚙ > Install Plugin from Disk…**
3. Select the zip file, click **OK**, restart the IDE.
4. Open any `.gb` file — you should see syntax highlighting immediately.
5. For LSP features: make sure `geblang` is on your PATH, or set the path in
   **Settings > Languages & Frameworks > Geblang**.

## Features

| Feature | Source |
|---|---|
| Syntax highlighting (keywords, types, strings, numbers, comments, operators) | Built-in lexer |
| Decorator highlighting (`@memoize`, dotted `@Assert.email`, `@Get("/x")`) | Built-in lexer |
| Color scheme customisation | Settings > Editor > Color Scheme > Geblang |
| File type recognition (`.gb` files with icon) | Built-in |
| Brace matching `{}` `[]` `()` | Built-in |
| Code folding: multi-line `{ ... }` blocks and multi-line `/* ... */` comments | Built-in |
| Line comment toggle `Ctrl+/` (prefix `#`) | Built-in |
| Block comment toggle `Ctrl+Shift+/` (`/* */`) | Built-in |
| Diagnostics (errors, warnings) | `geblang lsp` via LSP4IJ |
| Code completion | `geblang lsp` via LSP4IJ |
| Hover documentation | `geblang lsp` via LSP4IJ |
| Go-to-definition / Find usages | `geblang lsp` via LSP4IJ |
| Rename refactoring | `geblang lsp` via LSP4IJ |
| Formatting (`geblang fmt`) | `geblang lsp` via LSP4IJ |
| "geblang executable not found" warning notification, with a Configure… action | Built-in |
| "Geblang Test" run configuration: `geblang test --format teamcity` in the native test tree | Built-in |
| "Geblang File" run configuration: `geblang run <file>` in a plain console | Built-in |
| Run/Debug gutter markers: click-to-run `func main(`, test classes, and `@test` methods | Built-in |
| Live templates (code snippets): type a prefix and press Tab to expand | Built-in |
| "New > Geblang File" templates: File / Class / Module / Test scaffolds | Built-in |

## Running Geblang Tests

The plugin registers a "Geblang Test" run configuration that runs
`geblang test --format teamcity <target>` and renders the results in IntelliJ's
native test runner tree (the same tree used for JUnit, pytest, etc.).

To create one:

1. **Run > Edit Configurations... > + > Geblang Test**
2. Set **Target** to a `.gb` test file (e.g. `tests/user_test.gb`) or a directory
   (directories are scanned recursively for `*_test.gb` files by `geblang test`).
3. Optionally set a **Working directory** (defaults to the project root) and a
   **Tag filter** (forwarded as `--tag <value>`, matching tests decorated with
   `@tag("value")`).
4. Click **Run**. Test classes (`class X extends test.Test`) and their `@test`
   methods appear in the test tree with pass/fail status as they run.

The executable used is the one configured under
**Settings > Languages & Frameworks > Geblang** (falls back to `geblang` on PATH).

Double-clicking a test in the tree does a best-effort navigation to its `class`/`func`
declaration by scanning `.gb` files in the project for a textual match (Geblang has no
PSI parser yet, so this is not full go-to-definition); if no match is found, navigation
is simply unavailable for that entry rather than failing the run.

## Running a Geblang File

The plugin also registers a "Geblang File" run configuration that runs
`geblang run <file> [args]` in a plain console (no test tree - just process output).

To create one:

1. **Run > Edit Configurations... > + > Geblang File**
2. Set **Geblang file** to a `.gb` file to run.
3. Optionally set a **Working directory** (defaults to the project root) and
   **Program arguments** (forwarded verbatim after the file, whitespace-split).
4. Click **Run**.

The executable used is the same one configured under
**Settings > Languages & Frameworks > Geblang** (falls back to `geblang` on PATH)
that the "Geblang Test" configuration uses.

## Run/Debug gutter markers

Click-to-run gutter icons appear next to three anchors in a `.gb` file:

- A top-level `func main(` declaration - click Run/Debug to launch a "Geblang File"
  configuration targeting the enclosing file.
- A `class X extends test.Test` declaration - click Run/Debug to launch a
  "Geblang Test" configuration targeting the enclosing file.
- An `@test`-decorated method - click Run/Debug to launch the same "Geblang Test"
  configuration targeting the enclosing file.

These markers create and reuse run configurations automatically (via
`GeblangFileRunConfigurationProducer` and `GeblangTestRunConfigurationProducer`), so
there's no need to create one by hand first - though any configuration created this
way is still fully editable under **Run > Edit Configurations...** afterwards.

## Live templates

The plugin bundles 102 live templates (code snippets) ported from the vscode-geblang
extension, scoped to `.gb` files only.

To use one: in a `.gb` file, type the template's prefix (e.g. `func`, `class`,
`testclass`) and press **Tab** to expand it. Placeholders are pre-filled with sensible
defaults; press **Tab** again to move between them, and the final **Tab** lands the
cursor at the template's designated end position (usually the function/class body).

Coverage by category:

- **Declarations**: functions (`func`, `asyncfunc`, `genfunc`, `export`, `genericfunc`,
  `genericinterface`, `genericlambda`, `typetest`), classes (`class`, `classex`,
  `abstractclass`, `immutable`, `dataclass`, `ffihandle`, `contextmgr`, `iter`),
  interfaces (`interface`, `interfacedefault`), enums (`enum`, `enumstring`, `enumint`)
- **Control flow**: `match`, `try`, `trycatch`, `throw`, `forin`, `for`, `while`
- **Decorators**: `test`, `testclass`, `abstractmethod`, `override`, `deprecated`,
  `memoize`, `assertThrows`
- **Module system**: `import`, `fromimport`, `module`, `init`
- **Class members**: `staticconst`, `staticlet`, `statictyped`, `destructor`, `del`,
  `with`
- **Dunder overrides**: `serialize`, `deserialize`, `castString`, `castInt`,
  `castFloat`, `castBool`, `castDecimal`, `castBytes`
- **Standard library idioms**: `functools`, `asyncrate`, `asyncall`, `asyncrace`,
  `asynctimeout`, `asynccancel`, crypto (`aesencrypt`, `aesdecrypt`,
  `chacha20encrypt`, `chacha20decrypt`, `passwordhash`), regex (`rematch`,
  `rematchall`), HTTP (`httpsession`, `httpproxy`), scheduling (`schedtimer`,
  `schedticker`), encoding (`base32`, `base58`, `base64url`, `tobase`, `binarypack`),
  `stopwatch`, `color`, CSV (`csvparse`, `csvdict`), `stringbuilder`, streams
  (`streamsopen`, `streamsmem`, `streamscopy`), filesystem watching (`watchstart`,
  `watchrecursive`), processes (`procspawn`, `procpty`), sockets (`socketsdial`,
  `socketsserve`), SSH (`sshconnect`, `sshexec`, `sshspawn`), CLI widgets (`spinner`,
  `progressbar`), FFI (`ffidlopen`, `ffisymbol`, `ffihandle`), LLM (`llmchat`,
  `llmembed`), messaging (`messagingsqs`, `messagingsns`), `reflectloc`

## New file templates

The plugin registers a **New > Geblang File** action (Project View / File menu
> New) offering four starter templates:

| Kind | Produces |
|---|---|
| **File** | A minimal `.gb` file: a top `# <name>` comment and an empty `func main(): void {}` |
| **Class** | A `class <Name> { ... }` with a `string name` field and matching constructor |
| **Module** | A `module <name>;` declaration with one `export`ed function |
| **Test** | `import test;` plus a `class <Name>Test extends test.Test` with one `@test`-decorated method |

The same four templates are also editable under **Settings > Editor > File
and Code Templates > Geblang**, so you can tweak the boilerplate to match
your own conventions.

## Architecture

```
IntelliJ IDE
└── Geblang Plugin (this)
    ├── GeblangLanguage / GeblangFileType  — registers .gb with IntelliJ
    ├── GeblangLexer                       — hand-written lexer (no JFlex)
    ├── GeblangSyntaxHighlighter           — token-type → color mapping
    +-- psi/ (minimal PSI layer - no grammar)
    |        |-- GeblangParserDefinition   - builds a FLAT PSI tree of lexer tokens
    |        `-- GeblangFile               - PSI file root (PsiFileBase)
    ├── GeblangColorSettingsPage           — user-editable color scheme
    ├── GeblangCommenter                   — # / /* */ comment toggling
    ├── GeblangBraceMatcher                — {} [] () matching
    ├── GeblangFoldingBuilder              - folds multi-line {} blocks and /* */ comments
    ├── GeblangSettings                    — persists geblangExecutablePath
    ├── GeblangConfigurable                — settings UI
    ├── GeblangLspServerFactory            — launches `geblang lsp` via LSP4IJ
    │        └── GeblangStreamConnectionProvider  — stdio process connection
    ├── GeblangExecutable                  — resolves the configured executable path to a File
    ├── GeblangMissingExecutableNotifier   — warns (once per project) if it can't be resolved
    │        └── GeblangMissingExecutableState  — per-project "already warned" flag
    +-- templates/ (Live templates)
    |        |-- GeblangTemplateContextType         - restricts templates to .gb files
    |        `-- liveTemplates/Geblang.xml           - the bundled template set (resource)
    +-- actions/ (New file templates)
    |        |-- GeblangCreateFileAction            - "New > Geblang File" action + dialog kinds
    |        |-- GeblangFileTemplateGroupFactory    - exposes templates under Settings > File Templates
    |        `-- fileTemplates/internal/*.gb.ft     - the four bundled templates (resources)
    +-- run/ (Geblang Test / Geblang File run configurations, producers, gutter markers)
             |-- GeblangTestRunConfigurationType / GeblangTestConfigurationFactory
             |-- GeblangTestRunConfiguration        - target/workingDirectory/tag settings
             |-- GeblangTestSettingsEditor          - Edit Configurations UI
             |-- GeblangTestCommandLineState         - builds/launches the geblang process
             |-- GeblangTestConsoleProperties        - wires the SMTestRunner console
             |-- GeblangTestLocator                  - geblang_test:// URL -> source location
             |-- GeblangFileRunConfigurationType / GeblangFileConfigurationFactory
             |-- GeblangFileRunConfiguration         - target/workingDirectory/programArguments settings
             |-- GeblangFileSettingsEditor           - Edit Configurations UI
             |-- GeblangFileCommandLineState          - builds/launches `geblang run <file>`
             |-- GeblangRunAnchors                   - shared flat-leaf anchor detection (main/test class/@test method)
             |-- GeblangRunLineMarkerContributor     - gutter Run/Debug markers, built on GeblangRunAnchors
             |-- GeblangFileRunConfigurationProducer  - resolves a click context to a GeblangFileRunConfiguration
             `-- GeblangTestRunConfigurationProducer  - resolves a click context to a GeblangTestRunConfiguration
```

LSP4IJ (Red Hat) handles all JSON-RPC communication between IntelliJ and the
`geblang lsp` process. This plugin only needs to launch the process and register
the language mapping.

`GeblangParserDefinition` is intentionally minimal: it reuses `GeblangLexer`
directly and wraps every token as a flat, unnested PSI leaf under a single
`GeblangFile` root. There is no grammar and no Grammar-Kit - this is not a real
parser. Its only purpose is to give the platform a PSI tree to hang PSI-based
features off of later (folding, run-line markers, TODO highlighting,
spellcheck). Semantic analysis remains entirely owned by the LSP server.

## Building from Source

```bash
cd jetbrains-geblang
./gradlew buildPlugin          # produces build/distributions/geblang-intellij-0.1.0.zip
./gradlew verifyPlugin         # verifies against IC-2024.2.4 (downloads ~55 MB first run)
./gradlew runIde               # launches a sandboxed IntelliJ for live testing
```

Requires JDK 17 and network access (downloads the IntelliJ Platform and LSP4IJ on first run).

## Development / Testing

Run the unit tests with:

```bash
cd jetbrains-geblang
./gradlew test
```

Lexer tests live under `src/test/kotlin/com/dwgebler/geblang/highlighting/GeblangLexerTest.kt`
and drive `GeblangLexer` directly (via IntelliJ Platform's `LexerTestCase`), with no
IDE UI or PSI/parser involved. They cover:

- Line comments (`#`, `##`) and block comments (`/* */`, `/** */`)
- **`//` tokenizing as the integer-division `OPERATOR`, never as a comment** — the
  key distinction versus `#`-style line comments, and the most important guard test
  in the suite
- All four string forms: `"..."`, `"""..."""`, `'...'`, `'''...'''`, plus
  interpolation placeholders and backslash escape sequences inside double-quoted strings
- Numbers: decimal, underscore-separated, float, float with `f` suffix, scientific
  notation, and hex/octal/binary literals
- Keywords, constants (`true`/`false`/`null`/`this`), word operators (`is`/`not`/`xor`),
  and built-in types
- Multi-character operators (`//`, `**`, `??=`, `?.`, `|>`, `..`, `+=`, `==`, `=>`)
- Decorators: bare `@memoize`, short `@Get`, dotted composite names (`@Assert.email`,
  `@Foo.bar.baz`), and decorators with argument lists (`@Get("/x")`, where only the
  dotted name is a `DECORATOR` token — the parens/args lex normally afterwards); a
  bare `@` not followed by a letter/`_` remains the `@` `OPERATOR`
- Bracket tokens (`{}` `[]` `()`)
- A realistic multi-line snippet mixing several token categories
- Bad-character handling (`BAD_CHARACTER` fallback for unrecognized input)
- A round-trip sanity check confirming token offsets have no gaps/overlaps and
  concatenated token text reproduces the original input exactly, plus a lexer
  restart-consistency check

`src/test/kotlin/com/dwgebler/geblang/editor/GeblangFoldingBuilderTest.kt` covers
`GeblangFoldingBuilder` against the flat-leaf PSI: a multi-line function body folds, a
nested `if` block inside it produces its own (nested) fold region, a multi-line `/* */`
comment folds, and a single-line `{}` does not fold.

`src/test/kotlin/com/dwgebler/geblang/notification/GeblangExecutableTest.kt` covers the
pure `GeblangExecutable.resolve` helper (absolute paths, PATH lookup, blank input) as a
plain JUnit test — no IDE fixtures needed since the helper has no UI/notification
side effects. The notification UI itself is intentionally not unit-tested.

`src/test/kotlin/com/dwgebler/geblang/run/GeblangTestCommandLineStateTest.kt` covers
`buildTestArguments`, the pure helper that turns a target + optional tag into the
`geblang test --format teamcity [--tag <tag>] <target>` argument list.

`src/test/kotlin/com/dwgebler/geblang/run/GeblangTestLocatorTest.kt` covers
`GeblangTestLocator.parsePath`, the pure parser for `geblang_test://<Class>[/<method>]`
locator paths. Both are plain JUnit tests with no IDE fixtures. The full
`GeblangTestLocator.getLocation` PSI/VFS resolution and the actual test-tree rendering
are not exercised headlessly — see Troubleshooting below.

`src/test/kotlin/com/dwgebler/geblang/run/GeblangFileCommandLineStateTest.kt` covers
`buildRunArguments`, the pure helper that turns a target file plus a whitespace-split
program-arguments string into the `geblang run <file> [args]` argument list. A plain
JUnit test, no IDE fixtures needed.

`src/test/kotlin/com/dwgebler/geblang/run/GeblangRunLineMarkerContributorTest.kt`
covers `GeblangRunLineMarkerContributor` (backed by `GeblangRunAnchors`) against the
flat-leaf PSI: a `BasePlatformTestCase` fixture with a `func main(`, a
`class FooTest extends test.Test`, and an `@test`-decorated `testX` method asserts
that `getInfo` returns non-null at exactly the `main`/`FooTest`/`testX` identifier
leaves, null everywhere else (keywords, braces, the `@test` decorator leaf itself, an
unrelated local variable name), and that the whole file produces exactly three
anchors in total - only anchor *position* is asserted, not icon/tooltip identity.

`src/test/kotlin/com/dwgebler/geblang/run/GeblangFileRunConfigurationProducerTest.kt`
and `GeblangTestRunConfigurationProducerTest.kt` cover the two run-configuration
producers against a real (but headless) `ConfigurationContext` built from a
`BasePlatformTestCase` PSI file fixture - `ConfigurationContext` has a public
single-`PsiElement` constructor, so no run manager or live execution environment is
needed. Since `RunConfigurationProducer.setupConfigurationFromContext` itself is
`protected`, both are exercised indirectly through the public
`createConfigurationFromContext` entry point (the same path the platform uses when
dispatching a gutter Run/Debug click), asserting the resulting configuration's
`target` and that `isConfigurationFromContext` round-trips correctly.

`src/test/kotlin/com/dwgebler/geblang/templates/GeblangLiveTemplatesTest.kt` parses
`liveTemplates/Geblang.xml` directly as XML from the test classpath (no running IDE or
template engine involved) and asserts: the document is well-formed, the group is
"Geblang", the template count matches the 102 source snippets in
`vscode-geblang/snippets/geblang.json`, every template has a non-blank name and value,
every template carries the `GEBLANG` context option, template names (prefixes) are
unique, and no unconverted VS Code tabstop syntax (`${1:...}`, `$1`) leaked through.

`src/test/kotlin/com/dwgebler/geblang/templates/GeblangTemplateContextTypeTest.kt`
covers the parts of `GeblangTemplateContextType` that do not require IDE fixtures:
its presentable name, and the `GeblangFileType` singleton identity check the context
type's `isInContext` is built on. The full expansion path (typing a prefix and
pressing Tab inside a real editor) is only exercised manually via `runIde`, since
`isInContext(TemplateActionContext)` needs a live `PsiFile`/`Editor` pair.

Test reports are written to `build/reports/tests/test/index.html` (HTML) and
`build/test-results/test/*.xml` (JUnit XML) after each run.

## Troubleshooting

**No highlighting in .gb files**
: Check Settings > Plugins — ensure Geblang and LSP4IJ are both enabled.

**LSP features not working (no diagnostics/completion)**
: Ensure `geblang` is installed and on PATH (`which geblang`), or set the full
  path in Settings > Languages & Frameworks > Geblang.
: Check the LSP4IJ console: View > Tool Windows > LSP Consoles > Geblang Language Server.
: The plugin shows a "Geblang" warning notification the first time you open a `.gb`
  file if the configured executable can't be resolved, with a **Configure…** action
  that jumps straight to the settings page. This fires at most once per project session.

**`// integer division` being highlighted as comment**
: This is correctly NOT a comment in Geblang — `//` is integer division. The plugin
  treats `//` as an operator. Line comments start with `#`.

## Note on the vscode-geblang extension

The sibling `vscode-geblang/` extension incorrectly classifies `//` as a comment
(copied that pattern from common grammars). This IntelliJ plugin follows the language
spec: `#` = line comment, `/* */` = block comment, `//` = integer division operator.
