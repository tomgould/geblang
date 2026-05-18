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
