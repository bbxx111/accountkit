## ADDED Requirements

### Requirement: 以数据库会话状态判定吊销

内省 SHALL 先执行签名、issuer/audience、时效和 ID 格式校验，再按 `sid` 查询数据库会话。只有会话存在、所属账号与 `sub` 一致且未吊销时，才 SHALL 返回 `active:true`。吊销事务一旦提交，后续内省 SHALL 立即返回 `active:false`，不论 Redis 吊销集是否写入成功。内省 SHALL 不查询 Redis 吊销集，也不按刷新期限判定 access：刷新期限已到但未吊销的会话，其 access 在自身有效期内仍为 active。查询数据库失败或超时 SHALL 返回 503 OAuth `temporarily_unavailable`，不得返回 `active:true`，也不得用 `active:false` 掩盖依赖故障。内省 SHALL 不读写会话来源、客户端描述或账号资料，不更新会话的任何字段。同一判定能力 SHALL 以 Go 方法的形式提供给嵌入式宿主，结果与内省一致；普通 Authenticate 和消费者中间件 SHALL 保持 JWT + Redis 吊销校验及 fail-open 行为。

#### Scenario: Redis 写入失败后的撤销
- **WHEN** 撤销事务已提交，但 sid 写入 Redis 吊销集失败
- **THEN** 后续内省返回 `active:false`；普通 Authenticate 仍按既有 fail-open 边界处理

#### Scenario: 数据库不可用
- **WHEN** 令牌签名和时效有效，但会话查询失败或超时
- **THEN** 内省返回 503 `temporarily_unavailable` 和 `Cache-Control: no-store`，不返回主体信息

#### Scenario: 会话不存在或不属于该主体
- **WHEN** `sid` 对应的会话已被清理，或会话的所属账号与 `sub` 不一致
- **THEN** 返回 200 `{"active":false}`，不泄露原因

#### Scenario: 刷新期限已到但未吊销
- **WHEN** 会话的 refresh 期限已到但未被明确吊销，access 仍在自身有效期内
- **THEN** 内省返回 `active:true`，与会话期限规范中的 access 边界一致

#### Scenario: 冻结或注销
- **WHEN** 管理员冻结账号或用户发起注销，事务内吊销了该账号全部会话
- **THEN** 提交后这些会话的 access 立即在内省中返回 `active:false`

## MODIFIED Requirements

### Requirement: 接入方授权与依赖失败处理

接入文档和示例 SHALL 采用分级校验。可以接受吊销延迟的普通接口 SHALL 按本地验签规则使用 JWKS；要求撤销后立即失效的关键接口 SHALL 在每次请求时执行内省，不缓存成功结果，并且 SHALL 在本地验签通过后再调用内省，以免无效令牌消耗内省调用。哪些接口属于关键接口由业务服务决定，并 SHALL 写入其接入说明。业务服务 SHALL 自行检查 scope、必要的近期认证和资源归属。远程超时、网络失败、非 200 或畸形响应 SHALL 拒绝业务访问并作为依赖故障处理，不等同于用户凭据无效。示例客户端 SHALL 默认要求 HTTPS；只有调用方显式允许 HTTP 且部署提供受控外部传输保护（或本地开发）时才使用 HTTP 地址，不因服务端开关或响应重定向自动降级。

#### Scenario: 受限 scope
- **WHEN** 令牌 active:true 或本地验签通过，但 scope 仅为 user:bind 或 user:undelete
- **THEN** 普通业务访问仍被拒绝，只有符合该 scope 的业务动作可继续

#### Scenario: accountsvc 无响应
- **WHEN** 业务服务在关键接口上查询内省超时，或内省返回 503
- **THEN** 不使用历史成功结果或本地验签结果放行，不引导客户端无意义地刷新或退出登录，按依赖不可用处理

#### Scenario: 普通接口与关键接口
- **WHEN** 同一会话被撤销后，客户端继续用尚未过期的 access 访问普通接口和关键接口
- **THEN** 普通接口在 access 剩余有效期内可能仍被放行；关键接口经内省立即拒绝

#### Scenario: 外部 TLS 终止的调用方
- **WHEN** 业务服务经 HTTPS 网关调用，或显式允许 HTTP 与本地网格代理配合
- **THEN** 分别使用经过证书校验的 HTTPS 端点，或由部署方保证跨主机链路加密的 HTTP 应用连接；Basic 凭据、用户 token、不缓存和禁止重定向的行为保持

## REMOVED Requirements

### Requirement: 保留当前消费者认证语义

**Reason**：内省改为以数据库会话状态判定吊销，依赖故障时 fail-closed，不再复用 Redis 吊销检查与 fail-open 行为。

**Migration**：签名、issuer/audience、时效与 ID 格式校验保持不变，由“以数据库会话状态判定吊销”这一要求接替。依赖“Redis 故障时内省仍返回 active:true”的调用方，需要按 503 依赖故障处理；只需要尽力吊销的接口改为本地验签。
