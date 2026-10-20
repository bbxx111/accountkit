# Design

## Context

设计已由用户连续明确批准，动机见 [proposal](proposal.md)。工作基线为 `b414fef`，分支 `refactor/simplify-session-context`。前一活跃变更 [consolidate-session-context-initial-schema](../2026-10-20-consolidate-session-context-initial-schema/design.md) 保持历史，本次 delta 覆盖其当前结构决定；不改已发布 v0.3.0 或冻结源。

## Goals / Non-Goals

目标：会话来源只有非空集合；客户端数据只有一份完整观察值；业务层校验和显式 DTO 管理可扩展 JSON；当前空库安装保持 version=1。

非目标：未知来源兼容态、跨请求字段拼接、首次环境展示、任意 JSON HTTP 输入、JWT 热路径查库、自动迁移旧库、生产 0002、提交/推送/发布/归档。

## Decisions

### 1. 来源是领域不变量

删除 session 的 `authentication_identities_known` 和公开 `AuthenticationIdentitiesKnown`。新会话和重新认证在既有账号/会话锁下验证具体身份归属与有效状态并写入非空去重集合。当前会话查询、重新认证、解绑和换绑使用来源时检查缺失并报内部不变量错误，禁止用空集合表示安全或自动补造来源。常规 Authenticate/内省不增加数据库访问。

解绑仍精确撤销命中任一来源的同账号未吊销会话，包含自然过期但未清理者。相关缺失来源不能被当作未命中而放行。换绑仍撤销其他会话；当前会话仅在非空来源不含旧身份时保留。BIND 证明不改变来源。所有失败保持事务回滚与既有凭证消费边界。

### 2. 单份 JSONB 存储

`session_client_info` 仅保留 `session_id`、`client_info JSONB NOT NULL`、`observe_time`、`create_time`、`update_time`，主键为 `session_id`。归属及按用户清理通过 session 关联，继续使用 session(user_id,id) 索引，不冗余保存 user_id。删除 snapshot_kind、平坦描述列及 INITIAL/LATEST。数据库只保留主键、非空和 ID 格式等基本约束，不添加 JSON 对象形状或业务 CHECK、外键、自定义校验函数；JSON 对象及内容由业务读写校验。

查询仅以账号和会话定位一条描述。写入保留会话归属复核，与业务动作同事务。无描述无行；有描述整体 UPSERT，不先读取旧值合并。读取不合法 JSON 为内部错误，禁止静默返回空对象；无行仍是合法缺省。

### 3. Go 平坦模型与嵌套 JSON

保留 `ClientInfo` 现有平坦字段并新增 `DeviceManufacturer string`、`AppBuild string`、`Extensions map[string]json.RawMessage`。存储/公开 JSON 为 `{type,os:{name,version},device:{type,manufacturer,model},app:{name,version,build},browser:{name,version},extensions:{...}}`，缺失成员省略。WEB/NATIVE 仅描述客户端类型，app/browser 可同时存在，不因类型清除信息。

固定共享接口：`user.NormalizeClientInfo(*ClientInfo) (*ClientInfo, error)`、`user.MarshalClientInfo(*ClientInfo) ([]byte, error)`、`user.UnmarshalClientInfo([]byte) (*ClientInfo, error)`。Normalize 只规范化当前观察并深拷贝，空描述返回 nil，不与历史值合并。Marshal/Unmarshal 使用同一嵌套结构和校验；存储调用者仅在非 nil 时写入。两面 DTO 显式构造同样结构，不直接暴露 sqlc 行。

`MergeClientInfo` 如为兼容保留，只服务同一 HTTP 请求的 UA→Hints→显式值优先级，不用于持久化，也不能消除 app/browser 共存。单独的版本/build 是当前观察的合法字段，不从旧描述补名称。更换名称而没有版本时，整体替换自然移除旧版本。

### 4. 校验与扩展职责

核心名称、制造商、型号最多64个 Unicode 字符；版本32，build64。先拒绝非法 UTF-8/控制字符再 trim。规范化完整 JSON（包含键、结构和转义）最多2048字节，原始 JSON 在解析前限制为16KiB。核心对象拒绝未知键、重复键和错误类型；枚举非法值拒绝。数字以精确十进制规范化，先计算指数展开长度再有界分配，避免 JSONB 展开后超限；保持整数精度，不接受 NaN/Infinity。

extensions 仅来自 Go/`Deps.ClientInfo` 回调。允许 JSON primitive/null/object/array，所有层级 key 匹配 `[a-z][a-z0-9_]{0,63}`；根对象不计节点和深度，直接值深度1，每个值（含容器和数组元素）计一个节点，总计最多64，嵌套深度最多4，每个数组最多16项，每个字符串最多256字符。验证 RawMessage 时必须在 map 解码前发现重复键、非法 UTF-8 和控制字符；返回结果不复用输入 map 或字节切片。

所有层级扩展键拒绝已知敏感名与明确词段，如 password、secret、token、credential、authorization、cookie、验证码、raw UA/user_agent、IP/address、location/latitude/longitude、device_id/hardware_id/imei/mac 等；实现维护可审查的固定规则并测试，不声称识别任意值中的隐私。宿主负责扩展语义、隐私白名单和允许的来源。

### 5. 采集与观察时间

既有显式头、Hints、UA 的有界解析和凭证消费前校验保持。扩展、制造商/build 可由宿主回调或直接 Go 提供；本次不增加任意 JSON 请求体/头。HTTP parser 可按原标准输入识别新核心字段，但不新增未约定的显式头。

成功登录、最终补全、重新认证、新一轮 refresh 有规范化非空描述时整体替换并记录本次服务器观察时间，无描述保持旧值及时间。pending 首次描述和观察时间继续放加密 payload；最终本次非空描述优先，否则整体沿用首次描述与首次观察时间，不拼接。首次无描述且完成有描述时只写本次一条。失败、事务回滚、refresh 宽限重试、完成结果重放均不写描述/时间。

### 6. 初始定义与兼容

用户明确批准本轮直接整合当前未发布 0001；同步 down、baseline、当前固定哈希与 sqlc，不新增生产 0002。预检要求简化的完整结构，并明确拒绝旧标记、snapshot_kind/平坦快照和非 session_id 单列主键。旧 v0.3.0 v1、前一 b414fef v1、撤回 v2 均无写拒绝；保留 catalog、数据、version/dirty，dirty 优先。

三代历史保护继续固定源/v0.1-v0.2、v0.3.0 精确标签和当前定义。前一整合基线是测试旧结构夹具，不新增发布代际或修改冻结源。历史合成凭据格式测试需显式构造有效认证关联，或验证缺失来源被使用时拒绝；不能把 fixture 补关联宣称为生产自动转换。

### 7. 文件所有权与执行

根代理负责 SQL、生成代码、迁移检查、`service_client_info.go`、pending/登录/refresh 的描述持久化、恢复与整体集成。客户端代理负责 `client_info.go`、客户端单测、`httpapi/clientinfo` 和两面 `response.go` 及其 DTO 测试。认证来源代理负责 `service_session_context.go`、`session_context_types.go`、`service_identity_replacement.go`、当前会话 HTTP 与来源测试；`service.go`/共享测试由根代理协调，避免并发写同文件。

先固定本工件及接口，再进行独立文件任务；生成后的 SQL 接口由根代理同步给认证代理。每任务 RED/GREEN 后独立审查；最终合并检查及完整门禁由根代理负责。用户已批准执行，无需再次设计确认；此次不提交。

## Risks / Trade-offs

- 旧同号数据库无法自动采用新结构 → 拒绝并保留数据，需人工核验新空资源或单独迁移方案。
- 旧程序可能通过较弱预检而启动 → 文档明确阻止混跑/未验证回退，不依赖旧预检兜底。
- 自报扩展可在任意普通值中夹带隐私 → 有界校验与敏感键防线只覆盖已知结构，宿主负责语义白名单。
- RawMessage 共享造成后续变异或竞态 → 规范化、解码与 DTO 均测试深拷贝，race 门禁核验。
- 两个未归档变更都涉及当前初始结构 → 本变更明确后继覆盖，保留前一记录，归档另行授权。

## Migration Plan

新空实例执行当前完整 0001，重复 Up 无变化。旧同号或撤回 v2 不转换、不 Force、不改版本、不清库；产品数据迁移另行设计。公开调用移除 known/initial 字段与快照枚举，改读非空来源及单份 nested client_info，Go 用字段名初始化并配置扩展白名单。验证使用任务专属一次性数据库及独立恢复源/目标；证据写入 verification.md，原始输出留在 .test-output。
