package native

import (
	"fmt"
	"strings"
	"sync"

	"geblang/internal/runtime"
)

var (
	stringBuilderMu      sync.Mutex
	stringBuilderHandles = map[int64]*strings.Builder{}
	stringBuilderNextID  int64
)

func sbLookup(arg runtime.Value, label string) (*strings.Builder, error) {
	obj, ok := arg.(runtime.NativeObject)
	if !ok || obj.Kind != "StringBuilder" {
		return nil, fmt.Errorf("%s expects a StringBuilder handle", label)
	}
	stringBuilderMu.Lock()
	defer stringBuilderMu.Unlock()
	b, ok := stringBuilderHandles[obj.ID]
	if !ok {
		return nil, fmt.Errorf("%s: unknown StringBuilder handle", label)
	}
	return b, nil
}

func registerStringBuilder(r *Registry) {
	r.Register("strbuilder", "new", func(args []runtime.Value) (runtime.Value, error) {
		var initial string
		switch len(args) {
		case 0:
		case 1:
			s, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("strbuilder.new initial value must be string")
			}
			initial = s.Value
		default:
			return nil, fmt.Errorf("strbuilder.new expects 0 or 1 arguments")
		}
		b := &strings.Builder{}
		if initial != "" {
			b.WriteString(initial)
		}
		stringBuilderMu.Lock()
		stringBuilderNextID++
		id := stringBuilderNextID
		stringBuilderHandles[id] = b
		stringBuilderMu.Unlock()
		return runtime.NativeObject{Kind: "StringBuilder", ID: id}, nil
	})
	r.Register("strbuilder", "append", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("strbuilder.append expects 2 arguments")
		}
		b, err := sbLookup(args[0], "strbuilder.append")
		if err != nil {
			return nil, err
		}
		s, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("strbuilder.append value must be string")
		}
		b.WriteString(s.Value)
		return args[0], nil
	})
	r.Register("strbuilder", "appendLine", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("strbuilder.appendLine expects 2 arguments")
		}
		b, err := sbLookup(args[0], "strbuilder.appendLine")
		if err != nil {
			return nil, err
		}
		s, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("strbuilder.appendLine value must be string")
		}
		b.WriteString(s.Value)
		b.WriteByte('\n')
		return args[0], nil
	})
	r.Register("strbuilder", "build", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("strbuilder.build expects 1 argument")
		}
		b, err := sbLookup(args[0], "strbuilder.build")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: b.String()}, nil
	})
	r.Register("strbuilder", "length", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("strbuilder.length expects 1 argument")
		}
		b, err := sbLookup(args[0], "strbuilder.length")
		if err != nil {
			return nil, err
		}
		return runtime.NewInt64(int64(b.Len())), nil
	})
	r.Register("strbuilder", "clear", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("strbuilder.clear expects 1 argument")
		}
		b, err := sbLookup(args[0], "strbuilder.clear")
		if err != nil {
			return nil, err
		}
		b.Reset()
		return args[0], nil
	})
	r.Register("strbuilder", "dispose", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("strbuilder.dispose expects 1 argument")
		}
		obj, ok := args[0].(runtime.NativeObject)
		if !ok || obj.Kind != "StringBuilder" {
			return nil, fmt.Errorf("strbuilder.dispose expects a StringBuilder handle")
		}
		stringBuilderMu.Lock()
		delete(stringBuilderHandles, obj.ID)
		stringBuilderMu.Unlock()
		return runtime.Null{}, nil
	})
}
