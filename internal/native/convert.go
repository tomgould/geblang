package native

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"geblang/internal/runtime"

	tomllib "github.com/BurntSushi/toml"
	yamllib "gopkg.in/yaml.v3"
)

// InstanceInvokerFunc is the callback shape each backend installs so
// native code (convert.go etc.) can dispatch a method on a class
// instance. False return means the class has no such method.
type InstanceInvokerFunc func(instance *runtime.Instance, method string, args []runtime.Value) (runtime.Value, bool, error)

// ClassDeserializerFunc is the callback shape each backend installs
// to turn a parsed value into a class instance.
type ClassDeserializerFunc func(class runtime.Value, value runtime.Value) (runtime.Value, error)

type instanceInvokerCell struct{ fn InstanceInvokerFunc }
type classDeserializerCell struct{ fn ClassDeserializerFunc }

var (
	instanceInvokerPtr   atomic.Pointer[instanceInvokerCell]
	classDeserializerPtr atomic.Pointer[classDeserializerCell]
)

// SetInstanceInvoker installs the active backend's instance invoker.
// Safe to call concurrently with other backends starting up.
func SetInstanceInvoker(fn InstanceInvokerFunc) {
	instanceInvokerPtr.Store(&instanceInvokerCell{fn: fn})
}

// GetInstanceInvoker returns the currently installed invoker, or nil.
func GetInstanceInvoker() InstanceInvokerFunc {
	if c := instanceInvokerPtr.Load(); c != nil {
		return c.fn
	}
	return nil
}

// SetClassDeserializer installs the active backend's class deserializer.
func SetClassDeserializer(fn ClassDeserializerFunc) {
	classDeserializerPtr.Store(&classDeserializerCell{fn: fn})
}

// GetClassDeserializer returns the currently installed deserializer, or nil.
func GetClassDeserializer() ClassDeserializerFunc {
	if c := classDeserializerPtr.Load(); c != nil {
		return c.fn
	}
	return nil
}

func invokeInstanceSerialize(instance *runtime.Instance) (runtime.Value, bool, error) {
	fn := GetInstanceInvoker()
	if fn == nil {
		return nil, false, nil
	}
	if result, ok, err := fn(instance, "__serialize", nil); ok || err != nil {
		return result, ok, err
	}
	return fn(instance, "__serialize__", nil)
}

// instanceToSerializable converts a class instance to a
// dict-like map suitable for downstream stringify converters.
// Prefers __serialize__ when defined, else dumps "public" fields
// (those not prefixed with "_" or "__"). The recurse callback
// converts each contained value via the caller's format-specific
// converter (ValueToJSON, ValueToTOML, etc.).
func instanceToSerializable(instance *runtime.Instance, recurse func(runtime.Value) (any, error)) (any, error) {
	if instance == nil {
		return nil, nil
	}
	if value, called, err := invokeInstanceSerialize(instance); err != nil {
		return nil, err
	} else if called {
		return recurse(value)
	}
	out := map[string]any{}
	names := make([]string, 0, len(instance.Fields))
	for name := range instance.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, "_") {
			continue
		}
		converted, err := recurse(instance.Fields[name])
		if err != nil {
			return nil, err
		}
		out[name] = converted
	}
	return out, nil
}

// DictKey returns the canonical map key string for a runtime.Value used as a dict key.
// DictKey single-byte type prefixes. Distinct type tags so different
// runtime values never produce the same map-key string. Short
// prefixes (1 byte vs the previous 7-byte "string:" form) cut per-key
// allocation cost and shrink the live byte count walked by the map's
// hash function on every dict operation.
const (
	dictKeyPrefixNull    = "n"
	dictKeyPrefixBoolT   = "b1"
	dictKeyPrefixBoolF   = "b0"
	dictKeyPrefixInt     = "i"
	dictKeyPrefixDecimal = "d"
	dictKeyPrefixFloat   = "f"
	dictKeyPrefixString  = "s"
	dictKeyPrefixBytes   = "y"
	dictKeyPrefixList    = "L"
	dictKeyPrefixSet     = "S"
	dictKeyPrefixDict    = "D"
	dictKeyPrefixRange   = "r"
)

func DictKey(value runtime.Value) string {
	switch value := value.(type) {
	case runtime.Null:
		return dictKeyPrefixNull
	case runtime.Bool:
		if value.Value {
			return dictKeyPrefixBoolT
		}
		return dictKeyPrefixBoolF
	case runtime.SmallInt:
		return dictKeyPrefixInt + strconv.FormatInt(value.Value, 10)
	case runtime.Int:
		return dictKeyPrefixInt + value.Value.String()
	case runtime.Decimal:
		return dictKeyPrefixDecimal + value.Value.RatString()
	case runtime.Float:
		floatValue := value.Value
		if floatValue == 0 {
			floatValue = 0
		}
		return dictKeyPrefixFloat + strconv.FormatFloat(floatValue, 'g', -1, 64)
	case runtime.String:
		// Dict-key uniqueness relies on the single-byte type prefix
		// to keep distinct types from colliding (e.g. int 5 vs
		// string "5"). The previous form prepended "string:" which
		// is 6 bytes longer; shortening drops alloc + hash cost on
		// every dict insert. The prefix never appears at the start
		// of any other type's key because each type uses a distinct
		// leading byte.
		return dictKeyPrefixString + value.Value
	case runtime.Bytes:
		return dictKeyPrefixBytes + hex.EncodeToString(value.Value)
	case *runtime.List:
		parts := make([]string, 0, len(value.Elements))
		for _, element := range value.Elements {
			parts = append(parts, DictKey(element))
		}
		return dictKeyPrefixList + "[" + strings.Join(parts, ",") + "]"
	case runtime.Set:
		parts := make([]string, 0, len(value.Elements))
		for key := range value.Elements {
			parts = append(parts, key)
		}
		sort.Strings(parts)
		return dictKeyPrefixSet + "{" + strings.Join(parts, ",") + "}"
	case runtime.Dict:
		type kv struct{ k, v string }
		pairs := make([]kv, 0, len(value.Entries))
		for k, entry := range value.Entries {
			pairs = append(pairs, kv{k, DictKey(entry.Value)})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })
		parts := make([]string, len(pairs))
		for i, p := range pairs {
			parts[i] = p.k + "=" + p.v
		}
		return dictKeyPrefixDict + "{" + strings.Join(parts, ",") + "}"
	case runtime.Range:
		return dictKeyPrefixRange + value.Start.String() + ":" + value.End.String() + ":" + value.Step.String() + ":" + strconv.FormatBool(value.Exclusive)
	case runtime.BytecodeFunction:
		return fmt.Sprintf("bytecode-func:%s:%s:%d", value.Module, value.Name, value.Index)
	case runtime.BytecodeClosure:
		return fmt.Sprintf("bytecode-closure:%s:%d", value.Name, value.FunctionIndex)
	case runtime.BytecodeClass:
		return fmt.Sprintf("bytecode-class:%s:%s:%d", value.Module, value.Name, value.Index)
	case runtime.NativeObject:
		return fmt.Sprintf("native:%s:%d", value.Kind, value.ID)
	case runtime.Error:
		return "error:" + strconv.Quote(value.Class) + ":" + strconv.Quote(value.Message)
	case runtime.Type:
		return "type:" + strconv.Quote(value.Name)
	case *runtime.Module:
		return fmt.Sprintf("module:%p", value)
	case *runtime.Class:
		return fmt.Sprintf("class:%p", value)
	case *runtime.Interface:
		return fmt.Sprintf("interface:%p", value)
	case *runtime.Instance:
		return fmt.Sprintf("instance:%p", value)
	default:
		return fmt.Sprintf("%T:%p", value, &value)
	}
}

// NativeToValue converts a plain Go value (from JSON/TOML/YAML unmarshal) to a runtime.Value.
func NativeToValue(value any) (runtime.Value, error) {
	switch value := value.(type) {
	case nil:
		return runtime.Null{}, nil
	case bool:
		return runtime.Bool{Value: value}, nil
	case int:
		return runtime.NewInt64(int64(value)), nil
	case int64:
		return runtime.NewInt64(value), nil
	case float64:
		return runtime.Float{Value: value}, nil
	case string:
		return runtime.String{Value: value}, nil
	case []byte:
		return runtime.Bytes{Value: value}, nil
	case time.Time:
		return runtime.String{Value: value.UTC().Format(time.RFC3339Nano)}, nil
	case []any:
		elements := make([]runtime.Value, 0, len(value))
		for _, item := range value {
			converted, err := NativeToValue(item)
			if err != nil {
				return nil, err
			}
			elements = append(elements, converted)
		}
		return &runtime.List{Elements: elements}, nil
	case map[string]any:
		d := runtime.NewDict()
		for key, item := range value {
			converted, err := NativeToValue(item)
			if err != nil {
				return nil, err
			}
			keyValue := runtime.String{Value: key}
			d.PutEntry(DictKey(keyValue), runtime.DictEntry{Key: keyValue, Value: converted})
		}
		return d, nil
	case map[any]any:
		d := runtime.NewDict()
		for key, item := range value {
			keyText, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("only string map keys are supported")
			}
			converted, err := NativeToValue(item)
			if err != nil {
				return nil, err
			}
			keyValue := runtime.String{Value: keyText}
			d.PutEntry(DictKey(keyValue), runtime.DictEntry{Key: keyValue, Value: converted})
		}
		return d, nil
	default:
		return nil, fmt.Errorf("unsupported native value %T", value)
	}
}

// JSONToValue converts a value decoded by encoding/json (with UseNumber) to a runtime.Value.
func JSONToValue(value any) (runtime.Value, error) {
	switch value := value.(type) {
	case nil:
		return runtime.Null{}, nil
	case bool:
		return runtime.Bool{Value: value}, nil
	case string:
		return runtime.String{Value: value}, nil
	case json.Number:
		text := value.String()
		if strings.ContainsAny(text, ".eE") {
			return runtime.NewDecimalLiteral(text)
		}
		return runtime.NewIntLiteral(text)
	case []any:
		elements := make([]runtime.Value, 0, len(value))
		for _, item := range value {
			converted, err := JSONToValue(item)
			if err != nil {
				return nil, err
			}
			elements = append(elements, converted)
		}
		return &runtime.List{Elements: elements}, nil
	case map[string]any:
		entries := make(map[string]runtime.DictEntry, len(value))
		for key, item := range value {
			converted, err := JSONToValue(item)
			if err != nil {
				return nil, err
			}
			keyValue := runtime.String{Value: key}
			entries[DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: converted}
		}
		return runtime.Dict{Entries: entries}, nil
	default:
		return nil, fmt.Errorf("unsupported JSON value %T", value)
	}
}

// EncodeJSONValue serialises a runtime.Value as a JSON string directly,
// without the runtime.Value -> any -> json.Encoder intermediate of
// ValueToJSON. Skips the map[string]any allocations json.stringify
// otherwise pays per dict (heavy on json_roundtrip). The output
// buffer comes from a sync.Pool so successive calls re-use the
// underlying byte slab instead of every call paying for a fresh
// allocation + grow cycle.
func EncodeJSONValue(value runtime.Value) (string, error) {
	buf := jsonEncodeBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	if err := encodeJSONValueInto(buf, value); err != nil {
		jsonEncodeBufPool.Put(buf)
		return "", err
	}
	out := buf.String()
	jsonEncodeBufPool.Put(buf)
	return out, nil
}

var jsonEncodeBufPool = sync.Pool{
	New: func() any {
		buf := &bytes.Buffer{}
		buf.Grow(4096)
		return buf
	},
}

// jsonDictPairsPool reuses the per-dict sort scratch slice the JSON
// encoder builds when serialising a runtime.Dict. Each Get returns a
// fresh-or-recycled slice; the encoder Puts it back on exit (including
// on error paths) so concurrent encodes don't share the same buffer.
// Recursion inside an outer dict gets its own slice because the
// outer's hasn't been Put yet.
var jsonDictPairsPool = sync.Pool{
	New: func() any {
		p := make(jsonDictPairs, 0, 16)
		return &p
	},
}

func encodeJSONValueInto(buf *bytes.Buffer, value runtime.Value) error {
	switch v := value.(type) {
	case runtime.Null:
		buf.WriteString("null")
		return nil
	case runtime.Bool:
		if v.Value {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case runtime.SmallInt:
		buf.WriteString(strconv.FormatInt(v.Value, 10))
		return nil
	case runtime.Int:
		if v.Value.IsInt64() {
			buf.WriteString(strconv.FormatInt(v.Value.Int64(), 10))
			return nil
		}
		// Bigints larger than int64 are encoded as strings to round-trip
		// through downstream decoders that can't represent them as Number.
		buf.WriteByte('"')
		buf.WriteString(v.Value.String())
		buf.WriteByte('"')
		return nil
	case runtime.Decimal:
		buf.WriteString(v.Value.FloatString(10))
		return nil
	case runtime.Float:
		if math.IsNaN(v.Value) || math.IsInf(v.Value, 0) {
			return fmt.Errorf("json: unsupported value: %g", v.Value)
		}
		var scratch [32]byte
		out := strconv.AppendFloat(scratch[:0], v.Value, 'g', -1, 64)
		buf.Write(out)
		return nil
	case runtime.String:
		return encodeJSONString(buf, v.Value)
	case runtime.Bytes:
		buf.WriteByte('"')
		buf.WriteString(base64.StdEncoding.EncodeToString(v.Value))
		buf.WriteByte('"')
		return nil
	case *runtime.List:
		buf.WriteByte('[')
		for i, item := range v.Elements {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeJSONValueInto(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case runtime.Dict:
		buf.WriteByte('{')
		// Sort entries so output is deterministic. Borrow a
		// pre-allocated jsonDictPairs slice from the pool so the
		// per-dict allocation cost (800x records x 200 iterations
		// on the bench) goes through reuse rather than mallocgc.
		n := len(v.Entries)
		if n == 0 {
			buf.WriteByte('}')
			return nil
		}
		pairsPtr := jsonDictPairsPool.Get().(*jsonDictPairs)
		pairs := *pairsPtr
		pairs = pairs[:0]
		if cap(pairs) < n {
			pairs = make(jsonDictPairs, 0, n)
		}
		for _, entry := range v.Entries {
			key, ok := entry.Key.(runtime.String)
			if !ok {
				*pairsPtr = pairs[:0]
				jsonDictPairsPool.Put(pairsPtr)
				return fmt.Errorf("json.stringify only supports dicts with string keys")
			}
			pairs = append(pairs, jsonDictPair{key: key.Value, value: entry.Value})
		}
		sort.Sort(pairs)
		for i, p := range pairs {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeJSONString(buf, p.key); err != nil {
				*pairsPtr = pairs[:0]
				jsonDictPairsPool.Put(pairsPtr)
				return err
			}
			buf.WriteByte(':')
			if err := encodeJSONValueInto(buf, p.value); err != nil {
				*pairsPtr = pairs[:0]
				jsonDictPairsPool.Put(pairsPtr)
				return err
			}
		}
		*pairsPtr = pairs[:0]
		jsonDictPairsPool.Put(pairsPtr)
		buf.WriteByte('}')
		return nil
	case *runtime.Instance:
		converted, err := instanceToSerializable(v, ValueToJSON)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(converted)
		if err != nil {
			return err
		}
		buf.Write(encoded)
		return nil
	default:
		return fmt.Errorf("json.stringify does not support %s", v.TypeName())
	}
}

// encodeJSONString writes a JSON-quoted string directly into buf.
// Fast path: strings with no escape-required bytes (control chars,
// quote, backslash) emit as a single buf.Grow + raw copy. Bytes
// >= 0x80 pass through verbatim - RFC 8259 §3 specifies UTF-8 as
// the default JSON encoding, so valid multi-byte runes do not need
// escaping. Slow path walks byte-by-byte emitting the RFC-mandated
// escapes (" and \\ always, control chars 0x00-0x1F via standard
// short escapes or \uXXXX). Skips the HTML / JS-line-separator
// escapes that encoding/json emits by default (those are JS-embed
// safety, not JSON spec; matches Geblang's existing fast-path
// behaviour for ASCII chars like < > &).
func encodeJSONString(buf *bytes.Buffer, s string) error {
	if jsonStringIsSafe(s) {
		buf.Grow(len(s) + 2)
		buf.WriteByte('"')
		buf.WriteString(s)
		buf.WriteByte('"')
		return nil
	}
	buf.Grow(len(s) + 4)
	buf.WriteByte('"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		if start < i {
			buf.WriteString(s[start:i])
		}
		switch c {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			buf.WriteString(`\u00`)
			buf.WriteByte(jsonHexChars[c>>4])
			buf.WriteByte(jsonHexChars[c&0x0F])
		}
		start = i + 1
	}
	if start < len(s) {
		buf.WriteString(s[start:])
	}
	buf.WriteByte('"')
	return nil
}

const jsonHexChars = "0123456789abcdef"

// jsonDictPair / jsonDictPairs back the typed sort path the JSON
// encoder uses to alphabetise dict keys without paying for
// sort.Slice's reflect-based comparator on every entry.
type jsonDictPair struct {
	key   string
	value runtime.Value
}

type jsonDictPairs []jsonDictPair

func (p jsonDictPairs) Len() int           { return len(p) }
func (p jsonDictPairs) Less(i, j int) bool { return p[i].key < p[j].key }
func (p jsonDictPairs) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// jsonStringIsSafe reports whether s can be JSON-encoded by surrounding
// it with quotes verbatim - no escape sequences required. Returns
// false for any byte < 0x20, `"`, or `\\`. Non-ASCII bytes (>= 0x80)
// pass through (raw UTF-8); the caller relies on Geblang strings
// being valid UTF-8.
func jsonStringIsSafe(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

// ValueToJSON converts a runtime.Value to a plain Go value suitable for JSON encoding.
func ValueToJSON(value runtime.Value) (any, error) {
	switch value := value.(type) {
	case runtime.Null:
		return nil, nil
	case runtime.Bool:
		return value.Value, nil
	case runtime.SmallInt:
		return value.Value, nil
	case runtime.Int:
		if value.Value.IsInt64() {
			return value.Value.Int64(), nil
		}
		return value.Value.String(), nil
	case runtime.Decimal:
		return json.Number(value.Value.FloatString(10)), nil
	case runtime.Float:
		return value.Value, nil
	case runtime.String:
		return value.Value, nil
	case runtime.Bytes:
		return base64.StdEncoding.EncodeToString(value.Value), nil
	case *runtime.List:
		items := make([]any, 0, len(value.Elements))
		for _, item := range value.Elements {
			converted, err := ValueToJSON(item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	case runtime.Dict:
		items := map[string]any{}
		for _, entry := range value.Entries {
			key, ok := entry.Key.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("json.stringify only supports dicts with string keys")
			}
			converted, err := ValueToJSON(entry.Value)
			if err != nil {
				return nil, err
			}
			items[key.Value] = converted
		}
		return items, nil
	case *runtime.Instance:
		return instanceToSerializable(value, ValueToJSON)
	default:
		return nil, fmt.Errorf("json.stringify does not support %s", value.TypeName())
	}
}

// ValueToTemplateData converts a runtime.Value to data suitable for html/template.
func ValueToTemplateData(value runtime.Value) (any, error) {
	switch value := value.(type) {
	case runtime.Dict:
		items := map[string]any{}
		for _, entry := range value.Entries {
			key, ok := entry.Key.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("template data only supports dicts with string keys")
			}
			converted, err := ValueToTemplateData(entry.Value)
			if err != nil {
				return nil, err
			}
			items[key.Value] = converted
		}
		return items, nil
	case *runtime.List:
		items := make([]any, 0, len(value.Elements))
		for _, item := range value.Elements {
			converted, err := ValueToTemplateData(item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	default:
		return ValueToJSON(value)
	}
}

// ValueToTOML converts a runtime.Value to a plain Go value suitable for TOML encoding.
func ValueToTOML(value runtime.Value) (any, error) {
	switch value := value.(type) {
	case runtime.Null:
		return nil, fmt.Errorf("toml.stringify does not support null")
	case runtime.Bool:
		return value.Value, nil
	case runtime.SmallInt:
		return value.Value, nil
	case runtime.Int:
		if value.Value.IsInt64() {
			return value.Value.Int64(), nil
		}
		return value.Value.String(), nil
	case runtime.Decimal:
		return value.Value.FloatString(10), nil
	case runtime.Float:
		return value.Value, nil
	case runtime.String:
		return value.Value, nil
	case runtime.Bytes:
		return base64.StdEncoding.EncodeToString(value.Value), nil
	case *runtime.List:
		items := make([]any, 0, len(value.Elements))
		for _, item := range value.Elements {
			converted, err := ValueToTOML(item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	case runtime.Dict:
		items := map[string]any{}
		for _, entry := range value.Entries {
			key, ok := entry.Key.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("toml.stringify only supports dicts with string keys")
			}
			converted, err := ValueToTOML(entry.Value)
			if err != nil {
				return nil, err
			}
			items[key.Value] = converted
		}
		return items, nil
	case *runtime.Instance:
		return instanceToSerializable(value, ValueToTOML)
	default:
		return nil, fmt.Errorf("toml.stringify does not support %s", value.TypeName())
	}
}

// ValueToYAML converts a runtime.Value to a plain Go value suitable for YAML encoding.
func ValueToYAML(value runtime.Value) (any, error) {
	switch value := value.(type) {
	case runtime.Null:
		return nil, nil
	case runtime.Bool:
		return value.Value, nil
	case runtime.SmallInt:
		return value.Value, nil
	case runtime.Int:
		if value.Value.IsInt64() {
			return value.Value.Int64(), nil
		}
		return value.Value.String(), nil
	case runtime.Decimal:
		return value.Value.FloatString(10), nil
	case runtime.Float:
		return value.Value, nil
	case runtime.String:
		return value.Value, nil
	case runtime.Bytes:
		return base64.StdEncoding.EncodeToString(value.Value), nil
	case *runtime.List:
		items := make([]any, 0, len(value.Elements))
		for _, item := range value.Elements {
			converted, err := ValueToYAML(item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	case runtime.Dict:
		items := map[string]any{}
		for _, entry := range value.Entries {
			key, ok := entry.Key.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("yaml.stringify only supports dicts with string keys")
			}
			converted, err := ValueToYAML(entry.Value)
			if err != nil {
				return nil, err
			}
			items[key.Value] = converted
		}
		return items, nil
	case *runtime.Instance:
		return instanceToSerializable(value, ValueToYAML)
	default:
		return nil, fmt.Errorf("yaml.stringify does not support %s", value.TypeName())
	}
}

// ParseJSONText parses a JSON string into a runtime.Value.
func ParseJSONText(text string) (runtime.Value, *ParseError) {
	return parseJSONDirect(text)
}

// ParseTOMLText parses a TOML string into a runtime.Value.
func ParseTOMLText(text string) (runtime.Value, *ParseError) {
	var decoded map[string]any
	if err := tomllib.Unmarshal([]byte(text), &decoded); err != nil {
		parseErr := tomlParseError(err, text)
		return nil, &parseErr
	}
	value, err := NativeToValue(decoded)
	if err != nil {
		parseErr := NewParseError(err.Error(), text, -1)
		return nil, &parseErr
	}
	return value, nil
}

func tomlParseError(err error, text string) ParseError {
	var parseErr tomllib.ParseError
	if errors.As(err, &parseErr) {
		message := parseErr.Message
		if message == "" {
			message = parseErr.Error()
		}
		offset := int64(-1)
		if parseErr.Position.Start >= 0 {
			offset = int64(parseErr.Position.Start + 1)
		}
		result := NewParseError(message, text, offset)
		if parseErr.Position.Line > 0 {
			result.Line = int64(parseErr.Position.Line)
		}
		return result
	}
	return NewParseError(err.Error(), text, -1)
}

// ParseYAMLText parses a YAML string into a runtime.Value.
func ParseYAMLText(text string) (runtime.Value, *ParseError) {
	var root yamllib.Node
	if err := yamllib.Unmarshal([]byte(text), &root); err != nil {
		parseErr := yamlTextParseError(err, text)
		return nil, &parseErr
	}
	value, err := yamlNodeToValue(&root)
	if err != nil {
		parseErr := NewParseError(err.Error(), text, -1)
		return nil, &parseErr
	}
	return value, nil
}

// yamlNodeToValue walks a yaml.v3 Node tree preserving mapping order.
func yamlNodeToValue(node *yamllib.Node) (runtime.Value, error) {
	if node == nil {
		return runtime.Null{}, nil
	}
	switch node.Kind {
	case yamllib.DocumentNode:
		if len(node.Content) == 0 {
			return runtime.Null{}, nil
		}
		return yamlNodeToValue(node.Content[0])
	case yamllib.MappingNode:
		d := runtime.NewDict()
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			var keyText string
			if keyNode.Tag == "!!str" || keyNode.Tag == "" {
				keyText = keyNode.Value
			} else {
				keyText = keyNode.Value
			}
			keyValue := runtime.String{Value: keyText}
			child, err := yamlNodeToValue(valNode)
			if err != nil {
				return nil, err
			}
			d.PutEntry(DictKey(keyValue), runtime.DictEntry{Key: keyValue, Value: child})
		}
		return d, nil
	case yamllib.SequenceNode:
		elements := make([]runtime.Value, 0, len(node.Content))
		for _, child := range node.Content {
			converted, err := yamlNodeToValue(child)
			if err != nil {
				return nil, err
			}
			elements = append(elements, converted)
		}
		return &runtime.List{Elements: elements}, nil
	case yamllib.AliasNode:
		return yamlNodeToValue(node.Alias)
	case yamllib.ScalarNode:
		var decoded any
		if err := node.Decode(&decoded); err != nil {
			return nil, err
		}
		return NativeToValue(decoded)
	}
	return runtime.Null{}, nil
}

func yamlTextParseError(err error, text string) ParseError {
	parseErr := NewParseError(err.Error(), text, -1)
	if yamlErr, ok := err.(*yamllib.TypeError); ok && len(yamlErr.Errors) > 0 {
		parseErr.Message = strings.Join(yamlErr.Errors, "; ")
	}
	if line := ParseLineNumberFromMessage(parseErr.Message); line > 0 {
		parseErr.Line = line
	}
	return parseErr
}

// ParseLineNumberFromMessage extracts a line number from a go-yaml v3 error message.
// The library formats positional errors as "line N: ..."; update if that changes.
func ParseLineNumberFromMessage(message string) int64 {
	index := strings.Index(message, "line ")
	if index < 0 {
		return 0
	}
	start := index + len("line ")
	end := start
	for end < len(message) && message[end] >= '0' && message[end] <= '9' {
		end++
	}
	if end == start {
		return 0
	}
	line, err := strconv.ParseInt(message[start:end], 10, 64)
	if err != nil {
		return 0
	}
	return line
}
