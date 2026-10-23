# Design

## Context

见 proposal.md 的 Why。当前与本设计直接相关的实现事实：

- `enum.IdentityKind` 是 int16，`Valid()` 用 `PHONE..GOOGLE` 的序数区间判断；`identity.kind` 为 `SMALLINT`，参与 `uk_identity_kind_subject_digest`、`uk_identity_kind_provider_subject`、`idx_identity_kind_hint_suffix`、`idx_identity_kind_hint_prefix` 四个索引，以及 `LockAnchorSubjects` 的 `@kinds::smallint[]` 锁键和管理筛选 `ListUsersAdmin` 的 `::smallint` 参数。
- 第三方分派散落在 `user/service_idp.go`（switch）、`user/service_policy.go`（未配置时的兼容错误）、`user/service.go`（默认启用和能力校验）、`user/identity_policy.go`（`IdentityCapabilities` 五个布尔值）、`user/observability.go`（方法标签）、`httpapi/enduser/request.go`（五成员 oneof）和 `accountkit.go`（装配）。
- 登录补全配置指纹 `signInCompletionFlowConfiguration` 只纳入微信和 Apple 的 `PolicyFingerprint`，Google 漏掉了。
- 资料声明与首次填充以 `ident.Kind == GOOGLE` 为条件；微信与 Apple 的 `Identity` 不带 name/picture。
- 补全载荷 `completionPayload` 加密保存 `completionProof.Kind`，JSON 中现为数字。
- 审计 `identity_kind` 已以名字字符串存储。

## Goals / Non-Goals

**Goals:**

- 身份类型以字符串贯穿 Go、数据库、HTTP 与审计，不再依赖序数。
- 一处注册、一处分派：新增 OIDC 类型不需要改 switch、DTO 或 0001。
- 内置三种提供方的校验规则、错误码、nonce 键空间、指标与审计行为保持不变（请求格式除外）。

**Non-Goals:**

- 不在 `accountkit.Deps` 开放宿主自写 Provider，不提供能力声明和 `provider_meta` 扩展区（第二期）。
- 不支持 OIDC discovery、授权码流程、RS256 以外的算法、userinfo 端点或保存提供方 token。
- 不改变 PHONE/EMAIL 的锚点、验证码渠道、Required 规则。
- 不改其他枚举（UserState、RevokeReason 等）的存储类型。

## Decisions

### IdentityKind 改为字符串，其余枚举保持 SMALLINT

`type IdentityKind string`，内置常量值即 API 名，零值 `""` 为未指定。`Valid()` 改为格式校验 `^[A-Z][A-Z0-9_]{1,31}$`，新增 `IsBuiltin()`；`IsAnchor`/`IsChannel` 用显式集合判断。`ParseIdentityKind` 只校验格式，类型是否存在由注册表与策略决定。

只改 IdentityKind，是因为它是唯一会由部署方扩展的集合；其他枚举仍是包内封闭集合，改为字符串只有可读性收益。AGENTS.md 的“枚举存 SMALLINT”规则相应写明这一例外。

备选：全部枚举改字符串。改动面大且收益只在可读性，不采用。

### 数据库：VARCHAR(32) COLLATE "C" 加格式 CHECK

`identity.kind VARCHAR(32) COLLATE "C" NOT NULL CONSTRAINT identity_kind_format CHECK (kind ~ '^[A-Z][A-Z0-9_]{1,31}$')`。格式 CHECK 属于结构约束，不枚举合法值，符合“不新增枚举值 CHECK”的规则；它防止手工修数或旧代码写入数字或小写值。Go 端格式校验为准，CHECK 是兜底。

选 `VARCHAR(32)` 而不是 `TEXT`：PostgreSQL 中二者性能相同，但长度上限在列定义上可见；更重要的是 MySQL 的 `TEXT` 不能直接作为普通/唯一索引列，而 kind 是四个索引的首列。二进制排序（PostgreSQL `COLLATE "C"`，MySQL 对应 `ascii_bin`）保证 `wechat` 与 `WECHAT` 不被唯一索引视为相同。多数据库不是本次目标，只在零成本时选可移植写法；部分索引、事务级 advisory lock、JSONB、`RETURNING` 等 PostgreSQL 专有依赖不在本次调整范围。四个索引定义不变（列类型随之变化），`LockAnchorSubjects` 改为 `@kinds::text[]`，管理筛选改为 `::text`。sqlc 的 `identity.kind` override 继续指向 `enum.IdentityKind`。

运行时预检增加对 `identity.kind` 为 `character varying(32)` 且排序规则为 C 的检查：同为 version 1 但 `kind` 仍是 smallint 的旧库（v0.5.0 及当前 develop 安装）无写拒绝，dirty 优先，与头像列的处理方式一致。

### 注册表与 Provider 接口

```go
type Provider interface {
    Kind() enum.IdentityKind
    Verify(ctx context.Context, credential json.RawMessage) (Identity, error)
    PolicyFingerprint() string
}
```

`user.Deps.Providers []idp.Provider` 取代 `WeChat`/`Apple`/`Google` 三个字段，`NewService` 时构建 `map[IdentityKind]Provider` 不可变快照，校验：类型格式合法、不是 PHONE/EMAIL、不重复、非 nil。内置三种的类型名由其构造函数固定；OIDC 类型名来自配置且不得等于任何内置名（含 PHONE/EMAIL/WECHAT/APPLE/GOOGLE）。

分派统一为：格式校验 → 策略 Enabled 检查 → 注册表查找 → `Verify` → `Identity.Kind` 与请求一致且 `Subject` 非空。未注册的类型：Enabled 未显式配置时返回 `IDP_APP_NOT_ALLOWED`（保持现有“未配置提供方”契约），显式 Enabled 时返回 `IDENTITY_KIND_NOT_ENABLED`（启动校验保证显式 Enabled 中的第三方都已注册）。

`ProviderConfiguration` 字段保留，继续并入补全指纹；补全指纹改为 `policy.Fingerprint()` 加按类型排序的全部 `PolicyFingerprint()`，修复 Google 未计入的问题。

备选：保留三个专用接口并另加通用接口。会留下两条分派路径，不采用。

### 统一凭证信封，第三方凭证由提供方解析

所有接收凭证的 C 端请求体统一为顶层 `{"kind","credential"}`（登录补全另带 `sign_in_completion_token`）。去掉 `provider` 外层：`kind` 已经是判别字段，外层只会让绑定接口出现两种形状。`credential` 保持独立一层而不与 `kind` 平铺，使提供方拿到的是干净对象，可严格拒绝未知字段，也不与信封字段重名。备选“以类型名作字段名”（`{"corp_sso":{...}}`）使字段集随配置变化，OpenAPI 无法描述，不采用。

HTTP 层确认 `kind` 格式合法、属于该端点允许的类别（PHONE/EMAIL 或第三方），`credential` 是 JSON 对象且不超过 16 KiB。PHONE/EMAIL 的 `credential` 由 HTTP 层严格解码为 `{target, code_id, code}`，沿用原校验与错误码；第三方的 `credential` 原样传给领域层。各提供方严格解码自己的字段（拒绝未知字段），必填缺失或格式错误返回新的 `idp.ErrMalformedCredential`，映射为 400 `CREDENTIAL_INCOMPLETE`，且在任何网络调用和 nonce 登记之前返回、不写失败审计（与原 HTTP 层校验一致）。

内置凭证字段：WECHAT `{app_id, code}`；APPLE、GOOGLE 与 OIDC `{id_token, nonce}`。

### 共享 JWKS 缓存，OIDC 与 Google 共用验证核心

把 Apple/Google 中重复的 JWKS 懒加载、60 秒节流刷新、缓存密钥降级抽为内部 `jwksCache`（带降级场景参数）。Google 与通用 OIDC 共用一个 ID token 校验核心（issuer 集合、aud/azp、exp、30 秒偏差、原文 nonce 常量时间比较、nonce 登记期限 = max(下限, exp+30s−now)）；Google 是 issuer 为两个固定值、默认 JWKS、降级场景为 Google 的预设。Apple 的 nonce 是 SHA-256 摘要比较且只有一个 issuer，保留独立实现，只复用 `jwksCache`。

nonce 键：Apple `nonce:apple:`、Google `nonce:google:` 不变；OIDC 为 `nonce:oidc:<KIND>:`，均带实例前缀。

### 资料声明不再按类型判断

`providerProfileFrom` 去掉 GOOGLE 条件：任何提供方返回非空 name 或合法 picture 都记入 `provider_meta.profile` 并参与首次填充空资料。内置微信、Apple 不返回这两项，行为不变；OIDC 从 `name`、`picture` 声明读取。`seedEmptyGoogleProfile` 相应改名。

### 资料声明映射只在 OIDC 配置中

`Provider` 接口返回已映射的 `Identity.Name`/`Picture`，不承载声明名配置；宿主自写的提供方在代码中自行取值。OIDC 配置新增 `NameClaims`、`PictureClaims`（nil 默认 `["name"]`、`["picture"]`，显式空切片关闭），按顺序取第一个非空的顶层字符串声明，不支持嵌套路径，避免演变为通用取值表达式。内置 Google 固定 `name`/`picture`。邮箱提示固定读取 `email` 与 `email_verified`，因其承载“已验证”语义，不开放映射。

### 配置

`Config.OIDCProviders []OIDCProvider{Kind, Issuer, JWKSURL, ClientIDs, NonceTTL, NameClaims, PictureClaims}`，Issuer 与 JWKSURL 必填且为绝对 http(s) URL，ClientIDs 非空，NonceTTL 默认 10 分钟。`ConfigFromEnv(prefix)`：`<prefix>OIDC_PROVIDERS` 为逗号分隔类型名，每个类型读取 `<prefix>OIDC_<KIND>_ISSUER`、`_JWKS_URL`、`_CLIENT_IDS`、`_NONCE_TTL`、`_NAME_CLAIMS`、`_PICTURE_CLAIMS`（变量未设置取默认，设置为空串表示关闭）。`New` 不发起网络请求，JWKS 仍在首次验证时加载。

### 观测

`observationMethod` 对 OIDC 类型返回新增固定值 `oidc`，降级新增 `oidc_cached_key_fallback`；类型名不进入指标标签，避免基数随配置增长。审计照常记录具体类型名。

## Risks / Trade-offs

- [宿主配置的 OIDC 类型名与未来内置类型撞名] → 内置名单写进文档；将来新增内置类型时作为兼容性变更说明。
- [同一 OIDC 提供方以两个类型名配置] → 会产生两套互不相通的身份；文档要求类型名一经使用不得更换。
- [下线 OIDC 类型后历史身份残留] → 仍可列出、解绑、匿名化；完整性计算只算 Enabled 内的身份，与现有“禁用类型”规则一致。
- [滚动发布混跑新旧版本] → 新旧 schema 不兼容（列类型不同），预检拒绝旧库；部署必须使用新空资源，不存在混跑。
- [升级时进行中的补全流程] → 指纹变化使其被拒绝，客户端重新开始；补全流程寿命短，可接受。
- [宿主 Provider 返回的错误文本含敏感内容] → 非 idp 哨兵错误一律 500 固定消息，原始错误只进日志。

## Migration Plan

按用户“直接改 0001”的明确授权：仅修改当前 `0001_init.up.sql` 中 `identity.kind` 列定义（`VARCHAR(32)`、COLLATE、格式 CHECK）及相关注释，索引定义文本不变，`0001` down 整表语义不变；同步 sqlc、运行时预检与当前 `initialFrozen`；不新增 0002。已发布 tag/SQL、v0.5.0 与 v0.4.0 的已发布定义及冻结源保持。新安装为 version=1/dirty=false；旧同号库不重放、不转换，预检无写拒绝，不 Force、改版本或清库。AGENTS.md 增加一条限定本次范围的授权记录，并在枚举规则中写明 IdentityKind 例外。

回退：本版本写入的库不能被旧二进制使用（列类型不同）；回退须恢复到对应旧库或新空资源。
