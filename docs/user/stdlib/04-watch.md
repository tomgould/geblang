# Watch

Import `watch` for simple polling-based file change detection.

Functions:

- `snapshot(path)`: capture the current state for a file or directory.
- `wait(path, previousSnapshot, timeoutMilliseconds)`: wait until the state
  differs or the timeout expires.

Example:

```gb
import io;
import watch;

let before = watch.snapshot("config/app.yaml");
let changed = watch.wait("config/app.yaml", before, 5000);

if (changed["changed"] as bool) {
    io.println("config changed");
}
```

This module is intentionally simple. It is useful for tools, local development,
and tests; high-volume filesystem watching remains a future native class area.
