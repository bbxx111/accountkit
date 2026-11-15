# Spec Delta

## MODIFIED Requirements

### Requirement: 管理接口及审计兼容

包 SHALL 保留管理端用户查询、生命周期操作、身份敏感信息查看、会话和审计查询及逐操作授权。管理员身份由宿主注入，未配置管理认证时管理接口 SHALL 返回 503 ADMIN_NOT_CONFIGURED，消费者能力仍可独立使用。新权限验证能力采用可选扩展；仅实现旧角色验证契约的宿主 SHALL 保留原路由角色要求，不强制迁移。

审计 SHALL 支持通用事件及宿主外部查询，认证管理路由继续返回明确的认证 DTO，不直接序列化任意业务数据。审计资源 ID SHALL 显式升级为 UUIDv7，旧审计游标返回 400 INVALID_PAGE_TOKEN；非审计资源名和游标保持。仅注入审计写侧时，已授权审计查询 SHALL 返回 503 AUDIT_QUERY_NOT_CONFIGURED，不静默读取本地空表。默认本地模式 SHALL 保持可用。

身份明文查看 `GET /users/{user}/identities/{identity}:reveal` 的所有响应（成功与错误）SHALL 带 `Cache-Control: no-store`；该头由库自身设置，不依赖宿主或网关配置。

#### Scenario: 管理员权限不足
- **WHEN** 已识别的管理员缺少目标操作权限，或旧宿主路径缺少所需角色
- **THEN** 拒绝操作并沿用基线审计机制记录禁止事件

#### Scenario: 审计服务失败
- **WHEN** 审计记录失败或默认有界队列耗尽
- **THEN** 不改变认证操作结果，保留基线失败与丢弃观测行为

#### Scenario: UUID 审计列表
- **WHEN** 管理员查询有权限访问的账号审计
- **THEN** 资源名使用 users/{user}/auditEvents/{uuid}，原字段语义保留，其他账号及通用扩展敏感内容不泄露

#### Scenario: 身份明文响应禁止缓存
- **WHEN** 获得 identities.reveal 权限的管理员，或旧宿主允许的 super-admin 成功调用 `:reveal`
- **THEN** 200 响应带 `Cache-Control: no-store`，正文仍为既有 `{"name","kind","subject"}`，并写入 IDENTITY_REVEALED 审计

## ADDED Requirements

### Requirement: 可选管理权限验证扩展

包 SHALL 提供稳定的类型化管理权限与可选逐权限验证接口，供根门面及直接管理适配器宿主使用；不扩大已发布 AdminVerifier 的必需方法集，不移除既有角色常量。AdminVerifier 与 AdminPrincipal SHALL 保持成对注入。仅实现旧接口的宿主 SHALL 保留原行为；实现新能力的验证器 SHALL 对每条路由接收确定的操作权限，拒绝后不回退或叠加旧角色放行。

#### Scenario: 旧宿主不修改即可消费
- **WHEN** 外部 Go module 只实现 RequireRole 并按原契约提供管理员主体
- **THEN** 在 GOWORK=off、无本地 replace 下可编译并保留全部旧路由的角色要求；不需要部署环境变量或新增方法

#### Scenario: 新能力优先且没有授权回退
- **WHEN** 验证器同时实现新旧接口，新权限检查拒绝但旧角色检查会允许
- **THEN** 请求被拒绝，旧角色检查不被调用，不发生双重拒绝审计

#### Scenario: 配置缺失不选择旧路径
- **WHEN** accountsvc 提供具备新权限能力的验证器，但其显式角色权限映射为空
- **THEN** 保持新权限路径并拒绝管理操作，不因空集合退回旧接口

### Requirement: 管理权限目录的公共稳定性

管理权限常量 SHALL 与 HTTP 授权文档使用相同的稳定字符串；枚举目录的公开结果 SHALL 为独立副本，调用者不得通过修改返回值改变其他实例或路由的授权要求。新增权限 SHALL 显式绑定到对应操作；不得把通配符作为路由所需权限传给宿主，不得根据请求自报角色、权限或路径参数改变所需权限。

#### Scenario: 外部目录副本被修改
- **WHEN** 宿主修改一次取得的权限目录或原始策略输入
- **THEN** 不改变其他目录读取、已装配实例策略或实际路由的权限要求

#### Scenario: 路由要求精确权限
- **WHEN** 新宿主收到撤销全部会话的路由授权检查
- **THEN** 获得具体的 sessions.revoke_all 权限标识，不是 sessions.* 或用户提交的字符串
