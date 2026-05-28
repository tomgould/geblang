package ffi

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Policy is the capability-gate that callers apply before invoking
// Open. A nil or zero-valued Policy denies every load; that is
// intentional - FFI must be opt-in. Construct policies via
// NewPolicyFromManifest (project mode) or NewPolicyFromCLI (script
// mode) and merge with Overlay when a single run needs both.
type Policy struct {
	// Enabled is the master switch. False means no path is allowed
	// regardless of Entries.
	Enabled bool

	// Entries is the allow-list. A library load is permitted when at
	// least one entry matches.
	Entries []PolicyEntry

	// ProjectRoot anchors relative-path matching; defaults to the
	// current working directory when empty. Glob patterns and exact
	// paths are evaluated against the absolute form of the requested
	// path, so this only matters for relative entries.
	ProjectRoot string
}

// PolicyEntry is one rule in the allow-list. Exactly one of Path or
// Glob must be set; entries with both set are treated as malformed
// and rejected at parse time.
type PolicyEntry struct {
	Path string
	Glob string
}

// Validate confirms the entry is well-formed. Used by manifest /
// CLI parsing to reject configuration errors early.
func (e PolicyEntry) Validate() error {
	if e.Path == "" && e.Glob == "" {
		return errors.New("policy entry must have either path or glob")
	}
	if e.Path != "" && e.Glob != "" {
		return errors.New("policy entry must have path OR glob, not both")
	}
	if e.Glob != "" {
		if _, err := filepath.Match(e.Glob, ""); err != nil {
			return fmt.Errorf("policy entry glob %q: %w", e.Glob, err)
		}
	}
	return nil
}

// Allow reports nil if the requested library path is permitted by
// the policy. Otherwise it returns a PolicyError that callers can
// project onto a Geblang PermissionError in the runtime layer.
func (p *Policy) Allow(path string) error {
	if p == nil || !p.Enabled {
		return &PolicyError{Reason: ReasonDisabled, Path: path}
	}
	abs, err := absolutePath(path)
	if err != nil {
		return &PolicyError{Reason: ReasonInvalidPath, Path: path, Cause: err}
	}
	for _, entry := range p.Entries {
		if p.matches(entry, path, abs) {
			return nil
		}
	}
	return &PolicyError{Reason: ReasonNotAllowed, Path: path}
}

// Overlay returns a new policy combining the receiver's entries
// with extra. The result is enabled if either side is. Used to
// merge a per-project manifest with the --allow-ffi CLI overlay.
func (p *Policy) Overlay(extra *Policy) *Policy {
	out := &Policy{ProjectRoot: p.projectRootOrDefault()}
	if extra != nil && extra.ProjectRoot != "" {
		out.ProjectRoot = extra.ProjectRoot
	}
	out.Enabled = p.Enabled || (extra != nil && extra.Enabled)
	if p != nil {
		out.Entries = append(out.Entries, p.Entries...)
	}
	if extra != nil {
		out.Entries = append(out.Entries, extra.Entries...)
	}
	return out
}

func (p *Policy) projectRootOrDefault() string {
	if p == nil {
		return ""
	}
	return p.ProjectRoot
}

func (p *Policy) matches(entry PolicyEntry, requested, abs string) bool {
	switch {
	case entry.Path != "":
		want := entry.Path
		if !filepath.IsAbs(want) && p.ProjectRoot != "" {
			want = filepath.Join(p.ProjectRoot, want)
		}
		want = filepath.Clean(want)
		return abs == want || requested == entry.Path
	case entry.Glob != "":
		pattern := entry.Glob
		if !filepath.IsAbs(pattern) && p.ProjectRoot != "" {
			pattern = filepath.Join(p.ProjectRoot, pattern)
		}
		if ok, _ := filepath.Match(pattern, abs); ok {
			return true
		}
		// Also match against the un-anchored form so library names
		// like "libm.so.6" with no path prefix match a glob like
		// "libm.so.*".
		if ok, _ := filepath.Match(entry.Glob, requested); ok {
			return true
		}
	}
	return false
}

// absolutePath resolves to an absolute, cleaned filesystem path.
// Library names without separators (`libm.so.6`) are kept as-is so
// they match dlopen's runtime search behaviour; the policy lets a
// glob like `libm.so.*` cover them.
func absolutePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty path")
	}
	if !strings.Contains(path, string(filepath.Separator)) {
		return path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// PolicyError is returned by Policy.Allow. Callers in the runtime
// layer use Reason to format the user-facing message.
type PolicyError struct {
	Reason PolicyReason
	Path   string
	Cause  error
}

// PolicyReason classifies why a load was denied.
type PolicyReason int

const (
	ReasonDisabled    PolicyReason = iota // permissions.ffi block missing or disabled
	ReasonNotAllowed                      // path is real but not in the allow-list
	ReasonInvalidPath                     // path could not be resolved (e.g. bad chars)
)

func (e *PolicyError) Error() string {
	switch e.Reason {
	case ReasonDisabled:
		return fmt.Sprintf("ffi.dlopen %q: FFI disabled (no permissions.ffi block in geblang.yaml; add one or pass --allow-ffi)", e.Path)
	case ReasonNotAllowed:
		return fmt.Sprintf("ffi.dlopen %q: not in permissions.ffi.libraries (add an entry or pass --allow-ffi %s)", e.Path, e.Path)
	case ReasonInvalidPath:
		if e.Cause != nil {
			return fmt.Sprintf("ffi.dlopen %q: invalid path: %v", e.Path, e.Cause)
		}
		return fmt.Sprintf("ffi.dlopen %q: invalid path", e.Path)
	}
	return fmt.Sprintf("ffi.dlopen %q: denied", e.Path)
}

// Unwrap exposes the underlying cause for ReasonInvalidPath so
// errors.Is / errors.As work through the policy boundary.
func (e *PolicyError) Unwrap() error {
	return e.Cause
}

// NewPolicyFromConfig builds a Policy from a parsed manifest block.
// Pass nil cfg or a disabled block to produce a deny-everything
// policy. Returns an error if any entry is malformed.
func NewPolicyFromConfig(cfg *PolicyConfig, projectRoot string) (*Policy, error) {
	policy := &Policy{ProjectRoot: projectRoot}
	if cfg == nil {
		return policy, nil
	}
	policy.Enabled = cfg.Enabled
	for i, raw := range cfg.Libraries {
		entry := PolicyEntry{Path: raw.Path, Glob: raw.Glob}
		if err := entry.Validate(); err != nil {
			return nil, fmt.Errorf("permissions.ffi.libraries[%d]: %w", i, err)
		}
		policy.Entries = append(policy.Entries, entry)
	}
	return policy, nil
}

// PolicyConfig is the structured shape the manifest parser populates
// from the `permissions.ffi` block. The yaml tags match the schema
// callers will write in geblang.yaml.
type PolicyConfig struct {
	Enabled   bool                  `yaml:"enabled" json:"enabled"`
	Libraries []PolicyLibraryConfig `yaml:"libraries" json:"libraries"`
}

// PolicyLibraryConfig is one entry under `permissions.ffi.libraries`.
type PolicyLibraryConfig struct {
	Path string `yaml:"path" json:"path"`
	Glob string `yaml:"glob" json:"glob"`
}

// NewPolicyFromCLI builds a deny-by-default policy with the given
// `--allow-ffi` patterns turned into glob entries. Each pattern is
// treated as a glob unless it has no wildcard characters, in which
// case it becomes an exact-path entry.
func NewPolicyFromCLI(patterns []string, projectRoot string) (*Policy, error) {
	policy := &Policy{ProjectRoot: projectRoot, Enabled: len(patterns) > 0}
	for _, pat := range patterns {
		var entry PolicyEntry
		if strings.ContainsAny(pat, "*?[") {
			entry = PolicyEntry{Glob: pat}
		} else {
			entry = PolicyEntry{Path: pat}
		}
		if err := entry.Validate(); err != nil {
			return nil, fmt.Errorf("--allow-ffi %q: %w", pat, err)
		}
		policy.Entries = append(policy.Entries, entry)
	}
	return policy, nil
}
