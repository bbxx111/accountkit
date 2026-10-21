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

// The user-authorized current initial definition has its own immutable baseline. The source
// snapshot and the published v0.3.0 initial definition remain independently pinned.
var initialFrozen = map[string]string{
	"0001_init.up.sql":   "ea3f0a0d376addf95e9bc1852b6b753a04b214293ac32714e351ce9b6262c940",
	"0001_init.down.sql": "2f26c7dc5f5c5cfe2ee5a9967884fc70c7f213418a602fb2535621904f251f9b",
}

var legacyReleases = map[string]string{
	"refs/tags/v0.1.0": "48603d6d349c57910328d393d5e17decec934f11",
	"refs/tags/v0.2.0": "e19af8542f099f14103627140c0fe23f14c99b7e",
}

type pinnedInitialRelease struct {
	Commit string
	Hashes map[string]string
}

// Published initial definitions remain pinned independently. v0.3.0 predates
// session consolidation; v0.4.0 pins the current initial definition. Moving a
// tag or rewriting its SQL never receives a historical-baseline exception.
var publishedInitialReleases = map[string]pinnedInitialRelease{
	"refs/tags/v0.4.0": {
		Commit: "62f0799b6c6ef1603b33f81834d39033dd0d0c4d",
		Hashes: map[string]string{
			"0001_init.up.sql":   "e1c5f1232136ed655e4963411f972e64493290998c5de27f88e511b94dde710e",
			"0001_init.down.sql": "2f26c7dc5f5c5cfe2ee5a9967884fc70c7f213418a602fb2535621904f251f9b",
		},
	},
	"refs/tags/v0.3.0": {
		Commit: "ba2d0412764c659beabdbd0d3f0f0f9bb9007657",
		Hashes: map[string]string{
			"0001_init.up.sql":   "5480fd1b7183b43cfc09792683627c9c41dfd691c3626c1b4ae3905b6b8cffa8",
			"0001_init.down.sql": "53613c379dacd01f9739ce2824570f833060aca26548ff13b3ae8c42c49e63d6",
		},
	},
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
func releaseMigrations(root, ref string) (map[string][]byte, error) {
	paths, err := git(root, "ls-tree", "-r", "--name-only", ref, "--", "migrations")
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, path := range strings.Fields(string(paths)) {
		if filepath.ToSlash(filepath.Dir(path)) != "migrations" || !strings.HasSuffix(path, ".sql") {
			continue
		}
		data, err := git(root, "show", ref+":"+path)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(path)
		out[name] = data
	}
	return out, nil
}
func check(root string) error {
	return checkWithReleaseBaselines(root, initialFrozen, legacyReleases, publishedInitialReleases)
}

func checkWithBaselines(root string, initialHashes, legacyCommits map[string]string) error {
	return checkWithReleaseBaselines(root, initialHashes, legacyCommits, nil)
}

func checkWithReleaseBaselines(root string, initialHashes, legacyCommits map[string]string, published map[string]pinnedInitialRelease) error {
	legacy, err := sourceBaseline(root)
	if err != nil {
		return err
	}
	releases, err := releasedHistory(root)
	if err != nil {
		return err
	}
	// A deleted tag or a tag moved outside HEAD's ancestry must not silently
	// disappear from --merged HEAD and thereby escape its exact historical pin.
	for ref, want := range legacyCommits {
		if err := checkPinnedRelease(root, ref, want); err != nil {
			return err
		}
	}
	for ref, pinned := range published {
		if err := checkPinnedRelease(root, ref, pinned.Commit); err != nil {
			return err
		}
	}
	baseline := map[string][]byte{}
	for name, want := range initialHashes {
		got, err := os.ReadFile(filepath.Join(root, "migrations", name))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(got)
		if hex.EncodeToString(sum[:]) != want {
			return fmt.Errorf("consolidated initial migration changed: %s", name)
		}
		baseline[name] = got
	}
	initialVersion, err := validateNames(baseline)
	if err != nil {
		return err
	}
	snapshots := map[string]map[string][]byte{}
	// Every reachable release, including merged branches and HEAD, is immutable.
	// Conflicting published contents are errors, regardless of tag ordering.
	for _, ref := range releases {
		initial := baseline
		pinned, hasPinned := published[ref]
		if _, ok := legacyCommits[ref]; ok {
			initial = legacy
		}
		files, err := releaseMigrations(root, ref)
		if err != nil {
			return err
		}
		for name := range initialHashes {
			if hasPinned {
				sum := sha256.Sum256(files[name])
				if hex.EncodeToString(sum[:]) != pinned.Hashes[name] {
					return fmt.Errorf("release initial migration changed or missing: %s: %s", ref, name)
				}
			} else if !bytes.Equal(files[name], initial[name]) {
				return fmt.Errorf("release initial migration changed or missing: %s: %s", ref, name)
			}
		}
		if _, err := validateNames(files); err != nil {
			return fmt.Errorf("%s: %w", ref, err)
		}
		snapshots[ref] = files
		for name, data := range files {
			if _, initial := initialHashes[name]; initial {
				continue
			}
			version, _ := strconv.ParseUint(migrationName.FindStringSubmatch(name)[1], 10, 64)
			if version <= initialVersion {
				return fmt.Errorf("release new migration version must exceed %d: %s: %s", initialVersion, ref, name)
			}
			if old, exists := baseline[name]; exists && !bytes.Equal(old, data) {
				return fmt.Errorf("release rewrote historical migration: %s", name)
			}
			baseline[name] = data
		}
	}
	for prior, older := range snapshots {
		for ref, files := range snapshots {
			if prior == ref {
				continue
			}
			ancestor, err := isAncestor(root, prior, ref)
			if err != nil {
				return err
			}
			if !ancestor {
				continue
			}
			maxVersion, err := validateNames(older)
			if err != nil {
				return err
			}
			for name, data := range older {
				if _, initial := initialHashes[name]; initial {
					continue
				}
				if !bytes.Equal(files[name], data) {
					return fmt.Errorf("release removed or changed historical migration: %s: %s", ref, name)
				}
			}
			for name := range files {
				if _, exists := older[name]; exists {
					continue
				}
				version, _ := strconv.ParseUint(migrationName.FindStringSubmatch(name)[1], 10, 64)
				if version <= maxVersion {
					return fmt.Errorf("release new migration version must exceed %d: %s: %s", maxVersion, ref, name)
				}
			}
		}
	}
	maxVersion, err := validateNames(baseline)
	if err != nil {
		return err
	}
	for name, want := range baseline {
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
	current := map[string][]byte{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		current[name] = nil
	}
	if _, err := validateNames(current); err != nil {
		return err
	}
	for name := range current {
		m := migrationName.FindStringSubmatch(name)
		version, _ := strconv.ParseUint(m[1], 10, 64)
		if _, old := baseline[name]; !old && version <= maxVersion {
			return fmt.Errorf("new migration version must exceed %d: %s", maxVersion, name)
		}
	}
	return nil
}

func checkPinnedRelease(root, ref, wantCommit string) error {
	commit, err := git(root, "rev-parse", ref+"^{commit}")
	if err != nil {
		return fmt.Errorf("historical release tag missing: %s: %w", ref, err)
	}
	if strings.TrimSpace(string(commit)) != wantCommit {
		return fmt.Errorf("historical release tag moved: %s", ref)
	}
	return nil
}

func validateNames(files map[string][]byte) (uint64, error) {
	names := map[uint64]string{}
	directions := map[string]bool{}
	maxVersion := uint64(0)
	for name := range files {
		m := migrationName.FindStringSubmatch(name)
		if m == nil {
			return 0, fmt.Errorf("invalid migration filename: %s", name)
		}
		version, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			return 0, err
		}
		if prior, ok := names[version]; ok && prior != m[2] {
			return 0, fmt.Errorf("duplicate migration version: %s", name)
		}
		names[version] = m[2]
		direction := fmt.Sprintf("%d/%s", version, m[3])
		if directions[direction] {
			return 0, fmt.Errorf("duplicate migration direction: %s", name)
		}
		directions[direction] = true
		if version > maxVersion {
			maxVersion = version
		}
	}
	return maxVersion, nil
}

func isAncestor(root, prior, ref string) (bool, error) {
	c := exec.Command("git", "merge-base", "--is-ancestor", prior, ref)
	c.Dir = root
	err := c.Run()
	if err == nil {
		return true, nil
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base %s %s: %w", prior, ref, err)
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
