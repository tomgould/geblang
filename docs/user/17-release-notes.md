# Release Notes

## 1.0.1 (2026-05)

A correctness release for the generics surface.

- Generic class types are now invariant in their type parameters:
  `Box<Sub>` is not assignable to `Box<Base>` even when `Sub extends Base`.
- Explicit type arguments at call sites: `Box<int>()` and
  `assertIs<string>(x)` now parse and bind T.
- On a generic function call, an explicit `<T>` replaces T in every
  position of the signature - parameters, return type, and body.
- Invariance error messages include the offending value's reified
  bindings: `got Container<Sub>` rather than the bare `got Container`.
- Build: Go 1.26.3 minimum.

## 1.0.0 (2026-05)

The first stable release. Everything documented in this manual is in scope
for the 1.0 stability promise: source-level syntax, stdlib APIs, runtime
semantics and the bytecode chunk format. Future 1.x releases will add
features but not break backwards compatibility.


