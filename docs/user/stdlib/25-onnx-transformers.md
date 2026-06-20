# Local Models (`transformers`, `onnx`)

> Experimental (1.24.0). Local, offline AI/ML: tokenization today; ONNX model
> inference and sentence-transformer embedding are landing in this chapter as
> they ship. The surface may change.

These modules run models on the local machine with no network call, complementing
the API-backed [`llm`](23-llm.md) module. Tokenization is pure Geblang-native and
needs nothing external; model inference (coming) loads the ONNX Runtime shared
library and is gated behind the `--allow-onnx` launch flag.

## `transformers.tokenize`

`transformers.tokenize(tokenizerJson, texts, opts = {})` runs WordPiece
tokenization, the input stage for BERT-family sentence encoders (all-MiniLM,
bge, e5). `tokenizerJson` is the contents of a HuggingFace `tokenizer.json`
(read it yourself, e.g. with `io.readText`), and `texts` is a list of strings.

It returns a dict of `input_ids`, `attention_mask`, and `token_type_ids`, each a
list of integer rows aligned to `texts` and padded to a common width:

```gb
import transformers;
import io;

let tokenizerJson = io.readText("./model/tokenizer.json");
let batch = transformers.tokenize(tokenizerJson, ["Hello world", "Another sentence"]);

batch["input_ids"];        # list<list<int>>, e.g. [[101, 7592, 2088, 102, 0], ...]
batch["attention_mask"];   # 1 for real tokens, 0 for padding
batch["token_type_ids"];   # all 0 for single-sequence input
```

The tokenizer honours the `tokenizer.json` `normalizer` (lowercasing and accent
stripping for BertNormalizer), splits on whitespace and punctuation, performs
greedy longest-match WordPiece with `##` continuations, and wraps each sequence
with the `[CLS]` / `[SEP]` special tokens.

| `opts` key | Default | Meaning |
|------------|---------|---------|
| `maxLength` | 512 | Truncate each sequence to this many tokens (including specials). |
| `addSpecialTokens` | `true` | Wrap with `[CLS]` / `[SEP]`. |
| `padding` | batch | `"max_length"` pads every row to `maxLength`; otherwise rows pad to the longest row in the batch. |

Only WordPiece tokenizers are supported; BPE and SentencePiece are out of scope.
A parsed `tokenizer.json` is cached by content, so repeated calls with the same
tokenizer do not re-parse it.

## `onnx.session`

`onnx.session(modelPath, opts = {})` loads an ONNX model for local inference. It
requires the `--allow-onnx` launch flag (inference loads a native shared library)
and the ONNX Runtime library, located via `opts.libPath`, the
`$GEBLANG_ONNXRUNTIME` environment variable, or the system loader path. Without
the flag the call throws a `PermissionError`.

The returned `Session`:

| Method | Description |
|--------|-------------|
| `run(inputs)` | Runs the model. `inputs` maps each tensor name to an **int64** ndarray; returns a dict mapping each output name to a **float64** ndarray. |
| `inputNames()` | The model's input tensor names. |
| `outputNames()` | The model's output tensor names. |
| `close()` | Releases the session and ONNX Runtime resources. |

`opts`: `libPath` (path to `libonnxruntime.so`), `intraOpThreads` (int).

```gb
import onnx;
import transformers;
import ndarray;
import io;

let dir = "./models/all-MiniLM-L6-v2";
let tok = transformers.tokenize(io.readText(dir + "/tokenizer.json"), ["hello world"]);

let sess = onnx.session(dir + "/model.onnx");          # run with: geblang --allow-onnx
let out = sess.run({
    "input_ids":      ndarray.array(tok["input_ids"]),
    "attention_mask": ndarray.array(tok["attention_mask"]),
    "token_type_ids": ndarray.array(tok["token_type_ids"])
});
io.println(out["last_hidden_state"].shape());          # [1, 4, 384]
sess.close();
```

Inputs must be int64 ndarrays (the model's token ids/masks); outputs come back
as float64 ndarrays, so they feed straight into `ndarray` pooling and `vecmath`.
The binding pins the ONNX Runtime C API to a fixed version; an incompatible
runtime fails loudly at load.

## Sentence embeddings (end-to-end)

`transformers.pool(hidden, attentionMask, opts = {})` reduces a model's
`[batch, seq, dim]` hidden state and its `[batch, seq]` attention mask to
`[batch, dim]` sentence embeddings. `opts.pooling` is `"mean"` (default, weighted
by the mask), `"cls"`, or `"max"`; `opts.normalize` (default true) L2-normalizes
each row. This is the last step of a sentence-transformer encoder.

Putting tokenize, `onnx.session`, and `pool` together gives fully local, offline
sentence embeddings (no API call):

```gb
import onnx;
import transformers;
import ndarray;
import vecmath;
import io;

let dir   = "./models/all-MiniLM-L6-v2";
let tjson = io.readText(dir + "/tokenizer.json");
let sess  = onnx.session(dir + "/model.onnx");        # geblang --allow-onnx

func embed(list<string> texts): list<any> {
    let tok = transformers.tokenize(tjson, texts);
    let out = sess.run({
        "input_ids":      ndarray.array(tok["input_ids"]),
        "attention_mask": ndarray.array(tok["attention_mask"]),
        "token_type_ids": ndarray.array(tok["token_type_ids"])
    });
    return transformers.pool(out["last_hidden_state"],
                             ndarray.array(tok["attention_mask"])).toList() as list<any>;
}

let e = embed(["a dog runs in the park", "a puppy plays outside", "quantum physics"]);
vecmath.score("cosine", e[0] as list<any>, e[1] as list<any>);   # ~0.45 (similar)
vecmath.score("cosine", e[0] as list<any>, e[2] as list<any>);   # ~-0.09 (unrelated)
sess.close();
```

The resulting vectors plug directly into the [`vectorstore` and `rag`](24-vectorstore-rag.md)
modules and `vecmath.semanticSearch`, giving a fully on-device retrieval stack.
