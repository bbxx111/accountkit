# Facebook 登录与第三方数据删除实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增内置身份类型 FACEBOOK（Limited 与 Classic 两种凭证落到同一身份），以及按第三方 subject 发起的数据删除领域入口与 Facebook signed_request 校验。

**Architecture:** `user/idp.Facebook` 一个提供方按 credential 字段分派：Limited 复用 `idTokenVerifier`，Classic 走两次 Graph 调用。`user.Service.DeleteProviderIdentityData` 在账号行锁内判定 S2（匿名化该身份并吊销来源会话）或 S1（复用注销事务体），宿主阻止或冻结返回 BLOCKED。门面 `Auth.HandleFacebookDataDeletion` 串联签名校验与领域方法。

**Tech Stack:** Go 1.26、pgx/v5、sqlc（本次不改 SQL）、go-redis/miniredis、golang-jwt/v5、httptest。

**Spec:** `openspec/changes/add-facebook-sign-in/`（proposal.md、design.md、specs/*、tasks.md）。实施者必须同时阅读 design.md 与 specs；本计划不复述需求，冲突时以 specs 为准并暂停报告。

## Global Constraints

- 所有命令在 worktree 根目录运行，先 `export GOWORK=off`。
- 不修改 `migrations/`、`user/db/`、`audit/db/`、`user/query.sql`、`audit/query.sql`、`tests/testdata/source-baseline/`。
- 枚举只追加不重编号；`EventType` 新值为 27。
- 错误、日志、审计中不得出现 access token、App Secret、appsecret_proof 或完整 Facebook 用户 id。
- 修改 Go 后 `gofmt -w`；Go/SQL/shell 使用 LF。
- 新增必需顶层测试同步登记到 `scripts/required-tests.txt`（格式 `<import path>/<TestName>`）。
- 数据库用例依赖 `SERVER_TEST_DB_DSN`；缺失时测试 skip，记录为未执行，不得宣称通过。
- 每个任务完成后提交一次，Conventional Commits，结尾附 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`。
- 每完成一个任务，勾选 `openspec/changes/add-facebook-sign-in/tasks.md` 中对应条目。

## File Structure

| 文件 | 职责 |
|---|---|
| `enum/enum.go` | `IdentityFacebook`、`EventProviderDataDeletionProcessed` |
| `observability/classification.go` | `MethodFacebook`、`DependencyVerifyFacebook`、`DegradationFacebookCachedKeyFallback` |
| `user/idp/oidc.go` | `idTokenVerifier.hintEmailUnverified` 选项 |
| `user/idp/facebook.go`（新） | 提供方、credential 分派、Limited、Classic |
| `user/idp/facebook_signed_request.go`（新） | `ParseSignedRequest`、`ErrInvalidSignedRequest` |
| `user/idp/policy.go` | `(*Facebook).PolicyFingerprint` |
| `user/identity_policy.go`、`user/observability.go` | 内置顺序与观测映射 |
| `audit/authentication/authentication.go` | 新事件的目标映射与 SYSTEM 操作者许可 |
| `user/service_lifecycle.go` | 注销事务体抽取 `softDeleteLocked` |
| `user/service_provider_deletion.go`（新） | `DeleteProviderIdentityData` 及结果类型 |
| `config.go`、`accountkit.go` | 配置、环境变量、装配、门面 |
| `internal/accountsvc/config.go` | 服务配置校验与变量名映射 |
| 文档 | README、docs/go-integration.md、docs/accountsvc.md、docs/compatibility.md、deploy/accountsvc/.env.example |

---

### Task 1: 枚举与观测常量（tasks 1.1–1.3）

**Files:**
- Modify: `enum/enum.go:35-75`（IdentityKind）、`enum/enum.go:164-200`（EventType）
- Modify: `observability/classification.go:18-28,159-185`
- Modify: `user/identity_policy.go:65`、`user/observability.go:50-65,470-485`
- Modify: `audit/authentication/authentication.go`（`validActorKind`、目标映射）
- Test: `enum/enum_test.go`、`observability/*_test.go`、`audit/authentication/authentication_test.go`、`config_test.go`

**Interfaces:**
- Produces: `enum.IdentityFacebook IdentityKind = "FACEBOOK"`；`enum.EventProviderDataDeletionProcessed`（27）；`observability.MethodFacebook = "facebook"`；`observability.DependencyVerifyFacebook = "verify_facebook"`；`observability.DegradationFacebookCachedKeyFallback = "facebook_cached_key_fallback"`。

- [ ] **Step 1: 写失败测试**

`enum/enum_test.go` 追加：

```go
func TestIdentityFacebookIsBuiltinThirdParty(t *testing.T) {
	k, err := enum.ParseIdentityKind("FACEBOOK")
	if err != nil || k != enum.IdentityFacebook || !k.IsBuiltin() || k.IsAnchor() || k.IsChannel() {
		t.Fatalf("FACEBOOK: %v %v builtin=%v", k, err, k.IsBuiltin())
	}
}

func TestEventProviderDataDeletionProcessedAppended(t *testing.T) {
	if enum.EventProviderDataDeletionProcessed != 27 || enum.EventSignInCompletionRejected != 26 {
		t.Fatal("event values must be append-only")
	}
	e, err := enum.ParseEventType("PROVIDER_DATA_DELETION_PROCESSED")
	if err != nil || e != enum.EventProviderDataDeletionProcessed || !e.Valid() || enum.EventType(28).Valid() {
		t.Fatalf("parse: %v %v", e, err)
	}
}
```

`config_test.go` 追加（`package accountkit` 内已有 `validConfig()` 辅助时直接复用；若名称不同，以文件中已有构造有效 Config 的辅助为准）：

```go
func TestFacebookCannotBeRequiredOrOIDCName(t *testing.T) {
	c := validConfig()
	c.FacebookAppID, c.FacebookAppSecret = "123", "s"
	c.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail, enum.IdentityFacebook}
	c.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityFacebook}
	if err := c.Validate(); err == nil {
		t.Fatal("FACEBOOK in Required must fail")
	}
	c = validConfig()
	c.OIDCProviders = []accountkit.OIDCProvider{{Kind: enum.IdentityFacebook, Issuer: "https://i.example", JWKSURL: "https://i.example/k", ClientIDs: []string{"c"}}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "builtin") {
		t.Fatalf("OIDC FACEBOOK: %v", err)
	}
}
```

（此测试依赖 Task 3 的配置字段；若先跑 Task 1，把第一段留到 Task 3 再加入，第二段现在即可通过。）

`audit/authentication/authentication_test.go` 追加：

```go
func TestProviderDataDeletionEventSystemActor(t *testing.T) {
	o := authentication.Options{Source: "accountkit", Namespace: "ns"}
	id := uuid.Must(uuid.NewV7())
	e, err := authentication.Map(authentication.Input{ID: id, Type: enum.EventProviderDataDeletionProcessed, Actor: enum.ActorSystem, Result: enum.ResultSuccess, Reason: "DELETED", UserID: "u_0000000000000", IdentityKind: enum.IdentityFacebook, SubjectHint: "abcd1234"}, o)
	if err != nil || e.ID != id || e.Actor.Type != event.ActorSystem || len(e.Targets) != 1 || e.Targets[0].Type != "USER" {
		t.Fatalf("map: %+v %v", e, err)
	}
	for _, typ := range []enum.EventType{enum.EventIdentityUnbound, enum.EventUserDeleted} {
		if _, err := authentication.Map(authentication.Input{Type: typ, Actor: enum.ActorSystem, Result: enum.ResultSuccess, UserID: "u_0000000000000"}, o); err != nil {
			t.Fatalf("%s system actor: %v", typ, err)
		}
	}
	if _, err := authentication.Map(authentication.Input{Type: enum.EventProviderDataDeletionProcessed, Actor: enum.ActorUser, Result: enum.ResultSuccess}, o); err == nil {
		t.Fatal("user actor must be rejected")
	}
}
```

（`event` 指 `github.com/bbxx111/accountkit/audit/event`，以测试文件已有 import 为准。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./enum/ ./audit/authentication/ -run 'Facebook|ProviderDataDeletion' -count=1`
Expected: 编译失败，`undefined: enum.IdentityFacebook`。

- [ ] **Step 3: 实现**

`enum/enum.go`：

```go
	IdentityGoogle          IdentityKind = "GOOGLE"
	IdentityFacebook        IdentityKind = "FACEBOOK"
```

`IsBuiltin` 的 case 列表追加 `IdentityFacebook`。EventType 在 `EventSignInCompletionRejected // 26` 之后追加 `EventProviderDataDeletionProcessed // 27`，`eventTypeNames` 末尾追加 `"PROVIDER_DATA_DELETION_PROCESSED"`，`Valid` 上界改为 `EventProviderDataDeletionProcessed`。

`user/identity_policy.go:65`：

```go
var builtinKindOrder = []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat, enum.IdentityApple, enum.IdentityGoogle, enum.IdentityFacebook}
```

`observability/classification.go`：在 `MethodGoogle` 后加 `MethodFacebook Method = "facebook"`；`DependencyVerifyGoogle` 后加 `DependencyVerifyFacebook DependencyOperation = "verify_facebook"`；`DegradationGoogleCachedKeyFallback` 后加 `DegradationFacebookCachedKeyFallback DegradationScenario = "facebook_cached_key_fallback"`。若该文件有列举全部合法值的集合或 `Valid` 函数，同步加入新值。

`user/observability.go`：方法映射加 `case enum.IdentityFacebook: return ob.MethodFacebook`；依赖映射加 `case enum.IdentityFacebook: return ob.DependencyVerifyFacebook`。

`audit/authentication/authentication.go`：

```go
	case enum.EventUserDeleted, enum.EventUserUndeleted, enum.EventUserPurged, enum.EventUserFrozen, enum.EventUserUnfrozen, enum.EventProviderDataDeletionProcessed:
		add("USER", in.UserID, true)
```

`validActorKind`：

```go
	case enum.EventUserPurged, enum.EventProviderDataDeletionProcessed:
		return kind == enum.ActorSystem
	case enum.EventIdentityUnbound:
		return kind == enum.ActorUser || kind == enum.ActorSystem
	case enum.EventSessionRevoked:
		return kind == enum.ActorUser || kind == enum.ActorAdmin || kind == enum.ActorSystem
	case enum.EventUserDeleted:
		return kind == enum.ActorUser || kind == enum.ActorAdmin || kind == enum.ActorSystem
	case enum.EventUserUndeleted:
		return kind == enum.ActorUser || kind == enum.ActorAdmin
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./enum/ ./observability/ ./audit/... ./user/ -count=1`
Expected: PASS（数据库用例在无 DSN 时 skip）。检查是否有按事件总数断言的既有测试（如 `Actions()` 数量），按追加一个值更新期望。

- [ ] **Step 5: 提交**

```bash
gofmt -w enum observability audit user
git add enum observability audit user
git commit -m "feat(enum): add FACEBOOK identity kind and data deletion audit event"
```

---

### Task 2: Facebook 提供方（tasks 2.1–2.5）

**Files:**
- Create: `user/idp/facebook.go`
- Modify: `user/idp/oidc.go`（`idTokenVerifier` 增加 `hintEmailUnverified bool`）、`user/idp/policy.go`
- Test: `user/idp/facebook_test.go`

**Interfaces:**
- Consumes: Task 1 常量。
- Produces:

```go
type FacebookOptions struct {
	AppID, AppSecret, GraphAPIVersion, GraphAPIBaseURL, JWKSURL string
	HTTP     *http.Client
	Nonces   *NonceRegistry
	Logger   *slog.Logger
	Observer observability.Observer
	Now      func() time.Time
	NonceTTL time.Duration
}
func NewFacebook(o FacebookOptions) *Facebook
func (f *Facebook) Kind() enum.IdentityKind
func (f *Facebook) Verify(ctx context.Context, credential json.RawMessage) (Identity, error)
func (f *Facebook) VerifyLimited(ctx context.Context, idToken, rawNonce string) (Identity, error)
func (f *Facebook) VerifyClassic(ctx context.Context, accessToken string) (Identity, error)
func (f *Facebook) PolicyFingerprint() string
```

- [ ] **Step 1: 写失败测试** `user/idp/facebook_test.go`

```go
package idp_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/idp"
)

const fbApp, fbSecret = "1234567890", "fb-app-secret"

type fbFixture struct {
	key        *rsa.PrivateKey
	now        time.Time
	jwks       *httptest.Server
	graph      *httptest.Server
	graphHits  atomic.Int32
	jwksHits   atomic.Int32
	debug      func(w http.ResponseWriter, r *http.Request)
	me         func(w http.ResponseWriter, r *http.Request)
	fb         *idp.Facebook
}

func proofOf(token string) string {
	m := hmac.New(sha256.New, []byte(fbSecret))
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}

func newFBFixture(t *testing.T) *fbFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fbFixture{key: key, now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	f.jwks = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.jwksHits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwkOf("fk1", &key.PublicKey)}})
	}))
	t.Cleanup(f.jwks.Close)
	f.debug = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"app_id": fbApp, "type": "USER", "is_valid": true, "user_id": "asid-1", "expires_at": f.now.Add(time.Hour).Unix()}})
	}
	f.me = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "asid-1", "name": "Ada", "email": "ada@example.test", "picture": map[string]any{"data": map[string]any{"url": "https://example.test/fb.png", "is_silhouette": false}}})
	}
	f.graph = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.graphHits.Add(1)
		q := r.URL.Query()
		switch r.URL.Path {
		case "/v26.0/debug_token":
			appToken := fbApp + "|" + fbSecret
			if q.Get("access_token") != appToken || q.Get("appsecret_proof") != proofOf(appToken) {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "bad app token", "type": "OAuthException", "code": 190}})
				return
			}
			f.debug(w, r)
		case "/v26.0/me":
			if q.Get("appsecret_proof") != proofOf(q.Get("access_token")) {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "invalid appsecret_proof", "type": "GraphMethodException", "code": 100}})
				return
			}
			f.me(w, r)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.graph.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	f.fb = idp.NewFacebook(idp.FacebookOptions{
		AppID: fbApp, AppSecret: fbSecret, GraphAPIVersion: "v26.0", GraphAPIBaseURL: f.graph.URL, JWKSURL: f.jwks.URL,
		HTTP: f.graph.Client(), Nonces: idp.NewNonceRegistry(rdb, "t:"), NonceTTL: 10 * time.Minute,
		Now: func() time.Time { return f.now },
	})
	return f
}

func (f *fbFixture) idToken(t *testing.T, over map[string]any) string {
	t.Helper()
	claims := jwt.MapClaims{"iss": "https://www.facebook.com", "aud": fbApp, "sub": "asid-1", "iat": f.now.Unix(), "exp": f.now.Add(time.Hour).Unix(), "nonce": "n1", "name": "Ada", "picture": "https://example.test/fb.png", "email": "ada@example.test"}
	for k, v := range over {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "fk1"
	s, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFacebookCredentialShapes(t *testing.T) {
	f := newFBFixture(t)
	for name, raw := range map[string]string{
		"both":        `{"id_token":"x","nonce":"n","access_token":"t"}`,
		"only nonce":  `{"nonce":"n"}`,
		"empty token": `{"access_token":""}`,
		"empty nonce": `{"id_token":"x","nonce":""}`,
		"unknown":     `{"access_token":"t","x":1}`,
		"none":        `{}`,
		"not object":  `"t"`,
	} {
		if _, err := f.fb.Verify(context.Background(), json.RawMessage(raw)); !errors.Is(err, idp.ErrMalformedCredential) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if f.graphHits.Load() != 0 || f.jwksHits.Load() != 0 {
		t.Fatal("malformed credential must not reach Facebook")
	}
}

func TestFacebookLimitedLogin(t *testing.T) {
	f := newFBFixture(t)
	ctx := context.Background()
	id, err := f.fb.Verify(ctx, json.RawMessage(`{"id_token":"`+f.idToken(t, nil)+`","nonce":"n1"}`))
	if err != nil || id.Kind != enum.IdentityFacebook || id.Subject != "asid-1" || id.Name != "Ada" || id.Picture != "https://example.test/fb.png" || id.HintEmail != "ada@example.test" {
		t.Fatalf("limited: %+v %v", id, err)
	}
	if _, err := f.fb.VerifyLimited(ctx, f.idToken(t, nil), "n1"); !errors.Is(err, idp.ErrNonceReplayed) {
		t.Fatalf("replay: %v", err)
	}
	if _, err := f.fb.VerifyLimited(ctx, f.idToken(t, map[string]any{"aud": "other", "nonce": "n2"}), "n2"); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("aud: %v", err)
	}
	if _, err := f.fb.VerifyLimited(ctx, f.idToken(t, map[string]any{"iss": "https://limited.facebook.com", "nonce": "n3"}), "n3"); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("iss: %v", err)
	}
}

func TestFacebookClassicLogin(t *testing.T) {
	f := newFBFixture(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ { // 同一 token 可重复使用
		id, err := f.fb.Verify(ctx, json.RawMessage(`{"access_token":"user-token"}`))
		if err != nil || id.Kind != enum.IdentityFacebook || id.Subject != "asid-1" || id.Name != "Ada" || id.Picture != "https://example.test/fb.png" || id.HintEmail != "ada@example.test" || !id.ExpireTime.Equal(f.now.Add(time.Hour)) {
			t.Fatalf("classic %d: %+v %v", i, id, err)
		}
	}
}

func TestFacebookClassicRejections(t *testing.T) {
	cases := map[string]struct {
		debug map[string]any
		meID  string
		want  error
	}{
		"invalid":   {debug: map[string]any{"app_id": fbApp, "type": "USER", "is_valid": false, "user_id": "asid-1"}, want: idp.ErrInvalidCredential},
		"page type": {debug: map[string]any{"app_id": fbApp, "type": "PAGE", "is_valid": true, "user_id": "asid-1"}, want: idp.ErrInvalidCredential},
		"other app": {debug: map[string]any{"app_id": "999", "type": "USER", "is_valid": true, "user_id": "asid-1"}, want: idp.ErrAppNotAllowed},
		"id differs": {debug: map[string]any{"app_id": fbApp, "type": "USER", "is_valid": true, "user_id": "asid-1"}, meID: "asid-2", want: idp.ErrInvalidCredential},
	}
	for name, c := range cases {
		f := newFBFixture(t)
		f.debug = func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(map[string]any{"data": c.debug}) }
		if c.meID != "" {
			f.me = func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(map[string]any{"id": c.meID}) }
		}
		if _, err := f.fb.VerifyClassic(context.Background(), "user-token"); !errors.Is(err, c.want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestFacebookClassicDependencyAndConfigErrors(t *testing.T) {
	f := newFBFixture(t)
	f.debug = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }
	if _, err := f.fb.VerifyClassic(context.Background(), "user-token"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("5xx: %v", err)
	}
	f = newFBFixture(t)
	f.me = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "OAuthException", "code": 190}})
	}
	if _, err := f.fb.VerifyClassic(context.Background(), "user-token"); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("me 190: %v", err)
	}
	wrong := idp.NewFacebook(idp.FacebookOptions{AppID: fbApp, AppSecret: "wrong", GraphAPIVersion: "v26.0", GraphAPIBaseURL: f.graph.URL, JWKSURL: f.jwks.URL, HTTP: f.graph.Client()})
	_, err := wrong.VerifyClassic(context.Background(), "secret-user-token")
	if !errors.Is(err, idp.ErrMisconfigured) || strings.Contains(err.Error(), "secret-user-token") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("misconfigured: %v", err)
	}
}

func TestFacebookClassicSilhouetteAndNoExpiry(t *testing.T) {
	f := newFBFixture(t)
	f.debug = func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"app_id": fbApp, "type": "USER", "is_valid": true, "user_id": "asid-1", "expires_at": 0}})
	}
	f.me = func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "asid-1", "picture": map[string]any{"data": map[string]any{"url": "https://example.test/default.png", "is_silhouette": true}}})
	}
	id, err := f.fb.VerifyClassic(context.Background(), "user-token")
	if err != nil || id.Picture != "" || !id.ExpireTime.IsZero() || id.HintEmail != "" {
		t.Fatalf("silhouette: %+v %v", id, err)
	}
}

func TestFacebookPolicyFingerprint(t *testing.T) {
	base := idp.FacebookOptions{AppID: fbApp, AppSecret: fbSecret, GraphAPIVersion: "v26.0", GraphAPIBaseURL: "https://graph.facebook.com", JWKSURL: "https://www.facebook.com/.well-known/oauth/openid/jwks/"}
	fp := idp.NewFacebook(base).PolicyFingerprint()
	if strings.Contains(fp, fbSecret) {
		t.Fatal("fingerprint must not contain secret")
	}
	secret := base
	secret.AppSecret = "rotated"
	if idp.NewFacebook(secret).PolicyFingerprint() != fp {
		t.Fatal("secret must not affect fingerprint")
	}
	for name, mutate := range map[string]func(*idp.FacebookOptions){
		"app":     func(o *idp.FacebookOptions) { o.AppID = "other" },
		"version": func(o *idp.FacebookOptions) { o.GraphAPIVersion = "v25.0" },
		"graph":   func(o *idp.FacebookOptions) { o.GraphAPIBaseURL = "https://proxy.example" },
		"jwks":    func(o *idp.FacebookOptions) { o.JWKSURL = "https://limited.facebook.com/.well-known/oauth/openid/jwks/" },
	} {
		o := base
		mutate(&o)
		if idp.NewFacebook(o).PolicyFingerprint() == fp {
			t.Fatalf("%s must change fingerprint", name)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./user/idp/ -run Facebook -count=1`
Expected: 编译失败，`undefined: idp.NewFacebook`。

- [ ] **Step 3: 实现**

`user/idp/oidc.go`：`idTokenVerifier` 增加字段 `hintEmailUnverified bool // 提供方不给 email_verified 时仍把 email 作提示（Facebook）`，并把邮箱判断改为：

```go
	if email, _ := claims["email"].(string); email != "" && (v.hintEmailUnverified || emailVerified(claims["email_verified"])) {
		id.HintEmail = email
	}
```

`user/idp/facebook.go`：

```go
package idp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

const (
	facebookIssuer      = "https://www.facebook.com"
	facebookJWKSDefault = "https://www.facebook.com/.well-known/oauth/openid/jwks/"
	facebookGraphDefault = "https://graph.facebook.com"
	// FacebookGraphAPIVersionDefault 是固定的 Graph API 版本；升级属于兼容变更，须写入发布说明。
	FacebookGraphAPIVersionDefault = "v26.0"
	maxFacebookBody                = 64 << 10
)

// FacebookOptions 是 Facebook 校验器的依赖。AppSecret 只用于服务端 Graph 调用与 signed_request 校验。
type FacebookOptions struct {
	AppID, AppSecret, GraphAPIVersion, GraphAPIBaseURL, JWKSURL string
	HTTP     *http.Client
	Nonces   *NonceRegistry
	Logger   *slog.Logger
	Observer observability.Observer
	Now      func() time.Time
	// NonceTTL 是 Limited Login nonce 登记下限；实际期限覆盖 ID token 剩余有效期。默认 10m。
	NonceTTL time.Duration
}

// Facebook 校验 Limited Login 的 ID token 与 Classic Login 的 access token；两者的 subject
// 都是同一 App 下的用户 id，因此落到同一身份。Classic token 不做一次性限制（见 design）。
type Facebook struct {
	appID, secret, version, graph, jwksURL string
	hc                                     *http.Client
	logger                                 *slog.Logger
	now                                    func() time.Time
	v                                      *idTokenVerifier
}

// NewFacebook 构造校验器。不做 I/O；JWKS 首次 Limited 验证时加载。
func NewFacebook(o FacebookOptions) *Facebook {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NonceTTL <= 0 {
		o.NonceTTL = 10 * time.Minute
	}
	if o.GraphAPIVersion == "" {
		o.GraphAPIVersion = FacebookGraphAPIVersionDefault
	}
	if o.GraphAPIBaseURL == "" {
		o.GraphAPIBaseURL = facebookGraphDefault
	}
	if o.JWKSURL == "" {
		o.JWKSURL = facebookJWKSDefault
	}
	// token 与 appsecret_proof 在 query 中；跟随重定向会把它们转发给重定向目标。
	noRedirect := *o.HTTP
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	recorder := observability.NewRecorder(o.Observer, o.Logger)
	var clientIDs []string
	if o.AppID != "" {
		clientIDs = []string{o.AppID}
	}
	return &Facebook{
		appID: o.AppID, secret: o.AppSecret, version: o.GraphAPIVersion,
		graph: strings.TrimRight(o.GraphAPIBaseURL, "/"), jwksURL: o.JWKSURL,
		hc: &noRedirect, logger: o.Logger, now: o.Now,
		v: &idTokenVerifier{
			kind: enum.IdentityFacebook, label: "facebook", issuers: []string{facebookIssuer},
			clientIDs: clientIDSet(clientIDs),
			jwks:      newJWKSCache("facebook", o.JWKSURL, o.HTTP, o.Logger, recorder, observability.DegradationFacebookCachedKeyFallback, o.Now),
			nonces:    o.Nonces, nonceScope: "facebook", nonceTTL: o.NonceTTL, now: o.Now,
			nameClaims: []string{"name"}, pictureClaims: []string{"picture"}, hintEmailUnverified: true,
		},
	}
}

// Kind 实现 Provider。
func (f *Facebook) Kind() enum.IdentityKind { return enum.IdentityFacebook }

// facebookCredential 用指针区分“缺失”与“空字符串”，二者都按形状错误处理。
type facebookCredential struct {
	IDToken     *string `json:"id_token"`
	Nonce       *string `json:"nonce"`
	AccessToken *string `json:"access_token"`
}

// Verify 实现 Provider：credential 为 {"id_token","nonce"} 或 {"access_token"}，二者择一。
func (f *Facebook) Verify(ctx context.Context, credential json.RawMessage) (Identity, error) {
	var c facebookCredential
	if err := decodeCredential(credential, &c); err != nil {
		return Identity{}, err
	}
	switch {
	case c.AccessToken == nil && c.IDToken != nil && c.Nonce != nil && *c.IDToken != "" && *c.Nonce != "":
		return f.VerifyLimited(ctx, *c.IDToken, *c.Nonce)
	case c.AccessToken != nil && c.IDToken == nil && c.Nonce == nil && *c.AccessToken != "":
		return f.VerifyClassic(ctx, *c.AccessToken)
	}
	return Identity{}, fmt.Errorf("%w: facebook credential must be {id_token, nonce} or {access_token}", ErrMalformedCredential)
}

// VerifyLimited 校验 Limited Login ID token 与原始 nonce。
func (f *Facebook) VerifyLimited(ctx context.Context, idToken, rawNonce string) (Identity, error) {
	return f.v.verify(ctx, idToken, rawNonce)
}

type graphError struct {
	Type string `json:"type"`
	Code int    `json:"code"`
}

// throttled 是 Graph 的限流错误码，属于依赖暂不可用而非凭证或配置问题。
func (e *graphError) throttled() bool {
	switch e.Code {
	case 4, 17, 32, 613:
		return true
	}
	return false
}

type debugTokenResponse struct {
	Data struct {
		AppID     string `json:"app_id"`
		Type      string `json:"type"`
		IsValid   bool   `json:"is_valid"`
		UserID    string `json:"user_id"`
		ExpiresAt int64  `json:"expires_at"`
	} `json:"data"`
	Error *graphError `json:"error"`
}

type meResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Picture struct {
		Data struct {
			URL          string `json:"url"`
			IsSilhouette bool   `json:"is_silhouette"`
		} `json:"data"`
	} `json:"picture"`
	Error *graphError `json:"error"`
}

func (f *Facebook) proof(token string) string {
	m := hmac.New(sha256.New, []byte(f.secret))
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}

// graphGet 发起 GET 并把 JSON 解到 dst。4xx 的错误体同样解码交给调用方判断；5xx、传输与解码错误为不可用。
// 错误与日志不含 URL（其中有 token 与 proof）。
func (f *Facebook) graphGet(ctx context.Context, path string, q url.Values, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.graph+"/"+f.version+path+"?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("%w: facebook build request: %v", ErrUnavailable, redactTransportErr(err))
	}
	resp, err := f.hc.Do(req)
	if err != nil {
		f.logger.Warn("facebook graph request failed", "path", path, "err", redactTransportErr(err))
		return fmt.Errorf("%w: facebook request: %v", ErrUnavailable, redactTransportErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 || (resp.StatusCode >= 300 && resp.StatusCode < 400) {
		f.logger.Warn("facebook graph non-success", "path", path, "status", resp.StatusCode)
		return fmt.Errorf("%w: facebook http %d", ErrUnavailable, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxFacebookBody)).Decode(dst); err != nil {
		return fmt.Errorf("%w: facebook response: %v", ErrUnavailable, err)
	}
	return nil
}

// VerifyClassic 用 debug_token 确认 token 属于本 App 的用户，再用 /me 读取资料并核对 id。
func (f *Facebook) VerifyClassic(ctx context.Context, accessToken string) (Identity, error) {
	if f.appID == "" {
		return Identity{}, fmt.Errorf("%w: facebook sign-in not configured", ErrAppNotAllowed)
	}
	appToken := f.appID + "|" + f.secret
	var dt debugTokenResponse
	if err := f.graphGet(ctx, "/debug_token", url.Values{"input_token": {accessToken}, "access_token": {appToken}, "appsecret_proof": {f.proof(appToken)}}, &dt); err != nil {
		return Identity{}, err
	}
	if dt.Error != nil {
		if dt.Error.throttled() {
			return Identity{}, fmt.Errorf("%w: facebook debug_token throttled", ErrUnavailable)
		}
		f.logger.Error("facebook app credentials rejected", "app_id", f.appID, "code", dt.Error.Code, "type", dt.Error.Type)
		return Identity{}, fmt.Errorf("%w: facebook debug_token code %d", ErrMisconfigured, dt.Error.Code)
	}
	d := dt.Data
	if d.AppID != "" && d.AppID != f.appID {
		return Identity{}, fmt.Errorf("%w: facebook token issued for another app", ErrAppNotAllowed)
	}
	if !d.IsValid || d.Type != "USER" || d.UserID == "" || d.AppID == "" {
		return Identity{}, fmt.Errorf("%w: facebook token invalid", ErrInvalidCredential)
	}
	var me meResponse
	if err := f.graphGet(ctx, "/me", url.Values{"fields": {"id,name,email,picture"}, "access_token": {accessToken}, "appsecret_proof": {f.proof(accessToken)}}, &me); err != nil {
		return Identity{}, err
	}
	if me.Error != nil {
		switch {
		case me.Error.throttled():
			return Identity{}, fmt.Errorf("%w: facebook me throttled", ErrUnavailable)
		case me.Error.Code == 190:
			return Identity{}, fmt.Errorf("%w: facebook token rejected", ErrInvalidCredential)
		}
		f.logger.Error("facebook profile request rejected", "app_id", f.appID, "code", me.Error.Code, "type", me.Error.Type)
		return Identity{}, fmt.Errorf("%w: facebook me code %d", ErrMisconfigured, me.Error.Code)
	}
	if me.ID != d.UserID {
		return Identity{}, fmt.Errorf("%w: facebook user id mismatch", ErrInvalidCredential)
	}
	id := Identity{Kind: enum.IdentityFacebook, Subject: d.UserID, Name: me.Name, HintEmail: me.Email}
	if !me.Picture.Data.IsSilhouette {
		id.Picture = me.Picture.Data.URL
	}
	if d.ExpiresAt > 0 {
		id.ExpireTime = time.Unix(d.ExpiresAt, 0).UTC()
	}
	return id, nil
}
```

`user/idp/policy.go` 追加：

```go
// PolicyFingerprint 标识 App ID、Graph 版本与地址、JWKS 地址，不含 App Secret。
func (f *Facebook) PolicyFingerprint() string {
	return f.v.fingerprint(f.version + "\n" + f.graph + "\n" + f.jwksURL)
}
```

注意：`ExpireTime` 断言用 `Equal`，`time.Unix(...).UTC()` 与 `f.now.Add(time.Hour)`（UTC）相等。若 `newJWKSCache` 的参数签名与上面不同，以 `google.go` 的调用为准。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./user/idp/ -count=1 -race`（无 CGO 时去掉 `-race` 并记录）
Expected: PASS，包括既有 Google/OIDC 测试（`hintEmailUnverified` 默认 false 不改变它们）。

- [ ] **Step 5: 提交**

```bash
gofmt -w user/idp
git add user/idp
git commit -m "feat(idp): add Facebook provider with limited and classic credentials"
```

---

### Task 3: 配置与装配（tasks 3.1–3.4）

**Files:**
- Modify: `config.go`（字段 85-102、providerKinds 120-130、applyDefaults 184-198、validateBase 296-320、ConfigFromEnv 505-520）
- Modify: `accountkit.go`（`Auth` 增加 `facebook *idp.Facebook`；`identityProviders`；`New` 中记录 Facebook 实例）
- Test: `config_test.go`、`user/service_facebook_test.go`（新）、`httpapi/enduser/*_test.go`（按现有第三方登录测试所在文件追加）

**Interfaces:**
- Consumes: Task 2 的 `idp.NewFacebook`、`idp.FacebookGraphAPIVersionDefault`。
- Produces: `Config.FacebookAppID/FacebookAppSecret/FacebookGraphAPIVersion/FacebookNonceTTL/FacebookGraphAPIBaseURL/FacebookJWKSURL`；环境变量 `FACEBOOK_APP_ID`、`FACEBOOK_APP_SECRET`、`FACEBOOK_GRAPH_API_VERSION`、`FACEBOOK_NONCE_TTL`、`FACEBOOK_GRAPH_API_BASE_URL`、`FACEBOOK_JWKS_URL`（前缀由宿主给定）。

- [ ] **Step 1: 写失败测试**

`config_test.go`：

```go
func TestFacebookConfigValidation(t *testing.T) {
	c := validConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled facebook: %v", err)
	}
	c.FacebookAppID = "123"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "FacebookAppSecret") {
		t.Fatalf("missing secret: %v", err)
	}
	c.FacebookAppSecret = "s3cr3t-value"
	c.FacebookGraphAPIVersion = "26"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "FacebookGraphAPIVersion") || strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("version: %v", err)
	}
	c.FacebookGraphAPIVersion = ""
	c.FacebookNonceTTL = time.Second
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "FacebookNonceTTL") {
		t.Fatalf("ttl: %v", err)
	}
	c.FacebookNonceTTL = 0
	c.FacebookJWKSURL = "ftp://x"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "FacebookJWKSURL") {
		t.Fatalf("jwks: %v", err)
	}
}

func TestFacebookConfigFromEnv(t *testing.T) {
	setValidEnv(t, "ACCOUNTKIT_") // 以文件中已有的环境变量辅助为准
	t.Setenv("ACCOUNTKIT_FACEBOOK_APP_ID", "123")
	if _, err := accountkit.ConfigFromEnv("ACCOUNTKIT_"); err == nil || !strings.Contains(err.Error(), "FacebookAppSecret") {
		t.Fatalf("env missing secret: %v", err)
	}
	t.Setenv("ACCOUNTKIT_FACEBOOK_APP_SECRET", "s")
	t.Setenv("ACCOUNTKIT_FACEBOOK_NONCE_TTL", "5m")
	c, err := accountkit.ConfigFromEnv("ACCOUNTKIT_")
	if err != nil || c.FacebookAppID != "123" || c.FacebookGraphAPIVersion != "v26.0" || c.FacebookNonceTTL != 5*time.Minute {
		t.Fatalf("env: %+v %v", c, err)
	}
}
```

在 Task 1 留下的 Required 用例中启用第一段。装配测试（`accountkit_test.go` 中已有构造 `New` 的辅助时追加）：

```go
func TestFacebookDefaultEnablement(t *testing.T) {
	c := validConfig()
	c.FacebookAppID, c.FacebookAppSecret = "123", "s"
	a := newTestAuth(t, c) // 以文件中已有的 New 辅助为准
	if !slices.Contains(a.Users().IdentityPolicy().EnabledKinds(), enum.IdentityFacebook) {
		t.Fatal("FACEBOOK must be enabled by default when App ID is set")
	}
	c = validConfig()
	c.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail, enum.IdentityFacebook}
	if err := c.Validate(); err == nil {
		t.Fatal("explicit FACEBOOK without App ID must fail")
	}
}
```

服务层（`user/service_facebook_test.go`，数据库用例）：

```go
package user_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

// fakeFacebook 按 credential 形状分派：Limited 用 id_token 作 key，Classic 用 access_token 作 key。
type fakeFacebook struct{ identities map[string]idp.Identity }

func (f *fakeFacebook) provider() idp.Provider {
	return adaptedProvider{kind: enum.IdentityFacebook, verify: func(_ context.Context, m map[string]string) (idp.Identity, error) {
		key := m["id_token"]
		if key == "" {
			key = m["access_token"]
		}
		id, ok := f.identities[key]
		if !ok {
			return idp.Identity{}, idp.ErrInvalidCredential
		}
		id.Kind = enum.IdentityFacebook
		return id, nil
	}}
}

func facebookCred(raw string) user.IdpCredential {
	return user.IdpCredential{Kind: enum.IdentityFacebook, Credential: json.RawMessage(raw)}
}

func TestFacebookLimitedThenClassicSameAccount(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	fb := &fakeFacebook{identities: map[string]idp.Identity{
		"limited": {Subject: "asid-1", Name: "Ada", Picture: "https://example.test/fb.png", HintEmail: email1},
		"classic": {Subject: "asid-1", Name: "Renamed"},
	}}
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, fb.provider())
		d.RequiredIdentityKinds = []enum.IdentityKind{}
	})
	first := requireSignInTokens(t, mustSignIn(t, f, facebookCred(`{"id_token":"limited","nonce":"n"}`)))
	if !first.IsNewUser || first.HintEmail != email1 {
		t.Fatalf("first: %+v", first)
	}
	second := requireSignInTokens(t, mustSignIn(t, f, facebookCred(`{"access_token":"classic"}`)))
	if second.IsNewUser || second.UserID != first.UserID {
		t.Fatalf("classic must hit same account: %+v", second)
	}
	me, _ := f.svc.GetMe(ctx, first.UserID)
	if me.DisplayName != "Ada" || me.AvatarURL != "https://example.test/fb.png" {
		t.Fatalf("profile must keep first fill: %+v", me)
	}
	idents, _ := f.repo.Q().ListActiveIdentitiesByUser(ctx, first.UserID)
	if len(idents) != 1 || idents[0].Kind != enum.IdentityFacebook {
		t.Fatalf("identities: %+v", idents)
	}
}

func mustSignIn(t *testing.T, f *fixture, cred user.IdpCredential) user.SignInResult {
	t.Helper()
	res, err := f.svc.SignInWithIdp(context.Background(), cred, dev1, meta1)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
```

另加“未注册 FACEBOOK 提供方时返回 `idp.ErrAppNotAllowed` 分类”的用例：使用默认 fixture（不含 Facebook）调用 `SignInWithIdp(facebookCred(...))`，断言错误满足既有“未登记类型”映射（参照 `service_idp_test.go` 中 `ACME_SSO` 的断言写法）。

HTTP 层：在现有第三方登录 HTTP 测试文件中，参照 GOOGLE 用例追加 FACEBOOK 两种形状成功、并列凭证 400 `CREDENTIAL_INCOMPLETE`、提供方返回 `idp.ErrUnavailable` 时 503 `IDP_UNAVAILABLE`。

- [ ] **Step 2: 运行确认失败**

Run: `go test . ./user/ ./httpapi/... -run 'Facebook' -count=1`
Expected: 编译失败，`c.FacebookAppID undefined`。

- [ ] **Step 3: 实现**

`config.go` 字段（放在 `OIDCProviders` 之前）：

```go
	// FacebookAppID 为空表示未启用 Facebook 登录。非空时 FacebookAppSecret 必填：Classic 校验与
	// 数据删除回调的 signed_request 都依赖它。只对接一个 App，subject 为该 App 下的用户 id。
	FacebookAppID           string
	FacebookAppSecret       string
	FacebookGraphAPIVersion string        // 默认 idp.FacebookGraphAPIVersionDefault，格式 vN.N
	FacebookNonceTTL        time.Duration // 默认 10m
```

URL 覆盖与 Google 并列：

```go
	// WeChatAPIBaseURL / AppleJWKSURL / GoogleJWKSURL / Facebook* 供测试与代理部署覆盖；生产保持默认。
	FacebookGraphAPIBaseURL string
	FacebookJWKSURL         string
```

`providerKinds` 在 Google 之后：

```go
	if c.FacebookAppID != "" {
		kinds = append(kinds, enum.IdentityFacebook)
	}
```

`applyDefaults`：

```go
	def(&c.FacebookNonceTTL, 10*time.Minute)
	if c.FacebookGraphAPIVersion == "" {
		c.FacebookGraphAPIVersion = idp.FacebookGraphAPIVersionDefault
	}
	if c.FacebookGraphAPIBaseURL == "" {
		c.FacebookGraphAPIBaseURL = "https://graph.facebook.com"
	}
	if c.FacebookJWKSURL == "" {
		c.FacebookJWKSURL = "https://www.facebook.com/.well-known/oauth/openid/jwks/"
	}
```

`validateBase`（Google 校验之后）：

```go
	if c.FacebookAppID != "" && c.FacebookAppSecret == "" {
		return errors.New("accountkit: FacebookAppSecret is required when FacebookAppID is set")
	}
	if !facebookVersionPattern.MatchString(c.FacebookGraphAPIVersion) {
		return errors.New(`accountkit: FacebookGraphAPIVersion must match ^v[0-9]+\.[0-9]+$`)
	}
	if c.FacebookNonceTTL < time.Minute {
		return errors.New("accountkit: FacebookNonceTTL must be >= 1m")
	}
```

包级变量 `var facebookVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+$`)`；URL 校验 map 加入 `"FacebookGraphAPIBaseURL": c.FacebookGraphAPIBaseURL, "FacebookJWKSURL": c.FacebookJWKSURL`。

`ConfigFromEnv`（`GOOGLE_JWKS_URL` 之后）：

```go
	c.FacebookAppID = str("FACEBOOK_APP_ID")
	c.FacebookAppSecret = str("FACEBOOK_APP_SECRET")
	c.FacebookGraphAPIVersion = str("FACEBOOK_GRAPH_API_VERSION")
	dur("FACEBOOK_NONCE_TTL", &c.FacebookNonceTTL)
	c.FacebookGraphAPIBaseURL = str("FACEBOOK_GRAPH_API_BASE_URL")
	c.FacebookJWKSURL = str("FACEBOOK_JWKS_URL")
```

`accountkit.go` 的 `identityProviders`（Google 之后、OIDC 之前，顺序与 `providerKinds` 一致）：

```go
	if cfg.FacebookAppID != "" {
		providers = append(providers, idp.NewFacebook(idp.FacebookOptions{
			AppID: cfg.FacebookAppID, AppSecret: cfg.FacebookAppSecret, GraphAPIVersion: cfg.FacebookGraphAPIVersion,
			GraphAPIBaseURL: cfg.FacebookGraphAPIBaseURL, JWKSURL: cfg.FacebookJWKSURL, HTTP: deps.HTTPClient,
			Nonces: idp.NewNonceRegistry(deps.Redis, cfg.KeyPrefix), Logger: deps.Logger, NonceTTL: cfg.FacebookNonceTTL, Observer: deps.Observer,
		}))
	}
```

`New` 中把 `identityProviders(cfg, deps)` 先赋给局部变量 `providers`，传给 `user.Deps.Providers`，并：

```go
	for _, p := range providers {
		if fb, ok := p.(*idp.Facebook); ok {
			a.facebook = fb
		}
	}
```

`Auth` 结构体增加 `facebook *idp.Facebook // 未配置时为 nil；数据删除入口使用`。

显式 Enabled 含 FACEBOOK 但无 App ID：`NewIdentityPolicy` 已对“显式启用但未登记的第三方类型”报错；若测试显示没有指出配置项，按 Google 的既有做法补充错误文本（不得包含 Secret）。

- [ ] **Step 4: 运行确认通过**

Run: `go test . ./user/... ./httpapi/... -count=1`
Expected: PASS；数据库用例无 DSN 时 skip，记录。

- [ ] **Step 5: 提交**

```bash
gofmt -w . 
git add config.go accountkit.go config_test.go accountkit_test.go user httpapi
git commit -m "feat(config): configure and assemble Facebook sign-in"
```

---

### Task 4: signed_request 与注销事务体抽取（tasks 4.1–4.2）

**Files:**
- Create: `user/idp/facebook_signed_request.go`
- Modify: `user/service_lifecycle.go:44-99`
- Test: `user/idp/facebook_signed_request_test.go`；既有 `user/service_lifecycle*_test.go`

**Interfaces:**
- Produces:

```go
var ErrInvalidSignedRequest = errors.New("idp: invalid signed request")
type FacebookDeletionRequest struct {
	UserID    string    // 该 App 下的 Facebook 用户 id
	IssueTime time.Time // signed_request 的 issued_at
}
func (f *Facebook) ParseSignedRequest(raw string) (FacebookDeletionRequest, error)

// user 包内部
func (s *Service) softDeleteLocked(ctx context.Context, tx pgx.Tx, q *db.Queries, u db.UserAccount, now time.Time) (db.UserAccount, []string, error)
func (s *Service) afterSoftDelete(ctx context.Context, revoked []string, tmpl audit.AuthenticationEvent)
```

- [ ] **Step 1: 写失败测试** `user/idp/facebook_signed_request_test.go`

```go
package idp_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/user/idp"
)

func signRequest(secret, payload string) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(payload))
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(p))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)) + "." + p
}

func TestFacebookParseSignedRequest(t *testing.T) {
	fb := idp.NewFacebook(idp.FacebookOptions{AppID: fbApp, AppSecret: fbSecret})
	good := signRequest(fbSecret, `{"algorithm":"HMAC-SHA256","issued_at":1792756800,"user_id":"asid-1"}`)
	req, err := fb.ParseSignedRequest(good)
	if err != nil || req.UserID != "asid-1" || !req.IssueTime.Equal(time.Unix(1792756800, 0)) {
		t.Fatalf("good: %+v %v", req, err)
	}
	sig, payload, _ := strings.Cut(good, ".")
	for name, raw := range map[string]string{
		"wrong secret":  signRequest("other", `{"algorithm":"HMAC-SHA256","issued_at":1,"user_id":"asid-1"}`),
		"wrong alg":     signRequest(fbSecret, `{"algorithm":"HMAC-SHA1","issued_at":1,"user_id":"asid-1"}`),
		"no user":       signRequest(fbSecret, `{"algorithm":"HMAC-SHA256","issued_at":1}`),
		"no issued_at":  signRequest(fbSecret, `{"algorithm":"HMAC-SHA256","user_id":"asid-1"}`),
		"bad json":      signRequest(fbSecret, `{`),
		"one part":      payload,
		"three parts":   good + ".x",
		"padded":        sig + "=." + payload,
		"empty":         "",
	} {
		_, err := fb.ParseSignedRequest(raw)
		if !errors.Is(err, idp.ErrInvalidSignedRequest) || strings.Contains(err.Error(), fbSecret) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./user/idp/ -run ParseSignedRequest -count=1`
Expected: 编译失败，`fb.ParseSignedRequest undefined`。

- [ ] **Step 3: 实现** `user/idp/facebook_signed_request.go`

```go
package idp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidSignedRequest：Facebook signed_request 格式、算法或签名不符。
var ErrInvalidSignedRequest = errors.New("idp: invalid signed request")

// FacebookDeletionRequest 是校验通过的数据删除回调内容。
type FacebookDeletionRequest struct {
	UserID    string
	IssueTime time.Time
}

// ParseSignedRequest 校验 Meta 数据删除回调的 signed_request：<base64url(sig)>.<base64url(payload)>，
// 无填充；先以 App Secret 对 payload 段做 HMAC-SHA256 常量时间比较，再解析 payload。
func (f *Facebook) ParseSignedRequest(raw string) (FacebookDeletionRequest, error) {
	sigPart, payloadPart, ok := strings.Cut(raw, ".")
	if !ok || sigPart == "" || payloadPart == "" || strings.Contains(payloadPart, ".") {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: malformed", ErrInvalidSignedRequest)
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: signature encoding", ErrInvalidSignedRequest)
	}
	mac := hmac.New(sha256.New, []byte(f.secret))
	mac.Write([]byte(payloadPart))
	if f.secret == "" || !hmac.Equal(sig, mac.Sum(nil)) {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: signature mismatch", ErrInvalidSignedRequest)
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: payload encoding", ErrInvalidSignedRequest)
	}
	var p struct {
		Algorithm string `json:"algorithm"`
		UserID    string `json:"user_id"`
		IssuedAt  int64  `json:"issued_at"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: payload json", ErrInvalidSignedRequest)
	}
	if !strings.EqualFold(p.Algorithm, "HMAC-SHA256") || p.UserID == "" || p.IssuedAt <= 0 {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: payload fields", ErrInvalidSignedRequest)
	}
	return FacebookDeletionRequest{UserID: p.UserID, IssueTime: time.Unix(p.IssuedAt, 0)}, nil
}
```

`user/service_lifecycle.go` 抽取（行为不变）：

```go
func (s *Service) softDelete(ctx context.Context, userID string, missing error, tmpl audit.AuthenticationEvent) (db.UserAccount, error) {
	now := s.now()
	var (
		row     db.UserAccount
		revoked []string
	)
	err := s.d.Repo.withDomainTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
		u, err := lockUserAs(ctx, q, userID, missing)
		if err != nil {
			return err
		}
		row, revoked, err = s.softDeleteLocked(ctx, tx, q, u, now)
		return err
	})
	if err != nil {
		return db.UserAccount{}, err
	}
	s.afterSoftDelete(ctx, revoked, tmpl)
	return row, nil
}

// softDeleteLocked 是注销的事务体：调用方已持有账号行锁。供本人、管理员与第三方数据删除共用。
func (s *Service) softDeleteLocked(ctx context.Context, tx pgx.Tx, q *db.Queries, u db.UserAccount, now time.Time) (db.UserAccount, []string, error) {
	// 原 softDelete 事务体从 requireState(u, enum.UserActive) 开始，到 revokeAllForUser 为止，原样搬入；
	// userID 改用 u.ID；结尾 return row, revoked, err。
}

// afterSoftDelete 是注销提交后的吊销集写入与审计。
func (s *Service) afterSoftDelete(ctx context.Context, revoked []string, tmpl audit.AuthenticationEvent) {
	s.afterRevokeAll(ctx, revoked, enum.RevokeUserDeleted, tmpl)
	ev := tmpl
	ev.Type, ev.Result = enum.EventUserDeleted, enum.ResultSuccess
	s.record(ctx, ev)
}
```

`softDeleteLocked` 的函数体必须是原代码逐行搬移（`BeforeDelete` 调用、`SoftDeleteUser`、流程锁定与审计清洗、删除流程、`revokeAllForUser`），不得增删逻辑。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./user/idp/ ./user/ -count=1`
Expected: PASS；既有注销相关测试（`DeleteMe`、`AdminDeleteUser`、`BeforeDelete`）全部通过，数据库用例无 DSN 时 skip 并记录。

- [ ] **Step 5: 提交**

```bash
gofmt -w user
git add user
git commit -m "feat(idp): verify Facebook signed requests; refactor(user): extract locked deletion body"
```

---

### Task 5: 数据删除领域方法、并发与审计（tasks 4.3–4.5）

**Files:**
- Create: `user/service_provider_deletion.go`
- Test: `user/service_provider_deletion_test.go`

**Interfaces:**
- Consumes: Task 1 枚举、Task 4 的 `softDeleteLocked`/`afterSoftDelete`、`randomHex`（`user/service_purge.go:164`）。
- Produces:

```go
type ProviderDeletionStatus string

const (
	ProviderDeletionDeleted   ProviderDeletionStatus = "DELETED"
	ProviderDeletionScheduled ProviderDeletionStatus = "SCHEDULED"
	ProviderDeletionBlocked   ProviderDeletionStatus = "BLOCKED"
)

type ProviderDeletionResult struct {
	ConfirmationCode string
	Status           ProviderDeletionStatus
	PurgeTime        time.Time // 仅 SCHEDULED 非零
}

func (s *Service) DeleteProviderIdentityData(ctx context.Context, kind enum.IdentityKind, subject string, requestTime time.Time, meta Meta) (ProviderDeletionResult, error)
```

- [ ] **Step 1: 写失败测试** `user/service_provider_deletion_test.go`

使用 Task 3 的 `fakeFacebook`、`facebookCred`、`mustSignIn`。辅助函数：

```go
func useFacebook(t *testing.T, f *fixture, fb *fakeFacebook, mutate func(*user.Deps)) {
	t.Helper()
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, fb.provider())
		if mutate != nil {
			mutate(d)
		}
	})
}

func fbAccount(t *testing.T, f *fixture, token string) user.TokenResult {
	t.Helper()
	return requireSignInTokens(t, mustSignIn(t, f, facebookCred(`{"access_token":"`+token+`"}`)))
}

func deletionEvents(f *fixture) []audit.AuthenticationEvent {
	var out []audit.AuthenticationEvent
	for _, e := range f.audit.Events() { // 以 testAuditMemory 实际方法名为准
		if e.Type == enum.EventProviderDataDeletionProcessed {
			out = append(out, e)
		}
	}
	return out
}
```

场景测试（每个场景一个 `t.Run`，均以 `f.clock` 时间 `issued := *f.clock` 作为请求时间，账号在 `issued` 之前创建：先登录，再 `f.advance(time.Minute)`，再用 `*f.clock` 作请求时间）：

```go
func TestProviderDataDeletion(t *testing.T) {
	ctx := context.Background()
	fb := &fakeFacebook{identities: map[string]idp.Identity{"a": {Subject: "asid-a"}}}

	t.Run("unknown subject", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{} })
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "nobody", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted || res.ConfirmationCode == "" {
			t.Fatalf("%+v %v", res, err)
		}
	})

	t.Run("other identity remains: anonymize and revoke source sessions", func(t *testing.T) {
		f := newFixture(t)
		useFacebook(t, f, fb, func(d *user.Deps) { d.RequiredIdentityKinds = []enum.IdentityKind{} })
		phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
		p := principalOf(t, f, phone) // 以现有从 TokenResult 得到 Principal 的辅助为准
		if _, _, err := f.svc.BindWithIdp(ctx, p, facebookCred(`{"access_token":"a"}`), meta1); err != nil {
			t.Fatal(err)
		}
		fbTokens := fbAccount(t, f, "a")
		f.advance(time.Minute)
		res, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || res.Status != user.ProviderDeletionDeleted {
			t.Fatalf("%+v %v", res, err)
		}
		all, _ := f.repo.Q().ListIdentitiesByUserIncludingDeleted(ctx, phone.UserID)
		for _, id := range all {
			if id.Kind == enum.IdentityFacebook && (id.DeleteTime == nil || (id.ProviderSubject != nil && *id.ProviderSubject == "asid-a") || id.ProviderMeta != nil) {
				t.Fatalf("facebook identity not anonymized: %+v", id)
			}
		}
		if _, err := f.svc.Refresh(ctx, fbTokens.RefreshToken, dev1, meta1); err == nil { // 以现有刷新方法签名为准
			t.Fatal("facebook-sourced session must be revoked")
		}
		if _, err := f.svc.Refresh(ctx, phone.RefreshToken, dev1, meta1); err != nil {
			t.Fatalf("phone session must stay: %v", err)
		}
		evs := deletionEvents(f)
		if len(evs) != 1 || evs[0].ID.String() != res.ConfirmationCode || evs[0].Actor != enum.ActorSystem || evs[0].Reason != "DELETED" || strings.Contains(fmt.Sprint(evs[0]), "asid-a") {
			t.Fatalf("audit: %+v", evs)
		}
		// 重复请求：幂等
		again, err := f.svc.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, "asid-a", *f.clock, meta1)
		if err != nil || again.Status != user.ProviderDeletionDeleted || again.ConfirmationCode == res.ConfirmationCode {
			t.Fatalf("repeat: %+v %v", again, err)
		}
	})
}
```

同一测试函数内继续追加以下子测试，断言按 spec 场景逐条写出：

1. `remaining does not satisfy Required`：Required 为默认（PHONE、EMAIL），账号有 FACEBOOK 与 WECHAT（用 fixture 的 `fakeWeChat` 绑定）→ DELETED；随后用 WECHAT 会话刷新得到 `Scope == "user:bind"`。
2. `frozen with other identity`：管理员冻结账号（`AdminFreezeUser`，以现有方法名为准）→ DELETED，账号仍为 FROZEN。
3. `only facebook`：只有 FACEBOOK → SCHEDULED，`PurgeTime` 等于 `*f.clock + DeletionCoolingPeriod`，`GetMe` 状态为 PENDING_DELETION，审计同时有 `USER_DELETED`（Actor SYSTEM）。
4. `host blocks`：`d.BeforeDelete` 返回 `user.ErrDeletionBlocked` → BLOCKED；账号仍 ACTIVE、身份仍活动、会话可刷新；审计只有一条 BLOCKED 的数据删除事件，没有 `IDENTITY_UNBOUND` 与 `USER_DELETED`。
5. `host error`：`BeforeDelete` 返回 `errors.New("db down")` → 返回错误，状态零值，数据不变。
6. `frozen only facebook`：冻结且只有 FACEBOOK → BLOCKED，身份仍活动。
7. `already pending deletion`：先 `DeleteMe`，再请求 → SCHEDULED，`PurgeTime` 等于账号已有 purge_time，数据不变。
8. `identity created after request`：请求时间取登录前一分钟 → DELETED，身份仍活动。
9. `invalid arguments`：`kind` 为 PHONE 或 subject 为空 → `errors.Is(err, user.ErrInvalidArgument)`。
10. `audit failure does not change result`：fixture 的审计替身配置为写入失败（若 `testAuditMemory` 无此能力，用一个 `Record` 丢弃事件的替身）→ 结果仍为 DELETED。

并发测试（独立测试函数，`-race` 运行）：

```go
func TestProviderDataDeletionConcurrentWithBind(t *testing.T) {
	// 账号只有 FACEBOOK；并发执行 DeleteProviderIdentityData 与 BindWithCode(PHONE)，重复 20 轮。
	// 每轮结束断言二者之一成立：
	//   (a) 状态 SCHEDULED，且 PHONE 绑定返回错误或 PHONE 身份不活动；
	//   (b) 状态 DELETED，PHONE 身份活动，FACEBOOK 身份已匿名化，账号 ACTIVE。
	// 不允许出现：账号 PENDING_DELETION 且 PHONE 身份活动。
}
```

按上述注释写出完整代码：用 `sync.WaitGroup` 启动两个 goroutine，各自记录结果；每轮使用新 fixture；绑定所需验证码在 goroutine 启动前发好。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./user/ -run 'ProviderDataDeletion' -count=1`
Expected: 编译失败，`f.svc.DeleteProviderIdentityData undefined`。

- [ ] **Step 3: 实现** `user/service_provider_deletion.go`

```go
package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/db"
)

// ProviderDeletionStatus 是第三方平台数据删除请求的处理结果，不落库。
type ProviderDeletionStatus string

const (
	// ProviderDeletionDeleted：该第三方身份已被抹除，或本就不存在。
	ProviderDeletionDeleted ProviderDeletionStatus = "DELETED"
	// ProviderDeletionScheduled：账号已进入注销冷静期，到期匿名化。
	ProviderDeletionScheduled ProviderDeletionStatus = "SCHEDULED"
	// ProviderDeletionBlocked：未改动数据，宿主注销检查拒绝或账号冻结，需宿主跟进。
	ProviderDeletionBlocked ProviderDeletionStatus = "BLOCKED"
)

// ProviderDeletionResult 是同步返回给宿主的处理结果。ConfirmationCode 等于数据删除审计事件 ID；
// 审计为尽力写入，需要可靠状态页的宿主应自行保存本结果。
type ProviderDeletionResult struct {
	ConfirmationCode string
	Status           ProviderDeletionStatus
	PurgeTime        time.Time
}

var errProviderDeletionBlocked = errors.New("user: provider deletion blocked")

// DeleteProviderIdentityData 处理第三方平台按 subject 发起的数据删除（provider-data-deletion 规格）。
// 去掉该身份后仍有有效 Enabled 身份时抹除该身份并吊销以它为来源的会话，不检查 Required；
// 否则 ACTIVE 账号转入注销，宿主阻止或账号冻结返回 BLOCKED 且不改动数据。
// requestTime 之后才创建的身份与本请求无关。判定与写入在同一事务、账号行锁下完成。
func (s *Service) DeleteProviderIdentityData(ctx context.Context, kind enum.IdentityKind, subject string, requestTime time.Time, meta Meta) (ProviderDeletionResult, error) {
	if !kind.Valid() || kind.IsAnchor() || subject == "" {
		return ProviderDeletionResult{}, fmt.Errorf("%w: provider kind and subject are required", ErrInvalidArgument)
	}
	code, err := uuid.NewV7()
	if err != nil {
		return ProviderDeletionResult{}, fmt.Errorf("user: confirmation code: %w", err)
	}
	res := ProviderDeletionResult{ConfirmationCode: code.String(), Status: ProviderDeletionDeleted}
	tmpl := audit.AuthenticationEvent{Actor: enum.ActorSystem, IP: meta.IP, RequestID: meta.RequestID}
	var (
		target                db.Identity
		unbound, deleted      bool
		revoked               []string
	)
	err = s.d.Repo.withDomainTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
		found, err := q.FindActiveIdentityByProviderSubject(ctx, db.FindActiveIdentityByProviderSubjectParams{Kind: kind, ProviderSubject: &subject})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("user: find provider identity: %w", err)
		}
		u, err := lockUserAs(ctx, q, found.UserID, ErrNotFound)
		if err != nil {
			return err
		}
		ident, err := q.GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: found.ID, UserID: u.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // 加锁前已被并发解绑或匿名化
		}
		if err != nil {
			return fmt.Errorf("user: relock provider identity: %w", err)
		}
		target = ident
		if ident.CreateTime.After(requestTime) {
			return nil
		}
		if u.State == enum.UserPendingDeletion {
			res.Status = ProviderDeletionScheduled
			if u.PurgeTime != nil {
				res.PurgeTime = *u.PurgeTime
			}
			return nil
		}
		idents, err := q.ListActiveIdentitiesByUser(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("user: list identities: %w", err)
		}
		remaining := false
		for _, other := range idents {
			if other.ID != ident.ID && s.IdentityPolicy().Allows(other.Kind) {
				remaining = true
				break
			}
		}
		now := sessionTime(s.now())
		if remaining {
			repl, err := randomHex(32)
			if err != nil {
				return err
			}
			if _, err := q.AnonymizeIdentity(ctx, db.AnonymizeIdentityParams{ID: ident.ID, UserID: u.ID, Replacement: repl, Now: now}); err != nil {
				return fmt.Errorf("user: anonymize provider identity: %w", err)
			}
			reason := enum.RevokeIdentityUnbound
			revoked, err = q.RevokeSessionsByIdentity(ctx, db.RevokeSessionsByIdentityParams{UserID: u.ID, IdentityID: ident.ID, Now: now, Reason: &reason})
			if err != nil {
				return fmt.Errorf("user: revoke identity sessions: %w", err)
			}
			unbound = true
			return nil
		}
		if u.State != enum.UserActive {
			return errProviderDeletionBlocked
		}
		row, sids, err := s.softDeleteLocked(ctx, tx, q, u, s.now())
		if errors.Is(err, ErrDeletionBlocked) {
			return errProviderDeletionBlocked
		}
		if err != nil {
			return err
		}
		revoked, deleted = sids, true
		res.Status = ProviderDeletionScheduled
		if row.PurgeTime != nil {
			res.PurgeTime = *row.PurgeTime
		}
		return nil
	})
	if errors.Is(err, errProviderDeletionBlocked) {
		res, err = ProviderDeletionResult{ConfirmationCode: res.ConfirmationCode, Status: ProviderDeletionBlocked}, nil
		unbound, deleted, revoked = false, false, nil
	}
	tmpl.UserID = target.UserID
	ev := tmpl
	ev.ID, ev.Type, ev.IdentityKind, ev.SubjectHint = code, enum.EventProviderDataDeletionProcessed, kind, audit.Hint(subject)
	if err != nil {
		ev.Result = enum.ResultFailure
		s.record(ctx, ev)
		return ProviderDeletionResult{}, err
	}
	switch {
	case unbound:
		s.afterRevokeAll(ctx, revoked, enum.RevokeIdentityUnbound, tmpl)
		s.record(ctx, audit.AuthenticationEvent{Type: enum.EventIdentityUnbound, Actor: enum.ActorSystem, Result: enum.ResultSuccess, UserID: target.UserID, IdentityID: target.ID, IdentityKind: target.Kind, SubjectHint: audit.Hint(subject), IP: meta.IP, RequestID: meta.RequestID})
	case deleted:
		s.afterSoftDelete(ctx, revoked, tmpl)
	}
	ev.Result, ev.Reason = enum.ResultSuccess, string(res.Status)
	s.record(ctx, ev)
	return res, nil
}
```

注意事项：
- `RevokeSessionsByIdentity` 的返回类型以 sqlc 生成为准（`UnbindIdentity` 中直接赋给 `[]string`）。
- `GetActiveIdentityByIDAndUserParams` 字段名以 `user/db` 生成代码为准。
- `audit.Hint` 期望输入是摘要；这里传入原始 subject，`Hint` 只取前 8 字节。若 spec 要求的“脱敏提示”不应暴露 ASID 前缀，改为 `audit.Hint(hex.EncodeToString(sha256.Sum256([]byte(subject))[:]))`。实施时选后者，并在 `IDENTITY_UNBOUND` 事件中使用同一值。
- `softDeleteLocked` 内部会再次 `requireState(u, enum.UserActive)`；冻结已在前面返回 BLOCKED。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./user/ -run 'ProviderDataDeletion|Facebook' -count=1 -race`
Expected: PASS（需 `SERVER_TEST_DB_DSN`；无 DSN 时 skip 并在 verification.md 记为未执行）。然后运行 `go test ./user/... -count=1` 确认无回归。

- [ ] **Step 5: 提交**

```bash
gofmt -w user
git add user
git commit -m "feat(user): handle provider-initiated identity data deletion"
```

---

### Task 6: 门面入口（task 4.6）

**Files:**
- Modify: `accountkit.go`
- Test: `accountkit_facebook_test.go`（新，`package accountkit_test`）

**Interfaces:**
- Consumes: Task 3 `Auth.facebook`，Task 4 `ParseSignedRequest`，Task 5 `DeleteProviderIdentityData`。
- Produces:

```go
var ErrFacebookNotConfigured = errors.New("accountkit: facebook sign-in not configured")
func (a *Auth) HandleFacebookDataDeletion(ctx context.Context, signedRequest string, meta user.Meta) (user.ProviderDeletionResult, error)
```

- [ ] **Step 1: 写失败测试**

```go
func TestHandleFacebookDataDeletion(t *testing.T) {
	ctx := context.Background()
	c := validConfig() // 以已有辅助为准
	a := newTestAuth(t, c)
	if _, err := a.HandleFacebookDataDeletion(ctx, "x.y", user.Meta{}); !errors.Is(err, accountkit.ErrFacebookNotConfigured) {
		t.Fatalf("not configured: %v", err)
	}
	c.FacebookAppID, c.FacebookAppSecret = "123", "secret"
	a = newTestAuth(t, c)
	if _, err := a.HandleFacebookDataDeletion(ctx, signRequest("wrong", `{"algorithm":"HMAC-SHA256","issued_at":1,"user_id":"u"}`), user.Meta{}); !errors.Is(err, idp.ErrInvalidSignedRequest) {
		t.Fatalf("bad signature: %v", err)
	}
}
```

（`signRequest` 在本测试文件中复制一份；数据库可用时再追加一条成功用例：未知用户返回 DELETED 且确认码非空。）

- [ ] **Step 2: 运行确认失败**

Run: `go test . -run HandleFacebookDataDeletion -count=1`
Expected: 编译失败。

- [ ] **Step 3: 实现**

```go
// ErrFacebookNotConfigured：未配置 FacebookAppID 时调用 Facebook 数据删除入口。
var ErrFacebookNotConfigured = errors.New("accountkit: facebook sign-in not configured")

// HandleFacebookDataDeletion 处理 Meta 数据删除回调：校验 signed_request 后按 Facebook 用户 id
// 删除对应身份数据。宿主负责 HTTP 路由、把结果编码为 {url, confirmation_code} 以及状态页；
// 签名无效返回 idp.ErrInvalidSignedRequest，宿主应返回 400；其他错误应返回 5xx 让 Meta 重试。
func (a *Auth) HandleFacebookDataDeletion(ctx context.Context, signedRequest string, meta user.Meta) (user.ProviderDeletionResult, error) {
	if a.facebook == nil {
		return user.ProviderDeletionResult{}, ErrFacebookNotConfigured
	}
	req, err := a.facebook.ParseSignedRequest(signedRequest)
	if err != nil {
		return user.ProviderDeletionResult{}, err
	}
	return a.users.DeleteProviderIdentityData(ctx, enum.IdentityFacebook, req.UserID, req.IssueTime, meta)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test . -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w .
git add accountkit.go accountkit_facebook_test.go
git commit -m "feat: expose Facebook data deletion entry point"
```

---

### Task 7: accountsvc 配置（task 5.1）

**Files:**
- Modify: `internal/accountsvc/config.go:115-135,287`
- Test: `internal/accountsvc/config_test.go`

- [ ] **Step 1: 写失败测试**（沿用该文件中设置完整有效环境的辅助）

```go
func TestServiceFacebookConfig(t *testing.T) {
	setServeEnv(t) // 以文件中已有辅助为准
	t.Setenv("ACCOUNTKIT_FACEBOOK_APP_ID", "123")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ACCOUNTKIT_FACEBOOK_APP_SECRET") {
		t.Fatalf("missing secret: %v", err)
	}
	setServeEnv(t)
	t.Setenv("ACCOUNTKIT_ENABLED_IDENTITY_KINDS", "FACEBOOK")
	t.Setenv("ACCOUNTKIT_REQUIRED_IDENTITY_KINDS", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ACCOUNTKIT_FACEBOOK_APP_ID") {
		t.Fatalf("enabled without app: %v", err)
	}
	t.Setenv("ACCOUNTKIT_FACEBOOK_APP_ID", "123")
	t.Setenv("ACCOUNTKIT_FACEBOOK_APP_SECRET", "s")
	clearSMTPEnv(t)
	if _, err := Load(); err != nil {
		t.Fatalf("facebook-only service without SMTP: %v", err)
	}
}
```

再加 migrate 不要求 Facebook 变量的用例（参照文件中 Google/OIDC 的 migrate 用例）。函数名 `Load`、`setServeEnv`、`clearSMTPEnv` 以文件实际名称为准。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/accountsvc/ -run Facebook -count=1`
Expected: FAIL（错误未指出变量名）。

- [ ] **Step 3: 实现**

显式 Enabled 校验处追加：

```go
		if c.identityEnabled(enum.IdentityFacebook) && c.Library.FacebookAppID == "" {
			return Config{}, invalidConfig("ACCOUNTKIT_FACEBOOK_APP_ID")
		}
```

`names` 映射追加 `"FacebookAppSecret": "FACEBOOK_APP_SECRET", "FacebookGraphAPIVersion": "FACEBOOK_GRAPH_API_VERSION", "FacebookNonceTTL": "FACEBOOK_NONCE_TTL", "FacebookGraphAPIBaseURL": "FACEBOOK_GRAPH_API_BASE_URL", "FacebookJWKSURL": "FACEBOOK_JWKS_URL"`。若该映射按错误文本中的字段名定位变量，确认 Task 3 的错误文本以字段名开头（如 `accountkit: FacebookAppSecret is required ...`）能被匹配；不匹配时调整映射的查找方式与 Google 相同。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/accountsvc/ -count=1`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/accountsvc
git add internal/accountsvc
git commit -m "feat(accountsvc): configure Facebook sign-in"
```

---

### Task 8: 文档与必需测试登记（tasks 5.2–5.4）

**Files:**
- Modify: `README.md`、`docs/go-integration.md`、`docs/accountsvc.md`、`docs/compatibility.md`、`deploy/accountsvc/.env.example`、`scripts/required-tests.txt`

- [ ] **Step 1: README 配置表**：在 Google 配置行之后加入六个 Facebook 字段与环境变量、默认值、启用条件；说明 Graph API 默认版本 v26.0 的升级属于兼容变更。
- [ ] **Step 2: `docs/go-integration.md`**：新增“Facebook 登录”一节，写两种 credential 示例 JSON；email 只作提示且 Facebook 不提供验证声明；Classic token 可在有效期内重复使用、被偷风险等同 refresh token、建议在 Meta 后台开启 Require App Secret。新增“Facebook 数据删除回调”一节，给出宿主 handler 示例：

```go
mux.HandleFunc("POST /facebook/data-deletion", func(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	res, err := auth.HandleFacebookDataDeletion(r.Context(), r.PostForm.Get("signed_request"), user.Meta{IP: clientIP(r), RequestID: requestID(r)})
	switch {
	case errors.Is(err, idp.ErrInvalidSignedRequest):
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	saveDeletionStatus(r.Context(), res) // 宿主持久化确认码与状态
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"url":               "https://example.com/deletion-status?code=" + res.ConfirmationCode,
		"confirmation_code": res.ConfirmationCode,
	})
})
```

并说明三种状态含义、BLOCKED 需要宿主跟进、确认码对应的审计为尽力写入、回退旧版本后 FACEBOOK 身份不能登录。
- [ ] **Step 3: `docs/accountsvc.md`**：列出 `ACCOUNTKIT_FACEBOOK_*`；写明服务不提供数据删除回调，部署方须在 Meta 后台配置数据删除说明页或另行实现回调。
- [ ] **Step 4: `docs/compatibility.md`**：功能对照表加入 FACEBOOK 与数据删除入口；按文件已有格式登记新增必需测试与对应文件。
- [ ] **Step 5: `deploy/accountsvc/.env.example`**：加入注释掉的 `ACCOUNTKIT_FACEBOOK_APP_ID=`、`ACCOUNTKIT_FACEBOOK_APP_SECRET=`（值留空，不含真实秘密）。
- [ ] **Step 6: `scripts/required-tests.txt`**：登记本计划新增的全部顶层测试（Task 1–7），按文件既有排序插入。运行 `go test ./internal/testgate/ -count=1` 及其检查入口（以 `scripts/verify.sh` 中调用 testgate 的方式为准），确认登记与实际测试一致。
- [ ] **Step 7: 核对**：`rg -n "FACEBOOK_|Facebook" README.md docs deploy` 与代码中的变量名、函数名逐一比对；确认 design.md 的 Risks 五项都在文档中出现。
- [ ] **Step 8: 提交**

```bash
git add README.md docs deploy scripts/required-tests.txt
git commit -m "docs: document Facebook sign-in and data deletion callback"
```

---

### Task 9: 集成核对（task 6.1）

- [ ] **Step 1:** 运行

```bash
export GOWORK=off
go build ./...
go vet ./...
go test -count=1 ./...
bash scripts/check-generated.sh
bash scripts/check-migrations.sh
openspec validate --all --strict
git diff develop --stat -- migrations tests/testdata/source-baseline user/db audit/db
```

Expected: 全部通过；最后一条无输出。

- [ ] **Step 2:** 有 `SERVER_TEST_DB_DSN` 与 CGO 时运行 `go test -race -count=1 ./user/... .`；有三个一次性数据库时运行 `bash scripts/verify.sh`。条件不足时如实记录。
- [ ] **Step 3:** 新建 `openspec/changes/add-facebook-sign-in/verification.md`，写入实际执行的命令、结果摘要、跳过与未验证项。
- [ ] **Step 4:** 提交

```bash
git add openspec/changes/add-facebook-sign-in
git commit -m "docs(openspec): record facebook sign-in verification"
```

## Self-Review 记录

- Spec 覆盖：facebook-sign-in 七条要求 → Task 2、3；provider-data-deletion 六条 → Task 4、5、6；identity-provider-registry 两条 → Task 1、3；accountsvc-runtime → Task 7；standardized-metrics → Task 1、2（降级与方法值），Classic 只记一次验证器调用由现有 `observationDependency` 包装保证，Task 2 不另加计数。
- 已知需实施时以代码为准的名称：`validConfig`、`newTestAuth`、`setValidEnv`、`principalOf`、`Refresh`、`AdminFreezeUser`、`testAuditMemory.Events`、accountsvc 测试辅助。实施者必须先 `rg` 确认，不得新造重复辅助。
- design.md 的 Open Question（默认版本）在本计划确定为 v26.0，Task 3 完成时同步更新 design.md 的 Open Questions 与配置小节。
