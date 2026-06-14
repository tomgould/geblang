package transpilert

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/crc32"
)

// crypt module bridge: ONLY the Go-stdlib-backed functions. Output matches
// internal/native/native_crypt.go byte for byte (hex-encoded digests, IEEE
// crc32, base64 std encoding). The hash/hmac inputs accept string or bytes,
// mirroring singleHashInput / hmacInputs; crypt.base64Encode/Decode take a
// single string. Functions needing golang.org/x/crypto (sha3_256, blake2b,
// bcrypt*, argon2*, password*, aes*, chacha20*, jwt*, pki*) are NOT here and
// diagnose at lowering. Pure Go stdlib only.

func cryptHashInput(v any, name string) []byte {
	switch x := v.(type) {
	case string:
		return []byte(x)
	case []byte:
		return x
	}
	panic(NewError("RuntimeError", fmt.Sprintf("%s expects a string or bytes argument", name)))
}

func CryptMd5(v any) string {
	sum := md5.Sum(cryptHashInput(v, "crypt.md5"))
	return hex.EncodeToString(sum[:])
}

func CryptSha1(v any) string {
	sum := sha1.Sum(cryptHashInput(v, "crypt.sha1"))
	return hex.EncodeToString(sum[:])
}

func CryptSha256(v any) string {
	sum := sha256.Sum256(cryptHashInput(v, "crypt.sha256"))
	return hex.EncodeToString(sum[:])
}

func CryptSha512(v any) string {
	sum := sha512.Sum512(cryptHashInput(v, "crypt.sha512"))
	return hex.EncodeToString(sum[:])
}

func CryptCrc32(v any) int64 {
	return int64(crc32.ChecksumIEEE(cryptHashInput(v, "crypt.crc32")))
}

func CryptHmacSha256(key, msg any) string {
	mac := hmac.New(sha256.New, cryptHashInput(key, "crypt.hmacSha256 key"))
	_, _ = mac.Write(cryptHashInput(msg, "crypt.hmacSha256 message"))
	return hex.EncodeToString(mac.Sum(nil))
}

func CryptHmacSha256Bytes(key, msg any) []byte {
	mac := hmac.New(sha256.New, cryptHashInput(key, "crypt.hmacSha256Bytes key"))
	_, _ = mac.Write(cryptHashInput(msg, "crypt.hmacSha256Bytes message"))
	return mac.Sum(nil)
}

func CryptBase64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func CryptBase64Decode(s string) string {
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(NewError("RuntimeError", err.Error()))
	}
	return string(decoded)
}
