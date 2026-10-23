package user_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

func metadataProvider() idp.Provider {
	return adaptedProvider{kind: enum.IdentityWeChat, fp: "wechat-metadata-fixed-openid", verify: func(_ context.Context, f map[string]string) (idp.Identity, error) {
		return idp.Identity{Kind: enum.IdentityWeChat, Subject: "openid:wx1:o1", UnionID: f["unionid"], OpenIDs: map[string]string{"wx1": f["openid"]}, Name: f["name"]}, nil
	}}
}
func metadataCred(union, open, name string) user.IdpCredential {
	return providerCred(enum.IdentityWeChat, map[string]string{"unionid": union, "openid": open, "name": name})
}
func useMetadataProvider(t *testing.T, f *fixture, required []enum.IdentityKind) {
	newServiceWith(t, f, func(d *user.Deps) { *d = withProvider(*d, metadataProvider()); d.RequiredIdentityKinds = required })
}
func assertMetadata(t *testing.T, f *fixture, uid string) string {
	t.Helper()
	rows, err := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Kind != enum.IdentityWeChat {
			continue
		}
		var pm struct {
			Union   string            `json:"unionid"`
			Open    map[string]string `json:"openids"`
			Profile struct {
				Name string `json:"name"`
			} `json:"profile"`
		}
		if err := json.Unmarshal(row.ProviderMeta, &pm); err != nil {
			t.Fatal(err)
		}
		if pm.Union != "u1" || pm.Open["wx1"] != "o1" || pm.Profile.Name != "Person" || row.ProviderSubject == nil || *row.ProviderSubject != "openid:wx1:o1" {
			t.Fatalf("metadata lost: %s", row.ProviderMeta)
		}
		return row.ID
	}
	t.Fatal("missing wechat identity")
	return ""
}
func TestWeChatMetadataPersistence(t *testing.T) {
	ctx := context.Background()
	t.Run("direct registration and refresh rollback", func(t *testing.T) {
		f := newFixture(t)
		useMetadataProvider(t, f, []enum.IdentityKind{})
		first, err := f.svc.SignInWithIdp(ctx, metadataCred("", "o1", "Person"), dev1, meta1)
		if err != nil {
			t.Fatal(err)
		}
		uid := requireSignInTokens(t, first).UserID
		rows, _ := f.repo.Q().ListActiveIdentitiesByUser(ctx, uid)
		iid := rows[0].ID
		if _, err = f.svc.SignInWithIdp(ctx, metadataCred("u1", "o1", ""), dev1, meta1); err != nil {
			t.Fatal(err)
		}
		if got := assertMetadata(t, f, uid); got != iid {
			t.Fatal("identity changed")
		}
		if _, err = f.svc.SignInWithIdp(ctx, metadataCred("", "", ""), dev1, meta1); err != nil {
			t.Fatal(err)
		}
		assertMetadata(t, f, uid)
		var before int
		f.pool.QueryRow(ctx, "SELECT count(*) FROM session").Scan(&before)
		for _, cred := range []user.IdpCredential{metadataCred("u2", "o1", "overwrite"), metadataCred("u1", "different", "overwrite")} {
			if _, err = f.svc.SignInWithIdp(ctx, cred, user.Device{ID: "22222222-2222-2222-2222-222222222222"}, meta1); !errors.Is(err, user.ErrIdentityConflict) {
				t.Fatalf("conflict: %v", err)
			}
			assertMetadata(t, f, uid)
		}
		var after int
		f.pool.QueryRow(ctx, "SELECT count(*) FROM session").Scan(&after)
		if before != after {
			t.Fatal("conflict created session")
		}
	})
	t.Run("pending completion", func(t *testing.T) {
		f := newFixture(t)
		useMetadataProvider(t, f, []enum.IdentityKind{enum.IdentityEmail})
		r, err := f.svc.SignInWithIdp(ctx, metadataCred("u1", "o1", "Person"), dev1, meta1)
		if err != nil || r.Pending == nil {
			t.Fatalf("pending: %+v %v", r, err)
		}
		cred := completionCredential(t, f, r.Pending, enum.IdentityEmail, email1)
		r, err = f.svc.CompleteSignIn(ctx, r.Pending.SignInCompletionToken, cred, dev1, meta1)
		if err != nil {
			t.Fatal(err)
		}
		assertMetadata(t, f, requireSignInTokens(t, r).UserID)
	})
	t.Run("bind and conflict rollback", func(t *testing.T) {
		f := newFixture(t)
		useMetadataProvider(t, f, nil)
		p := principalOf(t, f, f.signIn(t, enum.IdentityEmail, email1, dev1))
		info, created, err := f.svc.BindWithIdp(ctx, p, metadataCred("u1", "o1", "Person"), meta1)
		if err != nil || !created {
			t.Fatalf("bind: %v %v", created, err)
		}
		if assertMetadata(t, f, p.UserID) != info.ID {
			t.Fatal("wrong identity")
		}
		if _, _, err = f.svc.BindWithIdp(ctx, p, metadataCred("u2", "o1", "overwrite"), meta1); !errors.Is(err, user.ErrIdentityConflict) {
			t.Fatalf("bind conflict: %v", err)
		}
		assertMetadata(t, f, p.UserID)
	})
	t.Run("concurrent exact subject", func(t *testing.T) {
		f := newFixture(t)
		useMetadataProvider(t, f, []enum.IdentityKind{})
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, err := f.svc.SignInWithIdp(ctx, metadataCred("u1", "o1", "Person"), user.Device{ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)}, meta1)
				errs <- err
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Error(err)
			}
		}
		var n int
		if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM identity WHERE kind='WECHAT' AND provider_subject='openid:wx1:o1'").Scan(&n); err != nil || n != 1 {
			t.Fatalf("identity count %d: %v", n, err)
		}
	})
}

func TestWeChatPendingConfigurationFingerprint(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"openid":"o1","unionid":"u1","session_key":"must-not-persist"}`)
	}))
	defer server.Close()
	for _, change := range []string{"mode", "group"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			config := idp.WeChatConfig{MiniProgramApps: []idp.App{{AppID: "wx1", Secret: "secret"}}, SubjectMode: idp.WeChatSubjectModeOpenID, BaseURL: server.URL}
			install := func() {
				p, err := idp.NewWeChatWithConfig(config, server.Client(), nil)
				if err != nil {
					t.Fatal(err)
				}
				newServiceWith(t, f, func(d *user.Deps) {
					*d = withProvider(*d, p)
					d.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
				})
			}
			install()
			r, err := f.svc.SignInWithIdp(ctx, wechatCred("wx1", "valid-code"), dev1, meta1)
			if err != nil || r.Pending == nil {
				t.Fatalf("pending: %+v %v", r, err)
			}
			if change == "mode" {
				config.SubjectMode = idp.WeChatSubjectModeUnionID
			} else {
				config.Apps = config.MiniProgramApps
				config.MiniProgramApps = nil
			}
			install()
			if _, err = f.svc.SendSignInCompletionCode(ctx, r.Pending.SignInCompletionToken, enum.IdentityEmail, email1, user.Device{}, meta1); !errors.Is(err, user.ErrSignInCompletionInvalid) {
				t.Fatalf("changed %s accepted: %v", change, err)
			}
		})
	}
}
