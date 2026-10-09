# verification-challenges Specification

## Purpose

为手机号与邮箱验证码提供宿主和客户端可依赖的验证轮次、主体绑定及跨轮次失败预算，使重新发送、并发校验、密钥轮换和投递异常具有一致的安全行为，避免旧请求消耗新轮次次数或通过重发恢复猜码预算。

## Requirements

### Requirement: 必需的轮次凭证
系统 SHALL 为每次成功占额发码生成不可预测、至少 128 bit 熵的 code_id，采用 32 位小写十六进制编码。发码成功响应 SHALL 包含 code_id 和 RFC3339 expire_time；PHONE/EMAIL 的登录、绑定、重新认证和换绑凭证 SHALL 要求 code_id、target 和 code。用途 SHALL 由服务端操作确定。凭证响应 SHALL 禁止缓存，日志、审计和 URL SHALL 不包含完整 code_id。

#### Scenario: 发码并提交完整凭证
- **WHEN** 发送器接受登录验证码投递，客户端携带同轮 code_id、目标和验证码登录
- **THEN** 返回现有令牌协议结果，不依赖旧无轮次协议

#### Scenario: 缺失或格式错误
- **WHEN** PHONE/EMAIL 凭证缺少 code_id 或不符合标识格式
- **THEN** 返回 400 INVALID_ARGUMENT，不回退到仅按目标验证

### Requirement: 轮次绑定与最新轮次隔离
轮次 SHALL 绑定实例、渠道、归一化目标、用途和有效期；BIND SHALL 另绑定当前用户，REAUTH SHALL 另绑定当前用户和会话。同一渠道、用途、目标 SHALL 最多一轮有效。重新占额发码 SHALL 使旧轮次失效；合法格式的未知、过期、已消费、被替换或绑定不匹配轮次 SHALL 统一返回 400 CODE_EXPIRED，不扣当前有效轮次次数或目标累计失败预算，但仍受接口频率限制。

#### Scenario: 旧页面不消耗新轮次
- **WHEN** A 轮次已被 B 替换，旧页面提交 A 的正确或错误验证码
- **THEN** A 被拒绝，B 的剩余次数与目标累计失败预算均不改变

#### Scenario: 相同数字仍隔离
- **WHEN** 两轮恰好产生相同的六位验证码，旧 code_id 随该数字提交
- **THEN** 旧轮次仍被拒绝，不能消费新轮次

#### Scenario: 目标与主体绑定
- **WHEN** 使用另一目标、渠道、用途、用户或适用会话的轮次验证
- **THEN** 请求失败，不消费被错误引用轮次或正确目标的验证预算

### Requirement: 原子次数与一次性消费
轮次和绑定检查、次数限制、摘要比对、失败计数及消费 SHALL 原子执行。并发正确校验 SHALL 至多成功一次；重发与校验 SHALL 符合某个串行顺序。每轮最大次数 SHALL 保持可配置及默认 5 次；配置为 3 次时第三次正确 SHALL 成功，第三次错误 SHALL 立即作废。错误验证码 SHALL 返回 400 CODE_INVALID，不返回会话过期 401。

#### Scenario: 并发消费
- **WHEN** 多个请求同时提交同一有效轮次正确凭证
- **THEN** 恰好一个请求消费成功，其余按失效轮次拒绝

#### Scenario: 三次配置边界
- **WHEN** 最大次数为 3，已有两次错误后提交第三次凭证
- **THEN** 正确码成功，错误码作废轮次，之后该轮不可使用

#### Scenario: 重发与校验竞争
- **WHEN** 新轮次创建与旧轮次正确校验并发
- **THEN** 先被替换的旧轮次不可再成功，先完成的旧轮次消费不被事后撤销

### Requirement: 跨轮次目标失败预算
系统 SHALL 提供独立于发送额度和轮次次数的目标验证失败预算，默认同一渠道、归一化目标从首次错误起 15 分钟最多 10 次错误，跨 SIGN_IN/BIND/REAUTH 共享。重发、用途切换、正确验证码和后续错误 SHALL 不重置或延长窗口。仅有效当前轮次的错误码 SHALL 计入预算；达到上限后发码和有效轮次校验 SHALL 返回 429 RESOURCE_EXHAUSTED、reason TARGET_VERIFY_LIMIT 和窗口剩余秒数的 Retry-After。依赖故障 SHALL 返回 503，不降级为无计数验证。

#### Scenario: 重发不恢复预算
- **WHEN** 一个目标在多轮中累计 10 次错误，随后重发或换用途
- **THEN** 新请求被目标失败预算拒绝，不获得新的猜码预算

#### Scenario: 窗口到期
- **WHEN** 首次错误起满 15 分钟，期间存在重发或正确校验
- **THEN** 预算自然恢复；到期前 Retry-After 指向原窗口结束

#### Scenario: 失效轮次流量
- **WHEN** 未知或失效 code_id 被重复提交
- **THEN** 目标失败预算不改变，接口和 IP 限流仍可拒绝请求

### Requirement: 协议升级和存储隔离
新轮次协议 SHALL 适用于库和 accountsvc 的全部 PHONE/EMAIL 验证入口，不保留无标识凭证生产旁路。存储 SHALL 不新增目标或验证码明文；新协议内密钥轮换 SHALL 保留标识、原期限、次数及目标失败预算。旧无标识轮次 SHALL 不接管，升级 SHALL 同步调用者、停止旧写入和校验实例，旧短期数据按 TTL 淘汰，不清空整实例 Redis。

#### Scenario: 旧协议拒绝
- **WHEN** 升级后客户端只提交 target/code，或存储只有无 code_id 的旧记录
- **THEN** 不签发令牌，客户端需使用新协议重新发码，原身份与会话数据不被清理

#### Scenario: 依赖和实例隔离
- **WHEN** 存储依赖故障或另一实例轮次被提交
- **THEN** 故障拒绝验证，其他实例轮次不可消费，不降级为无状态校验
