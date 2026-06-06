# Vector Search and RAG (`vectorstore`, `rag`)

Two source modules for working with embeddings: `vectorstore` stores dense
vectors and ranks them by similarity, and `rag` layers chunking, indexing,
retrieval, and prompt-context assembly on top for retrieval-augmented
generation. Both are pure Geblang and run on the bytecode VM.

They build on the embedding vectors produced by the [`llm`](llm.html) module
(`client.embed(text, opts)`), but `vectorstore` is independent of any provider
and `rag` accepts any embedder you supply.

## `vectorstore`

A vector store keeps `(id, vector, metadata)` records and answers
nearest-neighbour queries. Two implementations share one `VectorStore`
interface:

| Store | Use |
|-------|-----|
| `MemoryVectorStore(metric = "cosine")` | Brute-force in-memory search; mutex-guarded so one shared store is safe under concurrent requests. Ideal up to ~1e4-1e5 vectors. |
| `SqliteVectorStore(conn, table = "vectors", metric = "cosine")` | Persistent store backed by a `db` Connection. Vectors are stored as little-endian float32 BLOBs and metadata as JSON; the table is created if absent and `add` upserts by id. |

Metric is `"cosine"` (default), `"dot"`, or `"euclidean"`; all scores follow
"higher = closer". Vectors are `list<any>` so they accept the decimal numbers
that come back from JSON-parsed embeddings as well as float literals. Vectors are
stored packed as float32 and ranked by the native `vecmath` kernel (below), so
search is well off the interpreted path.

```gb
import vectorstore;

let store = vectorstore.MemoryVectorStore();
store.add("cats", [0.1, 0.2, 0.9], {"text": "about cats"});
store.add("cars", [0.9, 0.1, 0.1], {"text": "about cars"});

let hits = store.search([0.1, 0.2, 0.8], 1);
io.println(hits[0].record.metadata["text"]);   // about cats
io.println(hits[0].score);                      // similarity, higher = closer
```

### `VectorStore` interface

| Method | Description |
|--------|-------------|
| `add(id, vector, metadata)` | Adds or replaces a record by id. |
| `addAll(records)` | Adds or replaces a list of `VectorRecord`. |
| `get(id)` | Returns the `VectorRecord`, or `null`. |
| `delete(id)` | Removes id; returns `true` if it existed. |
| `search(query, k)` | Top `k` records by descending similarity. |
| `searchWhere(query, k, filter)` | Top `k` among records for which `filter(record)` is true. |
| `count()` | Number of stored records. |
| `clear()` | Removes everything. |

`searchWhere` filters on metadata before ranking, e.g. by tenant or source:

```gb
let hits = store.searchWhere(queryVector, 5, func(any rec): bool {
    return (rec as vectorstore.VectorRecord).metadata["lang"] == "en";
});
```

A persistent store is a drop-in replacement:

```gb
import db;
import vectorstore;

let conn  = db.connect("sqlite", "vectors.db");
let store = vectorstore.SqliteVectorStore(conn);
store.add("doc-1", embedding, {"text": "..."});
let hits = store.search(queryVector, 5);
```

The exported helper `vectorstore.score(metric, a, b)` computes a single
similarity score between two vectors.

## `rag`

`rag` turns documents into retrievable, prompt-ready context. It is built on a
small `Embedder` interface so it is not tied to any one provider:

```gb
interface Embedder { func embed(string text): list<any>; }
```

`LlmEmbedder` adapts an `llm` client to that interface; the options dict carries
the embedding model.

```gb
import db;
import llm;
import rag;
import vectorstore;

let store    = vectorstore.MemoryVectorStore();
let embedder = rag.LlmEmbedder(
    llm.client({"provider": "openai", "apiKey": key}),
    {"model": "text-embedding-3-small"}
);

rag.index(store, embedder, "handbook", longText, {"source": "handbook"}, {});

let hits   = rag.retrieve(store, embedder, "how do I reset my password?", 4);
let prompt = "Answer using only this context:\n" + rag.context(hits, {})
           + "\n\nQuestion: how do I reset my password?";
let answer = llm.client({"provider": "openai", "apiKey": key})
    .chat([{"role": "user", "content": prompt}], {"model": "gpt-4o-mini"})["content"];
```

### Functions

| Function | Description |
|----------|-------------|
| `chunk(text, opts)` | Splits text into overlapping chunks; returns `list<string>`. |
| `index(store, embedder, docId, text, metadata, opts)` | Chunks, embeds, and stores a document. Returns the number of chunks. |
| `retrieve(store, embedder, query, k)` | Embeds the query and returns the top `k` `SearchHit`s. |
| `context(hits, opts)` | Assembles hits into a prompt-ready block. |

`chunk` options:

| Key | Default | Meaning |
|-----|---------|---------|
| `by` | `"words"` | `"words"`, `"chars"`, or `"paragraphs"`. |
| `size` | 200 words / 1000 chars | Window size for the chosen unit. |
| `overlap` | 40 words / 200 chars | Overlap between consecutive windows (ignored for paragraphs). |

`index` stores each chunk under id `"<docId>#<i>"` and attaches the caller's
metadata plus `text`, `docId`, and `chunk` (the index), so retrieved hits carry
their own source text.

`context` options: `withSources` (default `true`) prefixes each chunk with
`[n] (docId): `; `separator` (default a blank line) joins the chunks. Pass
`{"withSources": false}` for the bare chunk text.

### Testing without a network

`rag` depends only on the `Embedder` interface, so tests can supply a
deterministic stub embedder and avoid any API calls:

```gb
class StubEmbedder implements rag.Embedder {
    func embed(string text): list<any> {
        let v = [];
        for (k in ["cat", "dog", "car"]) {
            if (text.lower().contains(k as string)) { v = v.push(1.0f); }
            else { v = v.push(0.0f); }
        }
        return v;
    }
}
```

## `vecmath`

The float32 similarity kernel underpinning the stores. It scores in native code
rather than the interpreted loop, and accepts vectors as either a list of
numbers or a packed little-endian float32 BLOB (the stored form).

| Function | Description |
|----------|-------------|
| `score(metric, a, b)` | Similarity (higher = closer) between two vectors for `"cosine"` / `"dot"` / `"euclidean"`. |
| `topK(vectors, query, k, metric)` | Ranks `vectors` (a list of lists or float32 blobs) against `query`, returns up to `k` `{index, score}` dicts in descending order. |

```gb
import vecmath;

vecmath.score("cosine", [1.0, 0.0], [1.0, 0.0]);   // 1.0
let hits = vecmath.topK([[1.0, 0.0], [0.0, 1.0]], [1.0, 0.0], 1, "cosine");
hits[0]["index"];                                   // 0
```

`vectorstore.score(metric, a, b)` delegates to `vecmath.score`; you rarely call
`vecmath` directly unless building your own ranking.
