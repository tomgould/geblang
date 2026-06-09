package bytecode

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"geblang/internal/modules"
	"geblang/internal/native"
)

func stdlibDirForDispatchGuard() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, "stdlib")
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// nativeWinsNoSourceExports are root dual-name modules that resolve to the
// native module on import; their stdlib .gb source is a reference-only class
// reachable solely by direct file import, so the VM never loads the source
// and there are no source exports to diverge on.
var nativeWinsNoSourceExports = map[string]bool{}

// TestDualNameRootModulesRouteToSource guards the dual-name dispatch trap:
// a root module that is both bytecode-callable and has resolvable stdlib
// source must either be routed to its source on the VM (isDualNameSourceModule)
// or be an explicit native-wins exception. Otherwise the VM treats it purely
// as native and its source exports become unreachable, diverging from the
// evaluator.
func TestDualNameRootModulesRouteToSource(t *testing.T) {
	stdlibDir := stdlibDirForDispatchGuard()
	if stdlibDir == "" {
		t.Skip("stdlib directory not found")
	}
	resolver := modules.NewResolver([]string{stdlibDir})
	offenders := []string{}
	for module := range native.NativeModuleNames {
		if strings.Contains(module, ".") {
			continue // root modules only
		}
		if !isBytecodeCallableModule(module) {
			continue // not native-dispatched on the VM; VM loads source
		}
		if _, err := resolver.Resolve(module); err != nil {
			continue // native-only: no stdlib source to reach
		}
		if isDualNameSourceModule(module) || nativeWinsNoSourceExports[module] {
			continue
		}
		offenders = append(offenders, module)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("root dual-name modules are bytecode-callable with resolvable stdlib source but neither routed to source (isDualNameSourceModule) nor listed as native-wins: %s", strings.Join(offenders, ", "))
	}
}
