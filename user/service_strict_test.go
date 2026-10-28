package user_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/testkeys"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/session/grace"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
)

// newOfflineService 构造数据库不可达的服务：只要触达数据库就会得到依赖错误。
func newOfflineService(t *testing.T) (*user.Service, *tokens.Signer) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://synthetic:synthetic@127.0.0.1:1/unreachable?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	dig, _ := pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{1}, 32)}, 1)
	ciph, _ := pii.NewCipher(map[uint16][]byte{1: bytes.Repeat([]byte{2}, 32)}, 1)
	signer, _ := tokens.NewSigner(tokens.Options{Keys: map[uint16]*ecdsa.PrivateKey{1: testkeys.JWT(3)}, Active: 1, Issuer: "shifang", Audience: "app"})
	svc, err := user.NewService(user.Deps{
		Repo:       user.NewRepo(pool),
		Codes:      code.NewStore(rdb, "t:", dig, code.Options{FailureLimitPerTarget: 10, FailureWindow: 15 * time.Minute, TTL: 5 * time.Minute, Cooldown: time.Minute, MaxAttempts: 5, DailyLimitPerTarget: 10, DailyLimitPerIP: 100}),
		Revocation: revocation.NewSet(rdb, "t:"), Grace: grace.NewCache(rdb, "t:", ciph),
		Signer: signer, Cipher: ciph, Digester: dig, SMS: &capture{}, Email: &capture{}, Audit: &testAuditMemory{},
		Logger:    slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		AccessTTL: 15 * time.Minute, RefreshTTL: 720 * time.Hour, RefreshGrace: 30 * time.Second, CodeTTL: 5 * time.Minute,
		DefaultRegion: "CN", MaxIdentitiesPerKind: 1, DeletionCoolingPeriod: 360 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, signer
}

func TestAuthenticateStrictWithoutDatabase(t *testing.T) {
	svc, signer := newOfflineService(t)
	ctx := context.Background()
	now := time.Now()
	// 签名或 ID 格式无效：在访问数据库前判定为无效，而不是依赖错误
	bad, _, _ := signer.Sign(tokens.Claims{UserID: "users/1", SessionID: "s_0123456789abc", Scope: user.ScopeUser, AuthTime: now}, now, time.Minute)
	for name, tok := range map[string]string{"empty": "", "garbage": "a.b.c", "malformed sub": bad} {
		if _, err := svc.AuthenticateStrict(ctx, tok); !errors.Is(err, user.ErrInvalidToken) || errors.Is(err, user.ErrUnavailable) {
			t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		}
	}
	// 有效令牌但数据库不可用：fail-closed，报依赖不可用而非无效或放行
	good, _, _ := signer.Sign(tokens.Claims{UserID: "u_0123456789abc", SessionID: "s_0123456789abc", Scope: user.ScopeUser, AuthTime: now}, now, time.Minute)
	p, err := svc.AuthenticateStrict(ctx, good)
	if !errors.Is(err, user.ErrUnavailable) || errors.Is(err, user.ErrInvalidToken) || p.UserID != "" {
		t.Fatalf("database outage must be ErrUnavailable: %+v %v", p, err)
	}
	// 对照：普通 Authenticate 不访问数据库，照常通过
	if _, err := svc.Authenticate(ctx, good); err != nil {
		t.Fatalf("Authenticate must not depend on the database: %v", err)
	}
}

func (f *fixture) sessionSnapshot(t *testing.T) string {
	t.Helper()
	var out string
	err := f.pool.QueryRow(context.Background(), `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM session s),
 (SELECT jsonb_agg(to_jsonb(c) ORDER BY session_id) FROM session_client_info c),
 (SELECT jsonb_agg(to_jsonb(a) ORDER BY session_id, identity_id) FROM session_authentication_identity a))::text`).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAuthenticateStrictDatabaseState(t *testing.T) {
	// 父测试本身须在缺库时 skip，否则子测试全部 skip 而父测试 PASS，会绕过必需测试门禁。
	if os.Getenv("SERVER_TEST_DB_DSN") == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	ctx := context.Background()
	dev2 := user.Device{ID: "22222222-2222-2222-2222-222222222222", Name: "iPad"}
	invalid := func(t *testing.T, f *fixture, tok, why string) {
		t.Helper()
		if _, err := f.svc.AuthenticateStrict(ctx, tok); !errors.Is(err, user.ErrInvalidToken) {
			t.Fatalf("%s: err = %v, want ErrInvalidToken", why, err)
		}
	}

	t.Run("valid matches Authenticate and writes nothing", func(t *testing.T) {
		f := newFixture(t)
		res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		before := f.sessionSnapshot(t)
		strict, err := f.svc.AuthenticateStrict(ctx, res.AccessToken)
		if err != nil {
			t.Fatal(err)
		}
		if plain := principalOf(t, f, res); strict != plain {
			t.Fatalf("strict %+v != plain %+v", strict, plain)
		}
		if after := f.sessionSnapshot(t); after != before {
			t.Fatal("strict authentication must not write session, client info or authentication sources")
		}
	})

	t.Run("revoked even when revocation set write fails", func(t *testing.T) {
		f := newFixture(t)
		res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		p := principalOf(t, f, res)
		f.mr.Close()
		if err := f.svc.RevokeSession(ctx, p.UserID, p.SessionID, meta1); err != nil {
			t.Fatalf("revoke with redis down: %v", err)
		}
		invalid(t, f, res.AccessToken, "revoked with redis write failure")
		if _, err := f.svc.Authenticate(ctx, res.AccessToken); err != nil {
			t.Fatalf("Authenticate fail-open boundary changed: %v", err)
		}
	})

	t.Run("refresh expired but not revoked stays valid", func(t *testing.T) {
		f := newFixture(t)
		res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		p := principalOf(t, f, res)
		if _, err := f.pool.Exec(ctx, "UPDATE session SET refresh_expire_time = now() - interval '1 hour' WHERE id = $1", p.SessionID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.AuthenticateStrict(ctx, res.AccessToken); err != nil {
			t.Fatalf("refresh expiry must not invalidate access: %v", err)
		}
	})

	t.Run("missing session or other subject", func(t *testing.T) {
		f := newFixture(t)
		res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		p := principalOf(t, f, res)
		forged, _, err := f.deps.Signer.Sign(tokens.Claims{UserID: "u_0123456789abc", SessionID: p.SessionID, Scope: user.ScopeUser, AuthTime: p.AuthTime}, *f.clock, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		invalid(t, f, forged, "subject mismatch")
		if _, err := f.pool.Exec(ctx, "DELETE FROM session WHERE id = $1", p.SessionID); err != nil {
			t.Fatal(err)
		}
		invalid(t, f, res.AccessToken, "cleaned session")
	})

	t.Run("lifecycle revocations take effect immediately", func(t *testing.T) {
		f := newFixture(t)
		a := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		b := f.signIn(t, enum.IdentityPhone, phone1, dev2)
		pa := principalOf(t, f, a)
		if err := f.svc.RevokeOtherSessions(ctx, pa.UserID, pa.SessionID, meta1); err != nil {
			t.Fatal(err)
		}
		invalid(t, f, b.AccessToken, "revokeOthers target")
		if _, err := f.svc.AuthenticateStrict(ctx, a.AccessToken); err != nil {
			t.Fatalf("current session must survive revokeOthers: %v", err)
		}
		if _, err := f.svc.Freeze(ctx, admin1, pa.UserID, "strict-test", meta1); err != nil {
			t.Fatal(err)
		}
		invalid(t, f, a.AccessToken, "frozen account")
	})

	t.Run("account deletion", func(t *testing.T) {
		f := newFixture(t)
		res := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		if _, err := f.svc.DeleteMe(ctx, principalOf(t, f, res), meta1); err != nil {
			t.Fatal(err)
		}
		invalid(t, f, res.AccessToken, "deleted account")
	})
}
