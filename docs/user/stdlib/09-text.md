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
| `codePointAt(index)` | `int` | Unicode code point at `index`, or `null` if out of range (the "ord" of one character) |
| `codePoints()` | `list<int>` | All Unicode code points as a list |

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

Cast with `as int`, `as decimal`, `as float`, `as bool` where needed. Also new in 1.0.2: `as bytes` encodes the string as UTF-8, and a `bytes` value cast back `as string` decodes UTF-8 (the cast raises a catchable `RuntimeError` if the byte sequence is not valid UTF-8).

```gb
let b = "résumé" as bytes;
io.println(b.length);     # 8 (two two-byte runes plus four ASCII)
io.println(b as string);  # résumé
```

---

## String Factories: `string`

Import `string`. The module is a small namespace for static / factory functions that don't belong on a string instance (you can't ask a non-existent string for its codepoint). Everything else string-related is an instance method - see [String Methods](#string-methods) above.

| Function | Returns | Description |
|----------|---------|-------------|
| `fromCodePoint(n)` | `string` | Single-character string for the Unicode codepoint `n` (this is "chr"). Rejects negative values, values above U+10FFFF, and the UTF-16 surrogate range U+D800..U+DFFF. |
| `fromCodePoints(list<int>)` | `string` | Multi-character string built from a list of codepoints. Same validation per element. |
| `compare(a, b)` | `int` | Three-way comparison returning -1 / 0 / +1. Pass it straight to `xs.sort(string.compare)` (sort accepts a three-way comparator). Compares the underlying UTF-8 bytes, which agrees with codepoint order. |
| `equalsFold(a, b)` | `bool` | Case-insensitive equality respecting Unicode case folding. `string.equalsFold("CafÉ", "café")` is `true`. |

```gb
import string;
import io;

io.println(string.fromCodePoint(65));               # A
io.println(string.fromCodePoint(8364));             # €
io.println(string.fromCodePoints([72, 105, 33]));   # Hi!
io.println(string.compare("apple", "banana"));      # -1
io.println(string.equalsFold("Hello", "HELLO"));    # true
```

Geblang has no separate `chr` / `ord`: `string.fromCodePoint(n)` is
`chr` (codepoint to character) and `s.codePointAt(i)` is `ord`
(character to codepoint). `s.codePoints()` and `string.fromCodePoints`
convert a whole string to and from a `list<int>` of codepoints.

For *timing-attack-safe* string equality (HMAC verification, token comparison, etc.) use `secrets.constantTimeEqual(a, b)` from the security module - see [Security](12-security.md). `string.equalsFold` and `string.compare` are **not** constant-time.

---

### Regex string-method variants

Three convenience methods route through the `re` module without
requiring the `import re`:

| Method | Returns | Description |
|--------|---------|-------------|
| `splitRegex(pattern)` | `list<string>` | Split by a regex pattern. |
| `replaceRegex(pattern, replacement)` | `string` | Replace every regex match. `$1` / `$2` capture-group references work in the replacement. |
| `matchesRegex(pattern)` | `bool` | True when the string contains a match. |

```gb
let parts = "foo, bar; baz".splitRegex("[,;] *");          # ["foo","bar","baz"]
let normalised = "John Smith".replaceRegex("(\\w+) (\\w+)", "$2, $1"); # "Smith, John"
let ok = "foo123".matchesRegex("[a-z]+[0-9]+");            # true
```

The pattern compile cache (introduced in 1.0.5 for the `re` module)
applies here too, so repeated calls with the same pattern skip the
recompile.

## Builder: `strings.StringBuilder`

Import `strings`. `StringBuilder` is a builder-backed accumulator. Use it for tight loops that append many fragments - internally a single `strings.Builder` grows amortised O(n) instead of the O(n²) cost of repeated `acc = acc + fragment` allocating a fresh string every iteration.

```gb
import strings;
import io;

let sb = strings.StringBuilder();
for (int i = 0; i < 10; i++) {
    sb.append("part-");
    sb.append(i as string);
    sb.appendLine("");
}
io.println(sb.build());
sb.dispose();
```

| Method | Returns | Description |
|--------|---------|-------------|
| `StringBuilder(initial = "")` | `StringBuilder` | Construct a new builder, optionally pre-seeded with `initial`. |
| `append(s)` | `StringBuilder` | Append a fragment. Returns `this` for chaining. |
| `appendLine(s)` | `StringBuilder` | Append a fragment followed by `\n`. Returns `this`. |
| `build()` | `string` | Materialise the accumulated content. |
| `length()` | `int` | Current byte length. |
| `clear()` | `StringBuilder` | Reset the buffer to empty. Returns `this`. |
| `dispose()` | `void` | Release the underlying handle. Safe to call multiple times. Call in long-running processes to free the builder. |

For the common `acc = acc + "literal"` idiom inside a loop, the bytecode compiler **automatically** swaps the local to a builder-backed representation behind the scenes, then materialises it back to a string on the next read. No source change required:

```gb
string acc = "";
for (int i = 0; i < 10000; i++) {
    acc = acc + "x";          # compiler emits builder-backed append
}
io.println(acc.length());     # 10000 - acc materialises here
```

Reach for the explicit `StringBuilder` when the auto-rewrite doesn't apply: dynamic (non-literal) RHS, accumulator written through a class field, or when you want chained writes (`sb.append("a").append("b")`).

### Low-level primitives: `strbuilder`

`StringBuilder` is implemented in `stdlib/strings.gb` on top of the `strbuilder` native module. The handle-based primitives are available directly for advanced uses:

| Function | Returns | Description |
|----------|---------|-------------|
| `strbuilder.new(initial = "")` | handle | Create a new builder; returns an opaque handle. |
| `strbuilder.append(h, s)` | handle | Append `s` to the builder; returns `h`. |
| `strbuilder.appendLine(h, s)` | handle | Append `s` followed by `\n`. |
| `strbuilder.build(h)` | `string` | Materialise the current content. |
| `strbuilder.length(h)` | `int` | Current byte length. |
| `strbuilder.clear(h)` | handle | Reset the buffer. |
| `strbuilder.dispose(h)` | `null` | Release the handle. |

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

## PCRE-compatible regex: `pcre`

Import `pcre`. `pcre` runs a PCRE-style engine (backed by .NET's
regex syntax) that supports the features RE2 omits: lookahead,
lookbehind, backreferences, atomic groups, possessive quantifiers,
and named captures via either `(?P<name>...)` (PHP / Python) or
`(?<name>...)` (.NET / PCRE2) syntax. Use it when porting PHP
code or when the pattern needs features RE2 can't express.

`re` and `pcre` coexist. Prefer `re` for hot paths or any input
that may be user-controlled (RE2 has linear-time matching and no
catastrophic backtracking); reach for `pcre` when you need the
richer syntax.

Every function accepts an optional flags string as the last
argument:

| Flag | Meaning |
|------|---------|
| `i` | Case-insensitive |
| `m` | Multiline (`^` / `$` match line boundaries) |
| `s` | Dotall (`.` matches newlines) |
| `x` | Extended (whitespace ignored, `#` comments allowed) |

### Functions

- `test(pattern, text, flags = "")` - returns `bool`.
- `find(pattern, text, flags = "")` - first match as a `string`, or `null`.
- `findAll(pattern, text, flags = "")` - every non-overlapping match as `list<string>`.
- `match(pattern, text, flags = "")` - dict with `text` / `groups` / `named` (same shape as `re.match`), or `null`.
- `matchAll(pattern, text, flags = "")` - `list<dict>`.
- `replace(pattern, replacement, text, flags = "")` - returns a `string`. Use `$1`, `$2`, `${name}` for backrefs.
- `split(pattern, text, flags = "")` - returns a `list<string>`.
- `quote(text)` - escapes regex metacharacters in a literal string.

### Examples

```gb
import pcre;
import io;

# Lookahead: PCRE-only.
io.println(pcre.find('\w+(?=ing\b)', "swimming and running"));  # swimm

# Lookbehind: PCRE-only.
io.println(pcre.find('(?<=\$)\d+', "price is $42"));            # 42

# Backreferences: PCRE-only.
io.println(pcre.test('(\w+)\s+\1', "hello hello"));             # true

# PHP-style (?P<name>...) syntax works unchanged.
let m = pcre.match('(?P<word>[a-z]+)(?P<num>\d+)', "abc123");
io.println(m["named"]["word"]);                                  # abc

# Numbered backreference in replacement.
io.println(pcre.replace('(\w+) (\w+)', "$2 $1", "hello world")); # world hello

# Case-insensitive via flags.
io.println(pcre.test("hello", "HELLO", "i"));                    # true

# Escape user input before splicing into a pattern.
let needle = pcre.quote("a.b+c");
io.println(pcre.test(needle, "x a.b+c y"));                      # true
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

## Unicode normalisation: `unicode` (1.6.0)

The `unicode` module exposes the four Unicode normalisation
forms via `unicode.normalize(s, form)`. `form` is the canonical
SPDX-style name: `"NFC"`, `"NFD"`, `"NFKC"`, or `"NFKD"`.

```gb
import unicode;

let nfd = "é";                 // e + U+0301 combining acute (2 code points)
let nfc = unicode.normalize(nfd, "NFC");
io.println(nfc.length());          // 1 - now a single code point
io.println(unicode.normalize("ﬁ", "NFKC"));   // "fi" - ligature decomposed
```

| Function | Returns | Description |
|----------|---------|-------------|
| `unicode.normalize(s, form)` | `string` | A copy of `s` normalised under `form`. Throws on an unknown form. |
| `unicode.isNormalized(s, form)` | `bool` | True when `s` is already in `form`. Cheap; does not allocate a normalised copy. |

### When to use which form

| Form | Effect | Typical use |
|------|--------|-------------|
| **NFC** | Canonical composition. Combining marks fold into precomposed code points where one exists. | Storage, display, equality comparison of "the same character" inputs. The Web's standard. |
| **NFD** | Canonical decomposition. Precomposed characters split into base + combining marks. | Sorting that respects diacritics, accent-insensitive search after stripping marks. |
| **NFKC** | Compatibility composition. Compatibility equivalents (ligatures, full-width, superscripts) fold to their base form, then canonical composition is applied. | Search across visually-similar characters; input sanitisation. |
| **NFKD** | Compatibility decomposition. Same compatibility folding as NFKC but no recomposition. | The fully decomposed canonical form; rarely needed directly. |

Normalising untrusted input before storing or comparing is good
defensive practice: it stops bypass attacks that rely on visually
identical but byte-different strings (`"admin"` vs `"admın"`
with a Turkish dotless i, for example - NFKC won't collapse
that, but normalising at least makes equality reliable).

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
