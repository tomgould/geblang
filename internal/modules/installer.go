package modules

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	yamllib "gopkg.in/yaml.v3"
)

// LockFile records the resolved commit SHA for each git dependency.
type LockFile struct {
	Dependencies map[string]LockEntry `yaml:"dependencies"`
}

// LockEntry is one vendored git dependency in geblang.lock.
type LockEntry struct {
	URL     string `yaml:"url"`
	Version string `yaml:"version,omitempty"`
	Commit  string `yaml:"commit"`
}

// Install fetches all git dependencies declared in manifestPath into
// <manifestRoot>/vendor/, pinning each resolved commit in lockPath.
// Local path dependencies are skipped (they need no fetching).
func Install(manifestPath, lockPath string) error {
	r := &Resolver{Manifests: map[string]*Manifest{}}
	manifest, err := r.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("install: read manifest: %w", err)
	}
	vendorDir := filepath.Join(manifest.Root, "vendor")
	lock := readLockFile(lockPath)
	changed := false

	for name, dep := range manifest.Dependencies {
		if dep.Git == "" {
			continue
		}
		destDir := filepath.Join(vendorDir, name)
		if entry, ok := lock.Dependencies[name]; ok &&
			entry.URL == dep.Git && entry.Version == dep.Version {
			if _, err := os.Stat(destDir); err == nil {
				short := entry.Commit
				if len(short) > 8 {
					short = short[:8]
				}
				fmt.Printf("  %s: already installed (%s)\n", name, short)
				continue
			}
		}
		fmt.Printf("  %s: fetching %s", name, dep.Git)
		if dep.Version != "" {
			fmt.Printf(" @ %s", dep.Version)
		}
		fmt.Println()

		if err := os.RemoveAll(destDir); err != nil {
			return fmt.Errorf("install %s: clean vendor dir: %w", name, err)
		}
		args := []string{"clone", "--depth", "1"}
		if dep.Version != "" {
			args = append(args, "--branch", dep.Version)
		}
		args = append(args, dep.Git, destDir)
		cmd := exec.Command("git", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("install %s: git clone: %w", name, err)
		}
		commit, _ := gitHead(destDir)
		if lock.Dependencies == nil {
			lock.Dependencies = map[string]LockEntry{}
		}
		lock.Dependencies[name] = LockEntry{URL: dep.Git, Version: dep.Version, Commit: commit}
		changed = true
	}

	if changed {
		if err := writeLockFile(lockPath, lock); err != nil {
			return fmt.Errorf("install: write lock file: %w", err)
		}
	}
	return nil
}

// InstallOne adds a git dependency to manifestPath, fetches it, and updates
// the lock file. If name is empty it is derived from the URL's last segment.
func InstallOne(manifestPath, lockPath, gitURL, version, name string) error {
	if name == "" {
		name = nameFromURL(gitURL)
	}
	if err := addDependency(manifestPath, name, gitURL, version); err != nil {
		return fmt.Errorf("install: update manifest: %w", err)
	}
	return Install(manifestPath, lockPath)
}

// nameFromURL derives a package name from the last segment of a git URL.
func nameFromURL(url string) string {
	url = strings.TrimSuffix(strings.TrimRight(url, "/"), ".git")
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return "package"
	}
	return parts[len(parts)-1]
}

func gitHead(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func readLockFile(path string) LockFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return LockFile{Dependencies: map[string]LockEntry{}}
	}
	var lock LockFile
	if err := yamllib.Unmarshal(data, &lock); err != nil {
		return LockFile{Dependencies: map[string]LockEntry{}}
	}
	if lock.Dependencies == nil {
		lock.Dependencies = map[string]LockEntry{}
	}
	return lock
}

func writeLockFile(path string, lock LockFile) error {
	data, err := yamllib.Marshal(lock)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// addDependency updates or inserts a git dependency in manifestPath.
// The manifest is re-marshalled; comments are not preserved.
func addDependency(manifestPath, name, gitURL, version string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var mf manifestFile
	if err := yamllib.Unmarshal(data, &mf); err != nil {
		return err
	}
	if mf.Dependencies == nil {
		mf.Dependencies = map[string]Dependency{}
	}
	mf.Dependencies[name] = Dependency{Git: gitURL, Version: version}
	out, err := yamllib.Marshal(&mf)
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath, out, 0o644)
}
