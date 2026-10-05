package accountsvc_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/pii"
	"github.com/redis/go-redis/v9"
)

// Catch active-only verification lookup, split target limits, duplicate IP
// increments and duplicate registration through actual accountsvc processes.
func TestServiceIntegrationCodeKeyRotation(t *testing.T) {
	f := newIntegration(t)
	key2 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	f.secrets = append(f.secrets, key2)
	f.env["ACCOUNTKIT_SUBJECT_HMAC_KEYS"] += ",2:" + key2
	f.env["ACCOUNTKIT_CODE_COOLDOWN"] = "2s"
	f.env["ACCOUNTKIT_CODE_DAILY_LIMIT_PER_TARGET"] = "3"
	f.env["ACCOUNTKIT_CODE_DAILY_LIMIT_PER_IP"] = "5"
	keys, err := pii.ParseKeyList(f.env["ACCOUNTKIT_SUBJECT_HMAC_KEYS"])
	if err != nil {
		t.Fatal("invalid synthetic rotation keys")
	}
	digester, err := pii.NewDigester(keys, 1)
	if err != nil {
		t.Fatal("invalid synthetic rotation digester")
	}
	// This scenario checks cumulative quotas within one UTC day. Start it
	// only when its one-minute deadline fits, with 30 seconds of headroom.
	// Near midnight the wait is bounded by 90 seconds; never skip the gate.
	start := time.Now()
	for delay := rotationQuotaStartDelay(start); delay > 0; delay = rotationQuotaStartDelay(start) {
		t.Log("waiting for a full UTC quota test window")
		time.Sleep(delay)
		start = time.Now()
	}
	ctx, cancel := context.WithDeadline(context.Background(), start.Add(time.Minute))
	defer cancel()
	day := start.UTC().Format("20060102")
	ipKey := f.prefix + "quota:EMAIL:ip:127.0.0.1:" + day
	targetKeys := func(target string) []string {
		var out []string
		for _, digest := range digester.AllDigests(target) {
			out = append(out, f.prefix+"quota:EMAIL:target:"+digest+":"+day)
		}
		return out
	}
	assertCounts := func(target string, wantK1, wantK2, wantIP int64) {
		t.Helper()
		all := append(targetKeys(target), ipKey)
		for i, want := range []int64{wantK1, wantK2, wantIP} {
			got, err := f.redis.Get(ctx, all[i]).Int64()
			if errors.Is(err, redis.Nil) && want == 0 {
				continue
			}
			if err != nil || got != want {
				t.Fatalf("rotation counter %d: got=%d want=%d (read failed=%v)", i, got, want, err != nil)
			}
		}
	}
	waitCooldown := func(target string) {
		t.Helper()
		// Poll only the two exact owned keys, allowing Redis's real clock to
		// expire them rather than using an arbitrary fixed sleep.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			live := false
			for _, digest := range digester.AllDigests(target) {
				ttl, err := f.redis.PTTL(ctx, f.prefix+"cooldown:EMAIL:"+digest).Result()
				if err != nil {
					t.Fatal("read rotation cooldown failed")
				}
				if ttl == -1 {
					t.Fatal("rotation cooldown has no expiry")
				}
				live = live || ttl >= 0
			}
			if !live {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("rotation cooldown did not expire within test bound")
	}
	send := func(p *serviceProcess, target string) string {
		t.Helper()
		f.secrets = append(f.secrets, target)
		f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": target}, 200)
		mail := f.smtp.mail(t)
		f.secrets = append(f.secrets, mail.target, mail.code)
		if mail.target != target || !strings.Contains(mail.body, "登录") {
			t.Fatal("rotation SMTP recipient or purpose mismatch")
		}
		return mail.code
	}
	signIn := func(p *serviceProcess, target, code string) map[string]any {
		t.Helper()
		out := f.call("POST", p.baseURL+"/v1/users:signInWithCode", "", map[string]any{"email": map[string]string{"target": target, "code": code}}, 200)
		for _, field := range []string{"access_token", "refresh_token"} {
			token, ok := out[field].(string)
			if !ok || token == "" {
				t.Fatal("rotation sign-in did not return a token pair")
			}
			f.secrets = append(f.secrets, token)
		}
		f.introspect(p, out["access_token"].(string), true)
		return out
	}
	limited := func(p *serviceProcess, target, reason string) {
		t.Helper()
		f.secrets = append(f.secrets, target)
		out := f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": target}, 429)
		errorBody, ok := out["error"].(map[string]any)
		if !ok || errorBody["status"] != "RESOURCE_EXHAUSTED" || errorBody["reason"] != reason {
			t.Fatal("rotation rate limit reason changed")
		}
		if retry, ok := errorBody["retry_after_seconds"].(float64); !ok || retry < 1 {
			t.Fatal("rotation rate limit retry delay missing")
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal("encode rotation error response failed")
		}
		f.assertSafe(string(raw))
		select {
		case <-f.smtp.messages:
			t.Fatal("rate-limited rotation request delivered mail")
		default:
		}
	}

	// All processes first receive the same complete key set; only active
	// differs. JWT/cipher keys, policies, schema and Redis prefix stay equal.
	p1 := f.start(map[string]string{"ACCOUNTKIT_SUBJECT_HMAC_ACTIVE_KEY": "1"})
	p2 := f.start(map[string]string{"ACCOUNTKIT_SUBJECT_HMAC_ACTIVE_KEY": "2"})
	t.Logf("started actual accountsvc processes: K1 pid=%d, K2 pid=%d", p1.cmd.Process.Pid, p2.cmd.Process.Pid)
	const target = "rotation-service@example.test"
	code1 := send(p1, target)
	limited(p2, target, "COOLDOWN")
	assertCounts(target, 1, 0, 1)
	first := signIn(p2, target, code1)
	uid, ok := first["user_id"].(string)
	if !ok || uid == "" || first["is_new_user"] != true {
		t.Fatal("first cross-active sign-in did not create the original account")
	}
	waitCooldown(target)
	code2 := send(p2, target)
	limited(p1, target, "COOLDOWN")
	assertCounts(target, 1, 1, 2)
	second := signIn(p1, target, code2)
	if second["user_id"] != uid || second["is_new_user"] != false {
		t.Fatal("reverse cross-active sign-in did not reuse original account")
	}

	waitCooldown(target)
	oldCode := send(p1, target)
	assertCounts(target, 2, 1, 3)
	p1.stop()
	p1 = f.start(map[string]string{"ACCOUNTKIT_SUBJECT_HMAC_ACTIVE_KEY": "2"})
	t.Logf("restarted accountsvc with only active changed to K2: pid=%d", p1.cmd.Process.Pid)
	afterRestart := signIn(p1, target, oldCode)
	if afterRestart["user_id"] != uid || afterRestart["is_new_user"] != false {
		t.Fatal("old code after active restart did not reuse original account")
	}
	var accounts, identities int
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM user_account), (SELECT count(*) FROM identity WHERE user_id=$1)", uid).Scan(&accounts, &identities); err != nil {
		t.Fatal("read rotation identity counts failed")
	}
	if accounts != 1 || identities != 1 {
		t.Fatal("rotation created duplicate account or identity")
	}
	waitCooldown(target)
	for _, p := range []*serviceProcess{p1, p2} {
		limited(p, target, "TARGET_LIMIT")
		assertCounts(target, 2, 1, 3)
	}

	// Use opposite active processes to fill the unchanged shared IP quota
	// with two fresh targets; each success increments it exactly once.
	otherK2 := "rotation-service-k2@example.test"
	send(p2, otherK2)
	assertCounts(otherK2, 0, 1, 4)
	// A K1 process also sees the same IP budget after the rolling restart.
	p3 := f.start(map[string]string{"ACCOUNTKIT_SUBJECT_HMAC_ACTIVE_KEY": "1"})
	t.Logf("started accountsvc K1 quota peer: pid=%d", p3.cmd.Process.Pid)
	limited(p3, target, "TARGET_LIMIT")
	assertCounts(target, 2, 1, 4)
	otherK1 := "rotation-service-k1@example.test"
	send(p3, otherK1)
	assertCounts(otherK1, 1, 0, 5)
	blocked := "rotation-service-ip-blocked@example.test"
	for _, p := range []*serviceProcess{p3, p2} {
		limited(p, blocked, "IP_LIMIT")
		assertCounts(blocked, 0, 0, 5)
	}
	p3.stop()
	p2.stop()
	p1.stop()
	f.assertAuditPrivate()
}

func rotationQuotaStartDelay(now time.Time) time.Duration {
	u := now.UTC()
	midnight := time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC)
	remaining := midnight.Sub(u)
	if remaining <= 90*time.Second {
		return remaining
	}
	return 0
}

func TestServiceRotationQuotaWindow(t *testing.T) {
	for _, tc := range []struct {
		at   string
		wait time.Duration
	}{
		{"2026-10-04T12:00:00Z", 0},
		{"2026-10-04T23:58:29Z", 0},
		{"2026-10-04T23:58:30Z", 90 * time.Second},
		{"2026-10-04T23:59:59.500Z", 500 * time.Millisecond},
		{"2026-10-05T07:59:59+08:00", time.Second},
		{"2026-10-05T00:00:00Z", 0},
	} {
		t.Run(tc.at, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339Nano, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			if got := rotationQuotaStartDelay(now); got != tc.wait {
				t.Fatalf("quota window wait: got %s, want %s", got, tc.wait)
			}
		})
	}
}
