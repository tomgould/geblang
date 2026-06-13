package native

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"geblang/internal/runtime"
	"time"

	googleuuid "github.com/google/uuid"
)

func registerUUID(r *Registry) {
	r.Register("uuid", "v1", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("uuid.v1 expects no arguments")
		}
		u, err := googleuuid.NewUUID()
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: u.String()}, nil
	})
	r.Register("uuid", "v4", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("uuid.v4 expects no arguments")
		}
		var data [16]byte
		if _, err := rand.Read(data[:]); err != nil {
			return nil, err
		}
		data[6] = (data[6] & 0x0f) | 0x40
		data[8] = (data[8] & 0x3f) | 0x80
		return runtime.String{Value: formatUUID(data)}, nil
	})
	r.Register("uuid", "v7", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("uuid.v7 expects no arguments")
		}
		var data [16]byte
		if _, err := rand.Read(data[:]); err != nil {
			return nil, err
		}
		ms := time.Now().UnixMilli()
		data[0] = byte(ms >> 40)
		data[1] = byte(ms >> 32)
		data[2] = byte(ms >> 24)
		data[3] = byte(ms >> 16)
		data[4] = byte(ms >> 8)
		data[5] = byte(ms)
		data[6] = (data[6] & 0x0f) | 0x70
		data[8] = (data[8] & 0x3f) | 0x80
		return runtime.String{Value: formatUUID(data)}, nil
	})
	r.Register("uuid", "v3", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("uuid.v3 expects 2 arguments: namespace (string), name (string)")
		}
		ns, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.v3: namespace must be a string")
		}
		name, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.v3: name must be a string")
		}
		nsUUID, err := googleuuid.Parse(ns.Value)
		if err != nil {
			return nil, fmt.Errorf("uuid.v3: invalid namespace UUID: %w", err)
		}
		u := googleuuid.NewMD5(nsUUID, []byte(name.Value))
		return runtime.String{Value: u.String()}, nil
	})
	r.Register("uuid", "v5", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("uuid.v5 expects 2 arguments: namespace (string), name (string)")
		}
		ns, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.v5: namespace must be a string")
		}
		name, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.v5: name must be a string")
		}
		nsUUID, err := googleuuid.Parse(ns.Value)
		if err != nil {
			return nil, fmt.Errorf("uuid.v5: invalid namespace UUID: %w", err)
		}
		u := googleuuid.NewSHA1(nsUUID, []byte(name.Value))
		return runtime.String{Value: u.String()}, nil
	})
	r.Register("uuid", "parse", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("uuid.parse expects 1 argument")
		}
		s, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.parse: argument must be a string")
		}
		u, err := googleuuid.Parse(s.Value)
		if err != nil {
			return nil, fmt.Errorf("uuid.parse: %w", err)
		}
		return runtime.String{Value: u.String()}, nil
	})
	r.Register("uuid", "isValid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("uuid.isValid expects 1 argument")
		}
		s, ok := args[0].(runtime.String)
		if !ok {
			return runtime.Bool{Value: false}, nil
		}
		return runtime.Bool{Value: googleuuid.Validate(s.Value) == nil}, nil
	})
	r.Register("uuid", "nil", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("uuid.nil expects no arguments")
		}
		return runtime.String{Value: "00000000-0000-0000-0000-000000000000"}, nil
	})
	r.Register("uuid", "toBytes", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("uuid.toBytes expects 1 argument")
		}
		s, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.toBytes: argument must be a string")
		}
		u, err := googleuuid.Parse(s.Value)
		if err != nil {
			return nil, fmt.Errorf("uuid.toBytes: %w", err)
		}
		return runtime.Bytes{Value: u[:]}, nil
	})
	r.Register("uuid", "fromBytes", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("uuid.fromBytes expects 1 argument")
		}
		b, ok := args[0].(runtime.Bytes)
		if !ok {
			return nil, fmt.Errorf("uuid.fromBytes: argument must be bytes")
		}
		u, err := googleuuid.FromBytes(b.Value)
		if err != nil {
			return nil, fmt.Errorf("uuid.fromBytes: %w", err)
		}
		return runtime.String{Value: u.String()}, nil
	})
	r.Register("uuid", "namespaceDNS", func(args []runtime.Value) (runtime.Value, error) {
		return runtime.String{Value: googleuuid.NameSpaceDNS.String()}, nil
	})
	r.Register("uuid", "namespaceURL", func(args []runtime.Value) (runtime.Value, error) {
		return runtime.String{Value: googleuuid.NameSpaceURL.String()}, nil
	})
	r.Register("uuid", "namespaceOID", func(args []runtime.Value) (runtime.Value, error) {
		return runtime.String{Value: googleuuid.NameSpaceOID.String()}, nil
	})
	r.Register("uuid", "namespaceX500", func(args []runtime.Value) (runtime.Value, error) {
		return runtime.String{Value: googleuuid.NameSpaceX500.String()}, nil
	})
	r.Register("uuid", "ulid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("uuid.ulid expects no arguments")
		}
		var randBytes [10]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return nil, err
		}
		return runtime.String{Value: encodeULID(time.Now().UnixMilli(), randBytes)}, nil
	})
}

func formatUUID(data [16]byte) string {
	encoded := hex.EncodeToString(data[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func encodeULID(tsMs int64, r [10]byte) string {
	var b [26]byte
	// 10 chars encode 48-bit timestamp (5 bits each, MSB first)
	b[0] = ulidAlphabet[(tsMs>>45)&0x1F]
	b[1] = ulidAlphabet[(tsMs>>40)&0x1F]
	b[2] = ulidAlphabet[(tsMs>>35)&0x1F]
	b[3] = ulidAlphabet[(tsMs>>30)&0x1F]
	b[4] = ulidAlphabet[(tsMs>>25)&0x1F]
	b[5] = ulidAlphabet[(tsMs>>20)&0x1F]
	b[6] = ulidAlphabet[(tsMs>>15)&0x1F]
	b[7] = ulidAlphabet[(tsMs>>10)&0x1F]
	b[8] = ulidAlphabet[(tsMs>>5)&0x1F]
	b[9] = ulidAlphabet[tsMs&0x1F]
	// 16 chars encode 80-bit randomness (5 bits each, MSB first)
	b[10] = ulidAlphabet[r[0]>>3]
	b[11] = ulidAlphabet[((r[0]&0x07)<<2)|(r[1]>>6)]
	b[12] = ulidAlphabet[(r[1]>>1)&0x1F]
	b[13] = ulidAlphabet[((r[1]&0x01)<<4)|(r[2]>>4)]
	b[14] = ulidAlphabet[((r[2]&0x0F)<<1)|(r[3]>>7)]
	b[15] = ulidAlphabet[(r[3]>>2)&0x1F]
	b[16] = ulidAlphabet[((r[3]&0x03)<<3)|(r[4]>>5)]
	b[17] = ulidAlphabet[r[4]&0x1F]
	b[18] = ulidAlphabet[r[5]>>3]
	b[19] = ulidAlphabet[((r[5]&0x07)<<2)|(r[6]>>6)]
	b[20] = ulidAlphabet[(r[6]>>1)&0x1F]
	b[21] = ulidAlphabet[((r[6]&0x01)<<4)|(r[7]>>4)]
	b[22] = ulidAlphabet[((r[7]&0x0F)<<1)|(r[8]>>7)]
	b[23] = ulidAlphabet[(r[8]>>2)&0x1F]
	b[24] = ulidAlphabet[((r[8]&0x03)<<3)|(r[9]>>5)]
	b[25] = ulidAlphabet[r[9]&0x1F]
	return string(b[:])
}
