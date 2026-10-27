# 本地 access 验签与严格内省 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消费者 access 改为只用 ES256 签发并发布 JWKS，内省改以数据库会话吊销状态为准、fail-closed。

**Architecture:** `tokens.Signer` 只持 P-256 私钥，签发 `typ=at+jwt`，并在构造时预生成 JWKS。`Auth.JWKSHandler()` 输出 JWKS。`user.Service.AuthenticateStrict` 在 JWT 校验后按 sid 读 `session.user_id/revoke_time`，accountsvc 的内省改用它。业务端示例增加本地验签器。

**Tech Stack:** Go 1.26、golang-jwt/jwt/v5（ES256）、crypto/ecdsa、crypto/x509（PKCS#8）、sqlc、chi、pgx/v5。

**Spec:** `openspec/changes/add-local-access-token-verification/`（proposal.md、design.md、specs/、tasks.md）

## Global Constraints

- 签名算法只用 ES256（P-256）；header 为 `alg=ES256`、`typ=at+jwt`、`kid=<十进制版本号>`；claims 不变。
- `Config.JWTKeys` 类型为 `map[uint16]*ecdsa.PrivateKey`；环境变量 `JWT_KEYS` 格式为 `版本:base64(PKCS#8 DER)`；错误信息只指出变量名，不回显值。
- JWKS 每个 key：`kty=EC`、`crv=P-256`、`x`、`y`（32 字节 base64url）、`kid`、`alg=ES256`、`use=sig`；不得输出 `d`。
- accountsvc 路由为 `GET /.well-known/jwks.json`，无认证，`Cache-Control: public, max-age=300`。
- 内省：按 sid 查会话；未命中、`user_id ≠ sub` 或已吊销返回 `active:false`；数据库错误返回 503 `temporarily_unavailable`；不读 Redis，不检查 refresh 期限。
- `Authenticate` 与中间件行为不变（JWT + Redis 吊销，fail-open）。
- 不新增迁移，不手改 `user/db/`，修改 Go 后执行 gofmt，构建使用 `GOWORK=off`。

## Review Focus

- 用公钥字节作 HMAC 密钥伪造的 HS256 令牌必须被拒绝（Task 1 测试 `hs256 with public key`）。
- JWK 坐标前导零：`x`/`y` 必须定长 32 字节（Task 1 测试循环生成密钥并断言解码后长度为 32）。
- 旧 HMAC 环境变量值必须得到指名变量的清晰错误，且不回显值（Task 2 测试）。
- 吊销已提交但 Redis 写入失败时，严格认证必须判定无效（Task 4 数据库测试）。
- 刷新期限已到但未吊销的会话，严格认证必须仍判定有效（Task 4 数据库测试）。

---

### Task 1: tokens 改为 ES256 并输出 JWKS（tasks 1.1–1.3）

**Files:**
- Modify: `tokens/tokens.go`
- Create: `tokens/keys.go`（PKCS#8 与 key list 解析）
- Create: `internal/testkeys/testkeys.go`（测试用的共享 ES256 密钥）
- Test: `tokens/tokens_test.go`、`tokens/keys_test.go`

**Interfaces:**
- Produces:
  - `tokens.Options{Keys map[uint16]*ecdsa.PrivateKey; Active uint16; Issuer, Audience string; Leeway time.Duration}`
  - `tokens.CheckKeys(keys map[uint16]*ecdsa.PrivateKey, active uint16) error`
  - `tokens.ParsePrivateKey(der []byte) (*ecdsa.PrivateKey, error)`
  - `tokens.ParseKeyList(spec string) (map[uint16]*ecdsa.PrivateKey, error)`
  - `(*Signer).JWKS() []byte`
  - 常量 `tokens.Algorithm = "ES256"`、`tokens.TokenType = "at+jwt"`
  - `testkeys.JWT(i int) *ecdsa.PrivateKey`（按 i 缓存）、`testkeys.JWTBase64(i int) string`
  - 删除 `tokens.MinKeyLen`

- [ ] **Step 1: 编写 testkeys**

```go
// Package testkeys 为测试提供按序号缓存的合成 ES256 签名密钥，不用于生产。
package testkeys

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"sync"
)

var (
	mu   sync.Mutex
	keys = map[int]*ecdsa.PrivateKey{}
)

// JWT 返回序号 i 对应的 P-256 私钥；同一进程内同一序号返回同一密钥。
func JWT(i int) *ecdsa.PrivateKey {
	mu.Lock()
	defer mu.Unlock()
	if k, ok := keys[i]; ok {
		return k
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	keys[i] = k
	return k
}

// JWTBase64 返回 JWT(i) 的 PKCS#8 DER 标准 base64，用于 JWT_KEYS 环境变量。
func JWTBase64(i int) string {
	der, err := x509.MarshalPKCS8PrivateKey(JWT(i))
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}
```

- [ ] **Step 2: 改写 tokens 测试（先失败）**

在 `tokens_test.go` 中，`opts()` 改用 `testkeys.JWT(1)`、`testkeys.JWT(2)`；round-trip 测试断言 header 为 `alg=ES256`、`typ=at+jwt`、`kid=2`；拒绝表新增以下用例：
- `hs256 with public key`：用 `x509.MarshalPKIXPublicKey(&testkeys.JWT(2).PublicKey)` 的字节作 HMAC 密钥签 HS256，`kid=2`，`typ=at+jwt`。
- `no typ`：用 ES256 私钥直接签，header 中不放 `typ`。
- `wrong typ`：`typ=JWT`。
- `other key same kid`：用 `testkeys.JWT(9)` 以 kid 2 签发。

NewSigner 的拒绝表包括：nil key、P-384 key、active 未配置、iss 为空、aud 为空。新增 `TestJWKS`：
- 解析 JSON，断言 `keys` 长度为 2、按 kid 升序，字段齐全且没有 `d`。
- 用 `x`/`y` 重建公钥后，能验证对应 kid 签发的令牌。
- 循环 64 次生成新密钥，断言 x/y 解码后都是 32 字节。

`keys_test.go` 覆盖以下情况：合法的 `1:<b64>,2:<b64>` 可以解析；HMAC 32 字节随机值、P-384 PKCS#8、RSA PKCS#8、非 base64 都返回 error，且 error 文本不含输入值。

Run: `GOWORK=off go test ./tokens/...` → 编译失败（预期结果）。

- [ ] **Step 3: 实现**

`tokens/tokens.go` 的要点：

```go
const (
	Algorithm = "ES256"
	TokenType = "at+jwt"
)

type Signer struct {
	keys     map[uint16]*ecdsa.PrivateKey
	active   uint16
	issuer, audience string
	leeway   time.Duration
	jwks     []byte
}

func CheckKeys(keys map[uint16]*ecdsa.PrivateKey, active uint16) error {
	if len(keys) == 0 {
		return errors.New("tokens: no signing keys configured")
	}
	for v, k := range keys {
		if k == nil || k.Curve != elliptic.P256() {
			return fmt.Errorf("tokens: key version %d must be a P-256 private key", v)
		}
	}
	if _, ok := keys[active]; !ok {
		return fmt.Errorf("tokens: active key version %d not configured", active)
	}
	return nil
}
```

各方法的改动：
- `Sign`：使用 `jwt.SigningMethodES256`，设置 `tok.Header["typ"] = TokenType`，用 `s.keys[s.active]` 签名。
- `Parse`：`jwt.WithValidMethods([]string{Algorithm})`；keyfunc 先检查 `t.Header["typ"] == TokenType`，再按 kid 返回 `&k.PublicKey`。
- `NewSigner` 构造时预生成 jwks：key 按版本升序；`pub.Bytes()` 得到 65 字节非压缩点，x=b[1:33]、y=b[33:65]，均用 base64.RawURLEncoding 编码；`kid` 为十进制字符串。
- `JWKS()` 返回副本。

`tokens/keys.go`：

```go
// ParsePrivateKey 解析 PKCS#8 DER，只接受 P-256 ECDSA 私钥；错误不包含输入。
func ParsePrivateKey(der []byte) (*ecdsa.PrivateKey, error) {
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("tokens: key is not PKCS#8")
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok || ec.Curve != elliptic.P256() {
		return nil, errors.New("tokens: key must be a P-256 ECDSA private key")
	}
	return ec, nil
}

// ParseKeyList 解析 "版本:base64(PKCS#8),..."。
func ParseKeyList(spec string) (map[uint16]*ecdsa.PrivateKey, error) {
	raw, err := pii.ParseKeyList(spec)
	if err != nil {
		return nil, err
	}
	out := make(map[uint16]*ecdsa.PrivateKey, len(raw))
	for v, der := range raw {
		k, err := ParsePrivateKey(der)
		if err != nil {
			return nil, fmt.Errorf("version %d: %w", v, err)
		}
		out[v] = k
	}
	return out, nil
}
```

实现前先确认 `pii` 不 import `tokens`，避免循环依赖。

- [ ] **Step 4:** 运行 `GOWORK=off go test ./tokens/... && go vet ./tokens/...`，预期 PASS。
- [ ] **Step 5:** 提交 `feat(tokens): sign consumer access tokens with ES256 and expose JWKS`。

### Task 2: 配置、门面 JWKSHandler 与测试夹具迁移（tasks 2.1–2.3）

**Files:**
- Modify: `config.go`、`accountkit.go`
- Modify（夹具）：`accountkit_alipay_http_test.go`、`compatibility_test.go`、`config_*_test.go`、`config_test.go`、`identity_policy_config_test.go`、`isolation_test.go`、`oidc_config_test.go`、`wechat_config_test.go`、`user/service_test.go`、`user/service_import_test.go`、`internal/accountsvc/introspection/compatibility_test.go`、`internal/accountsvc/fixtures_integration_test.go`
- Create: `jwks_test.go`

**Interfaces:**
- Consumes：Task 1 的全部接口。
- Produces：`func (a *Auth) JWKSHandler() http.Handler`。

- [ ] **Step 1: 编写测试**

`jwks_test.go`：用不可达的 pool 和已停止的 miniredis 构造 `Auth`，验证以下行为。
- GET 返回 200、`application/json`、`Cache-Control: public, max-age=300`，body 等于 `Signer.JWKS()` 的内容，按 kid 和 key 数量断言。
- HEAD 返回 200 且 body 为空。
- POST 返回 405，并带 `Allow: GET, HEAD`。

`config_test.go`：
- 合法配置改用 `testkeys.JWT(1)`。
- 环境变量值改用 `testkeys.JWTBase64(1)`。
- `"jwt key short"` 用例改为 `"jwt key wrong curve"`（P-384）。
- 新增 `T_JWT_KEYS="1:"+b64(k(1))`（旧 HMAC 值），断言错误包含 `T_JWT_KEYS` 且不包含该值。

- [ ] **Step 2: 实现**
- `config.go`：`JWTKeys map[uint16]*ecdsa.PrivateKey`；Validate 改为 `tokens.CheckKeys(c.JWTKeys, c.JWTActiveKey)`，错误包装为 `accountkit: JWTKeys: %w`；ConfigFromEnv 中的 `JWT_KEYS` 改用 `tokens.ParseKeyList`，失败时调用 `fail("JWT_KEYS", err)`，确认 fail 不回显值。
- `accountkit.go`：在 `Auth` 中保存 `jwks []byte`（来自 `signer.JWKS()`）。

```go
// JWKSHandler 返回消费者 access 验签公钥集（宿主挂载到 /.well-known/jwks.json）。不访问数据库或 Redis。
func (a *Auth) JWKSHandler() http.Handler {
	body := a.jwks
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	})
}
```

- 夹具迁移：`map[uint16][]byte{1: …}` 形式的 JWT 密钥改为 `map[uint16]*ecdsa.PrivateKey{1: testkeys.JWT(1)}`；环境变量值改为 `testkeys.JWTBase64(n)`。`compatibility_test.go` 中手工 HMAC 伪造的令牌改为用 `jwt.NewWithClaims(jwt.SigningMethodES256, …)` 签名，header 写入 `kid=1`、`typ=at+jwt`，私钥用 `cfg.JWTKeys[1]`。
- [ ] **Step 3:** 运行 `GOWORK=off go build ./... && go vet ./... && go test -count=1 ./...`，预期 PASS（数据库用例 skip）；`rg "JWTKeys: map\[uint16\]\[\]byte"` 应无结果。
- [ ] **Step 4:** 提交 `feat: switch JWT key configuration to ES256 and add JWKS handler`。

### Task 3: 会话吊销状态查询（task 3.1）

**Files:** Modify `user/query.sql`；生成 `user/db/query.sql.go`

- [ ] **Step 1:** 在 `LockSessionByID` 之后加入：

```sql
-- name: GetSessionRevocationState :one
-- 严格认证/内省：按主键读取会话归属与吊销状态，不加锁、不读刷新期限。
SELECT user_id, revoke_time FROM session WHERE id = $1;
```

- [ ] **Step 2:** 运行 `sqlc generate && bash scripts/check-generated.sh`，确认 diff 只新增这一个函数。
- [ ] **Step 3:** 提交 `feat(user): add session revocation state query`。

### Task 4: AuthenticateStrict（tasks 3.2–3.4）

**Files:** Modify `user/service_session.go`、`user/observability.go`；Test `user/service_test.go`（单元测试）和数据库集成测试文件（沿用现有 `SERVER_TEST_DB_DSN` 辅助函数，放在会话相关的集成测试文件中）。

**Interfaces:** Produces `func (s *Service) AuthenticateStrict(ctx context.Context, token string) (Principal, error)`；无效令牌返回值满足 `errors.Is(err, ErrInvalidToken)`；数据库错误返回的值不满足该条件。

- [ ] **Step 1: 编写测试**
- 单元测试：签名错误的令牌返回 `ErrInvalidToken`，且不触达 Repo（pool 指向不可达地址，若触达则会得到非 Invalid 错误）。
- 数据库测试，按顺序覆盖：
  1. 有效会话返回 Principal，字段与 `Authenticate` 相同；
  2. `RevokeSession` 后返回 Invalid；
  3. 关闭 miniredis 后 `RevokeSession`（Redis 写失败），严格认证返回 Invalid，而 `Authenticate` 仍然成功；
  4. 把 refresh_expire_time 改成过去时间，会话仍有效；
  5. 删除会话行后返回 Invalid；
  6. 用他人 sub 签发的令牌返回 Invalid；
  7. 冻结、注销、revokeOthers 后，对应会话返回 Invalid；
  8. 关闭 pool 后返回非 Invalid 错误；
  9. 前后快照 session、session_client_info、session_authentication_identity 行，确认不变。
- 回归：既有 Authenticate fail-open 测试保持不变且通过。
- [ ] **Step 2: 实现**

```go
func (s *Service) authenticateStrict(ctx context.Context, rawAccess string) (Principal, error) {
	c, err := s.d.Signer.Parse(rawAccess)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !ids.Valid(ids.User, c.UserID) || !ids.Valid(ids.Session, c.SessionID) {
		return Principal{}, fmt.Errorf("%w: malformed sub/sid", ErrInvalidToken)
	}
	row, err := s.d.Repo.Q().GetSessionRevocationState(ctx, c.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, fmt.Errorf("%w: session not found", ErrInvalidToken)
	}
	if err != nil {
		return Principal{}, fmt.Errorf("user: session state: %w", err)
	}
	if row.UserID != c.UserID || row.RevokeTime.Valid {
		s.observationReason(ctx, ob.ReasonSessionRevoked)
		return Principal{}, fmt.Errorf("%w: session revoked", ErrInvalidToken)
	}
	return Principal{UserID: c.UserID, SessionID: c.SessionID, Scope: c.Scope, AuthTime: c.AuthTime}, nil
}
```

`RevokeTime` 的实际生成类型以 sqlc 输出为准。在 `observability.go` 中添加导出包装：结构与 `Authenticate` 相同，操作使用 `ob.AuthAuthenticate`；注释说明以数据库为准，依赖失败时不放行。
- [ ] **Step 3:** 运行 `GOWORK=off go test -count=1 ./user/...`，有 DSN 时连同数据库测试一起运行。
- [ ] **Step 4:** 提交 `feat(user): add database-backed strict access authentication`。

### Task 4b: 严格认证中间件（task 3.4）

**Files:** Modify `httpapi/authn/authn.go`、`accountkit.go`；Test `httpapi/authn/authn_test.go`

**Interfaces:**
- `type StrictAuthenticator interface { AuthenticateStrict(ctx context.Context, rawAccess string) (user.Principal, error) }`
- `func RequireScopeStrict(a StrictAuthenticator, o Options, allowed ...string) func(http.Handler) http.Handler`
- `func (a *Auth) RequireScopeStrict(allowed ...string) func(http.Handler) http.Handler`

- [ ] **Step 1:** 编写测试，用桩 StrictAuthenticator 覆盖以下用例：
  - 缺 token 返回 401 `TOKEN_MISSING`；
  - Invalid 返回 401，并带 `error="invalid_token"` challenge；
  - `user.ErrUnavailable` 返回 503，且下游未被调用；
  - scope 不在允许列表中返回 403；
  - ctx 中已有 Principal（外层 RequireScope 注入）时仍调用 strict，strict 返回 Invalid 时结果为 401；
  - 串联 RequireRecentAuth 时，auth_time 过旧返回既有错误。
- [ ] **Step 2:** 实现。把 `Bearer` 的内部逻辑抽成 `bearerWith(authenticate func(ctx, raw) (user.Principal, error), reuseExisting bool, o Options)`。`Bearer` 调用时传 `reuseExisting=true`；strict 传 false，并用 `WithPrincipal` 覆盖 ctx 中已有的 Principal。scope 检查复用 RequireScope 的逻辑（抽出 `scopeCheck(set)`）。门面方法用 `a.users` 作为 StrictAuthenticator。
- [ ] **Step 3:** 运行 `GOWORK=off go test ./httpapi/authn/... .`，然后提交 `feat(authn): strict scope middleware for critical routes`。

### Task 5: accountsvc 装配与部署模板（tasks 4.1–4.3）

**Files:** Modify `internal/accountsvc/run.go`、`internal/accountsvc/http.go`、`internal/accountsvc/introspection/handler.go`（只改注释）、`internal/accountsvc/introspection/compatibility_test.go`、`internal/accountsvc/http_test.go`、相关集成测试、`deploy/accountsvc/compose.yaml` 及配置模板。

- [ ] **Step 1: 编写测试**
- introspection compatibility：改用 `AuthenticateStrict`；pool 不可达时，有效令牌应返回 503 `temporarily_unavailable` 和 no-store（原断言是 Redis fail-open 时返回 active:true，需要改写），无效令牌仍返回 active:false。
- `http_test.go`：`/.well-known/jwks.json` 无需凭据即可返回 200；`/v1/introspect` 仍需 Basic；`/internal/v1/introspect` 返回 404。
- 集成测试 (`service_integration_test.go`、`gateway_integration_test.go`)：
  - 加入 JWKS 拉取，并用拉取到的公钥本地验证登录得到的 access；
  - 撤销后内省立即返回 active:false。
- [ ] **Step 2: 实现**
- `run.go` 中 `introspection.New(..., auth.Users().AuthenticateStrict)`。
- `serviceHandler` 新增参数 `jwks http.Handler`，`router.Method(GET/HEAD, "/.well-known/jwks.json", jwks)`。
- 更新 handler 包注释：令牌由数据库校验。
- `compose.yaml`：保留 `JWT_KEY` 变量名，在注释中给出生成命令：

```
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -outform DER | base64 -w0
```

- [ ] **Step 3:** 运行 `GOWORK=off go test -count=1 ./internal/accountsvc/...`。
- [ ] **Step 4:** 提交 `feat(accountsvc): strict introspection and JWKS route`。

### Task 6: 业务端示例（tasks 5.1–5.2）

**Files:** Create `examples/remoteauth/local.go`、`examples/remoteauth/local_test.go`；Modify `examples/remoteauth/handler.go`、`examples/remoteauth/client_test.go`。

**Interfaces:**
- `type VerifierConfig struct { JWKSURL, Issuer, Audience string; HTTPClient *http.Client; Timeout, Leeway, MinRefreshInterval time.Duration; AllowHTTP bool }`
- `func NewVerifier(cfg VerifierConfig) (*Verifier, error)`
- `func (v *Verifier) Verify(ctx context.Context, raw string) (Result, error)`：无效时返回 `Result{Active:false}, nil`；JWKS 不可用时返回 `ErrUnavailable`。
- `ProtectedHandler` 新增字段 `Verifier *Verifier` 和 `Critical bool`：总是先本地验签；Critical 为 true 时再调用 `Client.Introspect`。

- [ ] **Step 1: 编写测试**
- `local_test.go`：
  - 使用 httptest TLS server 提供 JWKS（由 `tokens.NewSigner(...).JWKS()` 生成）；
  - 有效令牌通过；
  - HS256、none、`typ` 错误、aud 不符、iss 不符、过期、sub 格式错误都返回 Active=false；
  - 未知 kid 触发刷新：服务端换成新 JWKS 后通过；
  - 刷新节流：MinRefreshInterval 内的第二个未知 kid 不再请求；
  - JWKS 返回 500 时返回 ErrUnavailable；
  - 重定向被拒绝；
  - http URL 在 AllowHTTP=false 时被拒绝。
- `client_test.go` 新增 handler 分级测试：撤销场景用内省桩返回 active:false。普通路由只本地验签，返回 204；Critical 路由返回 401；内省返回 503 时 Critical 路由返回 503。
- [ ] **Step 2: 实现**
- Verifier 用互斥锁缓存 `map[string]*ecdsa.PublicKey`；首次 Verify 时拉取 JWKS，未知 kid 时按 MinRefreshInterval（默认 60s）节流刷新。
- 响应限制为 64 KiB，禁止重定向，HTTPS 默认开启。
- JWK 只接受 `kty=EC`、`crv=P-256`、`alg` 为空或 ES256 的条目，并用 `ecdsa.ParseUncompressedPublicKey`（Go 1.25+）从 0x04‖x‖y 重建公钥。
- 用 jwt/v5 解析：`WithValidMethods(ES256)`、`WithIssuer`、`WithAudience`、`WithLeeway`、`WithExpirationRequired`，并在 keyfunc 中检查 typ。
- [ ] **Step 3:** 运行 `GOWORK=off go test -count=1 ./examples/...`。
- [ ] **Step 4:** 提交 `feat(examples): local JWKS verification with tiered introspection`。

### Task 7: 文档（tasks 6.1–6.4）

**Files:** `README.md`、`docs/accountsvc.md`、`docs/gateway-integration.md`、`docs/compatibility.md`、`AGENTS.md`

- [ ] **Step 1: README**
  - 配置表 `JWT_KEYS` 改为 “`1:<base64 PKCS#8>` 版本化 ES256（P-256）私钥”，并附生成命令；
  - API 表新增 `AuthenticateStrict` 和 `JWKSHandler`；
  - Redis 失败模式表新增一行：严格认证不读 Redis，数据库失败时返回错误；
  - 会话过期一节把“原access和内省仍按JWT及Redis吊销规则判断”改为分别描述 Authenticate 和内省。
- [ ] **Step 2: `docs/accountsvc.md`**
  - 改写内省一节：数据库为准、fail-closed、刷新期限不影响、503 含义；
  - 新增“分级接入”和“JWKS 与密钥轮换（五步）”两节；
  - 删除“每次受保护业务请求查询内省”和“服务不发放消费者签名密钥给业务服务”的旧表述，改为“只发布公钥”。
- [ ] **Step 3: 其余文档**
  - `docs/gateway-integration.md`：新增 JWKS 入口策略；
  - `docs/compatibility.md`：新增本次破坏性变更一节；
  - `AGENTS.md`：Redis 失败模式一条补充“严格认证/内省以数据库为准，依赖失败拒绝”。
- [ ] **Step 4:** 运行 `rg -n "HS256|每次受保护业务请求查询内省" README.md docs AGENTS.md`，命中只应出现在历史记录或兼容说明中；检查新增链接。
- [ ] **Step 5:** 提交 `docs: local access verification and strict introspection`。

### Task 8: 验证与记录（tasks 7.1–7.3）

- [ ] **Step 1:** 运行 `GOWORK=off go build ./... && go vet ./... && go test -count=1 ./...`、`bash scripts/check-generated.sh`、`bash scripts/check-migrations.sh`、`openspec validate --all --strict`。
- [ ] **Step 2:** 用一次性 PostgreSQL 17 容器（3 个库）设置 `SERVER_TEST_DB_DSN` 和两个恢复 DSN，运行 `bash scripts/verify.sh`；Windows 下用 `tests/Dockerfile` 镜像运行。新增的必需顶层测试写入 `scripts/required-tests.txt`。
- [ ] **Step 3:** 编写 `openspec/changes/add-local-access-token-verification/verification.md`，记录实际运行的检查和未验证项，然后提交。
