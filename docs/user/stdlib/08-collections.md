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

A third bound is the step: `x[start:end:step]`. A negative step walks
backwards, so `x[::-1]` reverses (on both lists and strings):

```gb
list<int> a = [1, 2, 3, 4, 5];
io.println(a[::2]);    # [1, 3, 5]   (every second element)
io.println(a[::-1]);   # [5, 4, 3, 2, 1]   (reversed)
io.println(a[1:5:2]);  # [2, 4]
io.println("geblang"[::-1]); # gnalbeg
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
| `removeAt(index)` | `list<T>` | New list with the element at `index` removed |
| `remove(value)` | `list<T>` | New list with the first occurrence of `value` removed. Returns an equivalent list if `value` is absent |
| `copy()` | `list<T>` | New list with the same elements (shallow copy) |
| `reverse()` | `list<T>` | New list with elements in reverse order |
| `reversed()` | `list<T>` | Alias for `reverse` |

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
| `reverse()` | `list<T>` | New list with elements in reverse order |
| `reversed()` | `list<T>` | Alias for `reverse` |
| `sort([callback])` | `list<T>` | Sorted copy; optional `(a,b)->bool` less-than or `(a,b)->int` comparator |
| `sorted([callback])` | `list<T>` | Alias for `sort` |
| `sortBy(selector[, descending])` | `list<T>` | Sorted copy by selector key; `true` for descending |
| `binarySearch(value)` | `int` | Index of `value` in a sorted list, or -1 |
| `binarySearchBy(selector, key)` | `int` | Index whose selector key equals `key` in a list sorted by it, or -1 |
| `concat(other)` | `list<T>` | New list with `other` appended |
| `join(sep)` | `string` | Elements joined into a string with separator `sep` |
| `flatten()` | `list<any>` | Recursively flatten nested lists |
| `unique()` | `list<T>` | New list with duplicate values removed |

```gb
import io;

list<int> a = [3, 1, 4, 1, 5, 9, 2, 6];
io.println(a.reverse());        # [6, 2, 9, 5, 1, 4, 1, 3]
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

`sort` and `sorted` accept an optional callback. It may be either a
**less-than predicate** `func(a, b): bool` (returns `true` when `a` sorts
before `b`) or a **three-way comparator** `func(a, b): int` (negative when
`a` sorts before `b`, like `string.compare`). Without a callback, the natural
order is used (numeric ascending, lexicographic for strings). The sort is
stable.

```gb
import io;

list<int> nums = [3, 1, 4, 1, 5];

# Less-than predicate, descending: "a comes before b when a is larger".
io.println(nums.sorted(func(int a, int b): bool { return a > b; }));  # [5, 4, 3, 1, 1]

# Three-way comparator: pass string.compare straight in.
list<string> names = ["banana", "apple", "cherry"];
io.println(names.sort(string.compare));   # [apple, banana, cherry]

# Equivalent descending: sort ascending, then reverse (or names[::-1]).
io.println(nums.sorted().reverse());      # [5, 4, 3, 1, 1]
```

For key-driven sorting prefer `sortBy(selector)`: it computes each key once
per element rather than on every comparison. Pass `true` as a second argument
to sort descending.

```gb
import io;

list<dict<string, any>> users = [
    {"name": "Grace", "age": 85},
    {"name": "Ada", "age": 36}
];
let byAge = users.sortBy(func(dict<string, any> u): any { return u["age"]; });
io.println(byAge[0]["name"]);                 # Ada

let oldest = users.sortBy(func(dict<string, any> u): any { return u["age"]; }, true);
io.println(oldest[0]["name"]);                # Grace
```

To search a sorted list, `binarySearch(value)` returns the index of `value`
(or -1), and `binarySearchBy(selector, targetKey)` does the same for a list
sorted by a key. `lowerBound(value)` / `upperBound(value)` give insertion
points.

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
| `entries()` | `list<list<any>>` | Alias for `items` |
| `copy()` | `dict<K, V>` | New dict with the same entries (shallow copy) |

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
| `insert(key, value)` | `null` | Alias for `set` |
| `get(key)` | `V\|null` | Retrieve value by key, or `null` if absent |
| `delete(key)` | `null` | Remove an entry in place; no-op if key is absent |
| `remove(key)` | `null` | Alias for `delete` |
| `clear()` | `null` | Empty the dict in place |

All mutators write to the receiver in place; aliases of the same dict see
the change. On a frozen dict every mutation raises `ImmutableError`.

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
| `copy()` | `set<T>` | New set with the same elements (shallow copy) |
| `toList()` | `list<T>` | Members as a list |

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

## `deque.Deque<T>` (1.6.0)

A double-ended queue with amortised O(1) push / pop at both ends.
Backed by a ring buffer that doubles in capacity when full.

```gb
import deque;

let d = deque.Deque<int>();
d.pushBack(1); d.pushBack(2); d.pushBack(3);
d.pushFront(0);
io.println(d.popFront());   # 0
io.println(d.popBack());    # 3
io.println(d.toList());     # [1, 2]
```

### Operations

| Method | Returns | Complexity | Description |
|--------|---------|------------|-------------|
| `pushFront(value)` | `void` | Amortised O(1) | Insert at the front. |
| `pushBack(value)` | `void` | Amortised O(1) | Insert at the back. |
| `popFront()` | `T` | O(1) | Remove and return the front element. Throws `ValueError` on empty. |
| `popBack()` | `T` | O(1) | Remove and return the back element. Throws `ValueError` on empty. |
| `peekFront()` | `T` | O(1) | Return the front element without removing it. Throws `ValueError` on empty. |
| `peekBack()` | `T` | O(1) | Return the back element without removing it. Throws `ValueError` on empty. |
| `get(i)` | `T` | O(1) | Element at logical position `i` (0 = front; negative counts from the back). Throws `ValueError` on out-of-range. |
| `length()` | `int` | O(1) | Number of elements currently in the deque. |
| `isEmpty()` | `bool` | O(1) | True when the deque holds no elements. |
| `clear()` | `void` | O(cap) | Discard every element. |
| `toList()` | `list<T>` | O(n) | Snapshot in front-to-back order. |

The deque implements the iterator protocol so it slots into
`for` loops directly:

```gb
let d = deque.Deque<string>();
d.pushBack("a"); d.pushBack("b"); d.pushBack("c");
for (var s in d) {
    io.println(s);   # a, b, c
}
```

Mutating the deque during iteration is not supported (the cursor
is shared with the deque); snapshot with `toList()` first if you
need that pattern.

### Common patterns

**FIFO queue**: `pushBack` to enqueue, `popFront` to dequeue.
**LIFO stack**: `pushBack` to push, `popBack` to pop.
**Sliding window**: `pushBack` new elements, `popFront` to drop
the oldest when the window is full.

```gb
import deque;
import io;

# Last-N rolling buffer
let recent = deque.Deque<int>();
let windowSize = 3;
for (int v in [10, 20, 30, 40, 50]) {
    recent.pushBack(v);
    if (recent.length() > windowSize) { recent.popFront(); }
}
io.println(recent.toList());   # [30, 40, 50]
```

## `lrucache.LruCache<K, V>` (1.6.0)

A least-recently-used cache backed by a doubly-linked list and a
dict for O(1) `get`, `put`, and eviction. Optional time-to-live
expires entries lazily on access.

```gb
import lrucache;

let c = lrucache.LruCache<string, int>(100);   # capacity = 100
c.put("a", 1);
c.put("b", 2);
io.println(c.get("a"));    # 1 - now most recent

let withTtl = lrucache.LruCache<string, int>(100, 60);   # 60s expiry
```

`get(key)` returns `null` on a miss (or on a hit whose entry has
expired). Pair with `has(key)` when you need to distinguish a
stored-null value from an absent key, but note `has` does NOT
bump the entry to most-recent.

### Operations

| Method | Returns | Complexity | Description |
|--------|---------|------------|-------------|
| `get(key)` | `?V` | O(1) | Returns the value and bumps to most-recent. Null on miss or expired. |
| `put(key, value)` | `void` | O(1) | Stores value. Updates and bumps if present; evicts the LRU when at capacity. |
| `delete(key)` | `bool` | O(1) | Removes the entry. True if a removal happened, false if absent. |
| `has(key)` | `bool` | O(1) | Membership check. Does NOT bump LRU order. Drops expired entries lazily. |
| `length()` | `int` | O(1) | Number of entries currently held. |
| `capacity()` | `int` | O(1) | Configured maximum entry count. |
| `isEmpty()` | `bool` | O(1) | True when the cache is empty. |
| `clear()` | `void` | O(n) | Discards every entry. Stats counters are preserved. |
| `keys()` | `list<K>` | O(n) | MRU-to-LRU order. Does not affect access ordering. |
| `values()` | `list<V>` | O(n) | MRU-to-LRU order. Does not affect access ordering. |
| `stats()` | `dict<string, int>` | O(1) | Lifetime counters: `{hits, misses, evictions, expirations}`. |

### TTL behaviour

The `ttlSeconds` constructor argument sets a uniform expiry for
every entry. Expiry is checked lazily: a `get` on an expired
entry counts as a miss, increments the `expirations` counter,
and drops the entry from the cache. A `has` on an expired entry
returns `false` and likewise drops it. No background scan walks
the cache; entries only leave on access or on capacity-eviction.

### Common patterns

**Memoising an expensive call**:

```gb
import lrucache;

let memo = lrucache.LruCache<string, int>(1000);

func expensive(string input): int {
    let cached = memo.get(input);
    if (cached != null) { return cached; }
    let result = doExpensiveWork(input);
    memo.put(input, result);
    return result;
}
```

**Tuning capacity from stats**:

```gb
let s = memo.stats();
let ratio = s["hits"] as float / ((s["hits"] + s["misses"]) as float);
log.info("cache hit ratio: " + (ratio as string));
```

## `priorityq.PriorityQueue<T>` (1.6.0)

A binary min-heap-backed priority queue. The smallest-by-order
element is always at the head; `pop()` and `peek()` return it.

```gb
import priorityq;

let q = priorityq.PriorityQueue<int>();
q.push(3); q.push(1); q.push(2);
io.println(q.pop());   # 1
io.println(q.pop());   # 2
io.println(q.pop());   # 3
```

Without a comparator, elements are ordered by Geblang's `<`
operator (works for `int`, `float`, `decimal`, `string`). For
custom types or reverse order, pass a comparator
`func(T, T): int` that returns `< 0`, `0`, or `> 0`:

```gb
let highestFirst = priorityq.PriorityQueue<int>(
    func(int a, int b): int { return b - a; }
);
highestFirst.push(1); highestFirst.push(7); highestFirst.push(3);
io.println(highestFirst.pop());   # 7

class Job {
    string name;
    int priority;
    func Job(string name, int priority) {
        this.name = name;
        this.priority = priority;
    }
}

let byPriority = priorityq.PriorityQueue<Job>(
    func(Job a, Job b): int { return a.priority - b.priority; }
);
```

### Operations

| Method | Returns | Complexity | Description |
|--------|---------|------------|-------------|
| `push(value)` | `void` | O(log n) | Inserts value and re-heapifies. |
| `pop()` | `T` | O(log n) | Removes and returns the smallest element. Throws `ValueError` on empty. |
| `peek()` | `T` | O(1) | Returns the smallest element without removing it. Throws `ValueError` on empty. |
| `length()` | `int` | O(1) | Number of elements currently in the queue. |
| `isEmpty()` | `bool` | O(1) | True when the queue holds no elements. |
| `pushPop(value)` | `T` | O(log n) | Single-pass push-then-pop. Cheaper than separate calls when the inserted value would immediately become the new head; common in top-K and merge-K patterns. |
| `drain()` | `list<T>` | O(n log n) | Pops every element in priority order and returns them as a sorted list, leaving the queue empty. |
| `clear()` | `void` | O(1) | Discards every element without yielding them. |

### Common patterns

**Top-K largest values**: keep a min-heap of size K. For each
incoming value, `pushPop` it; when the queue size exceeds K, the
result is the new minimum being displaced. Total cost
O(n log K) instead of O(n log n).

```gb
import priorityq;
import io;

func topK<T>(list<T> values, int k): list<T> {
    let q = priorityq.PriorityQueue<T>();
    for (var v in values) {
        if (q.length() < k) {
            q.push(v);
        } else if (v > q.peek()) {
            q.pushPop(v);
        }
    }
    return q.drain();
}

io.println(topK([5, 2, 9, 1, 7, 3, 8, 4, 6], 3));   # [7, 8, 9]
```

**Heap-sort** is a one-liner: push everything, then `drain()`.
