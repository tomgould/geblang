package bytecode

import (
	"testing"

	"geblang/internal/runtime"
)

var primitiveBenchmarkResult runtime.Value

func BenchmarkPrimitiveMethodDispatch(b *testing.B) {
	dict := runtime.NewDict()
	dict.PutEntry("s:key", runtime.DictEntry{
		Key:   runtime.String{Value: "key"},
		Value: runtime.SmallInt{Value: 1},
	})
	list := &runtime.List{Elements: []runtime.Value{
		runtime.SmallInt{Value: 1},
		runtime.SmallInt{Value: 2},
		runtime.SmallInt{Value: 3},
	}}
	cases := []struct {
		name     string
		receiver runtime.Value
		method   string
		args     []runtime.Value
	}{
		{"length", runtime.String{Value: "template value"}, "length", nil},
		{"isEmpty", runtime.String{Value: "template value"}, "isEmpty", nil},
		{"contains", runtime.String{Value: "template value"}, "contains", []runtime.Value{runtime.String{Value: "value"}}},
		{"substring", runtime.String{Value: "template value"}, "substring", []runtime.Value{runtime.SmallInt{Value: 0}, runtime.SmallInt{Value: 8}}},
		{"dict-get", dict, "get", []runtime.Value{runtime.String{Value: "key"}}},
		{"list-get", list, "get", []runtime.Value{runtime.SmallInt{Value: 1}}},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				result, err := primitiveMethod(tc.receiver, tc.method, tc.args)
				if err != nil {
					b.Fatal(err)
				}
				primitiveBenchmarkResult = result
			}
		})
	}
}

func BenchmarkPrimitiveMethodTemplateShape(b *testing.B) {
	context := runtime.NewDict()
	context.PutEntry("s:title", runtime.DictEntry{
		Key:   runtime.String{Value: "title"},
		Value: runtime.String{Value: "Geblang"},
	})
	items := &runtime.List{Elements: []runtime.Value{
		runtime.String{Value: "one"},
		runtime.String{Value: "two"},
		runtime.String{Value: "three"},
	}}
	calls := []struct {
		receiver runtime.Value
		method   string
		args     []runtime.Value
	}{
		{context, "get", []runtime.Value{runtime.String{Value: "title"}}},
		{context, "contains", []runtime.Value{runtime.String{Value: "title"}}},
		{items, "length", nil},
		{items, "isEmpty", nil},
		{items, "get", []runtime.Value{runtime.SmallInt{Value: 1}}},
		{runtime.String{Value: "Geblang"}, "isEmpty", nil},
		{runtime.String{Value: "Geblang"}, "contains", []runtime.Value{runtime.String{Value: "lang"}}},
		{runtime.String{Value: "Geblang"}, "substring", []runtime.Value{runtime.SmallInt{Value: 0}, runtime.SmallInt{Value: 3}}},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, call := range calls {
			result, err := primitiveMethod(call.receiver, call.method, call.args)
			if err != nil {
				b.Fatal(err)
			}
			primitiveBenchmarkResult = result
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(calls)), "ns/call")
}
