# Proposal

## Why

业务服务需要通过手机号或邮箱查找当前关联账号，并取得状态、昵称和头像，用于邀请、成员展示等不同业务。accountkit 已有身份摘要匹配和统一服务认证，但尚无通用身份解析接口，调用方不应为此访问认证表或借用管理员权限。

## What Changes

- 新增服务端 `POST /v1/identities:resolve`，每次接受一个 `PHONE` 或 `EMAIL` 身份，返回 `user_id`、`state`、`display_name`、`avatar_url`。
- 注册独立权限 `identities.resolve`，复用现有统一服务客户端 Basic/Bearer 认证；权限采用复数资源名加动作。
- 只解析当前未解绑身份；ACTIVE、FROZEN、PENDING_DELETION 账号均可返回。未注册、已解绑、已匿名化且没有新关联统一返回 404；身份重新绑定后返回当前账号。
- 账号状态和资料为查询时的一致快照，不构成后续业务授权。冻结及注销冷静期内返回保留的基础资料，现有公开资料接口的隐藏规则保持。
- 身份规范化、密钥轮换匹配和领域查询可供嵌入式宿主复用；HTTP 只装配在 accountsvc 的服务入口。
- 增加脱敏审计、错误映射、权限隔离、生命周期与并发验收，并同步配置文档和 HTTP 覆盖登记。

## Capabilities

### New Capabilities

- `identity-resolution`：手机号／邮箱到当前账号的单条解析、最小资料视图、一致性、错误和隐私契约。

### Modified Capabilities

- `accountsvc-service-authentication`：将身份解析纳入统一认证、逐接口授权、权限目录、挑战和固定观测标签。
- `accountsvc-runtime`：身份解析路由装配、未配置能力响应和部署示例，保持迁移命令与服务凭据隔离。

## Impact

- 预计修改 `user/` 领域查询与公开结果类型、`user/query.sql` 及 sqlc 生成结果，以及 `internal/accountsvc/` 的处理器、配置、装配和测试；不需要数据库结构迁移。
- 更新 `docs/accountsvc.md`、README 的宿主接口说明、部署模板、`sdks/http-coverage.json` 与对应覆盖检查。服务权限不能进入消费者或管理 SDK 的运行接口。
- 新权限需显式授予；原客户端不会自动获得身份解析能力。回退旧二进制前须移除该权限配置，旧版本会拒绝它。
- 本变更不实现家庭业务、通知、批量解析、模糊搜索、第三方身份解析、登录／自动注册、账号合并、资料扩展字段或新的凭据体系。
