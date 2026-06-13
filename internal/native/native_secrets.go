package native

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"geblang/internal/runtime"
	"math/big"
)

func registerSecrets(r *Registry) {
	r.Register("secrets", "randomBytes", func(args []runtime.Value) (runtime.Value, error) {
		data, err := secureRandomBytes(args, "secrets.randomBytes")
		if err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: data}, nil
	})
	r.Register("secrets", "randomInt", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("secrets.randomInt expects min and max")
		}
		minBig, ok := IntValueToBigInt(args[0])
		if !ok {
			return nil, fmt.Errorf("secrets.randomInt min must be int")
		}
		maxBig, ok := IntValueToBigInt(args[1])
		if !ok {
			return nil, fmt.Errorf("secrets.randomInt max must be int")
		}
		if minBig.Cmp(maxBig) > 0 {
			return nil, fmt.Errorf("secrets.randomInt min must be <= max")
		}
		width := new(big.Int).Sub(maxBig, minBig)
		width.Add(width, big.NewInt(1))
		offset, err := rand.Int(rand.Reader, width)
		if err != nil {
			return nil, err
		}
		result := offset.Add(offset, minBig)
		if result.IsInt64() {
			return runtime.SmallInt{Value: result.Int64()}, nil
		}
		return runtime.Int{Value: result}, nil
	})
	r.Register("secrets", "randomHex", func(args []runtime.Value) (runtime.Value, error) {
		data, err := secureRandomBytes(args, "secrets.randomHex")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: hex.EncodeToString(data)}, nil
	})
	r.Register("secrets", "randomBase64", func(args []runtime.Value) (runtime.Value, error) {
		data, err := secureRandomBytes(args, "secrets.randomBase64")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: base64.RawURLEncoding.EncodeToString(data)}, nil
	})
	r.Register("secrets", "constantTimeEqual", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("secrets.constantTimeEqual expects exactly two arguments")
		}
		left, err := secretComparableBytes(args[0])
		if err != nil {
			return nil, err
		}
		right, err := secretComparableBytes(args[1])
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: constantTimeEqual(left, right)}, nil
	})
}
