## MODIFIED Requirements

### Requirement: 管理接口及审计兼容
包 SHALL 保留管理端用户查询、生命周期操作、身份敏感信息查看、会话和审计查询及角色限制。管理员身份由宿主注入，未配置管理认证时管理接口 SHALL 返回 503 ADMIN_NOT_CONFIGURED，消费者能力仍可独立使用。

审计 SHALL 支持通用事件及宿主外部查询，认证管理路由继续返回明确的认证 DTO，不直接序列化任意业务数据。审计资源 ID SHALL 显式升级为 UUIDv7，旧审计游标返回 400 INVALID_PAGE_TOKEN；非审计资源名和游标保持。仅注入审计写侧时，已授权审计查询 SHALL 返回 503 AUDIT_QUERY_NOT_CONFIGURED，不静默读取本地空表。默认本地模式 SHALL 保持可用。

身份明文查看 `GET /users/{user}/identities/{identity}:reveal` 的成功响应 SHALL 带 `Cache-Control: no-store`，错误响应沿用统一错误体的 no-store；该头由库自身设置，不依赖宿主或网关配置。

#### Scenario: 管理员权限不足
- **WHEN** 已识别的管理员缺少目标操作角色
- **THEN** 拒绝操作并沿用基线审计机制记录禁止事件

#### Scenario: 审计服务失败
- **WHEN** 审计记录失败或默认有界队列耗尽
- **THEN** 不改变认证操作结果，保留基线失败与丢弃观测行为

#### Scenario: UUID 审计列表
- **WHEN** 管理员查询有权限访问的账号审计
- **THEN** 资源名使用 users/{user}/auditEvents/{uuid}，原字段语义保留，其他账号及通用扩展敏感内容不泄露

#### Scenario: 身份明文响应禁止缓存
- **WHEN** super-admin 成功调用 `:reveal`
- **THEN** 200 响应带 `Cache-Control: no-store`，正文仍为既有 `{"name","kind","subject"}`，并写入 IDENTITY_REVEALED 审计
