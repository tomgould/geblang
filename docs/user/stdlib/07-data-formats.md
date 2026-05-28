# Data Formats

Geblang includes native parsers and serializers for common data formats.

## JSON

Import `json`:

- `parse(text)` - parse JSON string into Geblang value
- `parseAs(text, ClassRef)` - parse and reconstruct a class instance
  (calls static `__deserialize(dict)` when defined, else the
  constructor matched on parameter names)
- `tryParse(text)` - returns `null` on error instead of throwing
- `stringify(value)` - serialize to JSON string (accepts dicts, lists,
  scalars, and user-defined class instances; classes can override with
  `__serialize()`)
- `validate(text)` - returns `{"valid": bool, "error": string|null}`
- `validateDetailed(text)` - returns more detailed error information
- `reader(source)` - streaming reader (see below)
- `stream(source, handler)` - streaming with a callback

```gb
import json;

let data = json.parse('{"name":"Ada","roles":["admin"]}');
io.println(data["name"]);                    # Ada
io.println(json.stringify({"ok": true}));    # {"ok":true}

let result = json.tryParse("not json");
if (result == null) {
    io.println("invalid JSON");
}
```

## YAML

Import `yaml`:

- `parse(text)`, `parseAs(text, ClassRef)`, `tryParse(text)`,
  `stringify(value)`
- `validate(text)`, `validateDetailed(text)`
- `reader(source)`, `stream(source, handler)`

```gb
import yaml;

let cfg = yaml.parse("server:\n  port: 8080\n");
io.println(cfg["server"]["port"] as string);   # 8080
```

## TOML

Import `toml`:

- `parse(text)`, `parseAs(text, ClassRef)`, `tryParse(text)`,
  `stringify(value)`
- `validate(text)`, `validateDetailed(text)`

TOML is typically configuration-sized and does not expose a streaming reader.

```gb
import toml;

let cfg = toml.parse('[database]\nurl = "postgres://localhost/app"\n');
io.println(cfg["database"]["url"]);
```

## XML

Import `xml`:

- `parse(text)`, `parseAs(text, ClassRef)`, `tryParse(text)`,
  `stringify(value)`
- `validate(text)`, `validateDetailed(text)`
- `reader(source)`, `stream(source, handler)`

## Class instances: stringify and parseAs

`json.stringify`, `yaml.stringify`, and `toml.stringify` accept
user-defined class instances. By default the call emits the
**public** fields - any field whose name does not start with `_` or
`__`. Classes can replace the default by implementing
`__serialize()`; the return value is recursively serialised.

```gb
import json;

class Point {
    int x;
    int y;
    int _secret;
    func Point(int x, int y) { this.x = x; this.y = y; this._secret = 99; }
}

io.println(json.stringify(Point(3, 4)));
/* {"x":3,"y":4} - _secret is omitted. */
```

The companion `parseAs(text, ClassRef)` reconstructs an instance.
It calls a static `__deserialize(dict)` factory on the target
class when one is defined; otherwise it matches dict keys against
the constructor's parameter names and calls the constructor
positionally.

```gb
let p = json.parseAs("{\"x\":3,\"y\":4}", Point);
io.println(p.x);
```

The same conventions apply to `yaml.parseAs`, `toml.parseAs`, and
`xml.parseAs`. See chapter 6's *Serialisation* section for details.

## CSV

Import `csv` for both in-memory parsing and streaming.

| Function | Returns | Description |
|----------|---------|-------------|
| `csv.parse(text, options?)` | `list<list<string>>` | Parses CSV text into a list of rows; each row is a list of cell strings. Options: `delimiter` (single char), `trimSpace` (bool). |
| `csv.parseDict(text, options?)` | `list<dict<string, string>>` | Parses CSV text using the first row as headers; returns a list of dicts keyed by header name. Same options. |
| `csv.stringify(rows, options?)` | `string` | Serialises a list-of-lists into CSV text. Options: `delimiter`. |
| `csv.reader(source)` | `CsvReader` | Returns an incremental pull reader over a file / bytes / string source. Use `.hasNext()`, `.next()`, `.close()`. |
| `csv.stream(source, handler)` | `void` | Push-based streaming with one callback per row. |

```gb
import csv;
import io;

# In-memory parse / stringify.
let rows = csv.parse("name,age\nAda,37\nGrace,55");
io.println(rows[1][0]); # Ada

let dicts = csv.parseDict("name,age\nAda,37\nGrace,55");
io.println(dicts[0]["age"]); # 37

# Custom delimiter + trimSpace.
let semi = csv.parse("a; b; c\n1; 2; 3", {"delimiter": ";", "trimSpace": true});

# Large file - streaming.
csv.stream(io.open("large.csv", "r"), func(row): void {
    io.println(row[0]);
});
```

## Streaming readers

For large JSON, YAML, and XML files, use `reader` to pull records
one at a time rather than loading the whole file into memory:

```gb
import json;

let reader = json.reader(io.open("events.jsonl", "r"));
let record = reader.next();
while (record != null) {
    io.println(record["id"]);
    record = reader.next();
}
reader.close();
```

The reader object exposes `next()` and `close()`.

## Serde - format-agnostic serialization

Import `serde` when the format is runtime-determined (for example, a tool
that accepts a `--format json|yaml|toml` flag):

```gb
import serde;

let value = serde.parse("json", '{"ok":true}');
let text  = serde.stringify("yaml", value);
```

Supported format strings: `"json"`, `"yaml"`, `"toml"`.

## Schema validation

Import `schema` for JSON Schema-style validation of dicts and lists. This is
useful for validating API request bodies, configuration files, and parsed
data before using it.

```gb
import schema;

let userSchema = {
    "type": "object",
    "required": ["name", "email"],
    "properties": {
        "name":  {"type": "string"},
        "email": {"type": "string"},
        "age":   {"type": "number", "minimum": 0, "maximum": 150},
        "roles": {
            "type": "array",
            "items": {"type": "string"}
        }
    }
};

let result = schema.validate({"name": "Ada", "email": "ada@example.com"}, userSchema);
if (result["valid"] as bool) {
    io.println("valid");
} else {
    for (err in result["errors"]) {
        io.println(err);
    }
}
```

`schema.validate(value, schema)` returns:
- `{"valid": true}` on success
- `{"valid": false, "errors": list<string>}` on failure, with one message per violation

### Common schema keywords

| Keyword         | Applies to      | Description                                         |
|-----------------|-----------------|-----------------------------------------------------|
| `type`          | any             | `"string"`, `"number"`, `"bool"`, `"object"`, `"array"`, `"null"` |
| `required`      | object          | list of required property names                     |
| `properties`    | object          | map of property name to sub-schema                   |
| `items`         | array           | sub-schema applied to each element                  |
| `minimum`       | number          | inclusive lower bound                               |
| `maximum`       | number          | inclusive upper bound                               |
| `minLength`     | string          | minimum character count                             |
| `maxLength`     | string          | maximum character count                             |
| `minItems`      | array           | minimum element count                               |
| `maxItems`      | array           | maximum element count                               |
| `enum`          | any             | value must be one of the listed values              |
| `nullable`      | any             | allow `null` in addition to the declared type       |

Pair `schema.validate` with `json.parse` or `yaml.parse` to validate
untrusted external input:

```gb
let body = json.parse(io.stdinReadAll());
let check = schema.validate(body, requestSchema);
if (!(check["valid"] as bool)) {
    throw errors.new("ValidationError", "invalid request body");
}
```
