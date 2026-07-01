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
| Line comment toggle `Ctrl+/` (prefix `#`) | Built-in |
| Block comment toggle `Ctrl+Shift+/` (`/* */`) | Built-in |
| Diagnostics (errors, warnings) | `geblang lsp` via LSP4IJ |
| Code completion | `geblang lsp` via LSP4IJ |
| Hover documentation | `geblang lsp` via LSP4IJ |
| Go-to-definition / Find usages | `geblang lsp` via LSP4IJ |
| Rename refactoring | `geblang lsp` via LSP4IJ |
| Formatting (`geblang fmt`) | `geblang lsp` via LSP4IJ |

## Architecture

```
IntelliJ IDE
└── Geblang Plugin (this)
    ├── GeblangLanguage / GeblangFileType  — registers .gb with IntelliJ
    ├── GeblangLexer                       — hand-written lexer (no JFlex)
    ├── GeblangSyntaxHighlighter           — token-type → color mapping
    ├── GeblangColorSettingsPage           — user-editable color scheme
    ├── GeblangCommenter                   — # / /* */ comment toggling
    ├── GeblangBraceMatcher                — {} [] () matching
    ├── GeblangSettings                    — persists geblangExecutablePath
    ├── GeblangConfigurable                — settings UI
    └── GeblangLspServerFactory            — launches `geblang lsp` via LSP4IJ
             └── GeblangStreamConnectionProvider  — stdio process connection
```

LSP4IJ (Red Hat) handles all JSON-RPC communication between IntelliJ and the
`geblang lsp` process. This plugin only needs to launch the process and register
the language mapping.

## Building from Source

```bash
cd jetbrains-geblang
./gradlew buildPlugin          # produces build/distributions/geblang-intellij-0.1.0.zip
./gradlew verifyPlugin         # verifies against IC-2024.2.4 (downloads ~55 MB first run)
./gradlew runIde               # launches a sandboxed IntelliJ for live testing
```

Requires JDK 17 and network access (downloads the IntelliJ Platform and LSP4IJ on first run).

## Development / Testing

Run the lexer unit tests with:

```bash
cd jetbrains-geblang
./gradlew test
```

Tests live under `src/test/kotlin/com/dwgebler/geblang/highlighting/GeblangLexerTest.kt`
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

Test reports are written to `build/reports/tests/test/index.html` (HTML) and
`build/test-results/test/*.xml` (JUnit XML) after each run.

## Troubleshooting

**No highlighting in .gb files**
: Check Settings > Plugins — ensure Geblang and LSP4IJ are both enabled.

**LSP features not working (no diagnostics/completion)**
: Ensure `geblang` is installed and on PATH (`which geblang`), or set the full
  path in Settings > Languages & Frameworks > Geblang.
: Check the LSP4IJ console: View > Tool Windows > LSP Consoles > Geblang Language Server.

**`// integer division` being highlighted as comment**
: This is correctly NOT a comment in Geblang — `//` is integer division. The plugin
  treats `//` as an operator. Line comments start with `#`.

## Note on the vscode-geblang extension

The sibling `vscode-geblang/` extension incorrectly classifies `//` as a comment
(copied that pattern from common grammars). This IntelliJ plugin follows the language
spec: `#` = line comment, `/* */` = block comment, `//` = integer division operator.
