## Purpose

为无法使用同事务匿名化回调的外部业务服务提供持久、可分页、可补拉的已清理用户记录，使业务方能按 user_id 幂等清理自己的数据。规定记录的写入时机、顺序一致性、拉取契约、保留期与鉴权。

## ADDED Requirements

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

响应体为 `{"purged_users":[{"user_id","purge_time"}],"next_page_token"}`。`next_page_token` SHALL 始终返回当前读取位置，空列表表示已追平；业务方保存它并在下次拉取时传回。格式非法或超过当前已分配最大序号的 `page_token` SHALL 返回 400 `INVALID_PAGE_TOKEN`；后者表示数据库被恢复或重新部署导致序号回退，业务方 SHALL 清空游标从头重拉。游标对调用方不透明，不保证格式稳定，也不提供防伪造保护。响应 SHALL 设置 `Cache-Control: no-store`。接口没有确认语义，重复拉取同一位置返回相同记录（已被保留期清理的除外）。

#### Scenario: 从头拉取与续拉
- **WHEN** 业务方首次不带 page_token 拉取，随后用返回的 next_page_token 继续拉取
- **THEN** 第一次按序返回最早的记录；第二次只返回其后的记录；没有新记录时返回空列表和与传入位置等价的游标

#### Scenario: 非法参数
- **WHEN** page_token 格式非法，或 page_size 为 0、负数或超过 1000
- **THEN** 返回 400，不调用读取

#### Scenario: 数据库恢复后游标超前
- **WHEN** accountsvc 数据库从较早备份恢复，业务方保存的游标大于当前已分配的最大序号
- **THEN** 返回 400 `INVALID_PAGE_TOKEN`；业务方清空游标从头重拉，不会静默跳过之后写入的记录

### Requirement: 保留期与清理

已清理记录默认长期保留。库 SHALL 支持可选保留期配置：设置时不得短于 30 天，否则配置校验失败；设置后，维护任务按写入时间删除超出保留期的记录。文档 SHALL 说明业务方须在保留期内完成拉取，否则可能遗漏清理。

#### Scenario: 默认不清理
- **WHEN** 未配置保留期
- **THEN** 维护任务不删除任何已清理记录

#### Scenario: 保留期过短
- **WHEN** 保留期配置为 7 天
- **THEN** 配置校验失败并指出该配置项

### Requirement: 服务鉴权与暴露边界

`/v1/purgedUsers` SHALL 要求独立的 HTTP Basic 客户端凭据（`ACCOUNTSVC_PURGE_FEED_CLIENTS`，格式与内省客户端相同），不得接受内省凭据、消费者或管理员令牌。凭据缺失或错误 SHALL 返回 401，并带 Basic challenge。未配置凭据列表时，该路由 SHALL 返回 503 `PURGE_FEED_NOT_CONFIGURED`。凭据 SHALL 支持新旧秘密重叠轮换。该路由与内省一样属于内部入口，网关 SHALL 不对公网开放。

#### Scenario: 用内省凭据访问
- **WHEN** 调用方使用只在内省客户端列表中的凭据请求 `/v1/purgedUsers`
- **THEN** 返回 401，不返回任何记录

#### Scenario: 未配置
- **WHEN** 服务未设置 `ACCOUNTSVC_PURGE_FEED_CLIENTS`
- **THEN** 路由返回 503 `PURGE_FEED_NOT_CONFIGURED`，服务其他功能不受影响
