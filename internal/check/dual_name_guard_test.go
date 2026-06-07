package check

import (
	"os"
	"path/filepath"
	"testing"

	"geblang/internal/evaluator"
	"geblang/internal/modules"
)

func stdlibDirForGuard() string {
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

// TestDualNameModulesNoMemberOverlap guards the native/stdlib dual-name
// shadow trap (project_known_divergences #12): when a stdlib module shares
// a name with a native module and re-exports a member that also exists on
// the native surface, external `X.member` resolves to native on the
// evaluator but stdlib on the VM. Give the native primitives a distinct
// *native module instead (the ffinative/procnative/sshnative convention).
func TestDualNameModulesNoMemberOverlap(t *testing.T) {
	stdlibDir := stdlibDirForGuard()
	if stdlibDir == "" {
		t.Skip("stdlib directory not found")
	}
	resolver := modules.NewResolver([]string{stdlibDir})
	cache := NewModuleCache()
	for module, nativeMembers := range evaluator.NativeModuleSymbols() {
		if len(nativeMembers) == 0 {
			continue
		}
		path, err := resolver.Resolve(module)
		if err != nil {
			continue // native-only module: no dual-name source
		}
		_, sourceExports, err := cache.load(path)
		if err != nil {
			continue
		}
		for name := range sourceExports {
			if _, clash := nativeMembers[name]; clash {
				t.Errorf("dual-name module %q re-exports %q, which also exists on the native surface; this resolves to native on the evaluator but stdlib on the VM. Give the native primitives a distinct *native module (see ffinative/procnative/sshnative).", module, name)
			}
		}
	}
}
