## Context

动机及依赖见 [proposal.md](proposal.md)。已有 GetProfileAttributes/PatchProfileAttributes 覆盖读取、行锁、完整 Schema 校验和生命周期；HTTP 缺少适配。enduser.Service 为公开必需接口，当前会话详情已有可选能力接口的兼容先例。accountsvc 的库配置来自 ConfigFromEnv，尚不读取资料 Schema。

用户明确的设计输入：profile_attributes 全部属于本人可读可改的资料。设计默认采用普通 user scope、无需近期重新认证；“全部可修改”不豁免 Schema、状态、容量与禁止存储凭据等约束。

## Goals / Non-Goals

**Goals:** 本人能够通过内置消费者路由完整读取、部分修改和删除声明属性；嵌入库和 accountsvc 使用相同协议；保持旧适配器源码兼容。

**Non-Goals:** 管理员资料路由、他人公开资料、字段权限回调、Schema 查询/在线编辑、JSON Patch、PUT 整体替换、资料搜索、数据库迁移。display_name 仍属于既有 users/me。

## Decisions

### 1. 独立 singleton 资源

选用 GET/PATCH `/users/me/profile`（accountsvc 为 `/v1/users/me/profile`），资源名为 `users/{实际用户ID}/profile`。这是每个账号唯一且随账号存在的资料资源，不提供 Create/Delete。参考 [AIP-156](https://google.aip.dev/156)。

比较三种方案：扩展 users/me 最少增加路由，但会混合显示名更新和资料合并的事务/校验语义；逐属性子资源不适合嵌套对象和跨字段校验；独立资料资源可复用当前完整合并事务，因此采用第三种。保留资料字段名 profile_attributes，避免另造 attributes/data 协议别名。

GET 和 PATCH 成功均返回 200、application/json：

```json
{"name":"users/u_0000000000001/profile","profile_attributes":{"bio":"你好","preferences":{"locale":"zh-CN"}}}
```

无资料返回 profile_attributes 为 {}，不省略、不返回 null。name 为只读输出字段；不新增资料时间、Schema 版本或 revision。

PATCH 请求使用 application/json 的显式 DTO：

```json
{"profile_attributes":{"bio":"你好","preferences":{"locale":"zh-CN"},"timezone":null}}
```

profile_attributes 必须出现且是对象；外层只允许此字段，不接受 name、user_id、display_name 或 update_mask。内层执行 [RFC 7396](https://www.rfc-editor.org/rfc/rfc7396) 合并语义：缺失保持、对象递归合并、数组整体替换、null 删除。外层 {} 和属性为 null 拒绝；内层 {} 是无变化操作。该端点沿用现有 JSON 接口的 Content-Type 宽容行为，不新增媒体类型检查；本次不宣称外层请求是通用 Merge Patch 文档。

### 2. 认证与所有权

两个路由均使用 RequireScope(user)，user:bind 和 user:undelete 不允许访问，不增加 RequireRecentAuth。目标只取可信 Principal.UserID，忽略查询参数作为身份来源；不注册 `/users/{id}/profile` 或管理路由。所有资料均完整返回，不依据 readOnly/writeOnly 注解屏蔽或禁止修改。

领域生命周期保持：GET 允许 ACTIVE/FROZEN，PATCH 只允许 ACTIVE；认证中间件先行，无法获取有效令牌的冻结用户不会因此获得新登录通道。HTTP 测试需要覆盖通过认证后读到冻结状态的情况，不承诺冻结用户一定能继续使用被吊销令牌。

两条路由在认证中间件外设置 Cache-Control: no-store，覆盖成功及认证/业务错误响应。

### 3. 有界且保真的 HTTP 适配

新增 enduser.ProfileAttributesService 可选接口，方法签名沿用领域两个方法；handler 从 Deps.Users 断言，不扩大 enduser.Service。实际 user.Service 自然满足；未实现能力和未配置 Schema 统一返回 503 PROFILE_ATTRIBUTES_NOT_CONFIGURED，检查顺序为认证/scope → 能力 → 请求解析 → 领域校验。已实现但未配置 Schema 的适配器由领域方法报告配置缺失，不承诺其先于语法错误。

增加独立 profileResource DTO，不序列化领域 View 或 sqlc 行。入参保留 json.RawMessage 原始字节，不经 map[string]any/float64。外层读取上限沿用 Deps.MaxBodyBytes（默认 64 KiB，含外层和空白），内层原始补丁及规范化资料仍受领域 16 KiB 限制。独立解析逻辑拒绝外层重复键、未知字段、缺失值、尾随内容、非法 UTF-8/Unicode；内层原始字节完整交给现有领域解析，拒绝重复键、非法编码等。不得先重编码内层以规避原始字节预算。

不要复用会写出具体解析器错误的 decodeJSON 日志路径；记录安全分类与 request_id，避免未知属性名和原始内容进入日志。

### 4. 错误、审计和并发

沿用 apierror 包外壳（error.code 数字、status、reason、message）：

| 条件 | HTTP / status | reason |
|---|---|---|
| 缺失/非法令牌 | 401 UNAUTHENTICATED，保留 WWW-Authenticate | 原认证 reason |
| scope 不足 | 403 PERMISSION_DENIED | INSUFFICIENT_SCOPE |
| 请求 JSON 语法错误（包括内层）、外层字段/编码/总大小不合法 | 400 INVALID_ARGUMENT | MALFORMED_BODY |
| 语法可解析但内层非对象、重复键、编码/容量或 Schema 校验失败 | 400 INVALID_ARGUMENT | INVALID_PROFILE_ATTRIBUTES |
| 领域状态拒绝 | 400 FAILED_PRECONDITION | INVALID_ACCOUNT_STATE |
| 账号不存在 | 404 NOT_FOUND | NOT_FOUND |
| 能力或 Schema 未配置 | 503 UNAVAILABLE | PROFILE_ATTRIBUTES_NOT_CONFIGURED |
| 已识别的依赖不可用 ErrUnavailable | 503 UNAVAILABLE | DEPENDENCY_UNAVAILABLE |
| 存储不变量、未知或提交结果不确定错误 | 500 INTERNAL | 沿用固定 internal error |

所有新增参数错误使用固定消息；首版不增加 violations HTTP 格式，不透传 err.Error() 或第三方校验器错误。MALFORMED_BODY 消息使用不含固定 64 KiB 数字的专用安全文案，适应自定义上限。存储错误不因猜测被改成 503，也不自动重试提交。

分类示例：外层 `null`、`{}` 是 MALFORMED_BODY；`{"profile_attributes":null}` 是内层非对象，归 INVALID_PROFILE_ATTRIBUTES。冒号后和值末尾的 JSON 空白计入内层原始补丁字节预算，HTTP 不先压缩空白。

直接复用领域账号行锁；不加 ETag、If-Match 或 revision 协议。并发不重叠补丁保留双方结果，重叠内容以后执行事务为准。成功响应是本次事务结果，不承诺返回发送时数据库的最新值。失败后客户端按需 GET 核对，不盲目重放。

沿用 USER_PROFILE_ATTRIBUTES_UPDATED 尽力异步审计，Meta 传 request_id；当前固定 SERVICE/accountkit-host actor 保持，不把新增 HTTP 接口误描述为新增 END_USER 审计 actor。实际变化才更新时间和发事件；本次不扩展审计模型。

### 5. accountsvc 启用路径

serve 新增可选 ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE。在服务装配阶段、监听和创建运行依赖前，从本地普通文件最多读取 64 KiB + 1 字节用于检测超限，传入 Config.ProfileAttributes.Schema 并执行既有校验。不支持 URL、热更新或从客户端提交 Schema；空配置保持 nil。文件缺失、不可读、非普通文件、空文件、超限或 Schema 无效均失败退出，错误仅指出配置项及安全分类，不输出路径、文件内容或校验器原文。

ConfigFromEnv 和 accountkit.New 保持无文件 I/O。migrate 不读取或验证此服务设置；设置无效路径也不影响 migrate。模板只展示注释配置及只读挂载示例，不默认启用；多副本使用相同 Schema。Schema 变更重启生效，数据库中已有资料不自动修复。

## Risks / Trade-offs

- [既有私有字段将对本人开放] → 升级说明明确所有 profile_attributes 都必须允许本人完整读取和修改；升级前迁出不满足此约束的数据，不能依赖 Schema 注解隐藏。
- [已移出 Schema 的历史属性仍可读但无法修改/删除] → 保留现有完整读取及未知字段拒绝规则；部署方应在过渡 Schema 下先清理再删除声明。“全部可改”指无字段权限限制，不豁免当前 Schema。
- [客户端误用 {} 清空或忽略父对象删除影响] → 文档提供 null 删除、嵌套合并、数组替换及空补丁示例，补对应行为测试。
- [错误日志泄露资料] → 新端点使用固定错误分类，测试覆盖未知键、私密值、畸形 JSON 和服务错误。

## Migration Plan

本变更不执行数据库迁移。实施计划见[详细计划](../../../docs/superpowers/plans/2026-10-21-self-profile-http.md)，按任务完成 HTTP/装配/文档与测试。发布前说明新增本人访问面，旧 Service 适配器无需增加方法；需要新功能的适配器实现可选接口。关闭 Schema 会令资料接口返回 503，但不删除存储。应用回退到本变更之前只撤去 HTTP/配置能力，资料列和数据保持；其他基础结构回退仍须遵循迁移手册，不能据此承诺跨版本可回退。

基础 change 的“不新增内置路由”是先前范围记录，本变更明确取代该限制；先归档基础 change，再同步本变更，并在主规格说明本人全部访问的优先契约。历史 verification 不改写。
