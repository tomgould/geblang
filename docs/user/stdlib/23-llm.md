# LLM (`llm`)

A provider-agnostic LLM client. One Geblang interface for chat
completions, text embeddings, image analysis, and image generation
across OpenAI, Anthropic, and AWS Bedrock.

```gb
import llm;

let c = llm.client({
    "provider": "anthropic",
    "apiKey":   sys.getenv("ANTHROPIC_API_KEY") as string
});

let resp = c.chat([
    {"role": "user", "content": "Summarise this in one sentence: " + article}
], {"model": "claude-opus-4-8", "maxTokens": 256});

io.println(resp["content"]);
io.println("tokens: " + ((resp["usage"] as dict<string, any>)["totalTokens"] as string));
```

## Client interface

Every provider returns a value satisfying the `llm.Client`
interface:

| Method | Description |
|--------|-------------|
| `chat(messages, opts)` | Chat completion. Returns `{content, model, stopReason, usage}`. |
| `embed(text, opts)` | Single-string text embedding. Returns `{vector, model, usage}`. |
| `analyzeImage(image, prompt, opts)` | Multimodal vision call. `image` is the raw bytes; returns the same shape as `chat`. |
| `generateImage(prompt, opts)` | Image generation. Returns `{images, model}` where `images` is a list of `{bytes, format}`. |

`usage` is a dict with `inputTokens`, `outputTokens`,
`totalTokens` (zeros when the provider does not report a number).

`opts.model` is required for every call and is a free-form string.
The library does not validate model names locally; pass whatever
the provider exposes (e.g. `"gpt-5"`, `"claude-opus-4-8"`,
`"anthropic.claude-opus-4-8-v1:0"` for Bedrock).

Other common opts: `maxTokens` (int), `temperature` (decimal /
float), `topP` (decimal / float), `system` (string for system
prompt).

## Picking a provider

```gb
let openai    = llm.client({"provider": "openai",    "apiKey": "..."});
let anthropic = llm.client({"provider": "anthropic", "apiKey": "..."});
let bedrock   = llm.client({"provider": "bedrock",
    "region":    "us-east-1",
    "accessKey": "...",
    "secretKey": "..."});
```

Provider-specific options:

| Provider | Required | Optional |
|----------|----------|----------|
| `openai` | `apiKey` | `endpoint` (default `https://api.openai.com`), `organization` |
| `anthropic` | `apiKey` | `endpoint` (default `https://api.anthropic.com`), `apiVersion` (default `2023-06-01`) |
| `bedrock` | `region`, `accessKey`, `secretKey` | `endpoint` (default regional Bedrock Runtime host) |

## Capability matrix

Not every provider supports every operation. Calls into a
combination that is not supported throw a `RuntimeError` naming
the missing operation, so failures are immediate.

| Operation | OpenAI | Anthropic | Bedrock |
|-----------|--------|-----------|---------|
| `chat` | yes | yes | yes (Claude models via Anthropic Messages schema) |
| `embed` | yes | throws | yes (Titan / Cohere model families) |
| `analyzeImage` | yes (gpt-4o, etc.) | yes (claude vision) | yes (Claude models) |
| `generateImage` | yes (dall-e-*) | throws | yes (Titan / Stability model families) |

Bedrock dispatches by model id prefix:

- `chat` / `analyzeImage`: routed through the Anthropic Messages
  schema, covering every `anthropic.claude-*` model on Bedrock.
- `embed`: `amazon.titan-embed-*` and `cohere.embed-*` are folded
  into the common shape. The Cohere call accepts an extra
  `inputType` opt (default `"search_document"`); pass
  `"search_query"` when embedding a user query.
- `generateImage`: `amazon.titan-image-*` and `stability.*` are
  folded into the common shape. Common opts: `width`, `height`,
  `n` (Titan), `steps`, `cfgScale`, `seed` (Stability).

Calls with an unrecognised Bedrock model id throw a clear
`RuntimeError` directing the caller to the lower-level
`bedrock.invoke(model, payload)` escape hatch for model families
the v1 common shape does not cover (e.g. Llama, Mistral).

## Image analysis

`analyzeImage` takes bytes plus a prompt. The provider request is
constructed with the image inline as base64; pass the actual image
bytes (typically loaded via `io.readBytes` or fetched with
`http.get`).

```gb
let png = io.readBytes("./screenshot.png");
let resp = c.analyzeImage(png, "What error message is visible?", {
    "model":     "claude-opus-4-8",
    "mimeType":  "image/png",   /* optional; default image/png */
    "maxTokens": 512
});
io.println(resp["content"]);
```

## Image generation

```gb
let resp = c.generateImage("a photorealistic red square on a black background", {
    "model": "dall-e-3",
    "size":  "1024x1024",
    "n":     1
});
let images = resp["images"] as list<any>;
for (img in images) {
    let item = img as dict<string, any>;
    io.writeBytes("./out.png", item["bytes"] as bytes);
}
```

OpenAI is the only provider that supports image generation in
v1; calls on the other clients throw.

## Errors

A non-2xx HTTP response from the upstream API raises a
`RuntimeError` carrying the operation, status code, and response
body. Catch as a normal Geblang error:

```gb
try {
    let resp = c.chat(messages, {"model": "claude-opus-4-8"});
} catch (RuntimeError e) {
    /* rate limit, invalid key, validation error, etc. */
    io.println("LLM call failed: " + e.message);
}
```
