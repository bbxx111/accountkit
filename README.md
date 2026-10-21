# accountkit

### 可选用户资料属性（Go 接口）

宿主可在 `New` 前设置 `Config.ProfileAttributes`；nil 关闭接口且不删除已有资料。
`ProfileAttributesConfig` 仅含 `Schema`，`ProfileAttributesView` 仅含 `Data`。
存储仅增加 `profile_attributes` JSONB，实际变化复用账号原 `update_time`。
Schema 由宿主读取或嵌入，没有文件路径环境变量或 accountsvc 自动加载。
宿主装配示例见 [examples/embedded/profile_attributes.go](examples/embedded/profile_attributes.go)：

```go
cfg.ProfileAttributes = &user.ProfileAttributesConfig{
    Schema: json.RawMessage(`{
      "$schema":"https://json-schema.org/draft/2020-12/schema",
      "type":"object",
      "properties":{
        "bio":{"type":"string","maxLength":500},
        "preferences":{"type":"object","properties":{
          "locale":{"type":"string"},"timezone":{"type":"string"}
        },"additionalProperties":false}
      },
      "additionalProperties":false
    }`),
}
// 完成宿主授权后直接提交补丁，无需先读取或传入并发版本。
updated, err := auth.Users().PatchProfileAttributes(ctx, userID, user.ProfileAttributesPatch{
    Document: json.RawMessage(`{"bio":"Hello","preferences":{"locale":"zh-CN"}}`),
}, user.Meta{RequestID: requestID})
if err != nil { return err }
_ = updated
```

并发仅由数据库账号行锁控制：锁内读取最新资料、应用 Merge Patch、完整 Schema 校验后提交。
不需要 revision/expected_revision，不使用 Redis 锁，不返回版本冲突。
不同且不重叠属性均可更新并保留彼此结果；同属性以后取得锁并执行的事务为准。
数组整体替换、父对象删除按 RFC 7396，可能覆盖先写入的重叠子树；未知属性（包括 null 删除）拒绝。
无变化不写入，不推进账号 update_time，不产生修改审计。
读取仅允许 ACTIVE/FROZEN，普通写入仅允许 ACTIVE；冷静期不可读取。
宿主保证同一数据库实例的 Schema 配置一致。库不保存逐行 Schema 身份，不提供 SchemaMismatch 错误。读取只检查通用存储边界；更新按当前 Schema 校验完整合并结果，无变化请求也不得绕过。新增可选属性、放宽约束、修改注解可直接更新配置。收紧规则可能使旧资料完整结果不合法，更新拒绝且原资料与账号 update_time 保持；同次补丁可修复后保存。移除声明属性后，遗留属性仍不能以未知属性 null 删除，须通过过渡 Schema 或专项迁移清理；库不自动丢弃字段。未配置返回 `ErrProfileAttributesNotConfigured`。
需要转换旧资料时由宿主另行评审专项迁移。提交错误可能有不确定结果，宿主须重新读取核对。

冻结、解冻、注销冷静期及恢复保留资料内容。
最终 purge 在原匿名化事务中清空 JSON，复用账号时间语义，不依赖当前配置或 Schema；
不区分曾使用与未使用资料；已清理账号的重复维护不再次修改账号时间。
宿主匿名化回调失败会一并回滚清理。

Schema 使用 Draft 2020-12，根必须接受 `{}`，每层对象固定 properties 和
additionalProperties=false；支持受限本地 `$defs`/`$ref`、标准类型、条件组合和
date/date-time/email/uri/uuid format。资源边界及关键字清单见
[设计说明](openspec/changes/add-schema-validated-user-extensions/design.md)。Schema 原始上限
64 KiB，补丁原始内容和规范化资料各限 16 KiB；资料最多 8 层、512 个值节点。
数值精确处理且按数学值比较，拒绝重复键、非法 Unicode、NUL 及存储 null。

profile_attributes 只承载 bio、locale、timezone、pronouns 等用户资料，禁止存放宿主业务状态、权益或权限。
宿主负责目标账号与属性授权，包括删除和父对象替换；readOnly/writeOnly 只作注解。
资料没有字段级加密，不得存放验证码、令牌、密钥或身份联系方式明文副本。
现有 HTTP 响应、公开资料、JWT 和内省均不附带资料属性，也不新增内置路由或认证查询。
成功审计 `USER_PROFILE_ATTRIBUTES_UPDATED` 只记录账号目标，actor 为 SERVICE/accountkit-host；
`Meta` 仅取 request_id。参数错误支持 `errors.Is(err, user.ErrInvalidArgument)`，
`ProfileAttributesValidationError` 最多提供 16 条受控路径/规则，不含原始值。
新增接口的存储错误不保留可能含资料内容的底层错误链；审计和日志不记录资料值、补丁、未知属性名或 Schema 全文。
既有匿名化回调错误仍保留诊断原因并可能进入维护日志，宿主回调不得在错误中携带资料值或其他隐私数据。

可嵌入宿主服务的 C 端账号体系（多身份账号、JWT + 轮换 refresh 会话、软删除与匿名化、管理面）。
本 README 覆盖配置、密码学原语、迁移、生命周期、C 端 HTTP 面（登录、会话、IdP、身份绑定）、账号注销/恢复/purge、审计落库与密钥回填、管理面。模块路径为 `github.com/bbxx111/accountkit`，根包名为 `accountkit`。宿主装配示例见 `examples/embedded`。

已有能力、待真实环境验证项和未立项候选统一记录在[功能清单与候选功能](docs/features.md)。

首次以库方式接入请阅读 [Go 集成指南](docs/go-integration.md)，配套[可编译调用示例](examples/gointegration/example_test.go)涵盖登录分支、刷新与重新认证、业务事务和错误处理。

可选的标准化指标通过 `Deps.Observer` 接入，默认 no-op。支持显式宿主 Provider 的 OpenTelemetry 和独立 Registry 的原生 Prometheus；[指标指南](docs/metrics.md)包含 13 项指标、有限标签、实例隔离和关闭顺序。[OTel](examples/metricsotel/main.go)与[Prometheus](examples/metricsprometheus/main.go)示例只采集合成事件，不依赖业务存储或监听端口。accountsvc 可显式启用同监听器的 Prometheus 指标端点，默认关闭，配置与网络边界见[服务手册](docs/accountsvc.md#prometheus-指标)。

挂载内置 HTTP handler 的宿主可使用[消费者与管理面 OpenAPI 契约](docs/openapi.md)，按实际地址调整挂载前缀。

本文描述 v0.4.0 已交付的能力，包括身份策略、登录补全、通用审计、指标，以及会话认证来源与客户端描述，版本状态见[发布说明](docs/releases/v0.4.0.md)。账号资源沿用 `/users`，升级调用者见[协议迁移说明](docs/compatibility.md#身份策略与登录补全协议升级)。

accountkit 支持直接作为库嵌入宿主。accountsvc 是项目自带的可选服务实现，使用同一套库能力；选择库集成不需要部署或调用 accountsvc。服务入口是 `cmd/accountsvc`，包含 SMTP 邮件投递、消费者远程鉴权和默认关闭的外部管理员 JWT 验证，运行与配置见 [accountsvc 服务手册](docs/accountsvc.md)。`examples/embedded` 保留为库集成开发示例。

accountsvc 使用一个监听地址承载消费者、管理员、`/v1/introspect` 和探针；公网暴露、直连限制、TLS 终止与入口流量控制由部署方配置，统一规则见 [网关接入手册](docs/gateway-integration.md)。直接嵌入库的宿主也可沿用该契约。旧双监听部署与调用方需按[服务传输迁移说明](docs/compatibility.md#服务单监听与传输迁移)调整。

`v0.2.0` 默认使用 `account` schema；`v0.1.0` 默认仍为 `auth`。升级已有实例时须显式保留原 schema。宿主可通过 `Config.Schema` 或带宿主前缀的 `AUTH_SCHEMA` 设置合法 schema，库与 accountsvc 共用此默认值。

## 生命周期

完整可编译宿主示例见 [examples/embedded/main.go](examples/embedded/main.go)，开发与验证步骤见 [docs/development.md](docs/development.md)。导入使用 `"github.com/bbxx111/accountkit"`，通过 `accountkit.Config`、`accountkit.Deps` 和 `accountkit.New` 装配。旧包名调用方见[包名迁移说明](docs/compatibility.md#包名迁移)。

按 `Config/Deps → New → Migrate → Start → Close` 装配；库不绑定端口，宿主挂载 EndUserHandler 到 /v1，AdminHandler 到 /admin/v1。管理员验证器及主体解析器可选；未配置时管理接口返回 503 ADMIN_NOT_CONFIGURED。生产宿主须在管理路由前完成身份验证，并提供 AdminVerifier/AdminPrincipal，服务商发送器也由宿主注入。终端用户 HTTP 包与门面的源码迁移见[终端用户入口迁移说明](docs/compatibility.md#终端用户-http-入口迁移)。
## 领域层（阶段 3a）

`auth.Users()` 返回 `*user.Service`，是所有业务规则的唯一所在；HTTP handler（阶段 3b）、维护任务与管理面都只调用它。

| 能力 | 方法 | 备注 |
|---|---|---|
| 发登录码 | `SendSignInCode(ctx, channel, target, meta)` | 返回 `(user.CodeChallenge, error)`；先占额再发送，发送失败不退额；被限流返回 `*code.RateLimitedError` |
| 验证码登录 | `SignInWithCode(ctx, cred, dev, meta)` | `cred` 为包含轮次标识的 `user.CodeCredential`；返回 SignInResult 的 Tokens/Pending 联合；满足策略才建号；同设备再登录先吊销旧会话 |
| 刷新 | `Refresh(ctx, refreshToken, meta)` | 行锁 + CAS 轮换；30s 宽限内重放返回同一 pair；宽限外重放吊销会话 |
| 登出 | `Revoke(ctx, refreshToken, meta)` | RFC 7009 语义：未知 token 也返回成功 |
| 校验 access | `Authenticate(ctx, raw)` | 验签 + id 格式 + Redis 吊销集（Redis 不可用时 fail-open） |
| 重新认证 | `SendReauthenticationCode` / `Reauthenticate` | 只接受本账号已启用手机/邮箱身份；刷新 `auth_time` 并重签 access（不发 refresh） |
| 资料 | `GetMe` / `UpdateDisplayName` | 显示名 ≤ 32 个字符 |
| 批量公开资料 | `BatchPublicProfiles(ctx, ids)` | 一次查询返回 ID/DisplayName/State；未知账号略去，待注销/已删除账号显示名为空；宿主先过滤业务权限 |
| 宿主事务保护 | `WithActiveUsers(ctx, ids, fn)` | 验证、去重并排序锁定 ACTIVE 账号后执行 `fn(pgx.Tx)`；库提交，错误或 panic 回滚 |
| 离线导入 | `ImportAccounts(ctx, tx, accounts)` | 调用者事务中的 ACTIVE PHONE/EMAIL 锚点账号或 DELETED 匿名墓碑；调用者提交/回滚 |
| 会话 | `ListSessions` / `RevokeSession` / `RevokeOtherSessions` | 他人的会话一律 `ErrNotFound` |

### 身份策略与登录补全

`Config.EnabledIdentityKinds`（E）决定可使用的身份；`Config.RequiredIdentityKinds`（R）决定账号完整性。R 只接受 PHONE/EMAIL，按 any-of 判断，并须为 E 的子集。账号至少有一个已验证、未删除且启用的身份，并且 R 为空或其中任意一种身份已具备，才取得 `user` scope。R 不是 MFA，也不要求每次使用其中的身份登录。

Go 的 nil 表示省略；非 nil 空切片表示显式空。E 省略时沿用 PHONE、EMAIL 和已配置 IdP，R 省略时默认 PHONE/EMAIL 二选一。显式空 E 非法，显式空 R 允许纯第三方注册。环境键为宿主前缀加 `ENABLED_IDENTITY_KINDS` / `REQUIRED_IDENTITY_KINDS`，使用逗号分隔的 UPPER_SNAKE_CASE；只有 `[]` 表示显式空，已设置但空白非法。例如 E=`EMAIL,WECHAT`、R=`EMAIL`；E=`WECHAT` 时 R 必须为 `[]`。显式启用的类型必须具备真实发送器或验证器；禁用渠道可省略发送器。省略 E 的旧宿主仍保留原发送器必填和 Disabled 装配规则。策略为装配时快照，不随短暂依赖故障改变。

`SignInWithCode` 和 `SignInWithIdp` 返回 `user.SignInResult{Tokens *user.TokenResult, Pending *user.PendingSignIn}`，恰有一个成员。已知账号按全部可用身份推导 `user` 或 `user:bind`；未知身份满足策略时直接注册，否则只返回 pending，尚无账号、身份或会话。pending 不含 user_id、scope 或 token pair。

补全通过 `SendSignInCompletionCode(ctx, token, channel, target, dev, meta)` 获取专用轮次，再调用 `CompleteSignIn(ctx, token, cred, dev, meta)`。初次登录必须提供设备 ID；后续设备字段可省略，重复提供则须与首次一致。补全只接受已启用的 PHONE/EMAIL 验证码。目标属于活动账号 B 时关联尚未占用的本次身份并沿用 B 的 user_id；目标无归属且满足 R 时原子注册。页面应说明“验证并继续”会关联本次身份，未注册时自动注册。第三方 hint_email 不能证明邮箱归属，已有两个账号不会自动合并。R=[] 时未知第三方直接注册，不能自动发现另一邮箱账号；应先登录原账号再主动绑定。

HTTP 新增 `POST /users:sendSignInCompletionCode`（`{sign_in_completion_token,channel,target}`）和 `POST /users:completeSignIn`（`{sign_in_completion_token,phone|email:{target,code_id,code}}`）。唯一票据字段为 `sign_in_completion_token`，普通发码不接收它，也不接收公开 purpose。pending 返回 HTTP 200、no-store，仅含 `next_action=COMPLETE_IDENTITY`、票据、`expire_time`、`enabled_identity_kinds`、`required_identity_kinds`；成功继续使用顶层 RFC 6749 token 字段。

票据使用独立随机秘密，数据库仅存摘要；`sign_in_completion_flow` 中的证明、设备与完成结果按版本加密并绑定实例和流程。原期限最多5分钟，取原证明更早期限，追加证明和重试不续期。只有相同完成请求、原期限内且账号/会话/refresh 仍有效时才能读取原 token pair；refresh 已轮换则返回 `SIGN_IN_ALREADY_COMPLETED`，票据过期返回 `SIGN_IN_COMPLETION_INVALID`。验证码消费不会随 SQL 回滚恢复，证明落库失败须按原额度重新取证；数据库备份不含 Redis，恢复不能跳过缺失证明。

直接构造 `user.NewService` 的宿主可用 `user.Deps.SignInCompletionFlowContext` 补充稳定实例域；原 `IdentityFlowContext` 保留为 deprecated 兼容字段，新字段非空时优先，否则回退旧字段。自定义 IdP 的应用允许列表等配置变化时须改变 `ProviderConfiguration`。内置验证器自动提供配置指纹。策略或实例上下文变化拒绝旧流程；有效流程引用的旧加密密钥须保留到到期或清理。重命名保持密文上下文和 AAD、`f_` ID、审计资源类型 `IDENTITY_FLOW` 与 JSON `flow_id`，公开 Go 名称迁移见[兼容说明](docs/compatibility.md#登录补全流程重命名)。

### 验证码轮次

四类发码方法返回 `user.CodeChallenge{CodeID, ExpireTime}`。发送器接受投递后才返回轮次；发送失败保留冷却和额度，只清理仍匹配本次标识的轮次，不恢复旧轮次。发送器的 `sender.Message` 签名保持原样。PHONE/EMAIL 的登录、绑定、重新认证和换绑统一传 `user.CodeCredential{Channel, Target, CodeID, Code}`，用途由方法确定，不提供无标识验证旁路。

`code_id` 是 128 bit 随机值的 32 位小写十六进制编码。重发使同渠道、用途和目标的旧轮次失效，即使验证码数字相同；BIND 绑定用户，REAUTH 绑定用户和会话，SIGN_IN_COMPLETE 单独绑定流程。轮次匹配、绑定校验、错误次数和一次性消费在 Redis 原子执行；并发正确提交至多成功一次。格式错误标识为 400 `INVALID_ARGUMENT`，合法但未知、过期、已消费或绑定不符为 400 `CODE_EXPIRED`，不扣当前轮次或目标失败预算。每轮默认最多 5 次，可显式配置为 3 次；第三次正确可以成功，第三次错误返回 `CODE_INVALID` 并使轮次作废，之后为 `CODE_EXPIRED`。若同时达到目标累计失败上限，优先返回下述 `TARGET_VERIFY_LIMIT`。

另有跨 SIGN_IN/BIND/REAUTH/SIGN_IN_COMPLETE 和重发轮次共享的目标失败预算：同渠道、归一化目标从首次有效轮次错误起，默认 15 分钟内最多 10 次错误。重发、正确验证和后续错误均不清除或延长窗口。达到上限后发码和有效轮次验证返回 429 `TARGET_VERIFY_LIMIT`，`Retry-After` 指向原窗口结束。失效标识流量仍由宿主接口/IP 限流控制。所有凭证响应设置 `Cache-Control: no-store`；不要将完整轮次标识、验证码或目标放入日志、审计或 URL。

### 宿主账号事务

通过 `Deps.BeforeDelete user.BeforeDelete` 注入 `func(ctx context.Context, tx pgx.Tx, userID string) error`。消费者和管理员注销共用此回调，在账号锁和 ACTIVE 状态检查后、改变状态前执行。返回 `user.ErrDeletionBlocked` 时，两个 HTTP 面均为 400 `FAILED_PRECONDITION`、reason `DELETION_BLOCKED`；基础设施错误为固定安全消息的 500。拒绝或故障使账号、会话及同批宿主写入一起回滚；不配置时保持原注销行为。

新建宿主关系时可调用 `auth.Users().WithActiveUsers(ctx, ids, func(tx pgx.Tx) error { ... })`，要求非空账号集合及非 nil 回调；宿主空批次直接跳过。先取得按 ID 排序的账号锁，再取得业务资源锁。回调只能用传入事务，不得自行提交或回滚；未知、非法或非 ACTIVE 账号在回调前拒绝。`BatchPublicProfiles` 和这些事务方法只提供 Go 契约，不新增任意账号资料或导入 HTTP 入口。

离线导入须停写并核验目标库，使用 schema 匹配连接池创建的事务传入 `ImportAccounts`。`user.ImportAccount` 保留账号原 ID/时间，创建/更新时间必需非零且允许历史时钟回退。ACTIVE 必须有 PHONE/EMAIL 锚点，无删除/匿名化时间；DELETED 墓碑无锚点和显示名。库复用归一化、密文与摘要规则，重复账号或身份冲突直接失败，不合并、不发码、不创建会话、不签令牌、不调用 IdP。错误消息脱敏，调用者可用 `errors.Is`/`errors.As` 区分冲突和数据库原因，解包后的诊断也须脱敏。调用者负责批次记录、业务引用迁移和失败后的全事务回滚，详见[导入边界](docs/compatibility.md#离线账号导入)。

普通创建和 ACTIVE/DELETED 导入均初始化为空资料属性（`{}`，复用账号原时间）。`ImportAccount` 不接收扩展；ACTIVE 导入提交后可另调 `PatchProfileAttributes`，DELETED 拒绝写入。两个调用不承诺同批事务原子性。

### 会话过期语义

消费者/管理员会话列表及管理详情的 `active_session_count` 只包含未吊销且 `refresh_expire_time` 严格晚于当前判定时刻的会话，等于期限即过期。判定统一使用应用时钟的UTC微秒精度，刷新期限不额外叠加JWT leeway。

刷新与重新认证在取得行锁后重新判定期限；等待锁期间到期也不能续用。到期refresh返回400 OAuth `invalid_grant`，到期重新认证返回401 `TOKEN_INVALID`，客户端需要重新登录。有效重新认证仍只返回access，不延长refresh期限；关闭敏感操作新鲜度检查也不能绕过会话到期。REAUTH验证码可能已消费，失败不恢复该码。

宽限资格取会话读取后的时间；缓存返回后再检查会话/pair期限并计算剩余有效期。已符合宽限的请求不会仅因缓存等待跨过宽限而被认定为重放，仍返回同一pair。缓存不可用或到期拒绝不会被误记为已遏制的重放。

刷新期限与access有效性独立：自然到期不写撤销集，原access和内省仍按JWT及Redis吊销规则判断；“不在会话列表中”不等于“所有已发access都已失效”。显式单个/批量撤销、同设备重登和账号生命周期操作仍覆盖未清理的过期会话。列表读取不删除数据，原30天会话清理保留期不变。实际验证见[会话过期验收记录](openspec/changes/archive/2026-10-04-harden-session-expiry/verification.md)。

### Redis 键与失败模式

键前缀为 `Config.KeyPrefix`（默认 `auth:`）：`challenge:*`（验证码轮次）、`verify_failure:*`（目标失败预算）、`cooldown:*`、`quota:*`（验证码与额度，Lua 原子）、`grace:*`（AES-GCM 加密的轮换后 pair，TTL 30s）、`revoked:*`（吊销集，TTL = access TTL + 验签 leeway，覆盖 `Signer.Parse` 对 exp 的容忍窗口，避免吊销条目先于该窗口内仍会验签通过的旧 token 过期）。

| 场景 | Redis 不可用时 |
|---|---|
| 发码、验证码登录/重新认证 | fail-closed：`user.ErrUnavailable` |
| 宽限期查询 | 退化为拒绝（`ErrInvalidGrant`），不吊销会话 |
| 吊销检查 | fail-open：按签名与过期校验，记 Warn 日志 |
| 重放检测 / 冻结 / 待删除时的吊销 | 数据库写失败向上返回错误（不记"已遏制"审计），吊销集仍尽力写入 |

### 发送器

省略 Enabled 时 `Deps.SMSSender` / `Deps.EmailSender` 必填；显式策略只要求已启用渠道的发送器。正式接口为 `sender.SMSCodeSender`、`sender.EmailCodeSender`，输入为 `sender.CodeMessage{Purpose, Code, TTL}`；Deps 字段名不变。原 `SMSSender`、`EmailSender`、`Message` 保留为兼容别名，名称映射见[兼容指南](docs/compatibility.md#验证码发送器命名与渲染接入)。开发环境可用 `sender.NewLog(logger)`（把验证码打进日志，**禁止用于生产**）。

使用可选 SMTP 传输时，宿主通过 `smtp.NewWithRenderer(cfg, renderer)` 注入 `smtp.Renderer`。例如导入 `github.com/bbxx111/accountkit/examples/codeemail` 与 `github.com/bbxx111/accountkit/user/sender/smtp` 后：

```go
renderer := codeemail.New("en") // 固定默认语言；也可选择 zh-CN。
email, err := smtp.NewWithRenderer(smtpConfig, renderer)
if err != nil {
    return err
}
deps := accountkit.Deps{EmailSender: email /* 其余依赖由宿主装配 */}
_ = deps
```

[可编译本地化示例](examples/codeemail/example_test.go)与[渲染实现](examples/codeemail/renderer.go)覆盖四种用途、中文/英文、Text/HTML 和请求语言回退。`Render(context.Context, sender.CodeMessage) (smtp.EmailContent, error)` 返回 Subject、必需 Text 和可选 HTML；模板必须正确表达用途、实际验证码与有效期，动态 HTML 值由宿主使用 `html/template` 等方式转义。SMTP 负责 MIME 编码，不能验证任意语言的文案语义。原 `smtp.New(cfg)` 继续使用 `smtp.LegacyChineseRenderer{}`，保留原四用途中文纯文本和有效期分钟数向上取整行为。

宿主在自己的中间件或 Go 入口选择语言，再用示例的 `codeemail.WithLanguage(ctx, "zh-CN")` 派生 context 并传入本次服务调用；核心不解析 Accept-Language，不读取业务偏好，不增加消息字段。示例只支持 `zh-CN`/`en`：缺失或未知请求提示回退到实例默认，未知默认语言回退到 `zh-CN`。同一渲染器可并发复用，配置和模板构造后不再修改；不要通过共享字段设置“当前语言”。

渲染与 SMTP 共用一次总超时预算，派生 context 保留宿主值。宿主回调必须及时响应取消，避免无界 I/O；库不会通过遗留 goroutine 强制中断不合作的回调。渲染失败、非法内容或取消返回固定 `sender.ErrUnavailable`，沿用 503 和额度/冷却保留契约，不回退默认模板、不自动重试。宿主不得在日志中输出验证码、完整地址、正文或原始渲染错误。示例只做本地渲染与构造，不发送网络请求、不读取真实凭据，输出仅为非敏感标题或装配结果。

使用服务商托管模板时，直接实现 `EmailCodeSender.SendEmail(ctx, target, message)` 或 `SMSCodeSender.SendSMS(ctx, target, message)` 并注入原 Deps 字段。宿主把 `message.Purpose` 映射到服务商模板 ID，把 `message.Code`、`message.TTL` 转换为模板参数，并自行选择语言、处理取消及安全错误；这种适配无需 SMTP Renderer，也无需先生成正文。不要将模板 ID 或服务商参数加入核心 CodeMessage。

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
| `POST /users:sendSignInCode` `{channel, target}` | 无 | 200 `{code_id, expire_time}` | 429 `RESOURCE_EXHAUSTED` + `retry_after_seconds`；400 `CHANNEL_INVALID` / `INVALID_TARGET` |
| `POST /users:signInWithCode` `{phone\|email:{target,code_id,code}}` + `X-Device-Id`/`X-Device-Name` | 无 | 200 token 或 pending | 400 `CODE_INVALID` / `CODE_EXPIRED` / `DEVICE_ID_INVALID`；429 `TARGET_VERIFY_LIMIT`；403 `USER_FROZEN` |
| `POST /users:signInWithIdp` `{wechat\|apple:{...}}` + `X-Device-Id`/`X-Device-Name`（阶段 4a，见下） | 无 | 200 token 或 pending | 400 `IDP_CREDENTIAL_INVALID` / `IDP_APP_NOT_ALLOWED` / `IDP_NONCE_REPLAYED`；403 `USER_FROZEN`；503 `IDP_UNAVAILABLE`；403 USER_PENDING_DELETION（冷静期账号） |
| `POST /token` `{grant_type:"refresh_token", refresh_token}` | 无 | 200 token | RFC 6749：400 `invalid_grant`；403 `invalid_grant`+`reason:USER_FROZEN`；503 `temporarily_unavailable` |
| `POST /revoke` `{token}` | 无 | 200 空 | 400 `invalid_request`（缺 token） |
| `GET /users/me` | 任意 scope | 200 资源 | |
| `PATCH /users/me` `{display_name}` | `user` | 200 资源 | 400 `NO_FIELDS` / `INVALID_ARGUMENT` |
| `DELETE /users/me` | `user` + 近期认证 | 200 账号资源（`state=PENDING_DELETION`） | 400 `REAUTHENTICATION_REQUIRED` / `INVALID_ACCOUNT_STATE` / `DELETION_BLOCKED`；403 `USER_FROZEN` |
| `POST /users/me:undelete` | `user:undelete` | 200 账号资源（`state=ACTIVE`） | 400 `INVALID_ACCOUNT_STATE`；403 `USER_FROZEN` |
| `POST /users/me:sendReauthenticationCode` `{channel, target}` | `user` | 200 `{code_id, expire_time}` | 400 `TARGET_NOT_ANCHOR` |
| `POST /users/me:reauthenticate` 凭证 oneof | `user` | 200 token（无 refresh） | 400 `CODE_*`；401 会话已吊销 |
| `GET /users/me/sessions/current` | `user` / `user:bind` | 200 当前会话（no-store） | 401 `TOKEN_INVALID`；403 scope不足；503 `SESSION_QUERY_NOT_CONFIGURED` / 依赖不可用 |
| `GET /users/me/sessions` | `user` | 200 `{sessions:[…]}` | |
| `DELETE /users/me/sessions/{session}` | `user` | 204 | 400 `INVALID_ID`；404 |
| `POST /users/me/sessions:revokeOthers` | `user` | 200 `{}` | |
| `POST /users/me:sendBindCode` `{channel, target}` | `user` / `user:bind` | 200 `{code_id, expire_time}` | 429 限流；400 `CHANNEL_INVALID` / `INVALID_TARGET` |
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
- **scope**：已知账号按当前 E/R 的全部可用身份得到 `user` 或 `user:bind`；未知第三方在默认 R 下返回 pending，补全后才关联或建号。已有 user:bind 账号绑定补齐后，新一轮刷新取得 user。
- **错误**：400 `IDP_CREDENTIAL_INVALID` / `IDP_APP_NOT_ALLOWED`（含 IdP 未启用）/ `IDP_NONCE_REPLAYED`；503 `IDP_UNAVAILABLE`（IdP 网络、JWKS 不可得、Redis 不可用；带 `Retry-After: 1`）；500（appid/secret 无效、缺 unionid）。
- **配置**：宿主前缀加 `WECHAT_APPS`、`APPLE_BUNDLE_IDS`、`APPLE_NONCE_TTL`；`WECHAT_API_BASE_URL` / `APPLE_JWKS_URL` 仅供测试或代理部署覆盖。`Deps.HTTPClient` 默认 10 秒超时。

只有第三方身份的账号需要近期认证时，重新走普通 IdP 登录；同设备会话被替换，客户端更新 token pair 并确认仍为同一 user_id 后再继续敏感动作。手机/邮箱重新认证仍只更新当前会话 auth_time 和 access，不延长 refresh。纯第三方账号没有手机/邮箱自助恢复路径；普通 IdP 登录仍拒绝冷静期账号，合法管理员恢复保持。

## 身份绑定（阶段 4b）

| 端点 | scope | 近期认证 | 成功 | 失败 |
|---|---|---|---|---|
| `POST /users/me:sendBindCode` `{channel, target}` | `user` / `user:bind` | | 200 `{code_id, expire_time}` | 429 限流；400 `CHANNEL_INVALID` / `INVALID_TARGET` |
| `GET /users/me/identities` | `user` / `user:bind` | | 200 `{identities:[{name, kind, masked_subject, create_time}]}` | |
| `POST /users/me/identities` 凭证 oneof | `user` / `user:bind` | | 201 新建 / 200 幂等 | 400 `CODE_*` / `IDP_*`；409 `IDENTITY_ALREADY_BOUND` / `IDENTITY_KIND_LIMIT`；403 `USER_FROZEN` |
| `DELETE /users/me/identities/{identity}` | `user` | 是 | 204 | 400 `LAST_ANCHOR_IDENTITY` / `REAUTHENTICATION_REQUIRED` / `INVALID_ID`；404 |

规则：一个 subject 全局只属于一个账号（冲突 409，不合并不迁移）；每 kind 最多 `MaxIdentitiesPerKind`（默认 1）个活动身份；同一 subject 重复绑定到本账号为幂等；解绑是软删除，解绑后同一 subject 可绑到任一账号；解绑或换绑后的最终身份集合必须仍有至少一个已启用身份并满足当前 R；绑定不改变已签发 token 的 scope，客户端新一轮刷新后生效。`masked_subject` 由检索提示拼出（`+86 138****1234`、`ba***@example.com`），第三方身份为空串。

## 当前会话与客户端描述

以下能力随 v0.4.0 发布，v0.3.0 不含此查询、来源撤销或客户端描述。曾跟随开发分支接入的调用者须按[兼容说明](docs/compatibility.md#会话认证依据与客户端描述)调整；迁移和部署须按[会话升级说明](docs/migrations.md#会话认证依据初始整合)协调切换。

GET /users/me/sessions/current 返回真实会话 name（末段是 s_ ID）、原设备/时间字段、is_current=true、数据库最新 RFC3339 auth_time 和非空 authentication_identities。身份摘要只有 name/kind，按具体 ID 排序；客户端按完整 name 匹配拟解绑身份，不能只比较 kind。来源缺失或失效是内部不变量错误，返回固定500，不返回正常空集合，也不从身份列表或审计补造来源。查询是快照，最终影响以提交时状态为准。重新认证成功后保存新 access、保留原 refresh，再重查详情；旧 JWT 不被改写，查询也不赋予旧 token 新的近期认证证明。

普通登录来源只有本次验证的身份，补全包含该次最终成功验证的具体身份；新建会话在事务内校验身份归属和有效状态，写入非空去重集合。重新认证先检查当前来源，再用本次手机/邮箱身份替换整个集合，同时更新 auth_time。刷新、普通绑定和换绑 BIND 证明不改来源。成功解绑在同事务撤销同账号来源命中的所有未吊销会话，包括当前及 refresh 已到期但未清理的会话；同账号任一未吊销会话缺少有效来源时整事务拒绝，不能当作未命中放行。原204仍表示成功，后续认证失效时客户端清理凭据并重新登录，不能将成功解绑重试成失败。

查询支持 user/user:bind，user:undelete 返回403；缺少可选 CurrentSessionService 的自定义 HTTP 宿主返回503 SESSION_QUERY_NOT_CONFIGURED。数据库会话缺失、归属不符、撤销或 refresh 到期返回401 TOKEN_INVALID与Bearer挑战；可识别依赖不可用503、内部错误固定500，所有响应 no-store。普通 Authenticate/内省仍为JWT和Redis校验，不增加数据库查询；Redis吊销失败仍保留原fail-open与告警，数据库会拒绝已撤销会话的refresh/详情查询。

客户端描述只在成功验证码登录、IdP登录、补全、重新认证及新一轮刷新观察；发码、绑定、换绑、普通GET/业务请求、Authenticate和内省不采集。原 X-Device-Id / X-Device-Name 和请求体不变，下列头全部可选：

| 请求头 | 值/限制 |
|---|---|
| X-Client-Type | WEB / NATIVE |
| X-Client-OS-Name、X-Client-Browser-Name、X-Client-App-Name | 名称最多64字符 |
| X-Client-OS-Version、X-Client-Browser-Version、X-Client-App-Version | 版本最多32字符 |
| X-Device-Type | DESKTOP / PHONE / TABLET |
| X-Device-Model | 最多64字符 |

文本先拒绝非法UTF-8/控制字符，再去除首尾空白；名称、制造商、型号最多64个Unicode字符，版本32、build64。规范化后的完整JSON（包括键、结构与转义）至多2048字节；重复显式头拒绝。非法显式值在凭证消费前400 INVALID_ARGUMENT / INVALID_CLIENT_INFO，token端点为OAuth invalid_request。合法显式值优先，其次可用Client Hints，最后UA；自动输入失败不阻断认证。UA与全部Sec-CH-UA*值各自合计至多2048字节，超限/畸形忽略相应自动输入。不主动要求额外高熵提示；具体支持字段与CORS见[网关说明](docs/gateway-integration.md#客户端描述透传与-cors)。宿主可注入 Deps.ClientInfo，直接Go调用传 Meta.ClientInfo，采用字段名初始化，见[Go指南](docs/go-integration.md#当前会话解绑与客户端描述)。

客户端描述按会话只存一行 `client_info JSONB`，主键为 session_id，保留 observe_time/create_time/update_time；没有描述则没有行。业务读写统一校验，数据库保留主键、ID格式及非空约束，不定义JSON对象或业务CHECK。读取非法描述返回内部错误。HTTP详情和两个面的会话列表只可含 `client_info` 与 `client_info_update_time`，缺失成员省略，使用同一嵌套形状：

```json
{"type":"NATIVE","os":{"name":"Android","version":"14"},"device":{"type":"PHONE","manufacturer":"Example","model":"Model"},"app":{"name":"Example App","version":"1.0","build":"100"},"browser":{"name":"Chrome","version":"123"},"extensions":{"engine":{"channel":"stable"}}}
```

Go模型保留平坦字段并新增 DeviceManufacturer、AppBuild 和 Extensions；app/browser 可以并存，WEB/NATIVE 不清除另一组信息，单独的版本/build也是合法本次观察。成功登录、最终补全、重新认证和新轮refresh有规范化非空描述时整体替换并记录本次服务器观察时间；nil/空描述不写入，保留已有值与时间，不跨请求拼接字段。pending首次描述与观察时间放在加密payload；最终本次非空描述优先，否则整体沿用首次描述及其原观察时间。首次没有描述而完成时才提供描述，只写该次一行。失败、事务回滚、宽限刷新及补全结果重放均不更新描述或观察时间。

extensions仅接受直接Go调用或宿主回调，允许JSON primitive/null/object/array，不新增任意JSON请求体、请求头或制造商/build显式头。所有层级键匹配 `[a-z][a-z0-9_]{0,63}`；根扩展对象不计深度和节点，直接值深度1，每个值（含容器和数组元素）计一节点；最多64节点、4层、每数组16项、每字符串256个Unicode字符。完整原始JSON与每个RawMessage在解析前分别限16KiB，扩展根map超过64项在分配规范化副本前拒绝；规范化完整JSON仍限2048字节。核心对象拒绝未知键、重复键和错误类型；扩展也拒绝重复键、非法UTF-8/控制字符及非法JSON数字。合法指数数值精确规范化为十进制，先计算展开长度再有界分配，展开后的JSON仍受2048字节限制，不通过浮点转换损失整数精度。固定敏感词段和组合键清单见[Go指南](docs/go-integration.md#当前会话解绑与客户端描述)，规范化、解码和DTO均深拷贝扩展。宿主仍须确定允许的来源、扩展语义和隐私白名单，不能依赖敏感键检查识别任意值中的隐私。

描述不能作为授权、限流身份或可信设备证明；不保存原始UA/IP/位置，不进入日志、审计正文或指标标签，随会话清理和同事务匿名化删除。last_used_time和描述观察时间均不表示每次业务活动。当前变更验收状态见[简化会话上下文记录](openspec/changes/archive/2026-10-20-simplify-session-context/verification.md)。

## 手机号与邮箱换绑

`POST /v1/users/me/identities/{identity}:replace` 将指定的旧活动手机/邮箱身份替换为同类新身份。请求使用既有凭证结构，例如 `{"email":{"target":"new@example.test","code_id":"0123456789abcdef0123456789abcdef","code":"123456"}}`；成功返回200及新身份的掩码资源，身份 ID 更新、账号 ID 保持，不返回 token pair。

调用前须有 `user` scope 和有效当前会话，近期认证沿用 `REAUTH_MAX_AGE`（默认5分钟）及 `SENSITIVE_OP_VERIFICATION`（默认true）。需要时先走既有重新认证流程，再通过 `users/me:sendBindCode` 获取新地址 BIND 码。近期登录也满足现有新鲜度语义，不保证本次专门向旧地址发送验证码；首版不提供失去全部既有凭据的账号找回。

身份替换和会话撤销在一个数据库事务内完成。其他会话继续撤销；当前会话先检查有效非空来源，仅在来源不含被替换身份时保留，命中则一并撤销，来源缺失或失效返回内部错误并回滚。新目标 BIND 证明不替换来源；200 成功不发 token，当前会话也可能需要退出。正常 Redis 下被撤销会话的 access/内省无效，refresh 由数据库拒绝；Redis 吊销故障仍按原 fail-open 规则处理。旧地址不再进入原账号，但新的独立登录仍可能按原规则自动注册另一个账号。

只支持 PHONE→PHONE、EMAIL→EMAIL，原绑定/解绑接口不变。同目标返回400 `IDENTITY_UNCHANGED`；旧资源不存在/已删除/归属其他用户返回404；新目标已经绑定返回409 `IDENTITY_ALREADY_BOUND`。已消费的验证码不随数据库回滚退还，重取码仍受冷却/额度限制。成功后仍有效会话重试旧 identity 路径返回404，原会话已撤销则按认证失效拒绝；响应丢失时用仍有效会话或重新登录后列出身份确认状态，不盲目自动重发。新旧摘要版本、密文格式与冻结迁移保持兼容。

直接库调用使用 `Auth.Users().ReplaceIdentity(...)` 并传入已认证 Principal。领域层同样检查 scope、新鲜度、账号/会话和身份归属。自定义 `enduser.Service` 无需增加必需方法；实现可选 `enduser.IdentityReplacer` 即可启用新端点，否则认证后的调用返回503 `IDENTITY_REPLACEMENT_NOT_CONFIGURED`。实际检查与验收边界见[换绑验收记录](openspec/changes/archive/2026-10-03-add-identity-replacement/verification.md)。

## 账号生命周期（阶段 5a）

| 端点 | scope | 近期认证 | 成功 | 失败 |
|---|---|---|---|---|
| `DELETE /users/me` | `user` | 是 | 200 账号资源（`state=PENDING_DELETION`，含 `delete_time` / `purge_time`） | 400 `REAUTHENTICATION_REQUIRED` / `INVALID_ACCOUNT_STATE` / `DELETION_BLOCKED`；403 `USER_FROZEN` |
| `POST /users/me:undelete` | `user:undelete` | | 200 账号资源（`state=ACTIVE`） | 400 `INVALID_ACCOUNT_STATE`；403 `USER_FROZEN` |

- **软删除**：`state → PENDING_DELETION`，`purge_time = now + DeletionCoolingPeriod`（默认 15 天）；该账号**全部**会话（含当前）立即吊销，客户端随后收到 401。冻结中的账号不能注销（403 `USER_FROZEN`，需先解冻）。
- **冷静期**：用手机/邮箱验证码登录得到 `user:undelete`（只能读 `users/me`、调用 `:undelete`、`/token`、`/revoke`）；用微信/Apple 登录 → 403 `USER_PENDING_DELETION`；冷静期内会话不可刷新（`invalid_grant` 并吊销）。`:undelete` 不吊销当前会话，客户端新一轮刷新按当前 R 取得 `user` 或 `user:bind`。`:undelete` 只接受 `user:undelete` 凭证；`user` 凭证 → 403 `INSUFFICIENT_SCOPE`。
- **purge**（维护任务 `purge_users`）：`purge_time` 到期后单事务匿名化：身份行（含已解绑的）软删并把 subject 摘要/第三方 subject 换成随机值，清空密文、hint、`provider_meta` 与密钥版本；账号行清空 `display_name` 与冻结快照、`state = DELETED`、`purge_time` 改为实际执行时刻；兜底吊销会话；清理本来源、本 namespace 下账号及关联流程审计的 actor/resource `name`、`context.client_ip/user_agent/device_id` 和 `data.accountkit.subject_hint`，保留事件事实、稳定标识和追踪关联；最后按注册顺序调用宿主 `anonymize.Anonymizer`。任一步失败整事务回滚，账号留在 `PENDING_DELETION`，下一轮重试。purge 后原手机号/微信可重新注册为新账号；已 purge 的账号不可 undelete。远端审计副本由宿主持久化清理流程处理，见下节。
- **宿主匿名化器**：实现 `anonymize.Anonymizer{Name() string; Tables() []string; Anonymize(ctx, tx pgx.Tx, userID string) error}` 并通过 `Deps.Anonymizers` 注册（`Name` 唯一；业务表须与库表同库）。宿主应仿照库内 `TestPurgeCoversEveryTableWithUserID` 写覆盖性测试：每张含 `user_id` 的业务表要么被某个 `Anonymizer.Tables()` 覆盖，要么逐表写明豁免理由。
- **维护任务**（`auth.Start` 的 ticker，多副本用 advisory lock 互斥；`auth.RunMaintenanceOnce(ctx)` 供一次性作业/测试手动跑一轮）：`purge_users`（每批 100 个账号，逐账号独立事务，批内失败记日志继续、下一轮重试）→ `cleanup_sessions`（物理删除吊销或 refresh 过期 30 天以上的会话行，每批 1000；正被刷新/吊销事务锁住的行本轮跳过，下一轮再删）。完整任务列表见下节。

## 审计落库与密钥回填（阶段 5b）

- **默认本地审计**：不注入 `Deps.Audit/AuditReader` 时，库创建 `audit.Async(audit.Store)` 并配置本地管理查询。队列容量 1024，单 worker 每 100 条或每 1 秒批量写入 `audit_event`；`auth.Start` 启动 worker，`auth.Close` 刷出残留（默认关闭等待上限 5 秒）。写失败、生成失败、队列满及关闭后到达都不改变认证结果，允许丢失事件。`Async.Stats()` 区分 Written、Duplicate、Invalid、Conflicts、Dropped、Failed；丢弃和写失败日志分别节流，只输出安全计数，不输出宿主原始错误或事件正文。
- **事件模型**：`audit.Event` 是生产事件，ID 为生产方首次投递前生成的 UUIDv7；`audit.RecordView` 增加存储首次生成的 `CreateTime`。`Actor` 用 namespace + id 表示主体，`Resource` 用 namespace + type + id 表示资源；认证字段位于 `data.accountkit`。凭证、完整手机号/邮箱、完整摘要和原始请求不进入审计。公开 Go 类型和读侧的破坏性迁移见[兼容说明](docs/compatibility.md#通用审计公开接口与协议迁移)。
- **校验职责**：审计业务内容由 Go 的 `audit/event.Freeze` 统一校验，包括字段格式、资源数量和完整事件 64 KiB 上限；默认异步入队与本地 Store 直接写入分别调用。数据库保留 UUIDv7、JSON 基本形状、非空和唯一性等基础结构，不再提供自定义审计校验函数。导入和修数必须复用 Go 校验，直接 SQL 不获得完整业务校验；其他表 CHECK 表达式保持。已有同号库不会因重复 Up 自动移除旧函数；仍使用 identity_flow 的旧库明确拒绝接管，见[迁移手册](docs/migrations.md#当前初始基线)。
- **保留期与排序**：`audit_retention` 按配置 source 删除 `occur_time` 早于 `now − AuditRetentionDays`（默认 180 天）的本地事件，每批 1000，包含没有消费者资源的匿名/管理员事件。管理时间线按 `occur_time, UUID` 升序查询，索引覆盖 source、主体及 JSONB 资源关联。
- **密钥回填**：任务 `rekey_digests` / `reencrypt_subjects` 逐批（100）把仍用非 active 版本的活跃手机/邮箱身份重算摘要 / 重加密（CAS 更新，与并发登录/绑定不冲突；查找始终覆盖全部已配置版本，回填期间登录不受影响）。单行失败只记日志（`identity_id`）、计数，下一轮重试；第三方身份与已解绑/已 purge 的行不参与。
- **启动期校验**：`auth.Migrate` 在迁移后检查 `identity` 活跃行使用的密钥版本都在配置内，否则返回 `user.ErrUnknownKeyVersion`——在回填完成前移除旧密钥会在启动时失败，而不是在第一次登录时。
- **维护任务全集**（`auth.Start` 的 ticker；`auth.RunMaintenanceOnce(ctx)` 手动跑一轮）：`cleanup_sign_in_completion_flows`（每批最多1000条到期流程，清除密文与结果）→ `purge_users` → `cleanup_sessions` → `audit_retention` → `rekey_digests` → `reencrypt_subjects`，顺序执行、失败互不影响。流程清理任务原名为 `cleanup_identity_flows`，运维指标、日志检索和告警规则须同步采用新名称。

### 宿主审计装配与清理责任

| 模式 | `Deps.Audit` | `Deps.AuditReader` | 管理审计查询 |
|---|---|---|---|
| 默认本地 | nil | nil | 库自建本地读侧 |
| 宿主读写 | 宿主 Recorder | 宿主 Reader | 使用宿主读侧并复核范围 |
| 仅写入 | 宿主 Recorder | nil | 已授权查询返回 503 `AUDIT_QUERY_NOT_CONFIGURED` |

只注入 Reader 在 `New` 时被拒绝。未配置管理员验证时仍优先返回 `503 ADMIN_NOT_CONFIGURED`；已配置读侧故障或返回不合规数据时返回安全的 `503 AUDIT_QUERY_UNAVAILABLE`，不会伪装成空列表。

`audit.Query.Actions` 是可选动作集合，空集合保留通用全部动作查询。宿主 Reader 须校验、排序去重并在分页 Limit 前执行过滤，可复用 `Query.NormalizedActions()`。管理时间线固定传入 `authentication.Actions()` 导出的认证动作集合，同表业务事件不占用认证页；外部结果仍逐条复核，含 lookahead。流程归属证据独立于此动作过滤。审计 v2 游标绑定 source、完整资源及规范动作集合，旧 v1 UUID/旧 `e_` 游标或动作范围改变均返回 400，客户端清空 token 重新开始；用户列表游标不变。

本地 Reader 按各匹配分支取时间/ID 候选，去重取页后仅读取最终页的完整事件；`Page.FlowLinks` 只提供当前页关联所需的去重流程归属，不能用它枚举账号全部历史流程。历史归属在数据库内匹配，证明查找不受页面动作或游标范围限制。资源候选和流程历史仍可能随历史量增长而增加扫描，分页不承诺恒定查询时间。

`Config.AuditSource`、`Config.AuditNamespace` 的本地默认分别是 `accountkit/<schema>`、`accountkit/<schema>/resources`，只承诺当前数据库实例内一致；外部写侧必须显式设置二者，并让生产方、存储和查询适配器复用相同配置。source 标识事件来源，namespace 标识稳定身份体系，不是角色、展示名称或租户授权；同一消费者的不同登录方式沿用本地用户映射。跨数据库汇聚时须可区分独立账号体系，持久化后更改 namespace/source 要单独迁移历史关联。三个 To C 案例见[设计说明](openspec/changes/archive/2026-10-17-generalize-audit-events/design.md#to-c-案例一单个-app-使用自建账号体系)。

[examples/embedded](examples/embedded/main.go) 演示默认模式；[examples/audit](examples/audit/main.go) 演示全部三种装配。后者使用同库 Store 演示宿主组件所有权；配置与 embedded 相同，`ACCOUNTKIT_AUDIT_MODE` 可设 `local`（默认）、`host` 或 `write-only`，后两种另需显式的 `ACCOUNTKIT_AUDIT_SOURCE` 和 `ACCOUNTKIT_AUDIT_NAMESPACE`。两个示例都只使用本地开发日志发送器，要求 `ACCOUNTKIT_DEMO=1`，实际管理查询还需装配管理员验证。宿主组件由宿主 Start/Close；先排空业务请求，再 `auth.Close()` 停止库维护，最后关闭宿主审计组件及连接。真实远端接入需宿主实现 Recorder/Reader；Record 不得阻塞业务，示例不提供可靠网络投递或跨库原子性。

外部保留期、账号及流程关联脱敏由宿主负责。宿主须有独立于可丢弃审计通知的持久化清理流程，记录待清理资源、重试状态和完成核验；同库匿名化回调可在传入事务中写入宿主自己的清理任务，不能等待远端调用或另开连接提交。仅收到 `USER_PURGED` 不能作为外部清理完成证据。外部应按完整 namespace/type/id 关联账号及流程，删除规定的展示、请求隐私和身份提示字段，并核验迟到/重放不会恢复已清理值。切换到外部模式不会自动迁移或删除旧本地副本，本地保留和匿名化任务继续承担旧副本责任；外部历史回填另作受控操作。

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
| `GET /users/{user}/auditEvents?page_size=&page_token=` | operator | 200 `{audit_events:[…], next_page_token}` | 400 `INVALID_PAGE_*`；404；503 `AUDIT_QUERY_NOT_CONFIGURED` / `AUDIT_QUERY_UNAVAILABLE` |

- **`filter`（AIP-160 子集）**：`term (AND term)*`，`term := field op value`，值为 `"…"` 或无空白裸词；时间戳值必须加双引号（含 `:`）。字段：`state = ACTIVE|FROZEN|PENDING_DELETION|DELETED`；`create_time >= <RFC3339>`、`create_time < <RFC3339>`；`identity.phone = <手机号>`、`identity.email = <邮箱>`（服务端归一化后按摘要等值查找）；`identity.phone_prefix`（如 `"+86138"`）、`identity.phone_suffix`（后 4 位）、`identity.email_prefix`、`identity.email_domain`。身份条件须同属一种 kind 且落在同一条活跃身份上；不支持 `OR` / `NOT` / 括号。`show_deleted=true` 才包含 `PENDING_DELETION` / `DELETED`（AIP-164）。
- **分页**：`page_size` 默认 20、上限 100；`page_token` 不透明 keyset 游标（用户按 `create_time, id`，审计按 `occur_time, id`）；`next_page_token` 为空表示最后一页。
- **DTO**：与 C 端分开定义；时间戳 RFC 3339（UTC）；身份默认掩码，明文只在 `:reveal` 响应中出现；审计事件的可空字段输出 `null`，result 未知/不适用为 `null`，已知结果仍为 `SUCCESS` / `FAILURE`。审计资源 name 使用 `users/{user}/auditEvents/{标准小写 UUID}`；旧审计游标返回 400，客户端清除 page_token 后重新翻页，用户列表游标保持。
- **审计**：每个写操作一条 `actor_kind=ADMIN` 事件（`admin_issuer/subject/username`、`request_id`、`ip`）：`USER_FROZEN` / `USER_UNFROZEN`（reason 为管理员填写值）、`SESSION_REVOKED`（reason `ADMIN`，冻结时为 `USER_FROZEN`）、`USER_DELETED` / `USER_UNDELETED`、`IDENTITY_REVEALED`（只记 kind + hint）。读取端点不产生事件。`ADMIN_FORBIDDEN` 由宿主经 `auth.RecordAdminForbidden` 在 verifier 的 `OnForbidden` 钩子里写入。
- **状态码语义**：管理面上 403 只表示管理员角色不足（来自 verifier）；目标账号的状态问题一律 400 `FAILED_PRECONDITION`（`INVALID_ACCOUNT_STATE` / `USER_FROZEN`）；用户/身份/会话不存在或不属于该用户 → 404。

## 数据库与 Redis 隔离

- 所有表位于 `Config.Schema`（默认 `account`），迁移记录表也在其中；同一库可并存多个实例（不同 schema）。
- 表：`user_account`、`identity`、`session`、`audit_event`、`sign_in_completion_flow`，无外键；账号/身份/会话/流程 id 使用 `u_`、`i_`、`s_`、`f_` + 13 字符 TSID，审计使用原生 UUIDv7，对外为标准小写带连字符 UUID。
- 所有 Redis 键以 `Config.KeyPrefix`（默认 `auth:`）开头。
- `Deps.Pool` 的连接 `search_path` 首位必须是 `Config.Schema`；`Migrate` 会校验并返回 `ErrSearchPath`。宿主业务表建议通过明确的 schema 限定名访问，避免 search_path 名称冲突。
- `Deps.Pool` 的 `MaxConns` 必须至少为 2；`New` 在不连接数据库的情况下拒绝更小容量。维护轮次持有一条池连接用于 advisory lock，任务查询还需要连接容量。pgx v5.10 默认上限为 `max(4, runtime.NumCPU())`，本包不覆盖该默认值；显式 `pool_max_conns=1` 的宿主须调整配置，连接池仍由宿主管理。
- 库自行开启的账号、身份和会话领域事务显式使用 `READ COMMITTED`，在既有账号/会话锁之后通过后续语句复核最新状态；宿主默认设置为 `REPEATABLE READ` 或 `SERIALIZABLE` 不改变这一内部保证。公开 `user.Repo.WithTx` / `WithTxRaw` 仍遵循宿主默认隔离；`ImportAccounts` 使用宿主传入的事务，仍要求停写导入。
- 建议对认证连接关闭 PostgreSQL JIT（`jit=off`）。本包查询均为按索引读写少量行的短事务，JIT 无法摊销其编译成本；pgx 缓存的预编译语句改用通用计划后估算代价可能偏高而触发 JIT，每次执行额外付出约数百毫秒编译时间（实测审计资源查询约 10 ms 变为约 380 ms）。范围限定在认证连接：在 `PoolConfig` 返回的配置上设置 `cfg.ConnConfig.RuntimeParams["jit"] = "off"`，或对认证专用角色执行 `ALTER ROLE <role> SET jit = off`；不要为此修改共享实例的全局配置，以免影响同实例上的分析型查询。不便关闭时可调高 `jit_above_cost`。本包不自动修改该参数；未编译 LLVM 的 PostgreSQL 不受影响。

## 配置（环境变量，前缀由宿主传给 ConfigFromEnv）

| 变量 | 默认 | 说明 |
|---|---|---|
| `ENABLED_IDENTITY_KINDS` | 省略 | PHONE、EMAIL 与已配置 IdP；显式列表非空，见身份策略 |
| `REQUIRED_IDENTITY_KINDS` | PHONE,EMAIL | PHONE/EMAIL any-of；`[]` 表示不限定类型 |
| `AUTH_SCHEMA` | `account` | PostgreSQL schema，`^[a-z][a-z0-9_]{0,62}$` |
| `AUTH_KEY_PREFIX` | `auth:` | Redis 键前缀，须以 `:` 结尾 |
| `JWT_KEYS` / `JWT_ACTIVE_KEY` | 必填 | `1:<base64>,2:<base64>` 版本化 HS256 密钥（≥ 32 字节）与签发版本 |
| `JWT_ISSUER` / `JWT_AUDIENCE` | 必填 | |
| `ACCESS_TOKEN_TTL` / `REFRESH_TOKEN_TTL` / `REFRESH_GRACE` | `15m` / `720h` / `30s` | |
| `REAUTH_MAX_AGE` / `SENSITIVE_OP_VERIFICATION` | `5m` / `true` | 敏感操作要求 `auth_time` 在此时限内 |
| `SUBJECT_HMAC_KEYS` / `SUBJECT_HMAC_ACTIVE_KEY` | 必填 | 手机/邮箱摘要密钥（HMAC-SHA256，≥ 32 字节） |
| `SUBJECT_CIPHER_KEYS` / `SUBJECT_CIPHER_ACTIVE_KEY` | 必填 | 手机/邮箱密文密钥（AES-256-GCM），每个密钥**恰好 32 字节** |
| `CODE_TTL` / `CODE_MAX_ATTEMPTS` / `CODE_COOLDOWN` | `5m` / `5` / `60s` | 验证码 |
| `CODE_FAILURE_LIMIT_PER_TARGET` / `CODE_FAILURE_WINDOW` | `10` / `15m` | 跨用途/轮次目标失败预算，自首次错误起固定窗口 |
| `CODE_DAILY_LIMIT_PER_TARGET` / `CODE_DAILY_LIMIT_PER_IP` | `10` / `100` | |
| `MAX_IDENTITIES_PER_KIND` | `1` | 每账号每 kind 身份上限 |
| `DELETION_COOLING_PERIOD` / `AUDIT_RETENTION_DAYS` | `360h` / `180` | |
| `AUDIT_SOURCE` / `AUDIT_NAMESPACE` | `accountkit/<schema>` / `accountkit/<schema>/resources` | 本地默认在 New 应用；外部写侧必须显式配置稳定值，不能从请求采信 |
| `DEFAULT_REGION` | `CN` | 无 `+` 前缀手机号的解析区域 |
| `MAINTENANCE_INTERVAL` | `5m` | 维护 ticker 周期 |
| `WECHAT_APPS` | 空（未启用） | `appid:secret,appid:secret`（微信登录） |
| `APPLE_BUNDLE_IDS` | 空（未启用） | 逗号分隔（Apple 登录，id_token 的 `aud` 须在其中） |
| `APPLE_NONCE_TTL` | `10m` | |
| `WECHAT_API_BASE_URL` | `https://api.weixin.qq.com` | 测试/代理部署钩子 |
| `APPLE_JWKS_URL` | `https://appleid.apple.com/auth/keys` | 测试/代理部署钩子 |

## 密钥轮换

三组密钥分别使用“版本列表 + active 版本”，版本编号对应的密钥材料不可改写。JWT、身份 HMAC 和身份 AES 密钥独立轮换，不要求它们的 active 编号一致。JWT 仍按原有 access 有效期要求保留旧验签版本；以下说明身份密钥及验证码的正常轮换。

1. 保持 HMAC active=K1，先将所有服务进程升级到支持必需 code_id 和多版本验证码处理的实现。
2. 向全部进程分发相同的完整 K1、K2 密钥集合，仍使用 K1；确认验证码有效期、次数、冷却和额度策略一致，再滚动切换 active=K2。配置在重新装配或服务重启后生效，不提供热加载；实例 Redis 前缀保持不变。
3. 全部已配置 HMAC 版本共同定位轮次、目标失败预算、目标冷却和目标 UTC 日额度。同一新协议内的旧 active 轮次保留标识、绑定、原到期时间、错误次数和一次性消费语义；无 code_id 的旧协议轮次不接管，新发码替换全部版本的同用途旧码；目标冷却跨用途共享，日额度合计不同物理键的实际计数。IP 日额度原本就不依赖 HMAC，每次占额仍只增加一次 IP 计数。投递失败不退还冷却或额度。
4. 记录最后一个 K1 active 进程及其在途请求、维护操作全部结束的时刻 T0，然后按下述条件退役 K1。

混合 active 期间同一手机/邮箱在不同进程下会得到不同摘要，摘要唯一索引无法互相拦截；可能新建该 subject 身份的事务（首次登录、补全、绑定、换绑、导入）因此先对 AllDigests 全部版本取事务级 advisory lock，再加任何行锁。该互斥依赖所有进程持有相同完整密钥集合，即上面第 2 步。

周期性 `rekey_digests` / `reencrypt_subjects` 回填及默认5分钟维护间隔保持原状。混合 active 期间，不同进程可能把数据库回填到各自的 active，方向仍可能变化；应在 T0 后确认实际回填结果，不能以等待一个维护周期或重启成功代替检查。HMAC 退役要求实际 `Config.Schema` 下未删除的手机/邮箱 `identity` 不再引用 K1 的 `digest_key_version`。若同时独立轮换 AES，另行确认旧 AES 的 `cipher_key_version` 引用归零；只轮换 HMAC 不要求密文也切到相同版本编号。

HMAC 还须等待旧轮次、目标失败预算、目标冷却和当日目标额度的业务窗口结束，截止为 `max(T0 + 旧 CodeTTL, T0 + 旧 CodeFailureWindow, T0 + 旧 CodeCooldown, T0 之后的下一个 UTC 零点)`。配置曾变化时使用仍可能存活记录对应的最大旧期限，并留出部署时钟偏差余量。日额度键额外1小时仅用于垃圾回收，不延长业务窗口。数据库引用与这些 Redis 条件同时满足后，才从全部运行配置移除旧 HMAC 并重新部署；AES 退役还须确认有效 sign_in_completion_flow 的 cipher_version 不再引用旧版本；等待流程原期限或完成清理后再移除。`Migrate` 缺少仍被未删除身份引用的密钥时返回 `user.ErrUnknownKeyVersion`，但它不检查 Redis 状态，启动成功不证明 HMAC 可以退役。为仍需恢复的旧备份受控保留相应历史密钥，从运行配置移除不等于立即永久销毁。

旧程序曾在不同 active 下留下多份同用途存活验证码时，无法可靠判断签发先后；新实现原子作废全部冲突码并返回原 `CODE_EXPIRED`，冷却和额度保留，用户按原限制重新发码。连续性仅适用于所有进程已升级、持有相同完整密钥集合且策略一致的正常轮换，不覆盖缺失密钥、丢失 Redis 状态或泄露密钥的紧急撤销。回退优先保留支持本能力的程序与完整密钥集合，仅切回 active 并重新计算退役窗口；切回旧程序不能承诺轮换后状态连续。实际验证与未验证项见[轮换验收记录](openspec/changes/archive/2026-10-04-harden-code-key-rotation/verification.md)。

## 测试

- 单测：`go test ./...`（快速 Redis 测试用 miniredis 内嵌，缺少真实依赖的集成用例明确 skip）。
- 集成：设置 `SERVER_TEST_DB_DSN`；轮换集成还要求 `ACCOUNTSVC_TEST_REDIS_URL` 指向一次性真实 Redis，见[开发与验证](docs/development.md)。用例各自创建随机 schema/Redis 前缀并局部清理。
- 独立构建：`GOWORK=off go build ./...`；正式发布检查：`bash scripts/verify.sh`。

## 仓库与发布状态

远程仓库：https://github.com/bbxx111/accountkit.git

Go module：github.com/bbxx111/accountkit。`v0.4.0` 已发布，使用 `go get github.com/bbxx111/accountkit@v0.4.0` 接入；旧库不能直接接管、解绑/换绑撤销、直接 Go 解绑调用及连接池容量等升级边界见[发布说明](docs/releases/v0.4.0.md)。固定提交 `62f0799` 的[完整 CI](https://github.com/bbxx111/accountkit/actions/runs/39408975386)已通过。[发布验收](openspec/changes/archive/2026-10-20-prepare-v0-4-0-release/verification.md)记录候选、tag/Release 与独立宿主远端消费结果；发布及上线要求见[检查表](docs/release-checklist.md)。`v0.1.0`、`v0.2.0`、`v0.3.0` 历史说明和 tag 保持。

库版本发布与各产品生产上线分别验收。产品实际环境接入、真实服务商与预发布升级/恢复仍待完成；产品各自配置 schema、Redis 前缀、issuer/audience 和密钥，并承担自己的匿名化与网关接入责任。当前未附开源 LICENSE，本次版本供自有产品接入，尚未向外部使用方定义复用许可。

此前流程改名已纳入 v0.3.0 的初始定义。v0.4.0 发布前，会话0002先经明确授权合入当时未发布的0001，又经用户明确批准简化为必有来源与单行JSONB描述；当前0001已随 v0.4.0 发布，此前的有限授权不再适用于修改它；同步down、baseline、固定哈希和sqlc，不新增生产0002，新安装仍为version=1/dirty=false。**旧v0.3.0 v1、前一`b414fef`双快照v1及撤回会话v2均不能直接接管**，返回不兼容错误并保留数据、结构和版本。新部署须人工核验新空资源，需保留旧数据时另行制定并验证迁移方案，不Force、改版本或自动清库。公共`Down`仍拒绝；详见[迁移手册](docs/migrations.md#会话认证依据初始整合)。所有已发布标签、其中的历史SQL和固定源快照保持，历史与当前定义分别校验，两次有限授权均不允许日常改写初始SQL。

原 migrations.Down 保留签名但默认拒绝执行；新基线的一次性测试清库使用显式 UnsafeReset。升级、故障处理与恢复步骤见 [迁移手册](docs/migrations.md)，本次边界见[兼容说明](docs/compatibility.md#初始数据库基线整合)。
