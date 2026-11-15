# purged-user-feed Specification

## Purpose
为无法使用同事务匿名化回调的外部业务服务提供持久、可分页、可补拉的已清理用户记录，使业务方能按 user_id 幂等清理自己的数据。规定记录的写入时机、顺序一致性、拉取契约、保留期与鉴权。

## Requirements

### Requirement: 清理事务内写入已清理记录

账号清理（purge）SHALL 在匿名化事务内为该账号写入一条已清理记录，包含 user_id、单调递增的游标序号与写入时间。记录 SHALL 与匿名化、会话撤销及宿主 Anonymizer 同提交、同回滚：任一步失败时不留下记录，成功提交后记录必然存在。每个账号至多一条记录。写入 SHALL 在该表的写锁下进行，使游标序号顺序与提交可见顺序一致：任何已提交记录的序号都小于之后提交的记录，按游标拉取不会因并发清理而漏读。记录 SHALL 只包含 user_id 与时间，不包含任何身份目标、凭据或资料。

#### Scenario: 清理成功
- **WHEN** 冷静期到期的账号被维护任务清理
- **THEN** 已清理记录与匿名化在同一事务提交，记录的 user_id 为该账号

#### Scenario: 宿主回调失败
- **WHEN** 清理事务中宿主 Anonymizer 返回错误
- **THEN** 整个事务回滚，不写入已清理记录，下一轮重试

#### Scenario: 并发清理不漏读
- **WHEN** 多个清理事务并发提交，同时拉取方按游标持续读取
- **THEN** 拉取方最终读到全部已提交记录，没有任何记录因序号早于游标而被跳过

### Requirement: 游标分页拉取

库 SHALL 提供 Go 方法，从给定游标之后按序号升序读取已清理记录，返回记录与新的游标位置。accountsvc SHALL 提供 `GET /v1/purgedUsers`，参数如下：

- `page_token`：不透明游标，省略表示从最早的记录开始；
- `page_size`：默认 100，最大 1000，超出范围返回 400。

响应体为 `{"purged_users":[{"user_id","purge_time"}],"next_page_token"}`。`next_page_token` SHALL 始终返回当前读取位置，空列表表示已追平；业务方保存它并在下次拉取时传回。

#### Scenario: 从头拉取与续拉
- **WHEN** 业务方首次不带 page_token 拉取，随后用返回的 next_page_token 继续拉取
- **THEN** 第一次按序返回最早的记录；第二次只返回其后的记录；没有新记录时返回空列表和与传入位置等价的游标

#### Scenario: 非法参数
- **WHEN** page_token 格式非法，或 page_size 为 0、负数或超过 1000
- **THEN** 返回 400，不调用读取

#### Scenario: 数据库恢复后游标超前
- **WHEN** accountsvc 数据库从较早备份恢复，业务方保存的游标大于当前已分配的最大序号
- **THEN** 返回 400 `INVALID_PAGE_TOKEN`；业务方清空游标从头重拉，不会静默跳过之后写入的记录

### Requirement: 清理游标校验与恢复

格式非法或超过当前已分配最大序号的 `page_token` SHALL 返回 400 `INVALID_PAGE_TOKEN`；后者表示数据库被恢复或重新部署导致序号回退，业务方 SHALL 清空游标从头重拉。游标对调用方不透明，不保证格式稳定，也不提供防伪造保护。

#### Scenario: 恢复后重建调用方游标
- **WHEN** page_token 非法或大于当前已分配最大序号
- **THEN** 返回 400 INVALID_PAGE_TOKEN；超前表示数据库恢复或重新部署造成序号回退，业务方清空游标从头重拉；游标格式不保证稳定且不提供防伪造保护

### Requirement: 清理拉取的缓存与重复读取

响应 SHALL 设置 `Cache-Control: no-store`。接口没有确认语义，重复拉取同一位置返回相同记录（已被保留期清理的除外）。

#### Scenario: 重复读取不确认消费
- **WHEN** 调用方重复拉取同一位置
- **THEN** 在记录未被保留期清理时返回相同记录，接口不确认消费，每次响应均设置 Cache-Control: no-store

### Requirement: 保留期与清理

已清理记录默认长期保留。库 SHALL 支持可选保留期配置：设置时不得短于 30 天，否则配置校验失败；设置后，维护任务按写入时间删除超出保留期的记录。文档 SHALL 说明业务方须在保留期内完成拉取，否则可能遗漏清理。

#### Scenario: 默认不清理
- **WHEN** 未配置保留期
- **THEN** 维护任务不删除任何已清理记录

#### Scenario: 保留期过短
- **WHEN** 保留期配置为 7 天
- **THEN** 配置校验失败并指出该配置项

### Requirement: 服务鉴权与暴露边界

`/v1/purgedUsers` SHALL 只使用服务认证规格的统一注册表执行独立的服务调用认证：HTTP Basic 客户端凭据，或外部提供方签发的客户端凭据 Bearer 访问令牌，并在认证后要求 `purged_users.read`。同一客户端 SHALL 可以同时拥有内省权限；只持有内省权限的已认证客户端 SHALL 不得拉取，消费者或管理员令牌 SHALL 不作为服务凭据接受。

#### Scenario: 用内省凭据访问
- **WHEN** 清理能力已配置，调用方使用只持有 tokens.introspect 的注册客户端的有效 Basic 凭据或有效 Bearer 令牌请求 `/v1/purgedUsers`
- **THEN** 两者均返回 403 `CLIENT_NOT_AUTHORIZED`，不查询或返回任何记录

#### Scenario: 未配置
- **WHEN** 统一注册表无人持有 purged_users.read
- **THEN** 路由返回 503 `PURGE_FEED_NOT_CONFIGURED`，serve 启动时记录一条指出相关变量名的 WARN，服务其他功能不受影响

#### Scenario: Bearer 拉取
- **WHEN** 调用方携带有效服务访问令牌，azp 对应注册客户端已启用 bearer 且持有 purged_users.read，请求 `/v1/purgedUsers`
- **THEN** 返回与 Basic 调用相同格式的分页结果

#### Scenario: 令牌无效与签发方不可用
- **WHEN** 清理能力已配置，调用方的服务令牌已过期，或签发方 JWKS 不可用
- **THEN** 前者返回 401 `TOKEN_INVALID`，后者返回 503 `DEPENDENCY_UNAVAILABLE` 与 `Retry-After: 1`，依赖故障不返回 401
#### Scenario: 统一身份拉取
- **WHEN** 统一客户端同时拥有两项权限，以有效 Basic 或 Bearer 请求清理拉取
- **THEN** 正常返回原分页数据，不因客户端也能内省而拒绝

#### Scenario: 注册表权限拒绝
- **WHEN** 清理能力已配置，但有效 Basic 或 Bearer 客户端只持有 tokens.introspect
- **THEN** 返回 403 CLIENT_NOT_AUTHORIZED，不查询记录；Basic 不带挑战，Bearer 带 insufficient_scope 挑战

#### Scenario: 注册表未授权任何调用方
- **WHEN** 统一注册表无人被授予 purged_users.read
- **THEN** 路由先于认证和参数解析返回 503 PURGE_FEED_NOT_CONFIGURED，启动记录变量名级别的 WARN

#### Scenario: 协议和故障不变
- **WHEN** 已获授权的调用方提交非法 page_token，或 Bearer 必需公钥不可用
- **THEN** 分别返回原 400 INVALID_PAGE_TOKEN 或 503 DEPENDENCY_UNAVAILABLE 与 Retry-After: 1，均 no-store

### Requirement: 清理拉取认证错误协议

错误 SHALL 使用 AIP-193 格式：凭据缺失、错误或方式未启用返回 401 `INVALID_CLIENT`；携带的 Bearer 令牌无效返回 401 `TOKEN_INVALID`；有效 Bearer 未登记或已认证客户端缺拉取权限返回 403 `CLIENT_NOT_AUTHORIZED`；签发方不可用返回 503 `DEPENDENCY_UNAVAILABLE` 并带 `Retry-After: 1`。有效 Basic 缺权限的 403 SHALL 不带挑战，Bearer SHALL 按服务认证规格给出 insufficient_scope 挑战。

#### Scenario: 服务认证失败保留协议与挑战
- **WHEN** 清理拉取已配置，但调用凭据、权限或签发方发生相应错误
- **THEN** 使用 AIP-193 的 401 INVALID_CLIENT、401 TOKEN_INVALID、403 CLIENT_NOT_AUTHORIZED 或带 Retry-After: 1 的 503 DEPENDENCY_UNAVAILABLE；有效 Basic 缺权限不带挑战，Bearer 给出 insufficient_scope 挑战

### Requirement: 清理拉取未配置的优先响应

无人持有清理权限时，该路由 SHALL 先于认证和参数解析返回 503 `PURGE_FEED_NOT_CONFIGURED`。

#### Scenario: 未配置先于凭据和参数
- **WHEN** 统一注册表无人持有 purged_users.read，收到任意凭据和参数的拉取请求
- **THEN** 先返回 503 PURGE_FEED_NOT_CONFIGURED，不执行认证或参数解析

### Requirement: 清理拉取内部暴露与凭据轮换

Basic 凭据 SHALL 支持新旧秘密重叠轮换。该路由与内省一样属于内部入口，网关 SHALL 不对公网开放。SHALL 不保留旧逐接口认证路径，不改变分页、清理事务或传输边界。

#### Scenario: 内部路由保持轮换与原有边界
- **WHEN** 部署网关或轮换清理拉取 Basic 秘密
- **THEN** 网关不向公网开放该内部入口，Basic 新旧秘密可以重叠轮换；不恢复旧逐接口认证路径，不改变分页、清理事务或传输边界
