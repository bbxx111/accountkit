package accountsvc_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

func TestServiceIntegrationEmailLifecycle(t *testing.T) {
	f := newIntegration(t)
	p := f.start(nil)
	f.call("GET", p.baseURL+"/readyz", "", nil, 200)
	f.call("GET", p.baseURL+"/healthz", "", nil, 200)
	f.call("GET", p.baseURL+"/v1/introspect", "", nil, 401)
	f.call("GET", p.baseURL+"/internal/v1/introspect", "", nil, 404)
	f.call("GET", p.baseURL+"/admin/v1/users", "", nil, 503)
	out := f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "PHONE", "target": "+8613812345678"}, 400)
	if errObj, _ := out["error"].(map[string]any); errObj["reason"] != "CHANNEL_NOT_ENABLED" {
		t.Fatal("SMS not explicitly disabled")
	}
	tokens := f.login(p, "lifecycle@example.test")
	access, refresh := tokens["access_token"].(string), tokens["refresh_token"].(string)
	f.introspect(p, access, true)
	rotated := f.call("POST", p.baseURL+"/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": refresh}, 200)
	replay := f.call("POST", p.baseURL+"/v1/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": refresh}, 200)
	if rotated["access_token"] != replay["access_token"] || rotated["refresh_token"] != replay["refresh_token"] {
		t.Fatal("refresh grace did not return the same pair")
	}
	access = rotated["access_token"].(string)
	f.secrets = append(f.secrets, access, rotated["refresh_token"].(string))
	// Restart retains the same storage/config and therefore the already-issued token.
	p.stop()
	p = f.start(nil)
	f.introspect(p, access, true)
	// Cooldown is shared across purposes; let the real Redis TTL expire.
	time.Sleep(1100 * time.Millisecond)
	f.call("POST", p.baseURL+"/v1/users/me:sendReauthenticationCode", access, map[string]string{"channel": "EMAIL", "target": "lifecycle@example.test"}, 200)
	m := f.smtp.mail(t)
	if !strings.Contains(m.body, "重新认证") {
		t.Fatal("wrong reauthentication email purpose")
	}
	f.secrets = append(f.secrets, m.code)
	reauth := f.call("POST", p.baseURL+"/v1/users/me:reauthenticate", access, map[string]any{"email": map[string]string{"code_id": f.codeID(m.target), "target": m.target, "code": m.code}}, 200)
	if _, ok := reauth["refresh_token"]; ok {
		t.Fatal("reauthentication unexpectedly returns refresh token")
	}
	recent := reauth["access_token"].(string)
	f.secrets = append(f.secrets, recent)
	deleted := f.call("DELETE", p.baseURL+"/v1/users/me", recent, nil, 200)
	if deleted["state"] != "PENDING_DELETION" {
		t.Fatalf("wrong lifecycle state: %v", deleted["state"])
	}
	f.introspect(p, recent, false)
	f.introspect(p, access, false)
	p.stop()
	f.assertAuditPrivate()
}

func TestServiceIntegrationAdminLifecycle(t *testing.T) {
	f := newIntegration(t)
	f.oidc()
	p := f.start(nil)
	tokens := f.login(p, "admin-subject@example.test")
	access := tokens["access_token"].(string)
	uid := tokens["user_id"].(string)
	op, su := f.env["FIXTURE_operator"], f.env["FIXTURE_super-admin"]
	f.call("GET", p.baseURL+"/admin/v1/users", access, nil, 401)
	f.call("GET", p.baseURL+"/admin/v1/users", f.secrets[1], nil, 401)
	f.call("GET", p.baseURL+"/v1/users/me", op, nil, 401)
	r, _ := http.NewRequest("POST", p.baseURL+"/v1/introspect", strings.NewReader(url.Values{"token": {access}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", "Bearer "+op)
	resp, err := f.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("admin token used as introspection caller")
	}
	f.call("GET", p.baseURL+"/admin/v1/users/"+uid, op, nil, 200)
	var identity string
	if err := f.pool.QueryRow(context.Background(), "SELECT id FROM identity WHERE user_id=$1", uid).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	revealPath := p.baseURL + "/admin/v1/users/" + uid + "/identities/" + identity + ":reveal"
	f.call("GET", revealPath, op, nil, 403)
	revealed := f.call("GET", revealPath, su, nil, 200)
	if revealed["subject"] != "admin-subject@example.test" {
		t.Fatal("authorized reveal missing subject")
	}
	frozen := f.call("POST", p.baseURL+"/admin/v1/users/"+uid+":freeze", op, map[string]string{"reason": "integration freeze"}, 200)
	if frozen["state"] != "FROZEN" {
		t.Fatal("admin freeze did not change state")
	}
	f.introspect(p, access, false)
	active := f.call("POST", p.baseURL+"/admin/v1/users/"+uid+":unfreeze", op, map[string]string{"reason": "integration unfreeze"}, 200)
	if active["state"] != "ACTIVE" {
		t.Fatal("admin unfreeze did not change state")
	}
	p.stop()
	f.assertAuditPrivate()
	var denied, reveals int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FILTER (WHERE event_type=$1), count(*) FILTER (WHERE event_type=$2) FROM audit_event", enum.EventAdminForbidden, enum.EventIdentityRevealed).Scan(&denied, &reveals); err != nil {
		t.Fatal(err)
	}
	if denied != 1 || reveals != 1 {
		t.Fatalf("incorrect admin auditing: denied=%d reveals=%d", denied, reveals)
	}
}

func TestServiceIntegrationDependencyFailure(t *testing.T) {
	f := newIntegration(t)
	f.env["ACCOUNTKIT_CODE_COOLDOWN"] = "1m"
	proxy := newRedisProxy(t, f.redisURL)
	u, _ := url.Parse(f.redisURL)
	u.Host = proxy.ln.Addr().String()
	p := f.start(map[string]string{"ACCOUNTSVC_REDIS_URL": u.String()})
	tokens := f.login(p, "dependency@example.test")
	access := tokens["access_token"].(string)
	proxy.fail(true)
	started := time.Now()
	f.call("GET", p.baseURL+"/readyz", "", nil, 503)
	if time.Since(started) > 3*time.Second {
		t.Fatal("readiness exceeded two-second budget with scheduling margin")
	}
	f.call("GET", p.baseURL+"/healthz", "", nil, 200)
	f.introspect(p, access, true)
	if !strings.Contains(p.output.String(), "fail-open") {
		t.Fatal("Redis fail-open warning missing")
	}
	proxy.fail(false)
	f.call("GET", p.baseURL+"/readyz", "", nil, 200)
	// SMTP rejection is isolated to send-code and must not make the process unready.
	f.smtp.reject.Store(true)
	f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": "failed-mail@example.test"}, 503)
	m := f.smtp.mail(t)
	f.secrets = append(f.secrets, m.target, m.code)
	f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": "failed-mail@example.test"}, 429)
	f.call("GET", p.baseURL+"/readyz", "", nil, 200)
	p.stop()
	f.assertAuditPrivate()
}

func (f *integrationFixture) command(extra map[string]string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.binary, args...)
	env := map[string]string{"ACCOUNTSVC_HTTP_ADDR": freeAddress(f.t)}
	for k, v := range extra {
		env[k] = v
	}
	cmd.Env = f.environment(env)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		f.t.Fatal("service command did not exit within the startup test budget")
	}
	f.assertSafe(string(out))
	return string(out), err
}

func TestServiceIntegrationStartupSafety(t *testing.T) {
	f := newIntegration(t)
	// Both processes start from an absent schema and use the same migration locks.
	minimal := map[string]string{"ACCOUNTSVC_SMTP_HOST": "", "ACCOUNTSVC_SMTP_PORT": "", "ACCOUNTSVC_SMTP_FROM": "", "ACCOUNTSVC_SMTP_USERNAME": "", "ACCOUNTSVC_SMTP_PASSWORD": "", "ACCOUNTSVC_INTROSPECTION_CLIENTS": "", "ACCOUNTSVC_TLS_CERT_FILE": "", "ACCOUNTSVC_TLS_KEY_FILE": ""}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := f.command(minimal, "migrate"); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
	if _, err := f.command(minimal, "migrate"); err != nil {
		t.Fatal("repeat migrate failed")
	}
	var version int
	var dirty bool
	if err := f.pool.QueryRow(context.Background(), "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 1 || dirty {
		t.Fatalf("migration state version=%d dirty=%v err=%v", version, dirty, err)
	}
	p := f.start(nil)
	other := f.start(nil)
	tokens := f.login(p, "unknown-key@example.test")
	f.introspect(other, tokens["access_token"].(string), true)
	other.stop()
	p.stop()
	if _, err := f.pool.Exec(context.Background(), "UPDATE identity SET cipher_key_version=99"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.command(nil, "serve"); err == nil {
		t.Fatal("unknown key version started service")
	}
	var keyVersion int
	if err := f.pool.QueryRow(context.Background(), "SELECT cipher_key_version FROM identity LIMIT 1").Scan(&keyVersion); err != nil || keyVersion != 99 {
		t.Fatal("unknown key state was mutated")
	}
	if _, err := f.pool.Exec(context.Background(), "UPDATE schema_migrations SET dirty=true"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.command(nil, "migrate"); err == nil {
		t.Fatal("dirty migration succeeded")
	}
	if err := f.pool.QueryRow(context.Background(), "SELECT dirty FROM schema_migrations").Scan(&dirty); err != nil || !dirty {
		t.Fatal("failed startup cleared dirty")
	}
}

func (f *integrationFixture) assertAuditPrivate() {
	f.t.Helper()
	var all string
	if err := f.pool.QueryRow(context.Background(), "SELECT COALESCE(json_agg(a)::text,'[]') FROM audit_event a").Scan(&all); err != nil {
		f.t.Fatal(err)
	}
	if all == "[]" {
		f.t.Fatal("no audit events persisted on graceful exit")
	}
	f.assertSafe(all)
}

func TestServiceProcessSIGTERM(t *testing.T) {
	requireServiceIntegration(t)
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("timeout_%v", timeout), func(t *testing.T) {
			f := newIntegration(t)
			p := f.start(map[string]string{"ACCOUNTSVC_SHUTDOWN_TIMEOUT": "15.3s"})
			f.smtp.hold.Store(true)
			requestDone := make(chan error, 1)
			go func() {
				req, _ := http.NewRequest("POST", p.baseURL+"/v1/users:sendSignInCode", strings.NewReader(`{"channel":"EMAIL","target":"drain@example.test"}`))
				req.Header.Set("Content-Type", "application/json")
				resp, err := f.client.Do(req)
				if err == nil {
					resp.Body.Close()
					if !timeout && resp.StatusCode != 200 {
						err = fmt.Errorf("drained request status %d", resp.StatusCode)
					}
				}
				requestDone <- err
			}()
			select {
			case <-f.smtp.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not reach SMTP DATA")
			}
			m := f.smtp.mail(t)
			f.secrets = append(f.secrets, m.target, m.code)
			started := time.Now()
			p.signal()
			if !timeout {
				f.smtp.hold.Store(false)
				f.smtp.release <- struct{}{}
			}
			p.wait(!timeout, 18*time.Second)
			if time.Since(started) > 17*time.Second {
				t.Fatal("process shutdown violated total bound")
			}
			select {
			case err := <-requestDone:
				if !timeout && err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("HTTP request not terminated")
			}
			if !timeout {
				f.assertAuditPrivate()
			}
		})
	}
}
