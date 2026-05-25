# Bytes, Encoding, And Compression

## Bytes

Import `bytes`:

- `fromString(text)`, `toString(bytes)`
- `fromHex(text)`, `toHex(bytes)`
- `fromBase64(text)`, `toBase64(bytes)`
- `concat(list<bytes>)`

```gb
import bytes;

let data = bytes.fromString("hello");
io.println(bytes.toHex(data));
io.println(bytes.toString(data));
```

## Encoding

Import `encoding`:

- `base64Encode(value)`, `base64Decode(text)`
- `base32Encode(value)`, `base32Decode(text)` - RFC 4648 standard alphabet, accepts padded or unpadded input on decode.
- `base58Encode(value)`, `base58Decode(text)` - Bitcoin/IPFS alphabet (no `0`, `O`, `I`, `l`); preserves leading zero bytes by emitting leading `1`s.
- `urlEncode(text)`, `urlDecode(text)`
- `htmlEscape(text)`, `htmlUnescape(text)`

`base32Encode` / `base58Encode` accept either a `string` or `bytes` value, so
they compose directly with random material from `secrets.randomBytes`:

```gb
import encoding;
import secrets;

let totpSecret = encoding.base32Encode(secrets.randomBytes(20));
let walletId   = encoding.base58Encode(secrets.randomBytes(16));
```

Both decoders return `bytes` so binary payloads round-trip safely.

Use this module for transport encodings and escaping, not password hashing or
cryptographic operations.

## Compression

Import `compress`:

- `gzip(value)`
- `gunzip(bytes)`

```gb
import bytes;
import compress;

let packed = compress.gzip(bytes.fromString("payload"));
io.println(bytes.toString(compress.gunzip(packed)));
```

## Archives

Import `archive` for zip and tar archive reading and writing.
The 1.4.0 API is eager: readers materialise the full entry list
in memory; writers take a list of entry dicts and return bytes.
A streaming cursor API is queued for a follow-up.

- `archive.zipRead(bytes)` and `archive.zipWrite(entries)`.
- `archive.tarRead(bytes)` and `archive.tarWrite(entries)`.
- `archive.tarGzRead(bytes)` and `archive.tarGzWrite(entries)`
  for the common `.tar.gz` / `.tgz` shape.

Each reader returns a `list<dict<string, any>>` whose dicts
carry `name` (string), `data` (bytes), `isDir` (bool), and
`size` (int).  Each writer accepts the same shape; the `data`
field may be a `string` or `bytes`. Tar writers sort entries by
name so output is deterministic.

```gb
import archive;
import bytes;

let raw = archive.zipWrite([
    {"name": "hello.txt", "data": "hello world"},
    {"name": "nested/inside.txt", "data": "nested"}
]);

let entries = archive.zipRead(raw);
for (e in entries) {
    io.println(e["name"] as string);
    io.println(bytes.toString(e["data"] as bytes));
}

let tgz = archive.tarGzWrite([
    {"name": "config.toml", "data": "key = \"value\""}
]);
let configEntries = archive.tarGzRead(tgz);
```

Reader errors (corrupt or non-archive bytes) and writer errors
(missing `name` / `data` field) throw catchable runtime
exceptions, so callers can wrap untrusted input in `try` / `catch`.
