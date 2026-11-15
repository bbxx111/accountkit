## MODIFIED Requirements

### Requirement: JWKS 轮换与故障分类

密钥缓存 SHALL 最多使用 1 小时，未知 kid 刷新 SHALL 合并并发请求且间隔至少 60 秒。可用缓存中的已知 key SHALL 允许提供方短时故障期间继续验证；没有可用 key 且依赖拉取失败时 SHALL 返回 503，成功刷新仍无对应 key 时 SHALL 返回 401。静态配置错误 SHALL 阻止启动；首次发现/密钥加载 SHALL 在就绪后后台执行，远端故障不得阻止启动或改变整体 readiness。管理员请求单次等待 SHALL 不超过3秒；预加载 SHALL 与请求刷新合并并遵守相同冷却，停机取消并等待预加载与验证器在途刷新，单请求取消不得取消其他调用者共享的刷新。

#### Scenario: 正常换钥
- **WHEN** 提供方发布新 key 并签发未知 kid 的 token
- **THEN** 有界刷新后接受有效新 token，并发未知 kid 请求不形成无界拉取

#### Scenario: 提供方不可用
- **WHEN** 拉取失败且旧缓存已过期或不包含请求所需 key
- **THEN** 返回 503，不无限延长旧缓存、不把依赖故障误报为错误凭据；消费者接口仍按自己的规则工作

#### Scenario: 启动故障隔离
- **WHEN** 管理员提供方不可达而数据库和 Redis 正常
- **THEN** 服务正常就绪，管理员请求503；恢复并超过冷却窗口后有效令牌可通过，无需重启

## ADDED Requirements

### Requirement: 显式外部令牌格式
管理员 SHALL 按 ADMIN_TOKEN_PROFILE 选择 default 或 rfc9068 格式，缺省为 default；未知格式 SHALL 在启动前拒绝，管理员禁用时非空显式profile SHALL 作为半配置拒绝。rfc9068 SHALL 校验 at+jwt/application/at+jwt header 及必需 iss/aud/exp/sub/client_id/iat/jti。两种格式 SHALL 保持 issuer+sub 管理身份及角色授权，不应用服务权限或scope规则。

#### Scenario: 禁止自动格式回退
- **WHEN** 配置 rfc9068 但令牌缺少必需声明或访问令牌类型
- **THEN** 管理认证401，不回退到 default 格式

#### Scenario: 默认模式改名
- **WHEN** 管理认证启用且 ADMIN_TOKEN_PROFILE 省略、为空或为 default，或者显式使用旧模式值 azp
- **THEN** 前三者采用相同默认校验且不要求 azp 声明；旧模式值在启动前拒绝，不作为别名
