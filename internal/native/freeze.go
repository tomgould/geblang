package native

import (
	"fmt"

	"geblang/internal/runtime"
)

func registerFreeze(r *Registry) {
	r.Register("freeze", "shallow", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("freeze.shallow expects one argument")
		}
		return runtime.ShallowFreeze(args[0]), nil
	})

	r.Register("freeze", "deep", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("freeze.deep expects one argument")
		}
		return runtime.DeepFreeze(args[0]), nil
	})

	r.Register("freeze", "isFrozen", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("freeze.isFrozen expects one argument")
		}
		return runtime.Bool{Value: runtime.IsFrozen(args[0])}, nil
	})
}
