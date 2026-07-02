# Changelog

All notable changes to the Geblang IntelliJ plugin will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

## [0.1.0]

Initial prototype release: Geblang (`.gb`) language support for IntelliJ-based IDEs,
built on IntelliJ Platform 2024.2-2024.3 (`sinceBuild="242"`, `untilBuild="243.*"`).

### Added — Language support

- File type registration for `.gb` files, with icon.
- Hand-written lexer (`GeblangLexer`, no JFlex/grammar) covering keywords, built-in
  types, all four string literal forms (with interpolation and escapes), numbers
  (decimal, underscore-separated, float, scientific notation, hex/octal/binary),
  operators, and decorators (including dotted composite forms like `@Assert.email`).
- Syntax highlighting driven by the lexer, with a Color Settings page
  (Settings > Editor > Color Scheme > Geblang) for customizing every token color.
- Minimal, intentionally flat-token PSI (`GeblangParserDefinition`): one leaf PSI
  element per lexer token under a single file root, no grammar and no nesting. This
  is the foundation the folding, run-line-marker, TODO, and spellchecking features are
  built on; it does not attempt semantic analysis.
- Line comment toggling (`Ctrl+/`, `#` prefix) and block comment toggling
  (`Ctrl+Shift+/`, `/* */`). Note that `//` is the integer-division operator in
  Geblang, not a comment prefix — this plugin follows that grammar exactly.
- Brace matching for `{}`, `[]`, and `()`.
- Code folding for multi-line `{ ... }` blocks (including nested blocks, each as its
  own region) and multi-line `/* ... */` comments; single-line braces and comments
  are never folded.
- TODO highlighting: `# TODO: ...`, `# FIXME: ...`, and block-comment TODOs appear in
  the TODO tool window like any other language.
- Spellchecking: comment and string-literal prose is spellchecked; identifiers are
  split on camelCase/snake_case boundaries so only the misspelled word is flagged.

### Added — LSP integration

- LSP4IJ-backed language server integration: launches `geblang lsp` (stdio) as a
  child process and maps the Geblang language to it. Diagnostics, code completion,
  hover documentation, go-to-definition, find usages, rename, and formatting are all
  provided by the real Geblang compiler through this connection — the plugin does not
  reimplement any semantic analysis itself.
- A settings page (Settings > Languages & Frameworks > Geblang) to configure the path
  to the `geblang` executable, defaulting to PATH resolution.
- A one-time-per-project-session warning notification if the configured executable
  can't be resolved, with a "Configure..." action linking directly to the settings
  page.

### Added — Run configurations

- "Geblang Test" run configuration: runs `geblang test --format teamcity <target>`
  against a file or directory (with an optional `--tag` filter and working directory)
  and renders results in IntelliJ's native SMTestRunner test tree, including
  best-effort double-click navigation from a test result back to its source
  declaration.
- "Geblang File" run configuration: runs `geblang run <file> [args]` in a plain
  console, with optional working directory and program arguments.
- Run configuration producers so gutter markers and "create configuration from
  context" resolve automatically to the right configuration type.
- Run/debug gutter markers next to a top-level `func main(` declaration, a
  `class X extends test.Test` declaration, and `@test`-decorated methods, dispatching
  to the appropriate run configuration.

### Added — Templates

- 102 live templates (code snippets) ported from the vscode-geblang snippet set,
  scoped to `.gb` files: function/class/interface/enum declarations, control flow,
  decorators, the module system, class members, dunder overrides, and standard
  library idioms (async, crypto, regex, HTTP, encoding, streams, filesystem watching,
  processes, sockets, SSH, CLI widgets, FFI, LLM client, messaging). Type a prefix and
  press Tab to expand.
- "New > Geblang File" action offering four bundled file templates — File, Class,
  Module, and Test — also editable under Settings > Editor > File and Code Templates.

### Added — Build and testing

- Gradle build (IntelliJ Platform Gradle Plugin 2.2.1, Kotlin 1.9.25, JDK 17
  toolchain) producing an installable plugin zip via `buildPlugin`, with
  `verifyPlugin` for platform-compatibility checks and `runIde` for manual testing in
  a sandboxed IDE.
- Unit test suite (110 tests, headless, no running IDE required for most) covering
  the lexer, the flat PSI, code folding, run-configuration argument building and
  context resolution, run-line-marker anchor detection, test locator path parsing,
  live template validity, file template content, TODO discovery, and spellchecking
  tokenizer selection.
