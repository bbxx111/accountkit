# Go 库集成指南

本指南面向在自己的 Go 进程中嵌入 `github.com/bbxx111/accountkit` 的宿主。可以挂载内置 HTTP handler，也可以直接调用 `auth.Users()`；两者使用同一个领域服务，不需要启动 accountsvc。

内容依据 v0.5.0 公开契约，包含 v0.2.0 未提供的登录补全和通用审计 API、v0.3.0 未提供的当前会话、按来源撤销和客户端描述，以及 v0.4.0 后新增的资料属性和头像。实际集成应固定包含所需接口的已验证版本；不要把新签名直接套到旧发布。升级边界见[兼容指南](compatibility.md)。

## 基础资料与头像

完成认证、scope 与业务授权后，调用 `auth.Users().GetMe(ctx, userID)` 读取 `DisplayName` 和 `AvatarURL`；头像未设置返回空字符串。修改示例：

```go
name := "新显示名"
avatar := "https://cdn.example.com/avatar.png"
me, err := auth.Users().UpdateProfile(ctx, userID, user.ProfilePatch{
    DisplayName: &name,
    AvatarURL:   &avatar,
})
```

指针 nil 保持字段，指向空字符串清空；至少提供一个字段。更新在账号行锁内确认 ACTIVE 并原子保存，任一字段非法则整次拒绝。显示名最多 128 个 Unicode 码位。此前把 32 写死在客户端校验里的宿主需要接受 128。错误沿用 `ErrInvalidArgument`、`ErrNotFound`、`ErrInvalidState`；头像校验边界见 [HTTP 契约](openapi.md#基础资料与头像)。`UpdateDisplayName` 保留既有签名和行为，新代码需要原子更新或锁后状态检查时使用 `UpdateProfile`。

`BatchPublicProfiles` 返回 ID、DisplayName、AvatarURL 和 State；宿主先按业务权限过滤 ID，待注销和已删除账号的显示名与头像均为空。图片文件的上传、公开访问、默认图及删除由宿主管理；头像清空只清除账号中的引用。

自定义 `enduser.Service` 仍满足原必需方法集；实现可选 `enduser.ProfileUpdater` 的 `UpdateProfile` 方法即可支持头像。仅实现旧方法时，原显示名 PATCH 继续工作，含头像的 PATCH 返回 `503 PROFILE_UPDATE_NOT_CONFIGURED`，不会先修改显示名。

## Google 登录

配置 `GoogleClientIDs` 后，`SignInWithIdp` 接受 `IdpCredential{Kind: enum.IdentityGoogle, Credential: json.RawMessage(`{"id_token":"…","nonce":"…"}`)}`；`Credential` 是客户端 credential 对象的原始 JSON，由对应提供方严格解析。nonce 是客户端原始字符串。已验证邮箱出现在成功 `TokenResult.HintEmail` 或 pending `PendingSignIn.HintEmail`，补全时仍要传入明确的 `target`。`GOOGLE` 不能放入 `RequiredIdentityKinds`。只启用 Google 且 Required 为空时，不要求短信或邮件发送器。

`name` 和 `picture` 记在该身份的 `provider_meta.profile`。账号 `display_name`、`avatar_url` 只在该身份首次关联且对应列仍为空时填充。身份列表和 `:reveal` 不返回 `provider_meta`。宿主继续负责头像文件、默认图和后续同步。未配置客户端 ID 时 Google 登录保持关闭，配置里没有客户端秘密。

## 当前会话、解绑与客户端描述

使用Authenticate获取完整可信Principal，再调用auth.Users().GetCurrentSession(ctx,p)。user/user:bind可查询，user:undelete不允许。结果提供数据库最新AuthTime和非空AuthenticationIdentities；HTTP转换的name是users/{user}/identities/{identity}。与拟解绑身份的完整资源名精确匹配，不能只比较Kind。来源缺失或失效是内部错误，不返回正常空集合，不从其他身份或审计补造。重新认证成功后用新access重新Authenticate并重查；查询不会改写旧JWT或自动给它新的近期认证证明。

[可编译示例](../examples/gointegration/example_test.go)展示重新认证后重查及成功解绑后的凭据清理。UnbindIdentity要求可信Principal包含UserID、SessionID、ScopeUser和AuthTime，并在领域层复核账号、会话和按配置的近期认证；仅填写UserID的旧调用须迁移。204成功语义不变，命中任一来源的同账号未吊销会话会撤销，包含当前和自然过期但未清理的会话；同账号任一未吊销会话缺少有效来源时整事务报内部错误并回滚。后续实际认证失效应清理凭据。来源不命中只是快照，提交时可能并发变化。重新认证和换绑也检查当前来源；换绑只在来源非空且不含旧身份时保留当前会话，新BIND证明不替换来源。

直接Go请求元数据由宿主显式传入，HTTP回调不会自动用于Go调用：

```go
meta := user.Meta{IP: trustedIP, RequestID: requestID, ClientInfo: &user.ClientInfo{
    ClientType: enum.ClientNative, AppName: "Example App", AppVersion: "1.0",
    DeviceManufacturer: "Example", AppBuild: "100",
}}
```

新增公开结构字段可能使位置式初始化无法编译，请使用上述字段名写法。内置HTTP默认解析显式头、Client Hints和UA，宿主可通过accountkit.Deps.ClientInfo / enduser.Deps.ClientInfo的func(*http.Request) (*user.ClientInfo,error)替代；回调结果仍规范化校验，ErrInvalidClientInfo为400，任意其他回调错误固定500且不保留原文本。宿主回调也不得记录私密输入。不无条件信任转发UA；BFF明确适配原客户端描述，可信IP仍由ClientIP提供。

Go保留ClientInfo平坦字段，新增DeviceManufacturer、AppBuild及`Extensions map[string]json.RawMessage`（使用扩展时导入`encoding/json`）。`NormalizeClientInfo`规范化本次观察并深拷贝，nil/空描述返回nil；`MarshalClientInfo`和`UnmarshalClientInfo`采用README中的nested JSON，供业务存储读写校验，拒绝未知核心字段、重复键、非法类型/枚举、UTF-8和控制字符。完整原始JSON和每个RawMessage解析前各限16KiB，完整规范化JSON至多2048字节。合法指数数值精确展开为十进制，先核算长度再有界分配；展开后的完整JSON仍须满足2048字节，不使用浮点转换。空描述不写存储；读取非法已存描述应报内部错误。不要直接JSON序列化平坦Go模型充当公开DTO。

扩展允许JSON primitive/null/object/array；所有层级键匹配`[a-z][a-z0-9_]{0,63}`。根扩展对象不计节点/深度，直接值深度1，值、容器和数组元素各计一节点，最多64节点、4层、每数组16项、每字符串256个Unicode字符；根map超过64项在分配规范化副本前拒绝。固定拒绝的下划线分隔词段为`password`、`passwd`、`secret`、`token`、`credential`、`credentials`、`authorization`、`cookie`、`cookies`、`captcha`、`otp`、`ip`、`address`、`location`、`latitude`、`longitude`、`imei`、`mac`、`ua`；同时拒绝完整组合键或以`_`为边界出现的`user_agent`、`device_id`、`hardware_id`、`verification_code`、`sms_code`、`email_code`、`one_time_code`、`security_code`。这些固定规则不识别普通值中夹带的任意隐私；宿主须管理来源、扩展语义和隐私白名单，不记录私密回调输入。默认HTTP不接收扩展JSON，也不新增制造商/build显式头。

只有成功登录（含补全）、重新认证和新轮刷新采集描述。有非空观察时整体替换唯一一份描述；nil/空观察不写入且保留旧值和时间。版本/build单独出现合法，app/browser可并存；`MergeClientInfo`仅供同一请求UA→Hints→显式值overlay，不用于跨请求持久化合并。pending完成本次非空描述优先，否则整体沿用加密首次描述和原观察时间；失败、事务回滚及结果重放不更新。旧客户端可缺省，描述不作为授权或可信设备证明。完整字段和时间边界见[README](../README.md#当前会话与客户端描述)，上线和回退见[迁移手册](migrations.md#会话认证依据初始整合)，当前验收状态见[简化记录](../openspec/changes/archive/2026-10-20-simplify-session-context/verification.md)。

## 选择入口

| 需求 | 入口与宿主责任 |
|---|---|
| 直接提供内置账号 HTTP API | 挂载 `auth.EndUserHandler()`；宿主负责监听、TLS、可信代理、入口限流和业务路由 |
| 使用自己的 HTTP/RPC 协议或后台业务入口 | 调用 `auth.Users()`；宿主负责认证、scope、业务授权、输入/错误和 DTO 转换 |
| 提供账号管理 API | 独立挂载 `auth.AdminHandler()`，先完成管理员认证，成对注入 `AdminVerifier` / `AdminPrincipal` |
| 创建与账号关联的业务数据 | 完成业务授权后用 `WithActiveUsers`，同事务确认账号 ACTIVE 并写入业务表 |

优先通过根门面装配；`user.NewService` 是较低层入口，需要自己提供密码学、仓储、策略和审计等依赖，不适合作为首次接入的起点。不要直接读写 `user/db` 生成模型来绕过领域状态机。

## 装配、启动与关闭

完整开发宿主位于 [examples/embedded/main.go](../examples/embedded/main.go)，直接 Go 调用片段位于 [examples/gointegration/example_test.go](../examples/gointegration/example_test.go)。后者无 `Output` 的函数只参与编译，不实际连接依赖；变量声明处需替换为宿主真实对象。带 `Output` 的示例使用合成数据验证分支和错误判定。

1. 使用 `ConfigFromEnv("ACCOUNTKIT_")` 或显式 `Config` 加载配置。前缀由宿主决定；数据库 DSN、Redis 地址和发送器配置由宿主读取，不属于核心 `ConfigFromEnv` 的连接配置。
2. 用 `PoolConfig(dsn, cfg.Schema)` 创建 pgx 连接配置，再创建池。显式构造 `Config` 时先填入所需 schema（默认名称为 `account`）；不能向 `PoolConfig` 传空 schema。查询与迁移依赖 search_path 首项为该 schema。池的 `MaxConns` 至少为 2，默认 pgx 容量满足要求；`New` 会无 I/O 拒绝单连接池。建议同时在该配置上设置 `RuntimeParams["jit"] = "off"`，原因与范围见 [README](../README.md#数据库与-redis-隔离)。
3. 创建 Redis 客户端和发送器，注入 `Deps`。按部署设置独立 schema、以冒号结尾的 Redis 前缀、issuer、audience 和密钥。
4. 调用 `New(cfg, deps)`，完成默认值与依赖校验；此步不做 I/O。使用独立启动超时调用 `Migrate`，它检查 PostgreSQL、Redis、迁移状态及历史密钥；失败时停止启动。
5. 调用 `Start`，使用服务存活期间的 context，随后才接受请求。不能传一次 HTTP 请求的 context，也不能传马上会取消的迁移超时 context。
6. 关闭时先停止接入并排空所有 HTTP/RPC/后台调用，再 `Auth.Close()`，再关闭宿主拥有的审计组件，完成最终指标采集/刷新并关闭宿主观测设施，最后关闭 Pool、Redis 等共享依赖。`Auth.Close()` 不关闭宿主连接池、注入的审计或观测组件。

HTTP `Shutdown` 超时不意味着在途请求已完成。生产宿主应制定明确的排空与总退出预算，不能让仍在运行的调用使用已经关闭的依赖。`Start`/`Close` 具有幂等保护，但已关闭的实例不能重新启动，也不应再调用维护入口。

`Migrate` 不是通用旧库接管工具。旧初始定义、dirty 状态和不兼容历史必须按[迁移手册](migrations.md)处理，不自动 Force、改版本或清库。

可选指标通过 `accountkit.Deps.Observer` 注入；直接领域服务使用 `user.Deps.Observer`，均允许 nil/typed nil 并默认 no-op。宿主选择 OpenTelemetry 或原生 Prometheus，显式创建 Provider/Registry，提供固定非秘密实例名，负责导出与指标端点的网络访问控制。装配、指标口径和合成示例见[指标指南](metrics.md)；不要把合成采集称为真实认证联调。

## 身份策略与发送器

默认 `RequiredIdentityKinds` 是 PHONE/EMAIL 二选一，`nil` 与显式空列表含义不同。邮箱单渠道部署可显式配置：

```go
cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
cfg.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
// deps.EmailSender 必须提供；此策略无需 deps.SMSSender。
```

`RequiredIdentityKinds: []enum.IdentityKind{}` 允许纯第三方完整账号；仍须至少有一个已启用身份，并装配相应 IdP。Required 是账号完整性规则，不是每次登录都验证多个因素。配置在装配时固定，变更策略或实例配置可能使旧补全流程失效。

发送器实现 `sender.SMSCodeSender` / `sender.EmailCodeSender`，参数为 `sender.CodeMessage`。可用[SMTP 与渲染示例](../examples/codeemail/example_test.go)，或在宿主适配服务商模板；语言、模板 ID 和服务商凭据属于宿主。日志发送器仅用于明确启用的本地开发，不能用于生产。真实送达仍需单独联调。

## 验证码登录与登录补全

发码和提交验证码是两个客户端交互阶段：

1. `SendSignInCode(ctx, channel, target, meta)` 返回 `CodeChallenge{CodeID, ExpireTime}`。将轮次和期限返回客户端，验证码通过发送器送达。
2. 客户端提交同一目标、轮次和用户输入的验证码，宿主构造 `CodeCredential{Channel, Target, CodeID, Code}`，并调用 `SignInWithCode(ctx, cred, device, meta)`。
3. 检查错误后再处理 `SignInResult`：`Tokens` 与 `Pending` 恰好一个非 nil。第三方 `SignInWithIdp` 返回相同的结果类型。

| 结果 | 后续操作 |
|---|---|
| `Pending != nil` | 尚未创建账号、身份、会话或 token pair；保存短期补全票据，展示启用且符合要求的 PHONE/EMAIL 证明选项 |
| `Tokens.Scope == user.ScopeUser` | 正常业务；仍需业务授权与资源归属校验 |
| `Tokens.Scope == user.ScopeBind` | 已有账号缺少策略要求的身份；通过该账号的绑定流程补齐，再刷新获取新 scope |
| `Tokens.Scope == user.ScopeUndelete` | 注销冷静期账号；取得用户恢复意愿后走 `Undelete`，再刷新；不能进入普通业务 |

pending 与 `user:bind` 是不同状态。补全先调用 `SendSignInCompletionCode`，再用它返回的新轮次调用 `CompleteSignIn`，不能复用普通登录、绑定或 REAUTH 验证码。目标归属活动账号时可能关联本次尚未占用的身份，无归属且满足策略时注册；界面须说明这一含义，两个已有账号不会自动合并。

初次登录必须传 `Device.ID`；同设备重登会替换旧会话。补全可以省略设备字段，重复提供必须与初次相同。直接 Go 调用由宿主构造 `Device`，内置 HTTP 使用 `X-Device-Id` / `X-Device-Name`。

票据最多五分钟且不续期，只能用于补全。相同完成请求在原期限及账号/会话/refresh 仍有效时可以取回相同结果；refresh 已轮换返回 `ErrSignInAlreadyCompleted`。不能无限重试、把票据当 access/refresh 或凭第三方邮箱提示自动绑定。验证码消费与 SQL 不构成跨存储事务；SQL 失败后验证码不会恢复，应根据实际错误和已有流程状态决定是否重新取证。

## 认证、刷新与敏感操作

直接 Go 调用时先通过 `Authenticate(ctx, rawAccess)` 获取可信 `Principal`，不要从客户端提交的 user_id、session_id、scope 或 auth_time 构造它。认证成功后仍须检查允许的 scope，以及业务资源所属用户；不要把 `GetMe`、`BatchPublicProfiles` 或账号存在性检查视为授权。

`Authenticate` 校验 JWT、资源 ID 格式和 Redis 吊销集，不为每次请求查询 PostgreSQL 中的当前账号状态。吊销查询故障按既有策略 fail-open 并告警；需要防止冻结/注销并发穿透的业务写入，使用 `WithActiveUsers` 同事务复核 ACTIVE。宿主入口限流和可信 IP 解析仍由宿主完成；直接调用时必须自己传 `Meta`，`Deps.ClientIP` / `RequestID` 不会自动作用于 Go 方法。

刷新成功必须原子替换整个 token pair。宿主应协调同会话的并发刷新及持久化，防止旧响应覆盖新令牌；默认宽限内重复刷新返回相同 pair，宽限外重放执行原防重放处理。遇到 `ErrInvalidGrant` 不应无限重试；Redis 宽限故障也可能返回该错误，不能仅凭它断言发生攻击。

手机/邮箱近期认证先使用 `SendReauthenticationCode` 获取绑定用户与会话的轮次，再调用 `Reauthenticate`。它只返回新 access，不返回新 refresh，也不延长原 refresh 的绝对有效期。只更新 access、其期限与 scope，保留现存 refresh。

直接调用 Go 方法不会执行 HTTP 中间件。`DeleteMe` 的 scope/近期认证保护位于内置 HTTP 路由，直接调用前宿主必须检查 `ScopeUser`，并按 `auth.Config()` 中的 `SensitiveOpVerification` / `ReauthMaxAge` 校验可信 `Principal.AuthTime`；不能等待它自动返回 `ErrReauthenticationRequired`。开启近期认证时，AuthTime 为零或距当前时间超过窗口均应拒绝并引导重新认证。`Undelete` 必须先检查专用 `ScopeUndelete`，普通 user 凭据不能替代。v0.4.0 起 `UnbindIdentity` 与 `ReplaceIdentity` 在领域层也复核 scope、近期认证和所属有效当前会话；仅UserID的Principal不能解绑。自有敏感业务同样需要这些入口检查；HTTP 宿主可组合 `RequireScope` 和 `RequireRecentAuth`。

纯第三方账号当前通过普通 IdP 重登获得新的认证状态：核对返回的账号仍是原账号，再更新完整 pair；这会替换同设备会话，且不保证提供方强制输入密码或 MFA。纯第三方冷静期账号没有第三方自助恢复入口。

## 业务事务与匿名化

| 接口 | 事务由谁管理 | 使用边界 |
|---|---|---|
| `WithActiveUsers(ctx, ids, fn)` | 库创建、提交、回滚 | 非空 ID 集合，先排序锁账号并确认 ACTIVE，再在 `fn` 中锁业务资源并写入 |
| `Deps.BeforeDelete` | 库的注销事务 | 消费者和管理注销共用；返回 `ErrDeletionBlocked` 拒绝，其他错误使事务失败 |
| `Deps.Anonymizers` | 库的到期匿名化事务 | 所有回调使用同一 `pgx.Tx`，任一失败则认证表与业务表一起回滚 |
| `ImportAccounts(ctx, tx, accounts)` | **宿主** | 受控停写导入，宿主开始事务；任一错误回滚整批，成功后连同业务引用一起提交 |

回调必须使用传入的 `pgx.Tx`，不能自行提交/回滚、改用池另开连接或在事务中等待远端清理。业务表必须与认证表同库，可使用宿主自己的 schema；示例不向 accountkit 迁移中添加业务表。

`WithActiveUsers` 保证状态与写入的并发一致性，不负责业务授权。宿主先校验调用者是否有权为这些账号创建关联，再进入事务；空业务批次直接跳过。不能在持有业务资源锁后反向请求账号锁。

匿名化器的 `Name` 唯一，`Tables` 列出覆盖表；宿主须检查每张 user_id 关联表均已覆盖或明确豁免，不能把声明列表当作已验证覆盖。外部审计、搜索索引或其他远端副本须有宿主持久化清理、重试和完成核验；可以在传入事务中写宿主清理任务，不能以尽力 `USER_PURGED` 审计作为唯一完成证据。

导入接受 ACTIVE（有 PHONE/EMAIL 锚点）或 DELETED 墓碑，不自动合并、不创建会话或令牌。它是 Go 管理工具契约，不应直接包装为任意客户端可调用的导入 API。详见[离线导入边界](compatibility.md#离线账号导入)。

## Go 错误处理

用 `errors.Is` 判断哨兵错误，用 `errors.As` 读取结构化限流信息，不匹配错误字符串。`code.RateLimitedError` 包含 `Dimension` 和 `RetryAfter`；宿主可以转换为自己的协议，但不要丢失重试等待时间。

| 错误 | 宿主可采取的处理 |
|---|---|
| `code.ErrInvalid` / `code.ErrExpired` | 提示验证码错误或轮次失效；保留尝试限制，不当作 access token 失效 |
| `*code.RateLimitedError` | 按 `RetryAfter` 等待，不自动循环发码 |
| `user.ErrInvalidToken` | 当前 access 认证失败；按客户端会话策略刷新或重新登录 |
| `user.ErrInvalidGrant` | 刷新失败，停止无界重试，按客户端策略重新认证 |
| `user.ErrInsufficientScope` / `ErrReauthenticationRequired` | 分别引导权限对应流程或近期认证 |
| `user.ErrIdentityConflict` / `ErrDeletionBlocked` | 呈现明确业务冲突/拒绝，不自动合并身份或强制注销 |
| `user.ErrUnavailable` | 依赖不可用，返回安全错误；不回退到弱校验 |
| `user.ErrInvalidArgument` / `ErrInvalidTarget` / `ErrNotFound` | 分别作为输入错误或资源不存在处理；不暴露其他用户资源的存在性 |
| 已知账号状态、身份策略、IdP 或补全错误 | 按具体方法区分业务拒绝、重新取证或依赖故障；不能都变成 401 或 500 |
| 未识别的内部/基础设施错误 | 对外固定内部错误，内部仅记录经脱敏的必要诊断和关联 ID |

直接序列化 `SignInResult` 或 `TokenResult` 不会自动产生内置 HTTP 协议。自定义 HTTP 层应定义自己的 DTO；若要求兼容内置 API，必须保留标准 token 字段、pending 分支、OAuth token 错误及 Bearer challenge。不要输出原始 `err.Error()`、数据库 Detail、验证码、票据或完整目标。

## 管理面、审计与验证

管理员与消费者是独立身份体系。挂载管理面时，宿主中间件验证管理员身份，注入角色检查与可信主体；角色拒绝钩子接入 `RecordAdminForbidden`。直接调用管理领域方法同样是可信宿主边界，不能将客户端自报的管理员身份当作授权。未配置管理认证不影响消费者能力。

默认本地审计由库启动和关闭；注入外部 `Audit` 时显式设置稳定的 `AuditSource` / `AuditNamespace`，需要管理查询则同时提供 `AuditReader`。仅写侧模式的查询返回 503，不读本地旧副本；宿主仍负责旧副本及外部副本治理。三种装配见[审计示例](../examples/audit/main.go)。

在仓库根目录验证示例：

```powershell
$env:GOWORK = 'off'
go build ./examples/...
go test -count=1 -v ./examples/gointegration
go vet ./examples/...
```

这些检查验证示例类型与合成输出，不等于数据库事务、真实发送器或产品环境联调。实际宿主还应验证启动失败、token/pending 分支、刷新协调、业务授权、回调回滚与匿名化覆盖；库完整门禁见[开发与验证](development.md)。
