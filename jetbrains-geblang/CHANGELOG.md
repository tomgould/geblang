# Changelog

All notable changes to the Geblang IntelliJ plugin will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added

- Added comprehensive unit tests for the Geblang lexer, covering comments, strings,
  numbers, keywords, operators, and bracket tokenization.
- Added a guard test confirming `//` tokenizes as the integer-division operator and
  is never mistaken for a comment, distinguishing it from `#`-style line comments.
- Added a round-trip coverage test verifying the lexer's token stream has no gaps or
  overlaps and exactly reconstructs the original source text.
- Wired up the IntelliJ Platform Gradle Plugin 2.x test framework
  (`testFramework(TestFrameworkType.Platform)`) plus JUnit 4 and opentest4j test
  dependencies so `./gradlew test` can build and run platform-based tests.
