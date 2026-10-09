## Why

宿主接入需要验证码轮次隔离、批量公开资料和可参与同库事务的账号生命周期契约；现有 API 需要宿主重复认证逻辑或直接耦合账号表。将这些能力集中到 accountkit，可以让各宿主消费固定版本，并将库的设计、实施和验收保留在本仓库。

## What Changes

- **BREAKING** 为 PHONE/EMAIL 的 SIGN_IN、BIND、REAUTH 增加必需 code_id；发码返回 code_id/expire_time，公开 Go 参数和返回类型同步升级，无缺失标识的兼容旁路。
- 轮次绑定目标、渠道、用途及适用的用户/会话，重发使旧轮次失效，旧请求不扣新轮次预算；校验和消费在 Redis 原子完成。
- 增加跨用途、跨轮次的目标失败预算，默认 15 分钟最多 10 次错误；保留已有每轮次数配置及默认 5 次，宿主可显式选择 3 次。
- 新增只返回 ID、显示名、状态的批量 Go 查询、共用于消费者/管理员注销的 BeforeDelete 回调，以及 WithActiveUsers 同事务保护。
- 新增仅供受控离线工具使用的账号导入契约，复用库的身份归一化、加密和摘要，不创建登录会话。
- 扩展 HMAC 轮换、投递失败、匿名化/事务、独立消费与 accountsvc 验收，提供破坏性升级说明；现有 JWT/refresh/数据库冻结规则保持。

## Capabilities

### New Capabilities

- verification-challenges: 验证码轮次、主体绑定、原子消费、跨轮次失败预算及公开协议。
- host-account-contracts: 批量公开资料、宿主注销前置检查和账号事务保护。
- legacy-account-import: 受控离线账号/身份导入与调用者事务边界。

### Modified Capabilities

- embedded-auth-package: 明确本次验证码公开契约升级，保留其余认证/会话/生命周期边界。
- verification-delivery: 发码成功返回轮次，投递失败按标识清理且不退额度。
- code-key-rotation: 轮次与失败预算跨密钥连续，区分新协议内轮换与旧协议切换。

## Impact

影响根 Config/Deps、user/code、user.Service、httpapi/enduser、管理员错误映射、示例和 accountsvc 的协议测试；SQL 查询经 sqlc 生成，冻结 schema 不改写。宿主需同步升级 Go/HTTP 调用者并消费包含这些接口的新固定版本。业务表、团队规则、前端页面、宿主数据迁移和产品部署由各宿主仓库处理，不进入本 Change。
