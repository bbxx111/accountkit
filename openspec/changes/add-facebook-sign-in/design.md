# Design

## Context

见 proposal.md 的 Why。与本设计相关的现状：

- `idp.Provider` 接收 credential 的原始 JSON 并自行严格解码。Google 与配置 OIDC 共用 `idTokenVerifier`（RS256、JWKS 缓存、issuer 列表、aud/azp、原文 nonce 常量时间比较与登记）。微信是唯一由服务端调用外部 API 换取身份的提供方，有成熟的不跟随重定向、错误脱敏与 `ErrMisconfigured` 分类写法。
- 第三方身份的 subject 以明文存放在 `identity.provider_subject`，活动行由 `uk_identity_kind_provider_subject` 保证唯一。普通解绑 `SoftDeleteIdentity` 只设置 `delete_time`；`AnonymizeIdentity` 用随机值替换 subject 并清空 `provider_meta` 与 hint，目前只在账号到期匿名化时调用。
- 账号注销 `softDelete` 要求账号为 ACTIVE。它在同一事务内调用宿主 `BeforeDelete`、清理登录补全流程，并吊销全部会话。冷静期内的登录一律被拒绝。
- 普通解绑 `UnbindIdentity` 要求本人会话、近期认证，且解绑前后账号都满足完整性。以被解绑身份为认证来源的会话通过 `RevokeSessionsByIdentity` 吊销。
- 审计默认有界异步写入，失败不改变认证动作结果。

已核实的 Facebook 事实（2026-10-23）：

- Limited Login 的 discovery（`https://limited.facebook.com/.well-known/openid-configuration/`）给出 issuer `https://www.facebook.com`，签名算法只有 RS256，声明包含 `sub`、`nonce`、`name`、`picture`、`email`。
- `https://www.facebook.com/.well-known/oauth/openid/jwks/` 与 `https://limited.facebook.com/.well-known/oauth/openid/jwks/` 返回相同的密钥集。
- `debug_token` 返回 `app_id`、`type`、`is_valid`、`user_id`、`expires_at`、`scopes`。

## Goals / Non-Goals

**Goals:**

- 用一个内置提供方同时承接 Limited 与 Classic 两种凭证，两者落到同一身份行。
- 最大化复用现有 ID token 校验核心与微信的外部调用写法，不新增依赖。
- 数据删除的分支判定、强制解绑和注销兜底在同一事务内完成，与普通解绑、注销并发时不产生不一致。
- 宿主只需一次 Go 调用即可完成回调处理，签名校验和领域规则由库保证。

**Non-Goals:**

- 不提供数据删除的 HTTP 路由与状态页；不持久化删除请求的状态。
- 不实现 Deauthorize 回调；不处理 Facebook 的 `token_for_business` 与跨 App 账号对应。
- 不给普通解绑或注销增加新语义；不修改数据库结构。
- 不为数据删除接入 Google、Apple 等其他提供方的回调；领域方法按类型参数化，但本变更只接 Facebook。

## Decisions

### 一个提供方，按 credential 字段分派

新增 `idp.Facebook`，`Kind()` 为 `FACEBOOK`。`Verify` 先严格解码到同时声明 `id_token`、`nonce`、`access_token` 三个可选字段的结构，拒绝未知字段，然后按以下形状分派：

- 只有 `id_token` 与 `nonce`，且二者非空：走 Limited 路径。
- 只有 `access_token`，且非空：走 Classic 路径。
- 其他组合：`ErrMalformedCredential`。

备选方案是注册两个 Kind（如 `FACEBOOK` 与 `FACEBOOK_LIMITED`）。被否决，因为同一用户会因设备上的跟踪设置不同而被拆成两个身份。

### Limited 路径直接复用 idTokenVerifier

`idTokenVerifier` 的参数：issuer 列表 `["https://www.facebook.com"]`；clientIDs 只有配置的 App ID；nonce 作用域 `facebook`；name 声明 `name`；picture 声明 `picture`；email 只作为 hint。默认 JWKS 取 www 域名，原因是它与 issuer 和 discovery 一致；`FacebookJWKSURL` 可覆盖。降级场景使用独立的 `facebook_cached_key_fallback`。

### Classic 路径：两次 Graph 调用

1. `GET {base}/{version}/debug_token?input_token=<token>`，以 App access token `{app_id}|{app_secret}` 认证，并附带对该 App token 计算的 `appsecret_proof`。要求 `is_valid` 为真、`type` 为 `USER`、`app_id` 等于配置值，`user_id` 非空。
2. `GET {base}/{version}/me?fields=id,name,email,picture`，以用户 token 认证，并附带 `appsecret_proof = hex(HMAC-SHA256(app_secret, user_token))`。要求 `id` 等于第一步的 `user_id`。

错误分类：

| 情形 | 哨兵 |
|---|---|
| token 无效或过期（`is_valid=false`）、`type` 不是 USER、两步 id 不一致、`/me` 返回 OAuth 190 类错误 | `ErrInvalidCredential` |
| `app_id` 不符 | `ErrAppNotAllowed` |
| 网络错误、超时、5xx、响应无法解析 | `ErrUnavailable` |
| App token 被拒（App ID 或 Secret 配置错误） | `ErrMisconfigured`，并记录不含秘密的告警日志 |

HTTP 客户端沿用微信的写法：不跟随重定向；传输错误先去掉 URL 再包装，因为 URL 的 query 里有 token 和 proof；响应体有大小上限。`Identity.ExpireTime` 取 `expires_at`，值为 0 时保持零值。`/me` 的 `picture` 是 `{data:{url,is_silhouette}}` 对象：`is_silhouette` 为真时视为没有头像，否则取 `url`，再交给通用头像地址校验。

两条路径都有 email 时写入 `HintEmail`。Facebook 不提供与 Google `email_verified` 对等的声明。由于该值只用于预填、不用于合并账号，这里不另加验证判断，并在文档中写明。

### 不对 Classic token 做一次性限制

讨论中确认采用方案 A。原因：移动端 SDK 会缓存长效 token，一次性限制会让"退出后再次登录"失败；而攻击者只要抢先使用一次，就能绕过一次性限制，它对偷取 token 的防护有限。T1（替换为其他 App 的 token）由 `app_id` 校验防住。T2（偷取我方 token）的风险等同于 refresh token 被偷，在文档中如实说明，并建议在 Meta 后台开启 "Require App Secret"。

### 配置

库 `Config` 增加以下字段：

- `FacebookAppID`、`FacebookAppSecret`
- `FacebookGraphAPIVersion`：格式 `vN.N`，默认值在实现时按 Meta 当前的版本列表固定
- `FacebookNonceTTL`：默认 10m，下限 1m
- `FacebookGraphAPIBaseURL`：默认 `https://graph.facebook.com`
- `FacebookJWKSURL`

规则：

- App ID 为空表示未启用，此时其余字段不做要求。
- App ID 非空时必须提供 Secret。即使客户端只用 Limited Login，数据删除回调的签名校验也需要 Secret。
- 环境变量为 `<prefix>FACEBOOK_*`。
- `PolicyFingerprint` 包含 App ID、版本、Graph 地址与 JWKS 地址，不包含 Secret。

### 数据删除：库内入口分两层

- `idp.(*Facebook).ParseSignedRequest(raw string) (DeletionRequest, error)`：按 `.` 分成两段，用 base64url（无填充）解码；要求 `algorithm` 为 `HMAC-SHA256`；以 App Secret 计算签名并做常量时间比较；返回 `user_id` 与 `issued_at`。格式或签名错误返回新哨兵 `ErrInvalidSignedRequest`。
- `user.Service.DeleteProviderIdentityData(ctx, kind, subject, requestTime, meta)`：按类型参数化的领域方法，执行下文的分支规则。
- 门面 `Auth.HandleFacebookDataDeletion(ctx, signedRequest, meta)` 把两者串起来，宿主的回调 handler 只需调用它，再把结果编码成 Meta 要求的 `{url, confirmation_code}`。

### 分支规则在同一事务内判定

```
按 (kind, subject) 查找活动身份
  没有 -> DELETED（幂等）
  有   -> 锁账号行，在锁内重新读取该身份（仍为活动、属于同一账号）
          身份的 create_time 晚于请求的 issued_at -> DELETED（该身份是请求之后重新绑定的，不处理）
          账号 PENDING_DELETION -> SCHEDULED（不改动，返回 purge_time）
          去掉它之后仍有有效 Enabled 身份 -> S2：
              AnonymizeIdentity（随机替换 subject，清空 meta 与 hint，设置 delete_time）
              RevokeSessionsByIdentity（原因 IDENTITY_UNBOUND）
              -> DELETED
          否则：
              账号 FROZEN -> BLOCKED（不改动）
              账号 ACTIVE -> 在同一事务内执行注销主体（含 BeforeDelete）
                  BeforeDelete 返回 ErrDeletionBlocked -> 回滚 -> BLOCKED
                  成功 -> SCHEDULED
```

- 有效 Enabled 身份按 identity-policy 的定义：已验证、未软删除、Kind 属于 Enabled。S2 不检查 Required；剩下的身份不满足 Required 时，账号在下次签发时得到 `user:bind`，进入现有的补全流程。
- `softDelete` 的事务体抽成可在已有事务内调用的内部函数，`DeleteMe`、`AdminDeleteUser` 与本路径共用，行为不变。如果在事务外先判定、再调用原 `softDelete`，判定与注销之间会有竞态，因此不采用。
- S2 用 `AnonymizeIdentity` 而不是软删除。软删除会把 ASID 以明文永久留在库里，与"删除 Facebook 来源数据"不符。代价是管理面的历史身份看不到原 ASID。
- `BeforeDelete` 返回 `ErrDeletionBlocked` 以外的错误时，方法返回错误，宿主应返回 5xx 让 Meta 重试；不把它映射为 BLOCKED。
- 比较 `create_time` 与 `issued_at`，是为了防止旧请求被重放后误删用户后来重新绑定的身份。成本只是一次时间比较。

### 确认码与审计

- 每次调用生成一个 UUIDv7，作为确认码，同时也是新审计事件 `PROVIDER_DATA_DELETION_PROCESSED`（追加为 27）的事件 ID。
- 该事件：操作者为服务；结果为成功，返回错误时为失败；扩展数据只记录 `kind`、`status` 和 subject hint，不记录完整 ASID。
- S2 与 S1 同时照常产生 `IDENTITY_UNBOUND`、`SESSION_REVOKED`、`USER_DELETED` 等既有事件，操作者同为服务。
- 审计是尽力写入，确认码不保证能在审计里查到。需要可靠状态页的宿主应自行保存 `{确认码, 状态, purge_time}`。

### 指标

- 认证方法追加 `facebook`。
- 降级场景追加 `facebook_cached_key_fallback`。
- Graph 两次调用作为 IdP 验证器的一次逻辑调用记录，不冒充网络请求计数。
- 数据删除不新增指标，只走审计。

## Risks / Trade-offs

- [Classic token 被偷后，在有效期内可用于登录] → 校验 `app_id` 防住替换；文档说明风险并建议开启 Require App Secret；会话层的吊销与刷新轮换不变。
- [Meta 调整 Graph API 版本或下线旧版本] → 版本可配置，默认值随发布升级，并写进发布说明；版本失效时返回 `ErrMisconfigured` 告警，而不是 `ErrInvalidCredential`。
- [JWKS 域名将来分化] → 地址可配置；未知 kid 沿用节流刷新，且有缓存降级的观测。
- [确认码依赖尽力审计] → 领域方法同步返回状态，宿主自行持久化；文档写明。
- [S2 把账号带入不满足 Required 的状态] → 这是讨论中确认的取舍，由现有补全流程收敛，不产生新状态。
- [BLOCKED 请求需要人工跟进] → 状态如实返回给宿主，审计可检索；库不重试，也不替宿主决策。
- [匿名化身份后，历史管理查询看不到原 ASID] → 有意的隐私取舍，审计保留 hint。

## Migration Plan

无数据库迁移。宿主升级后，配置 `FacebookAppID` 与 `FacebookAppSecret` 即启用。未配置时行为与升级前完全一致。显式 `EnabledIdentityKinds` 的宿主需要手动加入 `FACEBOOK`。回退到旧版本后，已创建的 FACEBOOK 身份按"未登记的历史类型"规则仍可被列出、解绑和匿名化，但不能用于登录。

## Open Questions

- `FacebookGraphAPIVersion` 的默认值在实现时按 Meta 当时的版本列表确定，不影响 spec 与任务划分。
