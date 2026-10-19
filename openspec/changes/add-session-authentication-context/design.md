## Context

动机与范围见 [proposal.md](proposal.md)。基线是 develop 的 15309bf，v0.3.0 已发布，0001 已冻结。当前 session 只有 user_id、设备、auth_time、刷新与撤销字段；普通登录和补全统一经 establishSession 建会话。重新认证已按用户、会话锁序验证归属、期限并更新 auth_time。解绑目前不撤销会话，换绑撤销其他会话但无条件保留当前会话。

Authenticate/内省只有 JWT 与 Redis 吊销检查，不能为客户端描述引入数据库热路径。HTTP 设备头既是同设备登录替换的输入，也是 pending 设备一致性的一部分；新增描述不应改变这两个契约。源码依据：user/service.go、user/service_me.go、user/service_identity.go、user/service_identity_replacement.go、user/service_signin_completion.go、user/query.sql、httpapi/enduser/{handler,request,response}.go。

## Goals / Non-Goals

**Goals:**

- 用服务端当前认证依据决定身份移除的会话撤销范围，支持多身份、来源替换及未知存量会话。
- 通过当前会话详情提供最新、按调用者隔离的身份摘要和可选客户端信息，支撑退出提示。
- 保留原认证方式、重新认证时长、token 格式、刷新宽限、故障模式和宿主资源责任。
- 让新字段可以逐步接入，使用真实递增迁移并覆盖恢复、匿名化和并发。

**Non-Goals:**

- 不记录不可变的“所有历史登录身份”作为撤销依据，不新增 MFA、第三方重新认证端点或可信设备认证。
- 不在 JWT、内省或身份列表增加来源/解绑影响字段；不新增按任意会话 ID 获取详情的消费者路由。
- 不新增 IP/位置/ASN 存储、GeoIP 服务、原始 UA 持久化、设备指纹、硬件标识或每业务请求活动上报。
- 不给管理面添加认证身份详情，管理会话列表仅扩展与消费者独立的客户端描述 DTO。

## Decisions

### 1. 认证依据与历史审计分开

采用可变集合，表示“会话当前依赖的具体身份”，不是所有历史证明。普通成功登录关联本次验证、锁后确认的 identity.id；新身份须先创建再取实际 ID。成功补全关联此次流程全部已验证且最终归属该账号的具体身份，去重；不关联账号其他未参与认证的身份。来源集合不由客户端提交。

成功手机/邮箱重新认证用本次验证的单个身份替换整个集合，同时更新 auth_time。刷新、新增绑定和换绑新目标的 BIND 证明均不替换来源；第三方仍通过重新登录建立新会话。来源替换保留同一 sid 和 refresh，不追溯修改已签 JWT，历史动作继续按现有审计记录。

备选是不可变初始来源或累计来源；前者使重新证明其他身份后仍无法保留会话，后者不断扩大撤销关联，均不符合讨论中采用的当前依据模型。任一来源命中即整会话撤销，不自动删去一个来源后保留会话，也不宣称 pending 的多个身份构成 MFA。

### 2. 持久化模型及不变量

新增真实生产迁移 0002_session_authentication_context（实施时如已有其他生产迁移，按当时下一编号递增；测试专用下一版本不占生产号）：

- session.authentication_identities_known BOOLEAN NOT NULL DEFAULT false。旧行和旧程序写入的行保持 false；新代码的成功建会话/重新认证显式设为 true。
- session_authentication_identity(session_id, identity_id)，复合主键，TEXT COLLATE C 及既有 s_/i_ 格式 CHECK；反向索引 idx_session_authentication_identity_identity_id_session_id。没有外键。
- session_client_info：复合主键 (session_id, snapshot_kind)，snapshot_kind 为叶子 enum 的 SMALLINT（INITIAL/LATEST）；可空 client_type、device_type 同样使用 SMALLINT override，其他有限文本列为 os_name/os_version/browser_name/browser_version/app_name/app_version/device_model，另有 observe_time、create_time、update_time。无需独立资源 ID，无外键，无枚举值 CHECK。

选择关联表使按身份定位会话和清理可以直接索引；选择最多两条有类型的客户端快照行，避免复制两组大量 session 列或任意 JSON 数据入口。身份关联的主键支持按会话读取；客户端快照主键支持查询和清理，不为每个展示字段创建索引。

同一事务内保证：已知且可用的会话至少有一个来源；来源身份存在、属于 session.user_id，并在写入时已验证、未删除、类型启用。查询必须用 session.user_id 限定，不能只信任外部 session_id。来源已知但集合为空是内部不变量错误，不伪装成未知。已撤销会话允许保留历史关联直到有界清理；匿名化可以先撤销再删除关联和客户端快照。

### 3. 身份移除和事务边界

解绑仍先检查账号、身份归属、当前和剩余集合完整性。增强直接 Go 解绑路径，使其与 HTTP 一样验证 user scope、近期认证和当前数据库会话；采用用户锁 → 会话锁的已有顺序，必要时按 session.id 稳定排序获取多个会话锁。刷新只锁会话的路径不得反向请求用户锁。

解绑目标集合：同一账号、revoke_time IS NULL，并且存在目标来源关联或 authentication_identities_known=false。包括 refresh 已到期但仍可能有有效 access 的行。身份软删除与目标集合的数据库撤销同事务提交；返回被撤销 sid，提交后按既有流程写 Redis 吊销集并记录脱敏审计。新增 IDENTITY_UNBOUND 撤销原因只追加枚举，指标原因归类、审计映射和校验一并更新。

换绑保留原撤销其他会话行为；当前会话仅在来源已知且不含旧身份时排除。新 BIND 证明不将当前来源自动迁移到新身份；命中旧身份或来源未知的当前会话同样撤销，仍返回原 200 新身份资源，不发 token。解绑仍返回 204；操作成功后当前会话失效不是失败或回滚，客户端按新查询结果及后续认证失效处理。查询给出的影响是预览，不承诺与未来操作之间没有并发变化。

其他身份重新认证先提交：当前来源已替换，后续解绑旧身份可以保留该会话。解绑先提交并撤销会话：重新认证锁后拒绝，验证码已消费不恢复。普通登录/pending 最终提交同样复核来源关系，不能在解绑提交后使用旧快照建立会话。撤销后刷新（含宽限结果）和补全结果重放均必须拒绝；已撤销会话不能被旧快照覆盖或来源替换复活。

Redis 吊销失败不回滚已完成的解绑/换绑，refresh 由 PostgreSQL 拒绝；access 与内省保留既有 fail-open 和告警。当前会话详情直接检查数据库，可以在 Redis 故障时明确拒绝已撤销会话，但不据此改变普通 Authenticate。

### 4. 当前会话查询采用方案二

新增 GET /users/me/sessions/current，允许 user 和 user:bind；user:undelete 保持专用恢复范围，返回 403。从已验证 Principal 的 user_id/sid 查询数据库，要求所属关系正确、会话未吊销且刷新期限尚未到；缺失/已撤销/已过期返回 401 TOKEN_INVALID 和 Bearer challenge。可识别依赖不可用返回 503，其余内部错误固定 500，不把数据库错误转成凭据错误。

新增可选 CurrentSessionService 能力（建议方法 GetCurrentSession(ctx, Principal)）；不扩张现有 enduser.Service 必需方法集，自定义实现缺能力返回 503 SESSION_QUERY_NOT_CONFIGURED。领域层复核 scope 和会话条件，HTTP 仅转换协议。用一个 SQL 快照读取会话、来源及客户端快照，或等价的一致性读，防止 auth_time 与来源来自不同重新认证版本。

响应保持资源命名，name 使用真实 s_，不是 current；已有 device_id/device_name/create_time/last_used_time/is_current 同义。新增 auth_time 为 RFC3339、authentication_identities_known 布尔和 authentication_identities 数组，每项只含 name、kind，并按 identity.id 稳定排序。旧来源未知返回 false 与 []，不能解释为解绑安全。另有可选 initial_client_info、client_info（最近快照）、client_info_update_time。客户端快照内零信息字段省略；旧会话可完全没有客户端快照。

示意（省略号仅为文档占位）：

```json
{
  "name": "users/u_…/sessions/s_…",
  "device_id": "browser-instance-1",
  "device_name": "Chrome on Windows",
  "is_current": true,
  "auth_time": "2026-10-19T08:00:00Z",
  "authentication_identities_known": true,
  "authentication_identities": [
    {"name": "users/u_…/identities/i_…", "kind": "WECHAT"},
    {"name": "users/u_…/identities/i_…", "kind": "EMAIL"}
  ],
  "client_info": {"client_type": "WEB", "os_name": "Windows", "browser_name": "Chrome", "device_type": "DESKTOP"}
}
```

响应 Cache-Control: no-store。身份 name 用于精确匹配，kind 仅用于展示；不得按类型推断具体来源。旧 JWT 仍可携带旧 auth_time，接口返回的是数据库最新值；敏感操作仍使用客户端新取得的 access，不让查询自动升级旧 token 权限。消费者和管理员会话列表可增加 client_info/client_info_update_time，但不附加其他会话的认证身份。

### 5. 可选客户端描述和采集边界

新增 user.ClientInfo，通过 user.Meta 的可选字段传递，Device.ID/Name 与 pending 设备匹配语义不变。保留方法签名；宿主可通过可选 Deps.ClientInfo 请求解析回调提供规范化值，默认 HTTP 适配器执行有界、无 I/O 解析。HTTP 与直接 Go 调用共用字段校验，纯函数解析不在领域层读取 HTTP。

字段和建议请求头：

| 字段 | 输入头 | 限制 |
|---|---|---|
| client_type | X-Client-Type | 可选 WEB/NATIVE；缺失为未知 |
| os_name / os_version | X-Client-OS-Name / X-Client-OS-Version | 名称 64 字符，版本 32 字符 |
| browser_name / browser_version | X-Client-Browser-Name / X-Client-Browser-Version | 名称 64 字符，版本 32 字符 |
| app_name / app_version | X-Client-App-Name / X-Client-App-Version | 名称 64 字符，版本 32 字符 |
| device_type | X-Device-Type | 可选 DESKTOP/PHONE/TABLET |
| device_model | X-Device-Model | 64 字符 |

文本要求有效 UTF-8、无控制字符并去除首尾空白，整体规范化描述最大 2048 字节。显式新头/直接 Go 值非法时在凭证消费前拒绝：普通端点 400 INVALID_ARGUMENT / INVALID_CLIENT_INFO，/token 使用 OAuth invalid_request；不会改变原 X-Device-Id/Name 校验。缺失、无法识别的 UA 或 Client Hints 不拒绝认证。

优先使用合法显式字段，缺失项由可用的标准 Client Hints 补充，最后由 UA 解析补充。原始 UA 和相关 hints 输入各自合计限制 2048 字节，超限或畸形忽略自动解析部分，保留合法显式字段；不保存/打印原始值，不为获取 hints 发起额外网络请求，不要求客户端高熵信息。浏览器识别成功才推断 WEB；原生客户端推荐显式 NATIVE；iOS/Android 是系统，与客户端类型分开。未知枚举不写 0；未知系统/浏览器不编造版本。

所有这些值都是描述性声明，不能用于授权、限流身份、同设备合并或跳过重新认证。默认不使用 X-Forwarded-User-Agent 等转发头；BFF 需要通过明确宿主回调提供原客户端描述，不能把 BFF 的 HTTP 库识别为终端浏览器。新增自定义头的 CORS/代理允许清单由宿主配置，Client Hints 按浏览器支持可缺省。

### 6. 快照更新与 pending 的关系

普通登录有可用元数据时建立 INITIAL 与 LATEST 两行，observe_time 为服务器观察时刻。pending 票据加密 payload 保存初始规范化 ClientInfo 和观察时间；补全请求缺失这些字段时沿用初始快照；最终请求提供有效描述时以其更新 LATEST，INITIAL 仍为首次证明阶段观察值。旧 pending payload 缺字段可继续完成，不因本次展示字段变更主动使原票据失效；首次证明缺描述而完成时才获得描述的流程只建立 LATEST，不伪造 INITIAL。

成功重新认证和新一轮刷新，在原事务内更新 LATEST；有值的新字段覆盖对应旧值，缺失字段保留，未观察到任何有效字段则不写入快照或更新时间。原 INITIAL 存在后不改写；迁移来的旧会话重新认证/刷新仅补 LATEST，不伪造创建时环境。客户端类型发生显式切换时先清除不适用字段（NATIVE 清除 browser_*，WEB 清除 app_*）；版本只在对应名称一致或本次也提供名称时更新，新名称而无新版本则清除旧版本，避免拼接不同软件的版本。

宽限 refresh 重试只返回原 pair，不更新描述或观察时间；成功补全的幂等重放不更新已提交快照。失败的认证/事务不持久化元数据。last_used_time 保留既有会话语义，client_info_update_time 单独表示元数据最近观察时间，均不冒充所有业务活动时间。普通 Authenticate/内省不解析或写入这些字段。

### 7. 清理、隐私和兼容

会话有界清理在同一事务中删除选中会话的来源关联、两类客户端快照及会话行；禁止无界全表清理。账号匿名化覆盖该账号所有历史会话的新增描述与关联，并与宿主回调同事务回滚。新描述不进入审计详情、日志正文或指标标签；只记录既有允许的资源关联、动作和有界原因。来源变更和身份移除使用现有异步审计，不以审计成功作为会话写入的前提。

新客户端摘要不含 IP、原始 UA、完整手机号/邮箱、第三方 subject 或凭证。不将 DeviceID 提升为不可伪造标识。现有列表字段保持，新增字段可省略；token、内省、设备头及认证请求体保持。扩展 Meta/SessionInfo/Deps 等公开 Go 结构时，文档说明 keyed 初始化用法及位置式复合字面量需要修改；不宣称所有 Go 源码完全兼容。

## Risks / Trade-offs

- 来源未知会话被保守撤销可能扩大退出范围 → 已获用户明确确认；仅身份移除时触发，升级不撤销，重新认证可建立已知来源。
- 查询与解绑之间可能发生重新认证 → 查询只是快照；实际撤销以同事务结果为准，客户端收到会话失效后清理凭据。
- 元数据可伪造且解析会不完整 → 只作描述，允许未知，严格有界；不增加客户端指纹、硬件标识或网络依赖。
- UA 解析依赖与样本维护有成本 → 在 HTTP 适配层隔离并允许宿主回调替代，实施首步比较候选许可/体积/维护性，结果记录于 verification.md；不得手写声称覆盖所有浏览器的匹配器。
- 锁序或关联清理遗漏可能导致死锁/孤儿 → 复用账号先于会话的锁序，增加真实 PostgreSQL 并发、回滚与引用一致性测试，并检查反向索引执行计划。
- Redis 故障期间 access 仍可能有效 → 保留既有明确故障边界，不承诺强即时撤销；数据库拒绝 refresh 和新详情查询。
- 旧程序写入或解绑不遵循新约定 → 用协调升级和回退限制防止旧身份写路径重新上线；DDL 兼容不等于安全行为兼容。

## Migration Plan

1. 保持已发布 0001、固定哈希、源快照与全部正式 tag 原样；新增递增 up/down，公共 Down 继续无 I/O 拒绝。同步基线识别，使合法新 v2 可接受、旧撤回 v2 仍按结构拒绝；不以 version 数字代替结构核验。
2. 非空 v1 升级后来源未知、描述缺失，原 ID、密文、会话和 token 保留；不从当前身份列表或尽力审计回填来源，不自动撤销旧会话。新建会话显式记录来源，旧会话成功重新认证转为已知。
3. 部署时暂停身份变更入口或统一切换认证副本，完成迁移和新副本就绪后恢复入口；旧副本不得继续承接解绑/换绑或覆盖新来源。网关明确开放新详情 GET，按实际使用透传客户端头。部署命令及生产窗口由宿主执行。
4. 新来源逻辑启用后不直接回退到 v0.3.0 处理身份变更。优先前向修复；必须应用回退时停写并另行核验，确保旧代码不会恢复旧解绑语义。数据库恢复先落独立空库，不能以清空 Redis 代替恢复验证。
5. 在隔离资源验证空库安装、非空升级、重复 Up、dirty/旧结构拒绝、实例隔离、恢复演练及旧 token 行为；生产迁移加入后，把 migrations/testdata 的“下一版本”相应前移且保留历史拒绝覆盖。

## References

- [Clerk SessionActivity](https://clerk.com/docs/reference/backend/types/backend-session-activity)：客户端描述作为活动信息，支持浏览器/设备类型等可选字段。
- [Chrome Client Hints](https://developer.chrome.com/docs/privacy-security/user-agent-client-hints)：字段可缺省，平台与浏览器信息分开。
- [Android 应用实例标识建议](https://developer.android.com/identity/user-data-ids)：实例标识与硬件标识分开。
- [OWASP 会话环境检测](https://cheatsheetseries.owasp.org/cheatsheets/Cookie_Theft_Mitigation_Cheat_Sheet.html)：环境变化不能单独证明劫持。这里仅采用描述采集，不实现新的风控引擎。
