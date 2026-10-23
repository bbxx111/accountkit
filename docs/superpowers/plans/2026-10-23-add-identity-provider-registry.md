# 身份提供方注册表（第一期）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 身份类型改为字符串，第三方登录统一经注册表与 `idp.Provider` 分派，C 端凭证统一为 `{kind, credential}`，并提供按配置启用的通用 OIDC 验证器。

**Architecture:** `enum.IdentityKind` 变为 string 底型，`identity.kind` 改为 `VARCHAR(32) COLLATE "C"`；`user.Service` 持有不可变 `map[IdentityKind]idp.Provider`，HTTP 层只解析信封并把第三方 `credential` 原样交给提供方；Apple/Google/OIDC 共享内部 JWKS 缓存，Google 与 OIDC 共享 ID token 校验核心。

**Tech Stack:** Go 1.26.5、pgx/v5、sqlc 1.31.1、PostgreSQL 17、go-redis/v9（测试 miniredis）、golang-jwt/v5、chi。

**Spec:** `openspec/changes/archive/2026-10-23-add-identity-provider-registry/`（proposal.md、design.md、specs/、tasks.md）。本计划不复制需求；冲突时以 OpenSpec 工件为准。

## Global Constraints

- 构建与测试均在 `GOWORK=off` 下进行；修改 Go 文件后 gofmt；Go/SQL/shell 使用 LF。
- 类型名格式：`^[A-Z][A-Z0-9_]{1,31}$`；内置名 PHONE、EMAIL、WECHAT、APPLE、GOOGLE。
- 只修改当前 `migrations/0001_init.up.sql` 的 `identity.kind` 列定义与相关注释；不新增 0002；down、冻结源与已发布 tag 不变。
- 禁止手改 `user/db/`、`audit/db/`；改查询后 `sqlc generate`。
- 第三方 `credential` 上限 16 KiB；ID token 仅 RS256；时钟偏差 30 秒；nonce 下限默认 10 分钟。
- 错误码沿用：`CREDENTIAL_INCOMPLETE`、`CREDENTIAL_KIND_NOT_ALLOWED`、`IDP_APP_NOT_ALLOWED`、`IDP_CREDENTIAL_INVALID`、`IDP_NONCE_REPLAYED`、`IDP_UNAVAILABLE`、`IDENTITY_KIND_NOT_ENABLED`；`CREDENTIAL_ONEOF` 移除。
- 指标标签只用固定值：方法 `oidc`，降级 `oidc_cached_key_fallback`。
- 提交信息 Conventional Commits，结尾 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`。

## 执行方式

各任务共享写入且存在编译级前后依赖（Task 1 的类型变化波及全部包），不满足并行条件，按顺序在会话内执行；每组完成后做一次规格与代码复核再进入下一组。

---

### Task 1: 字符串 IdentityKind（tasks 1.1、1.5）

**Files:**
- Modify: `enum/enum.go`、`enum/enum_test.go`
- Modify（编译修复）：`user/**`、`httpapi/**`、`audit/authentication/**`、`config.go`、`internal/accountsvc/**`、`examples/**` 中把 kind 当数字使用之处

**Interfaces:**
- Produces:
  ```go
  type IdentityKind string
  const (
      IdentityKindUnspecified IdentityKind = ""
      IdentityPhone  IdentityKind = "PHONE"
      IdentityEmail  IdentityKind = "EMAIL"
      IdentityWeChat IdentityKind = "WECHAT"
      IdentityApple  IdentityKind = "APPLE"
      IdentityGoogle IdentityKind = "GOOGLE"
  )
  func (k IdentityKind) String() string      // 空值返回 "UNSPECIFIED"
  func (k IdentityKind) Valid() bool         // 仅格式
  func (k IdentityKind) IsBuiltin() bool
  func (k IdentityKind) IsAnchor() bool      // PHONE/EMAIL
  func (k IdentityKind) IsChannel() bool     // PHONE/EMAIL
  func ParseIdentityKind(v string) (IdentityKind, error) // 仅格式
  ```

- [ ] **Step 1: 改写 enum 测试**：删除 IdentityKind 数值钉死断言；新增 `TestIdentityKindFormat`（`"AB"`、`"A_1"`、32 字符合法；`""`、`"A"`、33 字符、`"wechat"`、`"1A"`、`"A-B"` 非法），`TestIdentityKindBuiltin`（5 个内置为 true，`CORP_SSO` 为 false），`ParseIdentityKind("CORP_SSO")` 成功、`ParseIdentityKind("facebook")` 失败。
- [ ] **Step 2:** `go test ./enum/` 预期编译失败或断言失败。
- [ ] **Step 3: 实现** enum 变更（正则 `regexp.MustCompile("^[A-Z][A-Z0-9_]{1,31}$")`），更新包注释说明 IdentityKind 为文本例外。
- [ ] **Step 4:** `go test ./enum/` 通过。
- [ ] **Step 5: 修编译点**：`go build ./... 2>&1` 逐个修：`return 0, ...` → `return "", ...`；`slices.Sort` 保留（字符串有序）；`int16(f.IdentityKind)` 改为字符串指针；观测 switch 无需变化。
- [ ] **Step 6:** `go build ./... && go vet ./...`（此时 sqlc 仍生成 int16 字段，`db.Identity.Kind` 类型已是 enum override，会随 Task 2 生成代码完成，Task 1 与 Task 2 合并提交）。

### Task 2: 0001、sqlc、预检与固定哈希（tasks 1.2–1.4）

**Files:**
- Modify: `migrations/0001_init.up.sql`、`user/query.sql`、`migrations/baseline.go`（或新文件 `migrations/identity_kind_baseline.go`）、`internal/migrationcheck/main.go`
- Generated: `user/db/*`
- Test: `migrations/identity_kind_test.go`

- [ ] **Step 1: 写迁移测试**（需 `SERVER_TEST_DB_DSN`，沿用 `avatar_test.go` 的随机 schema 辅助）：
  - 新空 schema `Up` 后：version=1/dirty=false；`information_schema.columns` 中 `identity.kind` 为 `varchar`、长度 32、collation `C`、NOT NULL；`identity_kind_format` 约束存在。
  - 插入 kind=`wechat` 或 `3` 的行被 CHECK 拒绝（SQLSTATE 23514）。
  - 用当前 0001 建库后 `ALTER TABLE identity ALTER COLUMN kind TYPE smallint USING 1` 模拟旧 v1（空与插入一行两种），再 `Up` 返回 `ErrIncompatibleBaseline`，catalog/数据/version/dirty 不变；重复两次仍拒绝且锁已释放（第二次 Up 不阻塞）。
  - 同旧库置 dirty=true 时返回 dirty 错误优先。
- [ ] **Step 2:** 运行测试确认失败。
- [ ] **Step 3: 改 0001**：
  ```sql
  kind                VARCHAR(32) COLLATE "C" NOT NULL  -- PHONE / EMAIL / WECHAT / APPLE / GOOGLE 或配置的 OIDC 类型；Go 定义格式
                      CONSTRAINT identity_kind_format CHECK (kind ~ '^[A-Z][A-Z0-9_]{1,31}$'),
  ```
  同步文件头关于 SMALLINT 的说明与 “第三方类（WECHAT/APPLE）” 注释；索引语句不改。
- [ ] **Step 4: 改 query.sql**：`(@kinds::smallint[])[d.n]` → `(@kinds::text[])[d.n]`；`sqlc.narg('identity_kind')::smallint` → `::text`（两处）。`sqlc generate`，`bash scripts/check-generated.sh`。
- [ ] **Step 5: 预检**：新增 `checkIdentityKindBaseline`，在 `checkBaseline` 末尾调用：
  ```sql
  SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='identity'
   AND column_name='kind' AND udt_name='varchar' AND character_maximum_length=32 AND collation_name='C' AND is_nullable='NO')
   AND EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace
   WHERE n.nspname=$1 AND r.relname='identity' AND c.conname='identity_kind_format' AND c.convalidated)
  ```
  不满足返回 `ErrIncompatibleBaseline`。
- [ ] **Step 6:** 计算 `sha256sum migrations/0001_init.up.sql` 更新 `initialFrozen`；`bash scripts/check-migrations.sh` 与 `go test ./internal/migrationcheck/ ./migrations/` 通过。
- [ ] **Step 7:** 全量 `go build ./... && go vet ./... && go test -count=1 ./...`（含 DSN）通过后提交 `feat(enum): store identity kind as text`。

### Task 3: AGENTS.md 与迁移文档（task 1.6）

- [ ] 在 AGENTS.md “主键、引用与枚举”中加入：IdentityKind 例外为 `VARCHAR(32) COLLATE "C"` 加格式 CHECK，因类型集合可由部署配置扩展；其余枚举仍为 SMALLINT。
- [ ] 在“迁移安全”加入本轮 `add-identity-provider-registry` 授权条目（范围：仅 `identity.kind` 列定义与注释，同步 sqlc、预检、当前 initialFrozen；旧 SMALLINT v1 无写拒绝；不新增 0002）。
- [ ] `docs/migrations.md` 当前初始基线段落同步；检查相对链接。提交 `docs: record identity kind text baseline`。

### Task 4: Provider 接口与内置提供方（tasks 2.1、2.2）

**Files:** `user/idp/idp.go`、`user/idp/wechat.go`、`user/idp/apple.go`、`user/idp/google.go`、`user/idp/policy.go`、对应 `_test.go`

**Interfaces — Produces:**
```go
type Provider interface {
    Kind() enum.IdentityKind
    Verify(ctx context.Context, credential json.RawMessage) (Identity, error)
    PolicyFingerprint() string
}
var ErrMalformedCredential = errors.New("idp: malformed credential")
func decodeCredential(raw json.RawMessage, dst any) error // DisallowUnknownFields + 单值；失败包装 ErrMalformedCredential
```
- 微信凭证 `{app_id, code}`；Apple/Google `{id_token, nonce}`；必填为空 → `ErrMalformedCredential`。
- 原 `VerifyWeChat/VerifyApple/VerifyGoogle` 改为非导出 `verify(ctx, ...)`，`Verify` 解码后调用。

- [ ] 测试先行：每个提供方新增 `TestXxxMalformedCredential`（未知字段、缺字段、非对象、尾随内容），断言 `errors.Is(err, ErrMalformedCredential)` 且 httptest 服务器请求计数为 0、miniredis 无 nonce 键；Google 新增 `PolicyFingerprint` 随 client ID 变化而变化。原测试改为调用 `Verify` 传 JSON。
- [ ] 实现、`go test ./user/idp/` 通过。

### Task 5: 注册表、策略与分派（tasks 2.3、2.4、2.6、2.8）

**Files:** `user/service.go`、`user/identity_policy.go`、`user/service_policy.go`、`user/service_idp.go`、`user/service_signin_completion_store.go`、`user/observability.go`、`user/errors.go`、`httpapi/enduser/response.go`、`accountkit.go`、`config.go`

**Interfaces — Produces:**
```go
// user
type Deps struct { ...; Providers []idp.Provider; ... } // 移除 WeChat/Apple/Google
type IdpCredential struct { Kind enum.IdentityKind; Credential json.RawMessage }
func NewIdentityPolicy(enabled, required []enum.IdentityKind, providers ...enum.IdentityKind) (*IdentityPolicy, error)
func (p *IdentityPolicy) ValidateCapabilities(phone, email bool, providers map[enum.IdentityKind]bool) error
```
- `NewService`：遍历 `Providers` 构建 `s.providers map[IdentityKind]idp.Provider`；nil、格式非法、锚点、重复 → `ErrInvalidArgument` 包装说明。
- `NewIdentityPolicy` 的 check：类型须格式合法且为 PHONE/EMAIL 或在 providers 中；Required 只允许锚点。
- `requireIdentityEnabled`：`!kind.Valid()` → InvalidArgument；Allows → nil；`EnabledIdentityKinds == nil && s.providers[kind] == nil && !kind.IsAnchor()` → `idp.ErrAppNotAllowed`；否则 `ErrIdentityKindNotEnabled`。
- `verifyIdp`：锚点类型 → `ErrInvalidArgument`（HTTP 已提前拦截）；查表调用 `Verify`。
- `ErrMalformedCredential` 在 `idpFailureReason` 中返回空（不审计）；HTTP 映射 400 `CREDENTIAL_INCOMPLETE`。
- 补全指纹：`fingerprintJSON(append([]string{policy.Fingerprint(), d.ProviderConfiguration}, sorted kind+":"+PolicyFingerprint()...))`。
- `Config.identityPolicy()` 与 `accountkit.New` 装配统一由一个 `func (c Config) providers(deps Deps) []idp.Provider` 构建；`identityPolicy` 用其 Kind 列表。

- [ ] 测试先行：`user/identity_policy_test.go` 增加未登记类型、重复；`user/service_test` 增加 Providers 校验（nil/重复/PHONE）；`service_idp_test` 增加未登记类型两种 Enabled 情况、`ErrMalformedCredential` 不审计、未分类错误不泄露；`signin_completion` 测试：同一 Deps 换 Google client ID 新建 Service 后旧 token 被拒绝。
- [ ] 实现、`go test ./user/... ./httpapi/... .` 通过，提交 `feat(idp): dispatch sign-in through provider registry`。

### Task 6: 资料声明通用化（task 2.5）

- [ ] `providerProfileFrom` 去掉 `ident.Kind != enum.IdentityGoogle` 判断；`seedEmptyGoogleProfile` 改名 `seedEmptyProviderProfile`，错误文案去掉 google。
- [ ] 测试：fake Provider（kind `CORP_SSO`）返回 Name/Picture，首次注册填充两列；微信 fake 不返回则 `provider_meta` 无 profile。DB 测试需 DSN。

### Task 7: 未登记历史类型（task 2.7）

- [ ] DB 集成测试：以注册了 fake `ACME_SSO` 的 Service 建账号并绑定，再用未注册该类型的 Service（Required 为空、Enabled 未配置）：`ListIdentities` 返回 `ACME_SSO`；管理筛选 `identity.kind=ACME_SSO` 命中；解绑成功（账号仍有 EMAIL）；`SignInWithIdp` kind=ACME_SSO → `idp.ErrAppNotAllowed`；匿名化覆盖该行。

### Task 8: C 端统一凭证（tasks 3.1–3.3）

**Files:** `httpapi/enduser/request.go`、`signin.go`、`identities.go`、`me.go`、`replacement.go`、`signin_completion.go` 及测试、`docs/openapi/enduser.json`、README、`docs/compatibility.md`

**Interfaces — Produces:**
```go
type credential struct {
    Kind       string          `json:"kind"`
    Credential json.RawMessage `json:"credential"`
}
func (c credential) anchor() (enum.IdentityKind, codeCredential, *apierror.Error)
func (c credential) provider() (user.IdpCredential, *apierror.Error)
func (c credential) isAnchor() bool
```
- `completeSignInRequest` 改为内嵌 `credential` 字段加 `sign_in_completion_token`。
- 校验顺序：kind 空或 credential 缺失/`null` → `CREDENTIAL_INCOMPLETE`；kind 格式非法 → 400 `INVALID_ARGUMENT`；类别不符 → `CREDENTIAL_KIND_NOT_ALLOWED`；credential 首个非空白字符不是 `{` 或长度 > 16384 → `CREDENTIAL_INCOMPLETE`；锚点严格解码 `{target, code_id, code}`（未知字段 → `CREDENTIAL_INCOMPLETE`），后续沿用原 target/code/code_id 校验。
- [ ] 测试先行（`httpapi/enduser/credential_test.go`）：表驱动覆盖上述分支与六个端点的类别限制；更新既有测试请求体。
- [ ] 更新 OpenAPI（`Credential` schema：`kind` string pattern、`credential` object；删除 oneof 成员）与一致性测试；README 与兼容指南写迁移对照表。提交 `feat(httpapi): unify credential envelope`。

### Task 9: JWKS 缓存与 OIDC（tasks 4.1–4.4、4.6）

**Files:** Create `user/idp/jwks_cache.go`、`user/idp/oidc.go`、`user/idp/oidc_test.go`；Modify `apple.go`、`google.go`、`observability/classification.go`、`observability/internal/metricdata/project.go`、`user/observability.go`、`config.go`、`config_test`

**Interfaces — Produces:**
```go
type OIDCOptions struct {
    Kind          enum.IdentityKind
    Issuer        string
    JWKSURL       string
    ClientIDs     []string
    NameClaims    []string // nil → ["name"]
    PictureClaims []string // nil → ["picture"]
    HTTP          *http.Client
    Nonces        *NonceRegistry
    Logger        *slog.Logger
    Observer      observability.Observer
    Now           func() time.Time
    NonceTTL      time.Duration
}
func NewOIDC(o OIDCOptions) *OIDC
// accountkit
type OIDCProvider struct {
    Kind enum.IdentityKind; Issuer, JWKSURL string; ClientIDs []string
    NonceTTL time.Duration; NameClaims, PictureClaims []string
}
type Config struct { ...; OIDCProviders []OIDCProvider }
```
- `jwksCache{url, hc, logger, observer, scenario, now, keys, lastRefresh, lastRefreshOK, mu}`，`key(ctx, kid)` 行为与现 Apple/Google 相同。
- `idTokenVerifier{issuers []string, clientIDs map, cache *jwksCache, nonces, nonceScope, nonceTTL, now, kind, nameClaims, pictureClaims}`；Google = issuers 两值、scope `google`、claims `name`/`picture`；OIDC scope `oidc:<KIND>`。
- `ObservationMethod()`：内部接口，OIDC 返回 `ob.MethodOIDC`；`observationMethod` 先查提供方。
- `ConfigFromEnv`：`OIDC_PROVIDERS` 逗号分隔；`OIDC_<KIND>_ISSUER/_JWKS_URL/_CLIENT_IDS/_NONCE_TTL/_NAME_CLAIMS/_PICTURE_CLAIMS`；`_NAME_CLAIMS` 存在但为空 → 空切片。

- [ ] 测试先行：`oidc_test.go` 覆盖 iss 不等、aud/azp、HS256/none、exp+偏差、nonce 原文、重放、JWKS 不可达 503、缓存降级场景、email_verified、claims 回退/关闭/非字符串；配置测试覆盖缺项变量名、非内置名、重复、URL、默认值。Apple/Google 现有测试不改断言通过。
- [ ] DB 集成：OIDC 首次注册（Required=[]）、pending 补全、绑定、再次登录命中。提交 `feat(idp): add configurable OIDC providers`。

### Task 10: accountsvc 与部署文档（task 4.5）

- [ ] `internal/accountsvc/config.go`：显式 Enabled 中非内置类型须出现在 `Library.OIDCProviders`，否则 `invalidConfig("ACCOUNTKIT_ENABLED_IDENTITY_KINDS")`；`ConfigFromEnv` 的 OIDC 错误名映射为 `ACCOUNTKIT_OIDC_<KIND>_*`；migrate 路径不读取 OIDC。测试：缺 issuer、仅 OIDC 无 SMTP 可构造。
- [ ] 更新 `deploy/accountsvc/.env.example`、`docs/accountsvc.md`、README 配置表。提交 `feat(accountsvc): configure OIDC providers`。

### Task 11: 集成验证（tasks 5.1、5.2）

- [ ] 一次性 PostgreSQL 17 容器（独立端口与名称）跑 `go test -count=1 ./...`；同步 `scripts/required-tests.txt`。
- [ ] `bash scripts/verify.sh`（race 用 `tests/Dockerfile` 镜像）；`openspec validate --all --strict`；写 `verification.md`。
