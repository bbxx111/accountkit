## MODIFIED Requirements

### Requirement: 账号授权与有效当前会话

换绑 SHALL 要求完整 user scope、ACTIVE 账号和属于该账号的有效当前会话。近期认证 SHALL 沿用现有 ReauthMaxAge 和 SensitiveOpVerification 配置，默认5分钟且启用；显式关闭仅跳过新鲜度检查。直接库调用 SHALL 同样执行这些规则。近期认证 SHALL 不被描述为强制向被替换地址再次发码的证明。

HTTP 换绑 SHALL 使用相对路由 POST /accounts/me/identities/{identity}:replace，返回 accounts/{user_id}/identities/{identity_id} 资源名；Go 和数据库身份标识保持原契约。换绑使用的身份 Kind SHALL 属于当前 Enabled，且事务完成后的身份集合 SHALL 继续满足 Required 与至少一个有效身份条件。Required SHALL 不要求本次换绑 Kind 自身属于必需列表；直接 Go 调用与 HTTP SHALL 一致执行。

#### Scenario: 近期认证不足
- **WHEN** 已启用新鲜度检查且 access 的 auth_time 缺失或超过配置窗口
- **THEN** 返回400 REAUTHENTICATION_REQUIRED，客户端可通过现有重新认证流程继续，未消费新地址验证码

#### Scenario: 凭据作用域不足或会话失效
- **WHEN** 请求只有 user:bind/user:undelete，或数据库当前会话已吊销、过期、缺失或属于其他用户
- **THEN** 作用域不足返回403，会话失效返回401，均不完成换绑；Redis 吊销查询 fail-open 不绕过数据库写路径检查

#### Scenario: 账号禁止操作
- **WHEN** 账号处于 FROZEN、PENDING_DELETION 或 DELETED
- **THEN** 沿用对应领域状态拒绝，不能完成换绑或恢复该账号

#### Scenario: 当前策略禁止换绑类型
- **WHEN** 持有旧 user 令牌的客户端在 EMAIL 已禁用后尝试 EMAIL 换绑
- **THEN** 在消费验证码之前拒绝，不因旧令牌允许而绕过部署策略

#### Scenario: 允许非必需类型换绑
- **WHEN** Required 为 EMAIL，账号已有 EMAIL 和 PHONE，PHONE 仍启用且其余换绑条件满足
- **THEN** 允许 PHONE 同类换绑，EMAIL 继续满足账号完整性
