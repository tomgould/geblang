package native

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"geblang/internal/runtime"
)

// DumpValue renders a value as a type-annotated debug string for the
// dump(value) builtin. Pure (operates on runtime.Value), so both the
// evaluator and the VM share this single implementation.
func DumpValue(value runtime.Value) string {
	switch value := value.(type) {
	case runtime.String:
		return "string(" + strconv.Quote(value.Value) + ")"
	case runtime.Bytes:
		return fmt.Sprintf("bytes(%q)", value.Value)
	case *runtime.List:
		parts := make([]string, 0, len(value.Elements))
		for _, element := range value.Elements {
			parts = append(parts, DumpValue(element))
		}
		return "list[" + strings.Join(parts, ", ") + "]"
	case runtime.Dict:
		parts := make([]string, 0, len(value.Entries))
		for _, entry := range value.Entries {
			parts = append(parts, DumpValue(entry.Key)+": "+DumpValue(entry.Value))
		}
		sort.Strings(parts)
		return "dict{" + strings.Join(parts, ", ") + "}"
	case runtime.Error:
		return value.Class + "(" + strconv.Quote(value.Message) + ")"
	case *runtime.Instance:
		parts := make([]string, 0, len(value.Fields))
		for name, field := range value.Fields {
			parts = append(parts, name+": "+DumpValue(field))
		}
		sort.Strings(parts)
		return value.Class.Name + "{" + strings.Join(parts, ", ") + "}"
	default:
		return value.TypeName() + "(" + value.Inspect() + ")"
	}
}
