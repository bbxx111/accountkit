//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

// These domain smoke checks prove fixture policies, not SDK contracts.
func fixtureCredential(t *testing.T, h *host, subject string) user.IdpCredential {
	t.Helper()
	resp, e := http.Get(h.origin + "/__fixture/credential?kind=TEST_OIDC&subject=" + url.QueryEscape(subject) + "&nonce=" + url.QueryEscape(fmt.Sprintf("nonce-%d", time.Now().UnixNano())))
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	var wire struct {
		Kind       enum.IdentityKind `json:"kind"`
		Credential json.RawMessage   `json:"credential"`
	}
	if e = json.NewDecoder(resp.Body).Decode(&wire); e != nil || resp.StatusCode != 200 {
		t.Fatal("fixture credential unavailable")
	}
	return user.IdpCredential{Kind: wire.Kind, Credential: wire.Credential}
}
func fixtureCode(t *testing.T, h *host, target, purpose string) string {
	t.Helper()
	resp, e := http.Get(h.origin + "/__fixture/code?target=" + url.QueryEscape(target) + "&purpose=" + purpose)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	var wire struct {
		Code string `json:"code"`
	}
	if e = json.NewDecoder(resp.Body).Decode(&wire); e != nil || len(wire.Code) != 6 {
		t.Fatal("fixture code unavailable")
	}
	return wire.Code
}
func TestEmbeddedPolicyFixturesRequired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	base := options{database: os.Getenv("SDK_FIXTURE_DATABASE_URL"), redis: os.Getenv("SDK_FIXTURE_REDIS_ADDR"), addr: "127.0.0.1:0", schema: "sdk_fixture_policy_" + unique, redisPrefix: "sdk_fixture_policy_" + unique + ":", profile: "custom-extended", requiredKinds: "EMAIL"}
	h, e := start(ctx, base)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if h != nil {
			h.close()
		}
	}()
	if h.auth.Config().ProfileAttributes == nil {
		t.Fatal("extended profile missing")
	}
	resp, e := http.Get(h.origin + "/custom/account/users/me")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("custom prefix not mounted: %d", resp.StatusCode)
	}
	meta := user.Meta{IP: "127.0.0.1", RequestID: "fixture-policy"}
	device := user.Device{ID: "fixture-policy-device", Name: "Synthetic"}
	svc := h.auth.Users()
	pending, e := svc.SignInWithIdp(ctx, fixtureCredential(t, h, "pending-"+unique), device, meta)
	if e != nil {
		t.Fatal(e)
	}
	if pending.Pending == nil || pending.Tokens != nil {
		t.Fatal("required EMAIL did not produce pending")
	}
	email := "pending-" + unique + "@example.test"
	challenge, e := svc.SendSignInCompletionCode(ctx, pending.Pending.SignInCompletionToken, enum.IdentityEmail, email, device, meta)
	if e != nil {
		t.Fatal(e)
	}
	completed, e := svc.CompleteSignIn(ctx, pending.Pending.SignInCompletionToken, user.CodeCredential{Channel: enum.IdentityEmail, Target: email, CodeID: challenge.CodeID, Code: fixtureCode(t, h, email, "SIGN_IN_COMPLETE")}, device, meta)
	if e != nil {
		t.Fatal(e)
	}
	if completed.Tokens == nil || completed.Tokens.Scope != user.ScopeUser {
		t.Fatal("pending completion did not produce full user scope")
	}
	h.close()
	h = nil
	base.requiredKinds = "[]"
	base.schema = "sdk_fixture_binding_" + unique
	base.redisPrefix = "sdk_fixture_binding_" + unique + ":"
	h, e = start(ctx, base)
	if e != nil {
		t.Fatal(e)
	}
	seed, e := h.auth.Users().SignInWithIdp(ctx, fixtureCredential(t, h, "binding-"+unique), device, meta)
	if e != nil || seed.Tokens == nil || seed.Tokens.Scope != user.ScopeUser {
		t.Fatal("empty required policy did not seed third party account")
	}
	h.close()
	h = nil
	base.requiredKinds = "EMAIL"
	h, e = start(ctx, base)
	if e != nil {
		t.Fatal(e)
	}
	svc = h.auth.Users()
	needs, e := svc.SignInWithIdp(ctx, fixtureCredential(t, h, "binding-"+unique), device, meta)
	if e != nil {
		t.Fatal(e)
	}
	if needs.Tokens == nil || needs.Tokens.Scope != user.ScopeBind || needs.Tokens.UserID != seed.Tokens.UserID {
		t.Fatal("required EMAIL restart did not produce NeedsBinding scope for same account")
	}
	principal, e := svc.AuthenticateStrict(ctx, needs.Tokens.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	email = "binding-" + unique + "@example.test"
	challenge, e = svc.SendBindCode(ctx, principal, enum.IdentityEmail, email, meta)
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = svc.BindWithCode(ctx, principal, user.CodeCredential{Channel: enum.IdentityEmail, Target: email, CodeID: challenge.CodeID, Code: fixtureCode(t, h, email, "BIND")}, meta)
	if e != nil {
		t.Fatal(e)
	}
	synced, e := svc.Refresh(ctx, needs.Tokens.RefreshToken, meta)
	if e != nil || synced.Scope != user.ScopeUser {
		t.Fatal("bound EMAIL refresh did not synchronize full user scope")
	}
}
