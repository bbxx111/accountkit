## Context

动机见 [proposal.md](proposal.md)。现有 [库契约](../../specs/embedded-auth-package/spec.md)、[发送契约](../../specs/verification-delivery/spec.md) 和 [内省契约](../../specs/consumer-token-introspection/spec.md) 继续适用。

`BindWithCode` 在同类数量达到上限时拒绝新增，`UnbindIdentity` 拒绝删除最后一个锚点；两者不能由客户端组合成安全换绑。identity 已支持软删除及活跃身份唯一索引，会话已有排除当前 sid 的批量撤销查询。验证码校验在 Redis Lua 中消费，与 PostgreSQL 不构成同一事务。

用户已确认独立换绑端点并复用发码、重新认证，随后通过 apply 授权按整份提案实施，会话策略采用“保留当前、撤销其他”。

## Goals / Non-Goals

**Goals:** 为已登录用户提供同类手机／邮箱替换；领域方法承担状态、会话、归属与一致性校验，库和服务共用；正常失败不损坏旧绑定，结果不夸大跨存储原子性。

**Non-Goals:** 跨 PHONE/EMAIL 转换、第三方身份替换、账号合并、丢失全部既有凭据的找回、管理员代办、强制每次向旧地址再发码、通知系统、外部环境验收及全局吊销一致性改造。

## Decisions

### 1. 单一显式操作，保留原接口

新增库内相对路由 `POST /users/me/identities/{identity}:replace`；默认挂载后为 `/v1/users/me/identities/{identity}:replace`。路径指向当前账号的旧活动身份，请求仅接受一个 phone/email 凭证，并要求 kind 与旧身份一致。

```json
{"email":{"target":"new@example.test","code":"123456"}}
```

成功返回 200 和现有 `identityResource` 格式，包含新 `i_` ID 对应的 name、kind、masked_subject、create_time；不返回令牌或明文身份。旧身份软删除，新身份新建，user_id 不变。

不把“数量已满”解释为自动替换；原 POST 绑定和 DELETE 解绑行为不变。也不原地改旧身份的 subject，以便保留历史绑定边界和既有软删除语义。相较 PATCH，这个操作同时消费一次性证明、终止旧绑定并执行会话策略，明确的自定义操作更容易审查和调用。

### 2. 复用近期认证与 BIND 码

调用者须持有 `user` scope。按现有 `ReauthMaxAge` 和 `SensitiveOpVerification` 判定近期认证；默认启用、窗口5分钟。过期时客户端调用既有 sendReauthenticationCode/reauthenticate，使用得到的新 access token 继续；近期登录也满足现有 auth_time 语义。显式关闭敏感操作新鲜度检查时仍必须满足有效会话、账号状态和新地址验证码要求。

这证明近期控制当前账号，不额外证明“此次刚向被替换的旧地址收过码”，也不绑定特定认证方式。当前 JWT 没有用于证明某次重新认证来源的 claim；本变更不新增此类 claim。文档须据此说明实际安全边界。

新地址通过既有 `POST /users/me:sendBindCode` 获取 BIND 码；SIGN_IN/REAUTH 码不能替代。发送器禁用、冷却、目标/IP 额度和尝试次数全部沿用。服务短信禁用不变；库宿主已有短信实现仍可支持手机换绑。

### 3. 领域入口与源码兼容

新增 `user.Service.ReplaceIdentity(ctx, p, identityID, channel, target, plainCode, meta) (IdentityInfo, error)`。宿主必须传入经过认证的 Principal；方法本身复核 scope、近期认证、当前会话属于该 user 且未吊销/未到刷新期限，以及 ACTIVE 状态。不能仅依赖 HTTP 中间件，否则直接库调用或 Redis 故障时会绕过写路径保护。

`user.Deps` 增加可选 `ReauthMaxAge` 和 `SensitiveOpVerification`，零时长使用5分钟、nil 使用true，负时长拒绝；根门面注入现有 Config 值，不新增环境变量。旧直接 NewService 调用方无需补必填字段。新领域 scope/近期认证错误映射到既有 403 INSUFFICIENT_SCOPE、400 REAUTHENTICATION_REQUIRED 语义。

不向公开的 `consumer.Service` 接口追加必需方法。通过独立可选接口检测 ReplaceIdentity 能力；标准 `*user.Service` 自动满足。自定义旧实现继续编译并使用旧路由，认证后的换绑请求在未提供该能力时返回503 `IDENTITY_REPLACEMENT_NOT_CONFIGURED`，文档给出添加方法的接入方式。

### 4. 数据库事务与锁序

新路径按以下顺序执行，查询修改只写 `user/query.sql` 并经 sqlc 生成：

1. 校验 Principal、scope、新鲜度、ID和输入格式，归一化新目标；不在错误中回显秘密。
2. 事务外预检 ACTIVE、当前有效 session、旧身份 id + user_id 归属、锚点类型和同类替换；归一化后仍为原目标时返回400 `IDENTITY_UNCHANGED`。此时已知无效的请求不消费新码。
3. 在不持有数据库写锁时校验一次 BIND 码，沿用现有 Redis 请求预算、错误码、尝试计数和 fail-closed。验证码校验失败不进入写事务。
4. 开启事务，按 user → current session 顺序加锁，重新确认状态、会话归属/有效期、旧身份及近期认证条件。事务前预检不能替代此步；并发变化导致此步拒绝时，验证码可能已经消费。
5. 校验新目标是否已由任何账号绑定；同一账号的另一条活动身份也返回409 `IDENTITY_ALREADY_BOUND`，本操作不合并两条身份。查找使用全部已配置摘要版本，不仅 active 版本。
6. 按替换后的最终活动数量检查同类上限；不能暂时放宽配置或绕开最终数量限制。软删除旧身份、用 active 摘要/加密版本创建新身份，撤销除当前 sid 外的会话；任一步失败整体回滚。
7. 提交成功后按既有机制写其他 sid 的 Redis 吊销集及异步审计，返回新身份。

旧账号的身份写入使用 user 行锁，同一旧身份并发替换至两个目标至多一方成功。跨账号争抢同一新目标由已有活跃唯一索引及领域检查兜底；不得靠应用预查询独自保证唯一性。与冻结、注销、解绑和刷新同时运行时按提交/锁定顺序得出合法结果，不死锁、不让已吊销会话被刷新复活。

批量会话撤销也需遵循先锁 user 再写 session 的顺序：现有 RevokeOtherSessions 和 AdminRevokeAllSessions 尚未取得 user 锁，实施时定向补齐并保留各入口原错误语义。否则其先持有其他 session 行、再等待换绑持有的当前 session 时可能形成锁环。单会话刷新/撤销不反向申请 user 写锁，不扩展成全局锁或重构无关路径。

代码核对发现 `findOrCreateUser` 先查 identity 再锁 user，`Reauthenticate` 在事务外确认锚点；单靠新增换绑事务不能阻止这两条路径使用旧读结果。因此本变更包含两处定向修正：验证码登录取得 user 锁后复核刚命中的 identity 仍活动且仍匹配目标，否则以既有400 CODE_INVALID 拒绝本次证明，不给原账号创建会话；重新认证事务改为 user → session 的锁序，在更新 auth_time 前重新确认锚点仍属于账号，否则以既有400 TARGET_NOT_ANCHOR 拒绝。测试明确编排“旧读取完成 → 换绑提交 → 原请求继续”的顺序，而非只做随机并发。普通非并发自动注册及第三方登录规则不变。

### 5. 会话策略及实际失效边界

当前 sid、其 refresh token 和 auth_time 保留；响应不签发新 token pair。其他会话的数据库撤销和身份替换同事务提交，追加 `IDENTITY_REPLACED` 撤销原因且不重编号既有枚举。

其他 refresh 的后续请求应遵循数据库撤销状态，包含宽限缓存命中路径。正常 Redis 下其他 access 及内省查询被拒绝；吊销集写入/查询故障仍沿用现有 fail-open，不能承诺所有旧 access 立刻失效。提交后 Redis 或异步审计失败不将已完成换绑伪装成数据库回滚。

旧地址从此不再登录原账号；现有登录本来支持自动注册，因此不能承诺旧地址永远不可登录或不能建立另一个账号。已经发出但尚未消费的旧地址验证码也不因本操作获得访问原账号的能力。

### 6. 错误、重试与跨存储边界

| 情况 | 结果 |
|---|---|
| 缺失/非法 token，或事务内发现当前会话失效 | 401，沿用 Bearer challenge |
| scope 不足 / 冻结账号 | 403，沿用对应既有原因 |
| 近期认证不足 | 400 REAUTHENTICATION_REQUIRED |
| ID 畸形 / 请求体错误 / 非锚点 / kind 不符 | 400，安全的参数错误 |
| 旧身份不存在、已软删或不属于调用者 | 404 NOT_FOUND，无归属泄露 |
| 新目标与旧目标相同 | 400 IDENTITY_UNCHANGED，无修改 |
| 新目标已绑定 | 409 IDENTITY_ALREADY_BOUND，换绑端点使用不误称“另一个账号”的通用安全消息 |
| 验证码错误/过期/耗尽、发送限额或 Redis 校验不可用 | 沿用现有400/429/503语义 |
| 注销中/已删除账号、数据库错误 | 沿用领域状态拒绝及固定500响应 |

只有身份与会话数据库写入具有事务原子性：已成功验证的验证码在 Redis 中已消费，随后冲突或数据库失败不退还；错误尝试计数也不随回滚消失。发送额度/冷却不返还、不自动重新发码。

本版不引入幂等键或操作账本。成功后的原 identity 路径已失效，重复请求返回404；网络超时导致结果未知时客户端先重新列出身份，再决定是否重发验证码。不得将盲目重复旧请求解释成再次成功；提交结果不确定时也不承诺数据库一定未变。

### 7. 审计与兼容验证

提交后分别产生旧身份 `IDENTITY_UNBOUND` 和新身份 `IDENTITY_BOUND`，reason 为 `IDENTITY_REPLACED`，通过相同 request_id 关联；其他会话各自记录 SESSION_REVOKED。追加 `IDENTITY_REPLACE_REJECTED` 事件用于进入领域后的可归因业务失败；认证中间件拒绝保持原审计行为，基础设施失败不伪装成成功或账号冲突。

事件只记录现有 kind、digest hint、user_id、sid、request_id 等允许字段；不增加明文目标、验证码或完整摘要。审计仍是有界异步记录，不成为换绑事务的提交依赖。新增枚举需同步解析/序列化和管理审计过滤测试，旧枚举数值不变。

验证覆盖纯 HTTP 协议、旧自定义 consumer.Service 编译、直接领域调用、真实 PostgreSQL 并发与回滚、真实 Redis/隔离 SMTP 的 accountsvc 双会话换邮箱链路。服务严格入口增加对应必需测试；不需要实际短信/邮件服务商或 OIDC 平台。

## Risks / Trade-offs

- [近期认证并非强制旧地址验证码证明] → 明确继承现有敏感操作契约；强证明/凭据找回另立设计。
- [预检与事务间身份发生变化] → 事务内持锁复核全部写入前提；验证码先于事务消费，避免等待 Redis 时持有数据库写锁。
- [验证码已消费但事务失败] → 明确客户端重新获取码及冷却规则，不宣称跨库原子性。
- [换绑成功但响应丢失] → 通过身份列表确认，旧路径重试404，不引入隐式第二次替换。
- [Redis 故障使其他 access 暂时仍可用] → 保持已有故障策略并验证日志/内省；数据库 refresh 撤销保持有效。
- [新增枚举影响旧消费者] → 记录追加值兼容说明；旧数据读取、冻结 SQL 和现有错误语义保持。

## Migration Plan

先部署包含新领域方法与消费者路由的库/服务，再由客户端增加换绑流程；旧客户端仍使用原绑定/解绑协议。无新增数据库表或结构迁移，不修改冻结基线。自定义 consumer.Service 可按需实现可选接口。

回退前停止调用新端点；身份软删除/新建和会话撤销仍是旧版本能够理解的存储形式。核验新增审计/撤销枚举在旧版本查询中的表现，不能把代码回退等同于恢复旧绑定或自动恢复被撤销会话。真实环境验收继续按用户要求暂缓。
