package idp_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/idp"
)

type appleFixture struct {
	key    *rsa.PrivateKey
	kid    string
	srv    *httptest.Server
	hits   atomic.Int32
	serve  atomic.Bool // false → 500
	apple  *idp.Apple
	now    time.Time
	nonces *idp.NonceRegistry
	mr     *miniredis.Miniredis
}

func jwkOf(kid string, pub *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func newAppleFixture(t *testing.T) *appleFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &appleFixture{key: key, kid: "k1", now: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)}
	f.serve.Store(true)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if !f.serve.Load() {
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwkOf(f.kid, &f.key.PublicKey)}})
	}))
	t.Cleanup(f.srv.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	f.mr = mr
	f.nonces = idp.NewNonceRegistry(rdb, "t:")
	f.apple = idp.NewApple(idp.AppleOptions{
		BundleIDs: []string{"co.shifang.zavelo", "co.shifang.diet"},
		JWKSURL:   f.srv.URL + "/auth/keys",
		HTTP:      f.srv.Client(),
		Nonces:    f.nonces,
		NonceTTL:  10 * time.Minute,
		Now:       func() time.Time { return f.now },
	})
	return f
}

const rawNonce = "client-random-nonce-1"

func nonceHash(raw string) string {
	s := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(s[:])
}

// token 签一个 id_token；claims 覆盖默认值；method 默认 RS256、kid 默认 f.kid。
func (f *appleFixture) token(t *testing.T, over map[string]any, method jwt.SigningMethod, kid string, signKey any) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": "https://appleid.apple.com", "aud": "co.shifang.zavelo", "sub": "001234.abcdef",
		"iat": f.now.Unix(), "exp": f.now.Add(10 * time.Minute).Unix(),
		"nonce": nonceHash(rawNonce), "nonce_supported": true,
		"email": "x@privaterelay.appleid.com", "email_verified": "true",
	}
	for k, v := range over {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	if method == nil {
		method = jwt.SigningMethodRS256
	}
	if kid == "" {
		kid = f.kid
	}
	if signKey == nil {
		signKey = f.key
	}
	tok := jwt.NewWithClaims(method, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(signKey)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAppleVerifyHappyPathAndHintEmail(t *testing.T) {
	f := newAppleFixture(t)
	ctx := context.Background()
	id, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce)
	if err != nil || id.Kind != enum.IdentityApple || id.Subject != "001234.abcdef" || id.HintEmail != "x@privaterelay.appleid.com" || id.OpenIDs != nil {
		t.Fatalf("%+v %v", id, err)
	}
	if f.hits.Load() != 1 {
		t.Fatalf("JWKS fetched %d times, want 1 (lazy, then cached)", f.hits.Load())
	}
	// email_verified false / 缺失 → 无 hint；bool true 也接受
	id, _ = f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash("n2"), "email_verified": "false"}, nil, "", nil), "n2")
	if id.HintEmail != "" {
		t.Fatal("unverified email must not hint")
	}
	id, _ = f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash("n3"), "email_verified": true}, nil, "", nil), "n3")
	if id.HintEmail == "" {
		t.Fatal("bool true email_verified must hint")
	}
	id, _ = f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash("n4"), "email": nil, "email_verified": nil}, nil, "", nil), "n4")
	if id.HintEmail != "" {
		t.Fatal("no email claim → no hint")
	}
	// aud 为数组也接受
	if _, err := f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash("n5"), "aud": []string{"other", "co.shifang.diet"}}, nil, "", nil), "n5"); err != nil {
		t.Fatalf("aud array: %v", err)
	}
	if f.hits.Load() != 1 {
		t.Fatal("cached key must be reused")
	}
}

func TestAppleVerifyRejections(t *testing.T) {
	f := newAppleFixture(t)
	ctx := context.Background()
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	cases := []struct {
		name  string
		tok   string
		nonce string
		want  error
	}{
		{"empty token", "", rawNonce, idp.ErrInvalidCredential},
		{"empty nonce", f.token(t, nil, nil, "", nil), "", idp.ErrInvalidCredential},
		{"nonce mismatch", f.token(t, nil, nil, "", nil), "someone-elses-nonce", idp.ErrInvalidCredential},
		{"nonce claim missing", f.token(t, map[string]any{"nonce": nil}, nil, "", nil), rawNonce, idp.ErrInvalidCredential},
		{"wrong iss", f.token(t, map[string]any{"iss": "https://evil.example"}, nil, "", nil), rawNonce, idp.ErrInvalidCredential},
		{"expired", f.token(t, map[string]any{"exp": f.now.Add(-time.Minute).Unix()}, nil, "", nil), rawNonce, idp.ErrInvalidCredential},
		{"no exp", f.token(t, map[string]any{"exp": nil}, nil, "", nil), rawNonce, idp.ErrInvalidCredential},
		{"aud not allowed", f.token(t, map[string]any{"aud": "com.evil.app"}, nil, "", nil), rawNonce, idp.ErrAppNotAllowed},
		{"empty sub", f.token(t, map[string]any{"sub": ""}, nil, "", nil), rawNonce, idp.ErrInvalidCredential},
		{"forged key", f.token(t, nil, nil, "", other), rawNonce, idp.ErrInvalidCredential},
		{"hs256 with public key material", f.token(t, nil, jwt.SigningMethodHS256, "", []byte("k")), rawNonce, idp.ErrInvalidCredential},
		{"unknown kid", f.token(t, nil, nil, "k-unknown", nil), rawNonce, idp.ErrInvalidCredential},
		{"garbage", "not.a.jwt", rawNonce, idp.ErrInvalidCredential},
	}
	for _, c := range cases {
		if _, err := f.apple.VerifyApple(ctx, c.tok, c.nonce); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	// 上述全部失败都不应登记 nonce → 现在正确的 token 仍可登录
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce); err != nil {
		t.Fatalf("valid after rejections: %v", err)
	}
	// 重放
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce); !errors.Is(err, idp.ErrNonceReplayed) {
		t.Fatalf("replay: %v", err)
	}
	// 30 秒 leeway：exp 在 20 秒前仍接受
	if _, err := f.apple.VerifyApple(ctx, f.token(t, map[string]any{"exp": f.now.Add(-20 * time.Second).Unix(), "nonce": nonceHash("n-leeway")}, nil, "", nil), "n-leeway"); err != nil {
		t.Fatalf("within leeway: %v", err)
	}
}

func TestAppleJWKSRefreshOnUnknownKidIsThrottled(t *testing.T) {
	f := newAppleFixture(t)
	ctx := context.Background()
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce); err != nil {
		t.Fatal(err)
	}
	// 轮换：服务端换 kid，客户端首次遇到未知 kid → 刷新一次 → 成功
	newKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	f.key, f.kid = newKey, "k2"
	if _, err := f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash("r1")}, nil, "", nil), "r1"); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
	if f.hits.Load() != 2 {
		t.Fatalf("hits %d want 2", f.hits.Load())
	}
	// 60 秒内再遇未知 kid → 不再刷新（节流），直接拒绝
	if _, err := f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash("r2")}, nil, "k-nope", nil), "r2"); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("unknown kid within throttle: %v", err)
	}
	if f.hits.Load() != 2 {
		t.Fatalf("throttled refresh must not hit JWKS again: %d", f.hits.Load())
	}
	f.now = f.now.Add(61 * time.Second)
	_, _ = f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash("r3")}, nil, "k-nope", nil), "r3")
	if f.hits.Load() != 3 {
		t.Fatalf("after throttle window a refresh is allowed: %d", f.hits.Load())
	}
}

func TestAppleJWKSDownWithoutCacheIsUnavailable(t *testing.T) {
	f := newAppleFixture(t)
	f.serve.Store(false)
	if _, err := f.apple.VerifyApple(context.Background(), f.token(t, nil, nil, "", nil), rawNonce); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("jwks down, no cache: %v", err)
	}
	// 有缓存后 JWKS 宕机不影响已知 kid
	f.serve.Store(true)
	f.now = f.now.Add(2 * time.Minute)
	if _, err := f.apple.VerifyApple(context.Background(), f.token(t, map[string]any{"nonce": nonceHash("c1")}, nil, "", nil), "c1"); err != nil {
		t.Fatal(err)
	}
	f.serve.Store(false)
	if _, err := f.apple.VerifyApple(context.Background(), f.token(t, map[string]any{"nonce": nonceHash("c2")}, nil, "", nil), "c2"); err != nil {
		t.Fatalf("cached key must keep working while JWKS is down: %v", err)
	}
}

func TestAppleDisabledWhenNoBundleIDs(t *testing.T) {
	f := newAppleFixture(t)
	a := idp.NewApple(idp.AppleOptions{JWKSURL: f.srv.URL, HTTP: f.srv.Client(), Nonces: f.nonces})
	if _, err := a.VerifyApple(context.Background(), f.token(t, nil, nil, "", nil), rawNonce); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("disabled: %v", err)
	}
}

// JWKS 故障发生在"从未成功拉取过"阶段时，同样必须受 60 秒节流：否则每次登录
// 尝试都会在持锁状态下无节流地发起网络请求并互相串行等待。
func TestAppleJWKSFetchFailureBeforeFirstSuccessIsThrottled(t *testing.T) {
	f := newAppleFixture(t)
	f.serve.Store(false)
	ctx := context.Background()
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("first attempt: %v", err)
	}
	if f.hits.Load() != 1 {
		t.Fatalf("hits %d want 1", f.hits.Load())
	}
	// 同一时钟再次尝试 → 节流，不应再打到 JWKS
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("second attempt (throttled): %v", err)
	}
	if f.hits.Load() != 1 {
		t.Fatalf("throttled retry must not hit JWKS again: %d", f.hits.Load())
	}
	f.now = f.now.Add(61 * time.Second)
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("third attempt after throttle window: %v", err)
	}
	if f.hits.Load() != 2 {
		t.Fatalf("hits %d want 2", f.hits.Load())
	}
}

// header 中没有 kid 时，keyfunc 必须直接拒绝，而不是猜测使用哪个缓存键。
func TestAppleVerifyMissingKidHeaderIsInvalidCredential(t *testing.T) {
	f := newAppleFixture(t)
	claims := jwt.MapClaims{
		"iss": "https://appleid.apple.com", "aud": "co.shifang.zavelo", "sub": "001234.abcdef",
		"iat": f.now.Unix(), "exp": f.now.Add(10 * time.Minute).Unix(),
		"nonce": nonceHash(rawNonce),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	delete(tok.Header, "kid")
	s, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.apple.VerifyApple(context.Background(), s, rawNonce); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("missing kid header: %v", err)
	}
}

func TestAppleVerifyNoNonceRegistryIsMisconfigured(t *testing.T) {
	a := idp.NewApple(idp.AppleOptions{BundleIDs: []string{"co.shifang.zavelo"}})
	if _, err := a.VerifyApple(context.Background(), "some.id.token", rawNonce); !errors.Is(err, idp.ErrMisconfigured) {
		t.Fatalf("no nonce registry: %v", err)
	}
}

// C1：Apple id_token 的 exp 可长达 24 小时，远超 AppleNonceTTL 这个下限（10 分钟）。
// nonce 登记的 TTL 必须延长到覆盖 id_token 的剩余有效期（+ leeway），否则密钥被截获者
// 在 AppleNonceTTL 过期之后、id_token 的 exp 之前，可以用同一个 id_token 重新登录一次。
func TestAppleNonceTTLExtendsToCoverIDTokenLifetime(t *testing.T) {
	f := newAppleFixture(t)
	ctx := context.Background()
	tok := f.token(t, map[string]any{"exp": f.now.Add(24 * time.Hour).Unix()}, nil, "", nil)

	if _, err := f.apple.VerifyApple(ctx, tok, rawNonce); err != nil {
		t.Fatalf("first use: %v", err)
	}

	sum := sha256.Sum256([]byte(nonceHash(rawNonce)))
	key := "t:nonce:apple:" + hex.EncodeToString(sum[:])
	if ttl := f.mr.TTL(key); ttl < 24*time.Hour {
		t.Fatalf("registered nonce ttl %v must be extended to cover the 24h id_token exp", ttl)
	}

	// 越过 AppleNonceTTL 这个下限（10m + 1s），但 id_token 本身（exp = +24h）仍未过期：
	// 若 TTL 没有被正确延长，miniredis 会在这里让键过期，重放就会被放行。
	f.now = f.now.Add(10*time.Minute + time.Second)
	f.mr.FastForward(10*time.Minute + time.Second)
	if _, err := f.apple.VerifyApple(ctx, tok, rawNonce); !errors.Is(err, idp.ErrNonceReplayed) {
		t.Fatalf("replay after the 10m floor must still be rejected (key must outlive it): %v", err)
	}
}

// I1：JWKS 刷新必须不受调用方 context 取消影响，否则一个中途断开的客户端请求会让
// 这次刷新以 context.Canceled 失败，进而把接下来 60 秒内所有 Apple 登录一起节流拒绝。
//
// nonce 登记仍然合理地使用调用方 ctx（Redis fail-closed 与本项修复无关），所以取消
// ctx 的这次调用本身依然会失败；断言的重点是 JWKS 抓取必须已经完成并缓存
// （hits==1），因此紧接着用一个未取消的 ctx 重试必须直接命中缓存成功，而不是先撞上
// 一次由取消引发的刷新失败、再被节流拒绝 60 秒。
func TestAppleJWKSFetchIgnoresCallerCancellation(t *testing.T) {
	f := newAppleFixture(t)
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.apple.VerifyApple(cctx, f.token(t, nil, nil, "", nil), rawNonce); err == nil {
		t.Fatal("cancelled ctx: nonce registration itself is expected to fail")
	}
	if f.hits.Load() != 1 {
		t.Fatalf("hits %d want 1 (JWKS fetch must complete despite the cancelled ctx)", f.hits.Load())
	}
	if _, err := f.apple.VerifyApple(context.Background(), f.token(t, map[string]any{"nonce": nonceHash("n2")}, nil, "", nil), "n2"); err != nil {
		t.Fatalf("follow-up with a live ctx must succeed from cache: %v", err)
	}
	if f.hits.Load() != 1 {
		t.Fatalf("hits %d want 1 (no throttled JWKS refetch caused by the earlier cancellation)", f.hits.Load())
	}
}

// M1：缓存已建立时，若 kid 未知，只有在最近一次刷新尝试确实成功过（无论是本次还是
// 节流期内沿用的上一次）的前提下，才是客户端的错（400）；刷新失败/仍处于失败节流期
// 一律映射 503，不能被误判成"这是个坏 kid"。
func TestAppleUnknownKidAfterFailedRefreshIsUnavailable(t *testing.T) {
	f := newAppleFixture(t)
	ctx := context.Background()
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(61 * time.Second) // 越过节流窗口，下一次未知 kid 会真正发起刷新
	f.serve.Store(false)
	if _, err := f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash("bad-kid")}, nil, "k-nope", nil), "bad-kid"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("unknown kid + failed refresh: got %v want ErrUnavailable", err)
	}
}

func TestAppleUnknownKidAfterSuccessfulRefreshIsInvalidCredential(t *testing.T) {
	f := newAppleFixture(t)
	ctx := context.Background()
	if _, err := f.apple.VerifyApple(ctx, f.token(t, nil, nil, "", nil), rawNonce); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(61 * time.Second)
	if _, err := f.apple.VerifyApple(ctx, f.token(t, map[string]any{"nonce": nonceHash("bad-kid-2")}, nil, "k-nope", nil), "bad-kid-2"); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("unknown kid + successful refresh: got %v want ErrInvalidCredential", err)
	}
}

// M8(a)：Redis 不可用时，即便 id_token 与 JWKS 都完全正常，也必须 fail-closed 拒绝，
// 而不是放行登录。
func TestAppleVerifyRedisDownIsUnavailable(t *testing.T) {
	f := newAppleFixture(t)
	rdb := redis.NewClient(&redis.Options{Addr: f.mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	f.apple = idp.NewApple(idp.AppleOptions{
		BundleIDs: []string{"co.shifang.zavelo", "co.shifang.diet"},
		JWKSURL:   f.srv.URL + "/auth/keys",
		HTTP:      f.srv.Client(),
		Nonces:    idp.NewNonceRegistry(rdb, "t:"),
		NonceTTL:  10 * time.Minute,
		Now:       func() time.Time { return f.now },
	})
	tok := f.token(t, nil, nil, "", nil)
	f.mr.Close()
	if _, err := f.apple.VerifyApple(context.Background(), tok, rawNonce); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("redis down: %v", err)
	}
}
