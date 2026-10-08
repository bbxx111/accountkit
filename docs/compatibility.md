# 功能与兼容对照

固定兼容基线包含 130 个文件，原始文件清单与 SHA-256 见 `tests/testdata/source-baseline/manifest.json`；该清单用于追溯冻结快照，不表示当前文件名和内容必须与历史快照相同。当前根包名为 `accountkit`，导入路径为 `github.com/bbxx111/accountkit`。

## 开发版默认 schema 调整

`v0.2.0` 在 `Config.Schema` 未指定或为空时采用 `account`；`v0.1.0` 的默认值为 `auth`。升级已有实例须显式保留原 schema。显式合法 schema（包括 `auth`）继续按配置使用，宿主通过 `Config.Schema` 或带宿主前缀的 `AUTH_SCHEMA` 设置。库与 accountsvc 共用默认值，连接池仍须通过 `PoolConfig` 配置匹配的 search_path。环境变量名称 `AUTH_SCHEMA` 和 Redis 默认前缀 `auth:` 保持不变。

## 验证码轮次契约升级

`v0.2.0` 的 `add-host-account-contracts` 修改 Go、HTTP 和短期 Redis 验证码格式；以下能力已在该固定版本发布，不属于 `v0.1.0` 接口。发布状态和本轮实际检查见[验收记录](../openspec/changes/add-host-account-contracts/verification.md)。JWT、refresh、身份密文、数据库结构、冻结迁移和发送器签名保持。

| 旧调用 | 新调用 |
|---|---|
| 三类 `Send*Code(...) error` | 三类 `Send*Code(...) (user.CodeChallenge, error)`；保存 `CodeID` 和 `ExpireTime` |
| `SignInWithCode(ctx, channel, target, code, dev, meta)` | `SignInWithCode(ctx, user.CodeCredential{Channel, Target, CodeID, Code}, dev, meta)` |
| `BindWithCode(ctx, p, channel, target, code, meta)` | `BindWithCode(ctx, p, cred, meta)` |
| `Reauthenticate(ctx, p, channel, target, code, meta)` | `Reauthenticate(ctx, p, cred, meta)` |
| `ReplaceIdentity(ctx, p, identityID, channel, target, code, meta)` | `ReplaceIdentity(ctx, p, identityID, cred, meta)` |
| PHONE/EMAIL `{target, code}` | `{target, code_id, code}`；`code_id` 必需，为 32 位小写十六进制 |
| 发码成功 `{}` | `{code_id, expire_time}`，RFC3339 时间，`Cache-Control: no-store` |

自定义 `enduser.Service`、可选 `enduser.IdentityReplacer`、嵌入示例、测试 fake 和服务客户端都须同步迁移。用途由服务器方法决定；BIND 固定用户，REAUTH 固定用户/会话。合法但未知、被替换、已消费或绑定不符的标识为 400 `CODE_EXPIRED`；缺失/非法标识为 400 `INVALID_ARGUMENT`。错误验证码仍为 400，不能触发凭据失效的 401 处理。

每轮次数仍默认 5，3 次策略由宿主显式配置。新增 `CodeFailureLimitPerTarget`/`CodeFailureWindow`，环境键为带宿主前缀的 `CODE_FAILURE_LIMIT_PER_TARGET`/`CODE_FAILURE_WINDOW`，默认 10/15m。同渠道归一化目标跨用途、重发轮次累计错误，从首次错误起固定窗口；重发和成功不清预算。达到上限为 429 `RESOURCE_EXHAUSTED`、reason `TARGET_VERIFY_LIMIT`、剩余 `Retry-After`。旧标识不扣新轮次或目标预算，宿主仍需接口/IP 限流。

达到每轮次数上限的错误请求返回 400 `CODE_INVALID` 并立即作废轮次，之后该标识返回 `CODE_EXPIRED`；第三次正确仍可成功。若同时达到目标累计上限，第十次错误本身返回 429 `TARGET_VERIFY_LIMIT`。旧 `code.ErrExhausted` 符号和 HTTP 兼容映射保留供自定义实现使用，但库的新轮次协议不再产生 `CODE_ATTEMPTS_EXHAUSTED`，调用者须同步更新该边界处理。

升级须逐账号实例安排停写窗口：停止全部旧验证码发码和校验进程，同步切换 Go/HTTP 调用者，再启用新实例。旧无标识轮次不接管，按原 TTL 淘汰；不要清空整个 Redis，身份、会话和吊销状态保留。无法同时切换的其他独立实例可另行安排，不允许同一实例新旧协议混跑。

全部进程支持新协议且持有相同完整密钥集合后，才执行正常 HMAC active 轮换。轮次、发送冷却、日额度和目标失败预算保持原期限和次数，同密钥材料别名不重复计数。退役除数据库摘要引用外，还须等旧轮次、冷却、日额度和失败预算窗口结束。回退须停止新验证码写/校验、同步回退调用者并等待新短期状态失效，核对新增错误值和存储兼容；只回退二进制或清 Redis 不能保证安全恢复，见[密钥轮换](../README.md#密钥轮换)。

## 宿主账号契约

`BatchPublicProfiles(ctx, ids)` 是 Go 批量查询，结果仅含 ID/DisplayName/State。输入去重，非法 ID 返回参数错误，未知账号略去，待注销及已删除账号清空显示名；宿主在调用前按业务权限过滤 ID。无任意用户资料 HTTP 端点。

`Deps.BeforeDelete` 在账号锁和状态检查后、改变状态前使用同一 `pgx.Tx`，消费者和管理员共享。返回 `user.ErrDeletionBlocked` 为 400 `FAILED_PRECONDITION`、reason `DELETION_BLOCKED` 和固定消息；其他错误为固定 500，同批账号、会话和业务写入回滚。其他回调错误不进入库的业务错误分类，不能通过返回旧 `ErrNotFound` 等哨兵变成 404；调用者不要依赖对这些底层错误的 `errors.Is`/`errors.As`，明确业务拒绝使用 `ErrDeletionBlocked`。

`WithActiveUsers(ctx, ids, fn)` 要求非空账号集合和非 nil 回调，先验证/去重/排序锁定 ACTIVE 账号，再执行宿主回调；锁序为账号后业务资源。库拥有提交/回滚，回调不得自行结束事务；回调错误按原事务契约返回，panic 回滚并向上传递。未知或非 ACTIVE 账号拒绝，回调不运行。

## 离线账号导入

`ImportAccounts(ctx, tx, accounts)` 只用于受控停写工具，事务来自 schema 匹配的认证连接池。库不另开连接、不提交、不回滚；调用者负责空目标核验、批次记录、重复执行、业务引用迁移和最终提交。发生任何错误时必须回滚整个批次，不继续提交部分成功数据。

`user.ImportAccount` 保留合法 `u_` ID 和原创建/更新时间；账号和锚点创建/更新时间必需非零，允许历史时钟回退，不强制更新时间晚于创建时间。ACTIVE 至少包含一个 `user.ImportAnchor` 的 PHONE/EMAIL 合法目标，删除/匿名化时间为空；DELETED 只允许无显示名、无锚点的匿名墓碑，可保留删除/匿名化时间，不重新抢占旧地址。FROZEN、PENDING_DELETION、第三方身份和非法目标拒绝。归一化和版本化加密/摘要沿用库配置；重复 ID 或归一化身份冲突失败，不合并、不静默跳过。

导入错误的 `Error()` 文本脱敏，唯一冲突可用 `errors.Is(err, user.ErrIdentityConflict)` 分类，`errors.As` 仍能获取数据库诊断。解包后的诊断可能包含原始详情，不能直接写入日志或对外输出。

此入口不发验证码、不创建会话、不签令牌、不调用 IdP，也没有导入 HTTP 路由。宿主停写、引用迁移和批次幂等在宿主工具中完成，不属于 accountkit 的认证行为。

## 包名迁移

根包已从 `authserver` 重命名为 `accountkit`，module 路径不变。使用默认导入的调用方将 `authserver.Config`、`authserver.New` 等引用改为 `accountkit.Config`、`accountkit.New`：

```go
import "github.com/bbxx111/accountkit"

var cfg accountkit.Config
```

若暂不调整调用点，可显式指定旧别名 `import authserver "github.com/bbxx111/accountkit"`；显式别名不要求实际包声明使用同一名称。`Auth`、`Config`、`Deps` 等公开类型和方法签名保持不变，包级诊断文本前缀改为 `accountkit:`，调用方应使用 `errors.Is` 判断哨兵错误。

此次重命名不改变 HTTP 路由和错误码、环境变量键、数据库结构、Redis 键、令牌或密文格式，无需迁移数据或重新登录。维护任务继续使用历史 advisory lock 标识，以保证新旧版本进程互斥；冻结 SQL 和原始 manifest 保持原样。

accountsvc 是基于本库的可选官方服务；直接嵌入 accountkit 的宿主无需部署或调用该服务。服务配置与匿名化责任见 [服务手册](accountsvc.md)。

## 终端用户 HTTP 入口迁移

终端用户 HTTP 适配包由 `consumer` 直接更名为 `enduser`，根门面改为 `Auth.EndUserHandler() http.Handler`。这是 **Go 源码破坏性变更**：旧导入路径与 `Auth.ConsumerHandler()` 已移除，不提供兼容包、类型别名或转发方法。根 module、package `accountkit` 和领域包 `user` 保持不变。

| 旧导入/调用 | 当前导入/调用 |
|---|---|
| `github.com/bbxx111/accountkit/httpapi/consumer` | `github.com/bbxx111/accountkit/httpapi/enduser` |
| `consumer.New` / `consumer.Deps` / `consumer.Handler` | `enduser.New` / `enduser.Deps` / `enduser.Handler` |
| `consumer.Service` / `consumer.IdentityReplacer` | `enduser.Service` / `enduser.IdentityReplacer` |
| `(*consumer.Handler).Router` / `AuthnOptions` / `RequireRecentAuth` | `(*enduser.Handler).Router` / `AuthnOptions` / `RequireRecentAuth` |
| `auth.ConsumerHandler()` | `auth.EndUserHandler()` |

宿主更新依赖时同步修改导入路径、包限定符和门面调用，重新编译并运行原接入测试。上述类型内容和方法签名保持不变，自定义 `enduser.Service` 无需新增方法，换绑仍由可选 `enduser.IdentityReplacer` 提供。显式 Go 导入别名可自行选择，但旧导入路径本身不能继续使用。构造及内部运行诊断前缀由 `consumer:` 改为 `enduser:`；HTTP 错误码与响应正文保持原样。

HTTP 调用方继续使用原 URL（包括 `/users`）、DTO、设备头、JWT 和刷新凭据；环境配置键、数据库与 Redis 数据、实例前缀和密钥均不变，无需数据迁移、会话清理或重新登录。相对路由仍由宿主自行挂载，accountsvc 的 `/v1`、`/admin/v1`、`/v1/introspect` 及监听/TLS 行为不变。

回退到旧库版本时同步恢复旧依赖、Go 导入和门面调用并重新编译，无需回退配置或数据；不执行 `Down`、`UnsafeReset` 或清空 Redis。下方明确标注的历史提取索引保留旧路径、方法名与行号，不代表当前可用入口。

## 可选服务与发送器扩展

新增 `cmd/accountsvc`、可复用 SMTP 子包及内部消费者内省。SMTP 采用显式 TLS 与有限投递预算，短信在服务首版中显式禁用。库的 `SMSSender`/`EmailSender` 原接口不变；原发送器默认启用。只有显式禁用发送器的宿主会在发码前得到 400 `CHANNEL_NOT_ENABLED`，该拒绝不消费额度；SMTP 不可用沿用503，已产生的发送额度不退还。已存在验证码的校验及其他认证行为不变。

消费者内省沿用当前吊销查询 fail-open；管理员适配位于服务内部，不给消费者增加管理员身份。无数据库迁移、默认库配置变化或令牌格式变化。独立服务不执行宿主业务匿名化回调，不能与依赖此回调的宿主混跑同一实例的维护任务。

## 服务单监听与传输迁移

本次 accountsvc 部署接口为破坏性直接迁移：消费者、管理员、内省与探针使用一个监听地址，独立认证链保持。根库 Config/Deps、嵌入式生命周期、SQL、数据库结构、令牌和业务额度契约不变，无数据迁移或重新登录要求。服务通用入口限流和网络隔离由部署方配置，规则见[网关接入手册](gateway-integration.md)。

| 旧配置/调用 | 新配置/调用 |
|---|---|
| `ACCOUNTSVC_HTTP_ADDR` 与 `ACCOUNTSVC_INTERNAL_ADDR` 双监听 | 仅 `ACCOUNTSVC_HTTP_ADDR`，默认 `127.0.0.1:8080`；删除所有 INTERNAL_ADDR 注入，任意非空旧值使 serve 报错 |
| Docker 暴露8080/8081，Compose `ACCOUNTSVC_PUBLIC_PORT=18080` / `ACCOUNTSVC_INTERNAL_PORT=18081` | Docker仅8080，Compose保留 `ACCOUNTSVC_PUBLIC_PORT=18080` 为统一映射；删除旧内部端口映射和 `ACCOUNTSVC_INTERNAL_PORT` 模板项 |
| `POST /internal/v1/introspect` | 统一地址的 `POST /v1/introspect`；旧路径404，无别名、重定向或客户端自动回退 |
| 内部监听上的 `/healthz`、`/readyz` | HTTP_ADDR 同址探针，协议随有效 TLS 模式改变；同步编排探针和网关摘流 |
| production 强制服务 HTTPS | 未设/空 TLS_ENABLED 仍沿用旧默认；true 强制完整有效证书；false 显式 HTTP 且与任一 HTTP 证书项冲突 |
| 内省调用方只使用旧内部 HTTPS 地址 | 更新端点地址；HTTPS校验证书，受控 HTTP 应用段必须由调用方显式设置 AllowHTTP=true |

部署前保存旧二进制、服务环境注入、证书、调用方 URL/AllowHTTP、探针和网关配置，作为成套回退材料。先清点全部内省调用与探针，再准备明确公网路由允许范围、阻断内省和探针、限制后端直连。不能因新内省在 `/v1` 下而扩大公网 `/v1/*` 规则。

切换时删除旧监听配置和端口映射，更新调用方/探针至统一地址。保留默认 production HTTPS 时继续提供有效证书；选择受控外部 TLS 终止时，先验收传输保护和网络边界，再显式 false、移除 HTTP 证书项并同步调用方与探针协议。配置均重启生效。`migrate` 仍忽略服务专用监听/TLS/SMTP/内省/管理员设置；HTTP TLS 开关不改变出站 SMTP、OIDC、PostgreSQL或Redis策略。

回退同时恢复旧二进制、INTERNAL_ADDR/内部端口、旧内省路径和双监听探针、调用方传输选择及网关策略。从 production false 回退旧版本必须恢复服务端 TLS 证书；只回退二进制或单改 URL 会造成配置拒绝或调用失败。回退不执行 Down、UnsafeReset 或清空 Redis。真实网关、网格、服务商和产品环境验证继续延期，本地证据见[传输验收记录](../openspec/changes/archive/2026-10-06-simplify-accountsvc-transport/verification.md)；历史归档工件保留原架构和原验收事实。

## 同类身份换绑扩展

新增 `user.Service.ReplaceIdentity` 和消费者 `:replace` 自定义操作。已有绑定/解绑签名、数量限制与最后身份保护保持；新操作以软删除旧身份、新建同类身份保留账号连续性，无数据库结构迁移。

`user.Deps.ReauthMaxAge` 的零值默认5分钟，`SensitiveOpVerification` 的 nil 默认true；根门面传入现有 Config 值，不新增环境变量。自定义 `enduser.Service` 可按需实现可选 `enduser.IdentityReplacer`，未实现时换绑端点返回503；旧包使用方须先按[终端用户入口迁移](#终端用户-http-入口迁移)修改导入与引用。近期认证、冲突、验证码消费/回滚和重试边界见 [README](../README.md#手机号与邮箱换绑)。

撤销原因追加 `IDENTITY_REPLACED`，审计类型追加 `IDENTITY_REPLACE_REJECTED`，既有枚举值不重编号。回退旧二进制前须确认其审计/会话展示对新增值的兼容表现；回退不会恢复旧绑定或被撤销会话。登录及重新认证补充持锁后身份复核，旧身份在等待锁期间被移除时分别返回既有 CODE_INVALID、TARGET_NOT_ANCHOR，避免旧读结果重新进入原账号。

## 会话期限边界加固

会话列表和管理员 `active_session_count` 改为过滤已到刷新期限的记录；到期会话重新认证返回既有401 `TOKEN_INVALID`，等待锁期间到期的刷新返回400 OAuth `invalid_grant`。这会改变旧版本在这些边界上的结果，客户端应允许设备列表减少，并在认证失效时重新登录，不反复尝试重新认证。

根 Config、环境变量、公开 Service 方法、DTO和默认TTL不变。sqlc列表/计数查询增加显式时间参数，仓库内调用随生成结果更新；无数据库结构迁移、数据回填或历史SQL修改。原access/内省验证、显式撤销覆盖以及会话清理保留期继续有效，不能把refresh到期当作自动吊销access。

滚动升级期间旧实例仍可能使用旧的期限判断；只有全部实例完成升级后才具备一致行为。回退二进制不会恢复已吊销会话，并会重新引入旧版本的展示和到期边界行为。本轮检查与限制见[会话过期验收记录](../openspec/changes/archive/2026-10-04-harden-session-expiry/verification.md)。

## 验证码 HMAC 轮换连续性

本节记录此前轮换能力的兼容范围；当前开发代码还包含上文的[轮次契约升级](#验证码轮次契约升级)。旧无标识记录不再接管，当前仅保证同一新协议内旧 active 完整轮次的连续性；本次新增 Go/HTTP 字段、配置及 Redis 格式以上文为准。

验证码、目标冷却和目标日额度改为在一次 Lua 操作内统一处理全部已配置 HMAC 版本。单份旧格式验证码原地继续原 TTL 和错误次数，跨 active 消费至多成功一次；新发码替换全部版本同用途旧码，目标日额度按不同物理键求和，同密钥材料的版本别名不会重复累计。IP 日额度原本独立于 HMAC，计数方式保持。历史混跑产生多份存活验证码时，校验原子作废冲突码并返回原 `CODE_EXPIRED`，冷却和额度不变。

库与 accountsvc 共用此能力，无公共接口、配置字段或默认值、HTTP 协议、Redis 键/hash 格式、SQL、数据库结构和依赖变化。用途/渠道/实例隔离、验证码 fail-closed、投递失败不退额、JWT/refresh/access 吊销及异步审计契约保持；周期性数据库回填和默认间隔不变，混合 active 期间回填方向仍可能变化。

应先升级所有程序、分发完整相同密钥集合并保持策略一致，再重启切换 active。旧 HMAC 退役同时检查未删除身份的旧摘要引用及验证码、冷却、UTC 日界线；AES 的旧密文引用独立检查。回退旧程序可能重新引入状态不可达和额度分裂，完整条件见 [README](../README.md#密钥轮换)。本次行为测试、真实依赖及验收阶段见[轮换验收记录](../openspec/changes/archive/2026-10-04-harden-code-key-rotation/verification.md)。

## 功能对照

| 规格能力 | 生产入口/路径 | 验证 |
|---|---|---|
| 独立消费、完整功能 | 根门面、全部子包、sqlc.yaml | 独立构建、临时宿主编译、源文件清单 |
| 手机/邮箱验证码、限流 | user/code、user/service_signin.go、httpapi/enduser | code/store_test.go、service_test.go、signin_test.go、TestConsumerEndToEndAgainstRealDB |
| 验证码 HMAC 轮换 | user/code/store.go、user/code/scripts.go，库及 accountsvc 共用 | 多版本旧状态/并发测试、TestCodeKeyRotationIntegration、TestCodeRotationRedisIntegration、TestServiceIntegrationCodeKeyRotation；见[轮换验收记录](../openspec/changes/archive/2026-10-04-harden-code-key-rotation/verification.md) |
| 微信/Apple | user/idp、service_idp.go | wechat_test.go、apple_test.go、nonce_test.go、service_idp_test.go、TestWeChatSignInEndToEndAgainstRealDB |
| 身份绑定解绑 | user/service_identity.go | service_identity_test.go、identities_test.go、TestIdentityBindingEndToEndAgainstRealDB |
| 同类身份换绑 | user/service_identity_replacement.go、httpapi/enduser/replacement.go | 领域回滚/竞态、嵌入式及服务E2E，见[换绑验收记录](../openspec/changes/archive/2026-10-03-add-identity-replacement/verification.md) |
| JWT、刷新、会话、重新认证 | tokens、session、service_session.go | tokens_test.go、session/*/*_test.go、service_test.go、TestConsumerEndToEndAgainstRealDB |
| 资料、注销恢复、冻结 | service_me.go、service_lifecycle.go、service_admin.go | service_lifecycle_test.go、service_admin_test.go、TestAccountLifecycleEndToEndAgainstRealDB |
| 管理接口、角色、审计 | httpapi/admin、audit | httpapi/admin/*_test.go、audit/*_test.go、TestAdminSurfaceEndToEndAgainstRealDB |
| 加密、旧密钥、维护 | pii、maintenance、service_rekey.go | pii_test.go、runner_test.go、service_rekey_test.go、TestKeyRotationBackfillEndToEnd |
| 业务同事务匿名化 | anonymize、service_purge.go | service_purge_test.go、TestHostContracts（宿主先写入再失败） |
| 产品隔离 | Config.Schema/KeyPrefix、JWTIssuer/JWTAudience | TestIndependentInstances（故意复用 ID、refresh、目标与 HMAC 密钥暴露缺少命名空间） |
| 包内 schema/版本/重复迁移 | migrations、Auth.Migrate | migrations_test.go、TestSourceDatabaseTakeover |
| 旧存储及凭证接管 | 原样 0001、合成旧格式数据 | TestSourceDatabaseTakeover：四类表和版本快照不变、旧 JWT/refresh 可用、旧身份可解密 |

HTTP 请求响应、状态码、错误码和环境配置约定见 README；下列三个索引保留提取阶段的入口、实现与测试事实，其中 `httpapi/consumer`、`ConsumerHandler`、私有字段及源码行号均为历史记录，不是当前路径或可执行示例。当前入口见[终端用户入口迁移](#终端用户-http-入口迁移)及上方功能对照，当前必需测试选择器以 `scripts/required-tests.txt` 为准；后续服务与换绑验证分别见对应验收记录。Redis 吊销检查 fail-open、审计失败不阻断、刷新宽限故障拒绝请求等原语义不变。第三方真实发送和设备联调由宿主负责。

## 历史提取索引：公开门面与路由

```text
accountkit.go:104:func New(cfg Config, deps Deps) (*Auth, error) {
accountkit.go:253:func (a *Auth) Users() *user.Service { return a.users }
accountkit.go:256:func (a *Auth) ConsumerHandler() http.Handler { return a.consumer.Router() }
accountkit.go:260:func (a *Auth) AdminHandler() http.Handler { return a.adminHandler }
accountkit.go:264:func (a *Auth) RecordAdminForbidden(r *http.Request, p AdminPrincipal) {
accountkit.go:273:func (a *Auth) RequireScope(allowed ...string) func(http.Handler) http.Handler {
accountkit.go:278:func (a *Auth) RequireRecentAuth() func(http.Handler) http.Handler {
accountkit.go:283:func PrincipalFrom(ctx context.Context) (user.Principal, bool) { return authn.PrincipalFrom(ctx) }
accountkit.go:286:func (a *Auth) Config() Config { return a.cfg }
accountkit.go:290:func (a *Auth) Migrate(ctx context.Context) error {
accountkit.go:321:func (a *Auth) Start(ctx context.Context) {
accountkit.go:331:func (a *Auth) Close() {
accountkit.go:343:func (a *Auth) RunMaintenanceOnce(ctx context.Context) bool { return a.runner.RunOnce(ctx) }
accountkit.go:346:func PoolConfig(dsn, schema string) (*pgxpool.Config, error) {
config.go:120:func (c Config) Validate() error {
config.go:228:func ParseWeChatApps(spec string) ([]WeChatApp, error) {
config.go:248:func ConfigFromEnv(prefix string) (Config, error) {
httpapi/admin/handler.go:135:	r.With(op).Get("/users", h.listUsers)
httpapi/admin/handler.go:136:	r.With(op).Get("/users/{user}", h.getUser)
httpapi/admin/handler.go:137:	r.With(su).Delete("/users/{user}", h.deleteUser)
httpapi/admin/handler.go:138:	r.With(op).Post("/users/{user}:freeze", h.freeze)
httpapi/admin/handler.go:139:	r.With(op).Post("/users/{user}:unfreeze", h.unfreeze)
httpapi/admin/handler.go:140:	r.With(su).Post("/users/{user}:undelete", h.undeleteUser)
httpapi/admin/handler.go:141:	r.With(su).Get("/users/{user}/identities/{identity}:reveal", h.revealIdentity)
httpapi/admin/handler.go:142:	r.With(op).Get("/users/{user}/sessions", h.listSessions)
httpapi/admin/handler.go:143:	r.With(op).Delete("/users/{user}/sessions/{session}", h.deleteSession)
httpapi/admin/handler.go:144:	r.With(op).Post("/users/{user}/sessions:revokeAll", h.revokeAllSessions)
httpapi/admin/handler.go:145:	r.With(op).Get("/users/{user}/auditEvents", h.listAuditEvents)
httpapi/consumer/handler.go:124:	r.Post("/users:sendSignInCode", h.sendSignInCode)
httpapi/consumer/handler.go:125:	r.Post("/users:signInWithCode", h.signInWithCode)
httpapi/consumer/handler.go:126:	r.Post("/users:signInWithIdp", h.signInWithIdp)
httpapi/consumer/handler.go:127:	r.Post("/token", h.token)
httpapi/consumer/handler.go:128:	r.Post("/revoke", h.revoke)
httpapi/consumer/handler.go:131:	r.With(anyScope).Get("/users/me", h.getMe)
httpapi/consumer/handler.go:134:		r.Patch("/users/me", h.updateMe)
httpapi/consumer/handler.go:135:		r.Post("/users/me:sendReauthenticationCode", h.sendReauthenticationCode)
httpapi/consumer/handler.go:136:		r.Post("/users/me:reauthenticate", h.reauthenticate)
httpapi/consumer/handler.go:137:		r.Get("/users/me/sessions", h.listSessions)
httpapi/consumer/handler.go:138:		r.Delete("/users/me/sessions/{session}", h.deleteSession)
httpapi/consumer/handler.go:139:		r.Post("/users/me/sessions:revokeOthers", h.revokeOtherSessions)
httpapi/consumer/handler.go:145:	r.With(fullScope, h.RequireRecentAuth()).Delete("/users/me", h.deleteMe)
httpapi/consumer/handler.go:146:	r.With(undeleteScope).Post("/users/me:undelete", h.undelete)
httpapi/consumer/handler.go:151:		r.Post("/users/me:sendBindCode", h.sendBindCode)
httpapi/consumer/handler.go:152:		r.Get("/users/me/identities", h.listIdentities)
httpapi/consumer/handler.go:153:		r.Post("/users/me/identities", h.bindIdentity)
httpapi/consumer/handler.go:155:	r.With(fullScope, h.RequireRecentAuth()).Delete("/users/me/identities/{identity}", h.unbindIdentity)
```

## 历史提取索引：错误映射实现

```text
httpapi/consumer/response.go:110:	apierror.WriteJSON(w, http.StatusOK, newTokenResponse(res))
httpapi/consumer/response.go:118:		apierror.Write(w, &apierror.Error{Status: apierror.StatusResourceExhausted, Reason: rl.Dimension, Message: "too many requests, retry later", RetryAfterSeconds: retryAfterSeconds(rl.RetryAfter)})
httpapi/consumer/response.go:120:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "IDP_NONCE_REPLAYED", "this sign-in attempt was already used; start again"))
httpapi/consumer/response.go:122:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "IDP_APP_NOT_ALLOWED", "this application is not allowed to sign in"))
httpapi/consumer/response.go:124:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "IDP_CREDENTIAL_INVALID", "identity provider credential is invalid or expired"))
httpapi/consumer/response.go:126:		apierror.Write(w, &apierror.Error{Status: apierror.StatusUnavailable, Reason: "IDP_UNAVAILABLE", Message: "identity provider temporarily unavailable, retry later", RetryAfterSeconds: 1})
httpapi/consumer/response.go:128:		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), err)
httpapi/consumer/response.go:130:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "CODE_INVALID", "verification code is incorrect"))
httpapi/consumer/response.go:132:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "CODE_EXPIRED", "verification code has expired or was never issued for this purpose"))
httpapi/consumer/response.go:134:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "CODE_ATTEMPTS_EXHAUSTED", "too many incorrect attempts; request a new code"))
httpapi/consumer/response.go:136:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_TARGET", "target is not a valid phone number or email address"))
httpapi/consumer/response.go:142:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", msg))
httpapi/consumer/response.go:144:		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "TARGET_NOT_ANCHOR", "target must be a phone or email already bound to this account"))
httpapi/consumer/response.go:146:		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "USER_FROZEN", "this account is frozen"))
httpapi/consumer/response.go:148:		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "USER_PENDING_DELETION", "this account is pending deletion; sign in with its phone or email to restore it"))
httpapi/consumer/response.go:150:		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "INVALID_ACCOUNT_STATE", "operation not allowed in the account's current state"))
httpapi/consumer/response.go:152:		apierror.Write(w, apierror.New(apierror.StatusAlreadyExists, "IDENTITY_ALREADY_BOUND", "this identity is already bound to another account; sign in with that account instead"))
httpapi/consumer/response.go:154:		apierror.Write(w, apierror.New(apierror.StatusAlreadyExists, "IDENTITY_KIND_LIMIT", "the maximum number of identities of this kind is already bound"))
httpapi/consumer/response.go:156:		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "LAST_ANCHOR_IDENTITY", "cannot unbind the last phone or email identity"))
httpapi/consumer/response.go:158:		apierror.Write(w, apierror.New(apierror.StatusNotFound, "NOT_FOUND", "resource not found"))
httpapi/consumer/response.go:160:		apierror.Write(w, &apierror.Error{Status: apierror.StatusUnavailable, Reason: "DEPENDENCY_UNAVAILABLE", Message: "service temporarily unavailable, retry later", RetryAfterSeconds: 1})
httpapi/consumer/response.go:164:		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), err)
httpapi/consumer/response.go:172:		apierror.WriteOAuth(w, http.StatusBadRequest, "invalid_request", "request body must be a single JSON object with known fields", nil)
httpapi/consumer/response.go:174:		apierror.WriteOAuth(w, http.StatusForbidden, "invalid_grant", "user is frozen", map[string]any{"reason": "USER_FROZEN"})
httpapi/consumer/response.go:176:		apierror.WriteOAuth(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid, expired, or revoked", nil)
httpapi/consumer/response.go:179:		apierror.WriteOAuth(w, http.StatusServiceUnavailable, "temporarily_unavailable", "service temporarily unavailable, retry later", nil)
httpapi/consumer/response.go:182:		apierror.WriteOAuth(w, http.StatusInternalServerError, "server_error", "internal error", nil)
httpapi/admin/response.go:27:// freezeResource 是冻结快照（用户资源的 freeze 字段；非 FROZEN 时为 null）。
httpapi/admin/response.go:54:		out.Freeze = &freezeResource{FreezeTime: fmtTime(u.Freeze.Time), Reason: u.Freeze.Reason, ActorSubject: u.Freeze.ActorSubject, ActorUsername: u.Freeze.ActorUsername}
httpapi/admin/response.go:130:		Reason: e.Reason, SessionID: e.SessionID, SubjectHint: e.SubjectHint, DeviceID: e.DeviceID, RequestID: e.RequestID,
httpapi/admin/response.go:150:// 目标账号的状态问题一律 400 FAILED_PRECONDITION。
httpapi/admin/response.go:154:		apierror.Write(w, apierror.New(apierror.StatusNotFound, "NOT_FOUND", "resource not found"))
httpapi/admin/response.go:156:		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "INVALID_ACCOUNT_STATE", "operation not allowed in the account's current state"))
httpapi/admin/response.go:158:		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "USER_FROZEN", "the account is frozen; unfreeze it first"))
httpapi/admin/response.go:160:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_FILTER", "invalid filter: identity.phone / identity.email value is not a valid phone number or email address"))
httpapi/admin/response.go:166:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", msg))
httpapi/admin/response.go:168:		apierror.WriteInternal(w, h.d.Logger, reqid.From(r.Context()), err)
```

## 历史提取索引：测试

| 文件 | 测试 |
|---|---|
| `accountkit_db_test.go` | `TestMigrateStartCloseAgainstRealDB` |
| `accountkit_db_test.go` | `TestAuditEventsPersistedAndExpiredEndToEnd` |
| `accountkit_db_test.go` | `TestConsumerEndToEndAgainstRealDB` |
| `accountkit_db_test.go` | `TestMigrateRejectsPoolWithoutSchemaOnSearchPath` |
| `accountkit_db_test.go` | `TestWeChatSignInEndToEndAgainstRealDB` |
| `accountkit_db_test.go` | `TestIdentityBindingEndToEndAgainstRealDB` |
| `accountkit_db_test.go` | `TestKeyRotationBackfillEndToEnd` |
| `accountkit_db_test.go` | `TestAccountLifecycleEndToEndAgainstRealDB` |
| `accountkit_db_test.go` | `TestAdminSurfaceEndToEndAgainstRealDB` |
| `accountkit_test.go` | `TestNewIsPureAndAppliesDefaults` |
| `accountkit_test.go` | `TestNewRejectsBadConfigAndMissingDeps` |
| `accountkit_test.go` | `TestNewExposesUsersService` |
| `accountkit_test.go` | `TestPoolConfigSetsSearchPath` |
| `accountkit_test.go` | `TestCloseBeforeStartIsSafe` |
| `accountkit_test.go` | `TestNewDefaultsAuditToAsyncStoreUnlessInjected` |
| `accountkit_test.go` | `TestDefaultRequestIDAndClientIP` |
| `accountkit_test.go` | `TestConsumerHandlerAndMiddlewareWiring` |
| `accountkit_test.go` | `TestHostMountShape` |
| `accountkit_test.go` | `TestNewWiresIdPVerifiersOnlyWhenConfigured` |
| `accountkit_test.go` | `TestNewRejectsBadAnonymizersAndAcceptsDistinctOnes` |
| `accountkit_test.go` | `TestLifecycleRoutesMounted` |
| `accountkit_test.go` | `TestNewRequiresBothAdminDepsOrNeither` |
| `accountkit_test.go` | `TestAdminHandlerUnconfiguredIs503` |
| `accountkit_test.go` | `TestAdminHandlerMountsBehindHostVerifier` |
| `accountkit_test.go` | `TestRecordAdminForbidden` |
| `compatibility_test.go` | `TestSourceDatabaseTakeover` |
| `config_test.go` | `TestValidateAppliesDefaults` |
| `config_test.go` | `TestConfigFromEnvOverridesAndParses` |
| `config_test.go` | `TestValidateRejects` |
| `config_test.go` | `TestConfigFromEnvReportsBadValues` |
| `config_test.go` | `TestIdPConfigDefaultsAndValidation` |
| `config_test.go` | `TestParseWeChatApps` |
| `config_test.go` | `TestConfigFromEnvIdP` |
| `host_contract_test.go` | `TestHostContracts` |
| `isolation_test.go` | `TestIndependentInstances` |
| `audit/async_test.go` | `TestAsyncFlushesByBatchSizeAndInterval` |
| `audit/async_test.go` | `TestAsyncCloseFlushesRemainingAndDropsAfterClose` |
| `audit/async_test.go` | `TestAsyncCloseWithoutStartFlushesSynchronously` |
| `audit/async_test.go` | `TestAsyncStartAfterCloseDoesNotLeakWorker` |
| `audit/async_test.go` | `TestAsyncDropsWhenQueueFullAndNeverBlocks` |
| `audit/async_test.go` | `TestAsyncWriteFailureIsCountedNotPropagated` |
| `audit/async_test.go` | `TestAsyncWriteFailureLogIsThrottled` |
| `audit/async_test.go` | `TestAsyncWorkerKeepsRunningAfterAFailedBatch` |
| `audit/async_test.go` | `TestAsyncContextCancelFlushesAndStops` |
| `audit/async_test.go` | `TestAsyncWritesToPostgres` |
| `audit/audit_test.go` | `TestNoopImplementsRecorder` |
| `audit/audit_test.go` | `TestMemoryRecordsInOrderAndIsConcurrencySafe` |
| `audit/audit_test.go` | `TestHint` |
| `audit/store_test.go` | `TestStoreInsertBatchMapsFieldsAndNulls` |
| `audit/store_test.go` | `TestStoreInsertBatchIsAllOrNothing` |
| `audit/store_test.go` | `TestStoreInsertBatchSurvivesSameMillisecondIDCollisions` |
| `audit/store_test.go` | `TestStoreInsertBatchRetriesOncePrimaryKeyConflict` |
| `audit/store_test.go` | `TestStoreDeleteOlderThanBatches` |
| `audit/store_test.go` | `TestStoreListByUserPaginatesByOccurTimeAndID` |
| `email/email_test.go` | `TestNormalize` |
| `email/email_test.go` | `TestNormalizeRejects` |
| `email/email_test.go` | `TestHintsAndMask` |
| `enum/enum_test.go` | `TestUserStateValuesArePinned` |
| `enum/enum_test.go` | `TestStringAndParseRoundTrip` |
| `enum/enum_test.go` | `TestParseRejectsLowercaseAndUnknown` |
| `enum/enum_test.go` | `TestValidAndUnknownString` |
| `enum/enum_test.go` | `TestIdentityKindHelpers` |
| `httpapi/admin/audit_test.go` | `TestListAuditEvents` |
| `httpapi/admin/filter_test.go` | `TestParseFilterAcceptsSupportedGrammar` |
| `httpapi/admin/filter_test.go` | `TestParseFilterRejectsUnsupportedInputWithoutEchoingValues` |
| `httpapi/admin/identities_test.go` | `TestRevealIdentity` |
| `httpapi/admin/page_test.go` | `TestParsePageSize` |
| `httpapi/admin/page_test.go` | `TestCursorRoundTripAndRejection` |
| `httpapi/admin/sessions_test.go` | `TestSessionsListRevokeAndRevokeAll` |
| `httpapi/admin/users_test.go` | `TestRolesGateEveryRoute` |
| `httpapi/admin/users_test.go` | `TestMissingPrincipalIs401` |
| `httpapi/admin/users_test.go` | `TestListUsersParsesQueryAndEncodesNextPage` |
| `httpapi/admin/users_test.go` | `TestGetUserDetail` |
| `httpapi/admin/users_test.go` | `TestFreezeUnfreezeDeleteUndelete` |
| `httpapi/apierror/apierror_test.go` | `TestHTTPStatusMapping` |
| `httpapi/apierror/apierror_test.go` | `TestWriteAIP193Body` |
| `httpapi/apierror/apierror_test.go` | `TestWriteOmitsEmptyOptionalFields` |
| `httpapi/apierror/apierror_test.go` | `TestWriteInternalHidesErrorAndLogs` |
| `httpapi/apierror/apierror_test.go` | `TestWriteOAuth` |
| `httpapi/apierror/apierror_test.go` | `TestWriteRouteNotFoundAndMethodNotAllowed` |
| `httpapi/authn/authn_test.go` | `TestBearerMissingAndInvalid` |
| `httpapi/authn/authn_test.go` | `TestBearerUnavailableIs503AndSkipsWhenAlreadyAuthenticated` |
| `httpapi/authn/authn_test.go` | `TestRequireScope` |
| `httpapi/authn/authn_test.go` | `TestRequireRecentAuth` |
| `httpapi/authn/authn_test.go` | `TestPrincipalFromEmpty` |
| `httpapi/consumer/identities_test.go` | `TestListIdentities` |
| `httpapi/consumer/identities_test.go` | `TestSendBindCode` |
| `httpapi/consumer/identities_test.go` | `TestSendBindCodeEmptyClientIPIs500` |
| `httpapi/consumer/identities_test.go` | `TestBindIdentity` |
| `httpapi/consumer/identities_test.go` | `TestUnbindIdentityRequiresRecentAuth` |
| `httpapi/consumer/me_test.go` | `TestGetMe` |
| `httpapi/consumer/me_test.go` | `TestUpdateMe` |
| `httpapi/consumer/me_test.go` | `TestReauthentication` |
| `httpapi/consumer/me_test.go` | `TestSendReauthenticationCodeEmptyClientIPIs500` |
| `httpapi/consumer/me_test.go` | `TestDeleteMe` |
| `httpapi/consumer/me_test.go` | `TestUndelete` |
| `httpapi/consumer/oauth_test.go` | `TestTokenEndpoint` |
| `httpapi/consumer/oauth_test.go` | `TestRevokeEndpoint` |
| `httpapi/consumer/request_test.go` | `TestRouterSetsRequestIDAndReturns404ForUnknownRoute` |
| `httpapi/consumer/request_test.go` | `TestNewRejectsMissingDeps` |
| `httpapi/consumer/response_test.go` | `TestWriteServiceErrorMapping` |
| `httpapi/consumer/response_test.go` | `TestWriteServiceErrorInvalidArgumentEmptyDetail` |
| `httpapi/consumer/response_test.go` | `TestWriteOAuthErrorMapping` |
| `httpapi/consumer/response_test.go` | `TestDTOShapes` |
| `httpapi/consumer/response_test.go` | `TestDeviceFromHeaders` |
| `httpapi/consumer/response_test.go` | `TestCredentialOneof` |
| `httpapi/consumer/response_test.go` | `TestCredentialExplicitNullIsAbsent` |
| `httpapi/consumer/response_test.go` | `TestCredentialIdp` |
| `httpapi/consumer/response_test.go` | `TestParseChannel` |
| `httpapi/consumer/sessions_test.go` | `TestSessions` |
| `httpapi/consumer/sessions_test.go` | `TestListSessionsNilIsEmptyArray` |
| `httpapi/consumer/signin_test.go` | `TestSendSignInCode` |
| `httpapi/consumer/signin_test.go` | `TestSendSignInCodeEmptyClientIPIs500` |
| `httpapi/consumer/signin_test.go` | `TestSignInWithCode` |
| `httpapi/consumer/signin_test.go` | `TestSignInWithIdp` |
| `httpapi/consumer/signin_test.go` | `TestSignInWithIdpPendingDeletionIs403` |
| `httpapi/jsonbody/jsonbody_test.go` | `TestDecodeAcceptsObjectRejectsEverythingElse` |
| `httpapi/reqid/reqid_test.go` | `TestMiddlewareSetsHeaderAndContext` |
| `ids/ids_test.go` | `TestNewHasPrefixAndLength` |
| `ids/ids_test.go` | `TestValidRejectsWrongKindCaseAndAlphabet` |
| `ids/ids_test.go` | `TestPatternMatchesValidAndIsAnchored` |
| `ids/ids_test.go` | `TestNewIsTimeOrderedAcrossMilliseconds` |
| `ids/ids_test.go` | `TestNewRandomBitsVary` |
| `internal/testgate/main_test.go` | `TestVerifyRequiresEveryTestToPass` |
| `maintenance/runner_test.go` | `TestRunOnceRunsAllTasksAndContinuesPastFailures` |
| `maintenance/runner_test.go` | `TestRunOnceSkipsWhenLockHeld` |
| `maintenance/runner_test.go` | `TestStartTicksAndCloseStops` |
| `maintenance/runner_test.go` | `TestPanicInTaskIsRecovered` |
| `maintenance/runner_test.go` | `TestCloseCancelsInFlightRound` |
| `maintenance/runner_test.go` | `TestPGLockerMutualExclusion` |
| `migrations/migrations_test.go` | `TestValidSchema` |
| `migrations/migrations_test.go` | `TestUpCreatesTablesInsideSchemaOnly` |
| `migrations/migrations_test.go` | `TestTwoSchemasCoexist` |
| `migrations/migrations_test.go` | `TestSchemaPinsInvariants` |
| `phone/phone_test.go` | `TestNormalize` |
| `phone/phone_test.go` | `TestNormalizeRejectsInvalid` |
| `phone/phone_test.go` | `TestHintsAndMask` |
| `pii/pii_test.go` | `TestParseKeyList` |
| `pii/pii_test.go` | `TestParseKeyListRejects` |
| `pii/pii_test.go` | `TestParseKeyListErrorDoesNotEchoSecret` |
| `pii/pii_test.go` | `TestCipherRoundTripAndVersion` |
| `pii/pii_test.go` | `TestNewCipherAndDigesterValidate` |
| `pii/pii_test.go` | `TestDigesterIsKeyedStableAndVersioned` |
| `pii/pii_test.go` | `TestNewCipherRequiresExactly32ByteKeys` |
| `session/grace/cache_test.go` | `TestPutGetRoundTripEncryptedAndExpires` |
| `session/grace/cache_test.go` | `TestGetMissAndTamperedValue` |
| `session/grace/cache_test.go` | `TestRedisDown` |
| `session/grace/cache_test.go` | `TestPutRejectsNonPositiveTTL` |
| `session/revocation/set_test.go` | `TestRevokeThenIsRevokedThenExpires` |
| `session/revocation/set_test.go` | `TestRedisDownReturnsUnavailable` |
| `session/revocation/set_test.go` | `TestRevokeRejectsNonPositiveTTL` |
| `tokens/tokens_test.go` | `TestSignParseRoundTrip` |
| `tokens/tokens_test.go` | `TestParseAcceptsOldKeyVersionDuringOverlap` |
| `tokens/tokens_test.go` | `TestParseRejects` |
| `tokens/tokens_test.go` | `TestNewSignerValidates` |
| `tokens/tokens_test.go` | `TestLeewayDefaultAndOverride` |
| `user/repo_test.go` | `TestRepoRotateIsCAS` |
| `user/repo_test.go` | `TestRepoRevokeSessionsByUserExceptCurrent` |
| `user/repo_test.go` | `TestRepoIdentityDigestsAndUniqueViolation` |
| `user/repo_test.go` | `TestRepoWithTxRollsBackOnError` |
| `user/repo_test.go` | `TestRepoLockUserByIDSerializes` |
| `user/repo_test.go` | `TestRepoProviderIdentityLookupAndMetaMerge` |
| `user/repo_test.go` | `TestRepoGetAndSoftDeleteIdentity` |
| `user/repo_test.go` | `TestRepoSoftDeleteUndeletePurgeUser` |
| `user/repo_test.go` | `TestRepoAnonymizeIdentityKeepsExactlyOneSubjectAndCoversSoftDeleted` |
| `user/repo_test.go` | `TestRepoScrubAuditEventsAndDeleteStaleSessions` |
| `user/repo_test.go` | `TestRepoKeyVersionQueries` |
| `user/repo_test.go` | `TestRepoFreezeUnfreezeAndCountSessions` |
| `user/repo_test.go` | `TestRepoListUsersAdminFiltersAndPaginates` |
| `user/service_admin_test.go` | `TestFreezeRevokesSessionsSnapshotsAndAudits` |
| `user/service_admin_test.go` | `TestAdminDeleteAndUndeleteShareConsumerRules` |
| `user/service_admin_test.go` | `TestAdminSessionsListRevokeOneAndAll` |
| `user/service_admin_test.go` | `TestListUsersFiltersAndPaginates` |
| `user/service_admin_test.go` | `TestGetUserDetailAndRevealIdentity` |
| `user/service_identity_test.go` | `TestListIdentitiesMasksAnchors` |
| `user/service_identity_test.go` | `TestSendBindCodeUsesBindPurposeAndQuota` |
| `user/service_identity_test.go` | `TestBindWithCodeHappyPathIdempotentAndScopeAfterRefresh` |
| `user/service_identity_test.go` | `TestBindWithCodeConflictAndKindLimit` |
| `user/service_identity_test.go` | `TestNewServiceRejectsZeroMaxIdentitiesPerKind` |
| `user/service_identity_test.go` | `TestBindWithIdpHappyIdempotentConflictAndLimit` |
| `user/service_identity_test.go` | `TestBindWithIdpRejectsNonIdpKindAndDisabledProvider` |
| `user/service_identity_test.go` | `TestUnbindIdentityGuardsLastAnchorAndOwnership` |
| `user/service_identity_test.go` | `TestBindWithCodeRejectsNonActiveAccountStates` |
| `user/service_identity_test.go` | `TestBindWithIdpRejectsNonActiveAccountStates` |
| `user/service_identity_test.go` | `TestUnbindIdentityRejectsFrozenPendingDeletionAndDeletedAccounts` |
| `user/service_identity_test.go` | `TestUnbindLastTwoAnchorsConcurrently` |
| `user/service_identity_test.go` | `TestBindSameSubjectConcurrentlyFromTwoAccounts` |
| `user/service_idp_test.go` | `TestSignInWithIdpWeChatRegistersWithBindScopeAndMergesOpenIDs` |
| `user/service_idp_test.go` | `TestSignInWithIdpAppleHintEmailAndReplay` |
| `user/service_idp_test.go` | `TestSignInWithIdpRejections` |
| `user/service_idp_test.go` | `TestSignInWithIdpWeChatIgnoresEmptyOpenID` |
| `user/service_idp_test.go` | `TestSignInWithIdpWeChatConcurrentFirstLoginCreatesOneUser` |
| `user/service_idp_test.go` | `TestSignInWithCodeStillWorksAfterRefactor` |
| `user/service_idp_test.go` | `TestSignInWithIdpRejectsPendingDeletionAccount` |
| `user/service_idp_test.go` | `TestSignInWithCodeFrozenAuditCarriesUserID` |
| `user/service_lifecycle_test.go` | `TestNewServiceRejectsZeroDeletionCoolingPeriod` |
| `user/service_lifecycle_test.go` | `TestDeleteMeSoftDeletesRevokesAllSessionsAndAudits` |
| `user/service_lifecycle_test.go` | `TestDeleteMeRejectsFrozenDeletedAndUnknownUser` |
| `user/service_lifecycle_test.go` | `TestUndeleteRestoresActiveAndRefreshWorksAgain` |
| `user/service_purge_test.go` | `TestNewServiceRejectsBadAnonymizers` |
| `user/service_purge_test.go` | `TestPurgeAnonymizesAccountIdentitiesSessionsAuditAndHostTables` |
| `user/service_purge_test.go` | `TestPurgeRollsBackWhenAnonymizerFailsAndSkipsUndueOrUndeleted` |
| `user/service_purge_test.go` | `TestPurgeIsANoopWhenContextCanceled` |
| `user/service_purge_test.go` | `TestPurgeCoversEveryTableWithUserID` |
| `user/service_rekey_test.go` | `TestRekeyDigestsRecomputesOnlyActiveOldVersionRows` |
| `user/service_rekey_test.go` | `TestReencryptSubjectsRewrapsCiphertextAndKeepsPlaintext` |
| `user/service_rekey_test.go` | `TestBackfillSkipsUndecryptableRowLogsWithoutPIIAndContinues` |
| `user/service_rekey_test.go` | `TestCheckKeyVersionsRejectsUnconfiguredVersions` |
| `user/service_test.go` | `TestSignInCreatesUserThenReusesIt` |
| `user/service_test.go` | `TestSignInWithEmailNormalizes` |
| `user/service_test.go` | `TestSignInWrongCodeAndCrossChannel` |
| `user/service_test.go` | `TestSignInSameDeviceReplacesSession` |
| `user/service_test.go` | `TestSignInFrozenAndPendingDeletion` |
| `user/service_test.go` | `TestSignInDeletedUserWithLiveIdentityIsInvariantViolation` |
| `user/service_test.go` | `TestSignInValidationAndRateLimitAndRedisDown` |
| `user/service_test.go` | `TestSendSignInCodeRequiresIP` |
| `user/service_test.go` | `TestRefreshRotatesAndOldTokenEntersGrace` |
| `user/service_test.go` | `TestRefreshReplayAfterGraceRevokesSession` |
| `user/service_test.go` | `TestRefreshRejectsUnknownRevokedAndExpired` |
| `user/service_test.go` | `TestRefreshFrozenAndPendingDeletionRevoke` |
| `user/service_test.go` | `TestRefreshConcurrentOnlyOneRotatesOthersGetSamePair` |
| `user/service_test.go` | `TestRefreshGraceDegradesWhenRedisDown` |
| `user/service_test.go` | `TestAuthenticateRejectsGarbageAndBadIDs` |
| `user/service_test.go` | `TestSessionsListRevokeOneAndOthers` |
| `user/service_test.go` | `TestRefreshReuseDetectedContainmentFailurePropagatesError` |
| `user/service_test.go` | `TestGetMeAndUpdateDisplayName` |
| `user/service_test.go` | `TestReauthenticateUpdatesAuthTimeOnlyForAnchor` |
| `user/service_test.go` | `TestReauthenticateFrozenUserAudited` |
| `user/service_test.go` | `TestCleanupSessionsDeletesOnlyStaleRows` |
| `user/code/store_test.go` | `TestIssueThenVerifySucceedsOnceAndDeletes` |
| `user/code/store_test.go` | `TestVerifyWrongCodeCountsAndExhausts` |
| `user/code/store_test.go` | `TestVerifyPurposeMismatchIsMissing` |
| `user/code/store_test.go` | `TestIssueCooldownAndReissueReplacesCode` |
| `user/code/store_test.go` | `TestIssueDailyLimits` |
| `user/code/store_test.go` | `TestIssueConcurrentBurstOnlyOneWins` |
| `user/code/store_test.go` | `TestVerifyConcurrentOnlyOneSucceeds` |
| `user/code/store_test.go` | `TestRedisDownIsUnavailable` |
| `user/code/store_test.go` | `TestCodeExpiresWithTTL` |
| `user/idp/apple_test.go` | `TestAppleVerifyHappyPathAndHintEmail` |
| `user/idp/apple_test.go` | `TestAppleVerifyRejections` |
| `user/idp/apple_test.go` | `TestAppleJWKSRefreshOnUnknownKidIsThrottled` |
| `user/idp/apple_test.go` | `TestAppleJWKSDownWithoutCacheIsUnavailable` |
| `user/idp/apple_test.go` | `TestAppleDisabledWhenNoBundleIDs` |
| `user/idp/apple_test.go` | `TestAppleJWKSFetchFailureBeforeFirstSuccessIsThrottled` |
| `user/idp/apple_test.go` | `TestAppleVerifyMissingKidHeaderIsInvalidCredential` |
| `user/idp/apple_test.go` | `TestAppleVerifyNoNonceRegistryIsMisconfigured` |
| `user/idp/apple_test.go` | `TestAppleNonceTTLExtendsToCoverIDTokenLifetime` |
| `user/idp/apple_test.go` | `TestAppleJWKSFetchIgnoresCallerCancellation` |
| `user/idp/apple_test.go` | `TestAppleUnknownKidAfterFailedRefreshIsUnavailable` |
| `user/idp/apple_test.go` | `TestAppleUnknownKidAfterSuccessfulRefreshIsInvalidCredential` |
| `user/idp/apple_test.go` | `TestAppleVerifyRedisDownIsUnavailable` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetValid` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetSkipsNonRSA` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetNoRSAKeysIsError` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetUndecodableModulusIsError` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetSmallModulusIsError` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetBadExponentIsError` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetLargeModulusIsError` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetSkipsNonSigningUse` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetSkipsNonRS256Alg` |
| `user/idp/nonce_test.go` | `TestNonceRegistryRegisterOnce` |
| `user/idp/nonce_test.go` | `TestNonceRegistryRegisterHonorsCallerTTL` |
| `user/idp/nonce_test.go` | `TestNonceRegistryRejectsNonPositiveTTL` |
| `user/idp/nonce_test.go` | `TestNonceRegistryRedisDownIsUnavailable` |
| `user/idp/nonce_test.go` | `TestNonceRegistryRejectsEmpty` |
| `user/idp/wechat_test.go` | `TestWeChatVerify` |
| `user/idp/wechat_test.go` | `TestWeChatNetworkErrorIsUnavailable` |
| `user/idp/wechat_test.go` | `TestWeChatNoAppsMeansNotAllowed` |
| `user/idp/wechat_test.go` | `TestWeChatDoesNotFollowRedirects` |
| `user/sender/log_test.go` | `TestLogSenderWritesMaskedTargetAndCode` |
