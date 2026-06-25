# Config

The `config` source-stdlib module provides helpers for nested configuration
dictionaries. It is useful for merging defaults, environment-specific override
layers, parsed config files, and application settings passed into framework
components.

```gb
import config;

let cfg = config.layer([
    {"debug": false, "db": {"host": "localhost", "port": 5432}},
    {"debug": true, "db": {"database": "app"}}
]);

io.println(config.require(cfg, "db.host"));
io.println(config.getOr(cfg, "cache.ttl", 300));
```

## Functions

| Function | Returns | Description |
|----------|---------|-------------|
| `clone(data)` | `dict<string, any>` | Shallow-copy a configuration dictionary |
| `merge(base, overrides)` | `dict<string, any>` | Recursively merge overrides into a copy of base |
| `defaults(values, defaultValues)` | `dict<string, any>` | Apply defaults first, then explicit values |
| `layer(layers)` | `dict<string, any>` | Merge a list of dictionaries from first to last |
| `has(data, path)` | `bool` | Test whether a dotted path exists |
| `get(data, path)` | `any` | Return a required dotted-path value or throw `ValueError` |
| `getOr(data, path, fallback)` | `any` | Return a dotted-path value or fallback |
| `require(data, path)` | `any` | Alias for `get` when the call site wants explicit required semantics |
| `parse(format, text)` | `Config` | Parse a serde-supported document into a `Config` object |

`merge` is recursive only when both sides at a key are dictionaries. Otherwise
the override value replaces the base value.

```gb
let base = {
    "app": {"name": "Geb", "debug": false},
    "cache": {"ttl": 60}
};

let local = {
    "app": {"debug": true},
    "cache": {"driver": "redis"}
};

let merged = config.merge(base, local);
io.println(merged["app"]["name"]);
io.println(merged["app"]["debug"]);
io.println(merged["cache"]["driver"]);
```

## Dotted Paths

Dotted paths walk nested dictionaries. They do not parse list indexes.

```gb
let settings = {
    "db": {
        "primary": {
            "host": "localhost",
            "port": 5432
        }
    }
};

io.println(config.has(settings, "db.primary.host"));
io.println(config.get(settings, "db.primary.port"));
io.println(config.getOr(settings, "db.replica.host", "none"));
```

`get` and `require` throw `ValueError` when any path segment is missing.
`getOr` returns the fallback if a segment is missing or a non-dictionary value is
encountered before the end of the path.

## `Config`

`Config` is an immutable-style wrapper around a cloned dictionary.

| Method | Returns | Description |
|--------|---------|-------------|
| `has(path)` | `bool` | Test whether a dotted path exists |
| `get(path)` | `any` | Return a required dotted-path value |
| `require(path)` | `any` | Alias for `get` |
| `getOr(path, fallback)` | `any` | Return a value or fallback |
| `toDict()` | `dict<string, any>` | Return a shallow copy of the stored config |

```gb
let cfg = config.Config({
    "mail": {"from": "noreply@example.com"}
});

io.println(cfg.require("mail.from"));
io.println(cfg.getOr("mail.transport", "smtp"));
```

## Parsing

`config.parse(format, text)` delegates to `serde.parse`, so it supports the
formats available through the serialization modules, such as `json`, `yaml`,
`toml`, and `xml` where the parsed top-level value is an object.

```gb
let cfg = config.parse("json", '{"server":{"port":8080}}');
io.println(cfg.require("server.port"));
```

Use `Config.toDict()` when an API expects a plain dictionary:

```gb
let options = cfg.toDict();
```
