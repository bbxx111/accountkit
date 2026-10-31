package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

var fixtureInitial = map[string][]byte{
	"0001_init.up.sql":   []byte("CREATE TABLE consolidated_fixture (id integer);\n"),
	"0001_init.down.sql": []byte("DROP TABLE consolidated_fixture;\n"),
}

type repositoryFixture struct {
	root string
}

func (r repositoryFixture) check() error {
	hashes := map[string]string{}
	for name, data := range fixtureInitial {
		sum := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(sum[:])
	}
	return checkWithInitial(r.root, hashes)
}

// fixture 创建只含当前初始定义的仓库；tag=true 时在 v1.0.0 固定该定义并再提交一次。
func fixture(t *testing.T, tag bool) repositoryFixture {
	t.Helper()
	root := t.TempDir()
	gitTest(t, root, "init")
	for name, data := range fixtureInitial {
		write(t, root, "migrations/"+name, data)
	}
	if tag {
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-m", "v1.0.0 baseline")
		gitTest(t, root, "tag", "-a", "v1.0.0", "-m", "first formal release")
		write(t, root, "README.md", []byte("next"))
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-m", "next")
	}
	return repositoryFixture{root}
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
				tc.mutate(t, r.root)
				if err := r.check(); (err == nil) != tc.ok {
					t.Fatalf("check=%v expected success %v", err, tc.ok)
				}
			})
		}
	}
}
func TestHeadTagCannotBlessHistoricalRewrite(t *testing.T) {
	r := fixture(t, true)
	write(t, r.root, "migrations/0001_init.up.sql", []byte("SELECT 1;"))
	gitTest(t, r.root, "add", ".")
	gitTest(t, r.root, "commit", "-m", "bad release")
	gitTest(t, r.root, "tag", "v1.1.0")
	if err := r.check(); err == nil {
		t.Fatal("HEAD tag bypassed history check")
	}
}
func TestWorkingTreeCannotRewriteMigrationAtHeadRelease(t *testing.T) {
	r := fixture(t, true)
	write(t, r.root, "migrations/0002_extra.up.sql", []byte("SELECT 1;"))
	gitTest(t, r.root, "add", ".")
	gitTest(t, r.root, "commit", "-m", "release")
	gitTest(t, r.root, "tag", "v1.1.0")
	write(t, r.root, "migrations/0002_extra.up.sql", []byte("SELECT 2;"))
	if err := r.check(); err == nil {
		t.Fatal("working tree rewrote a migration in the HEAD release")
	}
}

func TestMergedReleaseHistoryIsImmutable(t *testing.T) {
	for _, mutation := range []string{"rewrite", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			r := fixture(t, true)
			gitTest(t, r.root, "checkout", "-b", "mainline")
			gitTest(t, r.root, "checkout", "-b", "release")
			write(t, r.root, "migrations/0002_extra.up.sql", []byte("SELECT 1;"))
			gitTest(t, r.root, "add", ".")
			gitTest(t, r.root, "commit", "-m", "release migration")
			gitTest(t, r.root, "tag", "-a", "v1.1.0", "-m", "release")
			gitTest(t, r.root, "checkout", "mainline")
			write(t, r.root, "README.md", []byte("mainline work"))
			gitTest(t, r.root, "add", ".")
			gitTest(t, r.root, "commit", "-m", "mainline work")
			gitTest(t, r.root, "merge", "--no-ff", "release", "-m", "merge release")
			if err := r.check(); err != nil {
				t.Fatal(err)
			}
			if mutation == "rewrite" {
				write(t, r.root, "migrations/0002_extra.up.sql", []byte("SELECT 2;"))
			} else if err := os.Remove(filepath.Join(r.root, "migrations/0002_extra.up.sql")); err != nil {
				t.Fatal(err)
			}
			if err := r.check(); err == nil {
				t.Fatal("merged release migration was not protected")
			}
		})
	}
}

func TestPublishedIncrementCannotDisappearInLaterRelease(t *testing.T) {
	r := fixture(t, true)
	write(t, r.root, "migrations/0002_extra.up.sql", []byte("SELECT 1;"))
	gitTest(t, r.root, "add", ".")
	gitTest(t, r.root, "commit", "-m", "publish increment")
	gitTest(t, r.root, "tag", "v1.1.0")
	if err := os.Remove(filepath.Join(r.root, "migrations/0002_extra.up.sql")); err != nil {
		t.Fatal(err)
	}
	gitTest(t, r.root, "add", ".")
	gitTest(t, r.root, "commit", "-m", "delete increment")
	gitTest(t, r.root, "tag", "v1.2.0")
	// Restore only the working tree: historical release deletion must still fail.
	write(t, r.root, "migrations/0002_extra.up.sql", []byte("SELECT 1;"))
	if err := r.check(); err == nil {
		t.Fatal("later release deleted a published migration")
	}
}

func TestShallowHistoryIsRejected(t *testing.T) {
	r := fixture(t, true)
	shallow := t.TempDir()
	gitTest(t, shallow, "clone", "--depth=1", "file://"+filepath.ToSlash(r.root), ".")
	r.root = shallow
	if err := r.check(); err == nil || !strings.Contains(err.Error(), "full Git history") {
		t.Fatalf("shallow history must be rejected, got %v", err)
	}
}

func TestReleaseVersionsMustIncrease(t *testing.T) {
	for _, tc := range []struct{ name, prior, added string }{
		{"initial baseline", "", "0000_extra.up.sql"},
		{"prior release", "0003_extra.up.sql", "0002_extra.up.sql"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t, false)
			if tc.prior != "" {
				write(t, r.root, "migrations/"+tc.prior, []byte("SELECT 3;"))
				gitTest(t, r.root, "add", ".")
				gitTest(t, r.root, "commit", "-m", "prior increment")
				gitTest(t, r.root, "tag", "v1.1.0")
			}
			write(t, r.root, "migrations/"+tc.added, []byte("SELECT 2;"))
			gitTest(t, r.root, "add", ".")
			gitTest(t, r.root, "commit", "-m", "lower increment")
			gitTest(t, r.root, "tag", "v1.2.0")
			if err := r.check(); err == nil {
				t.Fatal("release introduced a nonincrementing migration")
			}
		})
	}
}

// 开发期 v0.x.y 标签已退役：即使仍存在且其迁移与当前基线不同，也不参与检查。
func TestPreReleaseTagsAreIgnored(t *testing.T) {
	for _, tc := range []struct{ name, tag string }{
		{"old minor", "v0.7.2"},
		{"old zero", "v0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixture(t, true)
			write(t, r.root, "migrations/0001_init.up.sql", []byte("SELECT 'retired';"))
			write(t, r.root, "migrations/0002_retired.up.sql", []byte("SELECT 1;"))
			gitTest(t, r.root, "add", ".")
			gitTest(t, r.root, "commit", "-m", "retired development release")
			gitTest(t, r.root, "tag", tc.tag)
			// 恢复工作树到当前基线；退役标签内的篡改与多余增量都不能影响结果。
			for name, data := range fixtureInitial {
				write(t, r.root, "migrations/"+name, data)
			}
			if err := os.Remove(filepath.Join(r.root, "migrations/0002_retired.up.sql")); err != nil {
				t.Fatal(err)
			}
			gitTest(t, r.root, "add", ".")
			gitTest(t, r.root, "commit", "-m", "back to baseline")
			if err := r.check(); err != nil {
				t.Fatalf("pre-1.0 tag must be ignored: %v", err)
			}
		})
	}
}
