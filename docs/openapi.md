# HTTP OpenAPI 契约

本契约描述已发布 v0.5.0 的内置 HTTP handler，基于 v0.4.0 增加本人资料属性接口及内置头像字段；已发布 v0.4.0 不包含这些能力。直接调用 Go 方法的宿主请先阅读 [Go 集成指南](go-integration.md)；挂载内置 handler 的宿主可以将对应契约用于客户端文档、类型生成和接口核对。

## 两个独立 HTTP 面

| 文档 | 范围 | 默认挂载示例 |
|---|---|---|
| [enduser.json](openapi/enduser.json) | 消费者 24 个操作：登录、补全、token/revoke、资料、身份、会话及注销/恢复 | `/v1` |
| [admin.json](openapi/admin.json) | 管理面 11 个操作：账号查询、状态操作、明文查看、会话及认证审计 | `/admin/v1` |

两份文件各自独立，使用 [OpenAPI 3.1.0](https://spec.openapis.org/oas/v3.1.0.html) 与 JSON Schema 描述。所有 paths 均为相对 handler 路由，`servers[0].variables.basePath.default` 由宿主调整为实际挂载前缀或完整服务 URL。路径中的 `user` / `session` / `identity` 是原始 `u_` / `s_` / `i_` ID；响应 `name` 才是 `users/{id}` 等资源名。

`info.version` 标识当前已发布 v0.5.0 的接口契约。文件不包含 accountsvc 专属的 `/v1/introspect`、探针或管理员登录流程，也不描述宿主自有业务接口；这些不能由库契约自动推导。

消费者使用自己的 Bearer access token。`x-allowed-scopes` 表示允许的 scope 列表，`x-recent-authentication` 表示现有近期认证要求；这些扩展与说明用于接入核对，代码生成器不会自动替宿主实施授权。未标 scope 的 `GET /users/me` 接受任何有效消费者 Bearer scope。

管理面使用独立宿主身份系统，`x-required-role` 对应路由要求；`operator` 与 `super-admin` 的层级由宿主 verifier 决定。文件中的 AdminBearer 表示常见的外部 Bearer 接入，宿主若使用其他认证方式须修改安全定义。外部 verifier 的 401/403 响应体与 challenge 由宿主定义，契约没有伪造统一错误体。完全未配置管理面时返回 `503 ADMIN_NOT_CONFIGURED`。

## 客户端需要区分的响应

- 登录和 `completeSignIn` 的 HTTP 200 是 `TokenPair` / `PendingSignIn` 的 `oneOf`。补全成功响应仍可能 pending，或返回 `user:bind`；只看状态码不能判断已获得普通业务权限。
- `reauthenticate` 使用独立 `AccessToken` schema，不含 refresh 字段。客户端保留原 refresh 和原绝对期限；普通登录/刷新才更新完整 pair。
- 身份绑定新增返回 201，幂等命中返回 200。单会话撤销和解绑返回 204 无内容；`revoke` 返回 200 无内容；`revokeOthers` 返回 200 `{}`。账号注销返回更新后的资源。
- 普通错误为 `{"error":{"code":400,"message":"…","status":"INVALID_ARGUMENT","reason":"…"}}`；`code` 是数字 HTTP 状态，`reason` 才是细分原因。`token` / `revoke` 使用独立 OAuth 错误体，不能按普通错误解析。
- 消费者 Bearer 401 带 `WWW-Authenticate`；限流 429 带 `Retry-After` 和 `retry_after_seconds`，部分 503 也带等待提示。凭证响应包含 `Cache-Control: no-store`，token/pending/OAuth 错误还带 `Pragma: no-cache`。
- 枚举使用大写字符串。身份类型为 `^[A-Z][A-Z0-9_]{1,31}$`：内置 PHONE、EMAIL、WECHAT、APPLE、GOOGLE，部署可另配 OIDC 等类型。凭证请求统一为 `{"kind","credential"}`，见下节；未知字段拒绝。POST token/revoke 也使用 JSON，不使用表单编码。
- nullable 字段和 optional 字段不同：例如 `delete_time` 必须出现但可以为 null；审计 `flow_id` 可以省略。管理 DTO 与消费者 DTO 独立，管理会话没有 `is_current`。

示例 token、验证码和票据只能使用合成值，不应把真实响应导入公开文档。刷新重放、验证码失败预算、事务回滚、账号状态及外部审计治理仍以 [README](../README.md)、[主规格](../openspec/specs)和对应实现为准，schema 不能表达或证明所有行为。

## 统一凭证信封

`signInWithCode`、`signInWithIdp`、`completeSignIn`、`reauthenticate`、绑定和换绑的请求体都是顶层 `kind` 加 `credential` 对象（`completeSignIn` 另带 `sign_in_completion_token`）。不再接受 `phone`、`email`、`wechat`、`apple`、`google` 成员，带这些字段返回 `400 MALFORMED_BODY`。

| kind | credential | 可用端点 |
|---|---|---|
| PHONE / EMAIL | `{"target","code_id","code"}` | 验证码登录、重新认证、换绑、登录补全、绑定 |
| WECHAT | `{"app_id","code"}` | 第三方登录、绑定 |
| APPLE / GOOGLE / 配置的 OIDC 类型 | `{"id_token","nonce"}`，nonce 为原始值 | 第三方登录、绑定 |

`kind` 或 `credential` 缺失、`credential` 不是对象、第三方 credential 原始 JSON 超过 16 KiB，或 credential 字段缺失、未知、类型错误时返回 `400 CREDENTIAL_INCOMPLETE`；kind 格式非法为 `400 INVALID_ARGUMENT`；类型不属于该端点允许的类别为 `400 CREDENTIAL_KIND_NOT_ALLOWED`。这些拒绝都发生在消费验证码、访问提供方和登记 nonce 之前。未配置的第三方类型在未显式设置 Enabled 时为 `400 IDP_APP_NOT_ALLOWED`，显式设置时为 `IDENTITY_KIND_NOT_ENABLED`。PHONE/EMAIL 的 `code_id` 缺失或格式错误沿用原分类 `400 INVALID_ARGUMENT`，其 credential 只受 64 KiB 请求体上限约束。同一对象内的重复键按标准 JSON 解码取最后一个值，客户端不应发送重复键。旧格式迁移见[兼容说明](compatibility.md#统一凭证信封与身份类型字符串)。

## 基础资料与头像

`GET /users/me` 及消费者注销/恢复响应包含 `avatar_url` 字符串，未设置为空；管理列表、详情及状态操作资源也返回该字段。`PATCH /users/me` 接受 `display_name`、`avatar_url`，要求 `user` scope，不要求近期认证。内置领域服务在账号行锁内复核 ACTIVE；两字段一起提交原子保存，成功返回 200 和完整本人资源。

省略或 `null` 保持原字段，`""` 清空；两个字段都没有非 null 值时为 `400 NO_FIELDS`。头像去除首尾空白后最长 2048 **字节**，必须是具有非空主机的绝对 HTTPS URL，无用户信息、fragment、反斜线、内部空白或控制字符且为合法 UTF-8；允许普通查询参数。库不请求该地址，上传和存储由宿主提供。

地址错误为 `400 INVALID_ARGUMENT`，JSON 类型/格式错误为 `400 MALFORMED_BODY`，锁后非 ACTIVE 为 `400 FAILED_PRECONDITION / INVALID_ACCOUNT_STATE`，目标不存在为 404。显示名去掉首尾空白后最多 128 个 Unicode 码位；超出或含有控制字符、双向格式符时整次拒绝，原值不变。自定义旧 Service 没有可选 `ProfileUpdater` 时，显示名请求仍可处理，含非 null 头像的请求返回 `503 PROFILE_UPDATE_NOT_CONFIGURED`，两字段都不写入。头像是内置公开资料，不依赖资料扩展 Schema，不能在 profile_attributes 顶层重复声明。

## 本人资料属性

`GET /users/me/profile` 返回 `{"name":"users/{user_id}/profile","profile_attributes":{...}}`；`PATCH /users/me/profile` 只接受 `{"profile_attributes":{...}}`，成功同样返回完整资源和 200。目标只取 Bearer 主体，两个接口均要求 `user` scope，无需近期重新认证；不支持指定其他账号或管理端访问。所有响应包含 `Cache-Control: no-store`。

所有属性对本人完整可读写。`readOnly/writeOnly` 注解不会隐藏或禁止属性；资料中不能存放仅供宿主读取或修改的字段。读取 ACTIVE/FROZEN，修改仅 ACTIVE，认证和吊销规则保持；状态错误为 `400 FAILED_PRECONDITION / INVALID_ACCOUNT_STATE`。`/users/me`、管理详情、公开资料和令牌响应不混入 profile_attributes；内置头像按上节独立返回。

请求使用 `application/json`。内层采用 RFC 7396：缺失保持、对象递归合并、数组整体替换、null 删除；`{"profile_attributes":{}}` 是无变化请求，外层 `{}` 或 profile_attributes=null 拒绝。未知属性（包括 null 删除）拒绝；结果须通过当前 Schema 完整校验。HTTP 外层默认总大小 64 KiB（可由宿主收紧），内层原始补丁和规范化资料各 16 KiB，深度 8、值节点 512，拒绝重复键与非法编码并保持数值精度。OpenAPI 的动态对象 schema 仅表达容器结构，实际属性和跨字段规则来自宿主 Schema。

语法/外层字段/总大小错误为 `400 MALFORMED_BODY`；可解析的内层对象/编码/容量/Schema 错误为 `400 INVALID_PROFILE_ATTRIBUTES`。缺少可选能力或 Schema 时，已认证合法请求为 `503 PROFILE_ATTRIBUTES_NOT_CONFIGURED`；scope 不足 403，缺失/无效令牌 401 并带 Bearer challenge，账号不存在 404，内部错误固定 500。错误不返回资料值或原始校验信息。

无需先 GET 或携带 revision/ETag；并发在账号行锁内合并最新数据，不重叠修改保留，重叠以后执行事务为准。无变化不更新时间或发修改审计；提交结果不确定时先 GET 核对再决定下一步。服务文件装配见 [accountsvc](accountsvc.md#本人资料属性)。

## 当前会话和客户端摘要

GET /users/me/sessions/current支持user/user:bind；返回真实s_资源名、数据库最新RFC3339 auth_time及非空authentication_identities，摘要只有name/kind且按具体ID稳定排序。客户端用完整name匹配，重新认证保存新access后重查。来源缺失或失效为内部错误，不返回正常空集合。开发分支曾出现的authentication_identities_known/initial_client_info已移除；详情和两面的列表均只可含单份client_info与client_info_update_time，管理面不增加身份来源详情。开发调用者须更新解析，见[兼容说明](compatibility.md#会话认证依据与客户端描述)。

client_info采用`{type,os:{name,version},device:{type,manufacturer,model},app:{name,version,build},browser:{name,version},extensions:{...}}`嵌套结构，缺失成员省略。app/browser可并存，version/build可单独出现；名称/制造商/型号64字符、版本32、build64，规范化完整JSON含转义最多2048字节。业务codec在解析前将完整原始JSON和每个RawMessage各限16KiB；合法指数数值精确规范化为有界十进制，展开后仍受完整2048字节限制。核心未知字段、重复键、非法类型/枚举及UTF-8/控制字符均由业务读写拒绝，存储非法描述为内部错误；数据库不定义JSON对象/业务CHECK。extensions的深度、节点、敏感键和宿主隐私职责见[Go指南](go-integration.md#当前会话解绑与客户端描述)。

新详情在数据库会话缺失、归属错误、撤销或refresh期限已到时返回401 TOKEN_INVALID并带Bearer挑战；依赖不可用503、内部错误固定500，缺可选宿主能力503 SESSION_QUERY_NOT_CONFIGURED，所有响应no-store。当前详情、重新认证、换绑均检查有效非空来源；解绑时同账号任一未吊销会话缺少有效来源则内部错误并整事务回滚，包含自然过期但未清理者。普通Authenticate/内省保留JWT/Redis判定，无额外数据库热路径；旧JWT不会因查询而取得新auth_time。解绑仍204、换绑仍200且不发token，成功也可能让当前会话退出。

九个显式客户端头只在验证码/IdP登录、completeSignIn、reauthenticate与新一轮refresh解析；UA和Client Hints可补充，自动解析失败放行，显式非法值凭证消费前400 INVALID_CLIENT_INFO（token为OAuth invalid_request）。没有新增制造商/build显式头或任意JSON HTTP输入；扩展仅来自直接Go或宿主回调。请求体不变，token、JWT、内省和身份列表不增加来源/描述。非空观察整体替换描述，nil/空不写；pending完成本次优先，否则整体沿用加密首次描述及原观察时间，失败、回滚和重放不更新。描述可缺省且不构成设备证明；采集/替换/清理边界见[README](../README.md#当前会话与客户端描述)，代理/CORS见[网关说明](gateway-integration.md#客户端描述透传与-cors)，当前验证状态见[简化验收](../openspec/changes/archive/2026-10-20-simplify-session-context/verification.md)。

## 管理查询

账号列表支持 `page_size`、`page_token`、`show_deleted` 和 `filter`。page_size 缺省或 0 时为 20，大于 100 截为 100，负数或非整数返回 400。`show_deleted=true` 才包含 PENDING_DELETION/DELETED，未提供或 false 时不包含。

filter 是 `term (AND term)*` 子集，不支持 OR、NOT 或括号。支持：

- `state = ACTIVE|FROZEN|PENDING_DELETION|DELETED`。
- `create_time >= "RFC3339"`、`create_time < "RFC3339"`。
- `identity.phone = "…"`、`identity.email = "…"`。
- `identity.phone_prefix`、`identity.phone_suffix`、`identity.email_prefix`、`identity.email_domain` 的等值条件。

身份条件必须同一种 kind 且作用于同一活跃身份。手机号/邮箱过滤值属于隐私数据，网关与日志应脱敏。游标按返回值原样传递，不能作为授权凭据或自行推断 ID 时间顺序。

审计只接受 `page_size` / `page_token`，按 `(occur_time, UUID)` 升序，限定认证动作及账号关联范围。审计使用独立 v2 游标，绑定 source、资源与认证动作集合；旧或跨范围游标返回 `400 INVALID_PAGE_TOKEN`，客户端从第一页重新开始，不复用用户列表游标。

## 验证与维护

JSON 文档直接维护，修改路由、DTO、scope、状态码或错误时同步更新；不从本文反向生成服务器实现。无依赖的源码对应检查：

```powershell
node scripts/check-openapi.cjs
```

此检查从现有 Router 提取方法与路径，核对覆盖、引用、路径参数及主要响应DTO字段与必填/可空规则（明确读取response.go及消费者current_session.go）；同时检查新查询状态码/scope/no-store、恰好五个采集入口的可选头、登录oneOf和token/身份列表字段边界；它不是完整 OpenAPI schema 校验，也不代替真实 HTTP 测试。提取器只处理当前 handler.go 中直接使用字符串字面量的 Get/Post 等注册；发现 Route/Mount/Method/Handle 或非字面量路径会要求更新提取器。若将注册逻辑移到其他文件或自定义辅助函数，也须同步调整检查范围，不能把它当作通用 Go 路由分析器。

正式结构校验可将工具安装到仓库忽略目录，不修改 Go module 或全局工具：

```powershell
npm.cmd install --prefix .test-output/contract-tools --cache .test-output/npm-cache --no-audit --no-fund @apidevtools/swagger-parser@12.1.0
node -e "const p=require('./.test-output/contract-tools/node_modules/@apidevtools/swagger-parser'); (async()=>{for(const n of ['enduser','admin']){await p.validate('docs/openapi/'+n+'.json');console.log(n+': valid')}})().catch(e=>{console.error(e.message);process.exit(1)})"
```

随后按改动范围运行 `GOWORK=off go test -count=1 ./httpapi/...` 与相关领域测试。标准工具通过只证明文档结构与引用合法；生成客户端、Mock、产品网关和真实业务链路仍需按宿主环境验收。

v0.3.0基础文档验收记录见 [document-go-integration-and-http-contracts](../openspec/changes/archive/2026-10-18-document-go-integration-and-http-contracts/verification.md)。

v0.4.0 会话变更的整体验证与未验证项由[本变更验收记录](../openspec/changes/archive/2026-10-20-add-session-authentication-context/verification.md)汇总；不能用历史验收代替当前检查。
