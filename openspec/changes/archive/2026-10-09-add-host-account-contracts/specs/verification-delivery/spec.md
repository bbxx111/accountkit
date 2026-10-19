## MODIFIED Requirements

### Requirement: 真实 SMTP 邮件投递

项目 SHALL 提供可选 SMTP 邮件发送能力，支持登录、绑定和重新认证三种用途，消息 SHALL 包含对应验证码和有效期。accountsvc 首版 SHALL 使用 SMTP 邮件并显式禁用短信，不使用日志发送器作为运行回退。

#### Scenario: 邮件登录闭环
- **WHEN** 用户请求邮箱登录验证码且 SMTP 接受邮件
- **THEN** 返回 code_id 和 expire_time，邮件验证码与该轮标识按配置有效期和次数完成登录；SMTP 接受不表示最终送达

#### Scenario: 区分验证码用途
- **WHEN** 分别请求登录、绑定或重新认证邮件
- **THEN** 邮件说明正确用途，验证码保持现有用途隔离，不可跨用途使用

### Requirement: 投递失败保留额度契约

SMTP 网络、超时和服务拒绝 SHALL 映射为 503 `DEPENDENCY_UNAVAILABLE`，保留原发送失败审计和已经消耗的额度/冷却。发送器 SHALL 不自动重发，也不得将 SMTP 接受等同于最终到达邮箱。投递失败 SHALL 不返回成功轮次；清理 SHALL 仅作废仍匹配本次 code_id 的轮次，不删除并发产生的较新轮次，不恢复已被替换的旧轮次。清理依赖故障 SHALL 通过原到期时间失效，不能退额度或降级验证。

#### Scenario: 发送失败后重试
- **WHEN** 已生成验证码的 SMTP 提交失败，客户端立即重试
- **THEN** 原额度和冷却仍生效，不退还次数、不触发后台无限重试，返回结果不声称已送达

#### Scenario: 失败清理与新轮次竞争
- **WHEN** A 轮次投递失败后执行清理，但 B 已成为最新轮次
- **THEN** A 的清理不删除 B，发送额度和冷却保持，不恢复此前旧轮次
