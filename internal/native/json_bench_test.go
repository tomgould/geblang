package native

import (
	"strings"
	"testing"
)

// benchJSON mirrors the shape of benchmarks/geblang/json_roundtrip.gb
// (nested objects, arrays, mixed scalar types) repeated to a realistic
// size, so the profile reflects the hot parse/encode paths.
var benchJSON = func() string {
	record := `{"id":1,"title":"alpha","score":95,"active":true,"labels":["x","y"],"meta":{"users":12345,"ratio":0.75}}`
	var b strings.Builder
	b.WriteString(`{"records":[`)
	for i := 0; i < 400; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(record)
	}
	b.WriteString(`]}`)
	return b.String()
}()

func BenchmarkJSONParse(b *testing.B) {
	b.SetBytes(int64(len(benchJSON)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v, perr := ParseJSONText(benchJSON)
		if perr != nil {
			b.Fatal(perr.Message)
		}
		_ = v
	}
}

func BenchmarkJSONStringify(b *testing.B) {
	v, perr := ParseJSONText(benchJSON)
	if perr != nil {
		b.Fatal(perr.Message)
	}
	b.SetBytes(int64(len(benchJSON)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s, err := EncodeJSONValue(v)
		if err != nil {
			b.Fatal(err)
		}
		_ = s
	}
}

func BenchmarkJSONRoundtrip(b *testing.B) {
	b.SetBytes(int64(len(benchJSON)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v, perr := ParseJSONText(benchJSON)
		if perr != nil {
			b.Fatal(perr.Message)
		}
		if _, err := EncodeJSONValue(v); err != nil {
			b.Fatal(err)
		}
	}
}
