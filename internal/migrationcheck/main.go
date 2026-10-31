// migrationcheck enforces that the current initial definition and every v1.0.0+ release's migrations are immutable.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// 当前初始定义的固定哈希；自 v1.0.0 tag 起不可改写，后续结构从 0002 递增。
var initialFrozen = map[string]string{
	"0001_init.up.sql":   "ef2d1f229fa15b660cc7ecb4f417615b76866c19494641c96faf25bab24188b6",
	"0001_init.down.sql": "812541721471025056edd1f655d876b2f4d938fffd8f4f7ee2cfa9a97ff490d5",
}

var migrationName = regexp.MustCompile(`^([0-9]+)_(.+)\.(up|down)\.sql$`)
var releaseTag = regexp.MustCompile(`^v([0-9]+)\.[0-9]+\.[0-9]+$`)

func git(root string, args ...string) ([]byte, error) {
	c := exec.Command("git", args...)
	c.Dir = root
	b, err := c.Output()
	if err != nil {
		return nil, fmt.Errorf("git %v: %w", args, err)
	}
	return b, nil
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
		m := releaseTag.FindStringSubmatch(tag)
		if m == nil || m[1] == "0" {
			// 主版本为 0 的开发期标签已退役，不参与比对，也不获得豁免。
			continue
		}
		refs = append(refs, "refs/tags/"+tag)
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
func check(root string) error { return checkWithInitial(root, initialFrozen) }

// checkWithInitial 固定当前初始定义，并要求 v1.0.0 及以上每个可达正式标签内的迁移不可改写、不可消失、版本递增。
func checkWithInitial(root string, initialHashes map[string]string) error {
	releases, err := releasedHistory(root)
	if err != nil {
		return err
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
		files, err := releaseMigrations(root, ref)
		if err != nil {
			return err
		}
		for name := range initialHashes {
			if !bytes.Equal(files[name], baseline[name]) {
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
