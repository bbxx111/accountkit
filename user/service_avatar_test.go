package user_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user"
)

func avatarPointer(s string) *string { return &s }

func TestAvatarProfileUpdate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
	initial, err := f.svc.GetMe(ctx, id)
	if err != nil || initial.AvatarURL != "" {
		t.Fatalf("initial: %+v %v", initial, err)
	}
	m, err := f.svc.UpdateProfile(ctx, id, user.ProfilePatch{DisplayName: avatarPointer(" new "), AvatarURL: avatarPointer(" https://example.com/a?q=one ")})
	if err != nil || m.DisplayName != "new" || m.AvatarURL != "https://example.com/a?q=one" {
		t.Fatalf("set: %+v %v", m, err)
	}
	for _, bad := range []string{"http://example.com/a", "/relative", "data:image/png;base64,a", "https:///a", "https://user:secret@example.com/a", "https://example.com/#fragment", "https://example.com/#", "https://example.com/a\\b", "https://example.com/a b", "https://example.com/a\tb", "https://example.com/\u00a0x", string([]byte{0xff}), "https://example.com/" + strings.Repeat("x", 2029)} {
		before, _ := user.TestRepoQueries(f.repo).GetUserByID(ctx, id)
		_, err = f.svc.UpdateProfile(ctx, id, user.ProfilePatch{DisplayName: avatarPointer("wrong"), AvatarURL: avatarPointer(bad)})
		if !errors.Is(err, user.ErrInvalidArgument) || strings.Contains(err.Error(), bad) {
			t.Fatalf("invalid avatar: %q: %v", bad, err)
		}
		after, _ := user.TestRepoQueries(f.repo).GetUserByID(ctx, id)
		if !after.UpdateTime.Equal(before.UpdateTime) || *after.DisplayName != "new" || *after.AvatarUrl != "https://example.com/a?q=one" {
			t.Fatal("invalid patch wrote fields/time")
		}
	}
	max := "https://example.com/" + strings.Repeat("x", 2028)
	if len(max) != 2048 {
		t.Fatal("boundary fixture")
	}
	if _, err = f.svc.UpdateProfile(ctx, id, user.ProfilePatch{AvatarURL: &max}); err != nil {
		t.Fatal("2048-byte avatar", err)
	}
	if _, err = f.svc.UpdateProfile(ctx, id, user.ProfilePatch{AvatarURL: avatarPointer("")}); err != nil {
		t.Fatal(err)
	}
	row, _ := user.TestRepoQueries(f.repo).GetUserByID(ctx, id)
	if row.AvatarUrl != nil || *row.DisplayName != "new" {
		t.Fatal("clear/omission")
	}
	if _, err = f.svc.UpdateProfile(ctx, id, user.ProfilePatch{}); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatal("empty patch", err)
	}
	if _, err = f.svc.UpdateProfile(ctx, "bad", user.ProfilePatch{AvatarURL: avatarPointer("")}); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatal("invalid id", err)
	}
	unknown, _ := ids.New(ids.User)
	if _, err = f.svc.UpdateProfile(ctx, unknown, user.ProfilePatch{AvatarURL: avatarPointer("")}); !errors.Is(err, user.ErrNotFound) {
		t.Fatal("unknown id", err)
	}
	if _, err = f.svc.UpdateProfile(ctx, id, user.ProfilePatch{DisplayName: avatarPointer(strings.Repeat("x", 129)), AvatarURL: avatarPointer("https://example.com/a")}); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatal("invalid name", err)
	}
}
