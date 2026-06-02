package native

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"

	"geblang/internal/runtime"
)

// MessagePack 5 codec, common cases:
//   nil / bool / int / uint / float32 / float64 /
//   fixstr / str 8|16|32 / bin 8|16|32 /
//   fixarray / array 16|32 / fixmap / map 16|32
//
// Out of scope for 1.6.0: ext types and the timestamp ext.
// Decimal round-trips as a MessagePack string (lossless, portable).

func registerMsgpack(r *Registry) {
	r.Register("msgpack", "encode", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("msgpack.encode expects exactly one argument")
		}
		out, err := msgpackEncode(args[0])
		if err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: out}, nil
	})
	r.Register("msgpack", "decode", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("msgpack.decode expects exactly one bytes argument")
		}
		data, ok := args[0].(runtime.Bytes)
		if !ok {
			return nil, fmt.Errorf("msgpack.decode argument must be bytes")
		}
		d := &msgpackDecoder{buf: data.Value}
		value, err := d.readValue()
		if err != nil {
			return nil, err
		}
		if d.pos != len(d.buf) {
			return nil, fmt.Errorf("msgpack.decode: %d trailing bytes after value", len(d.buf)-d.pos)
		}
		return value, nil
	})
	r.Register("msgpack", "tryDecode", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("msgpack.tryDecode expects exactly one bytes argument")
		}
		data, ok := args[0].(runtime.Bytes)
		if !ok {
			return runtime.Null{}, nil
		}
		d := &msgpackDecoder{buf: data.Value}
		value, err := d.readValue()
		if err != nil || d.pos != len(d.buf) {
			return runtime.Null{}, nil
		}
		return value, nil
	})
	r.Register("msgpack", "validate", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("msgpack.validate expects exactly one bytes argument")
		}
		data, ok := args[0].(runtime.Bytes)
		if !ok {
			return runtime.Bool{Value: false}, nil
		}
		d := &msgpackDecoder{buf: data.Value}
		if _, err := d.readValue(); err != nil {
			return runtime.Bool{Value: false}, nil
		}
		return runtime.Bool{Value: d.pos == len(d.buf)}, nil
	})
}

// ---- encode ----

func msgpackEncode(value runtime.Value) ([]byte, error) {
	out := make([]byte, 0, 32)
	return appendMsgpackValue(out, value)
}

func appendMsgpackValue(out []byte, value runtime.Value) ([]byte, error) {
	switch v := value.(type) {
	case nil, runtime.Null:
		return append(out, 0xc0), nil
	case runtime.Bool:
		if v.Value {
			return append(out, 0xc3), nil
		}
		return append(out, 0xc2), nil
	case runtime.SmallInt:
		return appendMsgpackSigned(out, int64(v.Value)), nil
	case runtime.Int:
		if v.Value == nil {
			return append(out, 0xc0), nil
		}
		if v.Value.IsInt64() {
			return appendMsgpackSigned(out, v.Value.Int64()), nil
		}
		// Out of int64 range. MessagePack 5 has uint64 / int64 only;
		// reject rather than truncate.
		return nil, fmt.Errorf("msgpack.encode: int %s exceeds 64-bit range", v.Value.String())
	case runtime.Float:
		bits := math.Float64bits(v.Value)
		out = append(out, 0xcb)
		out = binary.BigEndian.AppendUint64(out, bits)
		return out, nil
	case runtime.Decimal:
		// Decimal has no MessagePack representation; encode the canonical
		// string form so the round trip stays lossless. Callers who want
		// the typed Decimal back do `(value as decimal)` after decode.
		return appendMsgpackString(out, v.Inspect())
	case runtime.String:
		return appendMsgpackString(out, v.Value)
	case runtime.Bytes:
		return appendMsgpackBytes(out, v.Value)
	case *runtime.List:
		return appendMsgpackList(out, v.Elements)
	case runtime.Dict:
		return appendMsgpackDict(out, v)
	default:
		return nil, fmt.Errorf("msgpack.encode: unsupported type %s", value.TypeName())
	}
}

func appendMsgpackSigned(out []byte, n int64) []byte {
	switch {
	case n >= 0 && n <= 0x7f:
		return append(out, byte(n))
	case n < 0 && n >= -32:
		return append(out, byte(n))
	case n >= -128 && n <= 127:
		return append(out, 0xd0, byte(int8(n)))
	case n >= -32768 && n <= 32767:
		out = append(out, 0xd1)
		out = binary.BigEndian.AppendUint16(out, uint16(int16(n)))
		return out
	case n >= -2147483648 && n <= 2147483647:
		out = append(out, 0xd2)
		out = binary.BigEndian.AppendUint32(out, uint32(int32(n)))
		return out
	default:
		out = append(out, 0xd3)
		out = binary.BigEndian.AppendUint64(out, uint64(n))
		return out
	}
}

func appendMsgpackString(out []byte, s string) ([]byte, error) {
	data := []byte(s)
	n := len(data)
	switch {
	case n <= 31:
		out = append(out, 0xa0|byte(n))
	case n <= 0xff:
		out = append(out, 0xd9, byte(n))
	case n <= 0xffff:
		out = append(out, 0xda)
		out = binary.BigEndian.AppendUint16(out, uint16(n))
	case n <= 0xffffffff:
		out = append(out, 0xdb)
		out = binary.BigEndian.AppendUint32(out, uint32(n))
	default:
		return nil, fmt.Errorf("msgpack.encode: string too long (%d bytes)", n)
	}
	return append(out, data...), nil
}

func appendMsgpackBytes(out []byte, data []byte) ([]byte, error) {
	n := len(data)
	switch {
	case n <= 0xff:
		out = append(out, 0xc4, byte(n))
	case n <= 0xffff:
		out = append(out, 0xc5)
		out = binary.BigEndian.AppendUint16(out, uint16(n))
	case n <= 0xffffffff:
		out = append(out, 0xc6)
		out = binary.BigEndian.AppendUint32(out, uint32(n))
	default:
		return nil, fmt.Errorf("msgpack.encode: bytes too long (%d bytes)", n)
	}
	return append(out, data...), nil
}

func appendMsgpackList(out []byte, elements []runtime.Value) ([]byte, error) {
	n := len(elements)
	switch {
	case n <= 15:
		out = append(out, 0x90|byte(n))
	case n <= 0xffff:
		out = append(out, 0xdc)
		out = binary.BigEndian.AppendUint16(out, uint16(n))
	case n <= 0xffffffff:
		out = append(out, 0xdd)
		out = binary.BigEndian.AppendUint32(out, uint32(n))
	default:
		return nil, fmt.Errorf("msgpack.encode: list too long (%d elements)", n)
	}
	for _, el := range elements {
		var err error
		out, err = appendMsgpackValue(out, el)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func appendMsgpackDict(out []byte, d runtime.Dict) ([]byte, error) {
	// Iterate via the dict's Order slice so insertion order is
	// preserved on the wire. For all-string-key dicts the encoder
	// sorts by key to keep encode output reproducible across
	// backends (the VM's Inspect of dicts sorts alphabetically; the
	// evaluator's preserves insertion). Mixed-key dicts and
	// non-string-key dicts ride the insertion order.
	keys, values := dictOrderedPairs(d)
	if allStringKeys(keys) {
		indices := make([]int, len(keys))
		for i := range indices {
			indices[i] = i
		}
		sort.Slice(indices, func(i, j int) bool {
			return keys[indices[i]].(runtime.String).Value < keys[indices[j]].(runtime.String).Value
		})
		sortedKeys := make([]runtime.Value, len(keys))
		sortedValues := make([]runtime.Value, len(values))
		for i, idx := range indices {
			sortedKeys[i] = keys[idx]
			sortedValues[i] = values[idx]
		}
		keys = sortedKeys
		values = sortedValues
	}
	n := len(keys)
	switch {
	case n <= 15:
		out = append(out, 0x80|byte(n))
	case n <= 0xffff:
		out = append(out, 0xde)
		out = binary.BigEndian.AppendUint16(out, uint16(n))
	case n <= 0xffffffff:
		out = append(out, 0xdf)
		out = binary.BigEndian.AppendUint32(out, uint32(n))
	default:
		return nil, fmt.Errorf("msgpack.encode: dict too large (%d pairs)", n)
	}
	for i := range keys {
		var err error
		out, err = appendMsgpackValue(out, keys[i])
		if err != nil {
			return nil, err
		}
		out, err = appendMsgpackValue(out, values[i])
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func dictOrderedPairs(d runtime.Dict) ([]runtime.Value, []runtime.Value) {
	if d.Order == nil {
		return nil, nil
	}
	keys := make([]runtime.Value, 0, len(*d.Order))
	values := make([]runtime.Value, 0, len(*d.Order))
	for _, k := range *d.Order {
		entry, ok := d.Entries[k]
		if !ok {
			continue
		}
		keys = append(keys, entry.Key)
		values = append(values, entry.Value)
	}
	return keys, values
}

func allStringKeys(keys []runtime.Value) bool {
	for _, k := range keys {
		if _, ok := k.(runtime.String); !ok {
			return false
		}
	}
	return true
}

// ---- decode ----

type msgpackDecoder struct {
	buf []byte
	pos int
}

func (d *msgpackDecoder) require(n int) ([]byte, error) {
	if d.pos+n > len(d.buf) {
		return nil, errors.New("msgpack.decode: unexpected end of input")
	}
	chunk := d.buf[d.pos : d.pos+n]
	d.pos += n
	return chunk, nil
}

func (d *msgpackDecoder) readValue() (runtime.Value, error) {
	header, err := d.require(1)
	if err != nil {
		return nil, err
	}
	tag := header[0]
	switch {
	case tag == 0xc0:
		return runtime.Null{}, nil
	case tag == 0xc2:
		return runtime.Bool{Value: false}, nil
	case tag == 0xc3:
		return runtime.Bool{Value: true}, nil
	case tag <= 0x7f:
		return runtime.SmallInt{Value: int64(tag)}, nil
	case tag >= 0xe0:
		return runtime.SmallInt{Value: int64(int8(tag))}, nil
	case tag >= 0xa0 && tag <= 0xbf:
		return d.readString(int(tag & 0x1f))
	case tag >= 0x90 && tag <= 0x9f:
		return d.readArray(int(tag & 0x0f))
	case tag >= 0x80 && tag <= 0x8f:
		return d.readMap(int(tag & 0x0f))
	case tag == 0xcc:
		b, err := d.require(1)
		if err != nil {
			return nil, err
		}
		return runtime.SmallInt{Value: int64(b[0])}, nil
	case tag == 0xcd:
		b, err := d.require(2)
		if err != nil {
			return nil, err
		}
		return runtime.SmallInt{Value: int64(binary.BigEndian.Uint16(b))}, nil
	case tag == 0xce:
		b, err := d.require(4)
		if err != nil {
			return nil, err
		}
		return runtime.SmallInt{Value: int64(binary.BigEndian.Uint32(b))}, nil
	case tag == 0xcf:
		b, err := d.require(8)
		if err != nil {
			return nil, err
		}
		u := binary.BigEndian.Uint64(b)
		if u <= math.MaxInt64 {
			return runtime.SmallInt{Value: int64(u)}, nil
		}
		return runtime.Int{Value: new(big.Int).SetUint64(u)}, nil
	case tag == 0xd0:
		b, err := d.require(1)
		if err != nil {
			return nil, err
		}
		return runtime.SmallInt{Value: int64(int8(b[0]))}, nil
	case tag == 0xd1:
		b, err := d.require(2)
		if err != nil {
			return nil, err
		}
		return runtime.SmallInt{Value: int64(int16(binary.BigEndian.Uint16(b)))}, nil
	case tag == 0xd2:
		b, err := d.require(4)
		if err != nil {
			return nil, err
		}
		return runtime.SmallInt{Value: int64(int32(binary.BigEndian.Uint32(b)))}, nil
	case tag == 0xd3:
		b, err := d.require(8)
		if err != nil {
			return nil, err
		}
		return runtime.SmallInt{Value: int64(binary.BigEndian.Uint64(b))}, nil
	case tag == 0xca:
		b, err := d.require(4)
		if err != nil {
			return nil, err
		}
		return runtime.Float{Value: float64(math.Float32frombits(binary.BigEndian.Uint32(b)))}, nil
	case tag == 0xcb:
		b, err := d.require(8)
		if err != nil {
			return nil, err
		}
		return runtime.Float{Value: math.Float64frombits(binary.BigEndian.Uint64(b))}, nil
	case tag == 0xd9:
		b, err := d.require(1)
		if err != nil {
			return nil, err
		}
		return d.readString(int(b[0]))
	case tag == 0xda:
		b, err := d.require(2)
		if err != nil {
			return nil, err
		}
		return d.readString(int(binary.BigEndian.Uint16(b)))
	case tag == 0xdb:
		b, err := d.require(4)
		if err != nil {
			return nil, err
		}
		return d.readString(int(binary.BigEndian.Uint32(b)))
	case tag == 0xc4:
		b, err := d.require(1)
		if err != nil {
			return nil, err
		}
		return d.readBin(int(b[0]))
	case tag == 0xc5:
		b, err := d.require(2)
		if err != nil {
			return nil, err
		}
		return d.readBin(int(binary.BigEndian.Uint16(b)))
	case tag == 0xc6:
		b, err := d.require(4)
		if err != nil {
			return nil, err
		}
		return d.readBin(int(binary.BigEndian.Uint32(b)))
	case tag == 0xdc:
		b, err := d.require(2)
		if err != nil {
			return nil, err
		}
		return d.readArray(int(binary.BigEndian.Uint16(b)))
	case tag == 0xdd:
		b, err := d.require(4)
		if err != nil {
			return nil, err
		}
		return d.readArray(int(binary.BigEndian.Uint32(b)))
	case tag == 0xde:
		b, err := d.require(2)
		if err != nil {
			return nil, err
		}
		return d.readMap(int(binary.BigEndian.Uint16(b)))
	case tag == 0xdf:
		b, err := d.require(4)
		if err != nil {
			return nil, err
		}
		return d.readMap(int(binary.BigEndian.Uint32(b)))
	default:
		return nil, fmt.Errorf("msgpack.decode: unsupported tag 0x%02x", tag)
	}
}

func (d *msgpackDecoder) readString(n int) (runtime.Value, error) {
	bytes, err := d.require(n)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: string(bytes)}, nil
}

func (d *msgpackDecoder) readBin(n int) (runtime.Value, error) {
	bytes, err := d.require(n)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, bytes)
	return runtime.Bytes{Value: out}, nil
}

func (d *msgpackDecoder) readArray(n int) (runtime.Value, error) {
	elements := make([]runtime.Value, n)
	for i := 0; i < n; i++ {
		v, err := d.readValue()
		if err != nil {
			return nil, err
		}
		elements[i] = v
	}
	return &runtime.List{Elements: elements}, nil
}

func (d *msgpackDecoder) readMap(n int) (runtime.Value, error) {
	out := runtime.NewDictHint(n)
	for i := 0; i < n; i++ {
		k, err := d.readValue()
		if err != nil {
			return nil, err
		}
		v, err := d.readValue()
		if err != nil {
			return nil, err
		}
		out.PutEntry(DictKey(k), runtime.DictEntry{Key: k, Value: v})
	}
	return out, nil
}
