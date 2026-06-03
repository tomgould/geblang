package native

import (
	"fmt"

	"geblang/internal/runtime"
)

func registerClone(r *Registry) {
	r.Register("clone", "deep", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("clone.deep expects one argument")
		}
		return runtime.CloneValue(args[0]), nil
	})
}
