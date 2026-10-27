## ADDED Requirements

### Requirement: 严格认证中间件

门面 SHALL 为嵌入式宿主提供严格认证中间件，用于保护关键业务路由。该中间件的 Bearer 解析、401 响应体与 `WWW-Authenticate` challenge、403 `INSUFFICIENT_SCOPE` 以及 Principal 注入 SHALL 与 RequireScope 一致；令牌有效性 SHALL 按严格认证判定：只依据已提交的数据库会话吊销状态，不读取 Redis。依赖失败 SHALL 返回 503 `DEPENDENCY_UNAVAILABLE`，不得放行，也不得改报 401。请求上下文中已有普通认证注入的 Principal 时，该中间件 SHALL 仍执行严格认证，并以严格认证的结果替换上下文中的 Principal，不得因为已有 Principal 而直接放行。它 SHALL 能与 RequireRecentAuth 串联使用。普通 RequireScope 的行为 SHALL 保持不变。

#### Scenario: Redis 写入失败后的关键路由
- **WHEN** 会话撤销已提交但吊销集写入失败，客户端用该会话的 access 访问普通路由和关键路由
- **THEN** 普通路由按既有 fail-open 规则放行；关键路由返回 401，带 `error="invalid_token"` challenge

#### Scenario: 数据库不可用
- **WHEN** 关键路由收到有效 access，但会话查询失败
- **THEN** 返回 503 DEPENDENCY_UNAVAILABLE，不调用下游 handler

#### Scenario: 外层已有普通认证
- **WHEN** 宿主在外层挂了 RequireScope，又在内层关键路由挂了严格中间件，且该会话已在数据库中吊销
- **THEN** 内层仍执行严格认证并返回 401，不因外层已注入 Principal 而直接放行

#### Scenario: scope 与近期认证
- **WHEN** 严格认证通过，但 scope 不在允许列表中；或串联的 RequireRecentAuth 判定 auth_time 过旧
- **THEN** 分别返回 403 INSUFFICIENT_SCOPE 和既有的近期认证错误，与普通中间件一致
