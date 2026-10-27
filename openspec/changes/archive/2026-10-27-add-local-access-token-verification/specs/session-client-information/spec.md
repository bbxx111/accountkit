## MODIFIED Requirements

### Requirement: 重试不写入额外活动

合格 refresh 宽限重试和已完成补全的幂等重放 SHALL 返回既有结果，不更新客户端描述或观察时间。普通 access 验证、内省和业务请求 SHALL 不因本能力增加数据库查询、写入或外部解析服务。客户端描述观察时间 SHALL 不被描述为用户最后一次业务操作时间。

#### Scenario: 宽限重复刷新
- **WHEN** 胜者刷新已保存最近描述，后续同轮宽限请求携带不同描述
- **THEN** 返回原 pair，不再次更新快照或时间，不延长会话期限

#### Scenario: 普通业务鉴权
- **WHEN** 宿主调用 Authenticate 或远程内省处理业务请求
- **THEN** Authenticate 沿用 JWT 与 Redis 吊销校验，内省只读取会话吊销状态；两者都不为采集客户端信息访问数据库，也不读写客户端描述或观察时间
