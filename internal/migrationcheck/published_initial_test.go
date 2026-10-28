package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// An exact release can retain its own initial definition without exempting
// later releases, moved tags, or published increments from history checks.
func TestPinnedPublishedInitialBaseline(t *testing.T) {
	for _, release := range []string{"v0.3.0", "v0.4.0"} {
		t.Run(release, func(t *testing.T) {
			for _, mutation := range []string{"none", "moved tag", "unreachable tag", "deleted tag", "wrong hash", "future old initial", "future current initial", "changed current", "published increment rewritten"} {
				t.Run(mutation, func(t *testing.T) {
					r := fixture(t, true)
					old := []byte("CREATE TABLE published_initial (id integer);\n")
					write(t, r.root, "migrations/0001_init.up.sql", old)
					if mutation == "published increment rewritten" {
						write(t, r.root, "migrations/0002_extra.up.sql", []byte("SELECT 1;"))
					}
					gitTest(t, r.root, "add", ".")
					gitTest(t, r.root, "commit", "-m", "published initial")
					gitTest(t, r.root, "tag", "-a", release, "-m", "release")
					commit, err := git(r.root, "rev-parse", "HEAD")
					if err != nil {
						t.Fatal(err)
					}
					currentHashes, oldHashes := map[string]string{}, map[string]string{}
					for name, data := range fixtureInitial {
						sum := sha256.Sum256(data)
						currentHashes[name] = hex.EncodeToString(sum[:])
						oldHashes[name] = currentHashes[name]
						write(t, r.root, "migrations/"+name, data)
					}
					sum := sha256.Sum256(old)
					oldHashes["0001_init.up.sql"] = hex.EncodeToString(sum[:])
					pinned := map[string]pinnedInitialRelease{
						"refs/tags/" + release: {Commit: strings.TrimSpace(string(commit)), Hashes: oldHashes},
					}
					gitTest(t, r.root, "add", ".")
					gitTest(t, r.root, "commit", "-m", "authorized consolidation")
					switch mutation {
					case "moved tag":
						gitTest(t, r.root, "tag", "-f", release)
					case "unreachable tag":
						moveTagOutsideHistory(t, r.root, release)
					case "deleted tag":
						gitTest(t, r.root, "tag", "-d", release)
					case "wrong hash":
						oldHashes["0001_init.up.sql"] = currentHashes["0001_init.up.sql"]
					case "future old initial":
						write(t, r.root, "migrations/0001_init.up.sql", old)
						gitTest(t, r.root, "add", ".")
						gitTest(t, r.root, "commit", "-m", "bad new release")
						gitTest(t, r.root, "tag", "v0.5.0")
						write(t, r.root, "migrations/0001_init.up.sql", fixtureInitial["0001_init.up.sql"])
					case "future current initial":
						gitTest(t, r.root, "tag", "v0.5.0")
					case "changed current":
						write(t, r.root, "migrations/0001_init.up.sql", []byte("SELECT 42;"))
					case "published increment rewritten":
						write(t, r.root, "migrations/0002_extra.up.sql", []byte("SELECT 2;"))
					}
					err = checkWithReleaseBaselines(r.root, currentHashes, r.legacy, pinned)
					wantSuccess := mutation == "none" || mutation == "future current initial"
					if (err == nil) != wantSuccess {
						t.Fatalf("%s: check=%v, want success=%v", mutation, err, wantSuccess)
					}
				})
			}
		})
	}
}

func TestPublishedV040Pinned(t *testing.T) {
	pinned, ok := publishedInitialReleases["refs/tags/v0.4.0"]
	if !ok || pinned.Commit != "62f0799b6c6ef1603b33f81834d39033dd0d0c4d" {
		t.Fatal("v0.4.0 exact release not protected")
	}
	for name, want := range map[string]string{"0001_init.up.sql": "e1c5f1232136ed655e4963411f972e64493290998c5de27f88e511b94dde710e", "0001_init.down.sql": "2f26c7dc5f5c5cfe2ee5a9967884fc70c7f213418a602fb2535621904f251f9b"} {
		if pinned.Hashes[name] != want {
			t.Fatalf("v0.4.0 %s hash differs from immutable initial definition", name)
		}
	}
}

func TestPublishedV050Pinned(t *testing.T) {
	pinned, ok := publishedInitialReleases["refs/tags/v0.5.0"]
	if !ok || pinned.Commit != "102315c3069273f7a8c3ad253c1f4ae01255f8c6" {
		t.Fatal("v0.5.0 exact release not protected")
	}
	for name, want := range map[string]string{"0001_init.up.sql": "491068d7577ca33a94dc9d3f74b02acdbc95662aa954c693b63a3fd7b196b12c", "0001_init.down.sql": "2f26c7dc5f5c5cfe2ee5a9967884fc70c7f213418a602fb2535621904f251f9b"} {
		if pinned.Hashes[name] != want {
			t.Fatalf("v0.5.0 %s hash differs from immutable initial definition", name)
		}
	}
}

func moveTagOutsideHistory(t *testing.T, root, tag string) {
	t.Helper()
	gitTest(t, root, "branch", "original-history")
	gitTest(t, root, "checkout", "--orphan", "unrelated")
	gitTest(t, root, "commit", "-m", "unreachable unrelated history")
	gitTest(t, root, "tag", "-f", tag)
	gitTest(t, root, "checkout", "original-history")
}

func TestPinnedLegacyReleaseCannotDisappearFromHistory(t *testing.T) {
	for _, tag := range []string{"v0.1.0", "v0.2.0"} {
		for _, mutation := range []string{"deleted", "unreachable"} {
			t.Run(tag+"/"+mutation, func(t *testing.T) {
				r := fixture(t, true)
				if mutation == "deleted" {
					gitTest(t, r.root, "tag", "-d", tag)
				} else {
					moveTagOutsideHistory(t, r.root, tag)
				}
				if err := r.check(); err == nil {
					t.Fatal("missing or unreachable pinned historical release escaped validation")
				}
			})
		}
	}
}

func TestCurrentInitialDiffersFromPublishedV040(t *testing.T) {
	if initialFrozen["0001_init.up.sql"] == publishedInitialReleases["refs/tags/v0.4.0"].Hashes["0001_init.up.sql"] {
		t.Fatal("current initial must retain its independent baseline")
	}
}

func TestCurrentInitialDiffersFromPublishedV050(t *testing.T) {
	if initialFrozen["0001_init.up.sql"] == publishedInitialReleases["refs/tags/v0.5.0"].Hashes["0001_init.up.sql"] {
		t.Fatal("current initial must retain its independent baseline")
	}
}

func TestPublishedV060Pinned(t *testing.T) {
	pinned, ok := publishedInitialReleases["refs/tags/v0.6.0"]
	if !ok || pinned.Commit != "ec6802cd221db3ce107e79bac92174bc35a0ba7d" {
		t.Fatal("v0.6.0 exact release not protected")
	}
	for name, want := range map[string]string{"0001_init.up.sql": "4c6b565e336aba88db5f6bf219407b026a7c1b438057b61470377005ed2509c5", "0001_init.down.sql": "2f26c7dc5f5c5cfe2ee5a9967884fc70c7f213418a602fb2535621904f251f9b"} {
		if pinned.Hashes[name] != want {
			t.Fatalf("v0.6.0 %s hash differs from immutable initial definition", name)
		}
	}
}

func TestCurrentInitialDiffersFromPublishedV060(t *testing.T) {
	for _, name := range []string{"0001_init.up.sql", "0001_init.down.sql"} {
		if initialFrozen[name] == publishedInitialReleases["refs/tags/v0.6.0"].Hashes[name] {
			t.Fatalf("current %s must retain its independent baseline", name)
		}
	}
}
