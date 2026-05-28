package ffi

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyNilDeniesEverything(t *testing.T) {
	var p *Policy
	err := p.Allow("/usr/lib/libm.so.6")
	if err == nil {
		t.Fatalf("nil policy should deny")
	}
	var pe *PolicyError
	if !errors.As(err, &pe) || pe.Reason != ReasonDisabled {
		t.Errorf("expected PolicyError ReasonDisabled, got %v", err)
	}
}

func TestPolicyDisabledDeniesEverything(t *testing.T) {
	p := &Policy{Enabled: false, Entries: []PolicyEntry{{Path: "/usr/lib/libm.so.6"}}}
	if err := p.Allow("/usr/lib/libm.so.6"); err == nil {
		t.Fatalf("disabled policy should deny even allow-listed paths")
	}
}

func TestPolicyExactPathAllowed(t *testing.T) {
	p := &Policy{Enabled: true, Entries: []PolicyEntry{{Path: "/usr/lib/libm.so.6"}}}
	if err := p.Allow("/usr/lib/libm.so.6"); err != nil {
		t.Errorf("expected allow, got %v", err)
	}
}

func TestPolicyExactPathDeniedWhenNotListed(t *testing.T) {
	p := &Policy{Enabled: true, Entries: []PolicyEntry{{Path: "/usr/lib/libm.so.6"}}}
	err := p.Allow("/etc/shadow.so")
	if err == nil {
		t.Fatalf("path not in allow-list should be denied")
	}
	var pe *PolicyError
	if !errors.As(err, &pe) || pe.Reason != ReasonNotAllowed {
		t.Errorf("expected ReasonNotAllowed, got %v", err)
	}
	if !strings.Contains(err.Error(), "--allow-ffi") {
		t.Errorf("error should suggest --allow-ffi: %v", err)
	}
}

func TestPolicyGlobAllowed(t *testing.T) {
	p := &Policy{Enabled: true, Entries: []PolicyEntry{{Glob: "/usr/lib/libm.so.*"}}}
	if err := p.Allow("/usr/lib/libm.so.6"); err != nil {
		t.Errorf("expected glob match, got %v", err)
	}
}

func TestPolicyGlobDenied(t *testing.T) {
	p := &Policy{Enabled: true, Entries: []PolicyEntry{{Glob: "/usr/lib/libm.so.*"}}}
	if err := p.Allow("/usr/lib/libcurl.so.4"); err == nil {
		t.Fatalf("non-matching glob should deny")
	}
}

func TestPolicyShortLibraryNameMatchesGlob(t *testing.T) {
	// `libm.so.6` with no path prefix is what users will typically
	// pass to ffi.dlopen so dlopen's runtime search picks it up.
	// The unanchored glob must match.
	p := &Policy{Enabled: true, Entries: []PolicyEntry{{Glob: "libm.so.*"}}}
	if err := p.Allow("libm.so.6"); err != nil {
		t.Errorf("expected unanchored glob match, got %v", err)
	}
}

func TestPolicyRelativePathAnchoredToProjectRoot(t *testing.T) {
	root := t.TempDir()
	p := &Policy{
		Enabled:     true,
		ProjectRoot: root,
		Entries:     []PolicyEntry{{Path: "lib/libcustom.so"}},
	}
	target := filepath.Join(root, "lib", "libcustom.so")
	if err := p.Allow(target); err != nil {
		t.Errorf("expected relative entry to resolve under project root, got %v", err)
	}
}

func TestPolicyEntryValidation(t *testing.T) {
	cases := []struct {
		name    string
		entry   PolicyEntry
		wantErr bool
	}{
		{"empty", PolicyEntry{}, true},
		{"both", PolicyEntry{Path: "/x", Glob: "/y*"}, true},
		{"path only", PolicyEntry{Path: "/x"}, false},
		{"glob only", PolicyEntry{Glob: "/y*"}, false},
		{"bad glob", PolicyEntry{Glob: "/y[unclosed"}, true},
	}
	for _, c := range cases {
		err := c.entry.Validate()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: got err=%v, wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

func TestNewPolicyFromConfigNilProducesDenyAll(t *testing.T) {
	p, err := NewPolicyFromConfig(nil, "")
	if err != nil {
		t.Fatalf("NewPolicyFromConfig(nil): %v", err)
	}
	if p.Enabled {
		t.Errorf("nil config should produce disabled policy")
	}
	if err := p.Allow("/anything"); err == nil {
		t.Errorf("disabled policy should deny")
	}
}

func TestNewPolicyFromConfigPopulatesEntries(t *testing.T) {
	cfg := &PolicyConfig{
		Enabled: true,
		Libraries: []PolicyLibraryConfig{
			{Path: "/usr/lib/libm.so.6"},
			{Glob: "/opt/torch/*.so"},
		},
	}
	p, err := NewPolicyFromConfig(cfg, "/work")
	if err != nil {
		t.Fatalf("NewPolicyFromConfig: %v", err)
	}
	if !p.Enabled {
		t.Errorf("policy should be enabled")
	}
	if len(p.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(p.Entries))
	}
	if p.ProjectRoot != "/work" {
		t.Errorf("ProjectRoot = %q, want /work", p.ProjectRoot)
	}
}

func TestNewPolicyFromConfigRejectsMalformedEntries(t *testing.T) {
	cfg := &PolicyConfig{
		Enabled:   true,
		Libraries: []PolicyLibraryConfig{{Path: "/x", Glob: "/y*"}},
	}
	if _, err := NewPolicyFromConfig(cfg, ""); err == nil {
		t.Fatalf("expected error for malformed entry")
	}
}

func TestNewPolicyFromCLIWildcardBecomesGlob(t *testing.T) {
	p, err := NewPolicyFromCLI([]string{"libm.so.*"}, "")
	if err != nil {
		t.Fatalf("NewPolicyFromCLI: %v", err)
	}
	if !p.Enabled {
		t.Errorf("CLI policy with patterns should be enabled")
	}
	if p.Entries[0].Glob != "libm.so.*" {
		t.Errorf("wildcard pattern should become Glob entry, got %+v", p.Entries[0])
	}
	if err := p.Allow("libm.so.6"); err != nil {
		t.Errorf("CLI glob should match: %v", err)
	}
}

func TestNewPolicyFromCLIExactPathBecomesPath(t *testing.T) {
	p, err := NewPolicyFromCLI([]string{"/usr/lib/libm.so.6"}, "")
	if err != nil {
		t.Fatalf("NewPolicyFromCLI: %v", err)
	}
	if p.Entries[0].Path != "/usr/lib/libm.so.6" {
		t.Errorf("non-wildcard pattern should become Path entry, got %+v", p.Entries[0])
	}
}

func TestNewPolicyFromCLIEmptyDisabled(t *testing.T) {
	p, err := NewPolicyFromCLI(nil, "")
	if err != nil {
		t.Fatalf("NewPolicyFromCLI(nil): %v", err)
	}
	if p.Enabled {
		t.Errorf("empty CLI patterns should produce disabled policy")
	}
}

func TestPolicyOverlayCombines(t *testing.T) {
	base := &Policy{
		Enabled:     true,
		ProjectRoot: "/work",
		Entries:     []PolicyEntry{{Path: "/usr/lib/libm.so.6"}},
	}
	cli := &Policy{
		Enabled: true,
		Entries: []PolicyEntry{{Glob: "/opt/torch/*.so"}},
	}
	merged := base.Overlay(cli)
	if !merged.Enabled {
		t.Errorf("overlay should be enabled")
	}
	if len(merged.Entries) != 2 {
		t.Errorf("expected merged entries=2, got %d", len(merged.Entries))
	}
	if merged.ProjectRoot != "/work" {
		t.Errorf("overlay should preserve base ProjectRoot, got %q", merged.ProjectRoot)
	}
	if err := merged.Allow("/usr/lib/libm.so.6"); err != nil {
		t.Errorf("base entry should still match: %v", err)
	}
	if err := merged.Allow("/opt/torch/libtorch.so"); err != nil {
		t.Errorf("overlay entry should match: %v", err)
	}
}

func TestPolicyOverlayWithDisabledBase(t *testing.T) {
	// CLI overlay can enable FFI even when the manifest doesn't.
	base := &Policy{Enabled: false}
	cli := &Policy{
		Enabled: true,
		Entries: []PolicyEntry{{Path: "/usr/lib/libm.so.6"}},
	}
	merged := base.Overlay(cli)
	if err := merged.Allow("/usr/lib/libm.so.6"); err != nil {
		t.Errorf("CLI overlay should enable FFI: %v", err)
	}
}

func TestPolicyErrorUnwrap(t *testing.T) {
	cause := errors.New("boom")
	err := &PolicyError{Reason: ReasonInvalidPath, Path: "/x", Cause: cause}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is should reach the underlying cause")
	}
}
