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
			gitTest(t, r.root, "tag", "-a", "v0.3.0", "-m", "release")
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
				"refs/tags/v0.3.0": {Commit: strings.TrimSpace(string(commit)), Hashes: oldHashes},
			}
			gitTest(t, r.root, "add", ".")
			gitTest(t, r.root, "commit", "-m", "authorized consolidation")
			switch mutation {
			case "moved tag":
				gitTest(t, r.root, "tag", "-f", "v0.3.0")
			case "unreachable tag":
				moveTagOutsideHistory(t, r.root, "v0.3.0")
			case "deleted tag":
				gitTest(t, r.root, "tag", "-d", "v0.3.0")
			case "wrong hash":
				oldHashes["0001_init.up.sql"] = currentHashes["0001_init.up.sql"]
			case "future old initial":
				write(t, r.root, "migrations/0001_init.up.sql", old)
				gitTest(t, r.root, "add", ".")
				gitTest(t, r.root, "commit", "-m", "bad new release")
				gitTest(t, r.root, "tag", "v0.4.0")
				write(t, r.root, "migrations/0001_init.up.sql", fixtureInitial["0001_init.up.sql"])
			case "future current initial":
				gitTest(t, r.root, "tag", "v0.4.0")
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
