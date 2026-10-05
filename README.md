# accountkit

可嵌入宿主服务的 C 端账号体系（多身份账号、JWT + 轮换 refresh 会话、软删除与匿名化、管理面）。
本 README 覆盖配置、密码学原语、迁移、生命周期、C 端 HTTP 面（登录、会话、IdP、身份绑定）、账号注销/恢复/purge、审计落库与密钥回填、管理面。模块路径为 `github.com/bbxx111/accountkit`，根包名为 `accountkit`。宿主装配示例见 `examples/embedded`。

accountkit 支持直接作为库嵌入宿主。accountsvc 是项目自带的可选服务实现，使用同一套库能力；选择库集成不需要部署或调用 accountsvc。服务入口是 `cmd/accountsvc`，包含 SMTP 邮件投递、消费者远程鉴权和默认关闭的外部管理员 JWT 验证，运行与配置见 [accountsvc 服务手册](docs/accountsvc.md)。`examples/embedded` 保留为库集成开发示例。

accountsvc 使用一个监听地址承载消费者、管理员、`/v1/introspect` 和探针；公网暴露、直连限制、TLS 终止与入口流量控制由部署方配置，统一规则见 [网关接入手册](docs/gateway-integration.md)。直接嵌入库的宿主也可沿用该契约。旧双监听部署与调用方需按[服务传输迁移说明](docs/compatibility.md#服务单监听与传输迁移)调整。

## 生命周期

完整可编译宿主示例见 [examples/embedded/main.go](examples/embedded/main.go)，开发与验证步骤见 [docs/development.md](docs/development.md)。导入使用 `"github.com/bbxx111/accountkit"`，通过 `accountkit.Config`、`accountkit.Deps` 和 `accountkit.New` 装配。旧包名调用方见[包名迁移说明](docs/compatibility.md#包名迁移)。

按 `Config/Deps → New → Migrate → Start → Close` 装配；库不绑定端口，宿主挂载 EndUserHandler 到 /v1，AdminHandler 到 /admin/v1。管理员验证器及主体解析器可选；未配置时管理接口返回 503 ADMIN_NOT_CONFIGURED。生产宿主须在管理路由前完成身份验证，并提供 AdminVerifier/AdminPrincipal，服务商发送器也由宿主注入。终端用户 HTTP 包与门面的源码迁移见[终端用户入口迁移说明](docs/compatibility.md#终端用户-http-入口迁移)。
## 领域层（阶段 3a）

`auth.Users()` 返回 `*user.Service`，是所有业务规则的唯一所在；HTTP handler（阶段 3b）、维护任务与管理面都只调用它。

| 能力 | 方法 | 备注 |
|---|---|---|
| 发登录码 | `SendSignInCode(ctx, channel, target, meta)` | 先占额再发送，发送失败不退额；被限流返回 `*code.RateLimitedError` |
| 验证码登录即注册 | `SignInWithCode(ctx, channel, target, code, dev, meta)` | 同一事务创建账号 + 身份；同设备再登录先吊销旧会话 |
| 刷新 | `Refresh(ctx, refreshToken, meta)` | 行锁 + CAS 轮换；30s 宽限内重放返回同一 pair；宽限外重放吊销会话 |
| 登出 | `Revoke(ctx, refreshToken, meta)` | RFC 7009 语义：未知 token 也返回成功 |
| 校验 access | `Authenticate(ctx, raw)` | 验签 + id 格式 + Redis 吊销集（Redis 不可用时 fail-open） |
| 重新认证 | `SendReauthenticationCode` / `Reauthenticate` | 只接受本账号锚点身份；刷新 `auth_time` 并重签 access（不发 refresh） |
| 资料 | `GetMe` / `UpdateDisplayName` | 显示名 ≤ 32 个字符 |
| 会话 | `ListSessions` / `RevokeSession` / `RevokeOtherSessions` | 他人的会话一律 `ErrNotFound` |

### 会话过期语义

消费者/管理员会话列表及管理详情的 `active_session_count` 只包含未吊销且 `refresh_expire_time` 严格晚于当前判定时刻的会话，等于期限即过期。判定统一使用应用时钟的UTC微秒精度，刷新期限不额外叠加JWT leeway。

刷新与重新认证在取得行锁后重新判定期限；等待锁期间到期也不能续用。到期refresh返回400 OAuth `invalid_grant`，到期重新认证返回401 `TOKEN_INVALID`，客户端需要重新登录。有效重新认证仍只返回access，不延长refresh期限；关闭敏感操作新鲜度检查也不能绕过会话到期。REAUTH验证码可能已消费，失败不恢复该码。

宽限资格取会话读取后的时间；缓存返回后再检查会话/pair期限并计算剩余有效期。已符合宽限的请求不会仅因缓存等待跨过宽限而被认定为重放，仍返回同一pair。缓存不可用或到期拒绝不会被误记为已遏制的重放。

刷新期限与access有效性独立：自然到期不写撤销集，原access和内省仍按JWT及Redis吊销规则判断；“不在会话列表中”不等于“所有已发access都已失效”。显式单个/批量撤销、同设备重登和账号生命周期操作仍覆盖未清理的过期会话。列表读取不删除数据，原30天会话清理保留期不变。实际验证见[会话过期验收记录](openspec/changes/archive/2026-10-04-harden-session-expiry/verification.md)。

### Redis 键与失败模式

键前缀为 `Config.KeyPrefix`（默认 `auth:`）：`code:*`、`cooldown:*`、`quota:*`（验证码与额度，Lua 原子）、`grace:*`（AES-GCM 加密的轮换后 pair，TTL 30s）、`revoked:*`（吊销集，TTL = access TTL + 验签 leeway，覆盖 `Signer.Parse` 对 exp 的容忍窗口，避免吊销条目先于该窗口内仍会验签通过的旧 token 过期）。

| 场景 | Redis 不可用时 |
|---|---|
| 发码、验证码登录/重新认证 | fail-closed：`user.ErrUnavailable` |
| 宽限期查询 | 退化为拒绝（`ErrInvalidGrant`），不吊销会话 |
| 吊销检查 | fail-open：按签名与过期校验，记 Warn 日志 |
| 重放检测 / 冻结 / 待删除时的吊销 | 数据库写失败向上返回错误（不记"已遏制"审计），吊销集仍尽力写入 |

### 发送器

`Deps.SMSSender` / `Deps.EmailSender` 必填。开发环境用 `sender.NewLog(logger)`（把验证码打进日志，**禁止用于生产**）；服务商实现由宿主通过接口注入。

### 枚举

所有 SMALLINT 列的取值由 `enum` 包定义并钉死（`UserState`：ACTIVE=1 FROZEN=2 PENDING_DELETION=3 DELETED=4，其余见 `enum/enum.go`）。DB 无 CHECK；新增值只追加。

## HTTP 面（阶段 3b）

`auth.EndUserHandler()` 返回相对路由（chi），宿主决定前缀。直接构造适配器时导入 `github.com/bbxx111/accountkit/httpapi/enduser`，使用 `enduser.New(enduser.Deps{...})` 后挂载其 `Router()`：

```go
r.Route("/v1", func(r chi.Router) {
    r.Mount("/", auth.EndUserHandler())
    r.Group(func(r chi.Router) {
        r.Use(auth.RequireScope("user"))                                   // Bearer 认证 + scope
        r.Mount("/devices", device.ConsumerHandler())
        r.With(auth.RequireRecentAuth()).Post("/devices/{device}:unbind", device.Unbind)
    })
})
```

示例中的 `device.ConsumerHandler()` 是宿主设备业务模块的示意接口，不属于 accountkit 的公开门面。

| 端点 | 认证 | 成功 | 失败 |
|---|---|---|---|
| `POST /users:sendSignInCode` `{channel, target}` | 无 | 200 `{}` | 429 `RESOURCE_EXHAUSTED` + `retry_after_seconds`；400 `CHANNEL_INVALID` / `INVALID_TARGET` |
| `POST /users:signInWithCode` `{phone\|email:{target,code}}` + `X-Device-Id`/`X-Device-Name` | 无 | 200 token | 400 `CODE_INVALID` / `CODE_EXPIRED` / `CODE_ATTEMPTS_EXHAUSTED` / `DEVICE_ID_INVALID`；403 `USER_FROZEN` |
| `POST /users:signInWithIdp` `{wechat\|apple:{...}}` + `X-Device-Id`/`X-Device-Name`（阶段 4a，见下） | 无 | 200 token | 400 `IDP_CREDENTIAL_INVALID` / `IDP_APP_NOT_ALLOWED` / `IDP_NONCE_REPLAYED`；403 `USER_FROZEN`；503 `IDP_UNAVAILABLE`；403 USER_PENDING_DELETION（冷静期账号） |
| `POST /token` `{grant_type:"refresh_token", refresh_token}` | 无 | 200 token | RFC 6749：400 `invalid_grant`；403 `invalid_grant`+`reason:USER_FROZEN`；503 `temporarily_unavailable` |
| `POST /revoke` `{token}` | 无 | 200 空 | 400 `invalid_request`（缺 token） |
| `GET /users/me` | 任意 scope | 200 资源 | |
| `PATCH /users/me` `{display_name}` | `user` | 200 资源 | 400 `NO_FIELDS` / `INVALID_ARGUMENT` |
| `DELETE /users/me` | `user` + 近期认证 | 200 账号资源（`state=PENDING_DELETION`） | 400 `REAUTHENTICATION_REQUIRED` / `INVALID_ACCOUNT_STATE`；403 `USER_FROZEN` |
| `POST /users/me:undelete` | `user:undelete` | 200 账号资源（`state=ACTIVE`） | 400 `INVALID_ACCOUNT_STATE`；403 `USER_FROZEN` |
| `POST /users/me:sendReauthenticationCode` `{channel, target}` | `user` | 200 `{}` | 400 `TARGET_NOT_ANCHOR` |
| `POST /users/me:reauthenticate` 凭证 oneof | `user` | 200 token（无 refresh） | 400 `CODE_*`；401 会话已吊销 |
| `GET /users/me/sessions` | `user` | 200 `{sessions:[…]}` | |
| `DELETE /users/me/sessions/{session}` | `user` | 204 | 400 `INVALID_ID`；404 |
| `POST /users/me/sessions:revokeOthers` | `user` | 200 `{}` | |
| `POST /users/me:sendBindCode` `{channel, target}` | `user` / `user:bind` | 200 `{}` | 429 限流；400 `CHANNEL_INVALID` / `INVALID_TARGET` |
| `GET /users/me/identities` | `user` / `user:bind` | 200 `{identities:[{name, kind, masked_subject, create_time}]}` | |
| `POST /users/me/identities` 凭证 oneof | `user` / `user:bind` | 201 新建 / 200 幂等 | 400 `CODE_*` / `IDP_*`；409 `IDENTITY_ALREADY_BOUND` / `IDENTITY_KIND_LIMIT`；403 `USER_FROZEN` |
| `DELETE /users/me/identities/{identity}` | `user` | 204 | 400 `LAST_ANCHOR_IDENTITY` / `REAUTHENTICATION_REQUIRED` / `INVALID_ID`；404 |

约定：错误体 AIP-193（`/token`、`/revoke` 为 RFC 6749 §5.2）；401 只表示 bearer 缺失/无效/会话已吊销并带 `WWW-Authenticate: Bearer`；scope 不足 403 `INSUFFICIENT_SCOPE`；需重新认证 400 `FAILED_PRECONDITION` `REAUTHENTICATION_REQUIRED`；body ≤ 64 KiB、严格 JSON（未知字段 400 `MALFORMED_BODY`）；所有响应带 `X-Request-Id`。`Deps.ClientIP` 必须返回非空 IP（默认取 `RemoteAddr`；经代理部署时宿主注入可信跳数解析）。405（方法不允许）body 沿用 AIP-193 形状，但不带 `Allow` 头（RFC 9110 建议携带，这里为实现简单接受偏离）。

`authn.WithPrincipal` 仅用于受信任宿主/测试注入 Principal（例如宿主自己的上游网关已经做过身份校验）：`Bearer` 中间件发现 ctx 里已有 Principal 会直接放行，不再调用 `Authenticate`，业务代码不应依赖它来绕过正常的 token 校验路径。

## IdP 登录（阶段 4a）

`POST /users:signInWithIdp`，body 为凭证 oneof 的 `wechat` / `apple` 成员，设备头同验证码登录：

```json
{"wechat": {"app_id": "wx…", "code": "…"}}
{"apple":  {"id_token": "…", "nonce": "<客户端随机原值>"}}
```

- **微信**：服务端用 `WeChatApps` 中对应的 secret 调 `sns/oauth2/access_token` 取 `unionid`（身份 subject）与 `openid`（按 app_id 存入 `provider_meta.openids`，从另一 App 登录时增量合并）。换回的微信 token 用完即弃。缺 `unionid` 视为配置错误（500 + Error 日志），不会降级用 openid。
- **Apple**：仅 RS256；校验 `iss`、`aud ∈ AppleBundleIDs`、`exp`（30s leeway）、`nonce = sha256hex(原值)`；通过后 nonce 以 `SETNX` 登记 `AppleNonceTTL`，重复 → 400 `IDP_NONCE_REPLAYED`。JWKS 懒加载、缓存，未知 kid 触发 ≥ 60s 节流的刷新。`email` 仅在 `email_verified` 为真时作为 `hint_email` 返回，**不会**自动绑定为 EMAIL 身份。
- **scope**：无锚点（手机/邮箱）身份的账号得到 `user:bind`，只能读 `users/me` 与走绑定接口（阶段 4b）；绑定后刷新即得 `user`。
- **错误**：400 `IDP_CREDENTIAL_INVALID` / `IDP_APP_NOT_ALLOWED`（含 IdP 未启用）/ `IDP_NONCE_REPLAYED`；503 `IDP_UNAVAILABLE`（IdP 网络、JWKS 不可得、Redis 不可用；带 `Retry-After: 1`）；500（appid/secret 无效、缺 unionid）。
- **配置**：`SERVER_WECHAT_APPS`、`SERVER_APPLE_BUNDLE_IDS`、`SERVER_APPLE_NONCE_TTL`；`SERVER_WECHAT_API_BASE_URL` / `SERVER_APPLE_JWKS_URL` 仅供测试或代理部署覆盖。`Deps.HTTPClient` 默认 10 秒超时。

## 身份绑定（阶段 4b）

| 端点 | scope | 近期认证 | 成功 | 失败 |
|---|---|---|---|---|
| `POST /users/me:sendBindCode` `{channel, target}` | `user` / `user:bind` | | 200 `{}` | 429 限流；400 `CHANNEL_INVALID` / `INVALID_TARGET` |
| `GET /users/me/identities` | `user` / `user:bind` | | 200 `{identities:[{name, kind, masked_subject, create_time}]}` | |
| `POST /users/me/identities` 凭证 oneof | `user` / `user:bind` | | 201 新建 / 200 幂等 | 400 `CODE_*` / `IDP_*`；409 `IDENTITY_ALREADY_BOUND` / `IDENTITY_KIND_LIMIT`；403 `USER_FROZEN` |
| `DELETE /users/me/identities/{identity}` | `user` | 是 | 204 | 400 `LAST_ANCHOR_IDENTITY` / `REAUTHENTICATION_REQUIRED` / `INVALID_ID`；404 |

规则：一个 subject 全局只属于一个账号（冲突 409，不合并不迁移）；每 kind 最多 `MaxIdentitiesPerKind`（默认 1）个活动身份；同一 subject 重复绑定到本账号为幂等；解绑是软删除，解绑后同一 subject 可绑到任一账号；解绑后必须仍有至少一个手机或邮箱锚点；绑定不改变已签发 token 的 scope，客户端刷新后生效。`masked_subject` 由检索提示拼出（`+86 138****1234`、`ba***@example.com`），第三方身份为空串。

## 手机号与邮箱换绑

`POST /v1/users/me/identities/{identity}:replace` 将指定的旧活动手机/邮箱身份替换为同类新身份。请求使用既有凭证结构，例如 `{"email":{"target":"new@example.test","code":"123456"}}`；成功返回200及新身份的掩码资源，身份 ID 更新、账号 ID 保持，不返回 token pair。

调用前须有 `user` scope 和有效当前会话，近期认证沿用 `REAUTH_MAX_AGE`（默认5分钟）及 `SENSITIVE_OP_VERIFICATION`（默认true）。需要时先走既有重新认证流程，再通过 `users/me:sendBindCode` 获取新地址 BIND 码。近期登录也满足现有新鲜度语义，不保证本次专门向旧地址发送验证码；首版不提供失去全部既有凭据的账号找回。

身份替换和撤销其他会话在一个数据库事务内完成，当前会话及其 refresh token 保留。正常 Redis 下其他会话的 access/内省无效，refresh 由数据库拒绝；Redis 吊销故障仍按原 fail-open 规则处理。旧地址不再进入原账号，但新的独立登录仍可能按原规则自动注册另一个账号。

只支持 PHONE→PHONE、EMAIL→EMAIL，原绑定/解绑接口不变。同目标返回400 `IDENTITY_UNCHANGED`；旧资源不存在/已删除/归属其他用户返回404；新目标已经绑定返回409 `IDENTITY_ALREADY_BOUND`。已消费的验证码不随数据库回滚退还，重取码仍受冷却/额度限制。成功后重试旧 identity 路径返回404；响应丢失时先列出身份确认状态，不盲目自动重发。新旧摘要版本、密文格式与冻结迁移保持兼容。

直接库调用使用 `Auth.Users().ReplaceIdentity(...)` 并传入已认证 Principal。领域层同样检查 scope、新鲜度、账号/会话和身份归属。自定义 `enduser.Service` 无需增加必需方法；实现可选 `enduser.IdentityReplacer` 即可启用新端点，否则认证后的调用返回503 `IDENTITY_REPLACEMENT_NOT_CONFIGURED`。实际检查与验收边界见[换绑验收记录](openspec/changes/archive/2026-10-03-add-identity-replacement/verification.md)。

## 账号生命周期（阶段 5a）

| 端点 | scope | 近期认证 | 成功 | 失败 |
|---|---|---|---|---|
| `DELETE /users/me` | `user` | 是 | 200 账号资源（`state=PENDING_DELETION`，含 `delete_time` / `purge_time`） | 400 `REAUTHENTICATION_REQUIRED` / `INVALID_ACCOUNT_STATE`；403 `USER_FROZEN` |
| `POST /users/me:undelete` | `user:undelete` | | 200 账号资源（`state=ACTIVE`） | 400 `INVALID_ACCOUNT_STATE`；403 `USER_FROZEN` |

- **软删除**：`state → PENDING_DELETION`，`purge_time = now + DeletionCoolingPeriod`（默认 15 天）；该账号**全部**会话（含当前）立即吊销，客户端随后收到 401。冻结中的账号不能注销（403 `USER_FROZEN`，需先解冻）。
- **冷静期**：用手机/邮箱验证码登录得到 `user:undelete`（只能读 `users/me`、调用 `:undelete`、`/token`、`/revoke`）；用微信/Apple 登录 → 403 `USER_PENDING_DELETION`；冷静期内会话不可刷新（`invalid_grant` 并吊销）。`:undelete` 不吊销当前会话，客户端刷新一次即得 `user`。`:undelete` 只接受 `user:undelete` 凭证；`user` 凭证 → 403 `INSUFFICIENT_SCOPE`。
- **purge**（维护任务 `purge_users`）：`purge_time` 到期后单事务匿名化：身份行（含已解绑的）软删并把 subject 摘要/第三方 subject 换成随机值，清空密文、hint、`provider_meta` 与密钥版本；账号行清空 `display_name` 与冻结快照、`state = DELETED`、`purge_time` 改为实际执行时刻；兜底吊销会话；审计事件的 `ip` / `device_id` / `subject_hint` 置 NULL（事件与 `user_id` 保留）；最后按注册顺序调用宿主 `anonymize.Anonymizer`。任一步失败整事务回滚，账号留在 `PENDING_DELETION`，下一轮重试。purge 后原手机号/微信可重新注册为新账号；已 purge 的账号不可 undelete。
- **宿主匿名化器**：实现 `anonymize.Anonymizer{Name() string; Tables() []string; Anonymize(ctx, tx pgx.Tx, userID string) error}` 并通过 `Deps.Anonymizers` 注册（`Name` 唯一；业务表须与库表同库）。宿主应仿照库内 `TestPurgeCoversEveryTableWithUserID` 写覆盖性测试：每张含 `user_id` 的业务表要么被某个 `Anonymizer.Tables()` 覆盖，要么逐表写明豁免理由。
- **维护任务**（`auth.Start` 的 ticker，多副本用 advisory lock 互斥；`auth.RunMaintenanceOnce(ctx)` 供一次性作业/测试手动跑一轮）：`purge_users`（每批 100 个账号，逐账号独立事务，批内失败记日志继续、下一轮重试）→ `cleanup_sessions`（物理删除吊销或 refresh 过期 30 天以上的会话行，每批 1000）。完整任务列表见下节。

## 审计落库与密钥回填（阶段 5b）

- **审计落库**：`Deps.Audit` 为空时默认 `audit.Async(audit.Store)`：事件先进有界队列（1024），单 worker 每 100 条或每 1 秒批量写入 `audit_event`；`auth.Start` 启动 worker，`auth.Close` 刷出残留（上限 5 秒）。写失败、队列满、`Close` 后到达的事件**只丢弃并计数**（`Async.Stats()`；队列满/已关闭的 `Warn` 丢弃日志与写失败的 `Error` 日志各自独立节流，每种最多每 10 秒 1 行，且只带计数与错误），永不影响认证动作的结果。宿主可注入自己的 `audit.Recorder`（例如转发到日志管道），此时库不落库，但 `audit_retention` 仍会清理本表。凭证永不入审计；身份只以 `identity_kind` + `subject_hint`（摘要 / 第三方 subject 前 8 位）出现。
- **保留期**：任务 `audit_retention` 删除 `occur_time` 早于 `now − AuditRetentionDays`（默认 180 天）的事件，每批 1000。
- **分页排序**：管理端 `auditEvents`（阶段 6）按 `occur_time, id`，索引 `(user_id, occur_time, id)`。
- **密钥回填**：任务 `rekey_digests` / `reencrypt_subjects` 逐批（100）把仍用非 active 版本的活跃手机/邮箱身份重算摘要 / 重加密（CAS 更新，与并发登录/绑定不冲突；查找始终覆盖全部已配置版本，回填期间登录不受影响）。单行失败只记日志（`identity_id`）、计数，下一轮重试；第三方身份与已解绑/已 purge 的行不参与。
- **启动期校验**：`auth.Migrate` 在迁移后检查 `identity` 活跃行使用的密钥版本都在配置内，否则返回 `user.ErrUnknownKeyVersion`——在回填完成前移除旧密钥会在启动时失败，而不是在第一次登录时。
- **维护任务全集**（`auth.Start` 的 ticker；`auth.RunMaintenanceOnce(ctx)` 手动跑一轮）：`purge_users` → `cleanup_sessions` → `audit_retention` → `rekey_digests` → `reencrypt_subjects`，顺序执行、失败互不影响。

## 管理面（阶段 6）

宿主挂到 `/admin/v1`，前置 OIDC verifier 的 `Middleware()`；库内每条路由只检查一个角色名（`super-admin` 是包含 `operator` 的 composite role）。handler 内不判角色；C 端 token 打管理面在 verifier 处 401。

| 端点 | 角色 | 成功 | 失败 |
|---|---|---|---|
| `GET /users?filter=&show_deleted=&page_size=&page_token=` | operator | 200 `{users:[…], next_page_token}` | 400 `INVALID_FILTER` / `INVALID_PAGE_SIZE` / `INVALID_PAGE_TOKEN` / `INVALID_SHOW_DELETED` |
| `GET /users/{user}` | operator | 200 用户资源 + `identities`（掩码）+ `active_session_count` | 400 `INVALID_ID`；404 |
| `POST /users/{user}:freeze` `{reason}` | operator | 200 用户资源（`state=FROZEN`，`freeze` 快照） | 400 `INVALID_ARGUMENT`（reason 空/超 200 字/含控制符）/ `INVALID_ACCOUNT_STATE` / `USER_FROZEN`（已冻结）；404 |
| `POST /users/{user}:unfreeze` `{reason?}`（须为 JSON 对象，无 reason 时发送 `{}`） | operator | 200 用户资源（`state=ACTIVE`，`freeze=null`） | 400 `INVALID_ACCOUNT_STATE`；404 |
| `DELETE /users/{user}` | super-admin | 200 用户资源（`state=PENDING_DELETION`） | 400 `INVALID_ACCOUNT_STATE` / `USER_FROZEN`（需先解冻）；404 |
| `POST /users/{user}:undelete` | super-admin | 200 用户资源（`state=ACTIVE`） | 400 `INVALID_ACCOUNT_STATE`（含已 purge）；404 |
| `GET /users/{user}/identities/{identity}:reveal` | super-admin | 200 `{name, kind, subject}`（明文） | 400 `INVALID_ID`；404（非该用户或已解绑） |
| `GET /users/{user}/sessions` | operator | 200 `{sessions:[{name, device_id, device_name, create_time, last_used_time}]}` | 404 |
| `DELETE /users/{user}/sessions/{session}` | operator | 204 | 400 `INVALID_ID`；404 |
| `POST /users/{user}/sessions:revokeAll` | operator | 200 `{revoked_count}` | 404 |
| `GET /users/{user}/auditEvents?page_size=&page_token=` | operator | 200 `{audit_events:[…], next_page_token}` | 400 `INVALID_PAGE_*`；404 |

- **`filter`（AIP-160 子集）**：`term (AND term)*`，`term := field op value`，值为 `"…"` 或无空白裸词；时间戳值必须加双引号（含 `:`）。字段：`state = ACTIVE|FROZEN|PENDING_DELETION|DELETED`；`create_time >= <RFC3339>`、`create_time < <RFC3339>`；`identity.phone = <手机号>`、`identity.email = <邮箱>`（服务端归一化后按摘要等值查找）；`identity.phone_prefix`（如 `"+86138"`）、`identity.phone_suffix`（后 4 位）、`identity.email_prefix`、`identity.email_domain`。身份条件须同属一种 kind 且落在同一条活跃身份上；不支持 `OR` / `NOT` / 括号。`show_deleted=true` 才包含 `PENDING_DELETION` / `DELETED`（AIP-164）。
- **分页**：`page_size` 默认 20、上限 100；`page_token` 不透明 keyset 游标（用户按 `create_time, id`，审计按 `occur_time, id`）；`next_page_token` 为空表示最后一页。
- **DTO**：与 C 端分开定义；时间戳 RFC 3339（UTC）；身份默认掩码，明文只在 `:reveal` 响应中出现；审计事件的可空字段输出 `null`。
- **审计**：每个写操作一条 `actor_kind=ADMIN` 事件（`admin_issuer/subject/username`、`request_id`、`ip`）：`USER_FROZEN` / `USER_UNFROZEN`（reason 为管理员填写值）、`SESSION_REVOKED`（reason `ADMIN`，冻结时为 `USER_FROZEN`）、`USER_DELETED` / `USER_UNDELETED`、`IDENTITY_REVEALED`（只记 kind + hint）。读取端点不产生事件。`ADMIN_FORBIDDEN` 由宿主经 `auth.RecordAdminForbidden` 在 verifier 的 `OnForbidden` 钩子里写入。
- **状态码语义**：管理面上 403 只表示管理员角色不足（来自 verifier）；目标账号的状态问题一律 400 `FAILED_PRECONDITION`（`INVALID_ACCOUNT_STATE` / `USER_FROZEN`）；用户/身份/会话不存在或不属于该用户 → 404。

## 数据库与 Redis 隔离

- 所有表位于 `Config.Schema`（默认 `auth`），迁移记录表也在其中；同一库可并存多个实例（不同 schema）。
- 表：`user_account`、`identity`、`session`、`audit_event`，无外键；对外 id 带类型前缀（`u_`、`i_`、`s_`、`e_`）+ 13 字符 TSID。
- 所有 Redis 键以 `Config.KeyPrefix`（默认 `auth:`）开头。
- `Deps.Pool` 的连接 `search_path` 首位必须是 `Config.Schema`；`Migrate` 会校验并返回 `ErrSearchPath`。宿主业务表建议通过明确的 schema 限定名访问，避免 search_path 名称冲突。

## 配置（环境变量，前缀默认 `SERVER_`）

| 变量 | 默认 | 说明 |
|---|---|---|
| `AUTH_SCHEMA` | `auth` | PostgreSQL schema，`^[a-z][a-z0-9_]{0,62}$` |
| `AUTH_KEY_PREFIX` | `auth:` | Redis 键前缀，须以 `:` 结尾 |
| `JWT_KEYS` / `JWT_ACTIVE_KEY` | 必填 | `1:<base64>,2:<base64>` 版本化 HS256 密钥（≥ 32 字节）与签发版本 |
| `JWT_ISSUER` / `JWT_AUDIENCE` | 必填 | |
| `ACCESS_TOKEN_TTL` / `REFRESH_TOKEN_TTL` / `REFRESH_GRACE` | `15m` / `720h` / `30s` | |
| `REAUTH_MAX_AGE` / `SENSITIVE_OP_VERIFICATION` | `5m` / `true` | 敏感操作要求 `auth_time` 在此时限内 |
| `SUBJECT_HMAC_KEYS` / `SUBJECT_HMAC_ACTIVE_KEY` | 必填 | 手机/邮箱摘要密钥（HMAC-SHA256，≥ 32 字节） |
| `SUBJECT_CIPHER_KEYS` / `SUBJECT_CIPHER_ACTIVE_KEY` | 必填 | 手机/邮箱密文密钥（AES-256-GCM），每个密钥**恰好 32 字节** |
| `CODE_TTL` / `CODE_MAX_ATTEMPTS` / `CODE_COOLDOWN` | `5m` / `5` / `60s` | 验证码 |
| `CODE_DAILY_LIMIT_PER_TARGET` / `CODE_DAILY_LIMIT_PER_IP` | `10` / `100` | |
| `MAX_IDENTITIES_PER_KIND` | `1` | 每账号每 kind 身份上限 |
| `DELETION_COOLING_PERIOD` / `AUDIT_RETENTION_DAYS` | `360h` / `180` | |
| `DEFAULT_REGION` | `CN` | 无 `+` 前缀手机号的解析区域 |
| `MAINTENANCE_INTERVAL` | `5m` | 维护 ticker 周期 |
| `WECHAT_APPS` | 空（未启用） | `appid:secret,appid:secret`（微信登录） |
| `APPLE_BUNDLE_IDS` | 空（未启用） | 逗号分隔（Apple 登录，id_token 的 `aud` 须在其中） |
| `APPLE_NONCE_TTL` | `10m` | |
| `WECHAT_API_BASE_URL` | `https://api.weixin.qq.com` | 测试/代理部署钩子 |
| `APPLE_JWKS_URL` | `https://appleid.apple.com/auth/keys` | 测试/代理部署钩子 |

## 密钥轮换

三组密钥分别使用“版本列表 + active 版本”，版本编号对应的密钥材料不可改写。JWT、身份 HMAC 和身份 AES 密钥独立轮换，不要求它们的 active 编号一致。JWT 仍按原有 access 有效期要求保留旧验签版本；以下说明身份密钥及验证码的正常轮换。

1. 保持 HMAC active=K1，先将所有服务进程升级到支持多版本验证码处理的实现。
2. 向全部进程分发相同的完整 K1、K2 密钥集合，仍使用 K1；确认验证码有效期、次数、冷却和额度策略一致，再滚动切换 active=K2。配置在重新装配或服务重启后生效，不提供热加载；实例 Redis 前缀保持不变。
3. 全部已配置 HMAC 版本共同定位验证码、目标冷却和目标 UTC 日额度。旧码保留原到期时间、错误次数和一次性消费语义，新发码替换全部版本的同用途旧码；目标冷却跨用途共享，日额度合计不同物理键的实际计数。IP 日额度原本就不依赖 HMAC，每次占额仍只增加一次 IP 计数。投递失败不退还冷却或额度。
4. 记录最后一个 K1 active 进程及其在途请求、维护操作全部结束的时刻 T0，然后按下述条件退役 K1。

周期性 `rekey_digests` / `reencrypt_subjects` 回填及默认5分钟维护间隔保持原状。混合 active 期间，不同进程可能把数据库回填到各自的 active，方向仍可能变化；应在 T0 后确认实际回填结果，不能以等待一个维护周期或重启成功代替检查。HMAC 退役要求实际 `Config.Schema` 下未删除的手机/邮箱 `identity` 不再引用 K1 的 `digest_key_version`。若同时独立轮换 AES，另行确认旧 AES 的 `cipher_key_version` 引用归零；只轮换 HMAC 不要求密文也切到相同版本编号。

HMAC 还须等待旧验证码、目标冷却和当日目标额度的业务窗口结束，截止为 `max(T0 + 旧 CodeTTL, T0 + 旧 CodeCooldown, T0 之后的下一个 UTC 零点)`。配置曾变化时使用仍可能存活记录对应的最大旧期限，并留出部署时钟偏差余量。日额度键额外1小时仅用于垃圾回收，不延长业务窗口。数据库引用与这些 Redis 条件同时满足后，才从全部运行配置移除旧 HMAC 并重新部署；AES 退役按其数据库引用条件处理。`Migrate` 缺少仍被未删除身份引用的密钥时返回 `user.ErrUnknownKeyVersion`，但它不检查 Redis 状态，启动成功不证明 HMAC 可以退役。为仍需恢复的旧备份受控保留相应历史密钥，从运行配置移除不等于立即永久销毁。

旧程序曾在不同 active 下留下多份同用途存活验证码时，无法可靠判断签发先后；新实现原子作废全部冲突码并返回原 `CODE_EXPIRED`，冷却和额度保留，用户按原限制重新发码。连续性仅适用于所有进程已升级、持有相同完整密钥集合且策略一致的正常轮换，不覆盖缺失密钥、丢失 Redis 状态或泄露密钥的紧急撤销。回退优先保留支持本能力的程序与完整密钥集合，仅切回 active 并重新计算退役窗口；切回旧程序不能承诺轮换后状态连续。实际验证与未验证项见[轮换验收记录](openspec/changes/archive/2026-10-04-harden-code-key-rotation/verification.md)。

## 测试

- 单测：`go test ./...`（快速 Redis 测试用 miniredis 内嵌，缺少真实依赖的集成用例明确 skip）。
- 集成：设置 `SERVER_TEST_DB_DSN`；轮换集成还要求 `ACCOUNTSVC_TEST_REDIS_URL` 指向一次性真实 Redis，见[开发与验证](docs/development.md)。用例各自创建随机 schema/Redis 前缀并局部清理。
- 独立构建：`GOWORK=off go build ./...`；正式发布检查：`bash scripts/verify.sh`。

## 仓库与发布状态

远程仓库：https://github.com/bbxx111/accountkit.git

Go module：github.com/bbxx111/accountkit。面向自有产品接入的 `v0.1.0` 初版已发布，使用 `go get github.com/bbxx111/accountkit@v0.1.0` 接入；功能、运行限制与版本记录见[发布说明](docs/releases/v0.1.0.md)。发布提交 `48603d6` 的[完整 CI](https://github.com/bbxx111/accountkit/actions/runs/37366666273)已通过，独立宿主已确认无本地 replace 的远端拉取与构建；本地实现历史见[最近验收](openspec/changes/archive/2026-10-06-rename-consumer-to-enduser/verification.md)，发布及上线要求见[检查表](docs/release-checklist.md)。

库版本发布与各产品生产上线分别验收。产品实际环境接入、真实服务商与预发布升级/恢复仍待完成；产品各自配置 schema、Redis 前缀、issuer/audience 和密钥，并承担自己的匿名化与网关接入责任。当前未附开源 LICENSE，本次版本供自有产品接入，尚未向外部使用方定义复用许可。

迁移兼容性变化：原 migrations.Down 保留签名但默认拒绝执行；一次性测试清库改用显式 UnsafeReset。升级、故障处理与恢复步骤见 [迁移手册](docs/migrations.md)。
