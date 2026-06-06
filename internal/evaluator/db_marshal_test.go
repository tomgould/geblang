package evaluator

import (
	"bytes"
	"math"
	"math/big"
	"testing"
	"time"

	"geblang/internal/runtime"
)

// pgx returns int16/int32/float32 and mysql returns uint64, which sqlite never
// produces and integration tests can't reach without a live server.

func TestDBArgBindTypes(t *testing.T) {
	bigVal, _ := new(big.Int).SetString("123456789012", 10)
	overflow, _ := new(big.Int).SetString("99999999999999999999999999", 10)
	cases := []struct {
		name string
		in   runtime.Value
		want any
	}{
		{"null", runtime.Null{}, nil},
		{"bool", runtime.Bool{Value: true}, true},
		{"smallint", runtime.SmallInt{Value: 42}, int64(42)},
		{"int", runtime.Int{Value: bigVal}, int64(123456789012)},
		{"float", runtime.Float{Value: 1.5}, 1.5},
		{"string", runtime.String{Value: "hi"}, "hi"},
		{"decimal", runtime.Decimal{Value: big.NewRat(1, 2)}, "0.5000000000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := dbArg(c.in)
			if err != nil {
				t.Fatalf("dbArg(%s) error: %v", c.name, err)
			}
			if got != c.want {
				t.Fatalf("dbArg(%s) = %v (%T), want %v (%T)", c.name, got, got, c.want, c.want)
			}
		})
	}

	got, err := dbArg(runtime.Bytes{Value: []byte{1, 2, 3}})
	if err != nil {
		t.Fatalf("dbArg(bytes) error: %v", err)
	}
	if b, ok := got.([]byte); !ok || !bytes.Equal(b, []byte{1, 2, 3}) {
		t.Fatalf("dbArg(bytes) = %v, want [1 2 3]", got)
	}

	if _, err := dbArg(runtime.Int{Value: overflow}); err == nil {
		t.Fatal("dbArg(out-of-range int) should error")
	}
	if _, err := dbArg(&runtime.List{}); err == nil {
		t.Fatal("dbArg(list) should error as unsupported")
	}
}

func TestSQLValueToRuntimeScanTypes(t *testing.T) {
	cases := []struct {
		name     string
		in       any
		wantType string
		wantStr  string
	}{
		{"int64", int64(7), "int", "7"},
		{"int", int(9), "int", "9"},
		{"int32-pg-int4", int32(5), "int", "5"},
		{"int16-pg-int2", int16(3), "int", "3"},
		{"int8", int8(2), "int", "2"},
		{"uint32", uint32(11), "int", "11"},
		{"uint16", uint16(13), "int", "13"},
		{"uint8", uint8(17), "int", "17"},
		{"uint64-mysql-unsigned", uint64(math.MaxUint64), "int", "18446744073709551615"},
		{"uint", uint(21), "int", "21"},
		{"float64", float64(2.5), "float", "2.5"},
		{"float32-pg-float4", float32(1.5), "float", "1.5"},
		{"string", "txt", "string", "txt"},
		{"bool", true, "bool", "true"},
		{"null", nil, "null", "null"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sqlValueToRuntime(c.in)
			if err != nil {
				t.Fatalf("sqlValueToRuntime(%s) error: %v", c.name, err)
			}
			if got.TypeName() != c.wantType {
				t.Fatalf("sqlValueToRuntime(%s) type = %s, want %s", c.name, got.TypeName(), c.wantType)
			}
			if got.Inspect() != c.wantStr {
				t.Fatalf("sqlValueToRuntime(%s) = %s, want %s", c.name, got.Inspect(), c.wantStr)
			}
		})
	}

	got, err := sqlValueToRuntime([]byte{4, 5})
	if err != nil {
		t.Fatalf("sqlValueToRuntime(bytes) error: %v", err)
	}
	if got.TypeName() != "bytes" {
		t.Fatalf("sqlValueToRuntime(bytes) type = %s, want bytes", got.TypeName())
	}

	ts := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	got, err = sqlValueToRuntime(ts)
	if err != nil {
		t.Fatalf("sqlValueToRuntime(time) error: %v", err)
	}
	if got.TypeName() != "string" || got.Inspect() != "2026-06-06T12:00:00Z" {
		t.Fatalf("sqlValueToRuntime(time) = %v", got.Inspect())
	}

	if _, err := sqlValueToRuntime(struct{}{}); err == nil {
		t.Fatal("sqlValueToRuntime(struct) should error as unsupported")
	}
}
