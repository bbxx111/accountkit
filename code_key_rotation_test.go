package accountkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type rotationSender struct {
	captureSender
	fail atomic.Bool
}

func (s *rotationSender) SendSMS(ctx context.Context, target string, msg sender.Message) error {
	if s.fail.Load() {
		return sender.ErrUnavailable
	}
	return s.captureSender.SendSMS(ctx, target, msg)
}

func (s *rotationSender) SendEmail(ctx context.Context, target string, msg sender.Message) error {
	return s.SendSMS(ctx, target, msg)
}

type libraryRotation struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	rdb  *redis.Client
	cfg  accountkit.Config
	sent *rotationSender
}

func newLibraryRotation(t *testing.T, dailyLimit int) *libraryRotation {
	t.Helper()
	dsn := dbDSN(t)
	url := os.Getenv("ACCOUNTSVC_TEST_REDIS_URL")
	if url == "" {
		t.Skip("ACCOUNTSVC_TEST_REDIS_URL not set; real Redis required")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal("parse test Redis URL")
	}
	opts.MaxRetries = -1
	f := &libraryRotation{t: t, rdb: redis.NewClient(opts), sent: &rotationSender{}, cfg: minimal()}
	f.ctx = context.Background()
	f.cfg.Schema = fmt.Sprintf("rotation_lib_%016x", rand.Uint64())
	f.cfg.KeyPrefix = f.cfg.Schema + ":"
	f.cfg.SubjectHMACKeys = map[uint16][]byte{1: k(7), 2: k(9)}
	f.cfg.CodeCooldown = time.Second
	f.cfg.CodeDailyLimitPerTarget = dailyLimit
	f.cfg.MaxIdentitiesPerKind = 2
	pc, err := accountkit.PoolConfig(dsn, f.cfg.Schema)
	if err != nil {
		t.Fatal(err)
	}
	f.pool, err = pgxpool.NewWithConfig(f.ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		defer f.pool.Close()
		defer f.rdb.Close()
		// KEYS is restricted to this randomly owned prefix; no shared Redis state is deleted.
		keys, err := f.rdb.Keys(ctx, f.cfg.KeyPrefix+"*").Result()
		if err != nil {
			t.Error("list owned Redis keys for cleanup", err)
		} else if len(keys) > 0 {
			if err := f.rdb.Del(ctx, keys...).Err(); err != nil {
				t.Error("clean owned Redis keys", err)
			}
		}
		if _, err := f.pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+f.cfg.Schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	if err := f.rdb.Ping(f.ctx).Err(); err != nil {
		t.Fatal("real Redis unavailable")
	}
	return f
}

func (f *libraryRotation) auth(active uint16, isolated bool) *accountkit.Auth {
	f.t.Helper()
	cfg := f.cfg
	cfg.SubjectHMACActiveKey = active
	if isolated {
		cfg.KeyPrefix += "isolated:"
	}
	a, err := accountkit.New(cfg, accountkit.Deps{Pool: f.pool, Redis: f.rdb, SMSSender: f.sent, EmailSender: f.sent})
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(a.Close)
	if err := a.Migrate(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	a.Start(f.ctx)
	return a
}

func (f *libraryRotation) call(a *accountkit.Auth, method, path, bearer string, body any, status int, reason string) map[string]any {
	f.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(f.ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Id", "rotation-library")
	req.Header.Set("X-Request-Id", "rotation-library")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	a.ConsumerHandler().ServeHTTP(w, req)
	if w.Code != status {
		f.t.Fatalf("%s: status %d, want %d", path, w.Code, status)
	}
	out := map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatal("invalid response JSON")
	}
	if reason != "" {
		e, ok := out["error"].(map[string]any)
		if !ok || e["reason"] != reason {
			f.t.Fatalf("%s: incompatible error reason", path)
		}
	}
	return out
}

// These public-entry tests catch loss of old-key proof at library assembly, not just Store behavior.
func TestCodeKeyRotationIntegration(t *testing.T) {
	dbDSN(t)
	if os.Getenv("ACCOUNTSVC_TEST_REDIS_URL") == "" {
		t.Skip("ACCOUNTSVC_TEST_REDIS_URL not set; real Redis required")
	}
	for _, channel := range []string{"PHONE", "EMAIL"} {
		t.Run(channel, func(t *testing.T) {
			f := newLibraryRotation(t, 10)
			targets := []string{"+8613812345101", "+8613812345102", "+8613812345103"}
			if channel == "EMAIL" {
				targets = []string{"rotation-a@example.test", "rotation-b@example.test", "rotation-c@example.test"}
			}
			proof := func(target, c string) any {
				return map[string]any{strings.ToLower(channel): map[string]string{"target": target, "code": c}}
			}
			sendBody := func(target string) any { return map[string]string{"channel": channel, "target": target} }
			old := f.auth(1, false)
			f.call(old, "POST", "/users:sendSignInCode", "", sendBody(targets[0]), 200, "")
			original := f.call(old, "POST", "/users:signInWithCode", "", proof(targets[0], f.sent.code(targets[0])), 200, "")
			time.Sleep(1100 * time.Millisecond)
			f.call(old, "POST", "/users:sendSignInCode", "", sendBody(targets[0]), 200, "")
			plain := f.sent.code(targets[0])
			if len(plain) != 6 {
				t.Fatal("sign-in code was not delivered")
			}
			old.Close() // Rebuild after issuance with the other active key, keeping host resources.
			fresh := f.auth(2, false)
			wrong := "0" + plain[1:]
			if wrong == plain {
				wrong = "1" + plain[1:]
			}
			f.call(fresh, "POST", "/users:signInWithCode", "", proof(targets[0], wrong), 400, "CODE_INVALID")
			isolated := f.auth(2, true)
			f.call(isolated, "POST", "/users:signInWithCode", "", proof(targets[0], plain), 400, "CODE_EXPIRED")
			pair := f.call(fresh, "POST", "/users:signInWithCode", "", proof(targets[0], plain), 200, "")
			if pair["user_id"] != original["user_id"] || pair["is_new_user"] != false {
				t.Fatal("rotated login did not reuse the original K1 account")
			}
			uid, access := pair["user_id"].(string), pair["access_token"].(string)
			f.call(fresh, "POST", "/users:signInWithCode", "", proof(targets[0], plain), 400, "CODE_EXPIRED")
			p, err := fresh.Users().Authenticate(f.ctx, access)
			if err != nil || p.UserID != uid {
				t.Fatal("rotated login access has wrong account")
			}
			// Reverse direction: K2 issues BIND, a freshly constructed K1 consumes it.
			f.call(fresh, "POST", "/users/me:sendBindCode", access, sendBody(targets[1]), 200, "")
			back := f.auth(1, false)
			bound := f.call(back, "POST", "/users/me/identities", access, proof(targets[1], f.sent.code(targets[1])), 201, "")
			f.call(back, "POST", "/users/me:sendBindCode", access, sendBody(targets[2]), 200, "")
			name := bound["name"].(string)
			if !strings.HasPrefix(name, "users/"+uid+"/identities/") || bound["kind"] != channel {
				t.Fatal("bound identity belongs to wrong account/channel")
			}
			id := name[strings.LastIndex(name, "/")+1:]
			replaced := f.call(fresh, "POST", "/users/me/identities/"+id+":replace", access, proof(targets[2], f.sent.code(targets[2])), 200, "")
			if replaced["name"] == name || replaced["kind"] != channel {
				t.Fatal("replacement did not create correct identity")
			}
			list := f.call(fresh, "GET", "/users/me/identities", access, nil, 200, "")["identities"].([]any)
			if len(list) != 2 {
				t.Fatal("bind/replacement changed identity count")
			}
			found := false
			for _, item := range list {
				n := item.(map[string]any)["name"]
				if n == name {
					t.Fatal("replacement left old identity live")
				}
				found = found || n == replaced["name"]
			}
			if !found {
				t.Fatal("replacement identity absent")
			}
			time.Sleep(1100 * time.Millisecond) // Real Redis expiry of the original target cooldown.
			f.call(back, "POST", "/users/me:sendReauthenticationCode", access, sendBody(targets[0]), 200, "")
			reauth := f.call(fresh, "POST", "/users/me:reauthenticate", access, proof(targets[0], f.sent.code(targets[0])), 200, "")
			p2, err := fresh.Users().Authenticate(f.ctx, reauth["access_token"].(string))
			if err != nil || p2.UserID != uid || p2.SessionID != p.SessionID || !p2.AuthTime.After(p.AuthTime) {
				t.Fatal("old REAUTH proof did not renew the same account/session")
			}
			if _, ok := reauth["refresh_token"]; ok {
				t.Fatal("reauthentication unexpectedly issued refresh credential")
			}
			f.call(fresh, "GET", "/users/me", reauth["access_token"].(string), nil, 200, "")
			fresh.Close()
			back.Close()
			isolated.Close()
			for reason, typ := range map[string]enum.EventType{"CODE_INVALID": enum.EventSignInFailed, "CODE_EXPIRED": enum.EventSignInFailed, "IDENTITY_REPLACED": enum.EventIdentityBound} {
				var n int
				if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM audit_event WHERE request_id='rotation-library' AND reason=$1 AND event_type=$2", reason, typ).Scan(&n); err != nil || n == 0 {
					t.Fatalf("missing compatible audit reason %s", reason)
				}
			}
		})
	}
	t.Run("delivery failure preserves limits across active", func(t *testing.T) {
		f := newLibraryRotation(t, 1)
		old, fresh := f.auth(1, false), f.auth(2, false)
		for _, tc := range []struct{ channel, target string }{{"PHONE", "+8613812345199"}, {"EMAIL", "rotation-failed@example.test"}} {
			body := map[string]string{"channel": tc.channel, "target": tc.target}
			f.sent.fail.Store(true)
			f.call(old, "POST", "/users:sendSignInCode", "", body, 503, "DEPENDENCY_UNAVAILABLE")
			f.sent.fail.Store(false)
			f.call(fresh, "POST", "/users:sendSignInCode", "", body, 429, "COOLDOWN")
			time.Sleep(1100 * time.Millisecond)
			f.call(fresh, "POST", "/users:sendSignInCode", "", body, 429, "TARGET_LIMIT")
		}
		old.Close()
		fresh.Close()
		for _, reason := range []string{"SEND_FAILED", "COOLDOWN", "TARGET_LIMIT"} {
			var n int
			if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM audit_event WHERE request_id='rotation-library' AND reason=$1", reason).Scan(&n); err != nil || n != 2 {
				t.Fatalf("missing delivery/limit audit %s", reason)
			}
		}
	})
}
