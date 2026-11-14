# Design

## Context

动机见 [proposal.md](proposal.md)。现有 `user/query.sql` 的 `FindActiveIdentityByDigests` 按 kind、多版本摘要与 `delete_time IS NULL` 查找身份，但单行结果不能检查跨版本重复，也没有同时读取账号状态及资料。`user/service.go` 已有 PHONE/EMAIL 规范化，`pii.Digester.AllDigests` 提供轮换兼容检索。

`user/service_lifecycle.go` 的软删除保留身份与资料，最终匿名化才清理身份和资料。`user-basic-profile` 明确要求公开资料隐藏注销中账号头像；本变更新增受服务权限保护的结果视图，不能复用公开视图的过滤规则。

`internal/accountsvc/serviceauth` 已实现统一注册表、权限判断和 Basic/Bearer 挑战，当前权限白名单只有两项。`internal/accountsvc/http.go` 将服务接口独立装配在 `/v1` 下；`Auth.Users()` 已对宿主暴露领域服务。通用 `audit.Event` 已支持 SERVICE 主体。

## Goals / Non-Goals

**Goals:** 以现有领域、实例隔离和服务授权为基础提供一次一致的只读解析，保留最小资料与可核验的失败语义。

**Non-Goals:** 不增加数据库表、索引或 Redis 缓存；不引入业务关系检查或新的客户端 SDK；不更改公开资料、消费者登录、管理员授权及现有两项服务能力的契约。

## Decisions

### D1：已确认的接口方案与备选

用户已确认单条 PHONE/EMAIL 解析、404 未匹配、复数权限，以及返回 user_id、state、昵称和头像；冻结与注销冷静期均返回当前关联。讨论过仅返回 ID、再查询资料的方案，用户选择携带基础资料，便于业务服务按需使用。账号是否参与业务完全交由调用方。

另一种可行的接入方式是使用管理员列表按身份筛选，但这会迫使业务服务取得管理身份和更宽的数据视图；选择独立服务能力。以下请求大小、错误名、内部函数和审计映射是本设计提出的实施默认值，不表述为用户逐项指定。

### D2：领域入口与单语句快照

在 `user.Service` 新增 `ResolveIdentity(ctx, input)`，输入类型包含 Kind、Subject、可信调用者的审计 Actor 与 Meta；返回专用 `ResolvedIdentity`（UserID、State、DisplayName、AvatarURL）。服务调用者必须提供非空、合法的 SERVICE 主体，HTTP 从认证结果生成，禁止从 JSON 接收；宿主直接调用时自己负责认证授权并传入稳定主体。输入语义和规范化在领域层执行，HTTP 长度／JSON 校验在适配器执行。

新增 sqlc 查询，身份按 kind 与 AllDigests 匹配并 LEFT JOIN 账号；最多读取两条匹配身份以识别不变量异常。LEFT JOIN 保留悬空引用供领域显式拒绝；不添加账号软删除条件，不调用要求 ACTIVE 的登录校验。零条是未找到，多条（即使指向同一账号）、悬空或 DELETED/非法状态关联均为内部不变量错误。单个 SQL 语句保证身份、状态及资料来自同一快照；沿用现有身份索引与账号主键，无需额外锁或迁移。

不选择“先查身份，再普通查询账号”的方案，因为 READ COMMITTED 下两次语句可能混用解绑／匿名化前后数据。查询与写操作同时发生时允许返回语句快照中的旧结果；不承诺响应发送时关联仍有效，也不为调用方业务事务持有账号锁。查询开始前已提交的变更必须可见。

正常查询不读取密文、不解密、不读取 Redis、不调用提供方，也不执行 sender 或身份登录启用策略。数据库读路径只读；审计独立投递，不处于身份写事务内。

### D3：HTTP 适配与协议优先级

新增独立 `internal/accountsvc/identityresolve` 处理器，装配路径 `POST /v1/identities:resolve`。按“能力配置 → 唯一 Authorization 与认证 → 权限 → 方法 → Content-Type → 查询参数／JSON → 领域查询”处理；权限拒绝不得触发 JSON 解码或数据库查询。

`application/json` 可带合法媒体类型参数。解析采用已有有界 JSON 设施，限制 4 KiB，拒绝未知键、重复键、非法 UTF-8、尾随内容及所有 query 参数。输入只有 kind、subject；不新增任意扩展 JSON。接口即使使用 POST 也不修改账号数据，目的在于把敏感身份放入请求体，避免落入 URL 记录。

身份解析 realm 使用 `accountsvc-identity-resolution`，固定观测 endpoint 为 `identities_resolve`。403、401、挑战头以及 JWKS 503 沿用统一机制。无任何解析授权为 `IDENTITY_RESOLUTION_NOT_CONFIGURED`；输入错误为 `INVALID_ARGUMENT`；未匹配为 `NOT_FOUND`；数据库连接类故障与最多 3 秒读取超时为 `DEPENDENCY_UNAVAILABLE`；不变量与其他内部错误为 `INTERNAL`。错误正文使用 apierror，固定 500 消息；全部响应 no-store。

成功 DTO 固定四字段，空昵称／头像为字符串空值，不返回 profile_attributes、identity_id 或手机号／邮箱。PENDING_DELETION 的服务视图返回保留资料；公开批量资料隐藏规则不变，加入交叉回归用例。

### D4：最小权限与审计

权限目录添加 identities.resolve，配置解析与认证器继续共享同一注册表。原 token scope／角色不授予能力。复用现有 Basic 新旧秘密及 Bearer 验证器；服务接口均独立判断自己的配置状态，默认不授权解析。

使用已有通用审计 Recorder，不把服务查询伪装成消费者登录。事件 Action 使用 `IDENTITY_RESOLVE`，遵循现有大写 action 格式，区别于权限字符串 `identities.resolve`；Source 使用配置 AuditSource；Actor.Type=SERVICE、Namespace=AuditNamespace、ID=认证后的客户端 ID，Basic/Bearer 的同一客户端使用同一审计身份。成功 Targets 使用 Type=USER、Namespace=AuditNamespace、ID=目标 user_id，不填写 Name；未找到与失败无目标。Data 只含固定 kind 与 outcome（resolved/not_found/unavailable/internal），不含 subject、其摘要、资料或认证凭据。Result 映射已有成功／失败枚举，RequestID 由宿主可信回调提供。生成 UUIDv7、发生时间并 Freeze 后投递，沿用现有异步输出及失败观测；审计失败不影响查询。

嵌入式宿主通过相同领域入口提供 SERVICE 审计主体，库不自行授予服务权限。日志仅记录安全分类，数据库底层错误不得未经净化直接输出，以免 SQL 参数进入日志。身份枚举风险由显式服务授权及宿主网关访问／限流策略控制，本次不新增 Redis 限流产品接口。

审计读取使用通用 Reader 或宿主外部审计系统。既有管理员账号认证时间线仅筛选 `authentication.Actions()`，本次不向该目录加入 `IDENTITY_RESOLVE`，也不扩展管理员 DTO、枚举或 SDK。通用事件沿用保留与账号清理契约；事件不复制个人资料，异步延迟入库也不得重新带回已匿名化资料。

### D5：覆盖登记与文档

当前 `sdks/http-coverage.json` 与 `scripts/check-sdk-coverage.mjs` 只登记消费者／管理员 OpenAPI，不能把服务凭据塞进现有 SDK。实施时为该服务操作增加明确的“服务端操作、不适用客户端 SDK”登记（推荐独立 service surface、sdkApplicable=false），同时维护可机器校验的协议定义与真实 Go 协议测试引用。仅对已声明的服务 surface 允许此模式，原两类 surface 继续强制 SDK 方法、模型和测试覆盖，未知 surface 仍拒绝；不伪造 SDK 源文件或用例。

README 说明宿主调用与授权职责；accountsvc 文档、配置模板说明新权限、四字段、状态语义、返回码、无缓存及只供服务调用。新增服务协议登记不隐含创建服务 SDK，也不补做其他既有服务端点的覆盖重构。

## Risks / Trade-offs

- 账号识别能力可被滥用 → 独立显式授权、调用审计、最小结果和 no-store；业务服务负责向最终用户展示的权限。
- 查询后身份可能变化 → 明确时点快照，业务关键动作按自身需要重新验证，不把解析结果当授权凭据。
- 历史异常在多版本摘要下被单行查询掩盖 → 新查询检测最多两条匹配并拒绝异常，不修改登录旧路径。
- 服务视图与公开资料视图不同 → 分离 DTO 和领域结果并以注销中账号做交叉回归，不能沿用公开过滤误隐藏资料。
- 覆盖检查扩展可能放松门禁 → 仅新增显式服务登记分支，保留消费者／管理员的原严格校验与反例测试。

## Migration Plan

1. 无数据库迁移；发布新版后才在所需客户端 PERMISSIONS 中显式加入 identities.resolve。
2. 按现有启动快照机制重启／排空全部实例；原有两项权限不自动扩张。
3. 如回退旧二进制，先移除 identities.resolve 并恢复匹配配置；旧版本会拒绝未知权限。
4. 实施后的验收证据统一写入本变更 verification.md；本次工件创建不代表代码实现或集成验证完成。
