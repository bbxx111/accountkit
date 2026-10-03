package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, path string, b []byte) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
}
func gitTest(t *testing.T, root string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.autocrlf=false"}, args...)...)
	c.Dir = root
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}
func fixture(t *testing.T, tag bool) string {
	t.Helper()
	root := t.TempDir()
	gitTest(t, root, "init")
	for _, n := range []string{"0001_init.up.sql", "0001_init.down.sql", "manifest.json"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "tests", "testdata", "source-baseline", n))
		if err != nil {
			t.Fatal(err)
		}
		write(t, root, "tests/testdata/source-baseline/"+n, b)
		if n != "manifest.json" {
			write(t, root, "migrations/"+n, b)
		}
	}
	if tag {
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-m", "baseline")
		gitTest(t, root, "tag", "v0.1.0")
		write(t, root, "README.md", []byte("next"))
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-m", "next")
	}
	return root
}
func TestMigrationHistoryCheck(t *testing.T) {
	for _, tag := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			mutate func(*testing.T, string)
			ok     bool
		}{
			{"unchanged", func(*testing.T, string) {}, true},
			{"changed", func(t *testing.T, r string) { write(t, r, "migrations/0001_init.up.sql", []byte("SELECT 1;")) }, false},
			{"deleted", func(t *testing.T, r string) {
				if err := os.Remove(filepath.Join(r, "migrations/0001_init.up.sql")); err != nil {
					t.Fatal(err)
				}
			}, false},
			{"renumbered", func(t *testing.T, r string) {
				if err := os.Rename(filepath.Join(r, "migrations/0001_init.up.sql"), filepath.Join(r, "migrations/0002_init.up.sql")); err != nil {
					t.Fatal(err)
				}
			}, false},
			{"nonincrementing", func(t *testing.T, r string) { write(t, r, "migrations/0001_extra.up.sql", []byte("SELECT 1;")) }, false},
			{"append", func(t *testing.T, r string) { write(t, r, "migrations/0002_extra.up.sql", []byte("SELECT 1;")) }, true},
			{"duplicate new version", func(t *testing.T, r string) {
				write(t, r, "migrations/0002_a.up.sql", []byte("SELECT 1;"))
				write(t, r, "migrations/0002_b.up.sql", []byte("SELECT 1;"))
			}, false},
		} {
			name := tc.name
			if tag {
				name = "tag/" + name
			} else {
				name = "source/" + name
			}
			t.Run(name, func(t *testing.T) {
				r := fixture(t, tag)
				tc.mutate(t, r)
				if err := check(r); (err == nil) != tc.ok {
					t.Fatalf("check=%v expected success %v", err, tc.ok)
				}
			})
		}
	}
}
func TestHeadTagCannotBlessHistoricalRewrite(t *testing.T) {
	r := fixture(t, true)
	write(t, r, "migrations/0001_init.up.sql", []byte("SELECT 1;"))
	gitTest(t, r, "add", ".")
	gitTest(t, r, "commit", "-m", "bad release")
	gitTest(t, r, "tag", "v0.2.0")
	if err := check(r); err == nil {
		t.Fatal("HEAD tag bypassed history check")
	}
}
func TestFrozenBaselineCannotBeRewritten(t *testing.T) {
	r := fixture(t, false)
	b := []byte("SELECT 1;")
	write(t, r, "migrations/0001_init.up.sql", b)
	write(t, r, "tests/testdata/source-baseline/0001_init.up.sql", b)
	if err := check(r); err == nil {
		t.Fatal("rewritten baseline accepted")
	}
}

func TestWorkingTreeCannotRewriteMigrationAtHeadRelease(t *testing.T) {
	r := fixture(t, true)
	write(t, r, "migrations/0002_extra.up.sql", []byte("SELECT 1;"))
	gitTest(t, r, "add", ".")
	gitTest(t, r, "commit", "-m", "release")
	gitTest(t, r, "tag", "v0.2.0")
	write(t, r, "migrations/0002_extra.up.sql", []byte("SELECT 2;"))
	if err := check(r); err == nil {
		t.Fatal("working tree rewrote a migration in the HEAD release")
	}
}

func TestMergedReleaseHistoryIsImmutable(t *testing.T) {
	for _, mutation := range []string{"rewrite", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			r := fixture(t, true)
			gitTest(t, r, "checkout", "-b", "mainline")
			gitTest(t, r, "checkout", "-b", "release")
			write(t, r, "migrations/0002_extra.up.sql", []byte("SELECT 1;"))
			gitTest(t, r, "add", ".")
			gitTest(t, r, "commit", "-m", "release migration")
			gitTest(t, r, "tag", "-a", "v0.2.0", "-m", "release")
			gitTest(t, r, "checkout", "mainline")
			write(t, r, "README.md", []byte("mainline work"))
			gitTest(t, r, "add", ".")
			gitTest(t, r, "commit", "-m", "mainline work")
			gitTest(t, r, "merge", "--no-ff", "release", "-m", "merge release")
			if err := check(r); err != nil {
				t.Fatal(err)
			}
			if mutation == "rewrite" {
				write(t, r, "migrations/0002_extra.up.sql", []byte("SELECT 2;"))
			} else if err := os.Remove(filepath.Join(r, "migrations/0002_extra.up.sql")); err != nil {
				t.Fatal(err)
			}
			if err := check(r); err == nil {
				t.Fatal("merged release migration was not protected")
			}
		})
	}
}
