package transpilert

import (
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"html"
	"math/big"
	"net/url"
	"strings"
)

// encoding module bridge. Matches internal/native/native_encoding.go byte for
// byte. Encode functions accept a string or bytes input; base64/base64Url
// decode return string, base32/base58 decode return bytes, all mirroring the
// interpreter. sanitizeHtml is NOT bridged (bluemonday is non-stdlib); it
// diagnoses at lowering. Pure Go stdlib only.

func encodingInputBytes(v any, name string) []byte {
	switch x := v.(type) {
	case string:
		return []byte(x)
	case []byte:
		return x
	}
	panic(NewError("RuntimeError", fmt.Sprintf("%s expects string or bytes", name)))
}

func EncodingBase64Encode(v any) string {
	return base64.StdEncoding.EncodeToString(encodingInputBytes(v, "encoding.base64Encode"))
}

func EncodingBase64Decode(s string) string {
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(NewError("RuntimeError", "encoding.base64Decode: "+err.Error()))
	}
	return string(decoded)
}

func EncodingBase64UrlEncode(v any) string {
	return base64.RawURLEncoding.EncodeToString(encodingInputBytes(v, "encoding.base64UrlEncode"))
}

func EncodingBase64UrlDecode(s string) string {
	decoded, err := decodeBase64Url(s)
	if err != nil {
		panic(NewError("RuntimeError", "encoding.base64UrlDecode: "+err.Error()))
	}
	return string(decoded)
}

// decodeBase64Url accepts both unpadded (JOSE) and padded URL input.
func decodeBase64Url(s string) ([]byte, error) {
	trimmed := strings.TrimRight(s, "=")
	if decoded, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

func EncodingBase32Encode(v any) string {
	return base32.StdEncoding.EncodeToString(encodingInputBytes(v, "encoding.base32Encode"))
}

func EncodingBase32Decode(s string) []byte {
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		decoded, err = base32.StdEncoding.DecodeString(s)
		if err != nil {
			panic(NewError("RuntimeError", "encoding.base32Decode: "+err.Error()))
		}
	}
	return decoded
}

// base58Alphabet is the Bitcoin / IPFS alphabet (no 0, O, I, l).
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func EncodingBase58Encode(v any) string {
	return base58Encode(encodingInputBytes(v, "encoding.base58Encode"))
}

func EncodingBase58Decode(s string) []byte {
	decoded, err := base58Decode(s)
	if err != nil {
		panic(NewError("RuntimeError", "encoding.base58Decode: "+err.Error()))
	}
	return decoded
}

func base58Encode(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	leading := 0
	for _, b := range data {
		if b != 0 {
			break
		}
		leading++
	}
	x := new(big.Int).SetBytes(data)
	base := big.NewInt(58)
	mod := new(big.Int)
	var encoded []byte
	for x.Sign() > 0 {
		x.DivMod(x, base, mod)
		encoded = append(encoded, base58Alphabet[mod.Int64()])
	}
	for i := 0; i < leading; i++ {
		encoded = append(encoded, base58Alphabet[0])
	}
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	return string(encoded)
}

func base58Decode(s string) ([]byte, error) {
	x := new(big.Int)
	base := big.NewInt(58)
	leading := 0
	for _, c := range s {
		if c == rune(base58Alphabet[0]) {
			leading++
			continue
		}
		break
	}
	for _, c := range s {
		idx := strings.IndexRune(base58Alphabet, c)
		if idx < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", c)
		}
		x.Mul(x, base)
		x.Add(x, big.NewInt(int64(idx)))
	}
	decoded := x.Bytes()
	out := make([]byte, leading+len(decoded))
	copy(out[leading:], decoded)
	return out, nil
}

func EncodingUrlEncode(s string) string { return url.QueryEscape(s) }

func EncodingUrlDecode(s string) string {
	decoded, err := url.QueryUnescape(s)
	if err != nil {
		panic(NewError("RuntimeError", "encoding.urlDecode: "+err.Error()))
	}
	return decoded
}

func EncodingHtmlEscape(s string) string   { return html.EscapeString(s) }
func EncodingHtmlUnescape(s string) string { return html.UnescapeString(s) }
