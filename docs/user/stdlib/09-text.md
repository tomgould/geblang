# Text, Regex, Markdown, And Templates

## String Methods

Strings are immutable values. Every method returns a new string or a derived
value - the original is unchanged.

### Inspection

| Method | Returns | Description |
|--------|---------|-------------|
| `length()` | `int` | Number of Unicode code points |
| `isEmpty()` | `bool` | `true` when the string has no characters |
| `get(index)` | `string` | Single character at `index` (negative = from end) |
| `chars()` | `list<string>` | All characters as a list |
| `codePointAt(index)` | `int` | Unicode code point at `index`, or `null` if out of range |

```gb
import io;

let s = "hello";
io.println(s.length());     # 5
io.println(s.isEmpty());    # false
io.println(s.get(0));       # h
io.println(s.get(-1));      # o
io.println(s.chars());      # [h, e, l, l, o]
io.println(s.codePointAt(0)); # 104
```

### Searching

| Method | Returns | Description |
|--------|---------|-------------|
| `contains(needle)` | `bool` | `true` when `needle` appears anywhere in the string |
| `startsWith(prefix)` | `bool` | `true` when the string begins with `prefix` |
| `endsWith(suffix)` | `bool` | `true` when the string ends with `suffix` |
| `indexOf(needle)` | `int` | First index of `needle`, or `-1` if not found |
| `lastIndexOf(needle)` | `int` | Last index of `needle`, or `-1` if not found |
| `count(needle)` | `int` | Number of non-overlapping occurrences of `needle` |

```gb
import io;

let s = "hello world";
io.println(s.contains("world"));   # true
io.println(s.startsWith("hello")); # true
io.println(s.endsWith("world"));   # true
io.println(s.indexOf("l"));        # 2
io.println(s.lastIndexOf("l"));    # 9
io.println(s.count("l"));          # 3
```

### Slicing And Substrings

`substring(start[, end])` and `slice(start[, end])` are aliases - both extract a
sub-sequence by code-point index.  Negative indices count from the end.

| Method | Returns | Description |
|--------|---------|-------------|
| `substring(start[, end])` | `string` | Characters from `start` up to (not including) `end` |
| `slice(start[, end])` | `string` | Same as `substring` |

```gb
import io;

let s = "hello world";
io.println(s.substring(6));      # world
io.println(s.substring(0, 5));   # hello
io.println(s.slice(-5));         # world
io.println(s.slice(0, -6));      # hello
```

### Transformation

| Method | Returns | Description |
|--------|---------|-------------|
| `lower()` | `string` | All characters lower-cased |
| `upper()` | `string` | All characters upper-cased |
| `trim()` | `string` | Leading and trailing whitespace removed |
| `trimStart()` | `string` | Leading whitespace removed |
| `trimEnd()` | `string` | Trailing whitespace removed |
| `replace(old, new[, n])` | `string` | Replace occurrences of `old` with `new`; `n` limits replacements |
| `reverse()` | `string` | Characters in reversed order |
| `repeat(n)` | `string` | String repeated `n` times |
| `padStart(len[, pad])` | `string` | Pad to at least `len` characters on the left |
| `padEnd(len[, pad])` | `string` | Pad to at least `len` characters on the right |

```gb
import io;

let s = "  Hello, World!  ";
io.println(s.trim());                       # Hello, World!
io.println(s.lower());                      # "  hello, world!  "
io.println(s.upper());                      # "  HELLO, WORLD!  "
io.println("abc".repeat(3));               # abcabcabc
io.println("hello".reverse());             # olleh
io.println("7".padStart(4, "0"));          # 0007
io.println("hi".padEnd(5, "."));           # hi...
io.println("hello world".replace("o", "0")); # hell0 w0rld
io.println("hello world".replace("o", "0", 1)); # hell0 world
```

### Splitting And Joining

| Method | Returns | Description |
|--------|---------|-------------|
| `split(sep)` | `list<string>` | Split on `sep`; returns list of parts |
| `format(...)` | `string` | `printf`-style formatting with positional `{}` placeholders |

```gb
import io;

let csv = "a,b,c,d";
let parts = csv.split(",");
io.println(parts);          # [a, b, c, d]
io.println(parts.length()); # 4

let msg = "Hello, {}! You have {} messages.".format("Ada", 3);
io.println(msg);  # Hello, Ada! You have 3 messages.
```

### Conversion

| Method | Returns | Description |
|--------|---------|-------------|
| `toString()` | `string` | Returns the string itself (identity) |

Cast with `as int`, `as decimal`, `as float`, `as bool` where needed.

---

## Regex: `re`

Import `re`. The module is a thin wrapper over Go's [`regexp/syntax`](https://pkg.go.dev/regexp/syntax) (RE2 dialect, no backreferences but full Unicode, anchors, and lookahead-free alternation).

- `test(pattern, text)` - returns `bool`.
- `find(pattern, text)` - returns the first match as a `string`, or `null`.
- `findAll(pattern, text)` - returns every non-overlapping match as `list<string>`.
- `match(pattern, text)` - returns a dict with the first match plus capture groups (see below), or `null`.
- `matchAll(pattern, text)` - returns `list<dict>` with one entry per non-overlapping match.
- `replace(pattern, replacement, text)` - returns a `string`. Use `$1`, `$2`, `${name}` in `replacement` to reference capture groups.
- `split(pattern, text)` - returns a `list<string>`.

### Match results

`re.match` and `re.matchAll` return dicts in the same shape:

| Field | Type | Description |
|-------|------|-------------|
| `text` | `string` | The whole match (alias for `groups[0]`). |
| `groups` | `list<string>` | Every group in order. `groups[0]` is the whole match; `groups[1]`, `groups[2]`, ... are the parenthesised subexpressions. |
| `named` | `dict<string, string>` | Named capture groups (`(?P<name>...)`) keyed by name. |

```gb
import re;
import io;

let m = re.match("(?P<word>[A-Za-z]+)([0-9]+)", "Ada123");
io.println(m["text"]);              # Ada123
io.println(m["groups"][1]);         # Ada      (numbered group 1)
io.println(m["groups"][2]);         # 123      (numbered group 2)
io.println(m["named"]["word"]);     # Ada      (named group)

# Extract every name=value pair from a free-form string.
let pairs = re.matchAll("(?P<k>\\w+)=\"(?P<v>[^\"]*)\"",
                       "user=\"ada\" role=\"admin\"");
for (pair in pairs) {
    io.println(pair["named"]["k"] + " -> " + pair["named"]["v"]);
}
```

### Anchors and flags

Geblang regexes follow Go's RE2 syntax. Anchors `^`/`$` match at start/end of
input by default; pass `(?m)` to make them match line boundaries. Other useful
inline flags:

- `(?i)` - case-insensitive
- `(?s)` - dot matches newline
- `(?U)` - swap greedy and non-greedy quantifiers

```gb
io.println(re.test("(?i)^hello",  "Hello World"));   # true
io.println(re.test("(?s)foo.bar", "foo\nbar"));      # true
```

---

## Markdown: `markdown`

Import `markdown`. The module supports full [GitHub Flavored Markdown](https://github.github.com/gfm/) (GFM) - tables, strikethrough, task lists, autolinks, ordered lists, blockquotes, horizontal rules, setext headings, and raw HTML passthrough.

- `renderHtml(source)` - render to HTML string.
- `parse(source)` - returns a `list<dict>` of block nodes. Each dict has a `"type"` key; additional keys depend on the type (see below).
- `stripText(source)` - extract all plain text, stripping markup.

Block types returned by `parse`:

| `type` | Additional keys |
|--------|----------------|
| `"heading"` | `level: int`, `text: string` |
| `"paragraph"` | `text: string` |
| `"list"` | `items: list<string>` |
| `"ordered_list"` | `items: list<string>` |
| `"task_list"` | `items: list<dict>` - each `{text: string, checked: bool}` |
| `"code"` | `lang: string`, `code: string` |
| `"table"` | `headers: list<string>`, `rows: list<list<string>>` |
| `"blockquote"` | `text: string` |
| `"hr"` | _(no extra keys)_ |
| `"html"` | `html: string` |

```gb
import markdown;
import io;

let src = "## Hello\n\n| col1 | col2 |\n|------|------|\n| a | b |\n\n- [x] done\n- [ ] todo";
io.println(markdown.renderHtml(src));

let blocks = markdown.parse(src);
io.println(blocks[0]["type"]);          // heading
io.println(blocks[1]["headers"][0]);    // col1
io.println(blocks[2]["items"][0]["checked"]);  // true
```

---

## Templates: `template`

Import `template`:

- `renderString(source, data)`
- `Template(source)`
- `load(path)`
- `Engine(options)`

```gb
import template;

io.println(template.renderString("Hello {{.name}}", {"name": "Ada"}));
```
