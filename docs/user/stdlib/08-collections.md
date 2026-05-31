# Collections

## Counting Collections

Use `length()` to get the size of a built-in collection:

```gb
import io;

list<int> nums = [1, 2, 3];
set<int> ids = {1, 2, 2, 3};
dict<string, int> scores = {"ada": 10, "grace": 12};

io.println(nums.length());   # 3 elements
io.println(ids.length());    # 3 unique elements
io.println(scores.length()); # 2 key/value entries
```

Use `isEmpty()` for emptiness checks. It communicates intent better than
comparing `length()` to zero:

```gb
if (scores.isEmpty()) {
    io.println("no scores yet");
}
```

On lists, `count(fn)` means "count matching elements", not "return the total
size":

```gb
list<int> nums = [1, 2, 3, 4, 5, 6];
io.println(nums.length()); # 6
io.println(nums.count(func(int n): bool { return n % 2 == 0; })); # 3
```

The `collections` module also provides `collections.length(value)` and
`collections.isEmpty(value)` helpers when writing generic collection-oriented
code.

## Built-In List Methods

Lists are mutable, ordered sequences with reference semantics: assigning a list
to another variable, passing it to a function, or storing it in a field shares
the same underlying list. In-place mutations are visible through every
reference.

Two flavours of mutation method exist:

- **In-place methods** mutate the receiver and return `null`. Aliases of the
  same list see the change. These are `set`, `append`, `extend`, and `clear`,
  plus index assignment (`xs[0] = v`). On a frozen list they raise
  `ImmutableError`.
- **Copy-and-return methods** allocate a new list and leave the receiver
  unchanged. These are `push`, `pop`, `prepend`, `insert`, `concat`, `reverse`,
  `sort`, `slice`, `map`, `filter`, and friends. To accumulate, either reassign
  the result (`xs = xs.push(item);`) or use the in-place `append` for
  amortised O(1) growth.

Generic annotations such as `list<int>` are checked when values cross typed
declaration and function/method call boundaries. They are not a permanent
runtime lock; the in-place mutators (`append`, `extend`) honour the declared
element type and raise `TypeError` on mismatch, but only when the list value
still carries its element-type tag at runtime. Untagged values (lists that
flowed through `any` without a typed boundary on the way back) skip the check.
Validate before mutation when accepting dynamic data.

### Inspection

| Method | Returns | Description |
|--------|---------|-------------|
| `length()` | `int` | Number of elements |
| `isEmpty()` | `bool` | `true` when the list has no elements |
| `get(index)` | `T` | Element at `index` (negative = from end) |
| `first()` | `T\|null` | First element, or `null` if empty |
| `last()` | `T\|null` | Last element, or `null` if empty |
| `contains(value)` | `bool` | `true` when `value` is in the list |
| `indexOf(value)` | `int` | First index of `value`, or `-1` if absent |

```gb
import io;

list<int> a = [10, 20, 30, 40, 50];
io.println(a.length());     # 5
io.println(a.isEmpty());    # false
io.println(a.get(0));       # 10
io.println(a.get(-1));      # 50
io.println(a.first());      # 10
io.println(a.last());       # 50
io.println(a.contains(30)); # true
io.println(a.indexOf(30));  # 2
```

### Slicing

`slice(start[, end])` extracts a sub-list by index.  Negative indices count from
the end.  The original list is not modified.

```gb
import io;

list<int> a = [1, 2, 3, 4, 5];
io.println(a.slice(1, 3));  # [2, 3]
io.println(a.slice(2));     # [3, 4, 5]
io.println(a.slice(-2));    # [4, 5]
io.println(a.slice(1, -1)); # [2, 3, 4]
```

Lists and strings also support Python-style slicing syntax. The bounds
are exclusive on the right:

```gb
list<int> a = [1, 2, 3, 4, 5];
io.println(a[1:3]);  # [2, 3]
io.println(a[:3]);   # [1, 2, 3]
io.println(a[2:]);   # [3, 4, 5]
io.println(a[:]);    # [1, 2, 3, 4, 5]
io.println("hello"[1:4]); # ell
```

### Mutation

**In-place** (mutate the receiver, return `null`):

| Method | Returns | Description |
|--------|---------|-------------|
| `set(index, value)` | `null` | Replace element at `index`. Equivalent to `xs[index] = value` |
| `append(value)` | `null` | Append `value` to the end. Amortised O(1); aliases share the growth |
| `extend(other)` | `null` | Append every element of `other` |
| `clear()` | `null` | Empty the list |

**Copy-and-return** (allocate a new list; receiver unchanged):

| Method | Returns | Description |
|--------|---------|-------------|
| `push(value)` | `list<T>` | New list with `value` appended |
| `pop()` | `list<T>` | New list with the last element removed |
| `prepend(value)` | `list<T>` | New list with `value` at the front |
| `unshift(value)` | `list<T>` | Alias for `prepend` |
| `insert(index, value)` | `list<T>` | New list with `value` inserted before `index` |

```gb
import io;

list<string> words = ["a", "b", "c"];

# In-place: mutates and returns null.
words.append("d");
io.println(words);          # [a, b, c, d]
words.extend(["e", "f"]);
io.println(words);          # [a, b, c, d, e, f]
words.set(0, "z");
io.println(words);          # [z, b, c, d, e, f]

# Copy-and-return: receiver unchanged.
let bigger = words.push("g");
io.println(words);          # [z, b, c, d, e, f]
io.println(bigger);         # [z, b, c, d, e, f, g]

# Aliases see in-place mutations.
let alias = words;
words.append("X");
io.println(alias);          # [z, b, c, d, e, f, X]

# Reassign the result of a copy-and-return method to accumulate
# without the in-place fast path.
let snapshot = ["start"];
snapshot = snapshot.push("end");
io.println(snapshot);       # [start, end]

# clear empties the list (and every alias of it).
words.clear();
io.println(words);          # []
```

### Ordering And Transformation

| Method | Returns | Description |
|--------|---------|-------------|
| `reversed()` | `list<T>` | New list with elements in reverse order |
| `sorted([comparator])` | `list<T>` | New list sorted in ascending order |
| `concat(other)` | `list<T>` | New list with `other` appended |
| `join(sep)` | `string` | Elements joined into a string with separator `sep` |
| `flatten()` | `list<any>` | Recursively flatten nested lists |
| `unique()` | `list<T>` | New list with duplicate values removed |

```gb
import io;

list<int> a = [3, 1, 4, 1, 5, 9, 2, 6];
io.println(a.reversed());       # [6, 2, 9, 5, 1, 4, 1, 3]
io.println(a.sorted());         # [1, 1, 2, 3, 4, 5, 6, 9]
io.println(a.unique());         # [3, 1, 4, 5, 9, 2, 6]

list<string> words = ["hello", "world", "foo"];
io.println(words.join(", "));   # hello, world, foo
io.println(words.join(""));     # helloworldfoo

list<int> b = [1, 2, 3];
list<int> c = [4, 5, 6];
io.println(b.concat(c));        # [1, 2, 3, 4, 5, 6]

let nested = [[1, 2], [3, [4, 5]]];
io.println(nested.flatten());   # [1, 2, 3, 4, 5]
```

### Functional Operations

| Method | Returns | Description |
|--------|---------|-------------|
| `map(fn)` | `list<U>` | Apply `fn` to each element; collect results |
| `filter(fn)` | `list<T>` | Keep elements for which `fn` returns `true` |
| `reduce(fn, initial)` | `U` | Fold elements left to right |
| `find(fn)` | `T\|null` | First element matching `fn`, or `null` |
| `findLast(fn)` | `T\|null` | Last element matching `fn`, or `null` |
| `any(fn)` | `bool` | `true` when at least one element matches `fn` |
| `all(fn)` | `bool` | `true` when every element matches `fn` |
| `count(fn)` | `int` | Count of elements matching `fn` |

```gb
import io;

list<int> nums = [1, 2, 3, 4, 5, 6];

let doubled = nums.map(func(int x): int { return x * 2; });
io.println(doubled);  # [2, 4, 6, 8, 10, 12]

let evens = nums.filter(func(int x): bool { return x % 2 == 0; });
io.println(evens);    # [2, 4, 6]

let sum = nums.reduce(func(int acc, int x): int { return acc + x; }, 0);
io.println(sum);      # 21

let first_even = nums.find(func(int x): bool { return x % 2 == 0; });
io.println(first_even); # 2

io.println(nums.any(func(int x): bool { return x > 5; }));  # true
io.println(nums.all(func(int x): bool { return x > 0; }));  # true
io.println(nums.count(func(int x): bool { return x % 2 == 0; })); # 3
```

### Keyed Functional Helpers

These methods take a key/predicate function and operate on lists directly. Each
is also available as a top-level helper in the `collections` module (e.g.
`collections.sortBy(xs, fn)`); the instance form and the module form are
interchangeable.

| Method | Returns | Description |
|--------|---------|-------------|
| `sortBy(fn)` | `list<T>` | New list sorted by the key `fn` returns for each element |
| `minBy(fn)` | `T\|null` | Element with the smallest key; `null` if empty |
| `maxBy(fn)` | `T\|null` | Element with the largest key; `null` if empty |
| `topBy(fn, n)` | `list<T>` | Top `n` elements by key (descending) |
| `topK(n)` | `list<T>` | Top `n` elements by natural ordering |
| `bottomK(n)` | `list<T>` | Bottom `n` elements by natural ordering |
| `sumBy(fn)` | `int\|decimal` | Sum of the numeric key for each element |
| `averageBy(fn)` | `decimal` | Mean of the numeric key |
| `frequencies()` | `dict<any, int>` | Count of each distinct value |
| `mode()` | `T\|null` | Most-frequent element |
| `indexBy(fn)` | `dict<any, T>` | Dict keyed by `fn(element)` (latest wins on collision) |
| `containsBy(fn)` | `bool` | `true` when any element matches `fn` |
| `differenceBy(other, fn)` | `list<T>` | Elements whose key isn't in `other`'s keys |
| `intersectionBy(other, fn)` | `list<T>` | Elements whose key is in both lists |
| `zipWith(other, fn)` | `list<U>` | Pairwise combine via `fn(a, b)` |
| `binarySearch(value)` | `int` | Index of `value` in a sorted list; `-1` if absent |
| `lowerBound(value)` | `int` | First insertion index keeping a sorted list sorted |
| `upperBound(value)` | `int` | Last insertion index keeping a sorted list sorted |
| `take(n)` | `list<T>` | First `n` elements (returns the whole list if `n > length`) |
| `lazyMap(fn)` | `iterable<U>` | Like `map` but produces an iterable; the work happens on iteration |
| `lazyFilter(fn)` | `iterable<T>` | Like `filter` but lazy |

```gb
import io;

list<dict<string, any>> users = [
    {"name": "Ada",   "age": 36},
    {"name": "Grace", "age": 85},
    {"name": "Alan",  "age": 41},
];

let oldest = users.maxBy(func(dict<string, any> u): int { return u["age"] as int; });
io.println(oldest["name"]);   # Grace

let by_age = users.sortBy(func(dict<string, any> u): int { return u["age"] as int; });
io.println(by_age[0]["name"]); # Ada
```

### Grouping, Chunking, And Partitioning

| Method | Returns | Description |
|--------|---------|-------------|
| `groupBy(fn)` | `dict<string, list<T>>` | Group elements by the key returned by `fn` |
| `chunk(size)` | `list<list<T>>` | Split into sub-lists of at most `size` elements |
| `partition(fn)` | `list<list<T>>` | `[[matching], [not-matching]]` |
| `zip(other)` | `list<list<any>>` | Pair elements with a second list |

```gb
import io;

list<string> names = ["Ada", "Alan", "Bob", "Alice"];
let by_letter = names.groupBy(func(string s): string { return s.substring(0, 1); });
io.println(by_letter["A"]);  # [Ada, Alan, Alice]
io.println(by_letter["B"]);  # [Bob]

list<int> nums = [1, 2, 3, 4, 5];
io.println(nums.chunk(2));   # [[1, 2], [3, 4], [5]]

let parts = nums.partition(func(int x): bool { return x % 2 == 0; });
io.println(parts[0]);  # [2, 4]
io.println(parts[1]);  # [1, 3, 5]

list<string> letters = ["a", "b", "c"];
io.println(nums.slice(0, 3).zip(letters)); # [[1, a], [2, b], [3, c]]
```

---

## Built-In Dict Methods

Dictionaries are mutable, key-value stores.  Keys must be primitive values
(string, int, decimal, bool).

### Inspection

| Method | Returns | Description |
|--------|---------|-------------|
| `length()` | `int` | Number of entries |
| `isEmpty()` | `bool` | `true` when the dict has no entries |
| `hasKey(key)` | `bool` | `true` when `key` exists |
| `contains(key)` | `bool` | Alias for `hasKey` |
| `keys()` | `list<K>` | All keys as a list |
| `values()` | `list<V>` | All values as a list |
| `items()` | `list<list<any>>` | All entries as `[key, value]` pairs |

```gb
import io;

dict<string, int> d = {"a": 1, "b": 2, "c": 3};
io.println(d.length());      # 3
io.println(d.isEmpty());     # false
io.println(d.hasKey("a"));   # true
io.println(d.hasKey("z"));   # false
io.println(d.keys());        # [a, b, c]
io.println(d.values());      # [1, 2, 3]
io.println(d.items());       # [[a, 1], [b, 2], [c, 3]]
```

### Mutation

| Method | Returns | Description |
|--------|---------|-------------|
| `set(key, value)` | `null` | Insert or update an entry |
| `get(key)` | `V\|null` | Retrieve value by key, or `null` if absent |
| `delete(key)` | `null` | Remove an entry in place; no-op if key is absent |
| `clear()` | `null` | Empty the dict in place |

All four mutate the receiver; aliases of the same dict see the change. On
a frozen dict every mutation raises `ImmutableError`.

```gb
import io;

dict<string, int> d = {"x": 10};
d.set("y", 20);
io.println(d.get("y"));  # 20
d.delete("x");
io.println(d.hasKey("x")); # false
d.clear();
io.println(d.length()); # 0
```

### Combining

`merge(other)` returns a **new** dict containing all entries from both dicts.
When a key exists in both, the value from `other` wins.

```gb
import io;

dict<string, int> a = {"x": 1, "y": 2};
dict<string, int> b = {"y": 99, "z": 3};
let c = a.merge(b);
io.println(c["x"]);  # 1
io.println(c["y"]);  # 99  (b wins)
io.println(c["z"]);  # 3
```

---

## Built-In Set Methods

Sets are unordered collections of unique values.  Like dicts, they use value
equality for membership.

### Inspection

| Method | Returns | Description |
|--------|---------|-------------|
| `length()` | `int` | Number of elements |
| `isEmpty()` | `bool` | `true` when the set has no elements |
| `contains(value)` | `bool` | `true` when `value` is a member |

```gb
import io;

set<string> s = {"apple", "banana", "cherry"};
io.println(s.length());           # 3
io.println(s.contains("apple"));  # true
io.println(s.contains("grape"));  # false
```

### Mutation

| Method | Returns | Description |
|--------|---------|-------------|
| `add(value)` | `set<T>` | New set with `value` included |
| `remove(value)` | `set<T>` | New set with `value` excluded |

```gb
import io;

set<int> s = {1, 2, 3};
let s2 = s.add(4);
io.println(s2.contains(4));  # true
let s3 = s2.remove(2);
io.println(s3.contains(2));  # false
```

### Set Algebra

| Method | Returns | Description |
|--------|---------|-------------|
| `union(other)` | `set<T>` | Elements in either set |
| `intersection(other)` | `set<T>` | Elements in both sets |
| `difference(other)` | `set<T>` | Elements in this set but not in `other` |
| `toList()` | `list<T>` | Elements as a list (order not guaranteed) |

```gb
import io;

set<int> a = {1, 2, 3, 4};
set<int> b = {3, 4, 5, 6};

io.println(a.union(b));        # {1, 2, 3, 4, 5, 6}
io.println(a.intersection(b)); # {3, 4}
io.println(a.difference(b));   # {1, 2}
io.println(b.difference(a));   # {5, 6}

let members = a.union(b).toList();
```

### Conversion between list and set

`list as set<T>` and `set as list<T>` are explicit casts. The list-to-set
direction deduplicates (first occurrence wins); the set-to-list direction
materializes the elements in unspecified order (sets are unordered by design,
so sort if you need a deterministic result).

```gb
list<int> xs = [1, 1, 2, 3, 3, 2];
let unique = xs as set<int>;        # {1, 2, 3}
io.println(unique.length);          # 3

set<string> s = {"apple", "banana", "cherry"};
let names = s as list<string>;      # length 3; order unspecified
```

These casts mirror `set.toList()` going the other way, and de-duplicating a
list previously required `let s = {}; for (x in xs) s = s.add(x);` or
`xs.uniqueBy(x => x)`. *New in 1.0.2.*

---

## `collections` Module

Import `collections` for higher-level algorithms over any collection type.

Size and membership:

- `length(value)`, `isEmpty(value)`, `contains(value, needle)`

Transformations:

- `map`, `filter`, `reduce`
- `reverse`, `sort`, `sorted`, `join`
- `flatten`, `unique`, `groupBy`, `indexBy`
- `chunk`, `partition`

Searching:

- `find`, `findLast`, `any`, `all`, `containsBy`
- `binarySearch`, `lowerBound`, `upperBound`

Ranking and statistics:

- `minBy`, `maxBy`, `sortBy`, `topBy`, `topK`, `bottomK`
- `sumBy`, `averageBy`, `frequencies`, `mode`

Set-style helpers:

- `difference`, `intersection`
- `differenceBy`, `intersectionBy`
- `zip`, `zipWith`

Graph and tree algorithms:

- `bfs(graph, start)` - breadth-first traversal; returns visited nodes in BFS order
- `dfs(graph, start)` - depth-first traversal; returns visited nodes in DFS order
- `topologicalSort(graph)` - Kahn's algorithm; returns nodes in topological order or errors on cycle
- `shortestPath(graph, start, end)` - unweighted BFS shortest path; returns node list or `null`

`graph` is a `dict` mapping each node to its list of neighbors (adjacency list).

Lazy helpers:

- `range(start, end, step)`
- `take(iterable, count)`
- `lazyMap`, `lazyFilter`

```gb
import collections;
import io;

let users = [
    {"name": "Ada", "score": 10},
    {"name": "Grace", "score": 12}
];

let top = collections.maxBy(users, func(dict<string, any> user): int {
    return user["score"];
});

io.println(top["name"]);  # Grace
```

Range iteration:

```gb
for (i in collections.range(0, 5, 1)) {
    io.println(i);
}
```

## `streams` Module (1.0.6)

`streams.Stream` wraps any iterable (list, set, generator, range, or
another class with the 1.0.6 iterator protocol) in a fluent,
lazy-by-default pipeline. Intermediate ops return a new Stream that
pulls values on demand; terminal ops drive the pipeline and produce
a value.

| Intermediate (returns Stream) | Effect |
| --- | --- |
| `map(fn)` | Apply `fn` to every value |
| `filter(fn)` | Keep values where `fn(x)` is true |
| `take(n)` | Yield at most the first `n` values |

| Terminal (drives the pipeline) | Result |
| --- | --- |
| `toList()` | `list<any>` of all values |
| `toSet()` | `set<any>` of all values (duplicates collapse) |
| `count()` | Number of values |
| `first()` | First value or `null` when empty |
| `reduce(initial, fn)` | Left fold using `fn(acc, value)` |
| `forEach(fn)` | Invoke `fn(value)` for each value |
| `anyMatch(fn)` | True when any value satisfies `fn` |
| `allMatch(fn)` | True when every value satisfies `fn` (vacuously true for empty) |

`streams.of(source)` builds a Stream. Streams implement `__iter()` so
they slot into `for-in` loops and `iterable<T>` parameters without
materialising.

```gb
import streams;
import collections;
import io;

let sumOfSquares = streams.of([1, 2, 3, 4, 5])
    .map(func(int x): int { return x * x; })
    .reduce(0, func(int a, int b): int { return a + b; });
io.println(sumOfSquares);  # 55

let firstThreeMultiplesOf7 = streams.of(collections.range(1, 100))
    .filter(func(int x): bool { return x % 7 == 0; })
    .take(3)
    .toList();
io.println(firstThreeMultiplesOf7);  # [7, 14, 21]
```

Use a C-style `for` loop when you need the counter as an index:

```gb
let users = [
    {"name": "Ada", "score": 10},
    {"name": "Grace", "score": 12}
];

for (let int i = 0; i < users.length(); i++) {
    io.println("${i}: ${users[i]["name"]}");
}
```
