package native

import (
	"fmt"

	"geblang/internal/runtime"
)

// GeneratorMethods is the canonical method list for dir/catalog guards.
var GeneratorMethods = []string{"next", "done", "close"}

// GeneratorMethod is the manual-stepping dispatcher shared by both backends.
func GeneratorMethod(g *runtime.Generator, name string, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("generator.%s expects no arguments", name)
	}
	switch name {
	case "next":
		value, ok, err := g.Next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return runtime.Null{}, nil
		}
		return value, nil
	case "done":
		done, err := g.PeekDone()
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: done}, nil
	case "close":
		g.Close()
		return runtime.Null{}, nil
	default:
		return nil, fmt.Errorf("generator has no method %s", name)
	}
}
