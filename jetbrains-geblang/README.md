# Geblang — IntelliJ Plugin

JetBrains/IntelliJ Platform plugin providing Geblang (`.gb`) language support.

## Overview

This plugin follows a split architecture: **LSP4IJ wraps a `geblang lsp` subprocess as
the single source of truth for all semantic analysis** — diagnostics, code completion,
hover documentation, go-to-definition, find usages, rename, and formatting are all
delegated to the real Geblang compiler running as a language server. The plugin itself
does not reimplement or duplicate any of that semantic logic.

Alongside the LSP integration, a light native IntelliJ layer provides everything the
LSP protocol doesn't cover: syntax highlighting (via a hand-written lexer, not a
grammar), a minimal flat-token PSI, run configurations for `geblang run` and
`geblang test`, gutter run/debug markers, live templates, file templates, code folding,
brace matching, comment toggling, TODO highlighting, and spellchecking. These native
features are editor conveniences layered on top of the lexer output, not a competing
semantic analysis engine.

## Requirements

- **IntelliJ Platform 2024.2 — 2024.3** (`sinceBuild="242"`, `untilBuild="243.*"` in
  `plugin.xml`). Built and verified against IntelliJ IDEA Community 2024.2.4.
- **JDK 17** to build from source (Kotlin JVM toolchain is pinned to 17).
- **The `geblang` binary** on your `PATH`, or its path configured in
  Settings > Languages & Frameworks > Geblang, for all LSP-backed features
  (diagnostics, completion, hover, go-to-definition, find usages, rename, formatting)
  and step debugging (breakpoints, stepping, variable inspection via `geblang dap`)
  to activate. Without it, the native editor features (highlighting, folding, run
  configs, templates, etc.) still work; only the LSP-backed and debugging features
  stay disabled.
- **LSP4IJ** (`com.redhat.devtools.lsp4ij`, version 0.20.1 in the current build
  configuration) — declared as a hard plugin dependency (`<depends>` in `plugin.xml`).
  When you install this plugin from a zip via "Install Plugin from Disk", IntelliJ
  resolves and installs LSP4IJ from the JetBrains Marketplace automatically as part of
  dependency resolution (confirmed by the plugin verifier, which resolves
  `com.redhat.devtools.lsp4ij` and its own transitive dependency
  `com.redhat.devtools.intellij.telemetry` against the Marketplace). If your IDE has no
  network access to the Marketplace, install LSP4IJ manually first.
- **JSON and YAML support** (`com.intellij.modules.json`, `org.jetbrains.plugins.yaml`)
  - both bundled with every IntelliJ IDEA Community/Ultimate install, declared as
  plugin dependencies for the `geblang.yaml` manifest schema (completion and
  validation). No separate install step is needed on IC/IU-family IDEs.

## Install

### (a) Run in a sandbox IDE

```bash
cd jetbrains-geblang
./gradlew runIde
```

This launches a disposable, sandboxed IntelliJ IDEA Community instance with the plugin
pre-installed — the fastest way to try it without touching your real IDE settings.

### (b) Install from disk

```bash
cd jetbrains-geblang
./gradlew buildPlugin
```

This produces `build/distributions/geblang-intellij-0.1.0.zip`. In your IDE:

1. Settings > Plugins > (gear icon) > Install Plugin from Disk...
2. Select `build/distributions/geblang-intellij-0.1.0.zip`.
3. Restart the IDE when prompted.
4. LSP4IJ will be resolved and installed automatically as a dependency (see
   Requirements above); if that fails due to no Marketplace access, install
   LSP4IJ manually from Settings > Plugins > Marketplace first, then retry.

### (c) Marketplace

Not yet published. Planned for the future; for now, use (a) or (b).

## Build from source

```bash
cd jetbrains-geblang
JAVA_HOME=/path/to/jdk-17 ./gradlew buildPlugin   # build the installable zip
JAVA_HOME=/path/to/jdk-17 ./gradlew verifyPlugin  # check platform compatibility
JAVA_HOME=/path/to/jdk-17 ./gradlew runIde        # launch a sandboxed IDE
JAVA_HOME=/path/to/jdk-17 ./gradlew test          # run the unit test suite
```

Requires **JDK 17** and network access on first run (downloads the IntelliJ Platform
and LSP4IJ artifacts into the Gradle cache). Uses the **Gradle 8.13** wrapper
(see `gradle/wrapper/gradle-wrapper.properties`).

## Features

Each feature below is registered in `plugin.xml`; the extension point or class name is
noted so you can cross-check against the source.

### Language definition and file type

`.gb` files are recognized as Geblang source (`fileType` extension point,
`GeblangFileType`), complete with a dedicated icon. Just open or create a `.gb` file —
no configuration needed.

### Syntax highlighting

A hand-written lexer (`GeblangLexer`, no JFlex/grammar) drives highlighting for
keywords, built-in types, strings (all four literal forms), numbers, comments,
operators, and decorators (`lang.syntaxHighlighterFactory`, `GeblangSyntaxHighlighterFactory`).
Colors are customizable via a dedicated Color Settings page (`colorSettingsPage`,
`GeblangColorSettingsPage`) at Settings > Editor > Color Scheme > Geblang.

String interpolation (`${...}`) spans inside double-quoted (`"..."`) and
triple-double-quoted (`"""..."""`) strings get their own highlight, layered on
top of the base string color by an additive annotator (`annotator`,
`GeblangInterpolationAnnotator`) that scans the lexer's opaque STRING token
text for `${...}` sub-ranges (matching nested braces correctly) without
changing the lexer or its token stream. Raw single-quoted (`'...'` /
`'''...'''`) strings never interpolate and are left untouched. The
interpolation color is separately configurable (`colorSettingsPage`, "String
interpolation") from the base string color.

### Comment toggling

`Ctrl+/` toggles `#` line comments; `Ctrl+Shift+/` toggles `/* */` block comments
(`lang.commenter`, `GeblangCommenter`). Note that `//` is **not** a comment prefix in
Geblang — see Troubleshooting.

### Brace matching

`{}`, `[]`, and `()` are matched and highlighted when the caret sits next to one
(`lang.braceMatcher`, `GeblangBraceMatcher`).

### LSP integration (LSP4IJ)

The plugin registers a language server (`com.redhat.devtools.lsp4ij` `server`
extension point, `GeblangLspServerFactory`) that launches `geblang lsp` over stdio
(`GeblangStreamConnectionProvider`), and maps the Geblang language to it
(`languageMapping`). The binary path comes from the Geblang settings page, defaulting
to `geblang` resolved on `PATH`. Per `plugin.xml`'s own description and the
`GeblangMissingExecutableNotifier` warning text, the features this wires up are:
diagnostics, code completion, hover documentation, go-to-definition, find usages,
rename, and formatting. Because the connection is a generic LSP4IJ
`StreamConnectionProvider` (not a capability-restricted client), any other standard LSP
feature the `geblang lsp` server itself advertises (e.g. signature help, document/
workspace symbols, code actions) may also surface through LSP4IJ's generic UI, but only
the features listed above are explicitly documented and exercised by this plugin.

### Executable path setting

Settings > Languages & Frameworks > Geblang (`applicationConfigurable`,
`GeblangConfigurable`) lets you set the path to the `geblang` binary. It is persisted
at the application level (`GeblangSettings`, stored in `geblang.xml`) and defaults to
the bare name `geblang` (PATH lookup).

### "Geblang not found" notification

If the configured executable can't be resolved, a warning balloon appears the first
time you open a `.gb` file in a project session (`projectListeners` +
`GeblangMissingExecutableNotifier`, `notificationGroup` id `Geblang`), with a
"Configure..." action that jumps straight to the settings page. It fires at most once
per project session, and not at all once the executable resolves successfully.

### Minimal PSI (flat-token tree)

`lang.parserDefinition` (`GeblangParserDefinition`) builds a **flat** PSI tree: a
single file root whose children are one leaf per lexer token, in source order, with no
nesting and no grammar. This is explicitly not a full parser — its purpose is to give
the platform just enough structure to hang folding, run-line markers, TODO
highlighting, and spellchecking off of. All real semantic understanding stays with the
LSP server.

### Code folding

Multi-line `{ ... }` blocks (including nested blocks, each as its own fold region) and
multi-line `/* ... */` comments can be folded (`lang.foldingBuilder`,
`GeblangFoldingBuilder`). Single-line braces and single-line block comments are never
folded. Use the +/- gutter icons, or the editor's folding shortcuts/actions.

### "Geblang Test" run configuration

Runs `geblang test --format teamcity <target>` and renders results in IntelliJ's
native test runner tree (`configurationType`, `GeblangTestRunConfigurationType`).
Create one via Run > Edit Configurations... > + > Geblang Test, set a target file or
directory, and optionally a `--tag` filter and working directory.

### "Geblang File" run configuration

Runs `geblang run <file> [args]` in a plain console (`configurationType`,
`GeblangFileRunConfigurationType`). Create one via Run > Edit Configurations... > + >
Geblang File, set the target `.gb` file, and optionally a working directory and program
arguments.

### Run/debug gutter markers

Click-to-run/debug gutter icons (`runLineMarkerContributor`,
`GeblangRunLineMarkerContributor`) appear next to a top-level `func main(`
declaration, a `class X extends test.Test` declaration, and `@test`-decorated methods.
Clicking dispatches to a "Geblang File" or "Geblang Test" configuration as appropriate.

### Run configuration producers

`runConfigurationProducer` registrations (`GeblangFileRunConfigurationProducer`,
`GeblangTestRunConfigurationProducer`) let the gutter markers (and "Create
configuration from context") resolve a click location to the right run configuration
automatically, without requiring you to create one by hand first.

### Live templates

102 live templates (code snippets), ported from the vscode-geblang snippet set and
scoped to `.gb` files (`liveTemplateContext` + `defaultLiveTemplates`,
`GeblangTemplateContextType`, resource `liveTemplates/Geblang.xml`). Type a prefix
(e.g. `func`, `class`, `testclass`) and press Tab to expand it; Tab again to move
between placeholders.

### New > Geblang File templates

A "New > Geblang File" action (`actions` > `Geblang.NewFile`,
`GeblangCreateFileAction`, added to the standard `NewGroup` menu) offers four bundled
file templates (`fileTemplateGroup`, `GeblangFileTemplateGroupFactory`; resources under
`fileTemplates/internal/`): **File**, **Class**, **Module**, and **Test**. The same
four templates are editable under Settings > Editor > File and Code Templates >
Geblang.

### TODO highlighting

`# TODO: ...`, `# FIXME: ...`, and block-comment TODOs show up in the TODO tool window
like any other language. This required no dedicated extension point registration
beyond `GeblangParserDefinition.getCommentTokens()` — the platform's TODO indexing
scans comment token text directly.

### Spellchecking

Prose inside comments and string literals is spellchecked, and identifiers are split
on camelCase/snake_case boundaries so only the misspelled word is flagged rather than
the whole identifier (`spellchecker.support`, `GeblangSpellcheckingStrategy`).
Keywords, operators, numbers, decorators, and braces are never spellchecked.

### Debugging

Step debugging is wired through LSP4IJ's Debug Adapter Protocol (DAP) support
(`debugAdapterServer` extension point, `GeblangDebugAdapterDescriptorFactory` /
`GeblangDebugAdapterDescriptor`) plus a `languageMapping` tying the Geblang language to
that debug adapter (mirroring the existing LSP `languageMapping`, just with a different
`serverId`). It launches `geblang dap` over stdio, the debug counterpart of the LSP
integration's `geblang lsp`, using the same executable path resolution
(`GeblangExecutable.resolve`, Settings > Languages & Frameworks > Geblang).

To debug a `.gb` file:

1. Make sure the `geblang` binary is resolvable (on `PATH` or configured in settings) -
   see Configuration below.
2. Click in the gutter next to a source line in a `.gb` file to set a breakpoint (a red
   dot appears).
3. Click the Debug gutter icon next to a top-level `func main(` declaration (the same
   gutter icon used for "Run", via `GeblangRunLineMarkerContributor`), or use
   Run > Debug on a "Geblang File" run configuration/context.
4. LSP4IJ recognizes the `.gb` file as debuggable through the registered debug adapter
   and automatically creates a "Debug Adapter Protocol" run configuration for it,
   launching `geblang dap` and attaching. Step over/into/out, variable inspection, and
   (per `geblang` 1.31.0) multi-threaded call stacks are all handled generically by
   LSP4IJ's DAP client - no Geblang-specific debugging UI code is needed.

No separate "Geblang Debug" run configuration type or custom breakpoint type was added:
LSP4IJ's own generic `DAPBreakpointType` and "Debug Adapter Protocol" run configuration
apply automatically to any file type with a registered debug adapter mapping, the same
way its LSP client applies generically once a language server is mapped.

**Note:** LSP4IJ 0.20.1 marks its entire DAP extension point and descriptor API
`@ApiStatus.Experimental` (confirmed by the IntelliJ Plugin Verifier report, which
lists 5 experimental API usages against `DebugAdapterDescriptorFactory`). It works as
documented in this version, but the API surface may change in a future LSP4IJ release.

### geblang.yaml manifest support

Any file named exactly `geblang.yaml` gets completion and validation for the Geblang
package manifest, driven by a bundled JSON Schema
(`schemas/geblang-manifest.schema.json`) registered through the platform's JSON
schema engine (`GeblangManifestSchemaProviderFactory` /
`GeblangManifestSchemaFileProvider`, `JavaScript.JsonSchema.ProviderFactory`
extension point). The schema documents the known manifest keys - `name`, `version`,
`source`, `paths` / `modulePaths`, `resources`, `dependencies` (path and git forms),
the `package:` alias block, `permissions` (`ffi`, `onnx`, `processControl`,
`browser`), and `extensions` (subprocess extension config: `command`, `socket`,
`host`, `startup_timeout_ms`, `env`) - derived from `internal/modules/resolver.go`,
`internal/evaluator/eval_modules.go`/`ext.go`, and the bundling/modules-and-packages
docs. It is deliberately permissive (`additionalProperties: true` throughout), so
unknown keys are never flagged as errors - the goal is helpful completion, not
strict rejection. This requires the bundled JSON module
(`com.intellij.modules.json`) and the bundled YAML plugin
(`org.jetbrains.plugins.yaml`), both declared as plugin dependencies.

## Feature status

| Feature | Status | Notes |
|---|---|---|
| File type / icon for `.gb` | Implemented (headless-tested) | Exercised indirectly via lexer/PSI/template tests that load `.gb` fixtures |
| Syntax highlighting (lexer) | Implemented (headless-tested) | `GeblangLexerTest` — comments, strings, numbers, keywords, operators, decorators, bad-character handling, round-trip |
| Color Settings page | Implemented (verify in runIde) | No headless test exercises the rendered color page UI |
| Commenter (`#`, `/* */`) | Implemented (verify in runIde) | No dedicated commenter test; relies on standard platform wiring of `lang.commenter` |
| Brace matcher (`{}` `[]` `()`) | Implemented (verify in runIde) | No dedicated headless test; standard `PairedBraceMatcher` wiring |
| Minimal flat-token PSI | Implemented (headless-tested) | `GeblangParserDefinitionTest` — no PSI errors, lossless round-trip, flat leaf sequence |
| Code folding (`{}`, block comments) | Implemented (headless-tested) | `GeblangFoldingBuilderTest` — nested blocks, block comments, single-line exclusion, exact fold count |
| LSP integration (diagnostics/completion/hover/go-to-def/find usages/rename/formatting) | Implemented (verify in runIde) | Behavior depends on the real `geblang lsp` server; not exercised by this plugin's headless test suite |
| Executable path setting | Implemented (verify in runIde) | Settings UI (`GeblangConfigurable`) is not headlessly tested; the underlying resolve helper is |
| "Geblang not found" notification | Implemented (verify in runIde) | Notification UI is intentionally not unit-tested; `GeblangExecutableTest` covers the pure `GeblangExecutable.resolve` helper it depends on |
| "Geblang Test" run configuration | Implemented (headless-tested) | `GeblangTestCommandLineStateTest` (argument building), `GeblangTestRunConfigurationProducerTest` (context resolution) |
| "Geblang File" run configuration | Implemented (headless-tested) | `GeblangFileCommandLineStateTest` (argument building), `GeblangFileRunConfigurationProducerTest` (context resolution) |
| Test tree navigation (`GeblangTestLocator`) | Implemented (headless-tested for path parsing / verify in runIde for full resolution) | `GeblangTestLocatorTest` covers pure `parsePath`; full PSI/VFS `getLocation` resolution and test-tree rendering are GUI-only |
| Run/debug gutter markers | Implemented (headless-tested) | `GeblangRunLineMarkerContributorTest` — anchor position assertions for main/test-class/test-method; icon/tooltip rendering itself is GUI-only |
| Live templates (102 snippets) | Implemented (headless-tested for content / verify in runIde for expansion UX) | `GeblangLiveTemplatesTest` validates XML structure, count, uniqueness, context; actual Tab-to-expand UX needs `runIde` |
| New > Geblang File templates (File/Class/Module/Test) | Implemented (headless-tested) | `GeblangFileTemplatesTest` — content assertions per template kind |
| TODO highlighting | Implemented (headless-tested) | `GeblangTodoTest` — `PsiTodoSearchHelper` finds both comment forms |
| Spellchecking | Implemented (headless-tested) | `GeblangSpellcheckingStrategyTest` — tokenizer selection per token kind |
| geblang.yaml manifest schema (completion/validation) | Implemented (headless-tested) | `GeblangManifestSchemaFileProviderTest` - filename matching, schema type, bundled resource loads as valid JSON; schema validated offline against 10 real manifests plus a malformed negative case |
| Step debugging (DAP via LSP4IJ) | Implemented (verify in runIde) | `GeblangDebugAdapterDescriptorTest` covers the pure `geblang dap` command-array helper; the live debug session (breakpoints, stepping, variable inspection, `DAPRunConfigurationProvider` auto-creating a run config) can only be exercised by `runIde` |

## Configuration

**Settings > Languages & Frameworks > Geblang** — the only settings page the plugin
adds (`GeblangConfigurable`). It has a single field, the path to the `geblang`
executable. Leave it as the default `geblang` to resolve the binary from `PATH`, or
enter an absolute path. This path is used by the LSP server launch (`geblang lsp`),
the debug adapter launch (`geblang dap`), and by the "Geblang Test" / "Geblang File"
run configurations (`geblang test` / `geblang run`).

## Troubleshooting

**No LSP features working (no diagnostics, completion, hover, etc.)**
: Confirm the `geblang` binary is installed and either on `PATH` (`which geblang`) or
  configured with a full path in Settings > Languages & Frameworks > Geblang. Confirm
  LSP4IJ is installed and enabled (Settings > Plugins). Check
  View > Tool Windows > LSP Consoles for the Geblang Language Server's own log output —
  this is the most direct way to see why `geblang lsp` failed to start or crashed.

**No syntax highlighting**
: Confirm the file has a `.gb` extension so it's recognized as the Geblang file type.
  If it's an unusual filename, check Settings > Editor > File Types to see whether
  something else has claimed the extension.

**`//` renders as an operator, not a comment**
: This is correct behavior, not a bug. Geblang uses `#` for line comments; `//` is the
  integer-division operator. `GeblangCommenter` and `GeblangLexer` both treat `//`
  as an operator token, matching the language's actual grammar (the lexer test suite
  has a dedicated guard test for this).

**"Geblang not found" notification keeps appearing / never appears when expected**
: It is designed to fire at most once per project session, the first time a `.gb` file
  is opened while the executable can't be resolved. Reopening the same project in a
  new IDE session resets that "already warned" state.

**Test runner shows no results, or double-clicking a result doesn't navigate**
: `GeblangTestLocator`'s navigation is a best-effort text scan for `class <Name>` /
  `func <method>` in `.gb` files — it is not a full symbol resolution (the PSI is
  intentionally flat, with no grammar). If a match isn't found by that scan,
  navigation is simply unavailable for that entry; the test run itself still
  completes and reports pass/fail normally.

**Debug session doesn't start / no breakpoints hit**
: Confirm the `geblang` binary is installed and resolvable the same way the LSP
  section above describes - the debug adapter uses the identical
  `GeblangExecutable.resolve` lookup. Check View > Tool Windows > LSP Consoles (or the
  DAP-specific console, if enabled) for the debug adapter's own log output. Note the
  IDE log line `server 'geblang-dap' for mapping IntelliJ language 'Language: Geblang'
  not available` is a known, harmless LSP4IJ 0.20.1 warning: its LSP-server registry
  scans the same shared `languageMapping`/`fileTypeMapping` extension points used by
  DAP server registrations and warns because `geblang-dap` isn't an LSP server ID (it
  is a DAP-only ID) - it does not indicate a real problem with the debug adapter
  registration.

## Development / Testing

Unit tests live under `src/test/kotlin/com/dwgebler/geblang/`. Run them with:

```bash
cd jetbrains-geblang
JAVA_HOME=/path/to/jdk-17 ./gradlew test
```

As of this writing, the suite has **118 passing tests** (0 failures) across 17 test
classes, covering: the lexer (comments, strings, numbers, keywords, operators,
decorators, bad-character handling, round-trip integrity), the flat PSI (no parse
errors, lossless round-trip), code folding, run-configuration argument building and
context resolution, run line marker anchor detection, the test locator's path parsing,
live template XML validity/count/uniqueness, file template content, TODO discovery,
spellchecking tokenizer selection, and the debug adapter's `geblang dap` command-array
construction. Test reports are written to
`build/reports/tests/test/index.html` and `build/test-results/test/*.xml`.

Run `./gradlew verifyPlugin` to check the built plugin's structure and platform
compatibility using the JetBrains Plugin Verifier; it downloads the target IDE (IC
2024.2.4) and LSP4IJ on first run and reports compatibility against the configured
`sinceBuild`/`untilBuild` range.

Features that are GUI-only (see the Feature status table above) are only verified
manually via `./gradlew runIde`.

## Note on the vscode-geblang extension

The sibling `vscode-geblang/` extension classifies `//` as a comment. This IntelliJ
plugin follows the language's actual grammar instead: `#` is the line comment prefix,
`/* */` is the block comment form, and `//` is the integer-division operator.
