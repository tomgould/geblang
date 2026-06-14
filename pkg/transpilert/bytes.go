package transpilert

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Bytes helpers mirror internal/native bytes-module functions and the
// bytes-value primitive methods byte-for-byte over the Go stdlib (encoding/hex,
// encoding/base64), so --native matches the interpreter and stays zero-dep.

func BytesFromString(s string) []byte { return []byte(s) }

func BytesFromList(elems []int64) []byte {
	out := make([]byte, len(elems))
	for i, n := range elems {
		out[i] = byte(n)
	}
	return out
}

func BytesToString(b []byte) string { return string(b) }

func BytesFromHex(s string) []byte {
	data, err := hex.DecodeString(s)
	if err != nil {
		panic(NewError("RuntimeError", err.Error()))
	}
	return data
}

func BytesToHex(b []byte) string { return hex.EncodeToString(b) }

func BytesFromBase64(s string) []byte {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(NewError("RuntimeError", err.Error()))
	}
	return data
}

func BytesToBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func BytesFromBase64Url(s string) []byte {
	data, err := decodeBase64Url(s)
	if err != nil {
		panic(NewError("RuntimeError", "bytes.fromBase64Url: "+err.Error()))
	}
	return data
}

func BytesToBase64Url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func BytesConcat(parts ...[]byte) []byte {
	out := []byte{}
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// BytesGet returns the unsigned byte at i (negative i counts from the end),
// panicking out-of-range to match the interpreter.
func BytesGet(b []byte, i int) int64 {
	if i < 0 {
		i = len(b) + i
	}
	if i < 0 || i >= len(b) {
		panic(NewError("RuntimeError", "bytes index out of range"))
	}
	return int64(b[i])
}

func BytesToList(b []byte) []int64 {
	out := make([]int64, len(b))
	for i, v := range b {
		out[i] = int64(v)
	}
	return out
}

// BytesContains matches either a byte sub-slice or a single int byte (an int
// outside 0..255 can never be contained). int and int64 both occur because an
// untyped Go literal boxes as int while a typed int value boxes as int64.
func BytesContains(b []byte, needle any) bool {
	var n int64
	switch v := needle.(type) {
	case []byte:
		return bytes.Contains(b, v)
	case int64:
		n = v
	case int:
		n = int64(v)
	default:
		return false
	}
	if n < 0 || n > 255 {
		return false
	}
	return bytes.Contains(b, []byte{byte(n)})
}

// BytesSlice clamps start/end the interpreter's way (negative indices from the
// end, end<start collapses to start) and copies the range.
func BytesSlice(b []byte, hasEnd bool, start, end int) []byte {
	n := len(b)
	if start < 0 {
		start = n + start
	}
	if start < 0 {
		start = 0
	}
	if start > n {
		start = n
	}
	if !hasEnd {
		end = n
	} else {
		if end < 0 {
			end = n + end
		}
		if end < start {
			end = start
		}
		if end > n {
			end = n
		}
	}
	out := make([]byte, end-start)
	copy(out, b[start:end])
	return out
}

// BytesToStringEncoding backs bytes.toString(b, encoding); only utf-8 is valid.
func BytesToStringEncoding(b []byte, encoding string) string {
	if !strings.EqualFold(encoding, "utf-8") {
		panic(NewError("RuntimeError", "bytes.toString only supports utf-8 encoding"))
	}
	return string(b)
}

// BytesFromStringEncoding backs bytes.fromString(s, encoding); only utf-8 is valid.
func BytesFromStringEncoding(s string, encoding string) []byte {
	if !strings.EqualFold(encoding, "utf-8") {
		panic(NewError("RuntimeError", "bytes.fromString only supports utf-8 encoding"))
	}
	return []byte(s)
}
