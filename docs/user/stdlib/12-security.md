# Security

## Crypt

```gb
import crypt;
```

The `crypt` module provides hashing, message authentication, password hashing,
JWT generation and verification, and asymmetric key and certificate helpers.
All functions are pure Go - no external tools or system libraries are required.

### Hashes

All hash functions accept a `string` and return a lowercase hex-encoded `string`.

```gb
import crypt;

io.println(crypt.sha256("hello"));
# 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824

io.println(crypt.sha512("hello"));
io.println(crypt.sha3_256("hello"));
io.println(crypt.blake2b("hello"));
io.println(crypt.sha1("hello"));    # legacy - avoid for security use
io.println(crypt.md5("hello"));     # legacy - avoid for security use
```

`crypt.crc32(text)` returns an `int` (not hex) - it is a checksum, not a
cryptographic hash:

```gb
io.println(crypt.crc32("hello"));   # 907060870
```

**Choosing a hash algorithm:**

| Algorithm | Output | Use when |
|---|---|---|
| `sha256` | 64 hex chars | General-purpose signatures, content addressing |
| `sha512` | 128 hex chars | Larger security margin needed |
| `sha3_256` | 64 hex chars | Post-SHA-2 hardening, NIST standard |
| `blake2b` | 64 hex chars | High-speed hashing, file integrity |
| `sha1` | 40 hex chars | Legacy compatibility only |
| `md5` | 32 hex chars | Legacy checksums only |
| `crc32` | int | Non-cryptographic checksums |

### HMAC

`crypt.hmacSha256(secret, message)` computes an HMAC using SHA-256. Both
arguments are strings; the result is a lowercase hex string:

```gb
let sig = crypt.hmacSha256("my-secret-key", "the message");
io.println(sig);
# e.g. 4b2c3d...

# Verify by recomputing and comparing with constant-time equality
let ok = secrets.constantTimeEqual(sig, crypt.hmacSha256("my-secret-key", "the message"));
io.println(ok);   # true
```

HMAC-SHA256 is the standard algorithm for webhook signature verification,
API request signing, and message integrity checks.

### Password hashing

Never store passwords as plain hashes. Use a dedicated password hashing
function that incorporates a salt and a cost factor.

#### Argon2id (preferred)

`crypt.argon2idHash(password)` hashes a password using Argon2id and returns a
self-contained encoded string (PHC format) that includes the salt and parameters:

```gb
let hash = crypt.argon2idHash("hunter2");
io.println(hash);
# $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
```

`crypt.argon2idVerify(password, hash)` verifies a password against the stored
hash. Returns `true` on match:

```gb
let ok = crypt.argon2idVerify("hunter2", hash);
io.println(ok);   # true
io.println(crypt.argon2idVerify("wrong", hash));   # false
```

Default parameters: `memory=65536 KiB`, `time=3`, `parallelism=4`,
`keyLength=32`, `saltLength=16`. These can be tuned via an options dict:

```gb
let hash = crypt.argon2idHash("hunter2", {
    "memory":      131072,   # KiB (128 MiB)
    "time":        4,        # iterations (1-32)
    "parallelism": 2,        # threads (1-255)
    "keyLength":   32,       # output bytes (16-1024)
    "saltLength":  32        # salt bytes (8-1024)
});
```

Increase `memory` and `time` to make brute-force attacks more expensive. A
good starting point for interactive logins is `memory=65536, time=3`; for
high-security offline storage, use `memory=256000, time=4` or higher.

#### Bcrypt

`crypt.bcryptHash(password)` hashes a password with bcrypt at cost 10 (the
default). `crypt.bcryptVerify(password, hash)` verifies it:

```gb
let hash = crypt.bcryptHash("hunter2");
io.println(crypt.bcryptVerify("hunter2", hash));   # true

# Custom cost (4-31; higher = slower)
let strongHash = crypt.bcryptHash("hunter2", 12);
```

Bcrypt is well-supported but limited to 72-byte passwords and has lower
memory-hardness than Argon2id. Prefer Argon2id for new code.

### JWT - symmetric (HS256)

`crypt.jwtSign(payload, secret)` produces a signed JWT using HMAC-SHA256.
The payload is any dict (or value serialisable to JSON):

```gb
let token = crypt.jwtSign({
    "sub":  "user-42",
    "role": "admin",
    "exp":  datetime.nowUnix() + 3600
}, "my-signing-secret");

io.println(token);   # eyJhbGci...
```

`crypt.jwtVerify(token, secret)` verifies the signature and returns the decoded
payload as a dict, or `null` if the signature is invalid:

```gb
let payload = crypt.jwtVerify(token, "my-signing-secret");
if (payload != null) {
    io.println(payload["sub"]);   # user-42
}
```

`jwtVerify` returns `null` for any invalid token - bad format, wrong secret,
or corrupted signature. Expiry checking is not automatic; verify `exp` yourself:

```gb
let payload = crypt.jwtVerify(token, secret);
if (payload == null) {
    # reject
} else if (payload["exp"] < datetime.nowUnix()) {
    # expired
} else {
    # valid
}
```

`crypt.jwtDecode(token)` decodes a JWT without verifying the signature. Use
only for debugging or for inspecting the header:

```gb
let parts = crypt.jwtDecode(token);
io.println(parts["header"]);    # {"alg": "HS256", "typ": "JWT"}
io.println(parts["payload"]);   # {"sub": "user-42", ...}
```

Never trust the payload from `jwtDecode` for access control - always use
`jwtVerify` instead.

### JWT - asymmetric (RS256 and ES256)

Asymmetric JWTs let you sign with a private key and verify with a public key.
This is useful when the verifier (e.g. a microservice) should not have access
to the signing key.

#### RS256 (RSA + SHA-256)

```gb
import crypt;

let privPem = crypt.generateRsaKey(2048);       # or load from a file
let pubPem  = crypt.publicKey(privPem);

let token = crypt.jwtSignRS256({
    "sub":  "user-42",
    "exp":  datetime.nowUnix() + 3600
}, privPem);

let payload = crypt.jwtVerifyRS256(token, pubPem);
if (payload != null) {
    io.println(payload["sub"]);   # user-42
}
```

`jwtVerifyRS256` also accepts a certificate PEM in place of a public key PEM.

#### ES256 (ECDSA P-256 + SHA-256)

```gb
let privPem = crypt.generateEcKey("P-256");
let pubPem  = crypt.publicKey(privPem);

let token   = crypt.jwtSignES256({"sub": "user-42"}, privPem);
let payload = crypt.jwtVerifyES256(token, pubPem);
```

ES256 produces smaller signatures than RS256 and is preferred for new
asymmetric JWT work.

Both `jwtVerifyRS256` and `jwtVerifyES256` return `null` on any failure
(wrong key, bad format, corrupted signature).

### Key generation

#### RSA keys

`crypt.generateRsaKey(bits)` generates an RSA private key and returns it as a
PKCS#8 PEM string. The default bit size is 2048; valid range is 1024-8192:

```gb
let privPem = crypt.generateRsaKey();      # 2048-bit
let privPem = crypt.generateRsaKey(4096);  # 4096-bit
```

#### EC keys

`crypt.generateEcKey(curve)` generates an ECDSA private key. The default curve
is `"P-256"`; valid values are `"P-256"`, `"P-384"`, and `"P-521"`:

```gb
let privPem = crypt.generateEcKey();         # P-256
let privPem = crypt.generateEcKey("P-384");  # stronger curve
```

#### Ed25519 keys

`crypt.generateEd25519Key()` generates an Ed25519 private key:

```gb
let privPem = crypt.generateEd25519Key();
```

#### Extracting a public key

`crypt.publicKey(privatePem)` extracts the corresponding public key from any
supported private key type and returns it as a PKCS#8 PEM string:

```gb
let pubPem = crypt.publicKey(privPem);
io.println(pubPem);   # -----BEGIN PUBLIC KEY-----...
```

### Certificates

#### Self-signed certificates

`crypt.generateSelfSignedCert(options)` generates a self-signed X.509
certificate and returns a dict with `"cert"` and `"key"` PEM strings:

```gb
let result = crypt.generateSelfSignedCert({
    "subject": {
        "commonName":   "localhost",
        "organization": "Acme Inc",
        "country":      "GB"
    },
    "dnsNames":    ["localhost", "api.example.com"],
    "ipAddresses": ["127.0.0.1"],
    "validDays":   365,
    "keyType":     "EC-P256"   # RSA2048, RSA4096, EC-P256, EC-P384, EC-P521, Ed25519
});

io.println(result["cert"]);   # -----BEGIN CERTIFICATE-----...
io.println(result["key"]);    # -----BEGIN PRIVATE KEY-----...
```

Pass an existing `"key"` PEM to use a pre-generated key instead of generating
one:

```gb
let key  = crypt.generateEcKey("P-256");
let cert = crypt.generateSelfSignedCert({
    "subject": {"commonName": "localhost"},
    "key":     key
});
```

#### Certificate signing requests (CSR)

`crypt.generateCsr(options)` creates a PKCS#10 CSR for submission to a CA.
The `"key"` option is required:

```gb
let key = crypt.generateEcKey("P-256");
let csr = crypt.generateCsr({
    "key": key,
    "subject": {
        "commonName":   "api.example.com",
        "organization": "Acme Inc",
        "country":      "GB",
        "state":        "London",
        "locality":     "London"
    },
    "dnsNames": ["api.example.com", "www.example.com"]
});
io.println(csr);   # -----BEGIN CERTIFICATE REQUEST-----...
```

#### Parsing certificates

`crypt.parseCert(pem)` decodes an X.509 certificate PEM and returns a dict:

```gb
let info = crypt.parseCert(certPem);

io.println(info["subject"]);       # dict: commonName, organization, etc.
io.println(info["issuer"]);        # dict: same fields
io.println(info["dnsNames"]);      # list<string>
io.println(info["ipAddresses"]);   # list<string>
io.println(info["notBefore"]);     # RFC3339 string
io.println(info["notAfter"]);      # RFC3339 string
io.println(info["serialNumber"]);  # hex string
io.println(info["keyType"]);       # "RSA", "EC", or "Ed25519"
io.println(info["isCA"]);          # bool
```

Validate a certificate's expiry:

```gb
let info = crypt.parseCert(certPem);
let expiry = datetime.parseRFC3339(info["notAfter"]);
if (expiry < datetime.nowUnix()) {
    io.println("certificate has expired");
}
```

### Symmetric encryption

For protecting data at rest (session cookies, encrypted files, sensitive
config) Geblang exposes two authenticated AEAD ciphers. Both accept a 32-byte
key, generate a random nonce automatically, and produce a dict containing the
nonce and ciphertext.

#### AES-256-GCM

```gb
import crypt;
import bytes;
import secrets;

let key = secrets.randomBytes(32);            # 32-byte AES-256 key
let enc = crypt.aesEncrypt(key, "secret data");

# enc is {"nonce": bytes, "ciphertext": bytes}
let plaintext = crypt.aesDecrypt(key, enc["nonce"], enc["ciphertext"]);
io.println(plaintext.toString());             # secret data
```

Both calls accept an optional associated-data argument that is
authenticated but not encrypted (good for metadata that must not be
forged):

```gb
let aad = bytes.fromString("user-42");
let enc = crypt.aesEncrypt(key, "secret", aad);
let pt  = crypt.aesDecrypt(key, enc["nonce"], enc["ciphertext"], aad);
```

If the key, nonce, ciphertext, or associated data is altered between encrypt
and decrypt, `aesDecrypt` throws `RuntimeError: authentication failed` -
authenticity is checked alongside confidentiality, so callers do not need a
separate HMAC.

The key must be exactly 32 bytes (AES-256). Derive a key from a password with
Argon2id (and a stored salt) rather than passing the password directly. For
production secrets, prefer `secrets.randomBytes(32)` and store the key in a
dedicated secrets manager.

#### XChaCha20-Poly1305

XChaCha20-Poly1305 is the alternate modern AEAD. The 24-byte nonce can be
generated randomly without collision concerns even for very high message
volumes, which is convenient for stateless services.

```gb
let key = secrets.randomBytes(32);
let enc = crypt.chacha20Encrypt(key, "secret data");
let pt  = crypt.chacha20Decrypt(key, enc["nonce"], enc["ciphertext"]);
```

Use `aesEncrypt` when interoperating with other systems (AES-GCM is the
broader standard); use `chacha20Encrypt` when you need the larger nonce or
when the target platform lacks AES hardware acceleration.

### Miscellaneous encoding helpers

`crypt.base64Encode(text)` encodes a string to standard Base64.
`crypt.base64Decode(text)` decodes it back to a string. For binary-safe
encoding use the `bytes` module instead.

```gb
let encoded = crypt.base64Encode("hello world");
io.println(encoded);                       # aGVsbG8gd29ybGQ=
io.println(crypt.base64Decode(encoded));   # hello world
```

`crypt.randomHex(n)` generates `n` cryptographically random bytes and returns
them as a hex string of length `2n`. For secure random material, prefer
`secrets.randomHex` which is the canonical API:

```gb
let nonce = crypt.randomHex(16);   # 32-char hex string
```

---

## Secrets

```gb
import secrets;
```

The `secrets` module is the canonical place for reading secret material at
startup and generating cryptographically secure random values.

### Reading secrets

`secrets.requireEnv(name)` reads an environment variable and throws a runtime
error if it is not set. Use this for required secrets at application startup:

```gb
import secrets;

let dbUrl  = secrets.requireEnv("DATABASE_URL");
let apiKey = secrets.requireEnv("API_KEY");
```

`secrets.getEnv(name)` returns the value or `null` if the variable is not set:

```gb
let logLevel = secrets.getEnv("LOG_LEVEL") ?? "info";
```

`secrets.readFile(path)` reads a secret from a file and returns the content as
a string with trailing newlines stripped. Useful for Docker secrets and
Kubernetes secret mounts:

```gb
let cert = secrets.readFile("/run/secrets/tls.crt");
let key  = secrets.readFile("/run/secrets/tls.key");
```

Prefer `secrets.requireEnv` and `secrets.readFile` over `sys.getenv` when
accessing secrets - it signals intent clearly and makes secret access auditable.

### Secure random values

All `secrets.random*` functions read from the OS cryptographic random source
(`/dev/urandom` on Linux). They are safe for generating tokens, nonces, salts,
and session IDs.

`secrets.randomBytes(n)` returns `n` random bytes as a `bytes` value:

```gb
let salt = secrets.randomBytes(16);   # 16 random bytes
```

`secrets.randomHex(n)` returns `n` random bytes encoded as a lowercase hex
string of length `2n`:

```gb
let token    = secrets.randomHex(32);   # 64-char hex token
let csrfKey  = secrets.randomHex(16);   # 32-char key
```

`secrets.randomBase64(n)` returns `n` random bytes encoded as URL-safe Base64
(no padding):

```gb
let sessionId = secrets.randomBase64(32);   # URL-safe, ~43 chars
```

`secrets.randomInt(min, max)` returns a cryptographically random `int` in the
inclusive range `[min, max]`:

```gb
let otp = secrets.randomInt(100000, 999999);   # 6-digit OTP
let die = secrets.randomInt(1, 6);             # fair die roll
```

### `random` vs `secrets`: which one do I use?

Geblang ships two random number modules. Use the right one for the job:

| Purpose | Module | API |
|---------|--------|-----|
| Security tokens, session IDs, salts, OTPs, anything an attacker shouldn't predict | `secrets` | CSPRNG; reads from the OS entropy pool. Never seedable. |
| Simulation, sampling, shuffling, procedural generation, fuzz inputs, tests | `random` | Deterministic pseudo-random number generator. Seedable for reproducibility. |

`secrets.*` is the canonical security choice. `random.*` (documented in
`12-utilities` / **Random**) is for everything else where reproducibility
matters or cryptographic guarantees do not.

### Constant-time comparison

`secrets.constantTimeEqual(a, b)` compares two strings or `bytes` values in
constant time, preventing timing-based side-channel attacks. Both arguments
must be the same type:

```gb
let submitted = request.headers["X-Webhook-Signature"];
let expected  = crypt.hmacSha256(secret, request.body);

if (secrets.constantTimeEqual(submitted, expected)) {
    # signature valid
}
```

Always use `constantTimeEqual` when comparing authentication tokens, HMAC
signatures, or any other security-sensitive value. Regular `==` comparison can
leak information about how many bytes matched.

### Complete examples

#### API key authentication middleware

```gb
import secrets;
import crypt;

const API_KEY = secrets.requireEnv("API_KEY");

func checkApiKey(string submitted): bool {
    return secrets.constantTimeEqual(submitted, API_KEY);
}
```

#### Session token generation and storage

```gb
import secrets;
import datetime;

func newSession(string userId): dict<string, string> {
    let token    = secrets.randomHex(32);
    let expireAt = datetime.nowUnix() + 86400;   # 24 hours
    # store {token: userId, expireAt: expireAt} in your session store
    return {"token": token, "expireAt": expireAt as string};
}
```

#### Webhook signature verification

```gb
import crypt;
import secrets;

const WEBHOOK_SECRET = secrets.requireEnv("WEBHOOK_SECRET");

func verifyWebhook(string body, string signature): bool {
    let expected = "sha256=" + crypt.hmacSha256(WEBHOOK_SECRET, body);
    return secrets.constantTimeEqual(signature, expected);
}
```

#### Password authentication flow

```gb
import crypt;

# On registration - store hash, not the password
func hashPassword(string password): string {
    return crypt.argon2idHash(password);
}

# On login
func checkPassword(string submitted, string stored): bool {
    return crypt.argon2idVerify(submitted, stored);
}
```

#### JWT authentication middleware (HS256)

```gb
import crypt;
import datetime;
import secrets;

const JWT_SECRET = secrets.requireEnv("JWT_SECRET");

func issueToken(string userId): string {
    return crypt.jwtSign({
        "sub": userId,
        "exp": datetime.nowUnix() + 3600
    }, JWT_SECRET);
}

func verifyToken(string token): ?string {
    let payload = crypt.jwtVerify(token, JWT_SECRET);
    if (payload == null) { return null; }
    if (payload["exp"] < datetime.nowUnix()) { return null; }
    return payload["sub"] as string;
}
```

#### TLS certificate for a local HTTPS server

```gb
import crypt;
import io;

let result = crypt.generateSelfSignedCert({
    "subject":     {"commonName": "localhost"},
    "dnsNames":    ["localhost"],
    "ipAddresses": ["127.0.0.1"],
    "validDays":   365
});

io.writeFile("server.crt", result["cert"]);
io.writeFile("server.key", result["key"]);
```
