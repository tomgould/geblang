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

Lists are mutable, ordered sequences.  Methods that return a new list leave the
original unchanged; mutation methods (`push`, `pop`, `set`, `insert`,
`removeAt`) modify the list in place.

Generic annotations such as `list<int>` are checked when values cross typed
declaration and function/method call boundaries. They are not a permanent
runtime lock on a mutable list value; mutation methods still operate on the
underlying collection. Validate before mutation when accepting dynamic data, and
rely on the next typed boundary to catch mismatches when passing values onward.

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

| Method | Returns | Description |
|--------|---------|-------------|
| `push(value)` | `null` | Append `value` to the end |
| `pop()` | `T\|null` | Remove and return the last element |
| `insert(index, value)` | `null` | Insert `value` before `index` |
| `removeAt(index)` | `null` | Remove element at `index` |
| `set(index, value)` | `null` | Replace element at `index` |

```gb
import io;

list<string> words = ["a", "b", "c"];
words.push("d");
io.println(words);          # [a, b, c, d]
io.println(words.pop());    # d
words.insert(1, "x");
io.println(words);          # [a, x, b, c]
words.removeAt(1);
io.println(words);          # [a, b, c]
words.set(0, "z");
io.println(words);          # [z, b, c]
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
| `delete(key)` | `null` | Remove an entry; no-op if key is absent |

```gb
import io;

dict<string, int> d = {"x": 10};
d.set("y", 20);
io.println(d.get("y"));  # 20
d.delete("x");
io.println(d.hasKey("x")); # false
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
