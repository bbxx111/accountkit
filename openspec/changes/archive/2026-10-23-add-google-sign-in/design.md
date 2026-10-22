# Design

## Context

微信用授权码换 UnionID，Apple 校验 ID token。Google 登录的凭证形态接近 Apple：客户端交来 ID token，服务端用 JWKS 验签。现有 `identity.provider_meta` 只定义了微信 `openids`；`idp.Identity` 已能随登录补全证明加密暂存。`display_name` 是不限长的 `TEXT`，32 个码位的限制只在 Go 校验中。见 proposal.md 的 Why。

## Goals / Non-Goals

**Goals:**

- 用与 Apple 并列的校验器验证 Google ID token，并把 `sub` 写成 Google 身份。
- 姓名和头像进入 `provider_meta.profile`；仅在该身份行第一次写入时填充仍为空的账号资料。
- pending 的 `hint_email` 从已验证的初始证明读出，Apple 与 Google 共用这条响应路径。
- 显示名上限改为 128 个 Unicode 码位，本人更新与 Google 填充使用同一套字符规则。

**Non-Goals:**

- 不抽出通用 OIDC 框架，不接收授权码，不保存 Google access/refresh token。
- 不按邮箱合并账号，不限制 Workspace 域名，不下载头像。
- 不通过身份列表或 `:reveal` 返回 `provider_meta`。
- 不新增 `0002`，也不为 Google 增加列、索引或 CHECK。

## Decisions

### 独立校验器，复用 JWKS 与 nonce 登记

新增 `idp.Google`，形状对齐 `idp.Apple`：懒加载 JWKS、未知 kid 的 60 秒节流刷新、缓存密钥继续验证。RSA 解析继续用现有 `parseRSAJWKSet`。nonce 继续用 `NonceRegistry`。

不把 Apple 和 Google 收成一个参数化 OIDC 客户端。两者的签发者数量、nonce 比较和声明字段不同；共用的只有 JWKS 缓存和 nonce 登记。

备选是授权码换票。那需要 client secret，并产生本库明确丢弃的提供者 token，因此不采用。

### nonce 比较原文

Google 的 `nonce` 声明是客户端传入的原文字符串。校验使用常量时间比较，不计算 SHA-256。登记期限取配置下限与 `exp + 30s` 的较大者，覆盖 token 剩余寿命。

照搬 Apple 的摘要比较会使合法 token 全部失败。

### 两个签发者在验签后显式判断

解析器的单一 `iss` 约束不能同时表达 `https://accounts.google.com` 和 `accounts.google.com`。验签、算法、`exp` 和 30 秒偏差仍交给解析器；通过后把 `iss` 与两个字面值比较。`aud` 必须命中 client ID 允许列表；token 含 `azp` 时，`azp` 也必须命中。

### 声明跟着现有身份证明走

`idp.Identity` 增加 name 和 picture。补全证明已经加密保存整个 `Identity`，因此 pending 期间不必另建列或表。身份行创建时写入：

```json
{"profile":{"name":"...","picture":"https://..."}}
```

`providerMeta` 同时容纳 `openids` 和 `profile`。更新其中一边时重新序列化整个对象，避免微信 openid 或 Google 声明互相覆盖。name 先去掉显示名禁止的控制字符，最多保留 256 个码位；picture 必须通过现有头像 URL 校验。之后登录只覆盖本次非空字段。

### 资料只在身份行首次插入时填充

在持有账号行锁的同一事务内，身份行第一次插入后读取 `display_name` 和 `avatar_url`。前者为 NULL 时写入整理后 name 的前 128 个码位；后者为 NULL 且 picture 合法时写入该 URL。直接注册、补全创建、补全关联和绑定都走这一次。已存在的 Google 身份再次登录不修改这两列。用户同时 `PATCH /users/me` 时，以锁内读到的值为准。

本人 `PATCH` 超过 128 个码位或含禁止字符时整次拒绝。Google 填充对超长名字截断，对非法头像跳过，两者都不失败登录。

### 配置与观测沿用 Apple 的装配方式

`Config` 增加 `GoogleClientIDs`、`GoogleNonceTTL` 和 `GoogleJWKSURL`。默认 JWKS 为 `https://www.googleapis.com/oauth2/v3/certs`，默认 nonce 下限 10 分钟。环境变量为 `GOOGLE_CLIENT_IDS`、`GOOGLE_NONCE_TTL`、`GOOGLE_JWKS_URL`。空 client ID 列表不装配校验器。枚举在末尾追加 `IdentityGoogle = 5`，`Valid` 的上界跟着扩展。

指标增加 `verify_google` 和独立降级场景 `google_cached_key_fallback`。场景值来自固定常量，不使用 token 内容做标签。

### pending 提示来自证明而不是账号查询

`PendingSignIn` 增加 `HintEmail`，取值是初始 `Identity.HintEmail`。响应省略空值。发码和完成仍要求客户端提交 target。日志、审计和 URL 不记录该邮箱。

## Risks / Trade-offs

- [把 Apple 的 nonce 摘要比较复制到 Google] → 单测使用原始 nonce 的正例和摘要比较的反例。
- [Google 以后改用非 RSA 签名] → 当前只接受 RS256 和 RSA JWKS；没有可用密钥时返回 503，不放行。
- [同一人在不同 Cloud 项目中的 sub 不同] → 允许列表只应配置同一项目的 client ID；身份主键不做跨项目合并。
- [服务访问不了 Google JWKS] → 无缓存时登录失败为 503，不降级成本地信任。
- [回退到不认识 `GOOGLE` 的旧二进制] → 已写入的 kind 5 身份不能被旧版本解释。回退前停止新的 Google 登录；已有 Google 身份的库需要保留本版本或另行制定数据方案。
- [128 个字符的显示名影响所有本人更新] → 这是公开契约放宽。文档写明旧的 32 个字符上限不再适用，已有短名字保持有效。

## Migration Plan

用户要求数据库变更直接修改当前 `0001`。本变更只更新其中两处注释：`identity.kind` 写明包含 `GOOGLE`，`provider_meta` 写明 Google 的 `profile.name` 与 `profile.picture`。不新增列、索引、约束或生产 `0002`。`0001` down 的整表删除语义不变。

注释变更会改变当前初始 SQL 字节，因此只更新当前 `initialFrozen`。已发布 tag、其中的 SQL 和 `tests/testdata/source-baseline/` 保持原字节。新空 schema 安装仍为 version=1/dirty=false。已经安装的同号库不会重放 `0001`，注释也不会自动更新；不 Force、不改版本、不清库。

`provider_meta` 的 `profile` 对象由应用写入现有 JSONB。旧行没有该对象时保持原样。部署时未配置 `GOOGLE_CLIENT_IDS` 的实例行为与现在相同。启用时填写同一 Google Cloud 项目的 client ID，并保证实例能访问 JWKS 端点。回退配置即停止接受新的 Google 凭证；不删除已有身份行。二进制回退见上一节风险。

README、迁移说明、兼容说明和 accountsvc 配置模板同步这条限定授权、client ID、JWKS 地址、pending `hint_email` 和 128 个字符的显示名上限。该授权只覆盖上述注释，不构成以后改写初始 SQL 的许可。
