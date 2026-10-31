package accountkit_test

import (
	"encoding/json"
	"github.com/bbxx111/accountkit/internal/userdb"
	"strings"
	"testing"
)

func TestAvatarHTTPIntegration(t *testing.T) {
	a, pool, sent, _ := profileHTTPFixture(t)
	token := policyEmailSignIn(t, a, sent, "avatar-http@example.test", "avatar-http").Tokens
	h := a.EndUserHandler()
	call := func(body string) int {
		w := profileHTTPRequest(t.Context(), h, "PATCH", "/users/me", body, token.AccessToken, "avatar-update")
		if w.Code == 200 {
			var out map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if _, ok := out["avatar_url"]; !ok {
				t.Fatal("missing avatar response")
			}
		}
		return w.Code
	}
	if status := call(`{"display_name":"name","avatar_url":" https://example.com/a?size=64 "}`); status != 200 {
		t.Fatal(status)
	}
	row, err := db.New(pool).GetUserByID(t.Context(), token.UserID)
	if err != nil || row.AvatarUrl == nil || *row.AvatarUrl != "https://example.com/a?size=64" || row.DisplayName == nil || *row.DisplayName != "name" {
		t.Fatalf("persisted %+v %v", row, err)
	}
	for _, body := range []string{`{"display_name":"changed","avatar_url":"http://example.com/private"}`, `{"avatar_url":"https://example.com/` + strings.Repeat("x", 2029) + `"}`, "{\"avatar_url\":\"https://example.com/\xff\"}", `{"avatar_url":"https://example.com/\ud800"}`} {
		if status := call(body); status != 400 {
			t.Fatalf("invalid status %d", status)
		}
		after, _ := db.New(pool).GetUserByID(t.Context(), token.UserID)
		if !after.UpdateTime.Equal(row.UpdateTime) || *after.DisplayName != "name" || *after.AvatarUrl != *row.AvatarUrl {
			t.Fatal("invalid request changed account")
		}
	}
	if status := call(`{"avatar_url":null,"display_name":"other"}`); status != 200 {
		t.Fatal(status)
	}
	m, err := a.Users().GetMe(t.Context(), token.UserID)
	if err != nil || m.AvatarURL != "https://example.com/a?size=64" || m.DisplayName != "other" {
		t.Fatal("null semantics", err)
	}
	if status := call(`{"avatar_url":""}`); status != 200 {
		t.Fatal(status)
	}
	row, _ = db.New(pool).GetUserByID(t.Context(), token.UserID)
	if row.AvatarUrl != nil || *row.DisplayName != "other" {
		t.Fatal("clear semantics")
	}
}
