package accountsvc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bbxx111/accountkit/enum"
)

func TestServiceIntegrationIdentityReplacement(t *testing.T) {
	f := newIntegration(t)
	f.env["ACCOUNTKIT_REAUTH_MAX_AGE"] = "5s"
	p := f.start(nil)
	const oldTarget, newTarget = "replace-old@example.test", "replace-new@example.test"
	f.secrets = append(f.secrets, oldTarget, newTarget)
	rememberPair := func(pair map[string]any) {
		f.secrets = append(f.secrets, pair["access_token"].(string), pair["refresh_token"].(string))
	}
	privateResponse := func(out map[string]any) {
		t.Helper()
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		f.assertSafe(string(raw))
	}
	mail := func(target, purpose string) string {
		t.Helper()
		m := f.smtp.mail(t)
		f.secrets = append(f.secrets, m.target, m.code)
		if m.target != target || m.code == "" || !strings.Contains(m.body, purpose) {
			t.Fatal("SMTP recipient or verification purpose mismatch")
		}
		return m.code
	}
	login := func(target, device string) map[string]any {
		t.Helper()
		f.call("POST", p.public+"/v1/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": target}, 200)
		code := mail(target, "登录")
		out := replacementDeviceSignIn(t, f, p, target, code, device)
		rememberPair(out)
		return out
	}
	current := login(oldTarget, "replacement-current")
	uid, access, refresh := current["user_id"].(string), current["access_token"].(string), current["refresh_token"].(string)
	time.Sleep(1100 * time.Millisecond)
	other := login(oldTarget, "replacement-other")
	if other["user_id"] != uid {
		t.Fatal("second device entered a different account")
	}
	previousOther := other["refresh_token"].(string)
	rotatedOther := f.call("POST", p.public+"/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": previousOther}, 200)
	rememberPair(rotatedOther)
	f.introspect(p, access, true)
	f.introspect(p, other["access_token"].(string), true)
	f.introspect(p, rotatedOther["access_token"].(string), true)
	list := f.call("GET", p.public+"/v1/users/me/identities", access, nil, 200)
	privateResponse(list)
	identities := list["identities"].([]any)
	if len(identities) != 1 {
		t.Fatal("expected one original email")
	}
	oldName := identities[0].(map[string]any)["name"].(string)
	oldID := oldName[strings.LastIndex(oldName, "/")+1:]
	path := "/v1/users/me/identities/" + oldID + ":replace"
	f.call("POST", p.public+"/v1/users/me:sendBindCode", access, map[string]string{"channel": "EMAIL", "target": newTarget}, 200)
	bindCode := mail(newTarget, "绑定")
	body := map[string]any{"email": map[string]string{"target": newTarget, "code": bindCode}}
	time.Sleep(6 * time.Second)
	rejected := f.call("POST", p.public+path, access, body, 400)
	privateResponse(rejected)
	if rejected["error"].(map[string]any)["reason"] != "REAUTHENTICATION_REQUIRED" {
		t.Fatal("expired auth did not require reauthentication")
	}
	f.call("POST", p.public+"/v1/users/me:sendReauthenticationCode", access, map[string]string{"channel": "EMAIL", "target": oldTarget}, 200)
	reauthCode := mail(oldTarget, "重新认证")
	reauth := f.call("POST", p.public+"/v1/users/me:reauthenticate", access, map[string]any{"email": map[string]string{"target": oldTarget, "code": reauthCode}}, 200)
	if _, ok := reauth["refresh_token"]; ok {
		t.Fatal("reauthentication returned unexpected refresh token")
	}
	access = reauth["access_token"].(string)
	f.secrets = append(f.secrets, access)
	before := replacementClaims(t, access)
	newIdentity := f.call("POST", p.public+path, access, body, 200)
	privateResponse(newIdentity)
	newName, ok := newIdentity["name"].(string)
	if !ok || newName == oldName || newIdentity["kind"] != "EMAIL" || newIdentity["masked_subject"] == "" || newIdentity["create_time"] == nil || len(newIdentity) != 4 {
		t.Fatal("replacement response is not the new masked identity resource")
	}
	privateResponse(f.call("POST", p.public+path, access, body, 404))
	list = f.call("GET", p.public+"/v1/users/me/identities", access, nil, 200)
	privateResponse(list)
	identities = list["identities"].([]any)
	if len(identities) != 1 || identities[0].(map[string]any)["name"] != newName {
		t.Fatal("list does not confirm replacement")
	}
	me := f.call("GET", p.public+"/v1/users/me", access, nil, 200)
	if me["name"] != "users/"+uid {
		t.Fatal("replacement changed account")
	}
	f.introspect(p, access, true)
	for _, token := range []string{other["access_token"].(string), rotatedOther["access_token"].(string)} {
		privateResponse(f.call("GET", p.public+"/v1/users/me", token, nil, 401))
		f.introspect(p, token, false)
	}
	for _, token := range []string{previousOther, rotatedOther["refresh_token"].(string)} {
		out := f.call("POST", p.public+"/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": token}, 400)
		privateResponse(out)
		if out["error"] != "invalid_grant" {
			t.Fatal("other-device refresh was not revoked")
		}
	}
	retained := f.call("POST", p.public+"/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": refresh}, 200)
	rememberPair(retained)
	access = retained["access_token"].(string)
	after := replacementClaims(t, access)
	if after["sub"] != uid || after["sid"] != before["sid"] || after["auth_time"] != before["auth_time"] {
		t.Fatal("replacement/refresh changed current session/auth_time")
	}
	f.introspect(p, access, true)
	// Restart both HTTP listeners using the same database, Redis prefix and keys.
	p.stop()
	f.assertAuditPrivate()
	p = f.start(nil)
	f.introspect(p, access, true)
	f.introspect(p, rotatedOther["access_token"].(string), false)
	list = f.call("GET", p.public+"/v1/users/me/identities", access, nil, 200)
	privateResponse(list)
	identities = list["identities"].([]any)
	if len(identities) != 1 || identities[0].(map[string]any)["name"] != newName {
		t.Fatal("new identity not retained across service restart")
	}
	retained = f.call("POST", p.public+"/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": retained["refresh_token"].(string)}, 200)
	rememberPair(retained)
	if claims := replacementClaims(t, retained["access_token"].(string)); claims["sid"] != before["sid"] || claims["auth_time"] != before["auth_time"] {
		t.Fatal("current session not retained after restart")
	}
	newLogin := login(newTarget, "replacement-new-login")
	if newLogin["user_id"] != uid || newLogin["is_new_user"] != false {
		t.Fatal("new address did not enter original account after restart")
	}
	// Code cooldown is shared across purposes and follows the real Redis clock.
	time.Sleep(1100 * time.Millisecond)
	oldLogin := login(oldTarget, "replacement-old-login")
	if oldLogin["user_id"] == uid || oldLogin["is_new_user"] != true {
		t.Fatal("released address entered original account")
	}
	sms := f.call("POST", p.public+"/v1/users:sendSignInCode", "", map[string]string{"channel": "PHONE", "target": "+8613812345678"}, 400)
	if sms["error"].(map[string]any)["reason"] != "CHANNEL_NOT_ENABLED" {
		t.Fatal("service SMS disabled behavior changed")
	}
	p.stop()
	f.assertAuditPrivate()
	var linked int
	auditCtx, auditCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer auditCancel()
	if err := f.pool.QueryRow(auditCtx, "SELECT count(*) FROM audit_event WHERE user_id=$1 AND request_id=$2 AND reason='IDENTITY_REPLACED' AND event_type IN ($3,$4,$5)", uid, "svc-integration", enum.EventIdentityBound, enum.EventIdentityUnbound, enum.EventSessionRevoked).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 3 {
		t.Fatalf("expected correlated bind/unbind/session revoke audit; got %d", linked)
	}
}

// f.call intentionally uses one fixed device; sign-in needs an explicit device
// here so another login cannot accidentally revoke the current session first.
func replacementDeviceSignIn(t *testing.T, f *integrationFixture, p *serviceProcess, target, code, device string) map[string]any {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"email": map[string]string{"target": target, "code": code}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest("POST", p.public+"/v1/users:signInWithCode", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Device-Id", device)
	r.Header.Set("X-Request-Id", "svc-integration")
	resp, err := f.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("device sign-in expected 200 got %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal("invalid sign-in JSON")
	}
	return out
}

func replacementClaims(t *testing.T, token string) jwt.MapClaims {
	t.Helper()
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		t.Fatal("invalid JWT response")
	}
	if sid, ok := claims["sid"].(string); !ok || sid == "" {
		t.Fatal("JWT response missing session claim")
	}
	if authTime, ok := claims["auth_time"].(float64); !ok || authTime <= 0 {
		t.Fatal("JWT response missing authentication time")
	}
	return claims
}
