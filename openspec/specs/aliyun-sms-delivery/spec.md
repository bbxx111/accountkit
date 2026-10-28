# aliyun-sms-delivery Specification

## Purpose
提供可选的阿里云短信验证码发送器，供 accountsvc 与嵌入式宿主使用。规定用途到模板的映射、号码范围、请求签名、错误分类、超时与隐私边界；首版只发中国大陆号码，配置结构为国际短信预留扩展位置。

## Requirements

### Requirement: 发送器配置与构造

可选包 SHALL 提供阿里云短信发送器构造入口，配置包括：

- AccessKey ID 与 AccessKey Secret；
- 一个签名名称；
- 四个用途（SIGN_IN、BIND、REAUTH、SIGN_IN_COMPLETE）各自的模板 code，允许多个用途使用同一 code；
- 可选的 endpoint 与超时。

构造 SHALL 校验全部必填项非空。endpoint SHALL 为 HTTPS 绝对 URL，只有回环地址允许 HTTP（仅供测试）。endpoint 不得带 userinfo、查询串或 fragment。构造时 SHALL 不进行网络 I/O。构造失败的错误 SHALL 只指出配置项，不回显 Secret 或其他配置值。发送器 SHALL 实现 `sender.SMSCodeSender`，并可安全地被并发调用。

#### Scenario: 缺少用途模板
- **WHEN** 配置缺少任一用途的模板 code，或 AccessKey、签名为空
- **THEN** 构造失败并指出缺失项，不发起任何请求

#### Scenario: 非法 endpoint
- **WHEN** endpoint 为非回环 HTTP、带 userinfo、查询串或 fragment
- **THEN** 构造失败，错误不包含 endpoint 中的凭据

### Requirement: 号码范围与格式

首版发送器 SHALL 只发送中国大陆手机号（E.164 前缀 `+86` 后为 1 开头的 11 位号码；同长度的固定电话等不支持），并通过可选的号码支持能力向调用方声明这一范围，使不支持的号码在占用额度与冷却之前即被拒绝。发送时 SHALL 去掉 `+86`，以 11 位号码作为 PhoneNumbers 参数。直接调用发送接口传入不支持的号码 SHALL 返回不支持号码错误，不发起请求。号码范围 SHALL 是发送器实现的属性，而非 HTTP 协议或身份规范的固定约束。

#### Scenario: 中国大陆号码
- **WHEN** 发送到 `+8613812345678`
- **THEN** 请求的 PhoneNumbers 为 `13812345678`

#### Scenario: 中国大陆固定电话
- **WHEN** 目标为 `+8675512345678`（11 位但非手机号）
- **THEN** 号码支持能力返回不支持，不占用额度

#### Scenario: 非中国大陆号码
- **WHEN** 目标为 `+14155550123`
- **THEN** 号码支持能力返回不支持；直接调用发送接口返回不支持号码错误，不发起请求

### Requirement: 请求签名与模板参数

每次发送 SHALL 向配置的 endpoint 发起一次 SendSms 请求，使用阿里云 API 规定的签名方式和随机 nonce，时间戳采用 UTC。参数 SHALL 包括签名名称、对应用途的模板 code，以及只含 `code` 键的模板参数 JSON（不含有效期等其他变量）。签名实现 SHALL 以阿里云官方文档中的签名示例作为固定测试向量。发送器 SHALL 不自动重试，不跟随重定向，并限制响应体大小。

#### Scenario: 用途选择模板
- **WHEN** 分别发送四种用途的验证码
- **THEN** 每个请求使用该用途配置的模板 code，模板参数为 `{"code":"<验证码>"}`

#### Scenario: 签名可复现
- **WHEN** 使用官方文档示例中的固定参数、nonce 与时间戳计算签名
- **THEN** 结果与文档给出的签名一致

### Requirement: 错误分类与隐私

只有 HTTP 200 且响应 `Code` 为 `OK` 时，发送 SHALL 视为成功。非 200 响应中的 JSON `Code` 与 `RequestId`（如签名或 AccessKey 错误）SHALL 同样被解析用于定位。发送器 SHALL 支持可选日志器，失败时以告警记录阶段、HTTP 状态、服务商错误码与 RequestId。以下情况 SHALL 返回投递不可用错误（`sender.ErrUnavailable`），由服务按既有发送失败契约返回 503，已占用的额度与冷却不退还，不自动重发：

- 其他响应码；
- 服务商频控（如 `isv.BUSINESS_LIMIT_CONTROL`）及其他业务错误；
- 网络错误、超时、非 JSON 或超限响应。

这些情况 SHALL 不映射为 429。调用方取消 SHALL 保持取消语义。错误与日志 SHALL 只包含服务商错误码、RequestId 与阶段信息，不包含完整手机号、验证码、AccessKey Secret、签名串或服务商返回的消息原文。

#### Scenario: 服务商频控
- **WHEN** 服务商返回 `isv.BUSINESS_LIMIT_CONTROL`
- **THEN** 发送返回投递不可用错误，HTTP 层为 503，不是 429；验证码轮次与额度保持已占用状态

#### Scenario: 日志不含敏感数据
- **WHEN** 发送成功或因任一原因失败
- **THEN** 发送器产生的错误与日志不包含完整手机号、验证码或 Secret
