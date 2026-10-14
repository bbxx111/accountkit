## MODIFIED Requirements

### Requirement: 必需的轮次凭证
系统 SHALL 为每次成功占额发码生成不可预测、至少 128 bit 熵的 code_id，采用 32 位小写十六进制编码。发码成功响应 SHALL 包含 code_id 和 RFC3339 expire_time；PHONE/EMAIL 的登录、绑定、重新认证、换绑及登录补全凭证 SHALL 要求 code_id、target 和 code。用途 SHALL 由服务端操作确定。凭证响应 SHALL 禁止缓存，日志、审计和 URL SHALL 不包含完整 code_id。

#### Scenario: 发码并提交完整凭证
- **WHEN** 发送器接受登录验证码投递，客户端携带同轮 code_id、目标和验证码登录
- **THEN** 按登录结果契约返回令牌或待完成票据，不依赖旧无轮次协议

#### Scenario: 缺失或格式错误
- **WHEN** PHONE/EMAIL 凭证缺少 code_id 或不符合标识格式
- **THEN** 返回 400 INVALID_ARGUMENT，不回退到仅按目标验证

### Requirement: 轮次绑定与最新轮次隔离
轮次 SHALL 绑定实例、渠道、归一化目标、用途和有效期；BIND SHALL 另绑定当前用户，REAUTH SHALL 另绑定当前用户和会话。同一渠道、用途、目标 SHALL 最多一轮有效。重新占额发码 SHALL 使旧轮次失效；合法格式的未知、过期、已消费、被替换或绑定不匹配轮次 SHALL 统一返回 400 CODE_EXPIRED，不扣当前有效轮次次数或目标累计失败预算，但仍受接口频率限制。

SIGN_IN_COMPLETE SHALL 仅由独立的 /accounts:sendSignInCompletionCode 签发，并由 /accounts:completeSignIn 消费；请求使用 sign_in_completion_token 定位流程。该用途 SHALL 绑定待完成流程标识及本次操作，不能伪造为某个 user_id，也不能与 SIGN_IN、BIND、REAUTH 互换。原轮次协议的字段、最新轮次和尝试计数行为 SHALL 保持。

#### Scenario: 旧页面不消耗新轮次
- **WHEN** A 轮次已被 B 替换，旧页面提交 A 的正确或错误验证码
- **THEN** A 被拒绝，B 的剩余次数与目标累计失败预算均不改变

#### Scenario: 相同数字仍隔离
- **WHEN** 两轮恰好产生相同的六位验证码，旧 code_id 随该数字提交
- **THEN** 旧轮次仍被拒绝，不能消费新轮次

#### Scenario: 目标与主体绑定
- **WHEN** 使用另一目标、渠道、用途、用户或适用会话的轮次验证
- **THEN** 请求失败，不消费被错误引用轮次或正确目标的验证预算

#### Scenario: 两个补全流程隔离
- **WHEN** 流程甲签发的 SIGN_IN_COMPLETE 码在流程乙被提交
- **THEN** 返回 CODE_EXPIRED，不消费甲的正确轮次，也不将验证码用于任意账号关联

### Requirement: 跨轮次目标失败预算
系统 SHALL 提供独立于发送额度和轮次次数的目标验证失败预算，默认同一渠道、归一化目标从首次错误起 15 分钟最多 10 次错误，跨 SIGN_IN/BIND/REAUTH/SIGN_IN_COMPLETE 共享。重发、用途切换、正确验证码和后续错误 SHALL 不重置或延长窗口。仅有效当前轮次的错误码 SHALL 计入预算；达到上限后发码和有效轮次校验 SHALL 返回 429 RESOURCE_EXHAUSTED、reason TARGET_VERIFY_LIMIT 和窗口剩余秒数的 Retry-After。依赖故障 SHALL 返回 503，不降级为无计数验证。

#### Scenario: 重发不恢复预算
- **WHEN** 一个目标在多轮中累计 10 次错误，随后重发或换用途
- **THEN** 新请求被目标失败预算拒绝，不获得新的猜码预算

#### Scenario: 窗口到期
- **WHEN** 首次错误起满 15 分钟，期间存在重发或正确校验
- **THEN** 预算自然恢复；到期前 Retry-After 指向原窗口结束

#### Scenario: 失效轮次流量
- **WHEN** 未知或失效 code_id 被重复提交
- **THEN** 目标失败预算不改变，接口和 IP 限流仍可拒绝请求

#### Scenario: 补全不能绕过失败预算
- **WHEN** 目标已经在普通登录或绑定中耗尽累计失败预算，随后切换到登录补全
- **THEN** 发码及有效轮次验证仍被 TARGET_VERIFY_LIMIT 拒绝，原窗口不重置
