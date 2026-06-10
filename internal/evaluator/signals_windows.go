//go:build windows

package evaluator

import "os"

func platformSignalByName(name string) (os.Signal, bool) {
	return nil, false
}
