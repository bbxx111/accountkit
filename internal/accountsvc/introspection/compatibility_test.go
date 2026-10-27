package introspection_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/internal/testkeys"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// This test uses a real Service, signer and revocation set. The unreachable lazy
// pool ensures introspection does not add a database/account-state requirement.
func TestConsumerAuthenticationCompatibility(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, "postgres://synthetic:synthetic@127.0.0.1:1/unreachable?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	var logs bytes.Buffer
	cfg := accountkit.Config{JWTKeys: map[uint16]*ecdsa.PrivateKey{1: testkeys.JWT(1)}, JWTActiveKey: 1, JWTIssuer: "consumer.example", JWTAudience: "business.example", SubjectHMACKeys: map[uint16][]byte{1: bytes.Repeat([]byte{2}, 32)}, SubjectHMACActiveKey: 1, SubjectCipherKeys: map[uint16][]byte{1: bytes.Repeat([]byte{3}, 32)}, SubjectCipherActiveKey: 1, KeyPrefix: "introspection-test:"}
	a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil), Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	h, err := introspection.New(map[string][]string{"business": {secret(1)}}, a.Users().AuthenticateStrict)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	claims := tokens.Claims{UserID: "u_0000000000001", SessionID: "s_0000000000001", Scope: "user:bind", AuthTime: now.Add(-time.Minute)}
	sign := func(issuer, audience string, key int, c tokens.Claims, issued time.Time, ttl time.Duration) string {
		t.Helper()
		s, e := tokens.NewSigner(tokens.Options{Keys: map[uint16]*ecdsa.PrivateKey{1: testkeys.JWT(key)}, Active: 1, Issuer: issuer, Audience: audience})
		if e != nil {
			t.Fatal(e)
		}
		raw, _, e := s.Sign(c, issued, ttl)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	valid := sign("consumer.example", "business.example", 1, claims, now, time.Hour)
	invalidID := claims
	invalidID.UserID = "users/1"
	invalidSession := claims
	invalidSession.SessionID = "sessions/1"
	for _, tc := range []struct {
		name, raw string
		active    bool
	}{
		{"valid restricted scope", valid, true},
		{"expired", sign("consumer.example", "business.example", 1, claims, now.Add(-time.Hour), time.Minute), false},
		{"other issuer", sign("other.example", "business.example", 1, claims, now, time.Hour), false},
		{"other audience", sign("consumer.example", "other.example", 1, claims, now, time.Hour), false},
		{"other signing key", sign("consumer.example", "business.example", 9, claims, now, time.Hour), false},
		{"invalid user ID", sign("consumer.example", "business.example", 1, invalidID, now, time.Hour), false},
		{"invalid session ID", sign("consumer.example", "business.example", 1, invalidSession, now, time.Hour), false},
		{"refresh", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, directErr := a.Users().AuthenticateStrict(ctx, tc.raw)
			w := request(h, "token="+tc.raw+"&token_type_hint=refresh_token", secret(1))
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("missing no-store: %v", w.Header())
			}
			if tc.active {
				// 签名有效的令牌必须查库确认；数据库不可达时 fail-closed 为 503，不返回主体信息
				if w.Code != 503 || strings.TrimSpace(w.Body.String()) != `{"error":"temporarily_unavailable"}` || !errors.Is(directErr, user.ErrUnavailable) {
					t.Fatalf("database outage must fail closed: %d %s %v", w.Code, w.Body, directErr)
				}
				return
			}
			if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"active":false}` || !errors.Is(directErr, user.ErrInvalidToken) {
				t.Fatalf("invalid token: %d %s %v", w.Code, w.Body, directErr)
			}
		})
	}
	// 内省不读取 Redis 吊销集：Redis 记录吊销或 Redis 故障都不改变"必须查库"的结果
	set := revocation.NewSet(rdb, "introspection-test:")
	if err := set.Revoke(ctx, claims.SessionID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Users().Authenticate(ctx, valid); err == nil {
		t.Fatal("direct consumer accepted revoked token")
	}
	if w := request(h, "token="+valid, secret(1)); w.Code != 503 {
		t.Fatalf("introspection must consult the database, not Redis: %d %s", w.Code, w.Body)
	}
	if err := rdb.Close(); err != nil {
		t.Fatal(err)
	}
	// 普通 Authenticate 的 fail-open 契约保持不变
	if _, err := a.Users().Authenticate(ctx, valid); err != nil {
		t.Fatalf("direct fail-open changed: %v", err)
	}
	if w := request(h, "token="+valid, secret(1)); w.Code != 503 || strings.Contains(w.Body.String(), "active") {
		t.Fatalf("introspection must stay fail-closed: %d %s", w.Code, w.Body)
	}
	if strings.Contains(logs.String(), valid) {
		t.Fatalf("token leaked to logs: %s", logs.String())
	}
}
