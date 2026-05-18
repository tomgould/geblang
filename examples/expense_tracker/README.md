# Expense Tracker Example

This example shows a small multi-module Geblang package with a manifest-backed
`src/` tree:

- `config.gb` provides application settings.
- `domain.gb` defines the `Expense` value object and formatting helper.
- `repository.gb` owns in-memory persistence.
- `service.gb` validates input and coordinates writes.
- `reporting.gb` builds aggregate read models.
- `main.gb` wires the modules together.

Run it from the repository root:

```sh
go run ./cmd/geblang examples/expense_tracker/src/main.gb
```
