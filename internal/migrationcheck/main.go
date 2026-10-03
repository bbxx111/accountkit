// migrationcheck enforces immutable published SQL without changing runtime storage.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var frozen = map[string]string{
	"0001_init.up.sql":   "c696c3dcb613283ab61547d3f9c3146d3db6e18c32c90fbfb1a33ccea76f1d0e",
	"0001_init.down.sql": "06671861850f1c317eadaf73dc004684be0ea467332e94ae54b978358308bdcd",
}
var migrationName = regexp.MustCompile(`^([0-9]+)_(.+)\.(up|down)\.sql$`)
var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func git(root string, args ...string) ([]byte, error) {
	c := exec.Command("git", args...)
	c.Dir = root
	b, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("git %v: %w", args, err)
	}
	return b, nil
}
func sourceBaseline(root string) (map[string][]byte, error) {
	b, err := os.ReadFile(filepath.Join(root, "tests/testdata/source-baseline/manifest.json"))
	if err != nil {
		return nil, err
	}
	var entries []struct{ Path, SHA256 string }
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, err
	}
	recorded := map[string]string{}
	for _, e := range entries {
		recorded[e.Path] = e.SHA256
	}
	out := map[string][]byte{}
	for name, want := range frozen {
		b, err := os.ReadFile(filepath.Join(root, "tests/testdata/source-baseline", name))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want || recorded["migrations/"+name] != want {
			return nil, fmt.Errorf("frozen source baseline changed: %s", name)
		}
		out[name] = b
	}
	return out, nil
}
func releasedHistory(root string) ([]string, error) {
	shallow, err := git(root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(shallow)) == "true" {
		return nil, fmt.Errorf("full Git history is required; fetch tags and unshallow before release checks")
	}
	if _, err := git(root, "rev-parse", "--verify", "HEAD"); err != nil {
		return nil, nil
	}
	tags, err := git(root, "tag", "--merged", "HEAD")
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, tag := range strings.Fields(string(tags)) {
		if releaseTag.MatchString(tag) {
			refs = append(refs, "refs/tags/"+tag)
		}
	}
	return refs, nil
}
func mergeRelease(root, ref string, baseline map[string][]byte) error {
	paths, err := git(root, "ls-tree", "-r", "--name-only", ref, "--", "migrations")
	if err != nil {
		return err
	}
	for _, path := range strings.Fields(string(paths)) {
		if filepath.ToSlash(filepath.Dir(path)) != "migrations" || !strings.HasSuffix(path, ".sql") {
			continue
		}
		data, err := git(root, "show", ref+":"+path)
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		if old, exists := baseline[name]; exists && !bytes.Equal(old, data) {
			return fmt.Errorf("release rewrote historical migration: %s", name)
		}
		baseline[name] = data
	}
	return nil
}
func check(root string) error {
	baseline, err := sourceBaseline(root)
	if err != nil {
		return err
	}
	// Always enforce the source baseline, even if a past release was created incorrectly.
	for name, want := range baseline {
		got, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("historical migration changed: %s", name)
		}
	}
	releases, err := releasedHistory(root)
	if err != nil {
		return err
	}
	// Every reachable release, including merged branches and HEAD, is immutable.
	// Conflicting published contents are errors, regardless of tag ordering.
	for _, ref := range releases {
		if err := mergeRelease(root, ref, baseline); err != nil {
			return err
		}
	}
	maxVersion := uint64(0)
	for name, want := range baseline {
		m := migrationName.FindStringSubmatch(name)
		if m == nil {
			return fmt.Errorf("invalid historical migration: %s", name)
		}
		version, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			return err
		}
		if version > maxVersion {
			maxVersion = version
		}
		got, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return fmt.Errorf("historical migration missing: %s", name)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("historical migration changed: %s", name)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		return err
	}
	names := map[uint64]string{}
	directions := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		m := migrationName.FindStringSubmatch(name)
		if m == nil {
			return fmt.Errorf("invalid migration filename: %s", name)
		}
		version, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			return err
		}
		if prior, ok := names[version]; ok && prior != m[2] {
			return fmt.Errorf("duplicate migration version: %s", name)
		}
		names[version] = m[2]
		direction := fmt.Sprintf("%d/%s", version, m[3])
		if directions[direction] {
			return fmt.Errorf("duplicate migration direction: %s", name)
		}
		directions[direction] = true
		if _, old := baseline[name]; !old && version <= maxVersion {
			return fmt.Errorf("new migration version must exceed %d: %s", maxVersion, name)
		}
	}
	return nil
}
func main() {
	root, err := os.Getwd()
	if err == nil {
		err = check(root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Migration history is immutable; new versions are strictly increasing")
}
