package accountkit_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/internal/testkeys"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/bbxx111/accountkit/user/sender"
)

// TestJWKSHandler 在数据库与 Redis 均不可用时仍按配置输出公钥集。
func TestJWKSHandler(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://synthetic:synthetic@127.0.0.1:1/unreachable?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	mr.Close()

	cfg := minimal()
	cfg.JWTKeys = map[uint16]*ecdsa.PrivateKey{1: testkeys.JWT(1), 2: testkeys.JWT(2)}
	cfg.JWTActiveKey = 2
	a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	signer, err := tokens.NewSigner(tokens.Options{Keys: cfg.JWTKeys, Active: 2, Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience})
	if err != nil {
		t.Fatal(err)
	}
	h := a.JWKSHandler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("GET: %d %v", w.Code, w.Header())
	}
	if !bytes.Equal(w.Body.Bytes(), signer.JWKS()) || bytes.Contains(w.Body.Bytes(), []byte(`"d"`)) {
		t.Fatalf("body = %s", w.Body.Bytes())
	}
	var set struct {
		Keys []struct{ Kid string } `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &set); err != nil || len(set.Keys) != 2 {
		t.Fatalf("keys = %+v err=%v", set, err)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/.well-known/jwks.json", nil))
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("HEAD: %d len=%d", w.Code, w.Body.Len())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/.well-known/jwks.json", nil))
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST: %d %v", w.Code, w.Header())
	}
}
