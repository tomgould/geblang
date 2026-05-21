package native

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

// invokeInstanceSerialize calls __serialize__ on the instance if
// the class defines it. Returns (result, true, nil) when invoked,
// (nil, false, nil) when the class has no hook, (nil, false, err)
// on error.
func invokeInstanceSerialize(instance *runtime.Instance) (runtime.Value, bool, error) {
	fn := GetInstanceInvoker()
	if fn == nil {
		return nil, false, nil
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
func DictKey(value runtime.Value) string {
	switch value := value.(type) {
	case runtime.Null:
		return "null"
	case runtime.Bool:
		if value.Value {
			return "bool:true"
		}
		return "bool:false"
	case runtime.SmallInt:
		return "int:" + strconv.FormatInt(value.Value, 10)
	case runtime.Int:
		return "int:" + value.Value.String()
	case runtime.Decimal:
		return "decimal:" + value.Value.RatString()
	case runtime.Float:
		floatValue := value.Value
		if floatValue == 0 {
			floatValue = 0
		}
		return "float:" + strconv.FormatFloat(floatValue, 'g', -1, 64)
	case runtime.String:
		return "string:" + strconv.Quote(value.Value)
	case runtime.Bytes:
		return "bytes:" + hex.EncodeToString(value.Value)
	case runtime.List:
		parts := make([]string, 0, len(value.Elements))
		for _, element := range value.Elements {
			parts = append(parts, DictKey(element))
		}
		return "list:[" + strings.Join(parts, ",") + "]"
	case runtime.Set:
		parts := make([]string, 0, len(value.Elements))
		for key := range value.Elements {
			parts = append(parts, key)
		}
		sort.Strings(parts)
		return "set:{" + strings.Join(parts, ",") + "}"
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
		return "dict:{" + strings.Join(parts, ",") + "}"
	case runtime.Range:
		return "range:" + value.Start.String() + ":" + value.End.String() + ":" + value.Step.String() + ":" + strconv.FormatBool(value.Exclusive)
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
		return runtime.List{Elements: elements}, nil
	case map[string]any:
		entries := map[string]runtime.DictEntry{}
		for key, item := range value {
			converted, err := NativeToValue(item)
			if err != nil {
				return nil, err
			}
			keyValue := runtime.String{Value: key}
			entries[DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: converted}
		}
		return runtime.Dict{Entries: entries}, nil
	case map[any]any:
		entries := map[string]runtime.DictEntry{}
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
			entries[DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: converted}
		}
		return runtime.Dict{Entries: entries}, nil
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
		return runtime.List{Elements: elements}, nil
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
// otherwise pays per dict (heavy on json_roundtrip).
func EncodeJSONValue(value runtime.Value) (string, error) {
	var buf bytes.Buffer
	if err := encodeJSONValueInto(&buf, value); err != nil {
		return "", err
	}
	return buf.String(), nil
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
		text, err := json.Marshal(v.Value)
		if err != nil {
			return err
		}
		buf.Write(text)
		return nil
	case runtime.String:
		return encodeJSONString(buf, v.Value)
	case runtime.Bytes:
		buf.WriteByte('"')
		buf.WriteString(base64.StdEncoding.EncodeToString(v.Value))
		buf.WriteByte('"')
		return nil
	case runtime.List:
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
		// Sort keys so output is deterministic (json.Marshal of a Go
		// map alphabetises by default; downstream code relies on it).
		keys := make([]string, 0, len(v.Entries))
		for k, entry := range v.Entries {
			if _, ok := entry.Key.(runtime.String); !ok {
				return fmt.Errorf("json.stringify only supports dicts with string keys")
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			entry := v.Entries[k]
			key := entry.Key.(runtime.String)
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeJSONString(buf, key.Value); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := encodeJSONValueInto(buf, entry.Value); err != nil {
				return err
			}
		}
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

func encodeJSONString(buf *bytes.Buffer, s string) error {
	encoded, err := json.Marshal(s)
	if err != nil {
		return err
	}
	buf.Write(encoded)
	return nil
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
	case runtime.List:
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
	case runtime.List:
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
	case runtime.List:
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
	case runtime.List:
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
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		parseErr := JSONParseError(err, text)
		return nil, &parseErr
	}
	value, err := JSONToValue(decoded)
	if err != nil {
		parseErr := NewParseError(err.Error(), text, -1)
		return nil, &parseErr
	}
	return value, nil
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
	var decoded any
	if err := yamllib.Unmarshal([]byte(text), &decoded); err != nil {
		parseErr := yamlTextParseError(err, text)
		return nil, &parseErr
	}
	value, err := NativeToValue(decoded)
	if err != nil {
		parseErr := NewParseError(err.Error(), text, -1)
		return nil, &parseErr
	}
	return value, nil
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
