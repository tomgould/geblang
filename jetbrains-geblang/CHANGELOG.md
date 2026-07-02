# Changelog

All notable changes to the Geblang IntelliJ plugin will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added

- Added run/debug gutter markers (`GeblangRunLineMarkerContributor`) and a "Geblang
  File" run configuration for click-to-run/debug on `func main(`, `class X extends
  test.Test` declarations, and `@test`-decorated methods:
  - `GeblangFileRunConfigurationType`/`GeblangFileRunConfiguration`/
    `GeblangFileCommandLineState`/`GeblangFileSettingsEditor`: runs
    `geblang run <file> [args]` in a plain console (not the SMTestRunner tree,
    since running a file is not running tests), with optional working directory
    and program arguments, mirroring the existing "Geblang Test" configuration's
    structure.
  - `GeblangRunAnchors`: shared flat-leaf-stream anchor detection, since the
    Geblang PSI tree is FLAT (`GeblangParserDefinition` produces no grammar tree).
    Detects a top-level `func main(` by its previous/next non-trivial sibling
    leaves being the `func` KEYWORD and `(` respectively; a test class by
    `class <Name> extends test.Test` (KEYWORD, IDENTIFIER, KEYWORD, IDENTIFIER,
    `.` OPERATOR, IDENTIFIER); and an `@test` method by the same `func ... (`
    shape plus a `@test` DECORATOR leaf immediately preceding the `func` keyword.
    Discovered along the way: the platform's PsiBuilder-based parsing
    infrastructure wraps whitespace tokens as `PsiWhiteSpaceImpl` with element
    type `TokenType.WHITE_SPACE`, not the lexer's own `GeblangTokenTypes.WHITESPACE`,
    so sibling-skipping checks both; comment tokens are not remapped this way.
  - `GeblangRunLineMarkerContributor`: returns an `Info` for exactly one leaf per
    anchor (never one per token), built via the non-deprecated
    `Info(Icon, AnAction[], Function<? super PsiElement, String>)` constructor
    (the `Info(Icon, Function, AnAction...)` overload is `@Deprecated(forRemoval =
    true)` in the IC-2024.2.4 platform jars) with
    `ExecutorAction.getActions(0)` supplying the standard Run + Debug actions.
  - `GeblangFileRunConfigurationProducer`/`GeblangTestRunConfigurationProducer`:
    `LazyRunConfigurationProducer` implementations that resolve a gutter-click
    `ConfigurationContext` back to a `GeblangFileRunConfiguration` (file contains
    a top-level `main`) or the existing `GeblangTestRunConfiguration` (file
    contains a test-class or `@test`-method anchor), registered as
    `runConfigurationProducer` extensions so `ExecutorAction`'s Run/Debug actions
    dispatch through them.
  Verified against the real `com.intellij.execution.lineMarker.RunLineMarkerContributor`,
  `com.intellij.execution.lineMarker.ExecutorAction`, and
  `com.intellij.execution.actions.LazyRunConfigurationProducer`/
  `RunConfigurationProducer` contracts for IC-2024.2.4 (via `javap` against the
  platform jars). Covered by a `BasePlatformTestCase` asserting anchor leaf
  positions (and non-positions) for the line marker contributor, a plain JUnit
  test for the `geblang run` argument-construction helper, and
  `BasePlatformTestCase` tests for both producers driven through the public
  `createConfigurationFromContext` entry point (since
  `setupConfigurationFromContext` itself is `protected`).
- Added code folding (`GeblangFoldingBuilder`): multi-line `{ ... }` blocks fold
  (including nested blocks, each as its own region) and multi-line `/* ... */`
  block comments fold, using placeholders `{...}` and `/*...*/` respectively.
  Built directly off the flat-leaf PSI stream (matching `{`/`}` leaves with a
  depth counter) since `GeblangParserDefinition` produces no grammar tree.
  Single-line `{}` and single-line block comments are never folded, and
  unbalanced braces are skipped rather than causing an error. Verified against
  the real `com.intellij.lang.folding.FoldingBuilderEx` contract for IC-2024.2.4
  and covered by a `BasePlatformTestCase` asserting fold ranges and placeholders.
- Added a minimal PSI layer (`GeblangParserDefinition`, `GeblangFile`): builds a
  FLAT PSI tree of the existing lexer's tokens under a single file root, with no
  grammar rules and no Grammar-Kit. This is the foundation for later PSI-based
  features (folding, run-line markers, TODO highlighting, spellcheck); syntax
  highlighting is unaffected and continues to use the lexer-based highlighter
  directly. Verified against the real `com.intellij.lang.ParserDefinition`
  contract for IC-2024.2.4 and covered by a `BasePlatformTestCase` asserting
  zero `PsiErrorElement`s and a lossless PSI-text round-trip.
- Added "New > Geblang File" file templates: four bundled `.gb.ft` templates
  (File, Class, Module, Test) selectable from a `GeblangCreateFileAction`
  ("New > Geblang File" in the Project View / File menu), plus a
  `GeblangFileTemplateGroupFactory` exposing the same templates under
  Settings > Editor > File and Code Templates > Geblang for customisation.
- Added a "Geblang Test" run configuration: runs `geblang test --format teamcity <target>`
  (a `.gb` file or directory, with an optional `--tag` filter and working directory) and
  renders the results in IntelliJ's native SMTestRunner test tree. Includes a best-effort
  `GeblangTestLocator` for double-click navigation from the test tree back to the
  `class`/`func` declaration in source.
- Added a "geblang executable not found" warning notification: shown at most once
  per project session, the first time a `.gb` file is opened, if the configured
  executable path cannot be resolved. Includes a **Configure…** action that opens
  Settings > Languages & Frameworks > Geblang directly. No warning is shown when
  the executable resolves successfully.
- Added decorator highlighting: `@name` and dotted composite decorators
  (`@Assert.email`, `@Foo.bar.baz`) are now lexed as a single `DECORATOR` token,
  with their own customisable color under Settings > Editor > Color Scheme >
  Geblang > Decorator. Decorator argument lists (`@Get("/x")`) are lexed normally
  after the decorator name. A bare `@` not followed by an identifier is still the
  `@` operator.
- Added comprehensive unit tests for the Geblang lexer, covering comments, strings,
  numbers, keywords, operators, and bracket tokenization.
- Added a guard test confirming `//` tokenizes as the integer-division operator and
  is never mistaken for a comment, distinguishing it from `#`-style line comments.
- Added a round-trip coverage test verifying the lexer's token stream has no gaps or
  overlaps and exactly reconstructs the original source text.
- Wired up the IntelliJ Platform Gradle Plugin 2.x test framework
  (`testFramework(TestFrameworkType.Platform)`) plus JUnit 4 and opentest4j test
  dependencies so `./gradlew test` can build and run platform-based tests.
- Added 102 live templates (code snippets), ported from the vscode-geblang
  extension's snippet set and scoped to `.gb` files via a new `GeblangTemplateContextType`
  (contextId `GEBLANG`). Covers function/class/interface/enum declarations, control
  flow, decorators, the module system, dunder overrides, and standard library idioms
  (async, crypto, regex, HTTP, encoding, streams, sockets, SSH, FFI, LLM client,
  messaging). Type a prefix (e.g. `func`, `testclass`) and press Tab to expand.
