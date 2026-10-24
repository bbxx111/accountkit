# 功能与兼容对照

## 统一凭证信封与身份类型字符串

本节是未发布的开发分支变化，对 v0.5.0 不兼容，不提供旧数据迁移。

- **HTTP 请求**：`signInWithCode`、`signInWithIdp`、`completeSignIn`、`reauthenticate`、绑定和换绑的请求体统一为 `{"kind","credential"}`。旧写法与新写法对照：

  | 旧 | 新 |
  |---|---|
  | `{"phone":{"target":…,"code_id":…,"code":…}}` | `{"kind":"PHONE","credential":{"target":…,"code_id":…,"code":…}}` |
  | `{"email":{…}}` | `{"kind":"EMAIL","credential":{…}}` |
  | `{"wechat":{"app_id":…,"code":…}}` | `{"kind":"WECHAT","credential":{"app_id":…,"code":…}}` |
  | `{"apple":{"id_token":…,"nonce":…}}` / `{"google":{…}}` | `{"kind":"APPLE"\|"GOOGLE","credential":{"id_token":…,"nonce":…}}` |
  | `completeSignIn` 的 `phone`/`email` 成员 | 同一请求体内的 `kind` + `credential` |

  旧成员返回 `400 MALFORMED_BODY`，`CREDENTIAL_ONEOF` 不再出现；缺少 `kind`/`credential` 或 credential 字段不合法为 `CREDENTIAL_INCOMPLETE`。credential 内的未知字段从 `MALFORMED_BODY` 改为 `CREDENTIAL_INCOMPLETE`。发码接口的 `channel`/`target` 不变。
- **Go API**：`enum.IdentityKind` 改为字符串底型，常量值即 API 名，零值 `""`；`Valid()` 只检查格式 `^[A-Z][A-Z0-9_]{1,31}$`，新增 `IsBuiltin()`；`ParseIdentityKind` 不再拒绝格式合法的未知类型。按数值比较、持久化或用 `IdentityKind(5)` 构造的代码须改为字符串。`idp.WeChatVerifier`、`AppleVerifier`、`GoogleVerifier` 与 `user.Deps` 的 `WeChat`/`Apple`/`Google` 字段移除，改为 `user.Deps.Providers []idp.Provider`；`user.IdpCredential` 改为 `{Kind, Credential json.RawMessage}`；`user.IdentityCapabilities` 改为 `{Phone, Email bool; Providers []enum.IdentityKind}`。`accountkit.Config` 的微信/Apple/Google 配置不变。
- **策略与输出**：显式 Enabled 中的非内置类型必须已登记。`EnabledIdentityKinds`/`RequiredIdentityKinds` 的输出顺序保持内置类型的历史顺序，配置类型按名称排在其后。
- **数据库**：当前 0001 的 `identity.kind` 改为 `VARCHAR(32) COLLATE "C"` 加格式 CHECK。`identity.kind` 仍为 SMALLINT 的 version 1 被无写拒绝，须使用新空资源；旧二进制不能在新结构上运行，见[迁移手册](migrations.md#当前初始基线)。
- **进行中的登录补全**：补全载荷中的类型编码与配置指纹都已变化，升级前创建的 pending 流程会被拒绝，客户端须重新开始。补全指纹现在也覆盖 Google 的 client ID 配置。
- **指标**：宿主登记的其他第三方类型使用固定方法值 `provider` 与依赖操作 `verify_provider`，类型名不进入标签。

## Google 登录与显示名长度

`IdentityKind` 追加 `GOOGLE = 5`。`POST /users:signInWithIdp` 增加与 `apple` 互斥的 `google` 成员，字段为 `id_token` 和原始 `nonce`。未配置 `GoogleClientIDs` 时该登录关闭。已验证邮箱只作为 `hint_email` 预填，补全仍要显式 `target`。`Required` 不能包含 `GOOGLE`。

显示名上限由 32 放宽到 128 个 Unicode 码位，适用于 `PATCH /users/me` 和 Google 首次填充。把 32 写死的宿主需要同时放宽。旧二进制不能解释 kind 5；回退二进制前不能保留已写入的 Google 身份行。去掉客户端 ID 只停止新的 Google 登录。

## 支付宝 App 极简登录与空资料补填

新增内置 `ALIPAY`、`AlipayApp`、`AlipaySubjectMode` 及 `Config.AlipayApps` / `AlipaySubjectMode` / `AlipayGatewayURL`，沿用统一 `{"kind":"ALIPAY","credential":{"app_id","code"}}`。未配置应用时关闭；显式 Enabled 的宿主需加入 ALIPAY，Required 仍只接受 PHONE/EMAIL，纯支付宝账号须显式 Required 为空。只支持普通公钥模式，无 SQL/schema 或迁移变化。

UNIONID 默认使用 `unionid:<union_id>`，OPENID 使用 `openid:<app_id>:<open_id>`，不在模式间回退，也不以 `user_id` 兜底。跨 App 账号共享取决于支付宝应用分组及实际相同 UnionID；模式、分组或信任配置变化不提供自动迁移/合并。pending 指纹覆盖应用集合、模式、网关、公钥材料，变化后需重新授权；重排列表、等价 PEM 表达不影响指纹。所有副本应保持一致。

（已被下文“提供方资料补填统一”取代）ALIPAY 与其他提供方有明确资料差异：首次及后续成功登录都按字段补填本次可信且净化声明对应的空显示名/头像，用户主动清空也可以补填；不覆盖非空值、不使用历史 profile 补偿本次资料失败。重复绑定已有身份不触发后续补填，GOOGLE/APPLE/FACEBOOK/OIDC 保持仅首次关联身份时填充。写入遵守账号锁、生命周期和事务回滚，pending 完成/重放不再次请求支付宝。资料查询失败可降级，外层取消终止操作。

指标追加 `alipay`、`verify_alipay`、`alipay_profile_unavailable`，不输出 AppID 或秘密。禁用后历史 ALIPAY 身份仍可列出、解绑和匿名化；回退前验证目标版本处理未知历史类型的能力，不通过清库或改 subject 回退。客户端需极简 SDK、已安装支付宝和一次性 state 核对，code 兑换失败后重新授权；真实 App/应用分组由宿主另行检查，库侧采用 HTTP 自动契约验收；详情见[支付宝接入指南](alipay-sign-in.md)。

## QQ App SDK 登录

新增内置 `QQ`、`QQSubjectMode` 及 `Config.QQAppIDs` / `QQSubjectMode` / `QQAPIBaseURL`，沿用统一凭证信封：`{"kind":"QQ","credential":{"app_id","access_token"}}`。仅支持 QQ 互联移动 SDK；无网页回调、授权码或 AppKey 配置。

服务端调用 `oauth2.0/me` 校验令牌，`client_id` 必须等于请求的 `app_id`，另一 App 的令牌为 400 `IDP_APP_NOT_ALLOWED`。QQ access_token 与 Facebook Classic 一样不做一次性限制，有效期内可重复登录；客户端须保密令牌。

UNIONID 默认使用 `unionid:<unionid>`，需在 QQ 互联开通 UnionID 打通，未开通时登录返回固定 500；OPENID 使用 `openid:<app_id>:<openid>` 且不请求 UnionID。切换模式会让既有 QQ 身份无法命中，不自动迁移或合并。

（已被下文“提供方资料补填统一”取代）登录补填提供方集合现为 ALIPAY、QQ：两者首次及后续成功登录都按字段补填仍为空的显示名与头像；其他提供方仍只在首次写入身份时填充。QQ 头像仅对 `thirdqq.qlogo.cn` 的 `http://` 地址升级为 `https://`，其他 HTTP 地址丢弃。指标追加 `qq`、`verify_qq`、`qq_profile_unavailable`。无 schema/迁移变化。

## 微博 App SDK 登录

新增内置 `WEIBO` 及 `Config.WeiboAppIDs` / `WeiboAPIBaseURL`，沿用统一凭证信封：`{"kind":"WEIBO","credential":{"app_id","access_token"}}`，其中 `app_id` 即微博开放平台 AppKey。仅支持微博移动 SDK；无网页回调、授权码、取消授权回调或 AppSecret 配置。未配置 AppKey 时行为与升级前一致；显式设置 `EnabledIdentityKinds` 的宿主须手动加入 `WEIBO`。

服务端调用 `oauth2/get_token_info` 校验令牌，`appkey` 必须等于请求的 `app_id`，另一 App 的令牌为 400 `IDP_APP_NOT_ALLOWED`；`expire_in` 为负视为令牌失效。微博 access_token 与 QQ 一样不做一次性限制，有效期内可重复登录；客户端须保密令牌。

微博 uid 全局唯一且公开，subject 固定为 `uid:<uid>`，没有标识模式配置，也不写 unionid/openid 元数据；同一微博用户经任一已登记 App 登录都命中同一身份。uid 只作识别，不代表持有者证明，客户端也无法提交 uid。增删 AppKey 不改变既有身份命中，但会使未完成的登录补全失效。

（已被下文“提供方资料补填统一”取代）登录补填提供方集合现为 ALIPAY、QQ、WEIBO：三者首次及后续成功登录都按字段补填仍为空的显示名与头像；其他提供方仍只在首次写入身份时填充。微博头像只接受 HTTPS，不做协议改写，选中的头像字段为 HTTP 时丢弃头像。未审核应用超出测试用户数（21321）等应用权限错误返回固定 500。指标追加 `weibo`、`verify_weibo`、`weibo_profile_unavailable`。无 schema/迁移变化。见[微博接入指南](weibo-sign-in.md)。

## 提供方资料补填统一（ProviderProfileBackfill）

- 补填不再区分提供方。默认（`PROVIDER_PROFILE_BACKFILL=true`）下，GOOGLE、OIDC、FACEBOOK 等提供方的已有账号再次登录、绑定或补全关联，也会补填空的显示名与头像；用户主动清空的值会在下一次第三方登录时被重新填充。
- 设为 `false` 后，已有账号的第三方登录、绑定（含同账号重复绑定）与补全关联都不写账号资料；ALIPAY、QQ、WEIBO 也不再后续补填。此前所有提供方在绑定到已有账号时都会填充空资料，关闭后不再填充。
- 新建账号（第三方直接注册、补全新建账号）始终填充，不受配置影响。
- `provider_meta.profile` 记录规则不变；HTTP、数据库无变化。

## Facebook 登录与数据删除回调

`IdentityKind` 追加 `FACEBOOK`（内置第三方类型）。凭证沿用统一信封，`credential` 为 `{"id_token","nonce"}`（Limited Login）或 `{"access_token"}`（Classic Login）。配置 `FacebookAppID` 与 `FacebookAppSecret` 后启用，未配置时行为与升级前一致；显式设置 `EnabledIdentityKinds` 的宿主须手动加入 `FACEBOOK`，`Required` 不能包含它。新增配置 `FacebookGraphAPIVersion`（默认 `v26.0`，默认值升级属于兼容变更）、`FacebookNonceTTL`、`FacebookGraphAPIBaseURL`、`FacebookJWKSURL`，环境变量见 README。补全指纹覆盖 Facebook 配置，升级前创建的 pending 流程在配置变化后被拒绝。

新增 `Auth.HandleFacebookDataDeletion`、`accountkit.ErrFacebookNotConfigured`、`idp.ErrInvalidSignedRequest`、`user.Service.DeleteProviderIdentityData`、`user.ProviderDeletionResult` 与审计事件 `PROVIDER_DATA_DELETION_PROCESSED`。无数据库迁移。指标新增方法值 `facebook`、依赖操作 `verify_facebook` 与 scenario `facebook_cached_key_fallback`。回退到旧版本后，已写入的 FACEBOOK 身份仍可列出、解绑和匿名化，但不能登录。

## 内置头像属性

v0.5.0 新增 `user.Me`、`user.AdminUser`、`user.PublicProfile` 的 `AvatarURL string` 和对应两面 JSON `avatar_url`；未设置返回空字符串。使用结构体无字段名的位置字面量的 Go 调用者需改为带字段名的写法，HTTP 严格字段解析器需允许新字段。

新增 `user.ProfilePatch` 与 `Service.UpdateProfile`，支持显示名/头像原子部分更新，nil 保持、空字符串清空，仅允许 ACTIVE。`UpdateDisplayName` 保留签名和原行为。消费者 `Service` 必需方法集不变，新增可选 `enduser.ProfileUpdater`；旧适配器的显示名更新仍可用，头像请求缺少能力时返回 503，避免部分写入。

头像现在属于内置顶层保留字段，`profile_attributes` Schema 不得重复声明 `avatar_url`。宿主此前若自行声明同名扩展，应先设计并验证资料调整；库不会自动转移旧扩展值。图片文件仍由宿主管理。HTTP 细节见[基础资料与头像](openapi.md#基础资料与头像)。

按本轮明确授权，当前 `0001` 增加 `avatar_url TEXT NULL`，仍安装 version 1。无头像列的前一资料 v1 及历史拒绝结构无写拒绝；新部署使用经核验的新空 schema，保留数据需求另行制定迁移。详见[头像迁移边界](migrations.md#当前头像初始定义version-1)。

## 可选用户资料属性的 Go 与 HTTP 接口

新增 `Config.ProfileAttributes`、`user.Deps.ProfileAttributes`、`user.ProfileAttributesConfig`、
`user.ProfileAttributesView`、`user.ProfileAttributesPatch` 与 `Service.GetProfileAttributes/PatchProfileAttributes`。
nil 默认保持旧行为；既有 enduser/admin DTO、适配器必需接口、JWT、内省及
`BatchPublicProfiles` 不增加扩展字段。不要直接序列化新增列后的 sqlc 行。
构造和 `Auth.Config()` 均复制 Schema 字节；服务只读复用其构造时编译的策略。

新增错误为 `ErrProfileAttributesNotConfigured`；参数/校验错误支持 `ErrInvalidArgument`，
账号状态拒绝沿用 `ErrInvalidState`。结构化校验错误是
`ProfileAttributesValidationError`，其 `Violations` 最多包含 16 条安全的 Path/Rule。
Go 直接调用仍由宿主授权。内置新增 GET/PATCH `/users/me/profile`，要求 `user` scope，不要求近期重新认证，返回全部资料；参数错误为 `400 INVALID_PROFILE_ATTRIBUTES`，未配置为 `503 PROFILE_ATTRIBUTES_NOT_CONFIGURED`，外层格式错误为 `400 MALFORMED_BODY`。HTTP 只返回固定安全消息，不直接序列化 Violations。
数据库错误使用固定安全消息，不保留可能含整行资料的 PostgreSQL 错误详情。
提交结果不确定时重新读取核对，不盲目重放。Patch 仅含 Document，View 不含 Revision；不再接收 expected_revision 或返回 Conflict。
并发仅用数据库账号行锁：锁内读取最新资料→RFC 7396 合并→完整 Schema 校验→提交。不同不重叠属性保留彼此修改，同属性以后执行事务为准，数组整体替换及父对象删除仍遵循 RFC 7396；不使用 Redis 锁。

这是相对未发布 54afa4b 的 Go 源码破坏性调整：调用者将 UserExtensions/Extensions* 和 GetExtensions/PatchExtensions 改为上述 ProfileAttributes 名称，删除 ExpectedRevision、Revision 及 Conflict 处理；不保留旧 API 别名。配置只保留 Schema，视图只保留 Data；调用者移除 SchemaVersion、SchemaFingerprint、UpdateTime 及 ErrProfileAttributesSchemaMismatch 处理。
属性仅允许用户资料，禁止宿主业务状态、权益及权限。本轮授权将单列并入当前 0001、撤回生产 0002，新安装 version 1；旧 v0.4.0 v1 及各代撤回 v2 无写拒绝，无旧数据迁移，不重放同号 SQL、Force、改版本或清库。旧发布 tag/SQL 和冻结源独立固定，详见[迁移说明](migrations.md#当前用户资料属性初始整合version-1)。

**本人访问契约变化：** 启用 Schema 并挂载内置消费者 handler，即允许本人完整读取和修改所有 profile_attributes。升级前须迁出本人不可见或不可改的内部字段；Schema 的 readOnly/writeOnly 不能当作权限。已有 enduser.Service 必需方法集不变，旧自定义适配器继续编译；需要新能力时实现可选 `enduser.ProfileAttributesService`，未实现的新路由返回 503。`/users/me` 支持内置 display_name 和 avatar_url，不接收 profile_attributes；扩展更新仍使用独立路由。

accountsvc 新增可选 `ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE`，仅 serve 读取；migrate 忽略。未设置不启用资料，文件非法则启动失败；库 ConfigFromEnv/New 仍不读文件。HTTP 启用过程不新增数据库迁移，见[协议](openapi.md#本人资料属性)及[当前验收](../openspec/changes/archive/2026-10-21-add-self-profile-http/verification.md)。

固定兼容基线包含 130 个文件，原始文件清单与 SHA-256 见 `tests/testdata/source-baseline/manifest.json`；该清单用于追溯冻结快照，不表示当前文件名和内容必须与历史快照相同。当前根包名为 `accountkit`，导入路径为 `github.com/bbxx111/accountkit`。

## 数据库运行契约修复

本节对应 `harden-database-runtime`，随 v0.4.0 发布，不改数据库结构、迁移版本、HTTP 或令牌格式。

- **连接池最低容量：** `New` 现在无 I/O 拒绝 `Deps.Pool.Config().MaxConns < 2`。原来显式设置 `pool_max_conns=1` 或 `MaxConns=1` 的宿主，升级前须改为至少 2；pgx 默认容量不受影响。失败不会关闭宿主池，也不新增独立维护连接。
- **内部事务隔离：** 库自行开启的领域事务使用 `READ COMMITTED`，配合既有锁和锁后复核，避免宿主较高默认隔离导致旧证明登录、绑定数量失控或并发刷新异常。`BeforeDelete`、`Anonymizer`、`WithActiveUsers` 的回调随库事务使用该隔离级别；回调仍必须使用传入的同一事务。公开 `Repo.WithTx` / `WithTxRaw` 继续继承宿主默认；调用方已开启并传给 `ImportAccounts` 的事务不被修改。
- **审计分页：** 本地 Reader 只为最终页读取完整事件，`Page.FlowLinks` 提供当前页需要的去重流程归属，不应当作账号全部历史流程清单使用。公开类型、排序游标、动作范围、source/namespace 及管理面独立复核保持；证明查找独立于页面动作和游标范围。外部 Reader 的契约不变。

当前测试、执行计划和未验证项见[验收记录](../openspec/changes/archive/2026-10-20-harden-database-runtime/verification.md)。

## Schema 约束的用户资料属性补丁

用户资料属性由宿主通过 Go 接口显式使用，不自动进入既有账号或认证 HTTP 响应。宿主负责目标账号与字段授权，包含父对象删除和数组整体替换；Schema 的 `readOnly`、`writeOnly`、`default` 等仅是注解，不实施权限或填充值。

补丁遵循 RFC 7396，顶层必须是对象。对象成员的 `null` 删除该成员；数组整体替换，其中及其对象成员不能保留 `null`。递归对象补丁保留未提及成员：对已有对象提交 `{}` 保留内容，对缺失对象提交 `{}` 则创建空对象并检查子字段 `required`。删除父对象可以删除整个子树；删除不存在但已经声明的属性无变化。未知属性即使以 `null` 删除也拒绝，数组内对象的字段同样检查声明目录。合并后始终验证完整资料，包括未改动部分和条件约束。

Schema 固定为 Draft 2020-12 的受支持范围：主结构树每层对象声明固定 `properties` 与 `additionalProperties: false`，节点具有单一明确类型；数组声明单一 `items`。条件、组合及其本地复用定义可使用仅含约束或布尔的 Schema，但只约束主树已有字段，不引入新字段或改变结构类型。仅支持本文件 `$defs` 中无环引用，不加载远程或本地文件资源；未知关键字、未知 format、动态属性、类型联合和嵌套 `$id` 在初始化时拒绝。format 为 `date`、`date-time`、`email`、`uri`、`uuid`，均启用断言；pattern 使用 Go `regexp` 子集。

Schema 原始输入限 64 KiB、32 层；展开后的主树属性声明限 128，验证节点限 1024。同一属性的条件约束不增加声明计数，主树不同位置复用的结构按各位置计数。补丁原始输入与规范化最终资料分别限 16 KiB、深度 8、512 个值节点（含根和容器）。重复键、尾随文档、非法 Unicode、NUL 和 PostgreSQL JSONB 无法存储的数字均拒绝；资料不保存 JSON `null`。数字精确处理，`1`、`1.0`、`1e0` 按相同值比较。

宿主保证同一数据库实例的 Schema 配置一致。库不保存逐行 Schema 身份，不提供 SchemaMismatch 错误。读取只检查通用存储边界；更新按当前 Schema 校验完整合并结果，无变化请求也不得绕过。新增可选属性、放宽约束、修改注解可直接更新配置。收紧规则可能使旧资料完整结果不合法，更新拒绝且原资料与账号 update_time 保持；同次补丁可修复后保存。移除声明属性后，遗留属性仍不能以未知属性 null 删除，须通过过渡 Schema 或专项迁移清理；库不自动丢弃字段。详细设计与实际验证见[变更设计](../openspec/changes/archive/2026-10-21-add-schema-validated-user-extensions/design.md)及[验收记录](../openspec/changes/archive/2026-10-21-add-schema-validated-user-extensions/verification.md)。

冻结、解冻、注销冷静期与恢复保留资料。最终匿名化在原事务中清空 JSON，复用账号原时间语义，不区分曾使用与未使用；配置移除或 Schema 不兼容不阻断清理，回调失败整体回滚。

新增资料路径的校验器及存储错误不会保留能恢复原始资料的底层错误链，审计动作 USER_PROFILE_ATTRIBUTES_UPDATED 只保存固定服务 actor、账号目标、请求 ID 等受控事实，不保存资料值、补丁、属性名、Schema 版本、指纹、资料时间元数据或 Schema 全文。既有导入和匿名化错误的诊断 cause 契约保持；宿主匿名化回调不得在 error 中拼接资料或隐私数据，维护日志可能记录该错误。

## 会话认证依据与客户端描述

本节能力随 v0.4.0 发布，v0.3.0 不包含。新查询 GET /users/me/sessions/current 是可选 enduser.CurrentSessionService 能力，签名 GetCurrentSession(context.Context, user.Principal) (user.CurrentSessionInfo, error)，不扩大既有 enduser.Service 方法集；缺能力返回503 SESSION_QUERY_NOT_CONFIGURED。查询允许user/user:bind，拒绝user:undelete，按数据库检查会话，不改变普通Authenticate/内省热路径。token、JWT、refresh、身份列表、scope、设备头和认证请求体保持原形状。

**行为升级：** 每个会话必须有经事务校验的非空具体身份来源；当前详情、重新认证和换绑使用来源时检查有效性，缺失或失效报内部错误，不作为正常未知态返回，也不从身份列表或尽力审计推算。解绑在同事务撤销命中具体身份的同账号未吊销会话，包括当前和自然过期未清理的会话；同账号任一未吊销会话缺少有效来源则整事务拒绝。204成功仍可能退出；换绑仍撤销其他会话，当前会话仅非空来源不含旧身份时保留。BIND证明和refresh不改变来源。客户端用详情中的完整身份name判断，重新认证后保存新access再重查；预览不保证提交前没有并发变化。普通Authenticate/内省不增加数据库查询。

撤销原因追加 `IDENTITY_UNBOUND`（`enum.RevokeIdentityUnbound`，存储值9），解绑撤销的每个会话以该原因记录 `SESSION_REVOKED` 审计；既有枚举值不重编号。审计与会话展示的消费方须处理新值，回退旧二进制前须确认其对存量值9的表现。

直接Go UnbindIdentity 现在也验证完整user scope、配置要求的近期认证、ACTIVE账号和所属有效当前会话。仅给UserID的旧调用须迁移为Authenticate取得的可信完整Principal；关闭新鲜度检查仍必须scope和会话有效。ReplaceIdentity同样复核；DeleteMe的直接Go入口仍按[Go指南](go-integration.md#认证刷新与敏感操作)要求由宿主做入口保护。

Meta、SessionInfo、accountkit.Deps和enduser.Deps等公开结构新增字段，字段名初始化继续适用，位置式复合字面量须修改并重新编译，不能宣称全部Go源码兼容：

```go
meta := user.Meta{IP: trustedIP, ClientInfo: &user.ClientInfo{ClientType: enum.ClientNative, AppName: "Example App"}}
deps := accountkit.Deps{Pool: pool, Redis: redisClient, ClientInfo: parseClientInfo}
```

开发分支曾出现的公开`AuthenticationIdentitiesKnown`、`InitialClientInfo`及`enum.ClientInfoSnapshotKind`（含INITIAL/LATEST常量）已删除；HTTP移除`authentication_identities_known`和`initial_client_info`。已有开发调用者须删去这些字段/枚举引用，改读非空`AuthenticationIdentities`和唯一的`ClientInfo`。`client_info`从平坦HTTP字段调整为`{type,os:{name,version},device:{type,manufacturer,model},app:{name,version,build},browser:{name,version},extensions:{...}}`，缺失成员省略；客户端须更新DTO与解析。此调整限于尚未发布能力，不改变已发布v0.3.0的历史契约。

Go的ClientInfo保留平坦字段并新增DeviceManufacturer、AppBuild和`Extensions map[string]json.RawMessage`；字段名初始化仍适用。规范化非空描述整体替换单行JSONB，不跨请求补字段；nil/空观察保持原值和时间，孤立version/build合法，app/browser并存。pending最终本次描述优先，否则整体沿用加密首次描述及原观察时间；失败、回滚和重放不更新。客户端描述可缺省，不能识别不阻断认证，显式非法值在凭证消费前400（token为OAuth invalid_request）。业务读写统一验证，无数据库JSON对象/业务CHECK；扩展仅经Go/宿主回调，宿主负责来源及隐私白名单，不新增任意JSON HTTP输入。字段边界、nested示例与可选头见[README](../README.md#当前会话与客户端描述)，当前证据见[简化验收](../openspec/changes/archive/2026-10-20-simplify-session-context/verification.md)。

v0.4.0 发布前，用户先明确要求将未发布会话0002合入当时的0001，又批准在该未发布定义内简化必有来源和单份JSONB；初始化保持version=1，无生产0002。历史tag/SQL和固定源保持。**旧v0.3.0 v1、前一`b414fef`双快照v1及撤回会话v2均拒绝直接接管**，保留数据/结构/version/dirty，需保留数据时另行制定迁移方案。新部署须人工核验新空目标并统一切换认证副本，避免旧来源/撤销行为混跑。旧v0.3.0的Migrate不检查会话上下文，可能接受同号新结构，部署必须人工阻止混跑及未经核验的回退；DDL可读不等于安全回退。回退优先前向修复，不能Down、Force、改版本或清Redis，见[迁移步骤](migrations.md#会话认证依据初始整合)。

## 可选标准化指标

v0.3.0 新增 `accountkit.Deps.Observer`、`user.Deps.Observer` 及相关审计/IdP Options 字段，未配置或 typed nil 时使用 no-op。原位置参数构造入口保持可用；外部结构体字面量请使用具名字段，新增字段不兼容未命名字段字面量。v0.2.0 不包含该能力，接入时固定包含新接口的版本。

宿主显式选择 `observability/otel.New(provider, instance)` 或 `observability/prometheus.New(registerer, instance)`，注入同一个 Observer；依赖仍在根 go.mod，核心领域不导入后端。实例名、指标名称/标签/单位是有界公共观测契约，见[指标指南](metrics.md)。切换适配器无需修改领域调用；新进程或新聚合设施可能重置时间序列，OTel Exporter 可能转换最终名称。

宿主拥有 Provider/Registry、导出与端点，先排空业务并关闭库，再最终采集与关闭观测设施。库不关闭或注销注入组件。标准化指标适配器的回退可移除 Observer 注入或回退宿主依赖版本，不涉及 SQL、Redis 键、HTTP、令牌、密文或既有审计 Stats 迁移。accountsvc 默认关闭指标；显式启用 Prometheus 的配置、采集边界与回退见[服务手册](accountsvc.md#prometheus-指标)，不需要数据库迁移。标准化适配器的实际证据与未验证项见[验收记录](../openspec/changes/archive/2026-10-19-add-standardized-metrics/verification.md)。

## 初始数据库基线整合

当前 v0.5.0 初始定义包含资料单列和头像列，生产仅 0001，新安装 version 1；旧 v0.4.0 v1 及各代撤回 v2 无写拒绝，不提供旧数据迁移。已发布旧 SQL 与当前 initialFrozen 分别固定。以下保留此前整合历史，当前操作见[迁移手册](migrations.md#当前用户资料属性初始整合version-1)。

在尚无实际产品接入的前提下，项目曾执行初始结构整合：当时 `0001_init.up.sql` 定义 user_account、identity、session、audit_event、identity_flow 及字段、索引与约束。十列 UUIDv7 通用审计按用户授权纳入原始定义并更新固定校验，撤回当时独立的 `0002_identity_flow` 和通用审计 0002，也不提供 staging/Prepare 转换工具；初始版本为 1，配套 down 覆盖五个表，公开 Down 拒绝执行。后续审计校验职责调整经另行授权修改未发布 0001，移除五个审计校验函数并同步固定校验，其他表 CHECK 保持。在v0.3.0发布前另经用户明确授权，直接将 0001 的流程对象命名调整为 sign_in_completion_flow 并更新当前固定校验，撤回本分支新增的重命名 0002；初始化仍为 1，后续真实增量从 0002 开始。

v0.4.0 发布前，会话0002先经明确授权合入当时的0001，又经用户批准简化该未发布初始定义，随后随 v0.4.0 发布；七张业务表、配套down和version=1保持，当前来源无known标记，客户端描述按session_id保存单行JSONB。旧流程/审计结构、双表并存、缺少会话上下文的已发布v0.3.0 v1、前一`b414fef`双快照v1，以及所有撤回v2（含原会话v2）均明确拒绝，保留数据、结构和版本；不重放、改名、改版本、Force或自动清库。采用当前定义须人工核验新空资源；包含需保留数据的旧库先备份并单独制定和验证迁移方案。

历史v0.1.0/v0.2.0/v0.3.0标签及source-baseline原始字节保持。历史检查固定三个旧发布的精确提交，分别核验源快照、v0.3.0初始SQL、当前简化定义三代哈希；新发布不能复用历史基线例外。历史合成数据直接装入完整新结构只验证非审计ID、摘要、密文和有效access/refresh格式兼容，审计映射保留legacy_event_id；测试须显式建立有效来源或验证缺失来源被使用时拒绝，不代表生产自动补造来源或旧schema原地接管。操作与恢复边界见[迁移手册](migrations.md)。

## 登录补全流程重命名

当前流程表名为 `sign_in_completion_flow`，内部 sqlc 使用 `SignInCompletionFlow`。v0.3.0 发布前用户曾明确授权直接调整初始流程对象名称，保持列及定义；当时的重命名 0002 已撤回。v0.3.0 空库安装为 version=1/dirty=false。本轮会话 0002 另按新的明确授权合入当前 0001，同样安装为 1，但要求完整七表结构；旧表名、旧 v0.3.0 v1 和撤回 v2 均不自动接管。固定源、历史 SQL 和标签不变，两次有限授权各自只适用于指定范围。

| 旧公开 Go 名称（deprecated，仍可使用） | 新名称 |
|---|---|
| `ids.IdentityFlow` | `ids.SignInCompletionFlow` |
| `user.Deps.IdentityFlowContext` | `user.Deps.SignInCompletionFlowContext` |
| `(*user.Service).CleanupIdentityFlows` | `(*user.Service).CleanupSignInCompletionFlows` |

新上下文字段非空时优先，否则回退旧字段；迁移字段名称时沿用原值。实例上下文的序列化形状、AAD、`f_` ID 格式、`FlowState`、审计资源类型 `IDENTITY_FLOW`、JSON `flow_id` 和既有 HTTP 协议保持，票据仍受原期限与原证明校验约束；格式兼容不表示提供旧库自动迁移。维护任务名称由 `cleanup_identity_flows` 改为 `cleanup_sign_in_completion_flows`；运维指标、日志检索和告警规则需同步更新。

采用新定义时先核对目标、备份与密钥，人工核验后使用新空数据库/schema，由匹配版本的 `Migrate` 安装并确认新表名和完整会话上下文（当前与 v0.3.0 都为 version=1/dirty=false，实际结构不同）。需保留旧数据时另行制定并验证迁移方案，不能重复 Up、Force、修改版本或自动清库接管；回退前单独核验应用与数据库兼容。公共 `Down` 继续拒绝，旧备份先恢复到独立库核验，详见[迁移手册](migrations.md#登录补全流程命名调整)。

## 通用审计公开接口与协议迁移

本次审计 Go API 和审计资源标识是明确的破坏性升级，认证动作结果、其他 `u_/i_/s_/f_` ID、JWT/refresh/验证码格式保持。初始结构、旧库拒绝和历史格式演练边界见上节及[迁移手册](migrations.md)。客户端、宿主适配器和当前数据库基线应一并升级，旧二进制须验证结构兼容后才能回退。

### 事件生产与写侧

`audit.Event` 不再是认证字段集合，也不再由 Store 生成 `e_` ID。它与叶子包 `audit/event.Event` 是同一类型，包含 `ID uuid.UUID`、`Source`、`Action string`、`Result *enum.Result`、`Actor`、`Targets`、`Context`、`Data json.RawMessage`、`OccurTime`；`audit.RecordView` 在事件外增加存储方首次生成的 `CreateTime`。SQL 固定为十列，认证专属快照位于 `data.accountkit`，不再提供 `user_id/session_id/flow_id/identity_kind` 专属列或公共 schema_version。

认证专用调用迁移到 `audit.AuthenticationEvent`（`audit/authentication.Input`）和 `audit.NewAuthenticationRecorder`。直接操作通用事件的宿主先用 `uuid.NewV7()` 生成 ID，设置实际发生时间及可信 source，调用 `audit/event.Freeze` 校验、规范化并冻结可变值，再交给 `Recorder.Record(ctx, event)`。已有 UUIDv7 的认证输入由映射器保留，缺失时映射器首次生成；重投递保持 ID、OccurTime 和逻辑内容。生成或校验失败不得改变认证动作结果。

`Inserter.InsertBatch` 返回 `(audit.BatchResult, error)`，分别统计 Written、Duplicate、Failed、Invalid、Conflicts。同一 ID 同一事实不新增记录、不更新 CreateTime；真正冲突返回可识别的 `audit.ErrConflict`，不能重新生成 ID 或 upsert 覆盖。合法清理后的重放不能恢复已删除隐私。宿主记录器必须自行冻结输入、避免业务路径阻塞并提供安全失败观测；默认 `audit.Async` 已完成入队前冻结及有界批处理，但不提供持久化队列或可靠投递。

source/action 最长 128 ASCII 字符，source 匹配 `[A-Za-z0-9][A-Za-z0-9._/-]*`，action 匹配 `[A-Z][A-Z0-9_]*`；非 nil Result 只能是 SUCCESS/FAILURE。已知主体为 namespace + id，资源为 namespace + type + id；namespace/id 各最多 512 字节，name 最多 256 Unicode 字符。完整事件不超过 64 KiB，targets/related_resources 各最多 32 项，超限和重复 JSON 键拒绝，不截断稳定标识。

这些业务内容规则统一由 Go 校验：默认 `Async.Record` 在入队前调用 `event.Freeze`，本地 `Store.InsertBatch` 在写入前独立调用同一入口，Go 脱敏投影写回前也校验。数据库保留 UUIDv7、JSON 基本形状、非空、列类型、默认值、主键和索引，不再用自定义函数校验主体、资源、上下文、事件大小或业务时间范围。直接 SQL 可以写入形状合法但业务内容非法的数据；导入和修数必须复用应用校验，读侧仍可能拒绝这些非法记录。既有合法 Go/HTTP 输入及错误语义不变。

这是此前未发布初始定义的职责调整，当次没有为同号实例自动移除旧审计函数与 CHECK。此前仍使用 identity_flow 的完整十列 version=1 实例现在也明确拒绝接管；重复 Up 不会重放 0001 或自动转换。采用当前定义须经人工核验后准备新空资源，需保留数据时另行制定并验证迁移方案，不 Force、不修改版本、不自动清库。当次调整保留了其他四张表的 CHECK 和全部索引；验收记录见[该次变更](../openspec/changes/archive/2026-10-18-audit-validation-in-go/verification.md)。

认证映射可省略无效 IP、超长设备字段、自定义 request_id 或可选 name，并单独安全计数/告警；新边界不收紧原认证 HTTP 输入。已验证管理员 issuer/sub 超过 512 字节则拒绝该审计事件并安全观测，认证结果保持。显式历史映射 `authentication.MapLegacy` 对不兼容可选字段拒绝转换，不能据此推断包提供旧库自动升级。

### 配置、所有权与读侧

本地默认 source/namespace 分别为 `accountkit/<schema>`、`accountkit/<schema>/resources`。`ConfigFromEnv(prefix)` 对 `AUDIT_SOURCE/AUDIT_NAMESPACE` 保留原始空值，`New` 才应用本地默认；`Config.Validate` 检查有效默认而不修改调用方字段。注入 `Deps.Audit` 时必须显式提供 `Config.AuditSource/AuditNamespace`，允许明确填写与默认字符串相同的值。直接构造 `user.NewService` 时，每个缺失值分别从 Repo 池配置的首个 search_path schema 推导，池为空或首项为空回退 account；直接服务不强制判断外部接管，跨库中心审计仍须显式配置二者。

`audit.NewStore(pool)` 同样只推导本地默认；自定义配置使用 `audit.NewStoreWithOptions(pool, audit.StoreOptions{Source: source, Namespace: namespace})`，与事件生产方保持一致。namespace 表示稳定身份体系，可以是受控 URN 或完整 issuer URL，无需可访问；source 区分事件来源，role 和显示名不构成稳定身份。实际共享同一主体体系时才复用 namespace，独立数据库汇聚时须明确隔离，已有 namespace/source 变化须独立迁移。三个消费者应用案例见[设计说明](../openspec/changes/archive/2026-10-17-generalize-audit-events/design.md#to-c-案例一单个-app-使用自建账号体系)。

`Deps.AuditReader` 和管理适配器的读侧改用 `audit.Reader.ListByResource(ctx, audit.Query) (audit.Page, error)`，不再接收生成数据库行。`Query` 指定 Source、完整 Resource、可选 Actions、After 和 Limit；适配器按 `(occur_time, UUID)` 严格升序返回不超过 Limit 的 RecordView，所有记录严格大于 After，CreateTime 必须非零且为可序列化的有效时间。不得重复 UUID，包括同 UUID 不同发生时间；全页及用于判断下一页的额外记录都会被复核。

`Actions` 为空表示该资源的全部动作，通用 Reader 仍能查询业务事件；非空集合必须在排序和 Limit 前过滤。`Query.NormalizedActions()` 校验动作标识、返回排序去重的独立副本，不修改调用方输入，不将小写或空白改写为合法值。管理路由固定使用 `authentication.Actions()` 从认证枚举导出的完整集合，不增加业务审计 HTTP 接口或客户端动作筛选参数。宿主 Reader 必须执行这一条件；HTTP 对忽略条件的返回值仍整页拒绝为 503，不能先取页再静默跳过业务事件。`FlowLinks` 的归属证据独立查询，即使其动作不在记录筛选集合中，也须保留必要证据。

默认无注入时库管理本地写读；注入 Audit + AuditReader 时管理查询走宿主读侧；仅注入 Audit 时已授权审计查询返回 `503 AUDIT_QUERY_NOT_CONFIGURED`；只注入 Reader 在 New 被拒绝。读侧故障、越界记录、顺序或记录结构不合规返回安全的 `503 AUDIT_QUERY_UNAVAILABLE`。`ADMIN_NOT_CONFIGURED`、角色检查、目标账号存在性检查和独立管理员身份系统保持。

查询范围是本来源、本 namespace 下账号作为主体、直接目标或关联资源的认证事件。早期仅有 flow 的记录用 `Page.FlowLinks` 提供归属证据：历史 Evidence 须为同源、成功的 `SIGN_IN_COMPLETION_PROOF_VERIFIED` 或 `SIGN_IN_COMPLETION_COMPLETED`，并有可复核的 USER/FLOW 资源引用；`Live=true` 时 Evidence 必须为空，由管理面独立调用领域 FlowOwner 核验。已有明确其他账号归属的事件不能通过共享 flow 混入结果。历史资源后来被删除不使有效历史证据失效；证据不直接序列化，任意业务 data 不进入认证 HTTP DTO。

库只管理自建 worker，不会启动、刷新或关闭宿主组件；宿主先排空业务、关闭 Auth，再关闭自建审计组件与连接。三种可编译装配见 [examples/audit](../examples/audit/main.go)，默认嵌入宿主见 [examples/embedded](../examples/embedded/main.go)。示例中的宿主 Store 仍与认证同库，远端 Recorder/Reader 由宿主实现；不能把阻塞网络调用直接放入 Record 并声称不影响认证。

### HTTP 资源与游标

审计资源 name 的末段从 `e_…` 改为标准小写带连字符 UUIDv7，例如 `users/u_0000000000001/auditEvents/0199ed00-0000-7000-8000-000000000001`。不增加 `e_` 前缀。旧认证 DTO 的 event_type、actor_kind、用户/会话关联及受控管理员快照语义保持，result 为 `SUCCESS`、`FAILURE` 或 JSON `null`，其他可空字段仍输出 null；任意业务 JSON 不原样暴露。

审计使用独立的 v2 游标，绑定 source/resource、规范化的认证动作集合和 `(occur_time, UUID)`，客户端继续将其视为不透明字符串。旧 v1 UUID 游标、旧 `e_` 审计游标、跨账号/来源/动作集合的游标返回 `400 INVALID_PAGE_TOKEN`，响应提示从第一页重新开始，客户端须清空 page_token。认证枚举扩展导致动作范围变化时也须重新开始分页。用户列表等其他游标协议保持，不能通用解码或把游标当作授权凭据。

### 外部隐私与模式切换

本地匿名化保留事件事实及稳定资源引用，清理 actor/targets/related_resources 的 name、context 中 client_ip/user_agent/device_id 和 `data.accountkit.subject_hint`，保留 request/trace/span 关联；覆盖账号和流程历史，迟到及重放不能恢复隐私字段。外部设施须实现对应的资源关联、保留期和清理完成核验。

宿主必须建立独立于尽力审计的持久化清理流程，保存待清理资源、失败重试和完成状态。同库匿名化回调可用传入 pgx.Tx 写入宿主自己的清理任务，不可等待远端服务或另开连接提交同批数据。只收到 `USER_PURGED` 不构成完成证据；清理未完成时不得声称外部副本已匿名化，也不能回滚已提交本地事务或丢弃清理责任。此变更不内置可靠消息/outbox，也不承诺跨库原子提交。

切换外部写读不会自动迁移或删除本地旧副本，本地保留和匿名化继续覆盖配置范围内旧记录；外部历史回填须独立受控执行。改变 source/namespace 前须安排旧范围的持续清理，不能用新配置下查不到记录证明旧副本已删除。外部备份、副本及回填数据也须纳入宿主治理。

## 身份策略与登录补全协议升级

v0.3.0 包含身份策略与登录补全的破坏性 Go/HTTP 升级，v0.2.0 不具备这些能力。C 端及管理端沿用 `/users` 账号路由，包括自定义动作、me、身份和会话；响应 `name` 使用 `users/{user_id}` 及其子资源。此前开发中的 `/accounts` 路径尚未发布，本轮撤回，返回 404，不注册别名或重定向；已发布的 users 调用无须迁移资源名。管理列表顶层 JSON 字段 `users`、`user_id`、u_ ID、JWT claims、scope、Go User 类型、`user` 包和 `Auth.Users()` 均保留。客户端仍须适配下述 token/pending 与 Go 返回签名，网关须显式增加两个 users 补全端点。

`SignInWithCode` / `SignInWithIdp` 的返回类型由 `TokenResult` 改为 `SignInResult`，调用方先检查 `Tokens` 或 `Pending`，两者恰一非 nil。自定义 `enduser.Service` 同步改签名；新增补全能力为可选接口，缺能力返回503。成功 HTTP token 字段保持，pending 只有 next_action、`sign_in_completion_token`、期限和策略集合。新增两个端点及配置 nil/[]、自动关联与注册、设备绑定、幂等和失败边界见 [README](../README.md#身份策略与登录补全)。旧 `user:bind` 表示已有账号不完整；pending 尚无账号，不能把补全票据作为 Bearer 或 refresh 使用。

E/R 环境键沿宿主前缀，R 仅 PHONE/EMAIL any-of；省略 R 默认二选一，`[]` 允许纯第三方。服务显式禁用 EMAIL 时无需 SMTP，显式启用 PHONE 因未装配 SMS 服务商而报错。纯第三方账号需要近期认证时重走普通 IdP 登录，会按同设备规则替换会话；客户端保存新 pair、核对仍为原 user_id 后再继续敏感操作。此操作不保证提供方强制输入密码或 MFA。手机/邮箱及管理员恢复保留，普通 IdP 仍拒绝冷静期账号；纯第三方没有手机/邮箱自助恢复路径。

当前 0001 直接定义 sign_in_completion_flow 及 data.accountkit.flow_id 审计关联，初始化版本为 1，旧表名库不自动接管。全部认证副本及调用者采用同一新基线和协议后再启用策略，避免同实例混跑旧登录即建号流程。收紧策略前核验存量可用身份；新登录和新一轮 refresh 可能取得 user:bind，已有 access 沿原期限与吊销规则处理。保留 schema、Redis 前缀、issuer/audience 和完整旧密钥。回退先停新流程并排空最长 TTL 与在途请求，保留流程表和密钥；旧程序无法等价执行策略或兼容当前基线时不能作为安全回退。恢复先落独立空库，数据库备份不包含 Redis 或密钥系统，不能通过清 Redis 恢复证明。

以下旧变更的说明与提取索引记录各自升级边界；当前调用应优先遵循本节。

## 开发版默认 schema 调整

`v0.2.0` 在 `Config.Schema` 未指定或为空时采用 `account`；`v0.1.0` 的默认值为 `auth`。升级已有实例须显式保留原 schema。显式合法 schema（包括 `auth`）继续按配置使用，宿主通过 `Config.Schema` 或带宿主前缀的 `AUTH_SCHEMA` 设置。库与 accountsvc 共用默认值，连接池仍须通过 `PoolConfig` 配置匹配的 search_path。环境变量名称 `AUTH_SCHEMA` 和 Redis 默认前缀 `auth:` 保持不变。

## 验证码轮次契约升级

`v0.2.0` 的 `add-host-account-contracts` 修改 Go、HTTP 和短期 Redis 验证码格式；以下能力已在该固定版本发布，不属于 `v0.1.0` 接口。发布状态和本轮实际检查见[验收记录](../openspec/changes/archive/2026-10-09-add-host-account-contracts/verification.md)。JWT、refresh、身份密文、数据库结构、冻结迁移和发送器签名保持。

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

`BatchPublicProfiles(ctx, ids)` 是 Go 批量查询，结果仅含 ID/DisplayName/AvatarURL/State。输入去重，非法 ID 返回参数错误，未知账号略去，待注销及已删除账号清空显示名和头像；宿主在调用前按业务权限过滤 ID。无任意用户资料 HTTP 端点。

`Deps.BeforeDelete` 在账号锁和状态检查后、改变状态前使用同一 `pgx.Tx`，消费者和管理员共享。返回 `user.ErrDeletionBlocked` 为 400 `FAILED_PRECONDITION`、reason `DELETION_BLOCKED` 和固定消息；其他错误为固定 500，同批账号、会话和业务写入回滚。其他回调错误不进入库的业务错误分类，不能通过返回旧 `ErrNotFound` 等哨兵变成 404；调用者不要依赖对这些底层错误的 `errors.Is`/`errors.As`，明确业务拒绝使用 `ErrDeletionBlocked`。

`WithActiveUsers(ctx, ids, fn)` 要求非空账号集合和非 nil 回调，先验证/去重/排序锁定 ACTIVE 账号，再执行宿主回调；锁序为账号后业务资源。库拥有提交/回滚，回调不得自行结束事务；回调错误按原事务契约返回，panic 回滚并向上传递。未知或非 ACTIVE 账号拒绝，回调不运行。

## 离线账号导入

`ImportAccounts(ctx, tx, accounts)` 只用于受控停写工具，事务来自 schema 匹配的认证连接池。库不另开连接、不提交、不回滚；调用者负责空目标核验、批次记录、重复执行、业务引用迁移和最终提交。发生任何错误时必须回滚整个批次，不继续提交部分成功数据。

`user.ImportAccount` 保留合法 `u_` ID 和原创建/更新时间；账号和锚点创建/更新时间必需非零，允许历史时钟回退，不强制更新时间晚于创建时间。ACTIVE 至少包含一个 `user.ImportAnchor` 的 PHONE/EMAIL 合法目标，删除/匿名化时间为空；DELETED 只允许无显示名、无锚点的匿名墓碑，可保留删除/匿名化时间，不重新抢占旧地址。FROZEN、PENDING_DELETION、第三方身份和非法目标拒绝。归一化和版本化加密/摘要沿用库配置；重复 ID 或归一化身份冲突失败，不合并、不静默跳过。

普通创建和 ACTIVE/DELETED 导入均使用空扩展默认值：`{}`，复用账号原时间。`ImportAccount` 不接收用户资料属性；ACTIVE 导入提交后可另调 `PatchProfileAttributes`，DELETED 墓碑拒绝写入。导入和补丁是两个独立调用，不提供同批事务原子性保证。

导入先校验并归一化整批输入，再在写入前对整批 PHONE/EMAIL subject 按全部摘要版本一次性排序加事务级锁，因此任一账号的输入错误会先于任何数据库写入或冲突返回；误并行的批次即使身份顺序相反也只会排队，后到者得到身份冲突而不是死锁。锁持有到调用者事务结束，同一事务内的其他写入照常进行。

导入错误的 `Error()` 文本脱敏，身份冲突可用 `errors.Is(err, user.ErrIdentityConflict)` 分类；由唯一索引兜底发现的冲突（如与不加锁的旧进程并发），`errors.As` 仍能获取数据库诊断。解包后的诊断可能包含原始详情，不能直接写入日志或对外输出。

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

## 验证码发送器命名与渲染接入

验证码投递的正式名称更精确，旧名称继续作为带 Deprecated 注释的 Go 类型别名保留：

| 旧名称 | 正式名称 |
|---|---|
| `sender.SMSSender` | `sender.SMSCodeSender` |
| `sender.EmailSender` | `sender.EmailCodeSender` |
| `sender.Message` | `sender.CodeMessage` |

`SendSMS` / `SendEmail` 的签名及 Purpose、Code、TTL 字段保持，旧实现无需增加方法或显式转换；`accountkit.Deps.SMSSender` / `EmailSender`、`user.Deps.SMS` / `Email` 字段名不变。可逐步替换引用，不要求同步升级所有宿主。

原 `smtp.New(Config)` 与 Config 结构保持，仍由独立 `LegacyChineseRenderer` 生成原四用途中文纯文本，正 TTL 向上取整到分钟。自定义内容改用 `smtp.NewWithRenderer(cfg, renderer)`，实现 `Render(context.Context, sender.CodeMessage) (smtp.EmailContent, error)`；Subject 与 Text 必需，HTML 可选。新入口要求非 nil 渲染器，构造时不渲染、不连接网络；渲染失败不自动回退旧文案。accountsvc 显式注入中文兼容渲染器，部署配置、EMAIL 禁用和 migrate 依赖边界保持，不增加环境变量。

[本地化示例](../examples/codeemail/renderer.go)演示固定默认语言、私有类型化 context 键与请求级回退；[装配示例](../examples/codeemail/example_test.go)不发送网络请求、不输出验证码。宿主负责语言选择、并发安全、Text/HTML 的用途/验证码/有效期一致性、HTML 动态值转义与合作式取消；详见 [README 发送器契约](../README.md#发送器)。使用服务商托管模板的宿主继续直接实现两个发送器接口，把用途映射到模板与参数，无需先生成 SMTP 正文。

本变更无配置默认值、数据、令牌或限流迁移。回退旧版本时，使用新名称或 NewWithRenderer 的源码需同步回退并重新编译；只使用原接口的宿主保留原构建方式。自定义本地化内容不会自动保留到旧二进制，部署回退前需确认文案差异；不执行 Down、UnsafeReset 或清空 Redis。

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
| Facebook 登录（Limited/Classic） | user/idp/facebook.go、user/service_idp.go、config.go | user/idp/facebook*_test.go、user/service_facebook_test.go、httpapi/enduser/signin_facebook_test.go、config_facebook_test.go、internal/accountsvc/config_facebook_test.go |
| 支付宝 App 极简登录与空字段补填 | user/idp/alipay*.go、user/service_idp.go、user/service_signin_completion.go、config.go | user/idp/alipay*_test.go、user/service_alipay_test.go、config_alipay_test.go、accountkit_alipay_test.go、accountkit_alipay_http_test.go、internal/accountsvc/config_test.go；真实 App/应用分组属于宿主接入清单 |
| Facebook 数据删除入口 | accountkit.go `HandleFacebookDataDeletion`、user/service_provider_deletion.go、user/idp/facebook_signed_request.go | accountkit_facebook_test.go、user/service_provider_deletion_test.go、user/idp/facebook_signed_request_test.go、audit/authentication/authentication_test.go |
| 身份绑定解绑 | user/service_identity.go | service_identity_test.go、identities_test.go、TestIdentityBindingEndToEndAgainstRealDB |
| 同类身份换绑 | user/service_identity_replacement.go、httpapi/enduser/replacement.go | 领域回滚/竞态、嵌入式及服务E2E，见[换绑验收记录](../openspec/changes/archive/2026-10-03-add-identity-replacement/verification.md) |
| JWT、刷新、会话、重新认证 | tokens、session、service_session.go | tokens_test.go、session/*/*_test.go、service_test.go、TestConsumerEndToEndAgainstRealDB |
| 资料、注销恢复、冻结 | service_me.go、service_lifecycle.go、service_admin.go | service_lifecycle_test.go、service_admin_test.go、TestAccountLifecycleEndToEndAgainstRealDB |
| 管理接口、角色、审计 | httpapi/admin、audit | httpapi/admin/*_test.go、audit/*_test.go、TestAdminSurfaceEndToEndAgainstRealDB |
| 加密、旧密钥、维护 | pii、maintenance、service_rekey.go | pii_test.go、runner_test.go、service_rekey_test.go、TestKeyRotationBackfillEndToEnd |
| 业务同事务匿名化 | anonymize、service_purge.go | service_purge_test.go、TestHostContracts（宿主先写入再失败） |
| 产品隔离 | Config.Schema/KeyPrefix、JWTIssuer/JWTAudience | TestIndependentInstances（故意复用 ID、refresh、目标与 HMAC 密钥暴露缺少命名空间） |
| 包内 schema/版本/重复迁移 | migrations、Auth.Migrate | migrations_test.go、TestHistoricalFormatsOnInitialSchema；0001定义新流程表及简化会话上下文，初始版本为1，旧表名v1、双表、v0.3.0 v1、前一b414fef双快照v1与撤回v2明确拒绝 |
| 历史数据及凭证格式 | 新初始结构、固定历史合成数据 | TestHistoricalFormatsOnInitialSchema：重复迁移保留数据、旧JWT/refresh可用、旧身份可解密，夹具显式建立有效非空来源；不承诺旧schema原地接管或生产自动补来源 |

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

### 微信固定身份模式与小程序

微信 OAuth 与小程序共用 `WECHAT` 身份类型。默认 `UNIONID` 模式使用 `unionid:<unionid>`；`OPENID` 模式使用 `openid:<appid>:<openid>`，OpenID 按应用隔离。历史裸 UnionID 不会自动匹配新 subject；本次不提供旧数据迁移工具。升级、回退或切换模式前须审查已有身份数据，模式变化可能产生不同身份和账号，不会自动升级、降级、合并或通过元数据查找账号。模式或应用所属验证组变化会拒绝已有 pending 登录补全流程，须重新登录。

已验证的 UnionID、按 AppID 分类的 OpenID 与既有 profile 保存在 `provider_meta`。缺失字段保留旧值；既有非空 UnionID 或同一应用的 OpenID 矛盾返回现有 `user.ErrIdentityConflict` 并回滚，不创建会话。OpenID 模式后续取得 UnionID 仅补充元数据，subject 和身份 ID 保持。绑定、直接注册与 pending 完成遵循相同规则。code、令牌、session_key、Secret 和任意原始响应不保存到该元数据。Go `idp.Identity` 新增字段，位置式复合字面量调用方应改用字段名初始化。
