package evaluator

import (
	"fmt"
	"math/big"
	"net/netip"

	"geblang/internal/ast"
	"geblang/internal/runtime"
)

func netParseIP(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one argument", call.Callee.String())
	}
	s, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s argument must be string", call.Callee.String())
	}
	addr, err := netip.ParseAddr(s.Value)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid IP %q", call.Callee.String(), s.Value)
	}
	return ipDict(addr), nil
}

func netParseCIDR(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one argument", call.Callee.String())
	}
	s, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s argument must be string", call.Callee.String())
	}
	prefix, err := netip.ParsePrefix(s.Value)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid CIDR %q", call.Callee.String(), s.Value)
	}
	return cidrDict(prefix), nil
}

func netCIDRContains(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects (cidr, ip)", call.Callee.String())
	}
	cidrStr, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s first argument must be string", call.Callee.String())
	}
	ipStr, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s second argument must be string", call.Callee.String())
	}
	prefix, err := netip.ParsePrefix(cidrStr.Value)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid CIDR %q", call.Callee.String(), cidrStr.Value)
	}
	addr, err := netip.ParseAddr(ipStr.Value)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid IP %q", call.Callee.String(), ipStr.Value)
	}
	return runtime.Bool{Value: prefix.Contains(addr)}, nil
}

func netCIDRRange(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one argument", call.Callee.String())
	}
	s, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s argument must be string", call.Callee.String())
	}
	prefix, err := netip.ParsePrefix(s.Value)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid CIDR %q", call.Callee.String(), s.Value)
	}
	first, last := cidrEndpoints(prefix)
	count := prefixHostCount(prefix)
	d := runtime.NewDictHint(3)
	putStringEntry(&d, "first", first.String())
	putStringEntry(&d, "last", last.String())
	putValueEntry(&d, "count", countValue(count))
	return d, nil
}

func netIsIPv4(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	s, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return runtime.Bool{Value: false}, nil
	}
	return runtime.Bool{Value: addr.Is4()}, nil
}

func netIsIPv6(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	s, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return runtime.Bool{Value: false}, nil
	}
	return runtime.Bool{Value: addr.Is6() && !addr.Is4In6()}, nil
}

func netIPToBytes(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	s, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid IP %q", call.Callee.String(), s)
	}
	bs := addr.AsSlice()
	out := make([]byte, len(bs))
	copy(out, bs)
	return runtime.Bytes{Value: out}, nil
}

func netIPFromBytes(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one argument", call.Callee.String())
	}
	b, ok := args[0].(runtime.Bytes)
	if !ok {
		return nil, fmt.Errorf("%s argument must be bytes", call.Callee.String())
	}
	switch len(b.Value) {
	case 4, 16:
		addr, ok := netip.AddrFromSlice(b.Value)
		if !ok {
			return nil, fmt.Errorf("%s: bytes did not decode to an IP", call.Callee.String())
		}
		return runtime.String{Value: addr.String()}, nil
	default:
		return nil, fmt.Errorf("%s: IP byte length must be 4 or 16, got %d", call.Callee.String(), len(b.Value))
	}
}

func ipDict(addr netip.Addr) runtime.Value {
	d := runtime.NewDictHint(3)
	putIntEntry(&d, "version", ipVersion(addr))
	putStringEntry(&d, "address", addr.String())
	bs := addr.AsSlice()
	bytesCopy := make([]byte, len(bs))
	copy(bytesCopy, bs)
	putValueEntry(&d, "bytes", runtime.Bytes{Value: bytesCopy})
	return d
}

func cidrDict(prefix netip.Prefix) runtime.Value {
	first, last := cidrEndpoints(prefix)
	count := prefixHostCount(prefix)
	d := runtime.NewDictHint(5)
	putStringEntry(&d, "network", prefix.Masked().Addr().String())
	putIntEntry(&d, "prefixLen", int64(prefix.Bits()))
	putIntEntry(&d, "version", ipVersion(prefix.Addr()))
	putStringEntry(&d, "first", first.String())
	putStringEntry(&d, "last", last.String())
	putValueEntry(&d, "count", countValue(count))
	return d
}

func ipVersion(addr netip.Addr) int64 {
	if addr.Is4() {
		return 4
	}
	return 6
}

// cidrEndpoints returns the first and last addresses inside the
// prefix. The returned values are inclusive.
func cidrEndpoints(prefix netip.Prefix) (netip.Addr, netip.Addr) {
	first := prefix.Masked().Addr()
	bits := prefix.Bits()
	width := first.BitLen()
	hostBits := width - bits
	if hostBits <= 0 {
		return first, first
	}
	bs := first.AsSlice()
	// Set the host portion to all-ones to derive the broadcast / last addr.
	for i := width - 1; i >= bits; i-- {
		byteIdx := i / 8
		bitInByte := uint(7 - (i % 8))
		bs[byteIdx] |= 1 << bitInByte
	}
	last, _ := netip.AddrFromSlice(bs)
	if first.Is4() {
		last = last.Unmap()
	}
	return first, last
}

// prefixHostCount returns the number of addresses in the prefix as a
// *big.Int. IPv6 ranges easily overflow int64.
func prefixHostCount(prefix netip.Prefix) *big.Int {
	bits := prefix.Bits()
	width := prefix.Addr().BitLen()
	hostBits := width - bits
	if hostBits < 0 {
		hostBits = 0
	}
	return new(big.Int).Lsh(big.NewInt(1), uint(hostBits))
}

// countValue picks the runtime numeric type that fits. Small counts
// stay as SmallInt for cheap display; otherwise we lift to Int.
func countValue(count *big.Int) runtime.Value {
	if count.IsInt64() {
		return runtime.SmallInt{Value: count.Int64()}
	}
	return runtime.Int{Value: count}
}

func putStringEntry(d *runtime.Dict, key, value string) {
	k := runtime.String{Value: key}
	d.PutEntry(dictKey(k), runtime.DictEntry{Key: k, Value: runtime.String{Value: value}})
}

func putIntEntry(d *runtime.Dict, key string, value int64) {
	k := runtime.String{Value: key}
	d.PutEntry(dictKey(k), runtime.DictEntry{Key: k, Value: runtime.SmallInt{Value: value}})
}

func putValueEntry(d *runtime.Dict, key string, value runtime.Value) {
	k := runtime.String{Value: key}
	d.PutEntry(dictKey(k), runtime.DictEntry{Key: k, Value: value})
}
